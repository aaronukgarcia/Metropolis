import { SPECS, serviceDemandOf, findSpot, pickAutoSpec, isBrownoutActive, demandFixPlan, orderedDemandFixPlan, AUTO_BUILD_DEMAND_PERCENT } from '../../sim/data';
import { useSim } from '../../sim/simContext';
import { demandOf, levelOf } from '../../sim/engine';
import { useBusy } from '../Busy';
import { Panel } from '../Tabs';
import { formatBuildingCount, demandFixMessage } from '../demandFixUi';

export function DemandDock() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const demand = demandOf(state);
  const services = serviceDemandOf(state);
  const auto = pickAutoSpec(state);
  // FEAT-2326609728 inc2: per-service one-click fix. demandFixPlan(state) is
  // the SAME pure plan the MapView advisor prompt reads (SSOT, GR#3) — a row
  // here shows "Fix (N)" exactly when that service has a real, affordability-
  // uncapped shortfall AND an unlocked provider; keyed by serviceKey so each
  // row looks up its own plan entry (services.map below).
  const fixPlanByService = new Map(demandFixPlan(state).map((p) => [p.serviceKey, p]));
  // BUG-572 AC-2/AC-3: dynamic sort by demand height (worst shortfall first —
  // same `.value` descending comparator pickAutoSpec() already applies at
  // data.ts, reused verbatim), with Health pinned at the top — Aaron-approved
  // reading (a1): BOTH gp and hosp rows pinned together, sorted between
  // themselves by `.value` (whichever is in worse shortfall leads), above the
  // sorted rest. Stable tiebreak by id (GR#21 determinism — ties never flap).
  const sortedServices = [...services].sort((a, b) => {
    const aHealth = a.id === 'gp' || a.id === 'hosp';
    const bHealth = b.id === 'gp' || b.id === 'hosp';
    if (aHealth !== bHealth) return aHealth ? -1 : 1;
    if (b.value !== a.value) return b.value - a.value;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });

  function runResolveDemand(serviceKey: string) {
    run(() => {
      dispatch({ type: 'resolveDemand', serviceKey });
    });
  }

  // BUG-606 fix-all (Aaron, 2026-09-03): the SAME priority-ordered plan the
  // 'resolveDemandAll' reducer case walks (GR#3 SSOT — never re-derived here),
  // so the button is enabled/disabled by EXACTLY the condition the dispatch
  // will act on, and the tooltip names the real order.
  const fixAllOrder = orderedDemandFixPlan(state);
  function runFixAll() {
    if (fixAllOrder.length === 0) return;
    run(() => {
      dispatch({ type: 'resolveDemandAll' });
    });
  }
  // BUG-393: visible brownout signal — banner + power-row highlight while
  // power need exceeds capacity. FEAT-2326609711 inc1 fix: derived from
  // isBrownoutActive(), the SAME SSOT the income/wellbeing penalties read,
  // so the banner is suppressed exactly when Grid Import cover buys the
  // shortfall in — it can never disagree with the other two consumers.
  const brownoutActive = isBrownoutActive(state);

  function runAuto() {
    if (!auto) return;
    run(() => {
      // BUG-601 (Aaron ruling, 2026-09-02): Auto-build now sizes the SAME way
      // the Fix (N) button does — ceil(50% of the outstanding shortfall /
      // unit capacity), funds-capped by the resolveDemand reducer — instead
      // of always placing exactly one unit regardless of how large the
      // shortfall is. fixPlanByService is the SAME demandFixPlan(state) map
      // the Fix buttons read (SSOT, GR#3), keyed by auto.serviceKey.
      const plan = fixPlanByService.get(auto.serviceKey);
      if (plan) {
        dispatch({ type: 'resolveDemand', serviceKey: auto.serviceKey });
        return;
      }
      // Fallback for the pathological case where pickAutoSpec() recommends a
      // service demandFixPlan() has no entry for (e.g. a single-unit-only
      // affordability gap the plan's whole-shortfall budget check declines) —
      // preserves the pre-BUG-601 one-unit placement rather than silently
      // doing nothing.
      const spot = findSpot(state, auto.spec);
      if (!spot) return;
      dispatch({ type: 'place', spec: auto.spec, x: spot.x, y: spot.y });
    });
  }

  return (
    <Panel
      title="Demand"
      headerExtra={
        <button
          type="button"
          className="btn tiny fix-all-btn"
          disabled={fixAllOrder.length === 0}
          title={
            fixAllOrder.length === 0
              ? 'Nothing is in shortfall right now'
              : `Fix every shown shortfall, Health first: ${fixAllOrder.map((p) => SPECS[p.specId].name).join(', ')}`
          }
          onClick={runFixAll}
        >
          Fix All
        </button>
      }
    >
      <div className="demand-strip vertical">
        {brownoutActive && (
          <div className="brownout-banner" role="alert">
            BROWNOUT: demand exceeds supply
          </div>
        )}
        <DemandMeter label="Housing" value={demand.residential} color={SPECS.res_hut.color} />
        <DemandMeter label="Shops" value={demand.commercial} color={SPECS.com_shop.color} />
        <DemandMeter label="Industry" value={demand.industrial} color={SPECS.ind_factory.color} />
        {sortedServices.map((m) => {
          const fix = fixPlanByService.get(m.id);
          return (
            <DemandMeter
              key={m.id}
              label={m.label}
              value={m.value}
              color={SPECS[m.spec].color}
              alert={m.alert}
              fixCount={fix?.count}
              // BUG-587: "N x <Name>" (formatBuildingCount) — same shape as the
              // MapView advisor prompt and engine.ts's placeNotice (BUG-583),
              // sidestepping English pluralisation entirely. BUG-601: the
              // action now sizes to 50% of the outstanding shortfall (not a
              // full clear+5% headroom) — the copy says so, matching what
              // demandFixPlan()/resolveDemand actually place.
              fixTitle={fix ? `Place ${formatBuildingCount(SPECS[fix.specId].name, fix.count)} to fix ${AUTO_BUILD_DEMAND_PERCENT}% of this shortfall` : undefined}
              onFix={fix ? () => runResolveDemand(m.id) : undefined}
              // BUG-606 ("no help - how much what type ... one hypermarket or
              // 50?"): a visible (not just hover-title) sizing + alternative
              // line, built from the SAME fix plan item the button dispatches
              // against (agreement-by-construction).
              fixMessage={fix ? demandFixMessage(fix) : undefined}
            />
          );
        })}
        {auto && SPECS[auto.spec].unlock <= levelOf(state.xp) && (
          <button
            className="btn tiny auto-btn"
            title={`Auto-place the best ${auto.label.toLowerCase()} structure`}
            onClick={runAuto}
          >
            Auto-build: {SPECS[auto.spec].name}
          </button>
        )}
        {auto && SPECS[auto.spec].unlock > levelOf(state.xp) && (
          <p className="hint">
            {auto.label} needs a {SPECS[auto.spec].name} — unlocks at city level{' '}
            {SPECS[auto.spec].unlock}.
          </p>
        )}
        {!auto && <p className="hint">All covered — nothing needed right now.</p>}
      </div>
    </Panel>
  );
}

