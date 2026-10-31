// PropertyCard.tsx — FEAT-2326609841: the Monopoly-style property card for a
// selected building INSTANCE. Presentation only: every figure is read from the
// SSOT derivations (Spec, placementCost, constructionTicks, upkeepChargeableOf,
// buildingUtility's water apportionment, BULLDOZE_REFUND_FRACTION). No literal
// copied into the component (GR#3/GR#15), no clock or storage read in render
// (BUG-602), no per-tick O(N) scan beyond the memoised selectors (AC-9).
//
// Monopoly grammar: coloured title band (spec.color, white name) with the
// category label, picture plate, ruled stat table, footer with cost / value.
// Network pieces get the slim variant (band, name, picture, cost/resale,
// upkeep, status) — no utility table.

import { useEffect, type ReactNode } from 'react';
import {
  SPECS,
  BULLDOZE_REFUND_FRACTION,
  placementCost,
  constructionTicks,
  upkeepChargeableOf,
  isOnline,
  isBrownoutActive,
  effectiveCleanWaterCoverageOf,
  effectiveWastewaterCoverageOf,
  type Spec,
} from '../sim/data';
import type { SimState, Building } from '../sim/types';
import { fmtMoney, fmtNum, formatPower } from '../sim/utils';
import { buildingWaterShareOf, buildingPowerOf } from '../sim/buildingUtility';
import { BuildingPicture } from './buildingMotifs';

export type MaturityState = 'construction' | 'operational' | 'consolidated';

export interface Maturity {
  state: MaturityState;
  /** Human label, e.g. "Under construction (3/12 days)". */
  label: string;
  /** construction only: ticks (days) elapsed / total. */
  elapsed?: number;
  total?: number;
  /** consolidated only: tier N (1-based). */
  tier?: number;
}

export interface CardRow {
  key: string;
  label: string;
  /** Rendered value; '—' means "not applicable", never a measured zero. */
  value: string;
}

export interface CardModel {
  specId: string;
  name: string;
  bandColor: string;
  categoryLabel: string;
  slim: boolean;
  /** placementCost(spec) as paid at placement. */
  cost: number;
  /** Resale = BULLDOZE_REFUND_FRACTION x placementCost (Aaron ruling 2). */
  value: number;
  maturity: Maturity;
  rows: CardRow[];
  badges: string[];
}

const CATEGORY_LABEL: Record<Spec['category'], string> = {
  network: 'Network',
  zones: 'Zone',
  services: 'Service',
};

const DASH = '—';

/** Maturity ladder (Aaron ruling 3): construction -> operational -> consolidated tier N. */
export function maturityOf(state: SimState, b: Building, sp: Spec): Maturity {
  if (b.builtTick != null) {
    const total = constructionTicks(sp);
    const elapsed = state.tick - b.builtTick;
    if (elapsed < total) {
      const done = Math.max(0, elapsed);
      return {
        state: 'construction',
        label: `Under construction (${done}/${total} days)`,
        elapsed: done,
        total,
      };
    }
  }
  if ((b.placedBy ?? 'player') === 'auto') {
    const tier = (b.capacityTier ?? 0) + 1;
    return { state: 'consolidated', label: `Consolidated tier ${tier}`, tier };
  }
  return { state: 'operational', label: 'Operational' };
}

