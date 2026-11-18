// LiveEngineBadge.tsx — FEAT-1972079852 increment 1: the ONE real organ
// this increment feeds into the UI, per the build brief's inc1 scope (b):
// a read-only dev indicator showing the live Go engine's tick + funds,
// sourced from a real WebSocket connection through protocolClient.ts,
// behind a feature flag that defaults OFF. The mock sim remains the
// default UI everywhere else in the app — this badge is additive and
// never replaces TopBar's existing mock-sourced funds/tick display.
//
// Feature flag (AC "opts in via config/env/localStorage"): localStorage
// key LIVE_ENGINE_FLAG_KEY set to '1' enables it. Checked once per mount
// (a page reload is required to toggle it — acceptable for a dev-only
// indicator; no hot-toggle machinery needed for increment 1).
import { useEffect, useRef, useState } from 'react';
import {
  ERR_CITY_OPEN_ELSEWHERE,
  ERR_RECONNECT_GIVEUP,
  ProtocolClient,
  type ProtocolClientState,
  type WakeProgress,
} from '../sim/protocolClient.ts';
import { decodeFinanceBalanceSheetPatch, FINANCE_VIEW_NAME, type Delta } from '../sim/wire.ts';
import { versionRaw } from '../sim/version.ts';
import { setLiveEngineBuild, resetLiveEngineBuild } from '../sim/liveEngineBuild.ts';
import { isLiveEngineEnabled, resolveLiveEngineUrl } from '../sim/liveEngineFlag.ts';
import { financeStatusTracker } from '../sim/financeStatusTracker.ts';
import { engineMoneyToPounds } from '../sim/engineMoney.ts';

export { LIVE_ENGINE_FLAG_KEY, LIVE_ENGINE_URL_KEY } from '../sim/liveEngineFlag.ts';

interface LiveEngineSnapshot {
  tick: number;
  netWorthMicropounds: number | null;
}

/** inc2 (FEAT-1972079852): the two speeds the dev-only toggle button
 * cycles between. SetSpeed is deliberately the FIRST command this UI
 * ever drives end-to-end — it needs no gameplayHandler/build-module
 * wiring (engine.core's handleSetSpeed is self-contained), so the round
 * can prove "a UI action reaches the engine and the engine's
 * accept/reject comes back" without touching engine.ts or any
 * demographic/build code other live lanes own. */
const SPEED_TOGGLE_STATES = [1, 2] as const;

/**
 * LiveEngineBadge: renders nothing when the feature flag is off (the
 * default). When on, it opens a ProtocolClient connection, subscribes to
 * "f2.finance" once live, and renders a small badge with the connection
 * state plus the latest tick/netWorth it has seen. Never touches the mock
 * SimState/store — purely a read-only, side-channel indicator (AC "one
 * real organ ... behind a feature flag").
 */
