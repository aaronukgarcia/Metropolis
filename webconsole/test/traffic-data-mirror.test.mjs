// traffic-data-mirror.test.mjs — BUG-860: the committed mirror
// webconsole/src/sim/traffic-data/ must equal the SSOT data/traffic/*.json +
// data/traffic.json byte-for-byte (GR#3: duplication only with validation).
// MUTANT: edit one byte of any mirrored file (or of its SSOT twin) without
// re-running scripts/sync-traffic-data.mjs -> this reds naming the file.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import * as fsSync from 'node:fs';
import { mirrorPairs, orphans } from '../scripts/sync-traffic-data.mjs';

test('BUG-860: every traffic table has a byte-identical mirror under src/sim/traffic-data', () => {
  const pairs = mirrorPairs();
  assert.ok(pairs.length >= 2, 'mirror set derived from data/traffic must not be empty');
  for (const { src, dst, name } of pairs) {
    assert.ok(existsSync(dst), `mirror missing for ${name}: run node webconsole/scripts/sync-traffic-data.mjs`);
    assert.ok(readFileSync(src).equals(readFileSync(dst)), `mirror STALE for ${name}: run node webconsole/scripts/sync-traffic-data.mjs`);
  }
});

test('BUG-860: no orphan mirror (a table deleted from data/traffic must not linger under traffic-data)', () => {
  // MUTANT: drop a zz_orphan.json into src/sim/traffic-data without a data/ twin -> this reds naming it.
  assert.deepEqual(orphans(), [], 'orphan mirrors present: run node webconsole/scripts/sync-traffic-data.mjs');
});


test('BUG-860 class guard: no module under webconsole/src imports outside the webconsole root via ../../../data/', () => {
  // MUTANT: add `import x from '../../../data/anything.json'` to any src module -> this reds naming it.
  // Out-of-root imports resolve in a full checkout but not in the shadow copies
  // testsupport/mutant.mjs makes of webconsole/src (CI run 34402167631).
  const srcRoot = new URL('../src/', import.meta.url);
  const { readdirSync, statSync } = fsSync;
  const offenders = [];
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      const full = new URL(name + (statSync(new URL(name, dir)).isDirectory() ? '/' : ''), dir);
      if (full.pathname.endsWith('/')) { walk(full); continue; }
      if (!/\.(ts|tsx|mjs|js)$/.test(name)) continue;
      const text = readFileSync(full, 'utf8');
      const m = text.match(/from\s+['"](\.\.\/){3,}data\//);
      if (m) offenders.push(full.pathname.split('/src/')[1] + ': ' + m[0]);
    }
  };
  walk(srcRoot);
  assert.deepEqual(offenders, [], 'out-of-root data imports (mirror them via scripts/sync-traffic-data.mjs instead)');
});
