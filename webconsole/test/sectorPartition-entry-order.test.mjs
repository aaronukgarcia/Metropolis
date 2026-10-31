// sectorPartition-entry-order.test.mjs — FEAT-2326609764 inc2, BUG-1051
// regression guard.
//
// BUG-1051 (CI red after inc1 landed, 2026-09-11): sectorPartition.ts read
// `TILE_METRES` from consolidator.ts at its own MODULE TOP LEVEL, across the
// real consolidator.ts <-> data.ts <-> sectorPartition.ts import cycle this
// file's own header documents. A top-level binding read across a cycle is
// entry-order-sensitive: whichever module a given process happens to import
// FIRST determines which side of the cycle is still in its temporal-dead-zone
// when the read happens — the exact "ReferenceError: Cannot access X before
// initialization" class this project has already hit once for
// CONNECT_EXEMPT_KINDS/TICKS_PER_MONTH (consolidator.ts's own header comment
// on that incident). The fix: TILE_METRES now lives in grid.ts, a deliberate
// ZERO-IMPORT leaf (consolidator.ts re-exports it, mirroring its existing
// MAP_W/MAP_H pattern) — reading a constant from a leaf can never be
// entry-order-sensitive, because a leaf has nothing else to be mid-evaluation
// of.
//
// This test proves the fix holds by actually varying the entry order in a
// FRESH PROCESS per case (an in-process re-import would hit Node's module
// cache and prove nothing — the bug only manifests on a cold module graph).
// Every one of the four orders below must print LOADED with exit code 0;
// any TDZ ReferenceError is a real regression of BUG-1051's class, not a
// flake.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const execFileAsync = promisify(execFile);
const webconsoleDir = join(dirname(fileURLToPath(import.meta.url)), '..');

/**
 * Spawns a fresh `node --import tsx/esm` process that imports the given
 * module specifiers, in order, then prints LOADED. Returns the resolved
 * stdout on success; throws (with stderr attached) on any crash — a TDZ
 * ReferenceError from BUG-1051's class throws synchronously during module
 * evaluation, well before the `console.log('LOADED')` line ever runs.
 */
async function assertLoadsInOrder(t, label, specifiers) {
  const imports = specifiers.map((s) => `import ${JSON.stringify(s)};`).join(' ');
  const code = `${imports} console.log('LOADED');`;
  try {
    const { stdout } = await execFileAsync(
      process.execPath,
      ['--import', 'tsx/esm', '--input-type=module', '-e', code],
      { cwd: webconsoleDir, timeout: 30_000 },
    );
    assert.match(stdout, /LOADED/, `${label}: expected LOADED in stdout, got: ${stdout}`);
  } catch (err) {
    assert.fail(
      `${label}: fresh-process import order ${JSON.stringify(specifiers)} crashed (BUG-1051 class regression) — ${err.stderr ?? err.message}`,
    );
  }
}

test('BUG-1051 entry order: consolidator.ts imported first, then data.ts, then sectorPartition.ts', async (t) => {
  await assertLoadsInOrder(t, 'consolidator-first', [
    './src/sim/consolidator.ts',
    './src/sim/data.ts',
    './src/sim/sectorPartition.ts',
  ]);
});

test('BUG-1051 entry order: engine.ts imported first (pulls the whole cycle in via its own imports)', async (t) => {
  await assertLoadsInOrder(t, 'engine-first', [
    './src/sim/engine.ts',
    './src/sim/data.ts',
    './src/sim/sectorPartition.ts',
  ]);
});

test('BUG-1051 entry order: data.ts imported first, then consolidator.ts, then sectorPartition.ts', async (t) => {
  await assertLoadsInOrder(t, 'data-first', [
    './src/sim/data.ts',
    './src/sim/consolidator.ts',
    './src/sim/sectorPartition.ts',
  ]);
});

test('BUG-1051 entry order: sectorPartition.ts (this increment\'s own module) imported FIRST, before anything else in the cycle', async (t) => {
  await assertLoadsInOrder(t, 'sectorPartition-first', [
    './src/sim/sectorPartition.ts',
    './src/sim/consolidator.ts',
    './src/sim/data.ts',
    './src/sim/engine.ts',
  ]);
});
