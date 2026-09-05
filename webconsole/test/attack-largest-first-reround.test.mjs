// attack-largest-first-reround.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND
// (GR#23, attacker != author) against BUG-685/BUG-686 after the first round's
// REJECT was answered with the affordability fall-through
// (largestFirstFill(..., budget) in data.ts) + the placePlanItem() self-heal
// in engine.ts.
//
// VERDICT: REJECT. The money mechanism itself now works — a poor city with an
// unaffordable headline plan DOES build something, deterministically, without
// double-spending or going negative (every GREEN test below pins that, and
// three source mutations were proven to red it). What it does NOT do is TELL
// THE TRUTH about what it built.
//
// LEAD DEFECT (RR-1, phantom asset in the success notice):
//   'resolveDemandAll' pushes `planLabel(plan)` into its "built ..." list
//   whenever `result.placed >= plan.count`. `plan` is the PURE, budget-blind
//   largestFirstFill() plan; `result.placed` is a count of whatever the
//   SELF-HEAL actually substituted. Those are two different mixes, and the
//   comparison is a bare BUILDING COUNT, so a self-heal that swaps one £5bn
//   Three Gorges Dam for five small plants satisfies 5 >= 1 and the game
//   reports, verbatim: "Fix All: built ... 1 x Three Gorges Dam". There is no
//   dam on the map. Reproduced with no mutation at all, on a stock unlockAll'd
//   200k-population city with £5bn (test RR-1).
//
// SECOND DEFECT (RR-2, silent substitution reads as complete success):
//   'resolveDemand' returns `result.state` with NO placeNotice at all whenever
//   `result.placed >= plan.count` — same count-vs-count comparison. A player
//   who clicks "Fix (1 x Three Gorges Dam)" with £50M gets 1 Onshore Wind Farm
//   + 2 Wind Turbines (72 MW against a 1,200 MW need, ~6% of the shortfall)
//   and is told nothing whatsoever. NOTE the direction of travel: with the
//   self-heal REMOVED (mutant M2) the same click honestly reports "Placed 0 of
//   1 x Three Gorges Dam — insufficient funds". The fix made the placement
//   better and the REPORTING strictly worse — the exact D2/GR#17 class the
//   surrounding code's own comments swear against.
//
// MUTATION GAP FOUND (RR-3): reverting DemandDock.tsx's confirm-cost gate to
//   the pre-BUG-685 monoculture (`Array(plan.count).fill(plan.specId)`) was
//   caught by NOTHING — all 20 tests across bug606-demanddock-ui.test.tsx,
//   demand-fix-ui.test.tsx and bug606-round2-fixes.test.tsx passed against the
//   mutant. Closed here by RR-3.
//
// The two REJECT reproductions are marked `todo` so this file can sit in a
// green tree: they RUN, they currently FAIL, and node reports them as todo
// rather than as suite failures. The fixer removes `{ todo: true }` when the
// notice is made honest; from then on they are ordinary regressions.
//
// MUTATIONS RUN (scratch-copy of the source file, restored byte-identical —
// GR#24, never a git command):
//   M1  data.ts largestFirstFill(): `maxAffordable` forced to Infinity (the
//       budget gate removed)            -> RR-4 reds (0 built instead of 3).
//   M2  engine.ts placePlanItem(): early `return result` (self-heal removed)
//                                       -> RR-4 reds (0 built instead of 3).
//   M3  DemandDock.tsx: both gates monocultured back to plan.specId
//                                       -> caught by nothing; RR-3 added.
// M1/M2 are re-run mechanically by RR-8 below so the red-proof is not a claim.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { runWithMutant } from '../testsupport/mutant.mjs';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, largestFirstFill, demandFixPlan, placementCost } from '../src/sim/data.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEBCONSOLE = path.join(__dirname, '..');

/** Aaron-shape fixture: everything unlocked, a real population-scaled
 *  shortfall, an empty map, and a treasury the attack chooses. */
function city(population, funds) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds, administrationState: null };
}

/** Every power candidate largestFirstFill() can see, largest-credited first —
 *  derived from SPECS (GR#15), never a hardcoded ladder. */
function powerSpecs() {
  return Object.values(SPECS)
    .filter((sp) => sp.kind === 'power' && (sp.mw ?? 0) > 0)
    .sort((a, b) => b.mw - a.mw);
}

