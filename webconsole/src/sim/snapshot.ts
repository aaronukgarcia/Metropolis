// snapshot.ts — FEAT-1972079880: enriched debug snapshot builder.
//
// Pure (no React, no Date.now inside) so `node --test` exercises the exact
// shipped logic. Everything here is derived from state the sim already exposes
// cheaply — no new bookkeeping. Human-facing figures go through the shared
// fmtNum/fmtMoney/fmtSigned formatters so the snapshot reads in the console's
// one number style.

import type { SimState, ZoneKind } from './types.ts';
import {
  countByKind,
  residentsCapacity,
  onlineResidentsCapacity,
  underConstructionResidents,
  totalJobs,
  powerStats,
  SPECS,
} from './data.ts';
import {
  levelOf,
  xpForLevel,
  approvalOf,
  wellbeingOf,
  SPEED_MS,
  HISTORY_CAP,
  LEDGER_CAP,
} from './engine.ts';
import { fmtMoney, fmtNum, fmtSigned, formatPower } from './utils.ts';

export interface DebugSnapshot {
  clock: {
    tick: number;
    speed: SimState['speed'];
    /** ticks per real-time second at the current speed, e.g. "2.4/s" ("paused" at speed 0). */
    tickRate: string;
  };
  money: {
    funds: string;
    loanBalance: string;
    incomePerTick: string;
    expensePerTick: string;
    /** net funds delta extrapolated to a real-time minute at current speed */
    fundsDeltaPerMin: string;
  };
  progress: {
    level: number;
    xp: string;
    xpToNext: string;
  };
  entities: {
    population: string;
    /** BUG-417: ONLINE residential capacity (honest headline). */
    housingCapacity: string;
    /** BUG-417: capacity still under construction (gross − online); omitted when 0. */
    housingCapacityUnderConstruction?: string;
    /** BUG-417: gross residential capacity incl. offline dwellings. */
    housingCapacityGross: string;
    jobs: string;
    powerMw: string; // "cap / need MW"
    buildingsTotal: string;
    /** structure counts by kind — only kinds actually present */
    buildingsByKind: Partial<Record<ZoneKind, number>>;
    specCatalogueSize: number;
  };
  caps: {
    /** rolling buffers, changelog-cap style: "n / cap" */
    history: string;
    ledger: string;
  };
  taxRates: SimState['taxRates'];
  policiesOn: string[];
  approval: number;
  wellbeing: number;
}

/** Build the enriched, display-formatted debug snapshot from sim state. */
export function buildDebugSnapshot(s: SimState): DebugSnapshot {
  const ms = SPEED_MS[s.speed];
  const ticksPerSec = ms > 0 ? 1000 / ms : 0;
  const income = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const perMin = Math.round((income - expense) * ticksPerSec * 60);

  const counts = countByKind(s.buildings);
  const byKind: Partial<Record<ZoneKind, number>> = {};
  for (const [kind, n] of Object.entries(counts) as [ZoneKind, number][]) {
    if (n > 0) byKind[kind] = n;
  }

  const level = levelOf(s.xp);
  const next = xpForLevel(level + 1);
  const pw = powerStats(s);

  return {
    clock: {
      tick: s.tick,
      speed: s.speed,
      tickRate: ms > 0 ? `${(ticksPerSec).toFixed(1)}/s` : 'paused',
    },
    money: {
      funds: fmtMoney(s.funds),
      loanBalance: fmtMoney(s.loanBalance),
      incomePerTick: fmtMoney(income),
      expensePerTick: fmtMoney(expense),
      fundsDeltaPerMin: ms > 0 ? `${fmtSigned(perMin)}/min` : 'paused',
    },
    progress: {
      level,
      xp: fmtNum(s.xp),
      xpToNext: fmtNum(Math.max(0, next - s.xp)),
    },
    entities: {
      population: fmtNum(s.population),
      // BUG-417: honest headline = ONLINE capacity; gross + under-construction alongside.
      housingCapacity: fmtNum(onlineResidentsCapacity(s)),
      housingCapacityUnderConstruction: fmtNum(underConstructionResidents(s)),
      housingCapacityGross: fmtNum(residentsCapacity(s)),
      jobs: fmtNum(totalJobs(s)),
      powerMw: `${formatPower(pw.cap)} / ${formatPower(pw.need)}`,
      buildingsTotal: fmtNum(s.buildings.length),
      buildingsByKind: byKind,
      specCatalogueSize: Object.keys(SPECS).length,
    },
    caps: {
      history: `${fmtNum(s.history.length)} / ${fmtNum(HISTORY_CAP)}`,
      ledger: `${fmtNum(s.ledger.length)} / ${fmtNum(LEDGER_CAP)}`,
    },
    taxRates: s.taxRates,
    policiesOn: (Object.entries(s.policies) as [string, boolean][])
      .filter(([, on]) => on)
      .map(([id]) => id),
    approval: approvalOf(s),
    wellbeing: wellbeingOf(s).overall,
  };
}
