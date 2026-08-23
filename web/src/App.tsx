import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { WSBridge, type ConnectionStatus } from "./ws/bridge";
import type {
  DeltaFrame,
  EventFrame,
  ResultFrame,
  ViewportPatch,
} from "./ws/messages";
import { MapCanvas } from "./components/MapCanvas";
import { ConnectionStatusIndicator } from "./components/ConnectionStatusIndicator";
import {
  BuildMovePanel,
  FiscalPanel,
  InfoPanel,
} from "./components/Panels";

const WS_URL =
  (typeof import.meta !== "undefined" &&
    (import.meta as { env?: Record<string, string> }).env
      ?.VITE_METROPOLIS_WS_URL) ||
  `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;

/**
 * App is the S1 shell: left fiscal tab, bottom build/move bar, right info
 * tab, and the map canvas placeholder consuming f1.viewport over the WS
 * bridge, with a live connection indicator.
 */
export default function App() {
  const [status, setStatus] = useState<ConnectionStatus>("disconnected");
  const [patch, setPatch] = useState<ViewportPatch | null>(null);
  const [lastResult, setLastResult] = useState<ResultFrame | null>(null);
  const [lastDelta, setLastDelta] = useState<DeltaFrame | null>(null);
  const [deltasSeen, setDeltasSeen] = useState(0);
  const [events, setEvents] = useState<EventFrame[]>([]);
  const subscribedRef = useRef(false);

  const bridge = useMemo(
    () =>
      new WSBridge(WS_URL, {
        onStatus: setStatus,
        onFrame: (frame) => {
          switch (frame.type) {
            case "result":
              setLastResult(frame.result);
              break;
            case "delta":
              setDeltasSeen((n) => n + 1);
              setLastDelta(frame.delta);
              try {
                const parsed = JSON.parse(
                  String(frame.delta.patch),
                ) as ViewportPatch;
                if (parsed.schemaVersion === 1 && Array.isArray(parsed.cells)) {
                  setPatch(parsed);
                }
              } catch {
                // Not a viewport patch (or malformed); other views arrive later.
              }
              break;
            case "event":
              setEvents((prev) => [...prev.slice(-50), frame.event]);
              break;
            case "error":
              console.error("[ws] server error frame", frame.error);
              break;
          }
        },
      }),
    [],
  );

  useEffect(() => {
    bridge.connect();
    return () => bridge.close();
  }, [bridge]);

  // Re-subscribe whenever a connection (re)establishes; full snapshots make
  // reconnects self-healing.
  useEffect(() => {
    if (status === "connected" && !subscribedRef.current) {
      subscribedRef.current = true;
      bridge.send({ kind: "Subscribe", viewName: "f1.viewport" });
    }
    if (status === "disconnected") {
      subscribedRef.current = false;
    }
  }, [status, bridge]);

  const zoneStartCell = useCallback(() => {
    bridge.send({
      kind: "Zone",
      cell: { x: 0, y: 0 },
      zoneType: "dwelling",
    });
  }, [bridge]);

  return (
    <div className="shell">
      <header className="shell-header">
        <h1>Metropolis</h1>
        <ConnectionStatusIndicator status={status} />
      </header>
      <main className="shell-main">
        <aside className="shell-left">
          <FiscalPanel lastResult={lastResult} />
        </aside>
        <section className="shell-center">
          <MapCanvas patch={patch} />
        </section>
        <aside className="shell-right">
          <InfoPanel
            deltasSeen={deltasSeen}
            lastDelta={lastDelta}
            events={events}
          />
        </aside>
      </main>
      <BuildMovePanel onZone={zoneStartCell} />
    </div>
  );
}
