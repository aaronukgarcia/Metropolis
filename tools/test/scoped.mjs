#!/usr/bin/env node
// Spec ref: agent test-verification contract (docs/planning/agent-test-verification-contract.md).
// Module key: reserved for tooling (root tooling exempt per CLAUDE.md GR#2); no code.json GUID.
//
// WHY THIS EXISTS (2026-09-02, Aaron ruling "such a waste ... improve the harness,
// better monitoring, better specification for the testing"):
//
// A build agent burned ~1h37m and ~309k tokens re-running the FULL webconsole
// suite (`npm test` = `node --test "test/*.test.mjs" && tsx --test <14 files>`).
// In this environment that command emits 60k+ lines with the default `spec`
// reporter, the harness kills it for flooding, and the agent RE-RUNS it — an
// unbounded kill-and-retry loop that adds zero verification value over the
// targeted suites plus the CI node-test job.
//
// This runner makes ANY test run SAFE and BOUNDED so that can never recur:
//   * a HARD per-invocation timeout (default 240s, --timeout=<sec> or
//     SCOPED_TIMEOUT_MS env) — a genuine hang (tests never finish) is killed
//     cleanly with a clear message naming the culprit instead of looping,
//   * the CONCISE `dot` reporter (one char per test, not the per-assertion
//     firehose) so output can't flood the harness,
//   * a one-line pass/fail tally per group + a final summary,
//   * a non-zero exit on ANY failure or timeout (so it still gates honestly).
//
// BUG-599 (2026-09-02, "scoped.mjs runner wedges on tsx dangling timers", bit
// 3 lanes today): a tsx group's tests could all genuinely PASS and still
// leave the child node process alive afterwards — React act warnings, an
// un-cleared setInterval, or any other dangling handle keeps node's event
// loop non-empty forever, and node does NOT exit on its own just because the
// test run's own reporting concluded. Investigated directly (fixtures +
// this file's own git history + the real webconsole/test/mount.test.tsx)
// two failure modes that had been conflated:
//   1. A group that never completes at all (an async test awaiting a promise
//      that never resolves) was ALREADY handled correctly by the pre-existing
//      per-group hard-timeout + `child.kill('SIGKILL')` on this Node version
//      (25.3.0 places spawned children in a Windows Job Object, so a plain
//      kill already tears down node's own per-file test-isolation
//      grandchild here — confirmed by inspecting the live process tree, a
//      real grandchild PID with `--test-isolation=process` on its own
//      command line, that a plain kill did in fact take down too). Kept, and
//      hardened to a Windows-safe recursive tree-kill (`taskkill /PID <pid>
//      /T /F`) anyway — a grandchild surviving an un-recursed kill is
//      exactly BUG-599's shape, and nothing here should depend on one Node
//      version's Job Object behaviour holding on every CI image.
//   2. THE ACTUAL REPRODUCED DEFECT: a group whose tests already PASSED (the
//      dot reporter shows a clean `.`) still burned the FULL per-group
//      timeout and got reported FAIL+TIMEOUT — a working test suite silently
//      misreported as broken, which is exactly what burns lanes chasing a
//      phantom regression. Root cause: node does not force an exit once a
//      test run's reporting concludes if the event loop still has handles
//      registered. Fix: `--test-force-exit` (stable since Node 18.15/20) on
//      every child invocation — node's own official answer to this exact
//      class of bug, and the only party that actually KNOWS when its own
//      test run concluded (this runner can't observe that from outside; see
//      the rejected alternatives below). Verified directly against both a
//      synthetic fixture (hung indefinitely without the flag, reported the
//      correct PASS in ~0.3s with it) and the real
//      webconsole/test/mount.test.tsx (24 tests, exits clean in ~7s with the
//      flag, every time).
//
// TWO ALTERNATIVES WERE TRIED AND REJECTED for making the leak itself
// visible (a "PASS + warning naming the file" on top of the force-exit fix)
// — both failed for concrete, reproduced reasons, not just theoretically:
//   * `process.getActiveResourcesInfo()` in a `--require`d exit hook: by the
//     time `--test-force-exit` decides to force the exit, it has already
//     torn down every OS-level handle it can see, so this reports nothing
//     even on a file that plainly left a `setInterval` running (checked
//     against a leaked Timeout AND a leaked raw net.Server — both swept).
//   * An output-quiescence backstop (kill+report-PASS after N seconds of no
//     new dot/X output): tried and then REMOVED after it produced a false
//     trigger against the real webconsole/test/mount.test.tsx — a legitimate
//     React/jsdom test can pause for several seconds between two dots, which
//     is indistinguishable from "finished, now just lingering" without
//     already knowing the file's total test count. Coercing an early kill to
//     PASS on that ambiguous signal risks reporting PASS on a suite that
//     hadn't actually finished — exactly the "Do NOT weaken correctness
//     reporting" line this fix is not allowed to cross. Removed rather than
//     shipped as a maybe-right heuristic.
// Net result: `--test-force-exit` alone is the fix, verified sufficient and
// safe against both synthetic and real files; no separate detection layer is
// needed on top of it, and the ones that were tried made things worse, not
// better.
//
// USAGE:
//   node tools/test/scoped.mjs <file...>          run named test files (the
//                                                 normal agent path: only the
//                                                 files your change touched)
//   node tools/test/scoped.mjs --webconsole-ci    run the exact webconsole set
//                                                 CI's node-test job covers,
//                                                 bounded + concise (the lead's
//                                                 /ci-green path — one shot, no
//                                                 flood, no retry loop)
//   flags: --timeout=<sec> (default 240, or SCOPED_TIMEOUT_MS env in ms)
//          --cwd=<dir> (default webconsole for --webconsole-ci, else CWD)
//          --reporter=<dot|tap|spec> (default dot)
//
// It dispatches `.mjs`/`.js` to `node --test` and `.tsx`/`.ts` to `tsx --test`,
// grouping mixed inputs into at most two child processes. Extension drives the
// runner so an agent never has to remember which loader a file needs.
//
// SLOW_TEST_CAPS_SEC (BUG-599): a small allowlist of test files that
// legitimately need longer than the default cap, so a batch containing one
// fails on correctness, never on time. webconsole/test/chunked-replay.test.mjs
// measured ~350s at HEAD on 2026-09-02 (timed directly: a 300s bound killed it
// mid-run, a 580s bound let it complete clean) — it is not a hang, the 240s
// default is just too tight for what it legitimately does. Any group
// containing an allowlisted file gets the LARGEST matching cap instead of the
// default. Keep this list tiny and update it in the SAME commit as any test
// that grows past the default cap for a legitimate reason.