function tallyKind(state, kind) {
  const out = {};
  for (const b of state.buildings) if (SPECS[b.spec]?.kind === kind) out[b.spec] = (out[b.spec] ?? 0) + 1;
  return out;
}

// ===========================================================================
// RR-1 (LEAD DEFECT, REJECT): the success notice names a building that does
// not exist.
// ===========================================================================
test('RR-1 REJECT: Fix All must never report building a spec of which ZERO were placed', () => {
  const s = city(200_000, 5_000_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan, 'the fixture must have a power shortfall');
  const headline = SPECS[plan.specId];
  assert.ok(headline.cost >= s.funds / 2, 'attack precondition: the headline spec is a treasury-dominating buy');

  const after = reducer(s, { type: 'resolveDemandAll' });
  const notice = after.placeNotice ?? '';
  const actuallyBuilt = after.buildings.filter((b) => b.spec === plan.specId).length;

  // The notice is the ONLY surface telling the player what Fix All did.
  assert.ok(
    !(notice.includes(headline.name) && actuallyBuilt === 0),
    `Fix All reported "${headline.name}" but placed ${actuallyBuilt} of them.\n` +
      `  notice: ${notice}\n` +
      `  power actually built: ${JSON.stringify(tallyKind(after, 'power'))}\n` +
      `  ROOT CAUSE: resolveDemandAll pushes planLabel(plan) when result.placed >= plan.count, ` +
      `comparing a SUBSTITUTED build's unit count against the PURE plan's unit count.`
  );
});

// ===========================================================================
// RR-2 (REJECT): a self-heal substitution is reported as unqualified success
// (no notice at all).
// ===========================================================================
test('RR-2 REJECT: a substituted (self-healed) Fix must not report as silent complete success', () => {
  const s = city(100_000, 50_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan, 'the fixture must have a power shortfall');
  const headline = SPECS[plan.specId];
  assert.ok(headline.cost > s.funds, 'attack precondition: the headline plan is flatly unaffordable');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  const built = tallyKind(after, 'power');
  const totalBuilt = Object.values(built).reduce((a, b) => a + b, 0);
  assert.ok(totalBuilt > 0, 'sanity: the self-heal did place something');
  assert.equal(after.buildings.filter((b) => b.spec === plan.specId).length, 0, 'sanity: the headline spec was NOT built');

  // Capacity delivered vs capacity asked for — derived, never hardcoded.
  const delivered = Object.entries(built).reduce((sum, [id, n]) => sum + (SPECS[id].mw ?? 0) * n, 0);
  assert.ok(delivered * 4 < plan.need - plan.have, 'attack precondition: the substitution barely dents the shortfall');

  assert.ok(
    after.placeNotice != null && after.placeNotice.length > 0,
    `a Fix that substituted ${JSON.stringify(built)} (${delivered} MW) for the promised ` +
      `"${plan.count} x ${headline.name}" against a ${plan.need - plan.have} MW gap reported NOTHING ` +
      `(placeNotice === null). With the self-heal removed the SAME click honestly says ` +
      `"Placed 0 of 1 x ${headline.name} — insufficient funds".`
  );
});

