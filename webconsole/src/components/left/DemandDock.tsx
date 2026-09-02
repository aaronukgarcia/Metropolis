import { SPECS, serviceDemandOf, findSpot, pickAutoSpec, isBrownoutActive, demandFixPlan } from '../../sim/data';
import { useSim } from '../../sim/simContext';
import { demandOf, levelOf } from '../../sim/engine';
import { useBusy } from '../Busy';
import { Panel } from '../Tabs';
import { pluralizeBuildingName } from '../demandFixUi';

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

  function runResolveDemand(serviceKey: string) {
    run(() => {
      dispatch({ type: 'resolveDemand', serviceKey });
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
      const spot = findSpot(state, auto.spec);
      if (!spot) return;
      dispatch({ type: 'place', spec: auto.spec, x: spot.x, y: spot.y });
    });
  }

  return (
    <Panel title="Demand">
      <div className="demand-strip vertical">
        {brownoutActive && (
          <div className="brownout-banner" role="alert">
            BROWNOUT: demand exceeds supply
          </div>
        )}
        <DemandMeter label="Housing" value={demand.residential} color={SPECS.res_hut.color} />
        <DemandMeter label="Shops" value={demand.commercial} color={SPECS.com_shop.color} />
        <DemandMeter label="Industry" value={demand.industrial} color={SPECS.ind_factory.color} />
        {services.map((m) => {
          const fix = fixPlanByService.get(m.id);
          return (
            <DemandMeter
              key={m.id}
              label={m.label}
              value={m.value}
              color={SPECS[m.spec].color}
              alert={m.alert}
              fixCount={fix?.count}
              fixTitle={fix ? `Place ${fix.count} ${pluralizeBuildingName(SPECS[fix.specId].name, fix.count)} to clear this shortfall +5%` : undefined}
              onFix={fix ? () => runResolveDemand(m.id) : undefined}
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
}) {
  const w = Math.min(Math.abs(value), 100) / 2;
  const pos = value >= 0;
  return (
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
  );
}