import { spawn, execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve, extname, basename } from 'node:path';

const DEFAULT_TIMEOUT_SEC = process.env.SCOPED_TIMEOUT_MS
  ? Math.max(1, Math.ceil(Number(process.env.SCOPED_TIMEOUT_MS) / 1000) || 240)
  : 240;

// file basename -> cap in seconds. See SLOW_TEST_CAPS_SEC note above.
const SLOW_TEST_CAPS_SEC = new Map([
  ['chunked-replay.test.mjs', 600],
]);

// The exact webconsole test set CI's node-test coverage depends on, kept in
// lockstep with webconsole/package.json's "test" script. If that script gains
// or drops a file, update this list in the SAME commit (a drift check lives in
// scoped.test.mjs so the two can't silently diverge).
const WEBCONSOLE_NODE_GLOB = ['test/*.test.mjs'];
const WEBCONSOLE_TSX_FILES = [
  'test/mount.test.tsx',
  'test/rebuild-prompt.test.tsx',
  'test/rebuild-regression-bug468.test.tsx',
  'test/store-dispatch.test.tsx',
  'test/error-boundary.test.tsx',
  'test/store-reset-capture.test.tsx',
  'test/population-sankey.test.tsx',
  'test/arrivals-by-mode-sankey.test.tsx',
  'test/bug501-second-bailout-banner.test.tsx',
  'test/bug500-advisor-click-overlap.test.tsx',
  'test/bug498-forced-sales-dismiss.test.tsx',
  'test/bug499-queue-depth-overlap.test.tsx',
  'test/bug512-bug513-save-error-robustness.test.tsx',
  'test/keybindings.test.tsx',
];

