// aaron-channel-tunnel-footprint.test.mjs — Aaron ruling 2026-09-04 (verbatim):
// "the channel tunnel location needs to be bigger too".
//
// land_tunnel's footprint grew from 3x3 (9 tiles) to 6x4 (24 tiles) — see the
// PLACEHOLDER-tier disclosure comment beside the spec in data.ts for the
// reasoning. EXISTING placed tunnels must not suddenly overlap a neighbour
// that was legally placed adjacent to the OLD, smaller site: they are
// grandfathered at load via the SAME per-building footprintW/footprintH
// override the auto-scale ladder estates already use (footprintOf, data.ts;
// GR#3 — no parallel mechanism). NEW placements read the spec's current
// (bigger) dims straight through footprintOf's `?? sp.w/sp.h` fallback.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, footprintOf, LAND_TUNNEL_LEGACY_FOOTPRINT, TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH, stampTunnelFootprintGrandfather } from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

const TUNNEL = 'land_tunnel';
const TUNNEL_SPEC = SPECS[TUNNEL];

/** True if two footprint rectangles (tile coords) overlap at all. */
function rectsOverlap(a, b) {
  return a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;
}

function rectOf(building, sp) {
  const { w, h } = footprintOf(building, sp);
  return { x: building.x, y: building.y, w, h };
}

// ---------------------------------------------------------------------------
// The spec itself: bigger, and the legacy constant matches the OLD size.
// ---------------------------------------------------------------------------

test('land_tunnel is meaningfully bigger than before (area at least doubled)', () => {
  const OLD_AREA = LAND_TUNNEL_LEGACY_FOOTPRINT.w * LAND_TUNNEL_LEGACY_FOOTPRINT.h;
  const NEW_AREA = TUNNEL_SPEC.w * TUNNEL_SPEC.h;
  assert.equal(OLD_AREA, 9, 'the OLD footprint (grandfather target) was 3x3');
  assert.ok(NEW_AREA >= OLD_AREA * 2, `new footprint (${TUNNEL_SPEC.w}x${TUNNEL_SPEC.h}=${NEW_AREA}) must be at least double the old area (${OLD_AREA})`);
});

// ---------------------------------------------------------------------------
// Grandfathering: an old save's tunnel keeps its OLD footprint after load.
// ---------------------------------------------------------------------------

describe('load-time grandfather: existing tunnels keep the OLD footprint', () => {
  function legacySaveWithAdjacentNeighbour() {
    const genesis = initialState();
    // A tunnel placed under the OLD 3x3 rules at (10,10) — no footprintW/H
    // override, exactly as an old save serialized it. A neighbour placed
    // legally adjacent to that OLD 3x3 footprint, at x=13 (immediately to
    // the tunnel's right under 3x3; would overlap under the NEW 6x4 unless
    // grandfathered). `tunnelFootprintEpoch: 0` simulates a save that
    // predates this migration (mirrors bug652-jobs-grandfathering.test.mjs's
    // `economyEpoch: 0` convention) — initialState() itself always stamps
    // the CURRENT epoch, which a real old save would not carry.
    return {
      ...genesis,
      tunnelFootprintEpoch: 0,
      buildings: [
        ...genesis.buildings,
        { id: 600001, spec: TUNNEL, x: 10, y: 10, builtTick: 0 },
        { id: 600002, spec: 'res_hut', x: 13, y: 10, builtTick: 0 },
      ],
    };
  }

  test('control: WITHOUT the grandfather stamp, reading the tunnel at its NEW (bigger) footprint WOULD overlap the neighbour', () => {
    const snapshot = legacySaveWithAdjacentNeighbour();
    const tunnel = snapshot.buildings.find((b) => b.id === 600001);
    const neighbour = snapshot.buildings.find((b) => b.id === 600002);
    // Read at the spec's raw (current, bigger) dims directly — bypassing
    // footprintOf's override — to prove the overlap this fix prevents is
    // real, not a strawman.
    const naiveTunnelRect = { x: tunnel.x, y: tunnel.y, w: TUNNEL_SPEC.w, h: TUNNEL_SPEC.h };
    const neighbourRect = rectOf(neighbour, SPECS[neighbour.spec]);
    assert.ok(rectsOverlap(naiveTunnelRect, neighbourRect), 'precondition: the naive (ungrandfathered) reading really would overlap');
  });

  test('after stampTunnelFootprintGrandfather: the tunnel reads at its OLD 3x3 footprint and no longer overlaps the neighbour', () => {
    const snapshot = legacySaveWithAdjacentNeighbour();
    const stamped = stampTunnelFootprintGrandfather(snapshot);
    const tunnel = stamped.buildings.find((b) => b.id === 600001);
    const neighbour = stamped.buildings.find((b) => b.id === 600002);
    assert.equal(tunnel.footprintW, LAND_TUNNEL_LEGACY_FOOTPRINT.w);
    assert.equal(tunnel.footprintH, LAND_TUNNEL_LEGACY_FOOTPRINT.h);
    const tunnelRect = rectOf(tunnel, TUNNEL_SPEC);
    const neighbourRect = rectOf(neighbour, SPECS[neighbour.spec]);
    assert.equal(tunnelRect.w, 3, 'footprintOf must honour the stamped override, not the spec default');
    assert.equal(tunnelRect.h, 3);
    assert.ok(!rectsOverlap(tunnelRect, neighbourRect), 'grandfathered tunnel must not overlap its legally-adjacent neighbour');
  });

  test('wired into the real load path: the "hydrate" reducer case applies the stamp too', () => {
    const snapshot = legacySaveWithAdjacentNeighbour();
    const hydrated = reducer(initialState(), { type: 'hydrate', state: snapshot });
    const tunnel = hydrated.buildings.find((b) => b.id === 600001);
    assert.equal(tunnel.footprintW, 3);
    assert.equal(tunnel.footprintH, 3);
  });

  test('idempotent: stamping an already-stamped state a second time changes nothing', () => {
    const snapshot = legacySaveWithAdjacentNeighbour();
    const once = stampTunnelFootprintGrandfather(snapshot);
    const twice = stampTunnelFootprintGrandfather(once);
    assert.deepEqual(twice, once);
    assert.equal(twice, once, 'a true no-op returns the SAME object reference, not just a deep-equal copy');
  });

  test('determinism: pure function of state, no Date.now/Math.random — same input, byte-identical output', () => {
    const snapshot = legacySaveWithAdjacentNeighbour();
    const a = stampTunnelFootprintGrandfather(snapshot);
    const b = stampTunnelFootprintGrandfather(snapshot);
    assert.deepEqual(a, b);
  });
});

