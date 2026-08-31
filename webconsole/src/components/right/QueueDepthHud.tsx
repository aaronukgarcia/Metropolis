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
// Display-only: subscribes to the module-level queueDepthTracker singleton
// (sim/queueDepth.ts) and re-renders on every increment/decrement/reset.
// Never touches SimState/the journal/determinism — pure telemetry, same
// discipline as PerfHud.tsx.
import { useEffect, useState } from 'react';
import { queueDepthTracker, type QueueDepthSnapshot } from '../../sim/queueDepth';

export function QueueDepthHud() {
  const [snapshot, setSnapshot] = useState<QueueDepthSnapshot>(() => queueDepthTracker.snapshot());

  useEffect(() => queueDepthTracker.subscribe(setSnapshot), []);

  const rows = snapshot.entries.length > 0 ? snapshot.entries : [{ engine: 'protocol', depth: 0, highWaterMark: 0 }];

  return (
    <div className="queue-depth-hud mono" title="Queue Depth HUD — asks currently waiting per engine (diagnostic only, FEAT-1972079938)">
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
