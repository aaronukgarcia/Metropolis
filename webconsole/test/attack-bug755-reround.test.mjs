// attack-bug755-reround.test.mjs — INDEPENDENT RE-VERIFY ROUND (GR#23) of the
// author's close-out of the first round's P1 (lineage-pointer mismatch) and P2
// (unguarded recordError in the [boot] initializer).
// Attacker: opus-reverify-bug755 (NOT the author).
//
// Attacks the NEW code specifically:
//   * scanAllSavepointLineages must never THROW and never invent a lineage:
//     a bare Map-backed StorageLike (no length/key), a `key()` that throws,
//     a `key()` returning null, and every near-miss key family under the
//     same prefix (idbOnly, legacy+lineage forms, a non-numeric last
//     segment, a prefix that merely LOOKS like the savepoint prefix).
//   * it must work against a REAL Storage implementation (jsdom), which is
//     the only place the `.call(storage, i)` binding matters — plus a
//     RED-PROOF that detaching the method reduces it to a vacuous [].

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { runWithMutant } from '../testsupport/mutant.mjs';

/** A storage double that DOES expose Storage-like enumeration over a Map. */
function enumerableStorage(entries) {
  const m = new Map(entries);
  return {
    get length() { return m.size; },
    key(i) { return Array.from(m.keys())[i] ?? null; },
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, v); },
    removeItem: (k) => { m.delete(k); },
  };
}

test('REVERIFY 1: scanAllSavepointLineages degrades to [] (never throws) on hostile storages', async () => {
  const { scanAllSavepointLineages } = await import('../src/sim/replay.ts');

  // (a) the bare Map double used by most tests in this repo — no length/key.
  const bare = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
  assert.deepEqual(scanAllSavepointLineages(bare), [], 'a storage with no enumeration must degrade, not throw');

  // (b) length present but key() throws on every call.
  const throwing = { length: 3, key() { throw new Error('Illegal invocation'); }, getItem: () => null, setItem: () => {}, removeItem: () => {} };
  assert.deepEqual(scanAllSavepointLineages(throwing), [], 'a throwing key() must be swallowed into []');

  // (c) key() returns null for every index (a legal Storage return).
  const nullKeys = { length: 4, key: () => null, getItem: () => null, setItem: () => {}, removeItem: () => {} };
  assert.deepEqual(scanAllSavepointLineages(nullKeys), [], 'null keys must be skipped, not dereferenced');

  // (d) length is not a number (a wrapper exposing it as a getter-less field).
  const badLen = { length: '3', key: () => 'metropolis.savepoint.0', getItem: () => null, setItem: () => {}, removeItem: () => {} };
  assert.deepEqual(scanAllSavepointLineages(badLen), [], 'a non-numeric length must be rejected up front');
});

test('REVERIFY 2: near-miss keys never invent a lineage, and real keys are classified correctly', async () => {
  const { scanAllSavepointLineages, LEGACY_LINEAGE_ID } = await import('../src/sim/replay.ts');

  const noise = enumerableStorage([
    ['metropolis.savepointX.0', '{}'],            // prefix look-alike, no dot
    ['metropolis.savepoint.idbOnly', '{}'],       // legacy IDB overflow marker
    ['metropolis.savepoint.abc.idbOnly', '{}'],   // lineage IDB overflow marker
    ['metropolis.savepoint.meta', '{}'],          // non-numeric last segment
    ['metropolis.savepoint', '{}'],               // the bare prefix itself
    ['metropolis.journal.0', '{}'],               // an unrelated family
    ['', '{}'],                                   // empty key
  ]);
  assert.deepEqual(scanAllSavepointLineages(noise), [], `no near-miss key may be read as a savepoint: ${JSON.stringify(scanAllSavepointLineages(noise))}`);

  const real = enumerableStorage([
    ['metropolis.savepoint.0', '{}'],                 // legacy, unnamespaced
    ['metropolis.savepoint.lin-A.1', '{}'],           // ordinary lineage
    ['metropolis.savepoint.a.b.c.2', '{}'],           // lineage id containing dots
    ['metropolis.savepoint.lin-A.idbOnly', '{}'],     // must not add a second entry
  ]);
  const found = scanAllSavepointLineages(real).sort();
  assert.deepEqual(found, [LEGACY_LINEAGE_ID, 'a.b.c', 'lin-A'].sort(), `lineage classification wrong: ${JSON.stringify(found)}`);
});

test('REVERIFY 3: the scan works against a REAL Storage (jsdom localStorage)', async () => {
  const { scanAllSavepointLineages } = await import('../src/sim/replay.ts');
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
  try {
    const ls = dom.window.localStorage;
    ls.setItem('metropolis.savepoint.lin-real.0', '{}');
    ls.setItem('metropolis.savepoint.idbOnly', '{}');
    ls.setItem('unrelated', 'x');
    const found = scanAllSavepointLineages(ls);
    assert.deepEqual(found, ['lin-real'], `a real Storage must be enumerable by the scan: ${JSON.stringify(found)}`);
  } finally {
    dom.window.close();
  }
});

test('REVERIFY 4 RED-PROOF: detaching key() from the Storage makes the scan vacuously empty on a real Storage', () => {
  const out = runWithMutant({
    targetRelPath: 'sim/replay.ts',
    mutate: (src) => {
      const needle = 'const key = keyFn.call(storage, i);';
      if (!src.includes(needle)) throw new Error('RED-PROOF setup broken: the bound .call site was not found');
      return src.replace(needle, 'const key = keyFn(i);');
    },
    childBody: `
      const { JSDOM } = await import('jsdom');
      const { scanAllSavepointLineages } = await import('./sim/replay.ts');
      const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
      const ls = dom.window.localStorage;
      ls.setItem('metropolis.savepoint.lin-real.0', '{}');
      console.log('DETACHED_FOUND:' + JSON.stringify(scanAllSavepointLineages(ls)));
      dom.window.close();
    `,
  });
  assert.match(out, /DETACHED_FOUND:\[\]/, `the detached-call mutant must reduce the scan to a vacuous [] — this is the exact first-draft defect: ${out}`);
});
