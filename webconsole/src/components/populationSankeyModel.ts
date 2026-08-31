// FEAT-1972079925 — pure data-shaping for the population Sankey. Split out of
// PopulationSankey.tsx (which has JSX) so plain `node --test` .mjs specs can
// import this model directly without a JSX transform — the model's
// correctness is independent of the SVG rendering.

import type { MonthlyDemographics } from '../sim/types.ts';

export type SankeyWindow = 'month' | 'year';

export interface SankeyFlows {
  births: number;
  moveIns: number;
  deaths: number;
  moveOuts: number;
  totalIn: number;
  totalOut: number;
  monthsCovered: number;
  empty: boolean;
}

/**
 * Sums the recorded monthly flows over the selected trailing window
 * ('month' = last closed month, 'year' = last 12). GR#15: when there is no
 * recorded history, this returns an explicit empty result — never a
 * fabricated split.
 */
export function demographicSankeyModel(
  history: MonthlyDemographics[] | undefined,
  windowSel: SankeyWindow
): SankeyFlows {
  const monthsWanted = windowSel === 'month' ? 1 : 12;
  const slice = (history ?? []).slice(-monthsWanted);
  if (slice.length === 0) {
    return {
      births: 0,
      moveIns: 0,
      deaths: 0,
      moveOuts: 0,
      totalIn: 0,
      totalOut: 0,
      monthsCovered: 0,
      empty: true,
    };
  }
  let births = 0;
  let moveIns = 0;
  let deaths = 0;
  let moveOuts = 0;
  for (const m of slice) {
    births += m.births;
    moveIns += m.moveIns;
    deaths += m.deaths;
    moveOuts += m.moveOuts;
  }
  return {
    births,
    moveIns,
    deaths,
    moveOuts,
    totalIn: births + moveIns,
    totalOut: deaths + moveOuts,
    monthsCovered: slice.length,
    empty: false,
  };
}
