// bug606-fix-all.test.mjs — BUG-606 fix-all (P1, Aaron 2026-09-03: "next to
// the word demand for the right tab I want a fix-all button").
//
// 'resolveDemandAll' (engine.ts) is a THIN LOOP over the SAME placePlanItem()
// helper 'resolveDemand' already uses, walking orderedDemandFixPlan(state)'s
// priority order (Health first, then demand-descending — data.ts) as ONE
// journaled action. Justification for adding this engine action rather than
// N sequential per-service dispatches (the brief's default preference): the
// per-service dispatch path is ALREADY correct for fund-safety on its own
// (React's useReducer threads each dispatch's reducer call through the
// already-updated pending state, so sequential resolveDemand dispatches would
// never overspend either) — but it cannot produce ONE coherent "built X,
// skipped Y" placeNotice, since each dispatch would silently overwrite the
// previous one's notice. The brief explicitly allows a thin batched action
// when the aggregate report needs it; this is that case.
//
// Covers: priority order under a real funds cap (the aggregate-report reason
// this is a single action), full-batch ample-funds completion, no-op on an
// empty plan, and determinism. Real reducer runs throughout (no mocked
// state) — same fund-conservation shape as demand-fix.test.mjs's existing
// per-service affordability tests.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, orderedDemandFixPlan, SPECS, placementCost } from '../src/sim/data.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ENGINE_PATH = path.join(__dirname, '..', 'src', 'sim', 'engine.ts');

function shortfallState(population, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

function countOf(state, specId) {
  return state.buildings.filter((b) => b.spec === specId).length;
}

/** BUG-685: count placements of EVERY spec in a (possibly mixed) plan, not
 *  just the primary/largest one — a largestFirstFill() mix places several
 *  specs, so a single-specId filter undercounts. */
function countMixOf(state, mix) {
  const ids = new Set(mix.map((m) => m.specId));
  return state.buildings.filter((b) => ids.has(b.spec)).length;
}

test('resolveDemandAll: no shortfall anywhere is a true no-op', () => {
  const s = shortfallState(0);
  assert.equal(orderedDemandFixPlan(s).length, 0, 'precondition: zero population means zero real demand');
  const result = reducer(s, { type: 'resolveDemandAll' });
  assert.deepEqual(result, s, 'an empty plan must leave state byte-identical');
});

test('resolveDemandAll: ample funds fully clears EVERY service in the plan, in one action', () => {
  // RETUNE (this session, post-BUG-685 largest-first landing): shortfallState()'s
  // default £1bn is no longer genuinely "ample" for every service — the
  // largest-first picker (data.ts largestFirstFill()) can pick a spec whose
  // real cost dwarfs the old cheapest-total-plan pick (Three Gorges Dam,
  // £5bn, for power) regardless of price. An explicit, truly-ample override
  // keeps this test's actual point (a well-funded city clears its WHOLE
  // batch with nothing skipped) genuine rather than accidentally exercising
  // the BUG-685-MONEY affordability fallback this file's RED-PROOF test
  // below targets on purpose.
  const s = shortfallState(20_000, 1_000_000_000_000);
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length >= 5, 'precondition: a real multi-service shortfall must exist at this population');

  const result = reducer(s, { type: 'resolveDemandAll' });
  assert.ok(result.funds >= 0, 'funds must never go negative');
  assert.ok(result.funds < s.funds, 'ample-funds Fix All must actually spend money');

  for (const item of order) {
    const plan = demandFixPlan(s).find((p) => p.serviceKey === item.serviceKey);
    assert.ok(plan, `precondition: ${item.serviceKey} must still have a demandFixPlan entry`);
    const placed = countMixOf(result, plan.mix) - countMixOf(s, plan.mix);
    assert.equal(placed, plan.count, `${item.serviceKey} must be FULLY placed (${plan.count} units across its mix) under ample funds`);
  }

  assert.ok(result.placeNotice && result.placeNotice.startsWith('Fix All: built'), `expected a "Fix All: built ..." notice, got: ${result.placeNotice}`);
  assert.ok(!/skipped/i.test(result.placeNotice), 'ample funds must clear the whole batch — nothing skipped');
});

