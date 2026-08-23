import type { DeltaFrame, EventFrame, ResultFrame } from "../ws/messages";

export interface FiscalPanelProps {
  lastResult: ResultFrame | null;
}

/** Left tab: F2's fiscal placeholder — real ledger views land with f2 wiring. */
export function FiscalPanel({ lastResult }: FiscalPanelProps) {
  return (
    <section className="panel panel-fiscal" aria-label="fiscal">
      <h3>Fiscal</h3>
      <p className="placeholder">treasury / ledger placeholder</p>
      {lastResult && (
        <p data-testid="last-result">
          {lastResult.accepted ? "accepted" : `rejected: ${lastResult.error?.code ?? "?"}`}
        </p>
      )}
    </section>
  );
}

export interface InfoPanelProps {
  deltasSeen: number;
  lastDelta: DeltaFrame | null;
  events: EventFrame[];
}

/** Right tab: entity/inspect placeholder fed by the live delta counters. */
export function InfoPanel({ deltasSeen, lastDelta, events }: InfoPanelProps) {
  return (
    <section className="panel panel-info" aria-label="info">
      <h3>Info</h3>
      <p>deltas received: {deltasSeen}</p>
      {lastDelta && (
        <p>
          subscription {String(lastDelta.subscriptionId)} @ tick{" "}
          {Number(lastDelta.tick)}
        </p>
      )}
      {events.length > 0 && (
        <ul>
          {events.slice(-5).map((ev, i) => (
            <li key={i}>
              [{ev.severity}] {ev.kind}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export interface BuildMovePanelProps {
  onZone: () => void;
}

/** Bottom bar: build/move command placeholder — buttons issue real Zone commands. */
export function BuildMovePanel({ onZone }: BuildMovePanelProps) {
  return (
    <footer className="panel panel-buildmove" aria-label="build-move">
      <h3>Build / Move</h3>
      <button onClick={onZone} disabled={onZone === undefined ? true : false}>
        zone (0,0) dwelling
      </button>
      <span className="placeholder">build queue placeholder</span>
    </footer>
  );
}
