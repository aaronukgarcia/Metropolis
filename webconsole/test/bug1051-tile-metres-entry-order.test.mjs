// BUG-1051 — sectorPartition.ts must initialise cleanly whichever module is
// the entry point. CI run 34570495115 (node-test shard 2) went red after the
// FEAT-2326609764 inc1 landing: three consolidator suites imported
// consolidator.ts FIRST, consolidator.ts imports data.ts, data.ts imports
// sectorPartition.ts, and sectorPartition.ts read TILE_METRES from
// consolidator.ts at module top level — "Cannot access 'TILE_METRES' before
// initialization". The fix hosts TILE_METRES in the dependency-free leaf
// grid.ts (consolidator.ts re-exports it). This test spawns a FRESH process
// per entry order (module-graph state is per process, so an in-process
// dynamic import could not reproduce the failure) and asserts every order
// loads and agrees on the constant's identity.
import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const cwd = path.join(HERE, '..');

const ENTRY_ORDERS = [
  ['consolidator-first', ['./src/sim/consolidator.ts', './src/sim/sectorPartition.ts', './src/sim/data.ts']],
  ['consolidatorGlide-first', ['./src/sim/consolidatorGlide.ts', './src/sim/sectorPartition.ts']],
  ['sectorPartition-first', ['./src/sim/sectorPartition.ts', './src/sim/consolidator.ts']],
  ['data-first', ['./src/sim/data.ts', './src/sim/consolidator.ts', './src/sim/sectorPartition.ts']],
];

for (const [label, order] of ENTRY_ORDERS) {
  test(`BUG-1051 entry order ${label}: no TDZ, one TILE_METRES, SECTOR_TILES derived from it`, () => {
    const imports = order.map((m, i) => `const m${i} = await import('${m}');`).join(' ');
    const script =
      imports +
      " const c = await import('./src/sim/consolidator.ts'); const g = await import('./src/sim/grid.ts'); const p = await import('./src/sim/sectorPartition.ts');" +
      " if (c.TILE_METRES !== g.TILE_METRES) throw new Error('consolidator re-export diverged from grid.ts');" +
      " if (!Number.isInteger(p.SECTOR_TILES) || p.SECTOR_TILES !== Math.round(p.SECTOR_METRES / g.TILE_METRES)) throw new Error('SECTOR_TILES not derived from TILE_METRES');" +
      " console.log('LOADED ' + g.TILE_METRES + ' ' + p.SECTOR_TILES);";
    const out = execFileSync(process.execPath, ['--import', 'tsx/esm', '--input-type=module', '-e', script], {
      cwd,
      encoding: 'utf8',
      timeout: 120000,
    });
    assert.match(out, /LOADED \d+ \d+/, `${label}: the module graph must evaluate without a TDZ error`);
  });
}

test('BUG-1051 structural: sectorPartition.ts never imports from consolidator.ts', async () => {
  const fs = await import('node:fs');
  const src = fs.readFileSync(path.join(cwd, 'src/sim/sectorPartition.ts'), 'utf8');
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
  assert.doesNotMatch(code, /from\s+['"]\.\/consolidator(?:\.ts)?['"]/, 'sectorPartition.ts must import the grid leaf, never consolidator.ts');
  assert.match(code, /TILE_METRES[^;]*from\s+['"]\.\/grid\.ts['"]/, 'TILE_METRES must come from grid.ts');
});

// ---------------------------------------------------------------------------
// Round opus-round-bug1051 addition. The four entry-order tests above and the
// structural pin all hold ONLY because grid.ts is a genuine leaf — its own
// header states the invariant outright ("a deliberate LEAF: zero imports, so
// every other sim module can import it with no import-cycle risk at all").
// That invariant was UNPINNED: the round mutated grid.ts to add a real,
// non-cycling value import (`import './throttle.ts';`) and every suite above
// stayed GREEN, i.e. nothing in the tree would notice the leaf quietly
// growing an edge. It only reds once that edge happens to close a cycle back
// to grid.ts — by which time the TDZ is back and the diagnosis costs another
// CI-red day (BUG-1051 itself). Pin the property, not just its consequence.
test('BUG-1051 structural: grid.ts is a LEAF — zero imports of any kind', async () => {
  const fs = await import('node:fs');
  const src = fs.readFileSync(path.join(cwd, 'src/sim/grid.ts'), 'utf8');
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
  // Any static import/export-from, bare side-effect import, dynamic import()
  // or require() would make grid.ts a non-leaf. TYPE-only imports are erased
  // at runtime but are still banned here: the header's invariant is "zero
  // imports", and a type import is the usual first step to a value one.
  assert.doesNotMatch(code, /\bfrom\s*['"]/, 'grid.ts must not import from any module');
  assert.doesNotMatch(code, /^\s*import\s*['"]/m, 'grid.ts must not carry a bare side-effect import');
  assert.doesNotMatch(code, /\bimport\s*\(/, 'grid.ts must not use a dynamic import()');
  assert.doesNotMatch(code, /\brequire\s*\(/, 'grid.ts must not use require()');
});
