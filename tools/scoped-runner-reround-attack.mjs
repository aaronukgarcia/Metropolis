// RE-ROUND ATTACK ARTIFACT — independent destructive re-round on
// tools/test/scoped.mjs after the BUG-651 / BUG-708 rework
// (GR#23, 2026-09-05, attacker "opus-reround-scoped").
//
// Sibling of tools/scoped-runner-attack.mjs (the adopted permanent self-test).
// Deliberately NOT *.test.mjs and NOT under a test/ dir, so CI's repo-root
// `node --test` never auto-discovers it. Run explicitly:
//
//     node --test tools/scoped-runner-reround-attack.mjs
//
// Probes the four surfaces the rework's claims rest on:
//   A. group sequencing (parallel group and serial group must NEVER overlap)
//   B. the derived completeness invariant's real pattern coverage
//   C. NODE_TEST_CONTEXT stripping depth (root guard vs grandchildren)
//   D. the BUG-708 stale-.bak sweep's directory + naming coverage
//
// Tests marked [RED] encode a finding and are EXPECTED to fail against the
// estate as delivered.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, appendFileSync, existsSync, rmSync, mkdirSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const RUNNER = join(REPO, 'tools', 'test', 'scoped.mjs');
const RUNNER_SRC = readFileSync(RUNNER, 'utf8');

function run(args, opts = {}) {
  const env = { ...process.env, ...(opts.env || {}) };
  if (!(opts.env && 'NODE_TEST_CONTEXT' in opts.env)) delete env.NODE_TEST_CONTEXT;
  return spawnSync(process.execPath, [RUNNER, ...args], {
    cwd: opts.cwd || REPO, encoding: 'utf8', env, timeout: opts.killAfterMs || 180000,
  });
}
const outOf = (r) => `${r.stdout || ''}${r.stderr || ''}`;

// ─────────────── A. GROUP SEQUENCING (the core BUG-651 partition claim) ───────

