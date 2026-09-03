// attack-bug606-roadtopo.test.mjs — PROMOTED from webconsole/attack/
// atk-roadtopo.test.mjs (independent round r2, Aaron 2026-09-03: "promote
// the attacker's regressions into test/ ... so CI carries them"). Extension
// kept as .mjs — imports (engine.ts/data.ts/genesisReplay.ts) are all
// explicit-extension, no chain through demandFixUi.ts's extensionless
// internal imports, so plain `node --test` resolves it fine (confirmed by
// direct invocation before promotion). Content UNCHANGED from the original.
//
// ATTACK — roadTopologyMayHaveChanged handling in resolveDemandAll (BUG-566 class).
// After the action, the live state's cached roadConnectivity must equal a fresh
// recompute; if the flag was dropped/overwritten the cache goes stale.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';

const ticks = (n) => Array.from({ length: n }, () => ({ type: 'tick' }));

function grow(funds) {
  let s = initialState();
  for (const a of [
    { type: 'debugFunds', amount: 5_000_000 },
    { type: 'unlockAll' },
    { type: 'place', spec: 'res_hut', x: 5, y: 5 },
    { type: 'place', spec: 'res_hut', x: 7, y: 5 },
    { type: 'place', spec: 'res_hut', x: 9, y: 5 },
    ...ticks(60),
    { type: 'debugFunds', amount: -5_000_000 },
    { type: 'debugFunds', amount: funds },
  ]) s = reducer(s, a);
  return s;
}

for (const funds of [0, 25_000, 250_000, 5_000_000, 100_000_000]) {
  test(`ATTACK: roadConnectivity is fresh after resolveDemandAll (funds=${funds})`, () => {
    const s = grow(funds);
    const after = reducer(s, { type: 'resolveDemandAll' });
    assert.equal(
      stableStringify(after.roadConnectivity),
      stableStringify(computeRoadConnectivity(after)),
      `STALE roadConnectivity after resolveDemandAll at funds=${funds}`
    );
  });
  test(`ATTACK: roadConnectivity is fresh after resolveDemand (funds=${funds})`, () => {
    const s = grow(funds);
    const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
    assert.equal(
      stableStringify(after.roadConnectivity),
      stableStringify(computeRoadConnectivity(after))
    );
  });
}
