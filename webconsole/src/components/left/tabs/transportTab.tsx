// transportTab.tsx — FEAT-2326609805 inc10 (AC-2..AC-9): read-only Transport
// screen, same idiom as populationTabs.tsx's WellbeingTab (tiles summary +
// h4 section headers + labelled bar rows). Every number is pulled from
// `s.trafficSnapshot` (cadence-refreshed) or a memoOnState export — NEVER a
// live derivation (AC-8/BUG-877/BUG-935/BUG-936 class) and NEVER
// money/wellbeing/attract coupling (AC-10).
//
// GR#25 deviations from the acceptance doc (re-verified against THIS
// worktree's HEAD, not the doc's stale f5ae80e stamp — see
// trafficOverlays.ts's header for the full detail, referenced rather than
// repeated here per GR#3):
//  - AC-5 "Parking shortfall" reports `parkingShortfallOf(s).cityShare` (a
//    [0,1] population-weighted fraction) as a percentage, NOT a raw vehicle
//    count (no such count is exposed by the registered symbol).
//  - AC-5 "Fuel/EV shortfall" reports `evChargePointShortfallOf(s)`'s
//    binary state plus `fuelAndEVDemandOf(s)`'s raw litres/kWh totals as
//    informational read-outs — no capacity term exists to compute a
//    shortfall PERCENTAGE against (fuelAndEVDemandOf has no capacity
//    field).
//  - AC-6 "Road condition" converts `roadWearBySegment`'s raw cumulative
//    ESAL values via `conditionIndexOf` (trafficAssignment.ts) before
//    averaging — the raw map is NOT itself a [0,1] condition value.
//  - AC-7's four policy rows: `busPriorityCapacityInfoOf(s).delta` gives
//    bus priority its own real measured effect. The other three
//    (ownershipQuota/roadPricing/integratedTicketing) share ONE combined
//    measured number — the car-mode-share shift from
//    `policyModeShareAdjustmentOf(s)` vs the unadjusted `modeShareOf
//    (ladderPointOf(s))` baseline — because no per-policy-isolated demand
//    effect is exported. Each of the three rows shows that SAME combined
//    number when its own policy is ON (an honest attribution of a shared,
//    real figure), "inactive" when off.

import { useSim } from '../../../sim/simContext';
import { ladderPointOf, modeShareOf, policyModeShareAdjustmentOf, busPriorityCapacityInfoOf } from '../../../sim/trafficDemand';
import { emergencyTargetMinutesOf } from '../../../sim/emergencyResponse';
import { parkingShortfallOf, fuelAndEVDemandOf, evChargePointShortfallOf } from '../../../sim/parkingFuel';
import { conditionIndexOf } from '../../../sim/trafficAssignment';
import {
  finiteOr,
  averageRoadCondition,
  roadConditionBandOf,
  gridlockBandOf,
  scoreBandOf,
} from '../../../sim/trafficOverlays';
import { fmtNum } from '../../../sim/utils';

// FEAT-2326609805 inc10 r2 (BUG-956 fix): colours read from the SAME CSS
// custom properties ragThresholds.ts's own ragColor() uses (GR#3 — no
// second hex palette for the same three RAG states, even though overlays.json
// separately owns the CANVAS overlay's raw hex, which has no CSS-variable
// equivalent inside a <canvas> 2D context — that duplication is documented
// and validated there, this is a DOM component and has no such excuse).
const GREEN = 'var(--done)';
const YELLOW = 'var(--warn)';
const RED = 'var(--danger)';
const NEUTRAL = 'var(--muted, #8b949e)';

function bandColor(band: 'green' | 'yellow' | 'red'): string {
  return band === 'green' ? GREEN : band === 'yellow' ? YELLOW : RED;
}

function Row({
  label,
  value,
  barPct,
  color,
}: {
  label: string;
  value: string;
  barPct?: number;
  color?: string;
}) {
  return (
    <div className="wb-row">
      <span className="d-label">{label}</span>
      {barPct !== undefined && (
        <div className="d-bar">
          <span
            className="d-fill pos"
            style={{ left: 0, width: `${Math.max(0, Math.min(100, barPct))}%`, background: color ?? GREEN }}
          />
        </div>
      )}
      <span className="mono d-val" style={{ color: color ?? undefined }}>
        {value}
      </span>
    </div>
  );
}