test('resolveDemandAll (RED-PROOF for priority order): a funds cap sized to JUST the two Health services fully funds them and skips every lower-priority service, never going negative', () => {
  const s = shortfallState(60_000);
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length >= 3, 'precondition: at least 3 services in shortfall to prove ordering under a cap');
  assert.ok(
    order[0].serviceKey === 'gp' || order[0].serviceKey === 'hosp',
    `precondition: Health must lead the priority order, got ${order[0].serviceKey} first`
  );
  assert.ok(
    order[1].serviceKey === 'gp' || order[1].serviceKey === 'hosp',
    `precondition: Health must occupy the top TWO slots, got ${order[1].serviceKey} second`
  );
  assert.notEqual(order[0].serviceKey, order[1].serviceKey);

  // Real spend to fully resolve BOTH health services in priority order,
  // chained exactly the way resolveDemandAll's inner loop processes them
  // (each recomputed against the already-updated state) — same technique
  // demand-fix.test.mjs's affordability tests use to avoid assuming a
  // uniform per-unit price (road-adjacency connector costs vary by site).
  const afterFirst = reducer(s, { type: 'resolveDemand', serviceKey: order[0].serviceKey });
  const afterBoth = reducer(afterFirst, { type: 'resolveDemand', serviceKey: order[1].serviceKey });
  const healthCost = s.funds - afterBoth.funds;
  assert.ok(healthCost > 0, 'precondition: fully resolving both health services must actually spend money');

  const planFirst = demandFixPlan(s).find((p) => p.serviceKey === order[0].serviceKey);
  const planSecond = demandFixPlan(s).find((p) => p.serviceKey === order[1].serviceKey);
  assert.ok(planFirst && planSecond);

  const limited = { ...s, funds: healthCost };
  const result = reducer(limited, { type: 'resolveDemandAll' });

  assert.ok(result.funds >= 0, 'funds must never go negative from Fix All');
  assert.ok(result.funds <= limited.funds, 'Fix All must never manufacture funds');

  // RED-PROOF: the pre-BUG-606 world had no priority order at all (no
  // 'resolveDemandAll' existed) — a naive "just walk demandFixPlan() in
  // array order" implementation would have no guarantee Health lands first,
  // and this exact assertion (Health fully funded, third+ services entirely
  // skipped) would be the first thing to fail under array-order instead of
  // priority-order, since serviceCoverageOf()'s array order is NOT
  // Health-first (see data.ts: nursery/primary/college precede gp/hosp).
  const placedFirst = countMixOf(result, planFirst.mix) - countMixOf(limited, planFirst.mix);
  const placedSecond = countMixOf(result, planSecond.mix) - countMixOf(limited, planSecond.mix);
  assert.equal(placedFirst, planFirst.count, `${order[0].serviceKey} (priority 1) must be FULLY placed even under a tight funds cap`);
  assert.equal(placedSecond, planSecond.count, `${order[1].serviceKey} (priority 2) must be FULLY placed even under a tight funds cap`);

  // Every lower-priority service with a REAL (£>0) placement cost must be
  // entirely skipped — the budget was sized to leave zero funds once Health
  // is done. Zoning-category services (parks — placementCost() is £0 for the
  // 'zones' catalogue category, data.ts) are legitimately unaffected by a
  // funds cap and are excluded from this assertion, not because they might
  // slip through, but because "must be skipped when funds run out" simply
  // does not apply to a £0 action — this mirrors the SAME cost>0 guard
  // resolveDemand/placePlanItem() themselves use.
  let assertedAtLeastOneSkip = false;
  for (const item of order.slice(2)) {
    const plan = demandFixPlan(s).find((p) => p.serviceKey === item.serviceKey);
    if (!plan) continue;
    if (placementCost(SPECS[plan.specId]) <= 0) continue; // a free zone — funds cap does not apply
    const placed = countMixOf(result, plan.mix) - countMixOf(limited, plan.mix);
    assert.equal(placed, 0, `${item.serviceKey} (lower priority, real cost) must be skipped once Health exhausts the funds cap`);
    assertedAtLeastOneSkip = true;
  }
  assert.ok(assertedAtLeastOneSkip, 'precondition: at least one lower-priority, real-cost service must exist to prove the skip');

  assert.ok(result.placeNotice, 'a funds-capped Fix All must report a notice');
  assert.ok(/skipped|partial/i.test(result.placeNotice), `placeNotice must report what was skipped, got: ${result.placeNotice}`);
  assert.ok(result.placeNotice.includes('Fix All'));
});

