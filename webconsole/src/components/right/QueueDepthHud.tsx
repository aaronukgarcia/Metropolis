// QueueDepthHud.tsx — FEAT-1972079938: a compact, always-visible readout on
// the far right of the console showing live per-engine queue depth (asks
// currently WAITING on each engine), a total counter, and a session
// high-water mark, with a reset control.
//
// Purpose (Aaron): "10 waiting = fine, 100 = cool" — this is a DIAGNOSTIC,
// never an alarm. It exists to answer "which engine are we waiting on
// most" once multiple engines sit behind the protocol adapter
// (FEAT-1972079936 compute-offload epic). Today there is exactly one
// target (protocolClient.ts's sendCommand path, key PROTOCOL_ENGINE_KEY)
// and the mock/offline sim path is synchronous — so with the LIVE_ENGINE
// flag off (the default) this HUD renders 0/0 for that one row and stays
// quiet, exactly as the ground-truth read of protocolClient.ts/backend.ts
// found it: no async engine traffic exists in the mock-only default
// configuration. The row is still shown (not hidden) so the HUD is
// visibly wired and lights up the instant protocol asks start flowing —
// per the brief's point 4.
//
// BUG-499 (Aaron dogfood, 2026-09-01): reported as "a thin green vertical
// line on the far right lower half ... over the top of other information
// ... points to being the queue but I don't understand it". Two problems,
// both fixed here: (1) this HUD used to be `position: fixed` to the
// viewport corner and painted over the right-col fiscal panel underneath
// it regardless of layout — it is now laid out IN-FLOW as a normal
// .right-col flex child (see App.tsx/styles.css), so it owns its own
// space and never overdraws a sibling; (2) even though a `title` tooltip
// already named it, nothing was legible WITHOUT hovering — an
// aria-label plus a one-line always-visible caption now say what it is
// without requiring a mouseover.
//
// Display-only: subscribes to the module-level queueDepthTracker singleton
// (sim/queueDepth.ts) and re-renders on every increment/decrement/reset.
// Never touches SimState/the journal/determinism — pure telemetry, same
// discipline as PerfHud.tsx.
import { useEffect, useState } from 'react';
import { queueDepthTracker, type QueueDepthSnapshot } from '../../sim/queueDepth';

const HUD_LABEL = 'Queue depth HUD — asks currently waiting per backend engine (diagnostic only)';

export function QueueDepthHud() {
  const [snapshot, setSnapshot] = useState<QueueDepthSnapshot>(() => queueDepthTracker.snapshot());

  useEffect(() => queueDepthTracker.subscribe(setSnapshot), []);

  // GR#1: an unexpected/empty tracker state (nothing has ever been tracked)
  // renders a defined placeholder row, never a blank/crashed panel.
  const rows = snapshot.entries.length > 0 ? snapshot.entries : [{ engine: 'protocol', depth: 0, highWaterMark: 0 }];

  return (
    <div className="queue-depth-hud mono" role="group" aria-label={HUD_LABEL} title={HUD_LABEL}>
      <div className="qd-head">
        <span className="qd-title">Queue depth</span>
        <button
          type="button"
          className="qd-reset"
          title="Reset every engine's depth and high-water mark to 0"
          onClick={() => queueDepthTracker.resetAll()}
        >
          reset
        </button>
      </div>
      <p className="qd-caption muted">Asks waiting per backend engine — not the build queue; safe to ignore day-to-day.</p>
      <div className="qd-rows">
        {rows.map((r) => (
          <div key={r.engine} className="qd-row" data-engine={r.engine}>
            <span className="qd-engine">{r.engine}</span>
            <span className="qd-depth">{r.depth}</span>
            <span className="qd-hwm muted">hwm {r.highWaterMark}</span>
          </div>
        ))}
      </div>
      <div className="qd-total">
        <span>total</span>
        <b>{snapshot.total}</b>
        <span className="muted">· hwm {snapshot.totalHighWaterMark}</span>
      </div>
    </div>
  );
}
