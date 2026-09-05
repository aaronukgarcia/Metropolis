// ATTACK ARTIFACT — independent destructive round on tools/test/scoped.mjs
// (GR#23, 2026-09-05, attacker "opus-round-scoped", items BUG-546 / BUG-651 /
// BUG-696). Written because the estate had ZERO automated self-tests despite
// its own header claiming "a drift check lives in scoped.test.mjs" — that file
// does not exist anywhere in the repo (round finding F5).
//
// PLACEMENT IS DELIBERATE: this file is `tools/scoped-runner-attack.mjs`, NOT
// `*.test.mjs` and NOT under a `test/` directory, so CI's repo-root
// `node --test --test-shard=N/3` does NOT auto-discover it (see BUG-543 — the
// runner itself had to grow a NODE_TEST_CONTEXT no-op precisely because it
// lives under tools/test/). Run it explicitly:
//
//     node --test tools/scoped-runner-attack.mjs
//
// SOME TESTS HERE ARE EXPECTED TO FAIL AT THE TIME OF WRITING. They encode
// round findings F1/F2/F3 as executable RED proofs rather than prose. Each is
// marked below. Fix the runner, and they go green; that is the point.
//
// The runner exposes nothing (no exports, and importing it exits the process
// under NODE_TEST_CONTEXT), so every assertion here is BLACK-BOX: spawn the
// real CLI and read what it prints/returns. Two of its three decisions are
// observable that way (the effective timeout and the child cwd are both on the
// `▶ ... [timeout Ns, reporter dot, cwd X]` banner). The THIRD — BUG-651's
// `--test-concurrency=1` injection — is NOT printed anywhere, so it cannot be
// black-box tested at all (round finding F4); it is covered here by a
// source-level check plus a derived allowlist-completeness check instead.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync, rmSync, readdirSync, cpSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const RUNNER = join(REPO, 'tools', 'test', 'scoped.mjs');
const RUNNER_SRC = readFileSync(RUNNER, 'utf8');

const TRIVIAL_MJS = "import { test } from 'node:test';\ntest('trivial', () => {});\n";

// NOTE (round finding F6, discovered BY this harness): every child spawned
// from inside `node --test` inherits NODE_TEST_CONTEXT, and the runner's
// BUG-546 guard makes it exit 0 immediately having run NOTHING. Without the
// explicit delete below, EVERY test in this file passes vacuously — which is
// very likely why the drift check the runner's header promises was never
// actually written. Tests that want the hostile env set it back deliberately.
function run(args, opts = {}) {
  const env = { ...process.env, ...(opts.env || {}) };
  if (!(opts.env && 'NODE_TEST_CONTEXT' in opts.env)) delete env.NODE_TEST_CONTEXT;
  return spawnSync(process.execPath, [RUNNER, ...args], {
    cwd: opts.cwd || REPO,
    encoding: 'utf8',
    env,
    timeout: opts.killAfterMs || 120000,
  });
}

// The banner is `▶ <runner> --test (N targets)  [timeout Ns, reporter dot, cwd C]`
function banners(res) {
  const out = `${res.stdout || ''}${res.stderr || ''}`;
  return [...out.matchAll(/\[timeout (\d+)s, reporter (\w+), cwd (.+?)\]/g)]
    .map((m) => ({ timeoutSec: Number(m[1]), reporter: m[2], cwd: m[3] }));
}

function scratch(name, body = TRIVIAL_MJS) {
  const dir = mkdtempSync(join(tmpdir(), 'scoped-attack-'));
  const file = join(dir, name);
  writeFileSync(file, body, 'utf8');
  return { dir, file };
}

// ───────────────────────────── the 30-minute ceiling ─────────────────────────
// Aaron's watchdog ruling. Nothing may exceed it; the constant is the contract.

test('ceiling: --timeout is clamped to 1800s', () => {
  const s = scratch('ceil.test.mjs');
  try {
    const b = banners(run(['--timeout=999999', s.file]));
    assert.equal(b.length, 1);
    assert.equal(b[0].timeoutSec, 1800);
  } finally { rmSync(s.dir, { recursive: true, force: true }); }
});

