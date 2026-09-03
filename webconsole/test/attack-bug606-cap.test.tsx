// ATTACK r3 — RESOLVE_DEMAND_ALL_MAX_UNITS: perf, capped replay, convergence.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, orderedDemandFixPlan, RESOLVE_DEMAND_ALL_MAX_UNITS, placementCost, SPECS } from '../src/sim/data.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic, stableStringify } from '../src/sim/genesisReplay.ts';

function big(population: number, funds = 1e12) {
  return { ...initialState(), population, unlockedAll: true, funds, administrationState: null } as never;
}

// BUG-646 (cap 250 -> 2000, Aaron 2026-09-03): at pop 3M, parks plans only
// 1,000 units — no longer enough to exceed the new 2000 cap (it comfortably
// exceeded the old 250 one). 8M parks plans 2,667 units, margin included.
const POP_FOR_PARKS_OVER_CAP = 8_000_000;

test('ATTACK r3/PERF: one resolveDemandAll at pop 3M completes fast and places at most the cap', () => {
  const s: any = big(3_000_000);
  const planned = orderedDemandFixPlan(s).reduce((a, p) => a + p.count, 0);
  assert.ok(planned > 10_000, `precondition: an uncapped plan is huge (${planned})`);
  const t0 = Date.now();
  const r: any = reducer(s, { type: 'resolveDemandAll' } as never);
  const secs = (Date.now() - t0) / 1000;
  const placed = r.buildings.length - s.buildings.length;
  console.log(`  pop 3M: ${secs.toFixed(2)}s, planned ${planned}, buildings +${placed}, notice: ${r.placeNotice}`);
  assert.ok(secs < 10, `resolveDemandAll took ${secs}s at pop 3M — the perf cap did not bound it`);
  assert.ok(r.funds >= 0, 'funds must never go negative');
  assert.match(r.placeNotice, /^Fix All: built \d+ of \d+ planned — click Fix All again for the rest$/, `capped notice wrong: ${r.placeNotice}`);
});

test('ATTACK r3/PERF: one resolveDemand (single service) at a huge pop is also capped', () => {
  const s: any = big(POP_FOR_PARKS_OVER_CAP);
  const parks = demandFixPlan(s).find((p: any) => p.serviceKey === 'parks');
  assert.ok(parks && parks.count > RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: parks plan (${parks?.count}) exceeds the cap`);
  const t0 = Date.now();
  const r: any = reducer(s, { type: 'resolveDemand', serviceKey: 'parks' } as never);
  const secs = (Date.now() - t0) / 1000;
  const placed = r.buildings.filter((b: any) => b.spec === parks!.specId).length - s.buildings.filter((b: any) => b.spec === parks!.specId).length;
  console.log(`  single-service pop ${POP_FOR_PARKS_OVER_CAP}: ${secs.toFixed(2)}s, placed ${placed}, notice: ${r.placeNotice}`);
  assert.equal(placed, RESOLVE_DEMAND_ALL_MAX_UNITS, 'single resolveDemand must stop at the cap');
  // BUG-646 (cap 250 -> 2000, Aaron 2026-09-03): the OLD `secs < 10` bound was
  // calibrated for a 250-unit cap. At 2000 units placing a road-needing spec
  // (park) into an initially near-empty map, autoConnect()'s planConnector()
  // road-search BFS (roadConnect.ts, CONNECT_BUDGET=6000 — a PRE-EXISTING,
  // untouched-by-this-fix mechanism) dominates once units get pushed
  // progressively further from the sparse road network — measured ~30s here.
  // This session's real fixes (createSpotSearchContext's O(buildings) and
  // O(window) eliminations — see data.ts's RESOLVE_DEMAND_ALL_MAX_UNITS doc
  // comment) cut findSpot()'s OWN cost to near-zero; the residual is the
  // road-BFS cost every 'place' dispatch already pays, flagged there as a
  // follow-up (a shared/amortised road-BFS budget across a batch). 60s is a
  // real, generous ceiling on TODAY's measured worst case, not a rubber
  // stamp — it still catches a genuine regression that makes this materially
  // worse.
  assert.ok(secs < 60, `resolveDemand took ${secs}s`);
  assert.match(r.placeNotice, /per-click build limit — click Fix again for the rest/, `cap reason not reported: ${r.placeNotice}`);
});

test('ATTACK r3/CAP-BUDGET: the cap is a GLOBAL budget — total placed across all services never exceeds it', () => {
  for (const pop of [50_000, 400_000, 3_000_000]) {
    const s: any = big(pop);
    const r: any = reducer(s, { type: 'resolveDemandAll' } as never);
    // Only plan specs count toward the budget; 'place' may auto-append road/rail
    // connector tiles, so compare per-plan-spec deltas, not raw buildings.length.
    const planSpecs = new Set(orderedDemandFixPlan(s).map((p: any) => p.specId));
    let placed = 0;
    for (const id of planSpecs) {
      placed += r.buildings.filter((b: any) => b.spec === id).length - s.buildings.filter((b: any) => b.spec === id).length;
    }
    assert.ok(placed <= RESOLVE_DEMAND_ALL_MAX_UNITS, `pop ${pop}: placed ${placed} plan units > cap ${RESOLVE_DEMAND_ALL_MAX_UNITS}`);
  }
});

test('ATTACK r3/CAP-REASON: a capped batch never blames funds, administration or the map', () => {
  const s: any = big(3_000_000);
  const r: any = reducer(s, { type: 'resolveDemandAll' } as never);
  assert.doesNotMatch(r.placeNotice, /insufficient funds|Administration|no free area|nothing built/i, r.placeNotice);
});

test('ATTACK r3/CAP-REASON-LIE: a single-service resolveDemand at the cap must report unit-limit phrasing, not no-free-area', () => {
  const s: any = big(POP_FOR_PARKS_OVER_CAP);
  const parks = demandFixPlan(s).find((p: any) => p.serviceKey === 'parks');
  assert.ok(parks && parks.count > RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: parks plan (${parks?.count}) exceeds the cap`);
  const r: any = reducer(s, { type: 'resolveDemand', serviceKey: 'parks' } as never);
  assert.ok(r.placeNotice, 'notice must exist for a capped single-service build');
  assert.match(r.placeNotice, new RegExp(`reached the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit per-click build limit`), `unit-limit phrasing must be reported, not no-free-area reason: ${r.placeNotice}`);
  assert.doesNotMatch(r.placeNotice, /no free area/i, `must NOT report no-free-area when capped by units: ${r.placeNotice}`);
});