/** Pure model: everything the card shows, derived from (state, building). */
export function cardModelOf(state: SimState, b: Building): CardModel | null {
  const sp = SPECS[b.spec];
  if (!sp) return null;
  const cost = placementCost(sp);
  const maturity = maturityOf(state, b, sp);
  const slim = sp.category === 'network';
  const online = isOnline(state, b);
  const rows: CardRow[] = [];
  const badges: string[] = [];

  // Upkeep ACTUALLY charged (genesis map pieces honestly show 0).
  rows.push({ key: 'upkeep', label: 'Upkeep', value: `${fmtMoney(upkeepChargeableOf(b, sp))}/tick` });

  if (!slim) {
    // Power: plants report generation (only once online — Q100092), everything
    // else its draw. Spec has no figure -> dash.
    const pw = buildingPowerOf(b);
    // Under construction nothing is generated or drawn yet (Q100092): dash.
    const building = maturity.state === 'construction';
    if (!pw) rows.push({ key: 'power', label: 'Power', value: DASH });
    else if (pw.kind === 'generation')
      rows.push({ key: 'power', label: 'Power generated', value: building ? DASH : formatPower(pw.mw) });
    else rows.push({ key: 'power', label: 'Power draw', value: building ? DASH : formatPower(pw.mw) });

    // Water / waste water: the building's apportioned share of the citywide
    // demand (buildingUtility). Non-housing draws none in that model -> dash.
    const share = buildingWaterShareOf(state, b);
    rows.push({
      key: 'water',
      label: 'Water consumption',
      value: share ? `${fmtNum(share.clean)} people supplied` : DASH,
    });
    rows.push({
      key: 'waste',
      label: 'Waste water',
      value: share ? `${fmtNum(share.waste)} people sewered` : DASH,
    });

    // Refuse: collection / processing capacity where the spec has it.
    const refuse =
      sp.wasteCapacity != null
        ? `collects ${fmtNum(sp.wasteCapacity)} t/tick`
        : sp.processCapacity != null
          ? `processes ${fmtNum(sp.processCapacity)} t/tick`
          : DASH;
    rows.push({ key: 'refuse', label: 'Refuse', value: refuse });

    // Live-state badges (AC-7): never silently wrong numbers.
    if (maturity.state !== 'construction') {
      const housing = sp.kind === 'residential';
      if (!online) badges.push('Offline');
      if (housing && online) {
        if (effectiveCleanWaterCoverageOf(state) < 1) badges.push('Water shortfall');
        if (effectiveWastewaterCoverageOf(state) < 1) badges.push('Sewage shortfall');
      }
      // A brownout bites only when isBrownoutActive (the SSOT: physical deficit
      // AND Grid Import off) and this building is an online power CONSUMER.
      if (pw?.kind === 'draw' && online && isBrownoutActive(state)) badges.push('Brownout');
    }
  } else if (maturity.state !== 'construction' && !online) {
    badges.push('Offline');
  }

  return {
    specId: sp.id,
    name: sp.name,
    bandColor: sp.color,
    categoryLabel: CATEGORY_LABEL[sp.category],
    slim,
    cost,
    value: Math.round(cost * BULLDOZE_REFUND_FRACTION),
    maturity,
    rows,
    badges,
  };
}

export function PropertyCard({
  state,
  building,
  onClose,
  headerActions,
  children,
}: {
  state: SimState;
  building: Building;
  onClose: () => void;
  headerActions?: ReactNode;
  children?: ReactNode;
}) {
  // AC-1: Escape closes. (MapView's global Escape handler still owns tool
  // cancel; both ending in "deselect" is harmless.)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const model = cardModelOf(state, building);
  const sp = SPECS[building.spec];
  if (!model || !sp) return null;

  return (
    <div
      className={`building-card property-card${model.slim ? ' pc-slim' : ''} pc-${sp.category}`}
      data-testid="property-card"
      data-spec={model.specId}
    >
      <header className="pc-band" style={{ background: model.bandColor }} data-testid="pc-band">
        <span className="pc-category" data-testid="pc-category">
          {model.categoryLabel}
        </span>
        <b className="pc-name" data-testid="pc-name">
          {model.name}
        </b>
        <span className="pc-actions">
          {headerActions}
          <button className="btn tiny" data-testid="pc-close" aria-label="Close property card" onClick={onClose}>
            Close
          </button>
        </span>
      </header>
      <BuildingPicture spec={sp} />
      <div className="pc-maturity" data-testid="pc-maturity" data-state={model.maturity.state}>
        {model.maturity.label}
        {model.maturity.state === 'construction' && model.maturity.total ? (
          <span
            className="pc-progress"
            style={{ width: `${Math.min(99, Math.round(((model.maturity.elapsed ?? 0) / model.maturity.total) * 100))}%` }}
          />
        ) : null}
      </div>
      {model.badges.length > 0 && (
        <div className="pc-badges" data-testid="pc-badges">
          {model.badges.map((t) => (
            <span key={t} className="pc-badge">
              {t}
            </span>
          ))}
        </div>
      )}
      <table className="pc-table">
        <tbody>
          {model.rows.map((r) => (
            <tr key={r.key} data-row={r.key}>
              <th>{r.label}</th>
              <td>{r.value}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <footer className="pc-footer">
        <span data-testid="pc-cost">
          <i>Cost</i> <b>{fmtMoney(model.cost)}</b>
        </span>
        <span data-testid="pc-value">
          <i>Value (resale)</i> <b>{fmtMoney(model.value)}</b>
        </span>
      </footer>
      <div className="pc-details">{children}</div>
    </div>
  );
}

/**
 * AC-7 / F6: the selected building left the world by a path other than the
 * bulldozer (consolidation, forced sale ...). Never show the stale click-time
 * copy as if it were live; say so and offer Close.
 */
export function PropertyCardGone({ name, onClose }: { name: string; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div className="building-card property-card pc-slim" data-testid="property-card" data-gone="true">
      <header className="pc-band" style={{ background: '#555b66' }}>
        <b className="pc-name">{name}</b>
        <span className="pc-actions">
          <button className="btn tiny" data-testid="pc-close" onClick={onClose}>
            Close
          </button>
        </span>
      </header>
      <div className="pc-maturity" data-testid="pc-maturity" data-state="gone">
        No longer exists (removed or consolidated)
      </div>
    </div>
  );
}
