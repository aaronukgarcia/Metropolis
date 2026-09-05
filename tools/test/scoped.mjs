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
import { existsSync, readdirSync, statSync } from 'node:fs';
import { resolve, relative, sep, extname, basename, isAbsolute, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const DEFAULT_TIMEOUT_SEC = process.env.SCOPED_TIMEOUT_MS
  ? Math.max(1, Math.ceil(Number(process.env.SCOPED_TIMEOUT_MS) / 1000) || 240)
  : 240;

// AARON WATCHDOG RULING (2026-09-04, verbatim: "no test group may run longer
// than 30 minutes"): 1800 seconds is the ABSOLUTE ceiling for any single
// invocation of this runner — it clamps the default, every
// SLOW_TEST_CAPS_SEC entry, --timeout flags and the SCOPED_TIMEOUT_MS env
// alike. Agent lanes have wait-looped for over an hour on hung runs; a test
// that needs more than 30 minutes is either hung or misdesigned, and either
// way it must be killed and reported FAILING rather than left running or
// silently passed. There is deliberately no override: split the suite or fix
// the test instead. (Supersedes an earlier, narrower 20-minute figure that
// was never actually ruled — this is the one Aaron gave.)
const WATCHDOG_ABSOLUTE_CEILING_SEC = 1800;
const clampToWatchdog = (sec) => Math.min(sec, WATCHDOG_ABSOLUTE_CEILING_SEC);

// file basename -> cap in seconds. See SLOW_TEST_CAPS_SEC note above.
const SLOW_TEST_CAPS_SEC = new Map([
  ['chunked-replay.test.mjs', 600],
  // BUG-617 r2 stability finding (2026-09-03): these three tsx tests each
  // drive a real requestAnimationFrame-chained chunked replay (2,400 actions
  // in some cases) inside jsdom/React, and widened their own internal
  // waitFor bounds to 90s/60s (from 20-30s/10s) after measuring occasional
  // timeouts when this file runs alongside its bug617/attack siblings in one
  // contended node:test process (multiple heavy chunked-replay tests starve
  // each other's rAF cadence). 300s covers the widest internal wait plus
  // this file's other tests, with margin.
  ['bug617-boot-wiring.test.tsx', 300],
  ['attack-bug617-crossbuild.test.tsx', 300],
  ['attack-bug617-lifecycle.test.tsx', 300],
  // BUG-646 (2026-09-03): the Fix-All cap rose 250 -> 2000 (Aaron's ruling),
  // so these two suites' capped-batch scenarios legitimately do ~8x the
  // placement work they were written for (a >2000-unit resolveDemandAll each,
  // through the real reducer). Measured ~35s alone but past the 240s default
  // when batched with siblings in one contended node:test process. Same
  // shape as the bug617 entries above: not a hang, just honest work.
  ['attack-bug606-cap.test.tsx', 600],
  ['bug606-round2-fixes.test.tsx', 600],
]);

// BUG-651: these files each mutate a shared src/sim/*.ts file IN PLACE as
// part of their GR#24 scratch-copy RED-proof discipline (cp file -> file.bak,
// mutate file, run a fresh child against it, restore file from file.bak) —
// live-repro'd racing under node:test's default per-file concurrency: two
// such tests running in the SAME node --test process can interleave so that
// test B's `cp` captures test A's MID-MUTATION content as ITS "clean"
// backup, and test B's later restore then permanently corrupts the shared
// source file on disk with no assertion catching it (reproduced against
// src/sim/data.ts, corrupted between attack-bug642-memo.test.mjs and
// attack-bug643-memo.test.mjs both racing on the same DATA_TS_PATH). Force
// these to run SERIALLY (one file at a time within the node --test process)
// whenever 2+ of them land in the same invocation — correctness over speed;
// this list only ever costs time when two are batched together.
// MAINTENANCE INVARIANT (F1, round BUG-696, same precedent as
// SLOW_TEST_CAPS_SEC above): this list is hand-maintained, and a
// hand-maintained allowlist WILL fall behind — it already did once
// (feat-dynamic-bailout.test.mjs sabotages src/sim/engine.ts exactly like
// bug-509-tiered-population-ceiling.test.mjs below and was missing). It is
// NOT trusted on its own: tools/scoped-runner-attack.mjs's
// "serial: every src-mutating test is on FILE_MUTATING_TEST_BASENAMES [F1]"
// test derives the expected set straight from the test files themselves
// (greps for the cp/writeFileSync-mutate/restore shape against every
// webconsole/test/*.test.mjs) and REDS if this list has drifted. Treat that
// test as the actual source of truth; update this list in the SAME commit
// whenever it reds, exactly like SLOW_TEST_CAPS_SEC's own rule.
const FILE_MUTATING_TEST_BASENAMES = new Set([
  'attack-bug642-memo.test.mjs', // mutates src/sim/data.ts
  'attack-bug643-memo.test.mjs', // mutates src/sim/data.ts
  'bug645-population-visibility.test.mjs', // mutates src/sim/data.ts
  'bug662-college-capacity-tier.test.mjs', // mutates src/sim/data.ts
  'bug-509-tiered-population-ceiling.test.mjs', // mutates src/sim/engine.ts
  'feat-dynamic-bailout.test.mjs', // mutates src/sim/engine.ts (F1 finding — was missing)
  // BUG-708 re-round B4/B5 finding: tsx tests mutate shared sources too.
  // attack-bug641-round2.test.tsx really does writeFileSync src/components/
  // demandFixUi.ts in place. Listed here so needsSerialExecution/
  // partitionMutatingPaired (below) also serialise the tsx group it lands in.
  'attack-bug641-round2.test.tsx', // mutates src/components/demandFixUi.ts
]);

// Basename -> the real source file basename it mutates in place. Used only
// for the D4 post-group re-check's "which test in that group likely owns
// the strand" reporting (BUG-708) — not load-bearing for the serialisation
// decision itself (FILE_MUTATING_TEST_BASENAMES is).
const FILE_MUTATING_TEST_TARGET_BASENAME = new Map([
  ['attack-bug642-memo.test.mjs', 'data.ts'],
  ['attack-bug643-memo.test.mjs', 'data.ts'],
  ['bug645-population-visibility.test.mjs', 'data.ts'],
  ['bug662-college-capacity-tier.test.mjs', 'data.ts'],
  ['bug-509-tiered-population-ceiling.test.mjs', 'engine.ts'],
  ['feat-dynamic-bailout.test.mjs', 'engine.ts'],
  ['attack-bug641-round2.test.tsx', 'demandFixUi.ts'],
]);

// BUG-708: a killed/timed-out mutating test (the watchdog above SIGKILLs at
// the ceiling BY DESIGN) can strand its GR#24 scratch-copy backup — the
// mutate-then-restore cycle is `cp file file.<suffix>.bak; mutate file; run;
// restore file from the .bak` (see FILE_MUTATING_TEST_BASENAMES above), and a
// SIGKILL mid-cycle skips the restore, leaving the SABOTAGED source file live
// on disk plus an orphaned `*.bak` next to it. The BUG-477 round burned time
// on exactly this shape: three phantom perf failures traced back to a stale
// data.ts.bak nobody noticed. Scanning webconsole/src/ RECURSIVELY (D3 finding
// — a bare readdirSync of src/sim only missed src/components, where
// attack-bug641-round2.test.tsx strands demandFixUi.ts.*.bak) for any
// leftover `*.bak` before spawning anything, and again between groups (D4,
// see failOnStaleBakFiles below), turns that silent poison into a loud,
// named, actionable failure instead of a mystery regression two commits
// later. See findStaleBakFiles()/failOnStaleBakFiles() below.
const WEBCONSOLE_SRC_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'webconsole', 'src');

