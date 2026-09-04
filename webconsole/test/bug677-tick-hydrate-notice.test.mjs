// BUG-677 regression: the AC-31 over-cap notice must fire on LOAD hydrates
// only, never on worker-TICK hydrates. The web worker delivers every applied
// tick as `{type:'hydrate', source:'tick'}` (store.tsx worker.onmessage), so
// before the fix the once-per-load scan re-stamped placeNotice every second —
// Aaron's "This save has 23 × Five Gorges Dam" popup could not be dismissed
// (2026-09-04). The scan also cost an O(buildings) countOfSpec sweep per
// capped spec per tick, which this gate removes from the hot path.
//
// Both directions are asserted so the test can actually fail either way:
// a mutation that drops the source gate re-fires the notice on 'tick' (fails
// the first assert); a mutation that skips the scan entirely goes silent on
// load (fails the second).
import test from 'node:test';
import assert from 'node:assert/strict';
import { reducer, initialState } from '../src/sim/engine.ts';
import { SPECS, countOfSpec } from '../src/sim/data.ts';

function stateWithOverCap() {
  const capped = Object.values(SPECS).find((sp) => sp.maxPerCity === 1);
  assert.ok(capped, 'fixture precondition: a maxPerCity:1 spec must exist');
  const s = initialState();
  // Two instances of a one-per-city spec — the AC-31 over-cap shape. Placed
  // directly into state (as an old save would carry them), not via the
  // reducer, which would refuse the second.
  const mk = (id, x) => ({
    id,
    spec: capped.id,
    x,
    y: 2,
    builtTick: 0,
  });
  return { ...s, buildings: [...s.buildings, mk(900001, 2), mk(900002, 30)], placeNotice: null };
}

test('BUG-677: a tick-sourced hydrate never stamps the surplus-purge notice, and never purges', () => {
  const over = stateWithOverCap();
  const capped = Object.values(SPECS).find((sp) => sp.maxPerCity === 1);
  const out = reducer(initialState(), { type: 'hydrate', state: over, source: 'tick' });
  assert.equal(out.placeNotice, null, 'tick hydrate must not set placeNotice (this is the undismissable-popup bug)');
  // Aaron ruling 2026-09-04 (supersedes the old "none removed" AC-31 text):
  // a tick-sourced hydrate must not purge either — the O(buildings) scan is
  // gated on source !== 'tick' entirely (BUG-677), not just its notice.
  assert.equal(countOfSpec(out, capped.id), 2, 'tick hydrate must never purge surplus buildings — the gate gets the WHOLE scan, not just the notice');
});

test('BUG-677 guard: a load hydrate (default source) still fires the purge notice', () => {
  const over = stateWithOverCap();
  const capped = Object.values(SPECS).find((sp) => sp.maxPerCity === 1);
  const out = reducer(initialState(), { type: 'hydrate', state: over });
  assert.ok(out.placeNotice, 'load hydrate must still surface an honest notice');
  assert.match(out.placeNotice, new RegExp(`cap is ${capped.maxPerCity} per city`));
  assert.equal(countOfSpec(out, capped.id), capped.maxPerCity, 'load hydrate purges the surplus down to the cap');
});

test('BUG-677: dismissed notice STAYS dismissed across applied worker ticks', () => {
  const over = stateWithOverCap();
  // Load fires the notice…
  let s = reducer(initialState(), { type: 'hydrate', state: over });
  assert.ok(s.placeNotice);
  // …the player dismisses it (whatever clears placeNotice — model it directly)…
  s = { ...s, placeNotice: null };
  // …and three worker ticks later it must still be gone.
  for (let i = 0; i < 3; i++) {
    s = reducer(s, { type: 'hydrate', state: { ...s }, source: 'tick' });
  }
  assert.equal(s.placeNotice, null, 'worker ticks must never resurrect a dismissed notice');
});