test('resolveDemandAll: with £1 in the treasury, no REAL-COST building is placed and funds never go negative', () => {
  const s = shortfallState(60_000);
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length > 0, 'precondition: a real plan existed to attempt');
  const limited = { ...s, funds: 1 }; // effectively broke
  const result = reducer(limited, { type: 'resolveDemandAll' });
  assert.ok(result.funds >= 0);

  // £1 can never afford a single unit of any COSTED service — only free
  // zoning-category services (parks — placementCost() £0, see the funds-cap
  // test above) may still gain buildings, since a funds cap legitimately
  // does not apply to a £0 action.
  for (const item of order) {
    const plan = demandFixPlan(s).find((p) => p.serviceKey === item.serviceKey);
    if (!plan || placementCost(SPECS[plan.specId]) <= 0) continue;
    const placed = countOf(result, plan.specId) - countOf(limited, plan.specId);
    assert.equal(placed, 0, `${item.serviceKey} must place nothing with £1 in the treasury`);
  }
  assert.ok(result.placeNotice);
});

// BUG-685-MONEY (the round's LEAD DEFECT, item 1): the exact end-to-end
// reproduction the independent round demanded — a real 'resolveDemand'
// dispatch (not a bare largestFirstFill() call) against a state whose power
// demandFixPlan mix is a SINGLE unaffordable entry (pow_hydro, £5bn — the
// sole credited-largest power candidate at this small a shortfall) with a
// treasury that cannot afford it but CAN afford the next-cheaper/denser
// unlocked spec (pow_wind, £3.6M). Pre-fix, placePlanItem() had no fallback
// once its one-shot mix[0] failed the funds gate and placed ZERO buildings
// even though a real, affordable candidate existed. Population sized so
// power's fixAmount stays well under pow_hydro's ~5,000 MW credited capacity
// (no capacityTiers ladder on power specs generally, so credited == mw) —
// the one-shot branch's own condition (capacity >= remaining) — guaranteeing
// mix.length === 1 (see largestFirstFill()'s own doc comment).
test('resolveDemand (BUG-685-MONEY end-to-end): a single-entry unaffordable primary spec falls through to an affordable candidate, never places ZERO', () => {
  const s = shortfallState(10_000, 1_000_000_000); // £1bn: > pow_wind's £3.6M, < pow_hydro's £5bn
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan, 'precondition: a power shortfall must exist at this population');
  assert.equal(plan.mix.length, 1, 'precondition: the natural mix must be a SINGLE entry (the one-shot branch)');
  assert.equal(plan.specId, 'pow_hydro', 'precondition: pow_hydro must be the sole (unaffordable) candidate');
  assert.ok(SPECS.pow_hydro.cost > s.funds, 'precondition: pow_hydro must be unaffordable at this treasury');
  assert.ok(SPECS.pow_wind.cost < s.funds, 'precondition: a real, cheap, unlocked fallback (pow_wind) must be affordable');

  const before = s.buildings.length;
  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });

  assert.ok(result.buildings.length > before, 'BUG-685-MONEY: the fallback must place at least one building, never zero');
  const placedPower = result.buildings.slice(before).filter((b) => SPECS[b.spec]?.kind === 'power' || b.spec === 'pow_wind');
  assert.ok(placedPower.some((b) => b.spec !== 'pow_hydro'), 'the placed building(s) must be the FALLBACK spec, not the unaffordable pow_hydro');
  assert.equal(result.buildings.filter((b) => b.spec === 'pow_hydro').length, 0, 'pow_hydro itself must never have been placed (it was never affordable)');
  assert.ok(result.funds >= 0, 'funds must never go negative');
  assert.ok(!/Placed 0 of/.test(result.placeNotice ?? ''), `placeNotice must not report zero progress, got: ${result.placeNotice}`);
});