export function TransportTab() {
  const { state } = useSim();
  const snapshot = state.trafficSnapshot;

  if (!snapshot) {
    return (
      <>
        <h4>Transport</h4>
        <p className="hint">Not yet available — the traffic model has not produced its first cadence snapshot yet.</p>
      </>
    );
  }

  // AC-3: commute p50/p90 + gridlock share. BUG-957 fix: p90 now reads the
  // REAL p90CommuteMinutes field (inc3's commuteTimeDistributionOf already
  // computes it; inc10 r1 wired this row to echo the p50 field instead).
  const p50 = finiteOr(snapshot.medianCommuteMinutes, 0);
  const p90 = finiteOr(snapshot.p90CommuteMinutes, p50);
  const gridlockShare = finiteOr(snapshot.gridlockShare, 0);
  const gridlockBand = gridlockBandOf(gridlockShare);

  // AC-4: emergency coverage, three services in a FIXED order.
  const services: Array<{ id: 'ambulance' | 'fire' | 'police'; label: string }> = [
    { id: 'ambulance', label: 'Ambulance coverage' },
    { id: 'fire', label: 'Fire coverage' },
    { id: 'police', label: 'Police coverage' },
  ];

  // AC-5: parking + fuel/EV.
  const parkingCityShare = finiteOr(parkingShortfallOf(state).cityShare, 0);
  const fuelEvShortfallFlag = evChargePointShortfallOf(state) > 0;
  const fuelEv = fuelAndEVDemandOf(state);

  // AC-6: road wear.
  const rawWear = state.roadWearBySegment;
  const conditionByFraction: Record<string, number> = {};
  if (rawWear) {
    for (const [segId, w] of Object.entries(rawWear)) conditionByFraction[segId] = conditionIndexOf(w) / 100;
  }
  const avgCondition = averageRoadCondition(conditionByFraction);
  const conditionBand = roadConditionBandOf(avgCondition);
  const deferredCount = state.roadRepairDeferredSegmentIds?.length ?? 0;

  // AC-7: policy effects + scores.
  const baseline = modeShareOf(ladderPointOf(state));
  const adjusted = policyModeShareAdjustmentOf(state);
  const carShiftPct = Math.abs(finiteOr(adjusted.car, 0) - finiteOr(baseline.car, 0)) * 100;
  const demandPoliciesOn = state.policies.ownershipQuota || state.policies.roadPricing || state.policies.integratedTicketing;
  const busDelta = busPriorityCapacityInfoOf(state).delta;
  const safeRoadScore = finiteOr(snapshot.safeRoadScore, 0) * 100;
  const integrationScore = finiteOr(snapshot.integratedTransportScore, 0) * 100;
  const safeRoadBand = scoreBandOf(safeRoadScore);

  return (
    <>
      <div className="tiles">
        <div className="tile pos" style={{ borderColor: GREEN }}>
          <div className="n">{fmtNum(p50)}</div>
          <div className="l">Commute p50 (min)</div>
        </div>
        <div className={`tile ${gridlockBand === 'red' ? 'neg' : 'pos'}`} style={{ borderColor: bandColor(gridlockBand) }}>
          <div className="n" style={{ color: bandColor(gridlockBand) }}>{(gridlockShare * 100).toFixed(0)}%</div>
          <div className="l">Gridlock share</div>
        </div>
      </div>

      <h4>Commute</h4>
      <div className="wb-list">
        <Row label="Commute p50" value={`${fmtNum(p50)} min`} />
        <Row label="Commute p90" value={`${fmtNum(p90)} min`} />
        <Row label="Gridlock share" value={`${(gridlockShare * 100).toFixed(0)}%`} barPct={gridlockShare * 100} color={bandColor(gridlockBand)} />
      </div>

      <h4>Emergency coverage</h4>
      <div className="wb-list">
        {services.map((svc) => {
          // FEAT-2326609805 inc10 r2 (BUG-952 fix): reads the cadence-cached
          // per-service coverageShare off s.trafficSnapshot instead of
          // calling that per-service Dijkstra-isochrone function live — it
          // forces emergencyResponse.ts's multi-source Dijkstra isochrone
          // pass for EVERY service on EVERY render (measured 130.8ms for
          // the tab's whole derivation set on a 6,400-building grid,
          // round-1 REJECT finding). emergencyTargetMinutesOf is a cheap,
          // non-Dijkstra band-selection read and stays live.
          const target = emergencyTargetMinutesOf(state, svc.id);
          // GR#1/GR#16 defensive read: an in-memory snapshot object that
          // bypassed sanitizeTrafficSnapshot/computeTrafficSnapshot (should
          // never happen in production — both writers always populate this
          // field — but a raw/legacy object handed straight to SimContext,
          // as a test fixture or a future migration bug might, must not
          // throw reading .coverageShareByService[svc.id] off `undefined`).
          const rawShare = snapshot.coverageShareByService?.[svc.id] ?? null;
          const share = finiteOr(rawShare, 0);
          const covered = rawShare !== null;
          const band: 'green' | 'yellow' | 'red' = !covered ? 'red' : share >= 0.8 ? 'green' : share >= 0.5 ? 'yellow' : 'red';
          return (
            <Row
              key={svc.id}
              label={svc.label}
              value={covered ? `${(share * 100).toFixed(0)}% (target ${fmtNum(target)} min)` : 'n/a'}
              barPct={share * 100}
              color={bandColor(band)}
            />
          );
        })}
      </div>

      <h4>Parking &amp; fuel/EV</h4>
      <div className="wb-list">
        <Row
          label="Parking shortfall"
          value={`${(parkingCityShare * 100).toFixed(0)}% of demand`}
          barPct={parkingCityShare * 100}
          color={parkingCityShare > 0 ? RED : GREEN}
        />
        <Row
          label="Fuel/EV shortfall"
          value={fuelEvShortfallFlag ? `Shortfall (${fmtNum(finiteOr(fuelEv.litresPerDay, 0))} L/day, ${fmtNum(finiteOr(fuelEv.evKWhPerDay, 0))} kWh/day demand)` : 'None'}
          color={fuelEvShortfallFlag ? RED : GREEN}
        />
      </div>

      <h4>Road condition</h4>
      <div className="wb-list">
        <Row
          label="Road condition"
          value={`${(avgCondition * 100).toFixed(0)}%`}
          barPct={avgCondition * 100}
          color={bandColor(conditionBand)}
        />
        <Row
          label="Repairs deferred"
          value={String(deferredCount)}
          color={deferredCount > 0 ? RED : GREEN}
        />
      </div>

      <h4>Policy effects &amp; scores</h4>
      <div className="wb-list">
        <Row
          label={`Ownership quota: ${state.policies.ownershipQuota ? 'On' : 'Off'}`}
          value={state.policies.ownershipQuota ? `${carShiftPct.toFixed(1)}% car-share shift` : 'inactive'}
          color={state.policies.ownershipQuota ? YELLOW : NEUTRAL}
        />
        <Row
          label={`Road pricing: ${state.policies.roadPricing ? 'On' : 'Off'}`}
          value={state.policies.roadPricing ? `${carShiftPct.toFixed(1)}% car-share shift` : 'inactive'}
          color={state.policies.roadPricing ? YELLOW : NEUTRAL}
        />
        <Row
          label={`Bus priority: ${state.policies.busPriority ? 'On' : 'Off'}`}
          value={state.policies.busPriority ? `+${fmtNum(busDelta)} capacity` : 'inactive'}
          color={state.policies.busPriority ? YELLOW : NEUTRAL}
        />
        <Row
          label={`Integrated ticketing: ${state.policies.integratedTicketing ? 'On' : 'Off'}`}
          value={state.policies.integratedTicketing ? `${carShiftPct.toFixed(1)}% car-share shift` : 'inactive'}
          color={state.policies.integratedTicketing ? YELLOW : NEUTRAL}
        />
        <Row
          label="Safe-road score"
          value={`${safeRoadScore.toFixed(0)}%`}
          barPct={safeRoadScore}
          color={bandColor(safeRoadBand)}
        />
        <Row label="Integration score" value={`${integrationScore.toFixed(0)}%`} />
      </div>
      {!demandPoliciesOn && <p className="hint">No demand-shaping policy active — mode-share shift is 0%.</p>}
    </>
  );
}
