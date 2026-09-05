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
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync, rmSync, readdirSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

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

// ROUND FINDING F1 (P1) — was EXPECTED RED at time of writing, fixed by the
// BUG-708 re-work: webconsole/test/feat-dynamic-bailout.test.mjs is now on
// FILE_MUTATING_TEST_BASENAMES (see scoped.mjs).
//
// RE-ROUND (BUG-708) HONESTY FIX (B2-B4): this check derives the expected set
// from the tests themselves (GR#15), but it is a PATTERN-MATCHED heuristic,
// not an exhaustive proof — it can only ever catch mutation shapes it knows
// to grep for, and it is only as complete as the API list and the file-glob
// below. The original version matched ONLY `writeFileSync(` against ONLY
// `*.test.mjs` files, which the re-round's B2/B3/B4 probes showed missed:
// an `fs/promises` `writeFile(` mutation (B2), a `copyFileSync`/`renameSync`
// in-place swap (B3), and any mutating `.test.tsx` file at all (B4 —
// attack-bug641-round2.test.tsx writeFileSyncs src/components/demandFixUi.ts
// in place and was invisible to a `*.test.mjs`-only glob). All three are now
// covered below. The HONEST claim is: this check catches every mutation
// written through `writeFileSync`, `fs/promises.writeFile`, or a
// `copyFileSync`+`renameSync` swap, against a path that looks like
// `src/sim/*.ts`, `src/components/*.ts`, or one of the known path-variable
// names, in any `*.test.mjs` or `*.test.tsx` file — NOT every conceivable way
// a test could mutate a shared source file (e.g. it would still miss a
// mutation performed by a spawned child process, or one written through a
// stream/fd API, or a path built from strings this regex can't parse). It
// falls behind exactly as far as the shapes it doesn't grep for; it is not a
// guarantee against every future mutation style.
test('serial: every src-mutating test is on FILE_MUTATING_TEST_BASENAMES [F1]', () => {
  const listed = new Set(
    [...RUNNER_SRC.matchAll(/'([\w.\-]+\.test\.(?:mjs|tsx))', \/\/ mutates/g)].map((m) => m[1]),
  );
  assert.ok(listed.size >= 5, 'expected to parse the mutating-test allowlist');
  const testDir = join(REPO, 'webconsole', 'test');
  const offenders = [];
  // B2/B3: writeFileSync, fs/promises writeFile, and a copyFileSync+renameSync
  // in-place swap are all mutation APIs seen in this repo's own RED-proof
  // tests (see FILE_MUTATING_TEST_BASENAMES's comments in scoped.mjs).
  const mutatingApi = /writeFileSync\s*\(|\bwriteFile\s*\(|copyFileSync\s*\([\s\S]{0,200}renameSync\s*\(/;
  // Tolerate both contiguous paths (src/sim/data.ts) and path.join-style
  // comma/quote-separated segments (path.join('src', 'sim', 'data.ts')) — a
  // handful of stray separator characters between segments either way.
  const srcPath = /src[\\/'",\s]{0,5}(?:sim|components)[\\/'",\s]{0,5}[\w.-]+\.tsx?|_TS_PATH|engineTsPath|dataTsPath/i;
  // B4: scan .test.tsx alongside .test.mjs — a tsx mutator is exactly as
  // dangerous to a shared src/*.ts(x) file as an mjs one.
  for (const f of readdirSync(testDir).filter((n) => /\.test\.(mjs|tsx)$/.test(n))) {
    const src = readFileSync(join(testDir, f), 'utf8');
    if (mutatingApi.test(src) && srcPath.test(src) && !listed.has(f)) offenders.push(f);
  }
  assert.deepEqual(offenders, [], `src-mutating tests missing from the allowlist: ${offenders.join(', ')}`);
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
