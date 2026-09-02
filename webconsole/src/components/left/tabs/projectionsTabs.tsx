// projectionsTabs.tsx — FEAT-2326609720 inc2, Projections group child tabs.
//
// §1 row 20 (Milestones, relocated per open-question-1's recommendation:
// Projections over Alerts — a met/open milestone is a forward-looking
// target, not a "needs attention now" signal) + §1 row 26 (Demand/Revenue
// forecast, forward-declared STUBS — AC-6/AC-10: no engine forecast model
// exists yet, render an explicit "not yet available" state, no colour, no
// fabricated number).

import { useSim } from '../../../sim/simContext';
import { MILESTONES } from '../../../sim/data';
import { STUB_LABEL } from '../../ragThresholds';

// §1 row 20 — relocated from RightDock `milestones`, unchanged content.
// §2 row 12: binary met/open per item today — no invented AMBER/RED band.
export function MilestonesTab() {
  const { state } = useSim();
  return (
    <ul className="milestone-list">
      {MILESTONES.map((m) => {
        const met = m.test(state);
        return (
          <li key={m.id} className={met ? 'met' : ''}>
            <span className="ms-dot" />
            <div>
              <b>{m.label}</b>
              <p className="muted">{m.detail}</p>
            </div>
            <span className={`chip ${met ? 'done' : 'open'}`}>{met ? 'Met' : 'Open'}</span>
          </li>
        );
      })}
    </ul>
  );
}

// §1 row 26 / §2 row 13-ish (forecast rows) — STUB. AC-6/AC-10: explicit
// fallback text, no numeric RAG colour, distinguishable "coming soon" marker.
function ForecastStub({ label }: { label: string }) {
  return (
    <div className="tile stub" data-testid={`forecast-stub-${label.toLowerCase()}`}>
      <div className="n muted">{STUB_LABEL}</div>
      <div className="l">{label} forecast</div>
    </div>
  );
}

export function DemandForecastTab() {
  return (
    <>
      <ForecastStub label="Demand" />
      <p className="hint">
        No demand-forecast model exists in the engine yet — this tab is a forward-declared stub
        (§1 row 26). Coming in a future increment; not blocking inc2.
      </p>
    </>
  );
}

export function RevenueForecastTab() {
  return (
    <>
      <ForecastStub label="Revenue" />
      <p className="hint">
        No revenue-forecast model exists in the engine yet — this tab is a forward-declared stub
        (§1 row 26). Coming in a future increment; not blocking inc2.
      </p>
    </>
  );
}