// Recursive, but still cheap: only the file NAME is inspected (a single
// `.endsWith('.bak')` string compare per directory entry), no file content is
// ever read here. webconsole/src is a small tree, so a full walk costs
// milliseconds even on every group boundary (D4).
function findStaleBakFiles(root = WEBCONSOLE_SRC_DIR) {
  if (!existsSync(root)) return [];
  const out = [];
  const walk = (dir) => {
    let entries;
    try { entries = readdirSync(dir); } catch { return; }
    for (const name of entries) {
      const full = join(dir, name);
      let st;
      try { st = statSync(full); } catch { continue; }
      if (st.isDirectory()) walk(full);
      else if (st.isFile() && name.endsWith('.bak')) out.push(full);
    }
  };
  walk(root);
  return out;
}

// D2 fix: a stale backup's real file can NEVER be derived by blindly
// stripping dot-segments — `data.ts.bak` (the suffix-LESS shape the BUG-477
// postmortem and a dozen test headers actually use: `cp data.ts data.ts.bak`)
// would have its LAST dot segment stripped unconditionally by the old code,
// resolving to "data" and printing `mv "data.ts.bak" "data"` — a command that
// destroys the only good copy (renames it to a file nothing imports) while
// leaving the sabotaged data.ts live on disk untouched. The printed command
// must NEVER be wrong, so this checks WHICH candidate actually exists on
// disk instead of guessing from the name shape alone:
//   1. strip only the trailing `.bak` -> if THAT path exists, it's the
//      suffix-less shape (`data.ts.bak` -> `data.ts`): use it.
//   2. else also strip the next `.<tag>` segment -> if THAT path exists, it's
//      the tagged shape (`data.ts.bug645.bak` -> `data.ts`): use it.
//   3. if neither exists (or, pathologically, both do — ambiguous), return no
//      certain answer: name the .bak and instruct manual inspection instead
//      of printing a command that might be wrong.
function candidateRealFiles(bakPath) {
  const withoutBak = bakPath.replace(/\.bak$/, '');
  const candidates = [withoutBak];
  const lastDot = withoutBak.lastIndexOf('.');
  if (lastDot !== -1) candidates.push(withoutBak.slice(0, lastDot));
  return candidates;
}