// ---------------------------------------------------------------------------
// New placements: use the NEW (bigger) footprint, and are never touched by
// the legacy-grandfather stamp even across a LATER hydrate.
// ---------------------------------------------------------------------------

describe('new placements occupy the NEW footprint and are never shrunk back', () => {
  test('a freshly placed tunnel reads at the current (bigger) spec footprint', () => {
    const s = { ...initialState(), unlockedAll: true, funds: TUNNEL_SPEC.cost * 10 };
    const placed = reducer(s, { type: 'place', spec: TUNNEL, x: 50, y: 50 });
    const tunnel = placed.buildings.find((b) => b.spec === TUNNEL);
    assert.ok(tunnel, 'the tunnel must place');
    assert.equal(tunnel.footprintW, undefined, 'a new placement carries no legacy override');
    assert.equal(tunnel.footprintH, undefined);
    const { w, h } = footprintOf(tunnel, TUNNEL_SPEC);
    assert.equal(w, TUNNEL_SPEC.w);
    assert.equal(h, TUNNEL_SPEC.h);
  });

  test('the epoch guard is load-bearing: a NEW tunnel (placed after the first migration) survives a LATER hydrate un-shrunk', () => {
    // 1) An old save with one legacy tunnel loads — migration runs once, epoch bumps.
    const genesis = initialState();
    const legacy = {
      ...genesis,
      tunnelFootprintEpoch: 0, // simulate a save predating this migration
      buildings: [...genesis.buildings, { id: 610001, spec: TUNNEL, x: 10, y: 10, builtTick: 0 }],
    };
    const afterFirstLoad = reducer(initialState(), { type: 'hydrate', state: legacy });
    assert.equal(afterFirstLoad.tunnelFootprintEpoch, TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH);
    const legacyTunnel = afterFirstLoad.buildings.find((b) => b.id === 610001);
    assert.equal(legacyTunnel.footprintW, 3, 'the pre-existing tunnel is grandfathered');

    // 2) The player then places a brand-new tunnel under CURRENT (bigger) rules.
    const richState = { ...afterFirstLoad, unlockedAll: true, funds: TUNNEL_SPEC.cost * 10 };
    const afterNewPlacement = reducer(richState, { type: 'place', spec: TUNNEL, x: 100, y: 100 });
    const newTunnel = afterNewPlacement.buildings.find((b) => b.x === 100 && b.y === 100);
    assert.ok(newTunnel);
    assert.equal(newTunnel.footprintW, undefined, 'the new tunnel has no override yet');

    // 3) A LATER hydrate (e.g. a "Load Save" or worker-sync reconciliation)
    //    must NOT re-run the migration and wrongly shrink the new tunnel.
    const afterSecondLoad = reducer(initialState(), { type: 'hydrate', state: afterNewPlacement });
    const newTunnelAfter = afterSecondLoad.buildings.find((b) => b.x === 100 && b.y === 100);
    assert.equal(newTunnelAfter.footprintW, undefined, 'a genuinely new tunnel must never be grandfathered to the old dims');
    const { w, h } = footprintOf(newTunnelAfter, TUNNEL_SPEC);
    assert.equal(w, TUNNEL_SPEC.w);
    assert.equal(h, TUNNEL_SPEC.h);
    // And the pre-existing legacy tunnel is still correctly at its old size.
    const legacyTunnelAfter = afterSecondLoad.buildings.find((b) => b.id === 610001);
    assert.equal(legacyTunnelAfter.footprintW, 3);
  });

  test('a fresh (brand-new) city starts already at the current tunnel-footprint epoch', () => {
    const s = initialState();
    assert.equal(s.tunnelFootprintEpoch, TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH);
  });
});
