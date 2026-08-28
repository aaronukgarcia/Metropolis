// wasteModel.ts — FEAT-1972079906 inc3 (FEAT-1972079864): display-shaping helper
// for the Waste RightDock panel. DISPLAY-ONLY: it reads the LANDED inc1/inc2
// derived functions (wasteStatsOf / processingMixOf / efwPowerOf / the revenue
// functions) and reshapes them for the tab. It computes NO new sim logic — every
// tonnage/£/MW comes straight out of an existing pure derived read, so the panel
// cannot drift from the economy or break replay/consistency.
//
// Pure and deterministic (no Date.now / Math.random): a pure function of SimState,
// mirroring how lineUsageOf feeds the Lines tab. The percentages are the ONLY new
// arithmetic and are pure presentation (share of the already-collected tonnage).

import type { SimState } from '../../sim/types.ts';
import {
  wasteStatsOf,
  processingMixOf,
  efwPowerOf,
  recyclingRevenueOf,
  compostRevenueOf,
} from '../../sim/data.ts';

/** One processor row of the processing mix (tonnes routed + its share of collected). */
export interface WasteMixRow {
  key: 'landfill' | 'efw' | 'mrf' | 'compost';
  label: string;
  tonnes: number;
  /** Share of COLLECTED tonnage, 0..1. Zero when nothing is collected (no NaN). */
  fraction: number;
  /** True for landfill — the sink that "total recycling" minimises (rendered red). */
  isSink: boolean;
}

/** Full display model for the Waste tab — all reads derived, nothing computed anew. */
export interface WasteDisplayModel {
  // Collection (inc1, wasteStatsOf)
  generated: number;
  capacity: number;
  collected: number;
  uncollected: number;
  /** min(1, capacity/generated); 1 when nothing generated. */
  coverage: number;
  /** True when refuse is left on the street (coverage < 1) — the red condition. */
  hasUncollected: boolean;
  // Processing (inc2, processingMixOf)
  mixRows: WasteMixRow[];
  /** diverted / collected, 0..1 — the headline total-recycling KPI. */
  diversionRate: number;
  diverted: number;
  landfilled: number;
  // Recovered (inc2)
  efwPowerMw: number;
  recyclingRevenue: number;
  compostRevenue: number;
  /** recycling + compost £ — the recovered-materials revenue total. */
  materialRevenue: number;
}

/**
 * Shape the landed waste reads for the Waste panel. Pure/deterministic. The four
 * mix fractions are shares of COLLECTED tonnage and therefore sum to 1 whenever
 * anything is collected (landfill = collected − diverted, so the split is exact),
 * and are all 0 when nothing is collected — never NaN/Infinity.
 */
export function wasteDisplayModel(s: SimState): WasteDisplayModel {
  const stats = wasteStatsOf(s);
  const mix = processingMixOf(s);
  const collected = mix.collected;
  const frac = (t: number) => (collected > 0 ? t / collected : 0);

  const mixRows: WasteMixRow[] = [
    { key: 'landfill', label: 'Landfill', tonnes: mix.landfill, fraction: frac(mix.landfill), isSink: true },
    { key: 'efw', label: 'Energy-from-Waste', tonnes: mix.efw, fraction: frac(mix.efw), isSink: false },
    { key: 'mrf', label: 'Recycling (MRF)', tonnes: mix.mrf, fraction: frac(mix.mrf), isSink: false },
    { key: 'compost', label: 'Compost', tonnes: mix.compost, fraction: frac(mix.compost), isSink: false },
  ];

  const recyclingRevenue = recyclingRevenueOf(s);
  const compostRevenue = compostRevenueOf(s);

  return {
    generated: stats.generated,
    capacity: stats.capacity,
    collected: stats.collected,
    uncollected: stats.uncollected,
    coverage: stats.coverage,
    hasUncollected: stats.uncollected > 0,
    mixRows,
    diversionRate: mix.diversionRate,
    diverted: mix.diverted,
    landfilled: mix.landfill,
    efwPowerMw: efwPowerOf(s),
    recyclingRevenue,
    compostRevenue,
    materialRevenue: recyclingRevenue + compostRevenue,
  };
}
