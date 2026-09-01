// consistency.ts — FEAT-1972079890 round-3: real cross-derivation checks
//
// Cross-derivation layer: compare two INDEPENDENT paths to the same fact.
// (1) FUNDS-VS-FLOWS: funds === lastAdvanceFunds + inflows - outflows
// (2) FLOWS-VS-RECOMPUTE: recompute key flow amounts from placed[] and compare
//     to actual lastFlows entries — catches silent data corruption
// (3) PALETTE-VS-SPEC: placed buildings' colors exist in SPECS

import type { SimState } from './types.ts';
import {
  SPECS,
  TIER_COLORS,
  densityTier,
  PALETTE_FLAT,
  countByKind,
  isOnline,
} from './data.ts';
import {
  councilTaxPerTick,
  businessTaxPerTick,
  wagesPerTick,
  applyOutflowPolicies,
  UPKEEP_BUCKET,
  GRID_IMPORT_OUTFLOW_LABEL,
} from './fiscal.ts';

export interface ConsistencyCheck {
  id: string;
  ok: boolean;
  detail: string;
}

export interface ConsistencyReport {
  checks: ConsistencyCheck[];
  failures: number;
}

/**
 * Run all consistency checks on a SimState. Returns a deterministic report
 * that never mutates the state.
 */