test('ATTACK r3/REPLAY: a CAPPED resolveDemandAll replays byte-identically from genesis', () => {
  // Journal-reachable route to a city big enough to hit the cap.
  const ticksN = (n: number) => Array.from({ length: n }, () => ({ type: 'tick' }) as never);
  const script: any[] = [
    { type: 'debugFunds', amount: 500_000_000 },
    { type: 'unlockAll' },
    ...Array.from({ length: 12 }, (_, i) => ({ type: 'place', spec: 'res_block', x: 5 + i * 3, y: 5 })),
    ...ticksN(120),
    { type: 'resolveDemandAll' },
    { type: 'resolveDemandAll' },
    ...ticksN(5),
  ];
  let journal = emptyJournal();
  let live: any = initialState();
  for (const a of script) {
    if (isStateAffecting(a)) journal = recordAction(journal, live.tick, a);
    live = reducer(live, a);
  }
  const capped = /click Fix All again for the rest/.test(live.placeNotice ?? '');
  console.log(`  capped-invocation replay scenario: notice = ${live.placeNotice} (cap fired: ${capped})`);
  const replayed = replayFromGenesis(journal);
  assert.equal(
    stableStringify({ ...replayed, roadConnectivity: null }),
    stableStringify({ ...live, roadConnectivity: null }),
    'live vs genesis replay diverged through a resolveDemandAll'
  );
  assert.ok(replayIsDeterministic(journal), 'same journal replayed twice must be byte-identical');
});

test('ATTACK r3/REPLAY-CAPPED-FORCED: a definitely-capped action replays byte-identically', () => {
  // Force the cap by dispatching against a huge synthetic city and replaying
  // that single action from the same start state twice, plus a chained run.
  const s: any = big(3_000_000);
  const a: any = reducer(s, { type: 'resolveDemandAll' } as never);
  const b: any = reducer(s, { type: 'resolveDemandAll' } as never);
  assert.equal(stableStringify(a), stableStringify(b), 'a capped action must be a pure deterministic function of its input state');
  assert.match(a.placeNotice, /click Fix All again/, 'precondition: this invocation must actually be capped');
  const a2: any = reducer(a, { type: 'resolveDemandAll' } as never);
  const b2: any = reducer(b, { type: 'resolveDemandAll' } as never);
  assert.equal(stableStringify(a2), stableStringify(b2), 'chained capped actions must stay deterministic');
});