// The partition is only safe if the parallel group is fully FINISHED before the
// serial (mutating) group starts. If the two node processes ever overlap, a
// serial-group test's in-place mutation of src/sim/data.ts is live on disk
// while parallel-group tests import it — BUG-651's corruption recreated at
// group scale.
test('A1 sequencing: the parallel node group must fully complete before the serial group starts', () => {
  const dir = mkdtempSync(join(tmpdir(), 'scoped-seq-'));
  const ledger = join(dir, 'ledger.txt').replace(/\\/g, '\\\\');
  const body = (tag) => `
import { test } from 'node:test';
import { appendFileSync } from 'node:fs';
test('${tag}', async () => {
  appendFileSync('${ledger}', '${tag} START ' + Date.now() + '\\n');
  await new Promise((r) => setTimeout(r, 1500));
  appendFileSync('${ledger}', '${tag} END ' + Date.now() + '\\n');
});
`;
  // 'attack-bug642-memo.test.mjs' is on FILE_MUTATING_TEST_BASENAMES => serial group.
  const serialFile = join(dir, 'attack-bug642-memo.test.mjs');
  const parallelFile = join(dir, 'ordinary-probe.test.mjs');
  writeFileSync(serialFile, body('SERIAL'), 'utf8');
  writeFileSync(parallelFile, body('PARALLEL'), 'utf8');
  try {
    const res = run([parallelFile, serialFile, '--timeout=120']);
    assert.equal(res.status, 0, `probe run must pass; got:\n${outOf(res)}`);
    const lines = readFileSync(join(dir, 'ledger.txt'), 'utf8').trim().split('\n');
    const at = (t) => Number(lines.find((l) => l.startsWith(t)).split(' ')[2]);
    const pEnd = at('PARALLEL END');
    const sStart = at('SERIAL START');
    assert.ok(sStart >= pEnd,
      `serial group started ${pEnd - sStart}ms BEFORE the parallel group ended — groups overlap:\n${lines.join('\n')}`);
    // and the decisions must be printed for both groups (F7 contract)
    const out = outOf(res);
    assert.ok(/serialisation: parallel/.test(out), 'parallel group decision not printed');
    assert.ok(/serialisation: SERIAL/.test(out), 'serial group decision not printed');
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

// ─────────────── B. COMPLETENESS INVARIANT PATTERN COVERAGE ──────────────────

// The rework's answer to F1 is a DERIVED check in scoped-runner-attack.mjs that
// greps every webconsole/test/*.test.mjs for `writeFileSync(` AND a src/sim path
// marker. Plant a file that mutates the very same shared source through an
// equally ordinary API and see whether the invariant still holds.
function plantAndCheckInvariant(name, src) {
  const target = join(REPO, 'webconsole', 'test', name);
  writeFileSync(target, src, 'utf8');
  try {
    const res = spawnSync(process.execPath,
      ['--test', '--test-name-pattern', 'every src-mutating test', join(REPO, 'tools', 'scoped-runner-attack.mjs')],
      { cwd: REPO, encoding: 'utf8', timeout: 120000, env: (() => { const e = { ...process.env }; delete e.NODE_TEST_CONTEXT; return e; })() });
    return { caught: res.status !== 0, out: outOf(res) };
  } finally { rmSync(target, { force: true }); }
}

test('B1 invariant: catches the CANONICAL writeFileSync + *_TS_PATH shape (control)', () => {
  const r = plantAndCheckInvariant('zz-reround-control.test.mjs', `
import fs from 'node:fs';
import path from 'node:path';
const DATA_TS_PATH = path.join('webconsole', 'src', 'sim', 'data.ts');
fs.writeFileSync(DATA_TS_PATH, 'x');
`);
  assert.equal(r.caught, true, 'control must be caught, otherwise the invariant is vacuous');
});

// [RED] fs.promises.writeFile is the same mutation with a different API.
test('B2 invariant: [RED] must also catch a promises-API in-place mutation of src/sim', () => {
  const r = plantAndCheckInvariant('zz-reround-promises.test.mjs', `
import { writeFile } from 'node:fs/promises';
import path from 'node:path';
const p = path.join('webconsole', 'src', 'sim', 'data.ts');
await writeFile(p, 'sabotaged');
`);
  assert.equal(r.caught, true, 'a fs/promises writeFile mutation of src/sim/data.ts slipped past the derived invariant');
});

// [RED] the cp/restore half of the very cycle the allowlist documents.
test('B3 invariant: [RED] must also catch a copyFileSync/renameSync swap of src/sim', () => {
  const r = plantAndCheckInvariant('zz-reround-rename.test.mjs', `
import { copyFileSync, renameSync } from 'node:fs';
import path from 'node:path';
const p = path.join('webconsole', 'src', 'sim', 'engine.ts');
copyFileSync(p, p + '.zz.bak');
renameSync(p + '.mutated', p);
`);
  assert.equal(r.caught, true, 'a copyFileSync/renameSync in-place swap slipped past the derived invariant');
});

// [RED] tsx tests mutate shared sources too — attack-bug641-round2.test.tsx
// really does writeFileSync src/components/demandFixUi.ts in place today.
test('B4 invariant: [RED] the derived scan must cover .test.tsx, not only .test.mjs', () => {
  const src = readFileSync(join(REPO, 'tools', 'scoped-runner-attack.mjs'), 'utf8');
  const block = /every src-mutating test is on FILE_MUTATING_TEST_BASENAMES[\s\S]*?\n\}\);/.exec(src)[0];
  assert.ok(/test\.tsx/.test(block),
    'the completeness invariant only scans *.test.mjs; webconsole/test/attack-bug641-round2.test.tsx ' +
    'writeFileSyncs src/components/demandFixUi.ts in place and is invisible to it');
});

// [RED] and the partition itself is mjs-only: tsx groups are never partitioned.
test('B5 partition: [RED] tsx groups must also be partitioned for in-place mutators', () => {
  assert.ok(/partitionMutating\(tsx/.test(RUNNER_SRC) || /tsx[\s\S]{0,400}partitionMutating/.test(RUNNER_SRC),
    'main() calls partitionMutating only on nodeTargets; a tsx group containing an in-place mutator ' +
    '(attack-bug641-round2.test.tsx) runs at full concurrency alongside every other tsx test');
});

// ─────────────── C. NODE_TEST_CONTEXT DEPTH ──────────────────────────────────

test('C1 env: the root BUG-546 protection still holds (discovery = env + empty argv)', () => {
  const res = spawnSync(process.execPath, ['--test', 'tools/test/scoped.mjs'],
    { cwd: REPO, encoding: 'utf8', timeout: 60000 });
  assert.equal(res.status, 0, 'root discovery must still no-op green');
});

test('C2 env: an explicit invocation runs for real at EVERY depth (parent + child both hostile)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'scoped-env-'));
  const f = join(dir, 'boom.test.mjs');
  writeFileSync(f, "import { test } from 'node:test';\ntest('boom', () => { throw new Error('x'); });\n", 'utf8');
  try {
    const res = run([f, '--timeout=60'], { env: { NODE_TEST_CONTEXT: 'child-v8' } });
    assert.notEqual(res.status, 0, 'must not vacuously pass under an inherited NODE_TEST_CONTEXT');
    assert.ok(/RESULT: FAIL/.test(outOf(res)), 'must actually have run the failing target');
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

// Documents (does not assert a fix on scoped.mjs) that stripping is ONE level
// only: node re-sets NODE_TEST_CONTEXT for each test file it executes, so a
// GRANDCHILD spawned BY a test file still inherits it and self-skips unless the
// test strips it itself. This is why every mutating RED-proof has to delete the
// var by hand (bug645-population-visibility.test.mjs does).
test('C3 env: a grandchild node --test spawned from inside a test file self-skips unless the test strips the var', () => {
  const dir = mkdtempSync(join(tmpdir(), 'scoped-gc-'));
  const gc = join(dir, 'grandchild.test.mjs');
  writeFileSync(gc, "import { test } from 'node:test';\ntest('gc fails', () => { throw new Error('grandchild ran'); });\n", 'utf8');
  const parent = join(dir, 'parent.test.mjs');
  writeFileSync(parent, `
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
test('inherited env silently no-ops the grandchild', () => {
  const inherited = spawnSync(process.execPath, ['--test', ${JSON.stringify(gc)}], { encoding: 'utf8' });
  const stripped = (() => { const e = { ...process.env }; delete e.NODE_TEST_CONTEXT;
    return spawnSync(process.execPath, ['--test', ${JSON.stringify(gc)}], { encoding: 'utf8', env: e }); })();
  assert.equal(process.env.NODE_TEST_CONTEXT !== undefined, true, 'precondition: test file itself has the var');
  assert.equal(inherited.status, 0, 'inherited: grandchild vacuously exits 0');
  assert.notEqual(stripped.status, 0, 'stripped: grandchild really runs and fails');
});
`, 'utf8');
  try {
    const res = run([parent, '--timeout=120']);
    assert.equal(res.status, 0, `grandchild-depth probe:\n${outOf(res)}`);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

// ─────────────── D. STALE-.bak SWEEP COVERAGE ────────────────────────────────

function plantBakAndRun(absBakPath) {
  mkdirSync(dirname(absBakPath), { recursive: true });
  writeFileSync(absBakPath, '// planted by scoped-runner-reround-attack.mjs\n', 'utf8');
  const dir = mkdtempSync(join(tmpdir(), 'scoped-bak-'));
  const f = join(dir, 'trivial.test.mjs');
  writeFileSync(f, "import { test } from 'node:test';\ntest('t', () => {});\n", 'utf8');
  try {
    const res = run([f, '--timeout=60']);
    return { status: res.status, out: outOf(res) };
  } finally { rmSync(absBakPath, { force: true }); rmSync(dir, { recursive: true, force: true }); }
}

test('D1 sweep: a suffixed .bak in src/sim is caught with a CORRECT restore command (control)', () => {
  const bak = join(REPO, 'webconsole', 'src', 'sim', 'data.ts.zzreround.bak');
  const r = plantBakAndRun(bak);
  assert.notEqual(r.status, 0, 'must fail the run');
  assert.ok(/likely real file: .*[\\/]data\.ts$/m.test(r.out), `restore target must be data.ts; got:\n${r.out}`);
});

// [RED] the plain `<file>.bak` shape — the exact shape BUG-708's own comment
// cites ("one stale data.ts.bak") and the shape a dozen test headers document
// (`cp src/sim/data.ts src/sim/data.ts.bak`, `engine.ts.bak`, `store.tsx.bak`).
test('D2 sweep: [RED] a suffix-less data.ts.bak must not emit a WRONG, destructive restore command', () => {
  const bak = join(REPO, 'webconsole', 'src', 'sim', 'data.ts.bak');
  const r = plantBakAndRun(bak);
  assert.notEqual(r.status, 0, 'must fail the run');
  assert.ok(!/likely real file: .*[\\/]data$/m.test(r.out),
    'realFileForBak() strips the LAST dot segment unconditionally, so data.ts.bak resolves to "data": ' +
    'the printed command `mv "…/data.ts.bak" "…/data"` renames away the only good copy AND leaves the ' +
    `sabotaged data.ts live. Output:\n${r.out}`);
  assert.ok(/likely real file: .*[\\/]data\.ts$/m.test(r.out), 'should resolve to data.ts');
});

// [RED] the sweep scans exactly one directory, non-recursively.
test('D3 sweep: [RED] a stranded .bak outside src/sim (src/components) must also be caught', () => {
  const bak = join(REPO, 'webconsole', 'src', 'components', 'demandFixUi.ts.attack-r2.bak');
  const r = plantBakAndRun(bak);
  assert.notEqual(r.status, 0,
    'webconsole/test/attack-bug641-round2.test.tsx mutates src/components/demandFixUi.ts in place and ' +
    'strands demandFixUi.ts.attack-r2.bak there on a SIGKILL — findStaleBakFiles() only readdirSyncs ' +
    'webconsole/src/sim, so this is invisible and the run proceeds against sabotaged source');
});

// [RED] the sweep is pre-run only; a group killed by the watchdog strands a
// mutation that every LATER group in the same invocation then runs against.
test('D4 sweep: [RED] the sweep must also run BETWEEN groups, not only before the first spawn', () => {
  // NB: count STATEMENT call sites only. Matching the bare identifier counts
  // the declaration and a comment mention too and makes this check vacuous —
  // exactly the trap the rework itself nearly shipped.
  const calls = [...RUNNER_SRC.matchAll(/^[ \t]*failOnStaleBakFiles\(\);/gm)].length;
  assert.ok(calls >= 2,
    'failOnStaleBakFiles() is invoked once, before any spawn. --webconsole-ci runs parallel-node -> ' +
    'serial-mutating-node -> tsx sequentially; if the SERIAL (mutating) group is SIGKILLed at the ' +
    'watchdog ceiling it strands a sabotaged src/sim file, and the tsx group then runs against it ' +
    'inside the SAME invocation with no re-check. (A re-check must be gated so it never fires on the ' +
    'owning group\'s own live .bak — i.e. only AFTER a group has exited.)');
});