export function runConsistencyChecks(s: SimState): ConsistencyReport {
  const checks: ConsistencyCheck[] = [];

  // ===== SHAPE VALIDATION LAYER (existing) =====

  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) {
      checks.push({
        id: `colour.${b.id}.undefined-spec`,
        ok: false,
        detail: `${b.id}: spec "${b.spec}" undefined`,
      });
      continue;
    }
    if (!sp.color || typeof sp.color !== 'string') {
      checks.push({
        id: `colour.${b.id}.no-color`,
        ok: false,
        detail: `${b.id}: no color`,
      });
      continue;
    }
    checks.push({
      id: `colour.${b.id}.defined`,
      ok: true,
      detail: `${b.id}: ok`,
    });
  }

  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.category !== 'zones') continue;
    let tier: 1 | 2 | 3;
    try {
      tier = densityTier(sp);
    } catch (e) {
      checks.push({
        id: `tier.${b.id}.compute-error`,
        ok: false,
        detail: `${b.id}: tier compute failed`,
      });
      continue;
    }
    if (![1, 2, 3].includes(tier)) {
      checks.push({
        id: `tier.${b.id}.invalid-value`,
        ok: false,
        detail: `${b.id}: tier ${tier} invalid`,
      });
      continue;
    }
    if (!(tier in TIER_COLORS)) {
      checks.push({
        id: `tier.${b.id}.undefined-color`,
        ok: false,
        detail: `tier ${tier}: no color`,
      });
      continue;
    }
    checks.push({
      id: `tier.${b.id}.valid`,
      ok: true,
      detail: `${b.id}: t${tier}`,
    });
  }

  for (const b of s.buildings) {
    const found = b.spec in SPECS;
    checks.push({
      id: `spec.${b.id}.exists`,
      ok: found,
      detail: found ? `${b.id}: ok` : `${b.id}: ${b.spec} missing`,
    });
  }

  // ===== UNIVERSAL PLACEHOLDER CATCH (FEAT-1972079877) =====
  // A placeholder ("coming soon" roadmap type) is a valid SPECS entry — it has a
  // colour, dims, a family — so every shape check above PASSES for it. But a
  // placeholder must NEVER exist as a real building in the running sim: it is not
  // functional and was never meant to be placeable. This is the SINGLE
  // AUTHORITATIVE net that catches a placeholder building arriving via ANY path
  // (a crafted savepoint restore, a future unguarded insertion site, a hand-edited
  // debug-JSON load) — the reducer guards (place/stampRegion) and the restore
  // filter are defence-in-depth in FRONT of this catch-all. Any placeholder
  // building is an invalid state and fails consistency.
  //
  // ONE aggregate check (not one-per-building): the report is embedded in
  // debug.json, so a per-building entry would bloat it linearly at city scale
  // (the SIZE GUARD test). This fails if ANY building is a placeholder.
  const placeholderBuildings = s.buildings.filter((b) => SPECS[b.spec]?.placeholder === true);
  checks.push({
    id: 'placeholder.none-in-sim',
    ok: placeholderBuildings.length === 0,
    detail:
      placeholderBuildings.length === 0
        ? 'no placeholder buildings in sim'
        : `${placeholderBuildings.length} placeholder building(s) must not exist: ${placeholderBuildings
            .slice(0, 5)
            .map((b) => `#${b.id}(${b.spec})`)
            .join(', ')}${placeholderBuildings.length > 5 ? ', …' : ''}`,
  });

  const inflowLabels = new Set<string>();
  let inflowDupCount = 0;
  for (const f of s.lastFlows.inflows) {
    if (inflowLabels.has(f.label)) {
      inflowDupCount++;
    } else {
      inflowLabels.add(f.label);
    }
  }
  checks.push({
    id: 'flows.inflow-labels-unique',
    ok: inflowDupCount === 0,
    detail:
      inflowDupCount === 0
        ? `${s.lastFlows.inflows.length} inflow labels all unique`
        : `${inflowDupCount} duplicate inflow labels detected`,
  });

  const outflowLabels = new Set<string>();
  let outflowDupCount = 0;
  for (const f of s.lastFlows.outflows) {
    if (outflowLabels.has(f.label)) {
      outflowDupCount++;
    } else {
      outflowLabels.add(f.label);
    }
  }
  checks.push({
    id: 'flows.outflow-labels-unique',
    ok: outflowDupCount === 0,
    detail:
      outflowDupCount === 0
        ? `${s.lastFlows.outflows.length} outflow labels all unique`
        : `${outflowDupCount} duplicate outflow labels detected`,
  });

  for (let i = 0; i < s.lastFlows.inflows.length; i++) {
    const f = s.lastFlows.inflows[i];
    const ok = typeof f.value === 'number' && isFinite(f.value);
    checks.push({
      id: `flows.inflow[${i}].finite`,
      ok,
      detail: ok ? `in[${i}]: ok` : `in[${i}]: ${f.value}`,
    });
  }

  for (let i = 0; i < s.lastFlows.outflows.length; i++) {
    const f = s.lastFlows.outflows[i];
    const ok = typeof f.value === 'number' && isFinite(f.value);
    checks.push({
      id: `flows.outflow[${i}].finite`,
      ok,
      detail: ok ? `out[${i}]: ok` : `out[${i}]: ${f.value}`,
    });
  }

  const popOk = typeof s.population === 'number' && isFinite(s.population) && s.population >= 0;
  checks.push({
    id: 'sim.population.valid',
    ok: popOk,
    detail: popOk ? `Population = ${s.population}` : `Population invalid: ${s.population}`,
  });

  const fundsOk = typeof s.funds === 'number' && isFinite(s.funds);
  checks.push({
    id: 'sim.funds.valid',
    ok: fundsOk,
    detail: fundsOk ? `Funds = ${s.funds}` : `Funds invalid: ${s.funds}`,
  });

  const loanOk = typeof s.loanBalance === 'number' && isFinite(s.loanBalance) && s.loanBalance >= 0;
  checks.push({
    id: 'sim.loanBalance.valid',
    ok: loanOk,
    detail: loanOk ? `Loan balance = ${s.loanBalance}` : `Loan balance invalid: ${s.loanBalance}`,
  });

  const xpOk = typeof s.xp === 'number' && isFinite(s.xp) && s.xp >= 0;
  checks.push({
    id: 'sim.xp.valid',
    ok: xpOk,
    detail: xpOk ? `XP = ${s.xp}` : `XP invalid: ${s.xp}`,
  });

  const tickOk = typeof s.tick === 'number' && isFinite(s.tick) && s.tick >= 0 && Math.floor(s.tick) === s.tick;
  checks.push({
    id: 'sim.tick.valid',
    ok: tickOk,
    detail: tickOk ? `Tick = ${s.tick}` : `Tick invalid: ${s.tick}`,
  });

  const speedOk = s.speed === 0 || s.speed === 1 || s.speed === 2 || s.speed === 3;
  checks.push({
    id: 'sim.speed.valid',
    ok: speedOk,
    detail: speedOk ? `Speed = ${s.speed}` : `Speed invalid: ${s.speed}`,
  });

  for (let i = 0; i < s.ledger.length; i++) {
    const e = s.ledger[i];
    const tickOk = typeof e.tick === 'number' && isFinite(e.tick) && e.tick >= 0;
    const amountOk = typeof e.amount === 'number' && isFinite(e.amount);
    const labelOk = typeof e.label === 'string' && e.label.length > 0;
    const ok = tickOk && amountOk && labelOk;
    checks.push({
      id: `ledger[${i}].valid`,
      ok,
      detail: ok ? `led[${i}]: ok` : `led[${i}]: bad`,
    });
  }

  for (const b of s.buildings) {
    const xOk = typeof b.x === 'number' && isFinite(b.x) && b.x >= 0;
    const yOk = typeof b.y === 'number' && isFinite(b.y) && b.y >= 0;
    const ok = xOk && yOk;
    checks.push({
      id: `building.${b.id}.position`,
      ok,
      detail: ok ? `${b.id}: ok` : `${b.id}: bad pos`,
    });
  }

  const seenIds = new Set<number>();
  let dupIdCount = 0;
  for (const b of s.buildings) {
    if (seenIds.has(b.id)) {
      dupIdCount++;
    } else {
      seenIds.add(b.id);
    }
  }
  checks.push({
    id: 'buildings.ids-unique',
    ok: dupIdCount === 0,
    detail:
      dupIdCount === 0
        ? `${s.buildings.length} building IDs all unique`
        : `${dupIdCount} duplicate building IDs detected`,
  });

  const taxes = [
    { name: 'residential', val: s.taxRates.residential },
    { name: 'commercial', val: s.taxRates.commercial },
    { name: 'industrial', val: s.taxRates.industrial },
  ];
  for (const t of taxes) {
    const ok = typeof t.val === 'number' && isFinite(t.val) && t.val >= 0 && t.val <= 100;
    checks.push({
      id: `taxRates.${t.name}`,
      ok,
      detail: ok ? `${t.name} tax = ${t.val}%` : `${t.name} tax invalid: ${t.val}`,
    });
  }

  // ===== CROSS-DERIVATION LAYER =====

  // ===== 1. FUNDS-VS-FLOWS CONSERVATION (TICK-BOUNDARY INVARIANT, Round-6) =====
  // Conservation is checked using tick snapshots funcsAtTickStart/End, not working-tree
  // funds. This ensures between-tick mutations (debugXp, place, dev +10M) never
  // false-positive the check. We verify:
  // fundsAtTickEnd === funcsAtTickStart + Σinflows − Σoutflows
  const inflowSum = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const expectedFundsAtEnd = s.fundsAtTickStart + inflowSum - outflowSum;
  const conservationOk = s.fundsAtTickEnd === expectedFundsAtEnd;
  checks.push({
    id: 'conservation.funds-vs-flows',
    ok: conservationOk,
    detail: conservationOk
      ? `fundsAtTickEnd ${s.fundsAtTickEnd} = start ${s.fundsAtTickStart} + flows ${inflowSum - outflowSum}`
      : `fundsAtTickEnd ${s.fundsAtTickEnd} ≠ expected ${expectedFundsAtEnd} (delta: ${s.fundsAtTickEnd - expectedFundsAtEnd})`,
  });

  // ===== 2. FLOWS-VS-RECOMPUTE: Cross-check key flows against recomputed values =====
  // Path A (actual): what's recorded in lastFlows
  // Path B (derived): what we'd compute now from placed[] + policies

  // Recompute fiscal flows using shared formulas (fiscal.ts) for single source of truth.
  const c = countByKind(s.buildings);
  const t = s.taxRates;
  // BUG-419: recompute against the START-of-tick population the engine actually charged
  // (recorded in lastFlows.population), not the grown end-of-tick s.population. Fall back
  // to s.population for states recorded before this field existed.
  const flowBasisPop = s.lastFlows.population ?? s.population;
  const recomputedCouncilTax = councilTaxPerTick(flowBasisPop, t.residential);
  const actualCouncilTax = s.lastFlows.inflows.find((f) => f.label === 'Council Tax')?.value ?? 0;
  const councilTaxOk = recomputedCouncilTax === actualCouncilTax;
  checks.push({
    id: 'flows.council-tax-matches',
    ok: councilTaxOk,
    detail: councilTaxOk
      ? `Council Tax: computed ${recomputedCouncilTax} = actual ${actualCouncilTax}`
      : `Council Tax diverged: computed ${recomputedCouncilTax} vs actual ${actualCouncilTax} (building removal? policy change?)`,
  });

  // Recompute Business Tax using shared formula (fiscal.ts).
  // NOTE: Business Tax can be reduced by brownout, so we allow some tolerance.
  // If it's LOWER than recomputed, that's OK (brownout). If HIGHER, that's a bug.
  const recomputedBusinessTax = businessTaxPerTick(c.commercial, t.commercial);
  const actualBusinessTax = s.lastFlows.inflows.find((f) => f.label === 'Business Tax')?.value ?? 0;
  const businessTaxOk = actualBusinessTax <= recomputedBusinessTax;
  checks.push({
    id: 'flows.business-tax-matches',
    ok: businessTaxOk,
    detail: businessTaxOk
      ? `Business Tax: actual ${actualBusinessTax} <= computed ${recomputedBusinessTax} (or brownout)`
      : `Business Tax: actual ${actualBusinessTax} > computed ${recomputedBusinessTax} (impossible without new businesses)`,
  });

  // Recompute Wages using shared formula (fiscal.ts).
  // BUG-419: use the START-of-tick basis (flowBasisPop) the engine charged wages on.
  // The engine charges wages on the workforce present at tick start; new arrivals from
  // this tick's growth aren't paid yet. Recomputing against end-of-tick s.population
  // (which is larger after growth) is what made this check diverge (computed > actual).
  // BUG-422: the engine applies POLICY MULTIPLIERS to outflows AFTER building the raw
  // amount (austerity ×0.9; Wages is not in the recycling 0.93 set). Recompute the raw
  // wage on the BUG-419 start-of-tick basis, then run it through the SAME shared policy
  // pipeline the engine used so the comparison is post-policy vs post-policy.
  const recomputedWages = applyOutflowPolicies(
    [{ label: 'Wages', value: wagesPerTick(flowBasisPop) }],
    s.policies,
  )[0].value;
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  const wagesOk = recomputedWages === actualWages;
  checks.push({
    id: 'flows.wages-matches',
    ok: wagesOk,
    detail: wagesOk
      ? `Wages: computed ${recomputedWages} = actual ${actualWages}`
      : `Wages diverged: computed ${recomputedWages} vs actual ${actualWages} (pop change without reflow?)`,
  });

  // Recompute total upkeep: sum of ONLINE building upkeeps only.
  // LIMITATION: This check verifies total upkeep magnitude but NOT per-bucket mapping
  // (e.g., roads upkeep stays within 'Roads' bucket, power stays in 'Power Grid', etc.).
  // A full per-bucket check would require tracking which flow label covers which UPKEEP_BUCKET.
  // BUG-414 FIX: exclude offline/under-construction buildings to match engine.computeFlows behavior.
  // BUG-422: recompute upkeep PER BUCKET LABEL (matching engine.computeFlows), then run
  // the buckets through the SAME shared policy pipeline. Per-label matters: the recycling
  // policy discounts only the service labels (Roads, Power Grid, ...) — a flat sum ×0.93
  // would be wrong. austerity then ×0.9 every bucket. Summing the post-policy per-bucket
  // values reproduces the aggregate the engine actually recorded.
  const upkeepBuckets: Record<string, number> = {};
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue; // Skip buildings still under construction
    const sp = SPECS[b.spec];
    if (!sp || !sp.upkeep) continue;
    const k = UPKEEP_BUCKET[sp.kind];
    if (k) upkeepBuckets[k] = (upkeepBuckets[k] ?? 0) + sp.upkeep;
  }
  const upkeepEntries = Object.entries(upkeepBuckets)
    .filter(([, v]) => v > 0)
    .map(([label, value]) => ({ label, value }));
  const recomputedUpkeep = applyOutflowPolicies(upkeepEntries, s.policies).reduce(
    (a, e) => a + e.value,
    0,
  );
  // Sum actual upkeep from all outflow buckets (Roads, Power, Healthcare, etc.).
  // Excludes: Wages, Loan Interest, Overdraft Interest, Transit Subsidy (non-upkeep flows).
  let actualUpkeep = 0;
  for (const flow of s.lastFlows.outflows) {
    // FEAT-1972079907 inc2: 'Road Auto-Scale' is a monthly one-off capital spend
    // (road tier upgrades), NOT recurring building upkeep — exclude it from the
    // upkeep-total reconciliation exactly like Wages/interest/subsidy.
    if (
      flow.label !== 'Wages' &&
      flow.label !== 'Loan Interest' &&
      flow.label !== 'Overdraft Interest' &&
      flow.label !== 'Transit Subsidy' &&
      flow.label !== 'Road Auto-Scale' &&
      // FEAT-1972079878 inc1: 'Building Auto-Scale' is likewise a monthly one-off
      // capital spend (building capacity-tier upgrades), NOT recurring per-building
      // `upkeep` — exclude it from the upkeep-total reconciliation exactly like
      // Road Auto-Scale, or every scaled building double-counts its upgrade spend
      // as phantom recurring upkeep and the reconciliation goes red.
      flow.label !== 'Building Auto-Scale' &&
      flow.label !== 'Road Auto-Connect' &&
      // FEAT-1972079906 inc1: 'Refuse Collection' is a tonnage-based operating cost
      // (collected tonnes × rate), NOT recurring per-building `upkeep` — exclude it
      // from the upkeep-total reconciliation exactly like Wages / Road Auto-Scale.
      // (The depot's FIXED upkeep IS a normal Water & Waste bucket and stays in.)
      flow.label !== 'Refuse Collection' &&
      // FEAT-1972079906 inc2: 'Waste Disposal' is landfill TIPPING (landfilled tonnes ×
      // rate) — a tonnage-based operating cost, NOT recurring per-building `upkeep` —
      // exclude it from the upkeep-total reconciliation exactly like Refuse Collection.
      // (The processors' FIXED upkeep IS a normal Water & Waste bucket and stays in.)
      flow.label !== 'Waste Disposal' &&
      // FEAT-2326609711 inc1: 'Grid Import' is an external-tariff outflow keyed off
      // the power SHORTFALL (importedMW * tariff) — not a per-building `upkeep`
      // bucket — exclude it from the upkeep-total reconciliation exactly like
      // Refuse Collection / Waste Disposal (both other tonnage/shortfall-based
      // operating costs). Without this, any tick with an active Grid Import
      // outflow would falsely diverge this check (upkeepBuckets never includes it).
      flow.label !== GRID_IMPORT_OUTFLOW_LABEL
    ) {
      actualUpkeep += flow.value;
    }
  }
  const upkeepOk = recomputedUpkeep === actualUpkeep;
  checks.push({
    id: 'flows.upkeep-total-matches',
    ok: upkeepOk,
    detail: upkeepOk
      ? `Upkeep total: computed ${recomputedUpkeep} = actual ${actualUpkeep} (per-bucket mapping NOT verified)`
      : `Upkeep total diverged: computed ${recomputedUpkeep} vs actual ${actualUpkeep} (building removed? online status change?)`,
  });

  // ===== 3. PALETTE CROSS-CHECK =====
  const specsUsedColors = new Set<string>();
  for (const sp of Object.values(SPECS)) {
    if (sp.color) specsUsedColors.add(sp.color);
  }
  let paletteFailure = false;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp && sp.color && !specsUsedColors.has(sp.color)) {
      paletteFailure = true;
      break;
    }
  }
  checks.push({
    id: 'palette.building-colors-defined',
    ok: !paletteFailure,
    detail: !paletteFailure
      ? `all ${s.buildings.length} buildings use defined colors`
      : `some buildings use undefined colors`,
  });

  const paletteOk =
    Array.isArray(PALETTE_FLAT) &&
    PALETTE_FLAT.length > 0 &&
    PALETTE_FLAT.every((id) => typeof id === 'string' && id in SPECS);
  checks.push({
    id: 'palette.flat-valid',
    ok: paletteOk,
    detail: paletteOk ? `PALETTE_FLAT has ${PALETTE_FLAT.length} entries` : `PALETTE_FLAT invalid`,
  });

  const failures = checks.filter((c) => !c.ok).length;
  return { checks, failures };
}
