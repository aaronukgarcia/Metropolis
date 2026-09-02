// alertsTabs.tsx — FEAT-2326609720 inc2, Alerts group (§1 row 25).
//
// AC-12 IMPORTANT: this is a PASSIVE LOG/REVIEW surface of the same events
// the MapView banners already render (insolvencyState, declineState, notice,
// bailout state) — it reads the SAME SimState fields, never invents new
// state, and NEVER registers itself as a blocking overlay via
// useBlockingOverlay/BLOCKING_OVERLAY_ID (overlayManager.tsx). It is an
// ordinary in-flow tab body inside LeftDock, so it cannot violate the
// single-blocking-overlay invariant by construction — there is nothing here
// for resolveBlockingOverlay to arbitrate. The banners themselves keep
// rendering in MapView.tsx unmodified (out of this lane's file boundary).

import { useSim } from '../../../sim/simContext';
import { fmtMoney } from '../../../sim/utils';

interface AlertRow {
  key: string;
  text: string;
}

function severityRows(
  insolvencyState: string | undefined,
  declineState: unknown,
  bailoutSecondState: unknown,
  funds: number,
): { critical: AlertRow[]; warning: AlertRow[]; info: AlertRow[] } {
  const critical: AlertRow[] = [];
  const warning: AlertRow[] = [];
  const info: AlertRow[] = [];

  if (declineState) {
    critical.push({ key: 'decline', text: 'City in decline — hard game-over state active.' });
  }
  if (bailoutSecondState) {
    critical.push({ key: 'bailout2', text: 'Second bailout in progress — funds remain critically negative.' });
  }
  if (insolvencyState === 'administration') {
    critical.push({ key: 'admin', text: `Administration — treasury ${fmtMoney(funds)}, placements/policy changes blocked.` });
  } else if (insolvencyState === 'crisis') {
    warning.push({ key: 'crisis', text: `Fiscal crisis — treasury ${fmtMoney(funds)}, bailout imminent.` });
  } else if (insolvencyState === 'warning') {
    warning.push({ key: 'warn', text: `Fiscal warning — treasury ${fmtMoney(funds)} below the warning threshold.` });
  } else if (insolvencyState === 'bailout_second') {
    // already covered by the critical branch above; nothing else to add.
  }
  if (!declineState && !bailoutSecondState && insolvencyState !== 'administration' && insolvencyState !== 'crisis' && insolvencyState !== 'warning') {
    info.push({ key: 'solvent', text: 'No active fiscal alerts — city is solvent.' });
  }
  return { critical, warning, info };
}

function AlertList({ rows, empty }: { rows: AlertRow[]; empty: string }) {
  if (rows.length === 0) {
    return <p className="muted">{empty}</p>;
  }
  return (
    <ul className="milestone-list">
      {rows.map((r) => (
        <li key={r.key}>
          <div><p className="muted">{r.text}</p></div>
        </li>
      ))}
    </ul>
  );
}

export function AlertsCriticalTab() {
  const { state } = useSim();
  const { critical } = severityRows(state.insolvencyState, state.declineState, state.bailoutSecondState, state.funds);
  return <AlertList rows={critical} empty="No critical alerts." />;
}

export function AlertsWarningTab() {
  const { state } = useSim();
  const { warning } = severityRows(state.insolvencyState, state.declineState, state.bailoutSecondState, state.funds);
  const extra: AlertRow[] = [];
  if (state.roadNotice) extra.push({ key: 'road', text: `Road notice: ${state.roadNotice}` });
  if (state.railNotice) extra.push({ key: 'rail', text: `Rail notice: ${state.railNotice}` });
  return <AlertList rows={[...warning, ...extra]} empty="No warnings." />;
}

export function AlertsInfoTab() {
  const { state } = useSim();
  const { info } = severityRows(state.insolvencyState, state.declineState, state.bailoutSecondState, state.funds);
  const extra: AlertRow[] = [];
  if (state.notice) extra.push({ key: 'levelup', text: `Level ${state.notice.level} reached — +${fmtMoney(state.notice.cash)}.` });
  if (state.placeNotice) extra.push({ key: 'place', text: state.placeNotice });
  return <AlertList rows={[...extra, ...info]} empty="No informational notices." />;
}