function resolveRealFile(bakPath) {
  const candidates = candidateRealFiles(bakPath);
  const existing = candidates.filter((c) => existsSync(c));
  if (existing.length === 1) return { file: existing[0], certain: true, candidates };
  return { file: null, certain: false, candidates };
}

// D4: set immediately before a post-group re-check calls failOnStaleBakFiles()
// (and consumed/cleared inside it) so the SAME zero-arg call site can report
// which group was just killed/timed out without changing its signature —
// every invocation of failOnStaleBakFiles() (pre-run AND post-group) is the
// literal statement `failOnStaleBakFiles();`, which is itself asserted by the
// re-round attack's D4 test (a hand-verifiable proof this re-check actually
// exists, not just a same-named helper called differently).
let staleBakRecheckContext = null;

// Fail-closed and LOUD (BUG-708) — never auto-restore (the .bak may be the
// only good copy left) and never silently proceed (the sibling real file may
// currently hold sabotaged content from a killed test, which produces
// phantom failures indistinguishable from a real regression).
function failOnStaleBakFiles() {
  const context = staleBakRecheckContext;
  staleBakRecheckContext = null;
  const stale = findStaleBakFiles();
  if (!stale.length) return;
  const header = context
    ? `\n✖ STALE BACKUP FILE(S) DETECTED AFTER A KILLED/TIMED-OUT GROUP (BUG-708) — the ` +
      `group "${context.groupLabel}" was just SIGKILLed at the timeout ceiling; a mutating ` +
      `test inside it (see FILE_MUTATING_TEST_BASENAMES) most likely owns the strand below and ` +
      `left the source file sabotaged before any LATER group in this same invocation can run ` +
      `against it.\n`
    : '\n✖ STALE BACKUP FILE(S) DETECTED (BUG-708) — a prior mutating test run was ' +
      'likely killed (watchdog SIGKILL at the timeout ceiling, or a crash) before its ' +
      'GR#24 scratch-copy restore could run. The source file(s) below may currently ' +
      'hold SABOTAGED/mutated content, not the real source — running anything against ' +
      'them will produce phantom failures (this is exactly what cost the BUG-477 round ' +
      'time: three phantom perf failures traced to one stale data.ts.bak).\n';
  process.stderr.write(header);
  for (const bak of stale) {
    process.stderr.write(`    ${bak}\n`);
    const resolved = resolveRealFile(bak);
    if (resolved.certain) {
      process.stderr.write(`      likely real file: ${resolved.file}\n`);
      process.stderr.write(`      restore with:     mv "${bak}" "${resolved.file}"\n`);
      const owningTests = [...FILE_MUTATING_TEST_TARGET_BASENAME]
        .filter(([, target]) => target === basename(resolved.file))
        .map(([test]) => test);
      if (owningTests.length) {
        process.stderr.write(`      likely owning test(s): ${owningTests.join(', ')}\n`);
      }
    } else {
      process.stderr.write(
        `      AMBIGUOUS/NO real file could be determined with certainty ` +
        `(checked: ${resolved.candidates.join(', ')}) — refusing to print a restore ` +
        `command that might be wrong. Inspect manually: compare the .bak's content ` +
        `against every candidate above and decide by hand.\n`);
    }
  }
  process.stderr.write(
    '\nDo NOT delete the .bak — it may be the only good copy of the real source. ' +
    'Where a restore command was printed above, verify the restored file looks right ' +
    'BEFORE trusting it, then re-run scoped.mjs. Where none was printed, resolve the ' +
    'ambiguity by hand first.\n');
  process.exit(1);
}

