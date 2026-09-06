// bug-791-road-network-stability.test.mjs — BUG-791 (P1), ROUND VERDICT:
// NOT A DEFECT. The round-14 "1 -> ~130 components" finding was a
// MEASUREMENT BUG, not a sim bug: the round's own component counter only
// looked at buildings with `spec === 'road'` (tier 1, "Lane"), so every tile
// road auto-scale legitimately re-tiers in place to `rd_avenue`/`rd_aroad`/
// `rd_dual`/`m20` (a same-footprint, in-place spec swap — see
// evaluateRoadMonitors, engine.ts) DROPPED OUT of that count and looked like
// a disconnected/vanished tile, even though the tile is still there, still a
// member of the road network, just wearing a higher-tier spec id. Counting
// by the real road-FAMILY notion (every rung of the road ladder, kind-based,
// not spec-id-based) shows the network is a single connected component the
// entire run.
//
// Lead decision (round result): do NOT land defensive guards for this —
// there was never a live defect for them to guard against, and the
// candidate guards were either subsumed by existing structural invariants
// with no forcing test (V880/V883) or measured too expensive to justify
// wiring in for a check that can never fire today (wouldSever/V881, ~232ms/
// call at 23k tiles). This file is the TEST-ONLY deliverable: a permanent
// regression test using the CORRECT (kind-based, road-ladder-inclusive)
// component count, so the round-14 measurement mistake can never recur and
// silently look like a real regression again.
//
// No new src changes ship with this file — src/sim/data.ts and
// src/sim/engine.ts are back to HEAD content. This test imports only
// PRE-EXISTING, already-public data.ts/engine.ts exports (the same ones
// attack-inc3-round11-dogfood.test.mjs already uses) and does its own tile-
// participant classification and component counting LOCALLY, so it never
// depends on anything this round proposed and then withdrew.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity, SPECS, ROAD_TIER_SPECS } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';

/**
 * LOCAL road-network participant test — a spec counts iff its `kind` is one
 * that computeRoadConnectivity's own BFS treats as a network member: a
 * drivable road tier (kind 'road' — Lane/Avenue/A-Road/Dual/Motorway all
 * share this kind; only tier/roadTier distinguishes them) or a trunk kind
 * (motorway/rail/station — data.ts's isRoadOrTrunkSpec notion). This is
 * intentionally KIND-based, not spec-id-based, so an in-place tier re-swap
 * (road -> rd_avenue -> rd_aroad -> rd_dual -> m20, all kind 'road') is
 * counted as the SAME network tile throughout — the exact distinction the
 * round-14 count got wrong.
 */
const NETWORK_KINDS = new Set(['road', 'motorway', 'rail', 'station']);
function isNetworkSpec(sp) {
  return !!sp && NETWORK_KINDS.has(sp.kind);
}

/**
 * GR#15 self-check (validators derive from data, never a hardcoded list):
 * every rung of the real road ladder (ROAD_TIER_SPECS, data.ts's own SSOT
 * for "what a road tile upgrades into") must be classified as a network
 * spec by isNetworkSpec above, or this whole file's premise — that
 * KIND-based counting survives a tier re-swap — is untested for whichever
 * rung slipped through.
 */