function DemandMeter({
  label,
  value,
  color,
  alert,
  fixCount,
  fixTitle,
  onFix,
  fixMessage,
}: {
  label: string;
  value: number;
  color: string;
  alert?: boolean;
  /** FEAT-2326609728 inc2: demandFixPlan() count for this service, when a
   *  provider is unlocked and buildable — undefined/onFix absent = no button. */
  fixCount?: number;
  fixTitle?: string;
  onFix?: () => void;
  /** BUG-606: demandFixMessage(fix) — a visible sizing+alternative line
   *  ("<N> short — Fix builds <P>%: ... or ... — cheapest picked"), rendered
   *  BELOW the meter row itself (not just as a hover title) so the shortfall
   *  size and the concrete build recommendation are on-screen without
   *  hovering. undefined = no shortfall for this row -> no line rendered. */
  fixMessage?: string;
}) {
  const w = Math.min(Math.abs(value), 100) / 2;
  const pos = value >= 0;
  return (
    <div className="demand-row">
      <div
        className={`demand${alert ? ' alert' : ''}`}
        title={`${label}: ${value > 0 ? '+' : ''}${value}${alert ? ' — BROWNOUT' : ''}`}
      >
        <span className="swatch" style={{ background: color }} />
        <span className="d-label">{label}</span>
        <div className="d-bar">
          {pos ? (
            <span className="d-fill pos" style={{ left: '50%', width: `${w}%` }} />
          ) : (
            <span className="d-fill neg" style={{ right: '50%', width: `${w}%` }} />
          )}
          <span className="d-zero" />
        </div>
        <span className={`mono d-val ${pos ? 'in' : 'out'}`}>
          {value > 0 ? '+' : ''}
          {value}
        </span>
        {onFix && (
          <button
            type="button"
            className="btn tiny demand-fix-btn"
            title={fixTitle}
            onClick={(e) => {
              e.stopPropagation();
              onFix();
            }}
          >
            Fix ({fixCount})
          </button>
        )}
      </div>
      {fixMessage && <p className="fix-hint">{fixMessage}</p>}
    </div>
  );
}