test('ceiling: SCOPED_TIMEOUT_MS env is clamped to 1800s', () => {
  const s = scratch('ceil-env.test.mjs');
  try {
    const b = banners(run([s.file], { env: { SCOPED_TIMEOUT_MS: '999999999' } }));
    assert.equal(b[0].timeoutSec, 1800);
  } finally { rmSync(s.dir, { recursive: true, force: true }); }
});

test('ceiling: a SLOW_TEST_CAPS_SEC allowlist entry cannot exceed it either', () => {
  // Basename match is how the cap is looked up, so a scratch file with an
  // allowlisted name inherits the cap without running the real (slow) suite.
  const s = scratch('chunked-replay.test.mjs');
  try {
    assert.equal(banners(run([s.file]))[0].timeoutSec, 600, 'named slow cap must still apply');
    assert.equal(banners(run(['--timeout=999999', s.file]))[0].timeoutSec, 1800, 'and still be clamped');
  } finally { rmSync(s.dir, { recursive: true, force: true }); }
});

test('ceiling: the clamp does not SHORTEN a legitimate sub-ceiling cap', () => {
  // Guards the inverse risk: every SLOW_TEST_CAPS_SEC value must survive the
  // clamp unchanged, i.e. every entry must be <= the ceiling.
  const ceiling = Number(/WATCHDOG_ABSOLUTE_CEILING_SEC = (\d+)/.exec(RUNNER_SRC)[1]);
  const caps = [...RUNNER_SRC.matchAll(/\['([\w.\-]+\.test\.[mt]sx?j?s?)', (\d+)\]/g)]
    .map((m) => ({ file: m[1], cap: Number(m[2]) }));
  assert.ok(caps.length >= 5, 'expected to parse the slow-cap table');
  for (const c of caps) {
    assert.ok(c.cap <= ceiling, `${c.file} cap ${c.cap}s exceeds the ${ceiling}s ceiling and would be silently shortened`);
  }
});

// ───────────────────────── BUG-696: tsx group cwd rewrite ────────────────────

test('tsx: a webconsole target runs with cwd=webconsole', () => {
  const b = banners(run(['webconsole/test/keybindings.test.tsx']));
  assert.equal(b.length, 1);
  assert.ok(/[\\/]webconsole$/.test(b[0].cwd), `expected cwd to end in webconsole, got ${b[0].cwd}`);
});

test('tsx: an explicit --cwd wins over the rewrite', () => {
  const b = banners(run(['--cwd=.', 'webconsole/test/keybindings.test.tsx']));
  assert.ok(!/[\\/]webconsole$/.test(b[0].cwd), `--cwd must win, got ${b[0].cwd}`);
});

