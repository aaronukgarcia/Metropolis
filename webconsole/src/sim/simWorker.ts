// FEAT-webworker-sim-offload — Stage 1 / Landing 2 (2026-09-02): the actual
// Web Worker entry point. Deliberately THIN — every line of real logic lives
// in simWorkerProtocol.ts's runTick(), which is unit-testable directly
// (jsdom/node --test cannot construct a real Worker, so this file itself
// carries no test coverage of its own; test/simworker-offload.test.mjs
// proves runTick()'s behavior, which is all this file calls).
//
// Constructed by store.tsx via the standard Vite worker pattern:
//   new Worker(new URL('./simWorker.ts', import.meta.url), { type: 'module' })
// — this is what makes Vite bundle it as a separate worker chunk in both dev
// and `vite build` (no custom vite.config worker wiring needed, per the
// research pass: no existing worker config to interact with).
//
// GR#21: imports the SAME `reducer` (via simWorkerProtocol's runTick) that
// the main-thread fallback path calls — no forked logic.
import { runTick } from './simWorkerProtocol.ts';
import type { MainToWorkerMessage, WorkerToMainMessage } from './simWorkerProtocol.ts';

self.onmessage = (ev: MessageEvent<MainToWorkerMessage>) => {
  const msg = ev.data;
  if (msg.type === 'runTick') {
    const nextState = runTick(msg.state);
    const reply: WorkerToMainMessage = { type: 'tickResult', state: nextState, requestId: msg.requestId };
    (self as unknown as Worker).postMessage(reply);
  }
};