describe('BUG-791 (round verdict: NOT A DEFECT) — permanent regression guard against the round-14 measurement mistake', () => {
  test('self-check: isNetworkSpec recognises every ROAD_TIER_SPECS rung', () => {
    const rungs = Object.values(ROAD_TIER_SPECS);
    assert.ok(rungs.length >= 2, 'sanity: the road ladder has more than one rung to actually exercise a re-tier');
    for (const specId of rungs) {
      const sp = SPECS[specId];
      assert.ok(sp, `ROAD_TIER_SPECS references unknown spec id ${specId}`);
      assert.ok(
        isNetworkSpec(sp),
        `ROAD_TIER_SPECS rung ${specId} (kind '${sp.kind}') is NOT classified as a network spec by this test's isNetworkSpec() -- the round-14 mistake (a road-tier count that misses a re-tiered rung) could recur undetected`,
      );
    }
  });

  function mk(over) {
    const base = initialState();
    return {
      ...base,
      unlockedAll: true,
      roadMonitors: [],
      buildingMonitors: [],
      buildings: [],
      population: 0,
      funds: 1_000_000_000,
      tick: 0,
      consolidatorEnabled: false,
      consolidatorLayoutEnabled: false, // BUG-791 repro instruction: layout OFF
      consolidatorLog: [],
      consolidatorMode: 'monthly-twelfth',
      xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
      lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
      ...over,
    };
  }

  /** Same attacker-measured dogfood shape as attack-inc3-round11-dogfood.test.mjs. */
  function dogfoodFixture(over) {
    const W = 160;
    const H = 96;
    let id = 1;
    const buildings = [];
    const roadIds = [];
    for (let y = 0; y < H; y += 8) {
      for (let x = 0; x < W; x++) {
        const bid = id++;
        buildings.push({ id: bid, spec: 'road', x, y, builtTick: -1000 });
        roadIds.push(bid);
      }
    }
    for (let x = 0; x < W; x += 8) {
      for (let y = 0; y < H; y++) {
        const bid = id++;
        buildings.push({ id: bid, spec: 'road', x, y, builtTick: -1000 });
        roadIds.push(bid);
      }
    }
    let placed = 0;
    const resTerraceIds = [];
    for (let by = 4; by < H && placed < 340; by += 8) {
      for (let bx = 4; bx < W && placed < 340; bx += 8) {
        const spec = placed % 2 === 0 ? 'res_terrace' : 'com_shop';
        const bid = id++;
        buildings.push({ id: bid, spec, x: bx, y: by, builtTick: -1000 });
        if (spec === 'res_terrace') resTerraceIds.push(bid);
        placed++;
      }
    }
    for (let i = 0; i < 12; i++) {
      buildings.push({ id: id++, spec: 'hea_hospital', x: 4 + (i * 13) % (W - 8), y: 2, builtTick: -1000 });
    }
    for (let i = 0; i < 40; i++) {
      buildings.push({ id: id++, spec: 'edu_nursery', x: 4 + (i * 4) % (W - 8), y: H - 6, builtTick: -1000 });
    }
    const s = mk({ buildings, population: 200_000, ...over });
    return { ...s, roadConnectivity: computeRoadConnectivity(s), __roadIds: roadIds, __resTerraceIds: resTerraceIds };
  }

  /**
   * The raw dogfoodFixture never registers a road/building monitor (those
   * are normally minted only by the 'place' reducer action, which this
   * hand-built fixture bypasses), so road auto-scale and building auto-scale
   * are pure no-ops on it. Seed a monitor on every road tile (a permanent,
   * never-expiring window) and every res_terrace building so road re-tiering
   * (the exact mechanism the round-14 count mismeasured) actually runs the
   * whole test.
   */
  function withSeededMonitors(s) {
    const roadMonitors = (s.__roadIds ?? [])
      .map((bid) => {
        const b = s.buildings.find((x) => x.id === bid);
        return b ? { x: b.x, y: b.y, source: bid, until: 10_000_000 } : null;
      })
      .filter(Boolean);
    const buildingMonitors = (s.__resTerraceIds ?? []).map((bid) => ({ buildingId: bid, until: 10_000_000, type: 'residents' }));
    return { ...s, roadMonitors, buildingMonitors };
  }

  /** Local 4-connected component count over every network-kind tile — no src import, GR#21 order-independent flood fill via a plain stack. */
  function networkTiles(s) {
    const tiles = new Set();
    for (const b of s.buildings) {
      const sp = SPECS[b.spec];
      if (!isNetworkSpec(sp)) continue;
      const w = b.footprintW ?? sp.w;
      const h = b.footprintH ?? sp.h;
      for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) tiles.add(`${b.x + dx},${b.y + dy}`);
    }
    return tiles;
  }
  function componentCount(tiles) {
    const seen = new Set();
    let count = 0;
    const ordered = Array.from(tiles).sort();
    for (const start of ordered) {
      if (seen.has(start)) continue;
      count++;
      const stack = [start];
      seen.add(start);
      while (stack.length > 0) {
        const k = stack.pop();
        const c = k.indexOf(',');
        const x = Number(k.slice(0, c));
        const y = Number(k.slice(c + 1));
        for (const [ox, oy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
          const nk = `${x + ox},${y + oy}`;
          if (tiles.has(nk) && !seen.has(nk)) {
            seen.add(nk);
            stack.push(nk);
          }
        }
      }
    }
    return count;
  }
  function roadTierSpecCounts(s) {
    const c = {};
    for (const b of s.buildings) {
      const sp = SPECS[b.spec];
      if (sp && sp.roadTier) c[b.spec] = (c[b.spec] ?? 0) + 1;
    }
    return c;
  }

  for (const consolidatorEnabled of [false, true]) {
    test(`R1: road-family (kind-based, incl. rd_avenue/rd_aroad/rd_dual/m20) network component count never rises over 300 ticks (consolidatorEnabled=${consolidatorEnabled}, layout OFF, monitors seeded so road auto-scale genuinely re-tiers)`, () => {
      let s = withSeededMonitors(dogfoodFixture({}));
      if (consolidatorEnabled) s = reducer(s, { type: 'toggleConsolidator' });

      let prevTiles = networkTiles(s);
      let prevComp = componentCount(prevTiles);
      assert.equal(prevComp, 1, 'the seed fixture is a single connected grid at tick 0');

      let maxComponents = prevComp;
      for (let t = 1; t <= 300; t++) {
        s = reducer(s, { type: 'tick' });
        const tiles = networkTiles(s);
        const comp = componentCount(tiles);
        maxComponents = Math.max(maxComponents, comp);
        assert.ok(
          comp <= prevComp,
          `tick ${t}: road-family components rose ${prevComp} -> ${comp} (removed from the network-kind set: ${[...prevTiles].filter((k) => !tiles.has(k)).slice(0, 20)})`,
        );
        prevTiles = tiles;
        prevComp = comp;
      }

      const tierCounts = roadTierSpecCounts(s);
      const rungsSeen = Object.keys(tierCounts).length;
      assert.equal(maxComponents, 1, 'the road-family network stays a single component for the whole run');
      assert.ok(
        rungsSeen >= 2,
        `sanity: seeded road monitors should have re-tiered at least one tile off the base 'road' spec this run (else this test never actually exercises the round-14 mistake) -- saw tiers: ${JSON.stringify(tierCounts)}`,
      );
      // eslint-disable-next-line no-console
      console.log(`BUG-791 R1 (consolidatorEnabled=${consolidatorEnabled}): road tier spec counts=`, tierCounts, `final network tiles=${networkTiles(s).size}`);
    });
  }
});