// ===========================================================================
// RR-3 (mutation gap M3 closed): the DemandDock confirm-cost gate must price
// the REAL largest-first mix, never a monoculture of the primary spec.
// ===========================================================================
test('RR-3: DemandDock confirm gate expands plan.mix (a monoculture revert is a materially different bill)', () => {
  const src = fs.readFileSync(path.join(WEBCONSOLE, 'src', 'components', 'left', 'DemandDock.tsx'), 'utf8');
  // Source pin — this is what mutation M3 reverts, and nothing else in the
  // suite noticed. Both call sites must expand the per-service mix.
  assert.match(
    src,
    /evaluatePlacementBatch\(\s*state,\s*plan\.mix\.flatMap\(/,
    'runResolveDemand must gate plan.mix, not Array(plan.count).fill(plan.specId)'
  );
  assert.match(
    src,
    /fixAllOrder\.flatMap\(\(p\) => p\.mix\.flatMap\(/,
    'runFixAll must expand every service\'s real mix, not a monoculture of its primary spec'
  );

  // Behavioural half: prove the two expansions are genuinely different bills,
  // so the source pin is protecting something real rather than a style.
  const s = city(800_000, 1_000_000_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan.mix.length > 1, 'fixture must produce a genuinely multi-spec mix');
  const mixIds = plan.mix.flatMap((m) => Array(m.count).fill(m.specId));
  const monoIds = Array(plan.count).fill(plan.specId);
  assert.equal(mixIds.length, monoIds.length, 'both expansions carry the same unit count — only the SPECS differ');
  const mixCost = mixIds.reduce((t, id) => t + placementCost(SPECS[id]), 0);
  const monoCost = monoIds.reduce((t, id) => t + placementCost(SPECS[id]), 0);
  assert.notEqual(mixCost, monoCost, 'the monoculture gate prices a build that never happens');
});

// ===========================================================================
// RR-4 (M1/M2 canary): the money mechanism itself — a poor city whose headline
// plan is unaffordable STILL builds. This is the single assertion both source
// mutations red.
// ===========================================================================
test('RR-4: an unaffordable headline plan still places real units via the affordability fall-through', () => {
  const s = city(100_000, 50_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(placementCost(SPECS[plan.specId]) > s.funds, 'precondition: headline unaffordable');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  const n = after.buildings.filter((b) => SPECS[b.spec]?.kind === 'power').length;
  assert.ok(n > 0, 'the fall-through must reach a cheaper, affordable spec instead of placing nothing');
  assert.ok(after.funds >= 0, 'never overspend');
});

// ===========================================================================
// RR-5: the affordability boundary grid (attack item 1).
// ===========================================================================
test('RR-5: budget exactly one densest unit takes it; one penny less falls through to smaller specs', () => {
  const s = city(100_000, 0);
  const specs = powerSpecs();
  const densest = specs[0];
  const shortfall = densest.mw; // exactly one-shot territory for the densest spec

  const exact = largestFirstFill({ ...s, funds: densest.cost }, 'power', shortfall, densest.cost);
  assert.equal(exact.length, 1, 'an exactly-affordable densest spec is a clean one-shot');
  assert.equal(exact[0].specId, densest.id);
  assert.equal(exact[0].count, 1);

  const penny = largestFirstFill({ ...s, funds: densest.cost - 1 }, 'power', shortfall, densest.cost - 1);
  assert.ok(penny.length > 0, 'one penny short must fall THROUGH, never strand the plan empty');
  assert.ok(
    penny.every((m) => m.specId !== densest.id),
    'the unaffordable densest spec must not appear in the fallen-through mix'
  );
  // Aaron's rule survives the fall-through: whatever IS affordable is still
  // taken densest-first.
  for (let i = 1; i < penny.length; i++) {
    assert.ok(penny[i - 1].unitCapacity >= penny[i].unitCapacity, 'fall-through mix stays largest-first');
  }
  const totalCost = penny.reduce((t, m) => t + m.planCost, 0);
  assert.ok(totalCost <= densest.cost - 1, 'the fall-through mix must fit inside the budget it was handed');
});

test('RR-5b: a budget below EVERY candidate yields an empty mix, and the reducer says why (never silent)', () => {
  const s = city(100_000, 0);
  const cheapest = powerSpecs().reduce((min, sp) => (sp.cost < min.cost ? sp : min));
  const broke = cheapest.cost - 1;
  const mix = largestFirstFill({ ...s, funds: broke }, 'power', 5000, broke);
  assert.deepEqual(mix, [], 'nothing affordable -> empty mix, not a stranded unaffordable pick');

  // GR#17: zero placement must be SURFACED, not swallowed.
  const after = reducer(city(100_000, broke), { type: 'resolveDemand', serviceKey: 'power' });
  assert.equal(after.buildings.filter((b) => SPECS[b.spec]?.kind === 'power').length, 0);
  assert.ok(after.placeNotice && /insufficient funds/i.test(after.placeNotice), 'a zero-placement Fix must report a reason');
});

test('RR-5c: DENSITY-FIRST holds even when the money would buy more capacity cheaply elsewhere (Aaron ruling pinned)', () => {
  // Attack: with a budget that affords exactly one of the densest spec, a
  // pure GBP-per-MW optimiser would buy the smaller plants instead (more MW
  // per pound). Aaron's ruling is the opposite; pin it so a future
  // "efficiency" refactor cannot quietly reinstate the carpet-the-map picker.
  const s = city(100_000, 0);
  const specs = powerSpecs();
  const densest = specs[0];
  const budget = densest.cost;
  const mix = largestFirstFill({ ...s, funds: budget }, 'power', densest.mw * 10, budget);
  assert.equal(mix[0].specId, densest.id, 'the densest affordable spec must lead');
  // "cheapest" here means most COST-EFFICIENT per MW (cost/mw ascending),
  // never "smallest raw mw" (that was `specs[specs.length - 1]` before the
  // civic-tier pricing-coherence pass, back when the smallest-capacity spec
  // also happened to be the cheapest-per-MW one). That pass retuned
  // pow_solar to the catalogue's actual cheapest-per-MW spec (GBP650k/MW)
  // WITHOUT it being the smallest-mw spec, so the old ascending-mw pick
  // (pow_wind, GBP1.2M/MW) no longer represents "the cheap monoculture" the
  // attack needs — derived from SPECS (GR#15), never a hardcoded id.
  const cheapest = [...specs].sort((a, b) => a.cost / a.mw - b.cost / b.mw)[0];
  const alternativeMw = Math.floor(budget / cheapest.cost) * cheapest.mw;
  assert.ok(alternativeMw > densest.mw, 'attack precondition: the cheap monoculture really would deliver more MW');
  const turbines = mix.filter((m) => m.specId === cheapest.id).reduce((t, m) => t + m.count, 0);
  const total = mix.reduce((t, m) => t + m.count, 0);
  assert.ok(turbines < total || total === 0, `"${cheapest.name}" must never be the entire plan while a denser spec is affordable`);
});

// ===========================================================================
// RR-6: money integrity across a whole Fix All batch (attack item 3).
// ===========================================================================
test('RR-6: Fix All never overspends, never double-spends, and every pound leaves a building behind', () => {
  for (const funds of [50_000_000, 500_000_000, 5_000_000_000]) {
    const s = city(200_000, funds);
    const after = reducer(s, { type: 'resolveDemandAll' });
    assert.ok(after.funds >= 0, `funds went negative at budget ${funds}`);
    const added = after.buildings.slice(s.buildings.length);
    const billed = added.reduce((t, b) => t + placementCost(SPECS[b.spec] ?? {}), 0);
    const spent = s.funds - after.funds;
    // Spent must ACCOUNT for the buildings placed. Auto-laid road connectors
    // are billed too, so spent >= billed; what must never happen is spending
    // MORE than the batch could possibly have cost, or charging for a
    // substituted mix twice (the self-heal walks walkMix a second time).
    assert.ok(spent >= billed, `budget ${funds}: spent ${spent} < billed ${billed}`);
    assert.ok(spent <= funds, `budget ${funds}: spent more than the treasury held`);
    // No double-charge: the self-heal's retry must not bill the abandoned
    // first walk. The first walk placed nothing (that is its precondition), so
    // spend attributable to non-road buildings equals their catalogue bill.
    const nonRoad = added.filter((b) => !SPECS[b.spec] || SPECS[b.spec].kind !== 'road');
    const nonRoadBill = nonRoad.reduce((t, b) => t + placementCost(SPECS[b.spec] ?? {}), 0);
    assert.ok(spent - nonRoadBill >= 0 && spent - nonRoadBill < funds, `budget ${funds}: unexplained spend`);
  }
});

// ===========================================================================
// RR-7: determinism / replay safety of the self-heal path (attack item 6).
// The self-heal reads cur.funds, so it is a pure function of state; prove the
// SAME journal action from the SAME state is byte-identical, including the
// substituted mix and the notice.
// ===========================================================================
test('RR-7: the self-heal path is byte-identical on replay (GR#21)', () => {
  for (const [pop, funds] of [
    [100_000, 50_000_000],
    [200_000, 5_000_000_000],
    [300_000, 50_000_000],
  ]) {
    const s = city(pop, funds);
    const a = reducer(s, { type: 'resolveDemandAll' });
    const b = reducer(s, { type: 'resolveDemandAll' });
    assert.equal(JSON.stringify(a.buildings), JSON.stringify(b.buildings), `buildings diverged at pop ${pop}/£${funds}`);
    assert.equal(a.funds, b.funds, `funds diverged at pop ${pop}/£${funds}`);
    assert.equal(a.placeNotice, b.placeNotice, `notice diverged at pop ${pop}/£${funds}`);
    // Single-service Fix too — this is the one that actually self-heals.
    const c = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
    const d = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
    assert.equal(JSON.stringify(c.buildings), JSON.stringify(d.buildings));
    assert.equal(c.funds, d.funds);
  }
});

// ===========================================================================
// RR-7b: maxPerCity cannot be violated or exhausted-into-nothing by the
// self-heal (attack item 2).
// ===========================================================================
test('RR-7b: the self-heal never re-offers an exhausted maxPerCity spec and never loops', () => {
  const capped = powerSpecs().find((sp) => (sp.maxPerCity ?? 0) === 1);
  assert.ok(capped, 'fixture assumes at least one maxPerCity:1 power spec exists');
  let s = city(300_000, 1_000_000_000_000);
  s = reducer(s, { type: 'place', spec: capped.id, x: 5, y: 5 });
  assert.equal(s.buildings.filter((b) => b.spec === capped.id).length, 1);
  const mix = largestFirstFill(s, 'power', 100_000, s.funds);
  assert.equal(mix.filter((m) => m.specId === capped.id).length, 0, 'exhausted cap must not reappear');
  assert.ok(mix.length > 0, 'the plan must not blank just because the capped spec is gone');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  assert.equal(after.buildings.filter((b) => b.spec === capped.id).length, 1, 'maxPerCity must hold through the fix path');
});

// ===========================================================================
// RR-8: MECHANICAL RED-PROOF of M1 and M2 — mutate the real source on a
// scratch copy (GR#24: cp/restore, never a git command), re-run RR-4's exact
// assertion in a child process, and require it to FAIL. Restores the file
// byte-identically and verifies the restore.
// ===========================================================================
const CANARY = `
import { initialState, reducer } from './src/sim/engine.ts';
import { SPECS } from './src/sim/data.ts';
const b = initialState();
const s = { ...b, population: 100000, unlockedAll: true, funds: 50000000, administrationState: null };
const a = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
const n = a.buildings.filter((x) => SPECS[x.spec] && SPECS[x.spec].kind === 'power').length;
console.log('POWER_BUILT=' + n);
`;

function runCanary() {
  const probe = path.join(WEBCONSOLE, `zz-reround-canary-${process.pid}.mjs`);
  fs.writeFileSync(probe, CANARY);
  try {
    const out = execFileSync(process.execPath, ['--experimental-strip-types', probe], {
      cwd: WEBCONSOLE,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    return Number(/POWER_BUILT=(\d+)/.exec(out)?.[1] ?? -1);
  } finally {
    fs.rmSync(probe, { force: true });
  }
}

// BUG-739: mutation now runs against a private webconsole/test/helpers/
// mutant.mjs shadow copy of webconsole/src, never the real, shared
// data.ts/engine.ts — the shadow-relative canary below mirrors CANARY above
// but imports via './sim/...' (the shadow root mirrors src/ directly) rather
// than './src/sim/...' (which is what a script at the real webconsole root
// needs).
const MUTANT_CANARY = `
import { initialState, reducer } from './sim/engine.ts';
import { SPECS } from './sim/data.ts';
const b = initialState();
const s = { ...b, population: 100000, unlockedAll: true, funds: 50000000, administrationState: null };
const a = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
const n = a.buildings.filter((x) => SPECS[x.spec] && SPECS[x.spec].kind === 'power').length;
console.log('POWER_BUILT=' + n);
`;

function mutateAndProve(srcRelPath, find, replace, label) {
  const out = runWithMutant({
    targetRelPath: srcRelPath,
    mutate: (original) => {
      assert.ok(original.includes(find), `${label}: mutation anchor not found — the fix moved, re-target this proof`);
      return original.replace(find, replace);
    },
    childBody: MUTANT_CANARY,
    extraArgs: ['--experimental-strip-types'],
  });
  const mutantBuilt = Number(/POWER_BUILT=(\d+)/.exec(out)?.[1] ?? -1);
  assert.equal(mutantBuilt, 0, `${label}: the mutant still built ${mutantBuilt} units — RR-4 does NOT red on it`);
}

test('RR-8 (M1 red-proof): removing the affordability gate in largestFirstFill() makes the poor city build ZERO', () => {
  assert.ok(runCanary() > 0, 'baseline: unmutated source must build something');
  mutateAndProve(
    path.join('sim', 'data.ts'),
    'const maxAffordable = c.unitCost > 0 ? Math.floor(fundsRemaining / c.unitCost) : Infinity;',
    'const maxAffordable = Infinity; // MUTANT M1',
    'M1'
  );
  assert.ok(runCanary() > 0, 'restore: source must be back to green behaviour');
});

test('RR-8 (M2 red-proof): removing the placePlanItem() self-heal makes the poor city build ZERO', () => {
  mutateAndProve(
    path.join('sim', 'engine.ts'),
    '  const result = walkMix(cur, item.mix, item.count, unitCap);',
    '  const result = walkMix(cur, item.mix, item.count, unitCap);\n  if (true) return result; // MUTANT M2',
    'M2'
  );
  assert.ok(runCanary() > 0, 'restore: source must be back to green behaviour');
});

// ===========================================================================
// RR-9 (MUTATION for THIS round's own fix, 2026-09-05): reverting
// 'resolveDemandAll's built.push() back to the pre-fix bare-COUNT comparison
// (result.placed >= plan.count, ignoring builtMixMatchesPlan) must reproduce
// RR-1's exact phantom-asset defect. Proves RR-1 is pinned by a REAL
// assertion, not something that would pass either way.
// ===========================================================================
const CANARY_RR1 = `
import { initialState, reducer } from './src/sim/engine.ts';
import { SPECS, demandFixPlan } from './src/sim/data.ts';
const b = initialState();
const s = { ...b, population: 200000, unlockedAll: true, funds: 5000000000, administrationState: null };
const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
const headline = SPECS[plan.specId];
const after = reducer(s, { type: 'resolveDemandAll' });
const notice = after.placeNotice ?? '';
const actuallyBuilt = after.buildings.filter((x) => x.spec === plan.specId).length;
console.log('PHANTOM=' + ((notice.includes(headline.name) && actuallyBuilt === 0) ? 1 : 0));
`;

function runRR1Canary() {
  const probe = path.join(WEBCONSOLE, `zz-reround-rr1-canary-${process.pid}.mjs`);
  fs.writeFileSync(probe, CANARY_RR1);
  try {
    const out = execFileSync(process.execPath, ['--experimental-strip-types', probe], {
      cwd: WEBCONSOLE,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    return Number(/PHANTOM=(\d+)/.exec(out)?.[1] ?? -1);
  } finally {
    fs.rmSync(probe, { force: true });
  }
}

// Shadow-relative mirror of CANARY_RR1 (BUG-739: './sim/...' against the
// shadow root that mirrors src/ directly, not './src/sim/...' against the
// real webconsole root) — the real, shared engine.ts is never written to.
const MUTANT_CANARY_RR1 = `
import { initialState, reducer } from './sim/engine.ts';
import { SPECS, demandFixPlan } from './sim/data.ts';
const b = initialState();
const s = { ...b, population: 200000, unlockedAll: true, funds: 5000000000, administrationState: null };
const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
const headline = SPECS[plan.specId];
const after = reducer(s, { type: 'resolveDemandAll' });
const notice = after.placeNotice ?? '';
const actuallyBuilt = after.buildings.filter((x) => x.spec === plan.specId).length;
console.log('PHANTOM=' + ((notice.includes(headline.name) && actuallyBuilt === 0) ? 1 : 0));
`;

test('RR-9 (mutation of THIS round\'s fix): reverting to a bare-count comparison reproduces the RR-1 phantom asset', () => {
  assert.equal(runRR1Canary(), 0, 'baseline: the fixed source must never report a phantom asset');
  const find =
    "built.push(matchesPlan && result.placed >= plan.count ? planLabel(plan) : builtMixLabel(result.builtMix));";
  const mutatedLine = "built.push(result.placed >= plan.count ? planLabel(plan) : builtMixLabel(result.builtMix)); // MUTANT RR-9";
  const out = runWithMutant({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      assert.ok(original.includes(find), 'RR-9: mutation anchor not found — the RR-1 fix moved, re-target this proof');
      return original.replace(find, mutatedLine);
    },
    childBody: MUTANT_CANARY_RR1,
    extraArgs: ['--experimental-strip-types'],
  });
  assert.equal(Number(/PHANTOM=(\d+)/.exec(out)?.[1] ?? -1), 1, 'RR-9: the mutant (bare-count comparison) must reproduce the RR-1 phantom asset');
  assert.equal(runRR1Canary(), 0, 'restore: source must be back to green (no phantom asset) behaviour');
});
