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
//
// BUG-605 second half (Aaron Q100081 = C, both queues visible, 2026-09-03):
// the protocol-asks section above was always visible but, until now, was the
// ONLY thing this panel showed — with the metropolis.webworker flag off (the
// default) it read a permanently dim 0/0 and said nothing about the actual
// tick-worker offload (FEAT-webworker-sim-offload). Aaron's ruling: show a
// SECOND section, unconditionally, for the tick-queue itself:
//   - flag ON:  the REAL worker backlog depth + supersede-streak honesty
//     already tracked by workerQueueDepth.ts's global singleton (the same
//     tracker PerfHud.tsx's dev-only overlay reads) — no new instrumentation,
//     just a second reader of what store.tsx already writes.
//   - flag OFF: there is no worker backlog to report (ticks never leave the
//     main thread), so instead of a fake/misleading 0 this shows the real
//     main-thread tick cost via perfhud.ts's DEV-only global tick tracker
//     (store.tsx only records into it when import.meta.env.DEV) when that
//     data exists, or else the honest, literal explanation — "worker off —
//     ticks run on the main thread" — never a dim placeholder number.
// No dev-flag gates the panel's presence or this section; only the CONTENT
// of the worker line depends on the webworker flag / DEV tick data.
import { useEffect, useState } from 'react';
import { queueDepthTracker, type QueueDepthSnapshot } from '../../sim/queueDepth';
import { webWorkerOffloadEnabled } from '../../sim/webWorkerFlag';
import { getGlobalWorkerQueueTracker } from '../../sim/workerQueueDepth';
import { getGlobalTickTracker, tickMetrics } from '../../sim/perfhud';

const HUD_LABEL = 'Queue depth HUD — asks currently waiting per backend engine (diagnostic only)';

// How often the worker-backlog section polls its tracker. workerQueueDepth.ts
// exposes no subscribe/observer hook (unlike queueDepthTracker above) — it is
// a plain read-accessor mirroring PerfHud.tsx's own 1Hz poll of the same
// singleton, so this reuses that cadence rather than inventing a new one.
const WORKER_POLL_MS = 1000;

export function QueueDepthHud() {
  const [snapshot, setSnapshot] = useState<QueueDepthSnapshot>(() => queueDepthTracker.snapshot());
  const [workerDepth, setWorkerDepth] = useState(0);
  const [workerStreak, setWorkerStreak] = useState(0);
  const [tickAvgMs, setTickAvgMs] = useState<number | null>(null);

  useEffect(() => queueDepthTracker.subscribe(setSnapshot), []);

  const workerOn = webWorkerOffloadEnabled();

  // Poll the worker/tick trackers on the same cadence PerfHud.tsx already
  // uses. Re-runs whenever workerOn changes so a mid-session flag flip (e.g.
  // a dogfood toggling localStorage) re-targets which tracker is read.
  useEffect(() => {
    const readOnce = () => {
      if (workerOn) {
        const tracker = getGlobalWorkerQueueTracker();
        setWorkerDepth(tracker.depth());
        setWorkerStreak(tracker.supersedeStreak());
      } else {
        const tickTracker = getGlobalTickTracker();
        setTickAvgMs(tickTracker ? tickMetrics(tickTracker).avgMs : null);
      }
    };
    readOnce();
    const id = setInterval(readOnce, WORKER_POLL_MS);
    return () => clearInterval(id);
  }, [workerOn]);

  // GR#1: an unexpected/empty tracker state (nothing has ever been tracked)
  // renders a defined placeholder row, never a blank/crashed panel.
  const rows = snapshot.entries.length > 0 ? snapshot.entries : [{ engine: 'protocol', depth: 0, highWaterMark: 0 }];

  // BUG-605: the tick-queue line is ALWAYS meaningful, never a dim 0/0 —
  // three honest states depending on what is actually measurable right now.
  let workerLine: string;
  if (workerOn) {
    workerLine =
      workerStreak > 0
        ? `worker: ${workerStreak} tick(s) superseded — catching up`
        : `worker: ${workerDepth} pending`;
  } else if (tickAvgMs !== null) {
    workerLine = `worker off — sync mode, last tick ${tickAvgMs.toFixed(2)}ms avg (main thread)`;
  } else {
    workerLine = 'worker off — ticks run on the main thread';
  }

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
      <p className="qd-worker-line" data-testid="qd-worker-line">
        {workerLine}
      </p>
    </div>
  );
}