test('tsx: mixed roots fall back to the caller cwd rather than guessing', () => {
  // Same-drive mixed group: correctly falls back.
  const dir = join(REPO, 'tools', '.scoped-attack-tmp');
  mkdirSync(dir, { recursive: true });
  const file = join(dir, 'root-level.test.tsx');
  writeFileSync(file, "import { test } from 'node:test';\ntest('t', () => {});\n", 'utf8');
  try {
    const b = banners(run(['webconsole/test/keybindings.test.tsx', file]));
    assert.ok(!/[\\/]webconsole$/.test(b[0].cwd), 'a non-webconsole target must defeat the rewrite');
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

// ROUND FINDING F4 (P3) — EXPECTED RED AT TIME OF WRITING.
// resolveTsxGroupCwd decides "is this target under webconsole/" with
// `relative(webconsoleRoot, abs)` and rejects only `''`, a leading `..`, or a
// leading path separator. On Windows, `relative()` between DIFFERENT DRIVES
// returns the target's own ABSOLUTE path (e.g. `C:\tmp\x.test.tsx`), which
// matches none of those, so a cross-drive target is silently accepted as
// "under webconsole/" and the whole group is moved to cwd=webconsole. It does
// not currently break execution (the accepted path stays absolute, so the
// child still finds it), but the documented mixed-root fallback is defeated.
// Fix: also reject `isAbsolute(rel)`.
test('tsx: a cross-drive target must also defeat the rewrite [F4]', () => {
  const s = scratch('crossdrive.test.tsx', "import { test } from 'node:test';\ntest('t', () => {});\n");
  try {
    const b = banners(run(['webconsole/test/keybindings.test.tsx', s.file]));
    assert.ok(!/[\\/]webconsole$/.test(b[0].cwd),
      `a target on another drive is not under webconsole/, but cwd became ${b[0].cwd}`);
  } finally { rmSync(s.dir, { recursive: true, force: true }); }
});

test('tsx: failures are reported by the caller-typed path, not the rewritten one', () => {
  const res = run(['webconsole/test/NOPE-does-not-exist.test.tsx', '--timeout=60']);
  const out = `${res.stdout}${res.stderr}`;
  assert.notEqual(res.status, 0, 'a missing target must fail the run');
  assert.ok(out.includes('webconsole/test/NOPE-does-not-exist.test.tsx'),
    'the caller-typed target string must appear in the output');
});

// ──────────────────────── BUG-651: serial execution ──────────────────────────

test('serial: the concurrency injection exists and is gated on the allowlist', () => {
  assert.ok(RUNNER_SRC.includes('--test-concurrency=1'), 'BUG-651 injection removed');
  assert.ok(/function needsSerialExecution/.test(RUNNER_SRC), 'BUG-651 gate removed');
});

// ROUND FINDING F1 (P1), SUPERSEDED BY BUG-739 (2026-09-05): this test used
// to assert "every src-mutating test is on FILE_MUTATING_TEST_BASENAMES" —
// i.e. it TOLERATED in-place mutation of a shared webconsole/src file as
// long as scoped.mjs knew to serialise it. BUG-739's independent round found
// that tolerance was never actually safe: CI's node-test job invokes the
// bare `node --test` runner directly (sharded 3 ways, default concurrency),
// which never goes through scoped.mjs at all, so the serialisation this test
// was protecting was NEVER in effect on CI. An unrelated importing sibling
// landing in the same CI shard as a mutating test could transiently observe
// the sabotaged real file mid-mutation.
//
// The fix converted every one of those tests to run its RED-PROOF against a
// private, disposable shadow copy of webconsole/src (webconsole/test/
// helpers/mutant.mjs's runWithMutant/runMutantSelfReinvoke/createMutantShadow)
// instead of writing the real file in place — so the invariant this test
// enforces FLIPS: no test may write into webconsole/src at all, full stop.
// FILE_MUTATING_TEST_BASENAMES/FILE_MUTATING_TEST_TARGET_BASENAME in
// scoped.mjs are kept only as documentation of the retired mechanism (both
// now empty Sets/Maps) — this test no longer reads them.
//
// R3 (BUG-739 round REJECT, 2026-09-05): the FIRST version of this check used
// a fixed-size text WINDOW around each write call to avoid false-flagging a
// file that legitimately imports the real src (for a read-only baseline)
// while separately writing an unrelated throwaway probe file elsewhere. The
// round found this window is itself a gap: a real mutator that assigns a
// src-like path to a variable, then calls writeFileSync(thatVariable, ...)
// with 200+ characters of comment between the two, sails straight through —
// proximity was never the right signal. FIXED by tracing the write's TARGET
// EXPRESSION instead of textual distance: this scans the WHOLE file (no
// window) to build a set of variable names whose OWN declaration's
// right-hand side looks like a src path (directly, or via a simple
// alias-of-an-alias chain), then flags a write call whose first argument is
// either a literal src-like path OR a bare identifier in that traced set —
// regardless of how far apart the declaration and the call sit. The
// original false-positive concern (a file that merely MENTIONS a real src
// path in an unrelated baseline import, or in a comment, while writing to
// something else entirely) is handled the same way it always should have
// been: by requiring the WRITE CALL's own argument to resolve to a src path,
// not by requiring the two substrings to be textually close.
//
// Any file that imports webconsole/testsupport/mutant.mjs (static `from` or
// dynamic `await import(...)`) is exempted outright — it is trusted to route
// its mutation through the helper's own shadow-copy safety net (which
// independently asserts the real file is byte-identical after every run,
// BUG-708-style), so this heuristic does not need to (and, being purely
// textual, cannot reliably) verify HOW the helper itself is used inside that
// file. (The helper lives at webconsole/testsupport/, NOT webconsole/test/ —
// moved there same-session, BUG-739 follow-up P1, because CI's root
// `node --test --test-shard=N/3` discovers every file under any test/-named
// directory and choked on the helper's own ambient .d.mts type declarations
// as if they were a test.)
//
// Same honesty caveat as every version of this check: it is PATTERN-MATCHED,
// not a real interpreter — it would still miss a mutation performed by a
// spawned child process, one written through a raw stream/fd API, or a path
// built by string concatenation this regex can't parse. It falls behind
// exactly as far as the shapes it doesn't grep for.
test('forbidden: NO test writes into webconsole/src [F1, superseded by BUG-739]', () => {
  const testDir = join(REPO, 'webconsole', 'test');
  const offenders = [];
  const usesHelper = /(?:from\s+['"]|import\(\s*['"])\.\.\/testsupport\/mutant\.mjs['"]/;
  const mutatingApiCall = /(?:writeFileSync|writeFile|copyFileSync)\s*\(\s*([^,)]+)/g;
  // Tolerate both contiguous paths (src/sim/data.ts) and path.join-style
  // comma/quote-separated segments (path.join('src', 'sim', 'data.ts')) — a
  // handful of stray separator characters between segments either way.
  const srcPath = /src[\\/'",\s]{0,5}(?:sim|components)[\\/'",\s]{0,5}[\w.-]+\.tsx?|_TS_PATH|engineTsPath|dataTsPath/i;
  // A declaration whose RHS looks like a src path: `const X = ...src/sim/...`
  const declPattern = /(?:const|let|var)\s+(\w+)\s*=\s*([^;\n]+)/g;

  for (const f of readdirSync(testDir).filter((n) => /\.test\.(mjs|tsx)$/.test(n))) {
    const src = readFileSync(join(testDir, f), 'utf8');
    if (usesHelper.test(src)) continue; // trusts the helper's own internal shadow-copy safety net

    // Build the traced-alias set: any variable whose OWN declaration's RHS
    // is a src-like path, resolved through up to a few rounds of
    // alias-of-an-alias (`const a = REAL_PATH; const b = a;`).
    const srcVars = new Set();
    const decls = [...src.matchAll(declPattern)];
    for (let round = 0; round < 4; round++) {
      let changed = false;
      for (const [, name, rhs] of decls) {
        if (srcVars.has(name)) continue;
        const rhsIsDirectSrcPath = srcPath.test(rhs);
        const rhsIsAlias = /^\s*(\w+)\s*$/.test(rhs) && srcVars.has(rhs.trim());
        if (rhsIsDirectSrcPath || rhsIsAlias) {
          srcVars.add(name);
          changed = true;
        }
      }
      if (!changed) break;
    }

    let flagged = false;
    for (const m of src.matchAll(mutatingApiCall)) {
      const target = m[1].trim();
      if (srcPath.test(target) || srcVars.has(target)) {
        flagged = true;
        break;
      }
    }
    // copyFileSync + renameSync in-place swap (a real second shape this
    // repo's RED-proofs used to use): renameSync's own DESTINATION argument
    // is the same target-expression check, traced the same way.
    if (!flagged) {
      for (const m of src.matchAll(/renameSync\s*\(\s*[^,]+,\s*([^,)]+)/g)) {
        const target = m[1].trim();
        if (srcPath.test(target) || srcVars.has(target)) {
          flagged = true;
          break;
        }
      }
    }
    if (flagged) offenders.push(f);
  }
  assert.deepEqual(
    offenders,
    [],
    `test(s) still write into webconsole/src in place — convert to webconsole/test/helpers/mutant.mjs ` +
      `(runWithMutant/runMutantSelfReinvoke/createMutantShadow) instead: ${offenders.join(', ')}`,
  );
});

// R3 companion (BUG-739 round REJECT, 2026-09-05): the F1 test above proves
// no CURRENT test file writes into src — but a heuristic that has never seen
// a real positive is an untested detector. This proves it actually FIRES:
// a synthetic fixture, planted in a scratch temp dir (never inside the real
// webconsole/test — the check under test only scans that directory, so a
// fixture placed there would need cleanup this test can't risk skipping)
// mirroring the exact evasion the round found — an ALIASED path constant
// with 200+ characters of comment between the declaration and the write
// call — is fed through the SAME detection logic inline (duplicated
// minimally rather than importing the real test's closure, since node:test
// doesn't expose it) and must be caught.
test('R3: the F1 target-expression trace actually catches an aliased-path-with-a-comment-gap mutator (synthetic fixture, not a real file)', () => {
  const fixtureSrc = [
    "import { writeFileSync, readFileSync } from 'node:fs';",
    "const ENGINE_ALIAS = '../src/sim/engine.ts';",
    '// '.padEnd(220, 'x'), // 200+ chars of filler comment between decl and use
    'const original = readFileSync(ENGINE_ALIAS, "utf8");',
    'writeFileSync(ENGINE_ALIAS, original.replace("a", "b"), "utf8");',
  ].join('\n');

  const usesHelper = /(?:from\s+['"]|import\(\s*['"])\.\.\/testsupport\/mutant\.mjs['"]/;
  const mutatingApiCall = /(?:writeFileSync|writeFile|copyFileSync)\s*\(\s*([^,)]+)/g;
  const srcPath = /src[\\/'",\s]{0,5}(?:sim|components)[\\/'",\s]{0,5}[\w.-]+\.tsx?|_TS_PATH|engineTsPath|dataTsPath/i;
  const declPattern = /(?:const|let|var)\s+(\w+)\s*=\s*([^;\n]+)/g;

  assert.ok(!usesHelper.test(fixtureSrc), 'fixture sanity: must not accidentally import the helper (would exempt it)');
  const srcVars = new Set();
  for (const [, name, rhs] of fixtureSrc.matchAll(declPattern)) {
    if (srcPath.test(rhs)) srcVars.add(name);
  }
  assert.ok(srcVars.has('ENGINE_ALIAS'), 'fixture sanity: ENGINE_ALIAS must be traced as a src-path variable');
  let flagged = false;
  for (const m of fixtureSrc.matchAll(mutatingApiCall)) {
    if (srcPath.test(m[1].trim()) || srcVars.has(m[1].trim())) flagged = true;
  }
  assert.ok(flagged, 'the aliased-path-with-comment-gap fixture must be flagged by the SAME target-expression trace F1 uses');
});

// B5 (was RED, now fixed by the re-work): the partition itself used to be
// mjs-only (main() only called partitionMutating on nodeTargets), so a tsx
// group containing an in-place mutator ran at full concurrency regardless of
// FILE_MUTATING_TEST_BASENAMES. scoped.mjs now also partitions every tsx
// cwd-bucket via partitionMutatingPaired — assert the call exists so a
// regression here reds loudly instead of silently losing tsx serialisation.
test('serial: tsx groups are ALSO partitioned for in-place mutators [B5]', () => {
  assert.ok(/partitionMutatingPaired\s*\(/.test(RUNNER_SRC), 'BUG-708 tsx partition (partitionMutatingPaired) removed');
});

// ──────────────────────── exit-code / vacuous-pass honesty ───────────────────

test('honesty: a missing named target fails the run', () => {
  const res = run(['webconsole/test/NOPE-does-not-exist.test.mjs', '--timeout=60']);
  assert.notEqual(res.status, 0);
  assert.ok(/RESULT: FAIL/.test(res.stdout));
});

test('honesty: an unusable --cwd fails the run', () => {
  const res = run(['--cwd=' + join(tmpdir(), 'definitely-not-a-dir-xyz'), 'webconsole/test/consistency.test.mjs', '--timeout=60']);
  assert.notEqual(res.status, 0);
});

test('honesty: no arguments is a usage error, not a pass', () => {
  const res = run([]);
  assert.equal(res.status, 2);
});

// ROUND FINDING F3 (P1) — EXPECTED RED AT TIME OF WRITING.
// "A gate that cannot evaluate must not report success." A glob that matches
// zero files currently prints `RESULT: PASS` and exits 0 having run no tests.
// That is the same shape as --webconsole-ci's node group, whose target is the
// glob `test/*.test.mjs`: point it at a cwd without a test/ directory and the
// house CI gate reports green having verified nothing.
test('honesty: a glob matching zero files must NOT report PASS [F3]', () => {
  const res = run(['webconsole/test/zzz-no-such-prefix-*.test.mjs', '--timeout=60']);
  assert.notEqual(res.status, 0, 'zero tests discovered must not exit 0');
});

// ROUND FINDING F2 (P0 for the gate) — EXPECTED RED AT TIME OF WRITING.
// --webconsole-ci hands the node group the glob `test/*.test.mjs`. The slow-cap
// table is keyed by BASENAME, and a glob matches no basename, so the group that
// contains chunked-replay.test.mjs (measured ~350s ALONE, hence its own 600s
// entry) is capped at the 240s default -- and BUG-651 now also forces all 202
// matched files through --test-concurrency=1. The lead's /ci-green path can
// therefore never pass on time.
test('honesty: --webconsole-ci must budget at least its own slowest allowlisted file [F2]', () => {
  // Read the DEFAULT budget off the banner, then abandon the run immediately —
  // we must not actually sit through 202 serialised files.
  const b = banners(run(['--webconsole-ci'], { killAfterMs: 8000 }));
  assert.ok(b.length >= 1, 'expected a node-group banner');
  const nodeGroup = b[0];
  assert.ok(nodeGroup.timeoutSec >= 600,
    `--webconsole-ci node group budget is ${nodeGroup.timeoutSec}s but it contains chunked-replay.test.mjs, ` +
    'whose own allowlist entry is 600s — the glob defeats the basename-keyed cap lookup');
});

// ──────────────────────── BUG-546: NODE_TEST_CONTEXT no-op ───────────────────

test('BUG-546: discovered as a test under node --test, the runner no-ops green', () => {
  const res = spawnSync(process.execPath, ['--test', 'tools/test/scoped.mjs'], {
    cwd: REPO, encoding: 'utf8', timeout: 60000,
  });
  assert.equal(res.status, 0, 'must not red CI as a "failed test"');
});

// ROUND FINDING F6 (P2) — EXPECTED RED AT TIME OF WRITING.
// The BUG-546 guard keys off an INHERITED env var. Any invocation of the runner
// from inside a node --test process (a test that shells out to it, a wrapper
// script run under the test runner) inherits NODE_TEST_CONTEXT and silently
// exits 0 having run nothing at all — a vacuous pass with no output. Nothing
// in-repo does this today, so it is latent, not live; the guard should key off
// "am I the discovered file" rather than "is the env var present".
test('BUG-546: a real CLI run must not vacuously pass just because the env var is set [F6]', () => {
  const s = scratch('nested.test.mjs', "import { test } from 'node:test';\ntest('fails', () => { throw new Error('boom'); });\n");
  try {
    const res = run([s.file, '--timeout=60'], { env: { NODE_TEST_CONTEXT: 'child-v8' } });
    assert.notEqual(res.status, 0, 'a failing target must fail even under an inherited NODE_TEST_CONTEXT');
  } finally { rmSync(s.dir, { recursive: true, force: true }); }
});

// ──────────────────────── drift check the header promises ────────────────────

// ──────────────────────── BUG-708: stranded mutation backups ─────────────────

test('BUG-708: a stale in-source .bak fails the run loudly, naming the file, instead of silently testing sabotaged source', () => {
  const simDir = join(REPO, 'webconsole', 'src', 'sim');
  const fakeBak = join(simDir, 'data.ts.scoped-attack-fake.bak');
  writeFileSync(fakeBak, '// fake stranded backup planted by scoped-runner-attack.mjs — should never survive a real run\n', 'utf8');
  try {
    const s = scratch('bug708.test.mjs');
    try {
      const res = run([s.file, '--timeout=60']);
      const out = `${res.stdout}${res.stderr}`;
      assert.notEqual(res.status, 0, 'a stale .bak must fail the run, not silently pass');
      assert.ok(out.includes('data.ts.scoped-attack-fake.bak'), 'must name the stranded backup file');
      assert.ok(/BUG-708/.test(out), 'must reference the finding so it is traceable');
    } finally { rmSync(s.dir, { recursive: true, force: true }); }
  } finally { rmSync(fakeBak, { force: true }); }
});

// The runner's header says the webconsole tsx list is "kept in lockstep with
// webconsole/package.json's test script ... (a drift check lives in
// scoped.test.mjs so the two can't silently diverge)". No such file exists
// (round finding F5). Here it is.
test('drift: WEBCONSOLE_TSX_FILES matches the webconsole package script [F5]', () => {
  const listed = [...RUNNER_SRC.matchAll(/'(test\/[\w.\-]+\.test\.tsx)'/g)].map((m) => m[1]);
  const pkg = JSON.parse(readFileSync(join(REPO, 'webconsole', 'package.json'), 'utf8'));
  const script = pkg.scripts.test;
  const fromScript = [...script.matchAll(/(test\/[\w.\-]+\.test\.tsx)/g)].map((m) => m[1]);
  assert.deepEqual(listed, fromScript, 'runner tsx list has drifted from the package script');
});

// ──────────────────────── BUG-739: CI-shape reproduction ─────────────────────
//
// This is the round's own reproduction of the defect scoped.mjs's
// serialisation could never fix: CI's node-test job invokes the BARE
// `node --test` runner directly, sharded 3 ways, at DEFAULT concurrency —
// it never goes through scoped.mjs's FILE_MUTATING_TEST_BASENAMES
// serialisation at all. Two real, converted mutator tests
// (attack-bug643-memo.test.mjs, bug645-population-visibility.test.mjs — both
// target src/sim/data.ts) run alongside a minimal IMPORTING SIBLING of
// data.ts, via a direct `node --test <files...>` invocation (no scoped.mjs
// in the loop at all, matching CI's shape exactly), at whatever concurrency
// node picks by default.
//
// SAFETY: this whole reproduction runs against a DISPOSABLE SCRATCH COPY of
// webconsole (src/ + test/ + testsupport/, cpSync'd into a temp dir), never
// the real repo tree — including for the deliberately-unsafe
// mutation-testing proof below, which sets MUTANT_UNSAFE_WRITE_IN_PLACE=1
// specifically to reintroduce BUG-739's original in-place-write race. That
// knob's writes still land inside webconsole/testsupport/mutant.mjs's own
// SRC_ROOT computation (relative to ITS OWN file location), so copying the
// whole webconsole tree — testsupport/mutant.mjs included — is what makes
// "in place" mean "in the scratch copy" for this proof rather than "in the
// real, live repository source". (History: the first version of this proof
// pointed the unsafe
// knob at the REAL webconsole/src directly and the race it deliberately
// provoked left the real src/sim/data.ts genuinely corrupted on disk after
// the run — caught immediately by the very next `node --test` on the real
// tree going red, fixed by hand, and this test rewritten to never touch the
// real tree again.)
function buildScratchWebconsole() {
  const scratchDir = mkdtempSync(join(tmpdir(), 'scoped-attack-ci-shape-webconsole-'));
  cpSync(join(REPO, 'webconsole', 'src'), join(scratchDir, 'src'), { recursive: true });
  cpSync(join(REPO, 'webconsole', 'test'), join(scratchDir, 'test'), { recursive: true });
  // testsupport/ (mutant.mjs + its .d.mts) is a SIBLING of src/ and test/,
  // not nested under either — the real mutator files import it via
  // '../testsupport/mutant.mjs' relative to their own location under test/,
  // so the scratch copy needs the same sibling layout or that import fails
  // to resolve inside the scratch run (BUG-739 CI-shape follow-up, same
  // move that relocated the helper out of CI's test/-directory discovery).
  cpSync(join(REPO, 'webconsole', 'testsupport'), join(scratchDir, 'testsupport'), { recursive: true });
  return scratchDir;
}

function writeCiShapeSibling(scratchDir) {
  const dataTsUrl = pathToFileURL(join(scratchDir, 'src', 'sim', 'data.ts')).href;
  const file = join(scratchDir, 'test', 'zz-ci-shape-sibling.test.mjs');
  writeFileSync(
    file,
    [
      "import { test } from 'node:test';",
      "import assert from 'node:assert/strict';",
      `import { SPECS } from ${JSON.stringify(dataTsUrl)};`,
      "test('ci-shape sibling: SPECS is the real, unmutated catalogue', () => {",
      "  assert.ok(SPECS && Object.keys(SPECS).length > 0, 'SPECS must be populated');",
      "  assert.ok(SPECS.res_hut, 'a known real spec id must exist — a corrupted data.ts would likely lose or rename this');",
      '});',
      '',
    ].join('\n'),
    'utf8',
  );
  return file;
}

function runCiShape(extraEnv = {}) {
  const webconsoleDir = buildScratchWebconsole();
  try {
    const mutatorA = join(webconsoleDir, 'test', 'attack-bug643-memo.test.mjs');
    const mutatorB = join(webconsoleDir, 'test', 'bug645-population-visibility.test.mjs');
    const siblingFile = writeCiShapeSibling(webconsoleDir);
    // NODE_TEST_CONTEXT must NOT be inherited: this attack file itself runs
    // under an outer `node --test`, which sets that var for THIS process;
    // spawnSync inherits the parent env by default, and Node's test runner
    // then treats the child as a recursive re-entry and SKIPS running the
    // files entirely ("run() is being called recursively within a test
    // file") — the exact F6/BUG-546 class this repo has hit repeatedly.
    const childEnv = { ...process.env, ...extraEnv };
    delete childEnv.NODE_TEST_CONTEXT;
    const res = spawnSync(
      process.execPath,
      ['--test', mutatorA, mutatorB, siblingFile],
      { cwd: webconsoleDir, encoding: 'utf8', timeout: 180000, env: childEnv },
    );
    return { res, out: `${res.stdout || ''}${res.stderr || ''}` };
  } finally {
    rmSync(webconsoleDir, { recursive: true, force: true });
  }
}

test('BUG-739 CI-shape: an importing sibling of data.ts survives two mutator tests run alongside it via plain `node --test` (no scoped.mjs, matching CI\'s sharded default-concurrency shape)', () => {
  const { res, out } = runCiShape();
  assert.equal(res.status, 0, `CI-shape run must exit 0 (sibling + both mutators all green); status=${res.status}\n${out}`);
  assert.match(out, /ci-shape sibling: SPECS is the real, unmutated catalogue/, 'the sibling test must actually have run');
});

// MUTATION-TESTING PROOF (task gate): flip webconsole/test/helpers/
// mutant.mjs's MUTANT_UNSAFE_WRITE_IN_PLACE knob — which deliberately
// reintroduces BUG-739's original defect (mutate the file in place instead
// of a shadow copy, still confined to the scratch webconsole copy this
// whole reproduction runs against — see buildScratchWebconsole's safety
// comment above) — and confirm the SAME CI-shape run above now REDS. This is
// the mechanical proof that the shadow-copy mechanism is actually
// load-bearing, not just present: remove it, and this exact test goes red.
test('BUG-739 mutation-testing proof: with the shadow copy DISABLED (MUTANT_UNSAFE_WRITE_IN_PLACE=1), the same CI-shape run REDS', () => {
  const { res, out } = runCiShape({ MUTANT_UNSAFE_WRITE_IN_PLACE: '1' });
  assert.notEqual(
    res.status,
    0,
    'with the shadow-copy mechanism disabled, the CI-shape run must fail (either the sibling observes a ' +
      'mutant, or a mutator\'s own in-place restore step races and corrupts data.ts for the other) — a ' +
      'status of 0 here means the shadow copy was NOT the thing keeping this green\n' + out,
  );
});