export function LiveEngineBadge() {
  const [enabled] = useState(() => isLiveEngineEnabled());
  const [connState, setConnState] = useState<ProtocolClientState>('connecting');
  const [snapshot, setSnapshot] = useState<LiveEngineSnapshot | null>(null);
  const [refusal, setRefusal] = useState<{ code: string; msg: string } | null>(null);
  // FEAT-9995385597: wake progress, the reconnect attempt number and whether a
  // connect attempt belongs to a reconnect cycle (label stays "Reconnecting...").
  const [wakeProgress, setWakeProgress] = useState<WakeProgress | null>(null);
  const [reconnectInfo, setReconnectInfo] = useState<{ attempt: number; cycle: boolean }>({ attempt: 0, cycle: false });
  const clientRef = useRef<ProtocolClient | null>(null);
  // inc2: last SetSpeed the toggle button asked for (optimistic — the
  // engine's own CommandResult, not this, is the source of truth; a
  // rejection is surfaced via commandNote below without touching this).
  const [speedIndex, setSpeedIndex] = useState(0);
  const [commandNote, setCommandNote] = useState<string | null>(null);
  // BUG-1097: the engine build from the accepted handshake + whether it
  // differs from this client's build (a warning state, never a block).
  const [buildPair, setBuildPair] = useState<{ engine: string; mismatch: boolean } | null>(null);

  useEffect(() => {
    if (!enabled) return;
    const client = new ProtocolClient({
      url: resolveLiveEngineUrl(),
      clientVersion: versionRaw,
      // FEAT-9995385597: lifecycle protocol + reconnect machine. A lost
      // connection reconnects with backoff; it NEVER falls back to the
      // offline mock sim (AC-R8).
      lifecycle: {},
      onStateChange: (s) => {
        setConnState(s);
        setReconnectInfo({ attempt: client.getReconnectAttempt(), cycle: client.isReconnectCycle() });
        if (s === 'live') setRefusal(null);
      },
      onWakeProgress: setWakeProgress,
      onLive: (serverVersion, _nv, _caps, info) => {
        // BUG-1097 / Q100145: both builds are exchanged; a difference is a
        // warning (badge + debug files), never a refusal.
        const mismatch = client.isBuildMismatch();
        setLiveEngineBuild({ engineBuild: serverVersion, clientBuild: versionRaw, mismatch });
        setBuildPair({ engine: serverVersion, mismatch });
        // A reconnect re-issues the subscriptions inside the client (AC-R5);
        // subscribing here again would double them.
        if (!info?.reconnected) client.subscribe(FINANCE_VIEW_NAME);
      },
      onRefused: (code, msg) => setRefusal({ code, msg }),
      onDelta: (delta: Delta) => {
        const patch = decodeFinanceBalanceSheetPatch(delta.patch);
        setSnapshot({
          tick: delta.tick,
          netWorthMicropounds: patch?.balanceSheet?.netWorth ?? null,
        });
        // BUG-723 round finding F1: this badge is the ONLY current
        // decoder of "f2.finance" patches, so it is the seam through
        // which any other UI surface (NewsFeed.tsx) reaches live-engine
        // finance data — mirrors queueDepth.ts's instrument-at-the-source
        // convention. patch?.payrollShortfall ?? null covers both "no
        // patch this cycle" (schema mismatch/decode failure) and "patch
        // decoded but the server hasn't published this section yet".
        financeStatusTracker.setPayrollShortfall(patch?.payrollShortfall ?? null);
        // BUG-769: the OTHER real, already-consumed BUG-759 gap —
        // FinanceAPI.InsolvencyMonths()/IsInsolvent() advance for real
        // every month but nothing surfaced them. Same seam, same
        // absent/null-means-unknown discipline as payrollShortfall above:
        // both insolvencyMonths and insolvent must be present (non-null)
        // for a real reading; either missing means "no signal this cycle".
        const insolvencyMonths = patch?.insolvencyMonths;
        const insolvent = patch?.insolvent;
        financeStatusTracker.setInsolvency(
          insolvencyMonths != null && insolvent != null ? { months: insolvencyMonths, insolvent } : null
        );
      },
    });
    clientRef.current = client;
    client.connect();
    // BUG-723: the tracker must not keep reporting a stale shortfall
    // reading once this badge (the only current writer) unmounts or its
    // connection is torn down — reset() marks "no live data" the same
    // way the badge's own snapshot/connState fall back to their
    // disconnected display.
    return () => {
      client.close();
      financeStatusTracker.reset();
      resetLiveEngineBuild();
    };
  }, [enabled]);

  // inc2: drives the FIRST real UI-issued command over the wire. Only
  // usable once 'live' (the client itself no-ops sendCommand before
  // handshake, but the button is hidden regardless — a dev affordance
  // has no reason to invite a click that can only fail). Never touches
  // the TS journal (protocolClient.ts's sendCommand doc comment) — this
  // is a protocol-driven command, and Aaron's engine-owns-journal DD
  // means only mock/offline commands ever go through journal.ts.
  const sendSpeedToggle = () => {
    const client = clientRef.current;
    if (!client) return;
    const nextIndex = (speedIndex + 1) % SPEED_TOGGLE_STATES.length;
    const nextSpeed = SPEED_TOGGLE_STATES[nextIndex];
    client
      .sendCommand('SetSpeed', { speed: nextSpeed })
      .then(() => {
        setSpeedIndex(nextIndex);
        setCommandNote(`SetSpeed(${nextSpeed}) accepted`);
      })
      .catch((err: Error & { code?: string }) => {
        setCommandNote(`SetSpeed(${nextSpeed}) rejected: ${err.code ?? ''} ${err.message}`.trim());
      });
  };

  if (!enabled) return null;

  // AC-B2: states with a user action are clickable. Waking from `asleep` is
  // CLICK ONLY (AC-H3); an offline give-up and the open-elsewhere stop retry
  // on click. Nothing else ever connects an asleep client.
  const openElsewhere = connState === 'refused' && refusal?.code === ERR_CITY_OPEN_ELSEWHERE;
  const gaveUp = connState === 'error' && refusal?.code === ERR_RECONNECT_GIVEUP;
  // r4 ruling 2: EVERY refused state is retryable by click (a malformed or
  // not-accepted reply, MET-P011, after a reconnect is not a dead end).
  const refusedStop = connState === 'refused';
  const clickAction: 'wake' | 'retry' | null =
    connState === 'asleep' ? 'wake' : gaveUp || refusedStop ? 'retry' : null;
  const onBadgeClick = () => {
    const client = clientRef.current;
    if (!client) return;
    if (clickAction === 'wake') client.wakeFromAsleep();
    else if (clickAction === 'retry') client.retry();
  };

  const reconnectingLabel = connState === 'reconnecting' || (reconnectInfo.cycle && (connState === 'connecting' || connState === 'handshaking'));
  const label = reconnectingLabel
    ? 'LIVE ENGINE Reconnecting...'
    : connState === 'waking'
      ? `LIVE ENGINE Waking city ${wakeProgress?.pct ?? 0}%`
      : connState === 'asleep'
        ? 'LIVE ENGINE City asleep - click to wake'
        : openElsewhere
          ? 'LIVE ENGINE City is open in another tab'
          : gaveUp
            ? 'LIVE ENGINE Offline - click to retry'
            : connState === 'live'
              ? snapshot
                ? `LIVE ENGINE  tick ${snapshot.tick}${snapshot.netWorthMicropounds != null ? `  £${engineMoneyToPounds(snapshot.netWorthMicropounds).toFixed(0)}` : ''}`
                : 'LIVE ENGINE  (waiting for data)'
              : connState === 'refused'
                ? `LIVE ENGINE refused: ${refusal?.code ?? ''} - click to retry`
                : connState === 'error'
                  ? 'LIVE ENGINE unreachable'
                  : `LIVE ENGINE ${connState}`;

  const buildWarn = connState === 'live' && buildPair?.mismatch === true;
  const title = connState === 'waking' && wakeProgress
    ? `Waking city: ${wakeProgress.phase} ${wakeProgress.pct}%`
    : reconnectingLabel
      ? `Reconnecting, attempt ${Math.max(1, reconnectInfo.attempt)}`
      : refusal && connState !== 'live'
    ? refusal.msg
    : buildWarn
      ? `engine ${buildPair?.engine} / client ${versionRaw}`
      : 'Dev indicator: live Go engine feed (FEAT-1972079852 inc1)';

  return (
    <span
      className={buildWarn ? 'live-engine-badge mono live-engine-badge--build-mismatch' : 'live-engine-badge mono'}
      title={title}
      data-conn-state={connState}
      role={clickAction ? 'button' : undefined}
      tabIndex={clickAction ? 0 : undefined}
      onClick={clickAction ? onBadgeClick : undefined}
      onKeyDown={
        clickAction
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') onBadgeClick();
            }
          : undefined
      }
      data-build-mismatch={buildWarn ? 'true' : undefined}
      style={buildWarn ? { color: '#b45309', borderBottom: '2px solid #f59e0b' } : undefined}
    >
      {label}
      {connState === 'live' && (
        <button
          type="button"
          className="live-engine-speed-toggle"
          data-testid="live-engine-speed-toggle"
          title="Dev-only: send a SetSpeed command over the wire (FEAT-1972079852 inc2)"
          onClick={sendSpeedToggle}
        >
          speed→{SPEED_TOGGLE_STATES[(speedIndex + 1) % SPEED_TOGGLE_STATES.length]}x
        </button>
      )}
      {commandNote && <span className="live-engine-command-note">{commandNote}</span>}
    </span>
  );
}
