// attack-price-rail-m20-round.test.mjs — Independent DESTRUCTIVE round (GR#23)
// against FEAT-2326609782 (m20/rail realistic pricing + genesis-free upkeep
// exemption). Attacker is NOT the author. Findings recorded here; verdict
// recorded separately via claude-bow.js destructive.
//
// SCOPE COVERED (see also manual mutation/probe evidence in the round report,
// not re-encoded as tests where it required temporarily mutating source
// files via scratch copy per GR#24):
//   1. builtTick exemption boundary (tick-0 reachability, legacy-save shape)
//   2. UPKEEP_BUCKET completeness across every spec with nonzero upkeep
//   3. player/autoConnect/paste creation paths all stamp a real builtTick

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, upkeepChargeableOf } from '../src/sim/data.ts';
import { UPKEEP_BUCKET } from '../src/sim/fiscal.ts';

// -----------------------------------------------------------------------
// FINDING (adjudicated NOT EXPLOITABLE, verified structurally): tick-0
// placements. initialState() = advance(rawState()) always returns tick===1
// as the first playable state — rawState()'s own tick:0 is never externally
// observable (rawState is unexported and used nowhere else). replayFromGenesis
// also starts from initialState(), so no journal entry ever applies to a
// tick:0 state either. A player-placed m20/rail tile therefore ALWAYS gets
// builtTick >= 1, never 0 — the builtTick<=0 boundary's "<=" (vs strict "<")
// is exercised ONLY by genesis tiles (builtTick undefined -> ?? 0 -> 0), not
// by any reachable player action. This test pins that invariant so a future
// change (e.g. an initialState() that stops pre-advancing, or a new bulk
// action that stamps a building at literal tick 0) is caught if it ever
// makes tick 0 reachable for a real placement.
test('ATTACK builtTick boundary: initialState() is never observably at tick 0, so a player placement can never land builtTick===0', () => {
  const s = initialState();
  assert.ok(s.tick >= 1, 'initialState always pre-advances past tick 0');
  const placed = reducer({ ...s, unlockedAll: true, funds: 200_000_000 }, { type: 'place', spec: 'm20', x: 210, y: 210 });
  const tile = placed.buildings.find((b) => b.spec === 'm20' && b.x === 210 && b.y === 210);
  assert.ok(tile.builtTick > 0, 'a real player placement never stamps builtTick<=0 in practice');
});

// -----------------------------------------------------------------------
// FINDING (confirmed, not exploitable in this estate): the "<=0" boundary
// (vs a strict "<0") is load-bearing specifically BECAUSE genesis tiles
// carry builtTick===undefined, which `?? 0` normalizes to exactly 0 — not
// to a negative sentinel. A predicate using strict "<0" would WRONGLY charge
// genesis furniture (proven by manual mutation in the round: flipping
// `<=0` to `<0` made genesis upkeep jump from £0 to £110,000/tick instead of
// staying £0 — regression pinned here).
test('ATTACK builtTick boundary: the <=0 (not <0) inclusive boundary is required because genesis builtTick is undefined, not negative', () => {
  const s = initialState();
  const m20Tiles = s.buildings.filter((b) => b.spec === 'm20');
  assert.ok(m20Tiles.every((b) => b.builtTick === undefined), 'sanity: genesis m20 tiles have NO builtTick field at all (not 0, not negative)');
  const strictLessThanZero = (b, sp) => ((b.builtTick ?? 0) < 0 ? 0 : sp.upkeep);
  const naiveTotal = m20Tiles.reduce((sum, b) => sum + strictLessThanZero(b, SPECS.m20), 0);
  assert.ok(naiveTotal > 0, 'RED: a strict <0 boundary would WRONGLY charge genesis furniture (proves <=0 is necessary, not cosmetic)');
  const realTotal = m20Tiles.reduce((sum, b) => sum + upkeepChargeableOf(b, SPECS.m20), 0);
  assert.equal(realTotal, 0, 'GREEN: the real <=0 predicate correctly exempts genesis furniture');
});

// -----------------------------------------------------------------------
// FINDING (confirmed clean): every player-facing building-creation path
// (place, autoConnect connector-laying, road-line drag, clipboard paste)
// stamps a real builtTick. Only starterCity()'s locally-scoped `put()`
// helper omits it, and that helper is never reachable outside starterCity().
// -----------------------------------------------------------------------
test('ATTACK creation-path audit: clone-stamp (stampRegion) of an m20 tile stamps a real builtTick (not exempt)', () => {
  let s = { ...initialState(), unlockedAll: true, funds: 200_000_000 };
  const clip = { items: [{ spec: 'm20', dx: 0, dy: 0 }] };
  const pasted = reducer(s, { type: 'stampRegion', clipboard: clip, x: 220, y: 220 });
  const newTile = pasted.buildings.find((b) => b.spec === 'm20' && b.x === 220 && b.y === 220);
  assert.ok(newTile, 'stampRegion actually placed the m20 tile');
  assert.ok((newTile.builtTick ?? 0) > 0, 'stamped m20 tile is fully chargeable, not exempt');
  assert.equal(upkeepChargeableOf(newTile, SPECS.m20), SPECS.m20.upkeep);
});

// -----------------------------------------------------------------------
// FINDING (confirmed clean): UPKEEP_BUCKET has no other silent gap — every
// spec with nonzero upkeep has a bucket entry (grepped exhaustively).
// -----------------------------------------------------------------------
test('ATTACK bucket completeness: no spec with nonzero upkeep is missing a UPKEEP_BUCKET entry', () => {
  const missing = Object.entries(SPECS).filter(([, sp]) => sp.upkeep > 0 && !UPKEEP_BUCKET[sp.kind]);
  assert.deepEqual(missing.map(([id]) => id), [], 'every upkeep-bearing spec kind has a bucket, m20/rail included');
});

// -----------------------------------------------------------------------
// FINDING (confirmed, tripwire proven live in the round via scratch-copy
// mutation of engine.ts only, not committed here): mutating ONE of the two
// upkeepChargeableOf consumers (engine.computeFlows) while leaving
// consistency.ts's independent recompute on the real predicate produces a
// genuine `flows.upkeep-total-matches` divergence (measured: computed 117 vs
// actual 110117 on a fresh genesis city) — the BUG-628-class conservation
// seam genuinely screams on drift, exactly as the estate claims.
// -----------------------------------------------------------------------
