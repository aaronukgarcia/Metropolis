// FEAT-1972079926 — pure data-shaping for the arrivals-by-mode Sankey. Split
// out of ArrivalsByModeSankey.tsx (which has JSX) so plain `node --test` .mjs
// specs can import this model directly without a JSX transform — mirrors
// populationSankeyModel.ts's split (FEAT-1972079925).

import type { MonthlyArrivalsByMode } from '../sim/types.ts';

export type SankeyWindow = 'month' | 'year';

export interface ArrivalsByModeFlows {
  road: number;
  railLow: number;
  railHs: number;
  sea: number;
  plane: number;
  totalIn: number;
  monthsCovered: number;
  empty: boolean;
}

/**
 * Sums the recorded monthly arrivals-by-mode split over the selected
 * trailing window ('month' = last closed month, 'year' = last 12). GR#15:
 * when there is no recorded history, this returns an explicit empty result
 * — never a fabricated split.
 */
export function arrivalsByModeSankeyModel(
  history: MonthlyArrivalsByMode[] | undefined,
  windowSel: SankeyWindow
): ArrivalsByModeFlows {
  const monthsWanted = windowSel === 'month' ? 1 : 12;
  const slice = (history ?? []).slice(-monthsWanted);
  if (slice.length === 0) {
    return {
      road: 0,
      railLow: 0,
      railHs: 0,
      sea: 0,
      plane: 0,
      totalIn: 0,
      monthsCovered: 0,
      empty: true,
    };
  }
  let road = 0;
  let railLow = 0;
  let railHs = 0;
  let sea = 0;
  let plane = 0;
  for (const m of slice) {
    road += m.road;
    railLow += m.railLow;
    railHs += m.railHs;
    sea += m.sea;
    plane += m.plane;
  }
  return {
    road,
    railLow,
    railHs,
    sea,
    plane,
    totalIn: road + railLow + railHs + sea + plane,
    monthsCovered: slice.length,
    empty: false,
  };
}
