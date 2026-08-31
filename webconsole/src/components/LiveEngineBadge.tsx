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
  ProtocolClient,
  type ProtocolClientState,
} from '../sim/protocolClient.ts';
import { decodeFinanceBalanceSheetPatch, FINANCE_VIEW_NAME, type Delta } from '../sim/wire.ts';
import { versionRaw } from '../sim/version.ts';
import { isLiveEngineEnabled, resolveLiveEngineUrl } from '../sim/liveEngineFlag.ts';

export { LIVE_ENGINE_FLAG_KEY, LIVE_ENGINE_URL_KEY } from '../sim/liveEngineFlag.ts';

interface LiveEngineSnapshot {
  tick: number;
  netWorthMicropounds: number | null;
}

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
  const clientRef = useRef<ProtocolClient | null>(null);

  useEffect(() => {
    if (!enabled) return;
    const client = new ProtocolClient({
      url: resolveLiveEngineUrl(),
      clientVersion: versionRaw,
      onStateChange: setConnState,
      onLive: () => {
        client.subscribe(FINANCE_VIEW_NAME);
      },
      onRefused: (code, msg) => setRefusal({ code, msg }),
      onDelta: (delta: Delta) => {
        const patch = decodeFinanceBalanceSheetPatch(delta.patch);
        setSnapshot({
          tick: delta.tick,
          netWorthMicropounds: patch?.balanceSheet?.netWorth ?? null,
        });
      },
    });
    clientRef.current = client;
    client.connect();
    return () => client.close();
  }, [enabled]);

  if (!enabled) return null;

  const label =
    connState === 'live'
      ? snapshot
        ? `LIVE ENGINE  tick ${snapshot.tick}${snapshot.netWorthMicropounds != null ? `  £${(snapshot.netWorthMicropounds / 1_000_000).toFixed(0)}` : ''}`
        : 'LIVE ENGINE  (waiting for data)'
      : connState === 'refused'
        ? `LIVE ENGINE refused: ${refusal?.code ?? ''}`
        : connState === 'error'
          ? 'LIVE ENGINE unreachable'
          : `LIVE ENGINE ${connState}`;

  return (
    <span
      className="live-engine-badge mono"
      title={refusal ? refusal.msg : 'Dev indicator: live Go engine feed (FEAT-1972079852 inc1)'}
      data-conn-state={connState}
    >
      {label}
    </span>
  );
}