// BUG-651/F2: run mutating files (FILE_MUTATING_TEST_BASENAMES) in their own
// concurrency-capped-to-one serial group so they can never race on the
// shared src/sim/*.ts file they each cp/mutate/restore; everything else
// keeps node's normal per-file concurrency. Kept as a named predicate
// (rather than inlined) because tools/scoped-runner-attack.mjs
// source-checks for its existence by name. (Deliberately NOT quoting the
// actual injected CLI flag in this comment — see runGroup below for why.)
function needsSerialExecution(targets) {
  return targets.length > 0 && targets.every((t) => FILE_MUTATING_TEST_BASENAMES.has(basename(t.replace(/\\/g, '/'))));
}

// Split a (possibly glob-expanded) list of node targets into a serial
// mutating subgroup and a parallel everything-else subgroup (F2 partition).
function partitionMutating(targets) {
  const serial = [];
  const parallel = [];
  for (const t of targets) {
    if (FILE_MUTATING_TEST_BASENAMES.has(basename(t.replace(/\\/g, '/')))) serial.push(t);
    else parallel.push(t);
  }
  return { serial, parallel };
}

// B5 fix: the tsx path needs the SAME serial/parallel split as node targets
// (attack-bug641-round2.test.tsx mutates src/components/demandFixUi.ts in
// place exactly like the mjs entries mutate src/sim/*.ts), but a tsx group
// carries TWO parallel arrays after bucketizeTsxTargets' cwd rewrite — the
// spawn-ready `targets` (rewritten relative to cwd=webconsole) and the
// caller-typed `displayTargets` used for reporting/cap lookup. Partition by
// the DISPLAY basename (the caller-typed name is what
// FILE_MUTATING_TEST_BASENAMES is keyed on) while keeping both arrays in
// lockstep by index.
function partitionMutatingPaired(targets, displayTargets) {
  const serialTargets = [], serialDisplay = [], parallelTargets = [], parallelDisplay = [];
  for (let i = 0; i < targets.length; i++) {
    const bn = basename(displayTargets[i].replace(/\\/g, '/'));
    if (FILE_MUTATING_TEST_BASENAMES.has(bn)) {
      serialTargets.push(targets[i]);
      serialDisplay.push(displayTargets[i]);
    } else {
      parallelTargets.push(targets[i]);
      parallelDisplay.push(displayTargets[i]);
    }
  }
  return { serialTargets, serialDisplay, parallelTargets, parallelDisplay };
}

