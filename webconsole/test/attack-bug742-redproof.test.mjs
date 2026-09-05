import { test, describe } from 'node:test';
import { fileURLToPath } from 'node:url';
import { join, dirname, resolve } from 'node:path';
import { mkdirSync } from 'node:fs';
const HERE = dirname(fileURLToPath(import.meta.url));
// In-tree TEMP so the mutant shadow resolves lz-string; the directory must exist
// before mutant.mjs uses it (the lead's port found a fresh checkout has no
// .mutant-tmp and every mutation reported as a 3ms crash) - create it here, never
// rely on the machine. Gitignored via webconsole/.gitignore.
process.env.TEMP = resolve(HERE, '..', '.mutant-tmp');
process.env.TMP = process.env.TEMP;
mkdirSync(process.env.TEMP, { recursive: true });
const { runMutantSelfReinvoke } = await import('../testsupport/mutant.mjs');
const LANE = join(HERE, 'bug742-capacity-failclosed.test.mjs');
const B736 = join(HERE, 'attack-bug736-round.test.mjs');
const MINE = join(HERE, 'attack-bug742-round.test.mjs');
const MINE2 = join(HERE, 'attack-bug742-r2.test.mjs');

const eng = {
  M1_group: (s) => s.replace('if (!Number.isFinite(groupCapacityReal)) {', 'if (false) {'),
  M2_gain: (s) => s.replace('if (!Number.isFinite(capacityGain)) {', 'if (false) {'),
  M3_ceil3: (s) => s.replace('if (!Number.isFinite(groupCapacityApprox) || !Number.isFinite(familyTotalBefore)) {', 'if (false) {'),
  M5_undo_log0: (s) => s.replace('const undoIndex = log.findIndex((p) => p.transactions.length > 0);', 'const undoIndex = log.length > 0 ? 0 : -1;'),
  M6_reset_any: (s) => s.replace('consolidatorPassLogs.some((p) => p.transactions.length > 0) ? false', 'consolidatorPassLogs.length > 0 ? false'),
};

describe('R2 RED-PROOFS', () => {
  for (const [name, mutate] of Object.entries(eng)) {
    for (const [label, file] of [['lane742', LANE], ['bug736', B736]]) {
      test(`${name} vs ${label}`, () => {
        const r = runMutantSelfReinvoke({ targetRelPath: 'sim/engine.ts', mutate, testFileAbsPath: file, timeoutMs: 500000 });
        console.log(`RP ${name} ${label}: detected=${r.failed && !r.crashed} crashed=${r.crashed}`);
      });
    }
  }
  test('M7 data.ts coercion neutered vs lane742 + mine', () => {
    for (const [label, file] of [['lane742', LANE], ['mine', MINE], ['mine2', MINE2]]) {
      const r = runMutantSelfReinvoke({
        targetRelPath: 'sim/data.ts',
        mutate: (s) => s.replace('  if (coerced === raw) return b;', '  if (true) return b;'),
        testFileAbsPath: file, timeoutMs: 500000,
      });
      console.log(`RP M7 ${label}: detected=${r.failed && !r.crashed} crashed=${r.crashed}`);
    }
  });
  test('M8 replay.ts decode boundary un-wired vs lane742 + mine', () => {
    for (const [label, file] of [['lane742', LANE], ['mine', MINE], ['mine2', MINE2]]) {
      const r = runMutantSelfReinvoke({
        targetRelPath: 'sim/replay.ts',
        mutate: (s) => s.replace('    snap.buildings = coerceSnapshotBuildings(snap.buildings) as unknown[];', '    void coerceSnapshotBuildings;'),
        testFileAbsPath: file, timeoutMs: 500000,
      });
      console.log(`RP M8 ${label}: detected=${r.failed && !r.crashed} crashed=${r.crashed}`);
    }
  });
});
