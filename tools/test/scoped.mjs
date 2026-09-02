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
//   * a HARD per-invocation timeout (default 240s, --timeout=<sec>) — a hang
//     is killed cleanly with a clear message instead of looping,
//   * the CONCISE `dot` reporter (one char per test, not the per-assertion
//     firehose) so output can't flood the harness,
//   * a one-line pass/fail tally per group + a final summary,
//   * a non-zero exit on ANY failure or timeout (so it still gates honestly).
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
//   flags: --timeout=<sec> (default 240)  --cwd=<dir> (default webconsole for
//          --webconsole-ci, else CWD)     --reporter=<dot|tap|spec> (default dot)
//
// It dispatches `.mjs`/`.js` to `node --test` and `.tsx`/`.ts` to `tsx --test`,
// grouping mixed inputs into at most two child processes. Extension drives the
// runner so an agent never has to remember which loader a file needs.

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve, extname } from 'node:path';

const DEFAULT_TIMEOUT_SEC = 240;

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

// Run one child (node or tsx) over a set of test targets with a hard timeout
// and the concise reporter. Resolves to { ok, timedOut, code }.
function runGroup(runner, targets, cwd, timeoutSec, reporter) {
  return new Promise((resolveGroup) => {
    const isNode = runner === 'node';
    // tsx doesn't accept --test-reporter before v4; pass it only to node, and
    // fall back to node's dot reporter env for tsx (NODE_TEST_CONTEXT unset).
    const args = isNode
      ? ['--test', `--test-reporter=${reporter}`, ...targets]
      : ['--test', ...targets];
    const bin = isNode ? process.execPath : (process.platform === 'win32' ? 'npx.cmd' : 'npx');
    const spawnArgs = isNode ? args : ['tsx', ...args];
    const label = `${runner} --test (${targets.length} target${targets.length === 1 ? '' : 's'})`;
    process.stdout.write(`\n▶ ${label}  [timeout ${timeoutSec}s, reporter ${isNode ? reporter : 'default'}]\n`);

    const child = spawn(bin, spawnArgs, { cwd, stdio: ['ignore', 'inherit', 'inherit'], shell: false });
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      process.stderr.write(`\n⏱  TIMEOUT after ${timeoutSec}s — killing ${label}. This is a hang or an oversized set; scope it down to the files your change touched.\n`);
      child.kill('SIGKILL');
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
  if (nodeTargets.length) results.push(await runGroup('node', nodeTargets, cwd, opts.timeoutSec, opts.reporter));
  if (tsxTargets.length) results.push(await runGroup('tsx', tsxTargets, cwd, opts.timeoutSec, opts.reporter));

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

main().catch((err) => {
  process.stderr.write(`scoped.mjs crashed: ${err && err.stack ? err.stack : err}\n`);
  process.exit(1);
});