function parseArgs(argv) {
  const opts = { files: [], timeoutSec: DEFAULT_TIMEOUT_SEC, cwd: null, reporter: 'dot', webconsoleCi: false };
  for (const a of argv) {
    if (a === '--webconsole-ci') opts.webconsoleCi = true;
    else if (a.startsWith('--timeout=')) opts.timeoutSec = Math.max(1, Number(a.slice('--timeout='.length)) || DEFAULT_TIMEOUT_SEC);
    else if (a.startsWith('--cwd=')) opts.cwd = a.slice('--cwd='.length);
    else if (a.startsWith('--reporter=')) opts.reporter = a.slice('--reporter='.length) || 'dot';
    else if (a.startsWith('--')) { /* ignore unknown flags, fail-open */ }
    else opts.files.push(a);
  }
  return opts;
}

// The effective per-group cap: the larger of the caller's requested timeout
// and any allowlisted slow-test cap matched among this group's targets.
function effectiveTimeoutSec(targets, requestedSec) {
  let cap = requestedSec;
  for (const t of targets) {
    const slow = SLOW_TEST_CAPS_SEC.get(basename(t.replace(/\\/g, '/')));
    if (slow && slow > cap) cap = slow;
  }
  return cap;
}

// Windows-safe process-tree kill (BUG-599 hardening). A plain
// `child.kill('SIGKILL')` only ever signals the immediate PID; that has been
// enough on this Node version (children land in a Windows Job Object that
// tears down with the parent — verified directly against a real grandchild
// PID), but a grandchild surviving an un-recursed kill is exactly BUG-599's
// shape, so this does not rely on that being true everywhere. `taskkill /T
// /F` recurses the whole tree explicitly and falls back to a direct kill if
// taskkill itself can't run or the process is already gone (taskkill exits
// non-zero for "not found" — harmless here).
function killTree(child) {
  if (process.platform === 'win32' && child.pid) {
    try {
      execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
      return;
    } catch {
      // fall through to the plain kill below
    }
  }
  try { child.kill('SIGKILL'); } catch { /* already gone */ }
}