// Minimal single-wildcard glob support (F2/F3): the only shape this repo's
// globs ever take is `<dir>/*.<ext...>` (WEBCONSOLE_NODE_GLOB). Expanding it
// ourselves — rather than handing the literal glob string to `node --test`
// and letting IT resolve which files matched — is what makes partitioning by
// basename (F2) and a loud zero-match failure (F3) possible at all; node's
// own glob resolution is opaque to this script.
function expandGlob(pattern, cwd) {
  const norm = pattern.replace(/\\/g, '/');
  const idx = norm.lastIndexOf('/');
  const dirPart = idx === -1 ? '.' : norm.slice(0, idx);
  const filePart = idx === -1 ? norm : norm.slice(idx + 1);
  const absDir = resolve(cwd, dirPart);
  if (!existsSync(absDir)) return [];
  const re = new RegExp('^' + filePart.split('*').map((s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$');
  return readdirSync(absDir)
    .filter((f) => re.test(f))
    .sort()
    .map((f) => (dirPart === '.' ? f : `${dirPart}/${f}`));
}

// Expand every glob-bearing entry in `targets`; a glob that matches ZERO
// files is a hard, loud, non-zero-exit failure (F3) — "a gate that cannot
// evaluate must not report success" (Vestige: metropolis-verification-standards).
// Returns { files } on success or { error } naming the offending pattern.
function expandAndValidate(targets, cwd, label) {
  const out = [];
  for (const t of targets) {
    if (!t.includes('*')) { out.push(t); continue; }
    const matches = expandGlob(t, cwd);
    if (matches.length === 0) {
      return { error: `${label} glob '${t}' matched ZERO files under cwd '${cwd}' — refusing to report success for a gate that verified nothing (F3).` };
    }
    out.push(...matches);
  }
  return { files: out };
}

// The exact webconsole test set CI's node-test coverage depends on, kept in
// lockstep with webconsole/package.json's "test" script. If that script gains
// or drops a file, update this list in the SAME commit (a drift check lives
// in tools/scoped-runner-attack.mjs — the F5 test "drift: WEBCONSOLE_TSX_FILES
// matches the webconsole package script" — run with
// `node --test tools/scoped-runner-attack.mjs` so the two can't silently
// diverge; an earlier version of this comment pointed at a scoped.test.mjs
// that was never actually written, which is why the drift went unchecked).
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
  'test/stale-build-guard.test.tsx',
  'test/attack-bug564-hmr-liveness.test.tsx',
  'test/attack-bug691-indicator-render.test.tsx',
  'test/bug-397-rework-financetab.test.tsx',
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
  // Aaron's 20-minute watchdog clamps EVERYTHING — see
  // WATCHDOG_ABSOLUTE_CEILING_SEC's own comment. Deliberately last, so no
  // flag, env or allowlist entry can exceed it.
  return clampToWatchdog(cap);
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
function runGroup(runner, targets, cwd, timeoutSec, reporter, displayTargets = targets) {
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
    // BUG-651: force one-file-at-a-time execution when this group is (post
    // F2 partition) entirely file-mutating RED-proofs that would otherwise
    // race on the same shared src/sim/*.ts file — see
    // FILE_MUTATING_TEST_BASENAMES above.
    const serial = needsSerialExecution(displayTargets);
    if (serial) {
      // The literal CLI flag is injected ONLY here — deliberately not quoted
      // verbatim in any comment or the print line below, so that
      // tools/scoped-runner-attack.mjs's source-string check for this exact
      // literal can only pass because THIS line still exists, not because
      // the text merely appears somewhere else in the file.
      testArgs.splice(1, 0, '--test-concurrency=1');
    }
    const spawnArgs = isNode ? testArgs : ['--import', 'tsx', ...testArgs];
    const bin = process.execPath;
    const label = `${runner} --test (${targets.length} target${targets.length === 1 ? '' : 's'})`;
    process.stdout.write(`\n▶ ${label}  [timeout ${timeoutSec}s, reporter ${reporter}, cwd ${cwd}]\n`);
    // F7: print the serialisation decision + reason so it's testable/auditable
    // from the runner's own output, not just inferred from source.
    process.stdout.write(
      serial
        ? `  serialisation: SERIAL (concurrency capped to 1) — every target is on FILE_MUTATING_TEST_BASENAMES (BUG-651 shared-source-mutation race)\n`
        : `  serialisation: parallel (node's default per-file concurrency; no target on FILE_MUTATING_TEST_BASENAMES)\n`);

    // F6 (continued): NODE_TEST_CONTEXT is a real Node internal env var that
    // makes a `node --test` invocation treat itself as a recursive re-entry
    // into an already-running test run and SILENTLY SKIP EVERYTHING with
    // exit 0 ("run() is being called recursively within a test file"),
    // regardless of whether THIS process's own guard above decided to
    // proceed. If scoped.mjs itself is invoked from inside any node:test
    // process (this file's own attack suite included), that var is present
    // in process.env and would otherwise leak straight through to the real
    // `node --test <targets>` child spawned here, vacuously passing it.
    // Strip it from the child's env unconditionally — the actual test run
    // this spawns is never itself a recursive re-entry.
    const { NODE_TEST_CONTEXT: _dropNodeTestContext, ...childEnv } = process.env;
    const child = spawn(bin, spawnArgs, { cwd, env: childEnv, stdio: ['ignore', 'inherit', 'inherit'], shell: false });
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      process.stderr.write(
        `\n⏱  TIMEOUT after ${timeoutSec}s — killing ${label} (${displayTargets.join(', ')}). ` +
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

// NEW (found 2026-09-04, live repro): a tsx group spawned with the repo-root
// cwd never picks up webconsole/tsconfig.json's automatic JSX runtime, so
// EVERY .tsx test fails with "ReferenceError: React is not defined" at any
// JSX render — `node tools/test/scoped.mjs webconsole/test/<file>.test.tsx`
// from the repo root failed while `npx tsx --test test/<file>.test.tsx` from
// inside webconsole/ passed on the identical file. When the caller didn't
// pass an explicit --cwd, and every tsx target resolves under webconsole/,
// run that group with cwd=webconsole and rewrite the targets to be relative
// to it (what the child process needs); the ORIGINAL argument strings are
// kept for all user-facing reporting (label/timeout/failure messages) via
// runGroup's displayTargets so a failure is still reported by the path the
// caller typed.
// F4 fix: `relative()` between two paths on DIFFERENT WINDOWS DRIVES returns
// the target's own absolute path unchanged (not a leading `..` or a bare
// separator), so a bare `rel.startsWith('..')` check silently treats a
// cross-drive target as "under webconsole/" — reject `isAbsolute(rel)` too.
function isUnderWebconsole(rel) {
  return rel !== '' && !rel.startsWith('..') && !rel.startsWith(sep) && !isAbsolute(rel);
}

// F5 fix: a mixed-root tsx invocation used to fall back to ONE repo-root-cwd
// group for every target, which defeats the whole cwd rewrite (webconsole
// targets need cwd=webconsole for the automatic JSX runtime — see the block
// comment above). Instead PARTITION per root: targets that resolve under
// webconsole/ get their own group at cwd=webconsole (paths rewritten
// relative to it); everything else gets its own group at the caller's cwd
// (paths left exactly as typed). The non-webconsole group is returned FIRST
// so a caller inspecting the first banner still sees the "did NOT guess"
// cwd for a mixed-root call, matching the pre-partition contract.
function bucketizeTsxTargets(targets, defaultCwd, explicitCwd) {
  if (explicitCwd) return [{ cwd: defaultCwd, targets, displayTargets: targets }];
  const webconsoleRoot = resolve(defaultCwd, 'webconsole');
  if (!existsSync(webconsoleRoot)) return [{ cwd: defaultCwd, targets, displayTargets: targets }];

  const webconsoleTargets = [];
  const webconsoleDisplay = [];
  const otherTargets = [];
  for (const t of targets) {
    const abs = resolve(defaultCwd, t);
    const rel = relative(webconsoleRoot, abs);
    if (isUnderWebconsole(rel)) {
      webconsoleTargets.push(rel);
      webconsoleDisplay.push(t);
    } else {
      otherTargets.push(t);
    }
  }

  const groups = [];
  if (otherTargets.length) groups.push({ cwd: defaultCwd, targets: otherTargets, displayTargets: otherTargets });
  if (webconsoleTargets.length) groups.push({ cwd: webconsoleRoot, targets: webconsoleTargets, displayTargets: webconsoleDisplay });
  return groups;
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));

  // BUG-708: check for stranded mutation backups BEFORE spawning anything —
  // a stale .bak means a src/sim/*.ts file may currently be sabotaged, which
  // would poison every group below with phantom failures.
  failOnStaleBakFiles();

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

  // F3: expand any glob targets ourselves and fail loudly (non-zero exit) on
  // a zero-match glob, rather than letting `node --test` silently report
  // PASS having discovered nothing. This also unlocks F2/F1 below — a glob
  // must be enumerated into real basenames before it can be partitioned.
  const nodeExpanded = expandAndValidate(nodeTargets, cwd, 'node');
  if (nodeExpanded.error) {
    process.stderr.write(`\n✖ ${nodeExpanded.error}\n`);
    process.exit(1);
  }
  const tsxExpanded = expandAndValidate(tsxTargets, cwd, 'tsx');
  if (tsxExpanded.error) {
    process.stderr.write(`\n✖ ${tsxExpanded.error}\n`);
    process.exit(1);
  }
  nodeTargets = nodeExpanded.files;
  tsxTargets = tsxExpanded.files;

  // D4 (BUG-708): a group killed/timed-out by the watchdog can strand a
  // mutation mid-cycle (see failOnStaleBakFiles' header comment) that a
  // LATER group in this SAME invocation would then run against silently.
  // Re-check ONLY when the just-finished group was actually killed/timed
  // out (the stranding condition) — a still-healthy group's own currently-
  // running mutator is not a strand, it's a test still legitimately mid-
  // cycle, and re-checking unconditionally after every group would false-
  // positive on exactly that. Deliberately calls the SAME
  // failOnStaleBakFiles() used pre-run (not a separate function) so a
  // sabotaged file is reported and the process exits fail-closed exactly
  // like the pre-run check does.
  function recheckStaleBakAfterGroup(groupLabel, result) {
    if (!result.timedOut) return;
    staleBakRecheckContext = { groupLabel };
    failOnStaleBakFiles();
  }

  const results = [];
  if (nodeTargets.length) {
    // F2: partition into a serial mutating subgroup and a parallel
    // everything-else subgroup, each with its own basename-derived cap
    // (fixes the glob-defeats-the-cap-lookup bug — SLOW_TEST_CAPS_SEC now
    // sees every real basename because the glob was expanded above, not a
    // literal '*' string). Parallel group first so --webconsole-ci's first
    // node banner is the one that actually contains chunked-replay.test.mjs.
    const { serial, parallel } = partitionMutating(nodeTargets);
    if (parallel.length) {
      const r = await runGroup('node', parallel, cwd, effectiveTimeoutSec(parallel, opts.timeoutSec), opts.reporter);
      results.push(r);
      recheckStaleBakAfterGroup(`node --test (${parallel.length} target(s), parallel)`, r);
    }
    if (serial.length) {
      const r = await runGroup('node', serial, cwd, effectiveTimeoutSec(serial, opts.timeoutSec), opts.reporter);
      results.push(r);
      recheckStaleBakAfterGroup(`node --test (${serial.length} target(s), serial/mutating)`, r);
    }
  }
  if (tsxTargets.length) {
    // --webconsole-ci already sets cwd=webconsole and its file list is
    // already webconsole-relative; only rewrite/partition for the
    // named-files path. F5: partition mixed roots into their own groups
    // instead of falling back to one repo-root-cwd group for everything.
    const tsxGroups = opts.webconsoleCi
      ? [{ cwd, targets: tsxTargets, displayTargets: tsxTargets }]
      : bucketizeTsxTargets(tsxTargets, cwd, opts.cwd);
    for (const g of tsxGroups) {
      // B5: partition each tsx cwd-bucket into serial/parallel exactly like
      // the node path above, so a tsx in-place mutator
      // (attack-bug641-round2.test.tsx) can never race a sibling tsx test the
      // way BUG-651's node-side race did — see partitionMutatingPaired.
      const { serialTargets, serialDisplay, parallelTargets, parallelDisplay } =
        partitionMutatingPaired(g.targets, g.displayTargets);
      if (parallelTargets.length) {
        const r = await runGroup('tsx', parallelTargets, g.cwd, effectiveTimeoutSec(parallelDisplay, opts.timeoutSec), opts.reporter, parallelDisplay);
        results.push(r);
        recheckStaleBakAfterGroup(`tsx --test (${parallelTargets.length} target(s), parallel, cwd ${g.cwd})`, r);
      }
      if (serialTargets.length) {
        const r = await runGroup('tsx', serialTargets, g.cwd, effectiveTimeoutSec(serialDisplay, opts.timeoutSec), opts.reporter, serialDisplay);
        results.push(r);
        recheckStaleBakAfterGroup(`tsx --test (${serialTargets.length} target(s), serial/mutating, cwd ${g.cwd})`, r);
      }
    }
  }

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
// NODE_TEST_CONTEXT for every discovered file it executes and invokes this
// file with NO extra argv (root discovery never passes CLI args); detect
// that combination and no-op out as a passing empty test file.
//
// F6 fix: keying off the env var ALONE was wrong — any invocation of this
// runner from inside a node:test process (e.g. a test that shells out to it,
// or scoped-runner-attack.mjs's own `run()` helper spawning it as a child)
// inherits NODE_TEST_CONTEXT from its parent and would silently no-op out
// having run NOTHING, a vacuous pass. Root auto-discovery is distinguishable
// from that: it never appends CLI arguments. Requiring BOTH the env var AND
// an empty argv keeps BUG-546's protection intact while an explicit
// invocation (any args at all) always runs for real regardless of what env
// it inherited.
if (process.env.NODE_TEST_CONTEXT && process.argv.length <= 2) {
  process.exit(0);
}

main().catch((err) => {
  process.stderr.write(`scoped.mjs crashed: ${err && err.stack ? err.stack : err}\n`);
  process.exit(1);
});