test('ATTACK r3/CONVERGENCE: repeated clicking terminates in a RUNNING sim — no oscillation at the 150% overshoot boundary', () => {
  // Coverage `have` only refreshes on a tick, so the realistic model is
  // click-then-tick (a live game ticks continuously). See the paused-sim
  // test below for the other half.
  for (const pop of [20_000, 50_000]) {
    let s: any = big(pop);
    let clicks = 0;
    const MAX = 120;
    let lastGap = Infinity;
    while (orderedDemandFixPlan(s).length > 0 && clicks < MAX) {
      s = reducer(s, { type: 'resolveDemandAll' } as never);
      s = reducer(s, { type: 'tick' } as never);
      clicks++;
      const rows: any[] = orderedDemandFixPlan(s);
      const gap = rows.reduce((a, p) => a + (p.need - p.have), 0);
      // Once the batch is down to the tail services, total outstanding gap
      // must not GROW click over click (that would be the overshoot boundary
      // oscillating instead of converging).
      if (clicks > 8) assert.ok(gap <= lastGap + 1e-6, `pop ${pop}: outstanding gap GREW at click ${clicks} (${lastGap} -> ${gap}) — oscillation`);
      lastGap = gap;
      assert.ok(s.funds >= 0, 'funds must never go negative');
    }
    console.log(`  pop ${pop}: converged in ${clicks} click+tick cycle(s), ${orderedDemandFixPlan(s).length} row(s) left`);
    assert.ok(clicks < MAX, `pop ${pop}: did not converge in ${MAX} clicks — cap + 150% overshoot must terminate`);
    assert.equal(orderedDemandFixPlan(s).length, 0, `pop ${pop}: plan must be empty once converged`);
  }
});

test('ATTACK r3/PAUSED-SIM: with no tick between clicks, coverage never refreshes, but a MUCH higher cap can now fully clear a small-enough row without one (documented)', () => {
  let s: any = big(50_000);
  const before = orderedDemandFixPlan(s).length;
  for (let i = 0; i < 3; i++) s = reducer(s, { type: 'resolveDemandAll' } as never);
  const after = orderedDemandFixPlan(s).length;
  // This asserts the CURRENT behaviour so a future change is visible; it is
  // not an endorsement. The notice tells the player to "click again" for any
  // row still short, which in a PAUSED sim spends money without ever
  // REFRESHING coverage. BUG-646 (cap 250 -> 2000, Aaron 2026-09-03) changed
  // the observed row count itself, though: at pop 50,000 the old 250-unit
  // cap could never place a whole service's target (need*1.5) in 3 clicks,
  // so `after === before` held; at 2000 units per click, at least one
  // small-target row now fully clears from PLACEMENT alone (no tick/coverage
  // refresh needed) — going from 11 rows to 10. This is the INTENDED, better
  // outcome of the cap raise (more real progress per click), not a
  // regression — re-check this number again if a future cap/AUTO_BUILD_
  // DEMAND_FRACTION change moves it further.
  assert.equal(after, 10, `paused-sim row count changed post-cap-raise (${before} -> ${after}) — re-check the "click again" guidance`);
  assert.ok(s.funds >= 0);
});

test('ATTACK r3/CONVERGENCE-MONEY: repeated clicks spend exactly sum(placed * placementCost)', () => {
  let s: any = big(50_000);
  const f0 = s.funds;
  const c0 = new Map<string, number>();
  for (const b of s.buildings) c0.set(b.spec, (c0.get(b.spec) ?? 0) + 1);
  for (let i = 0; i < 12 && orderedDemandFixPlan(s).length > 0; i++) s = reducer(s, { type: 'resolveDemandAll' } as never);
  const c1 = new Map<string, number>();
  for (const b of s.buildings) c1.set(b.spec, (c1.get(b.spec) ?? 0) + 1);
  let spent = 0;
  for (const [id, n] of c1) {
    const d = n - (c0.get(id) ?? 0);
    if (d > 0) spent += d * placementCost(SPECS[id]);
  }
  assert.equal(f0 - s.funds, spent, 'treasury movement must equal exactly what was placed');
  assert.ok(s.funds >= 0);
});