// Run one child (node or tsx) over a set of test targets with a hard timeout
// and the concise reporter, plus BUG-599's `--test-force-exit` fix so a
// group that already passed never wedges the caller waiting on a dangling
// handle. Resolves to { ok, timedOut, code }.
function runGroup(runner, targets, cwd, timeoutSec, reporter) {
  return new Promise((resolveGroup) => {
    const isNode = runner === 'node';
    // Always spawn node directly (process.execPath). For .tsx/.ts targets, load
    // tsx as a module hook (`node --import tsx`) instead of spawning `npx tsx` —
    // on Windows `spawn('npx.cmd', …, {shell:false})` throws EINVAL, which broke
    // every tsx run. Going through node also lets tsx targets use the same
    // concise --test-reporter.
    const testArgs = [
      '--test',
      '--test-force-exit', // BUG-599 fix — see header comment
      `--test-reporter=${reporter}`,
      ...targets,
    ];
    const spawnArgs = isNode ? testArgs : ['--import', 'tsx', ...testArgs];
    const bin = process.execPath;
    const label = `${runner} --test (${targets.length} target${targets.length === 1 ? '' : 's'})`;
    process.stdout.write(`\n▶ ${label}  [timeout ${timeoutSec}s, reporter ${reporter}]\n`);

    const child = spawn(bin, spawnArgs, { cwd, stdio: ['ignore', 'inherit', 'inherit'], shell: false });
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      process.stderr.write(
        `\n⏱  TIMEOUT after ${timeoutSec}s — killing ${label} (${targets.join(', ')}). ` +
        `TIMEOUT (likely dangling timers/handles that never let the tests conclude at ` +
        `all — see BUG-599). This is a hang or an oversized set; scope it down to the ` +
        `files your change touched.\n`);
      killTree(child);
    }, timeoutSec * 1000);

    child.on('error', (err) => {
      clearTimeout(timer);
      process.stderr.write(`\n✖ failed to launch ${label}: ${err.message}\n`);
      resolveGroup({ ok: false, timedOut: false, code: 127 });
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      resolveGroup({ ok: !timedOut && code === 0, timedOut, code: code ?? 1 });
    });
  });
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));

  let nodeTargets = [];
  let tsxTargets = [];
  let cwd = opts.cwd ? resolve(opts.cwd) : process.cwd();

  if (opts.webconsoleCi) {
    if (!opts.cwd) cwd = resolve('webconsole');
    nodeTargets = [...WEBCONSOLE_NODE_GLOB];
    tsxTargets = [...WEBCONSOLE_TSX_FILES];
  } else {
    if (opts.files.length === 0) {
      process.stderr.write(
        'scoped.mjs: no test files given.\n' +
        'Run the files your change touched, e.g.:\n' +
        '  node tools/test/scoped.mjs webconsole/test/consistency.test.mjs webconsole/test/debugjson.test.mjs\n' +
        'or the bounded full webconsole CI set:\n' +
        '  node tools/test/scoped.mjs --webconsole-ci\n');
      process.exit(2);
    }
    for (const f of opts.files) {
      const ext = extname(f).toLowerCase();
      if (ext === '.tsx' || ext === '.ts') tsxTargets.push(f);
      else nodeTargets.push(f);
    }
    // Sanity: warn (don't fail) on a named file that doesn't exist relative to cwd.
    for (const f of [...nodeTargets, ...tsxTargets]) {
      if (!f.includes('*') && !existsSync(resolve(cwd, f)) && !existsSync(f)) {
        process.stderr.write(`⚠  named target not found (will let the runner report it): ${f}\n`);
      }
    }
  }

  const results = [];
  if (nodeTargets.length) results.push(await runGroup('node', nodeTargets, cwd, effectiveTimeoutSec(nodeTargets, opts.timeoutSec), opts.reporter));
  if (tsxTargets.length) results.push(await runGroup('tsx', tsxTargets, cwd, effectiveTimeoutSec(tsxTargets, opts.timeoutSec), opts.reporter));

  const failed = results.filter((r) => !r.ok);
  const timedOut = results.some((r) => r.timedOut);
  process.stdout.write('\n──────── scoped test summary ────────\n');
  process.stdout.write(`groups: ${results.length}   passed: ${results.length - failed.length}   failed: ${failed.length}${timedOut ? '   (incl. TIMEOUT)' : ''}\n`);
  if (failed.length) {
    process.stdout.write('RESULT: FAIL — scope down and re-run the failing group, or read the dot/failure output above.\n');
    process.exit(1);
  }
  process.stdout.write('RESULT: PASS\n');
  process.exit(0);
}

// This file lives under a `test/` directory, so CI's repo-root `node --test`
// auto-discovers it and runs it AS a test. It is a CLI tool, not a test suite —
// run with no args it would exit non-zero and report as a failed test
// (`not ok - tools/test/scoped.mjs`, the BUG-543 CI red). `node --test` sets
// NODE_TEST_CONTEXT for every discovered file it executes; detect that and
// no-op out as a passing empty test file. Direct CLI invocation (env unset)
// runs normally.
if (process.env.NODE_TEST_CONTEXT) {
  process.exit(0);
}

main().catch((err) => {
  process.stderr.write(`scoped.mjs crashed: ${err && err.stack ? err.stack : err}\n`);
  process.exit(1);
});
