import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { WSBridge, type ConnectionStatus } from "./ws/bridge";
import type {
  DeltaFrame,
  EventFrame,
  FinancePatch,
  ResultFrame,
  ViewportPatch,
} from "./ws/messages";
import { FINANCE_VIEW } from "./ws/messages";
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

/** Decode a delta patch as an f2.finance payload; null if it isn't one. */
function parseFinancePatch(raw: unknown): FinancePatch | null {
  try {
    const parsed = JSON.parse(String(raw)) as Partial<FinancePatch> & {
      cells?: unknown;
    };
    if (
      parsed.schemaVersion === 1 &&
      !Array.isArray(parsed.cells) &&
      (parsed.balanceSheet !== undefined ||
        parsed.sankey !== undefined ||
        parsed.loans !== undefined)
    ) {
      return parsed as FinancePatch;
    }
  } catch {
    // Not a finance patch (or malformed); the viewport decoder tries next.
  }
  return null;
}

/**
 * App is the S1/S2 shell: left fiscal tab (live f2.finance panels),
 * bottom build/move bar, right info tab, and the map canvas consuming
 * f1.viewport over the WS bridge.
 */
export default function App() {
  const [status, setStatus] = useState<ConnectionStatus>("disconnected");
  const [patch, setPatch] = useState<ViewportPatch | null>(null);
  const [finance, setFinance] = useState<FinancePatch | null>(null);
  const [lastResult, setLastResult] = useState<ResultFrame | null>(null);
  const [lastDelta, setLastDelta] = useState<DeltaFrame | null>(null);
  const [deltasSeen, setDeltasSeen] = useState(0);
  const [events, setEvents] = useState<EventFrame[]>([]);
  // FEAT-1972079851: the 'Power' map layer toggle. Default OFF — placed
  // pylons only paint once the player opts in (mirrors the tcell F1
  // overlay cycle, where Power is also never the initial selection).
  const [showPower, setShowPower] = useState(false);
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
              const fin = parseFinancePatch(frame.delta.patch);
              if (fin) {
                // Month-over-month: each f2.finance delta replaces the
                // previous month-close snapshot wholesale.
                setFinance(fin);
                break;
              }
              try {
                const parsed = JSON.parse(
                  String(frame.delta.patch),
                ) as ViewportPatch;
                if (parsed.schemaVersion === 1 && Array.isArray(parsed.cells)) {
                  setPatch(parsed);
                }
              } catch {
                // Undecodable patch; dropped loudly below only if neither
                // decoder matched a known view shape.
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
      bridge.send({ kind: "Subscribe", viewName: FINANCE_VIEW });
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
          <FiscalPanel fin={finance} lastResult={lastResult} />
        </aside>
        <section className="shell-center">
          <div className="map-toolbar">
            <button
              type="button"
              onClick={() => setShowPower((v) => !v)}
              aria-pressed={showPower}
            >
              Power layer: {showPower ? "ON" : "OFF"}
            </button>
          </div>
          <MapCanvas patch={patch} showPower={showPower} />
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