test('RED-PROOF (source revert, GR#24 scratch-copy — never git): disabling placePlanItem\'s self-heal fallback reddens the BUG-685-MONEY end-to-end test', () => {
  const original = fs.readFileSync(ENGINE_PATH, 'utf8');
  const fixedLine =
    'if (result.placed > 0 || (cur.administrationState && item.mix.some((m) => placementCost(SPECS[m.specId]) > 0))) {';
  assert.ok(original.includes(fixedLine), 'precondition: the self-heal guard is present in engine.ts');

  // Force the guard to always short-circuit — exactly the pre-BUG-685-MONEY
  // shape (a one-shot unaffordable mix places 0 and NOTHING retries).
  const buggyLine = 'if (true) {';
  const reverted = original.replace(fixedLine, buggyLine);
  assert.notEqual(reverted, original, 'precondition: the textual swap actually changed the file');

  const backupPath = ENGINE_PATH + '.bug685-money-red-proof.bak';
  fs.copyFileSync(ENGINE_PATH, backupPath); // scratch copy, per GR#24 — never git for revert
  try {
    fs.writeFileSync(ENGINE_PATH, reverted, 'utf8');

    const nodeExe = process.execPath;
    const childEnv = { ...process.env };
    delete childEnv.NODE_TEST_CONTEXT; // BUG-509/BUG-662 precedent: required or the child silently IPCs to the outer runner instead of exiting non-zero
    let failed = false;
    let output = '';
    try {
      output = execFileSync(
        nodeExe,
        [
          '--test',
          '--test-name-pattern=BUG-685-MONEY end-to-end',
          fileURLToPath(import.meta.url),
        ],
        { cwd: path.join(__dirname, '..'), encoding: 'utf8', stdio: 'pipe', env: childEnv }
      );
    } catch (err) {
      failed = true;
      output = (err.stdout || '') + (err.stderr || '');
    }

    assert.ok(failed, 'the BUG-685-MONEY end-to-end test must FAIL against a self-heal-disabled revert — proves the test can fail');
    assert.match(output, /not ok|fail/i, 'child test run output reports a failure against the disabled-fallback revert');
  } finally {
    // Restore the fixed file — scratch copy only, never git (GR#24).
    fs.copyFileSync(backupPath, ENGINE_PATH);
    fs.unlinkSync(backupPath);
  }

  const restored = fs.readFileSync(ENGINE_PATH, 'utf8');
  assert.equal(restored, original, 'engine.ts is restored byte-identical to the fixed version after the RED proof');
});

test('resolveDemandAll: determinism — dispatched twice from the same state yields byte-identical results', () => {
  const s = shortfallState(15_000);
  const r1 = reducer(s, { type: 'resolveDemandAll' });
  const r2 = reducer(s, { type: 'resolveDemandAll' });
  assert.deepEqual(r1.buildings, r2.buildings, 'identical placements from identical input (GR#21)');
  assert.equal(r1.funds, r2.funds);
  assert.equal(r1.placeNotice, r2.placeNotice);
});

// Integrity fix (BUG-606 independent round, Aaron 2026-09-03): this test was
// titled "journaled and replays identically" but never actually invoked a
// replay function — it only checked reducer purity + plan-count realisation.
// Retitled to describe what it actually proves; the REAL byte-identical
// genesis-replay proof (replayFromGenesis + replayIsDeterministic across a
// real journal, incl. a funds-capped PARTIAL resolveDemandAll and two
// consecutive dispatches) lives in attack/atk-replay.test.mjs, kept green and
// not duplicated here.
test('resolveDemandAll: pure reducer (no input mutation) — every plan item\'s count is fully realised under ample funds', () => {
  // RETUNE (this session, post-BUG-685 largest-first landing): see the
  // sibling "ample funds" test's comment above — the default £1bn is no
  // longer genuinely ample against a largest-first pick like Three Gorges
  // Dam (£5bn).
  const s = shortfallState(12_000, 1_000_000_000_000);
  const before = JSON.stringify(s);
  const result = reducer(s, { type: 'resolveDemandAll' });
  assert.equal(JSON.stringify(s), before, 'reducer must not mutate its input');
  assert.notDeepEqual(result.buildings, s.buildings, 'a real shortfall must actually place buildings');
  // Cross-check every plan item's own count is fully realised (no second
  // placement mechanism was introduced — placePlanItem() is the SAME helper
  // 'resolveDemand' uses). Not a check that EVERY new building's spec is a
  // plan spec: findSpot()/'place' may legitimately auto-append road/rail
  // connector tiles (autoConnect/autoBranchRail) when a site needs one, an
  // existing, unrelated side effect of the shared 'place' path.
  for (const item of orderedDemandFixPlan(s)) {
    const placed = countMixOf(result, item.mix) - countMixOf(s, item.mix);
    assert.equal(placed, item.count, `${item.serviceKey}'s mix must be placed exactly ${item.count} units under ample funds`);
    assert.ok(SPECS[item.specId], 'plan spec must be a real catalogue entry');
  }
});
