import { SPECS, serviceDemandOf, findSpot, pickAutoSpec } from '../../sim/data';
import { useSim, demandOf, levelOf } from '../../sim/store';
import { useBusy } from '../Busy';
import { Panel } from '../Tabs';

export function DemandDock() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const demand = demandOf(state);
  const services = serviceDemandOf(state);
  const auto = pickAutoSpec(state);

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
        <DemandMeter label="Housing" value={demand.residential} color={SPECS.res_hut.color} />
        <DemandMeter label="Shops" value={demand.commercial} color={SPECS.com_shop.color} />
        <DemandMeter label="Industry" value={demand.industrial} color={SPECS.ind_factory.color} />
        {services.map((m) => (
          <DemandMeter key={m.id} label={m.label} value={m.value} color={SPECS[m.spec].color} />
        ))}
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

function DemandMeter({ label, value, color }: { label: string; value: number; color: string }) {
  const w = Math.min(Math.abs(value), 100) / 2;
  const pos = value >= 0;
  return (
    <div className="demand" title={`${label}: ${value > 0 ? '+' : ''}${value}`}>
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
    </div>
  );
}
