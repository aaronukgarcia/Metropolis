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
import { applyStateDelta, diffSimState, DeltaBasisMismatchError } from './simWorkerDelta.ts';
import type { SimState } from './types.ts';

// FEAT-2326609777 (2026-09-06) — this worker's own persistent cache of the
// last full SimState it is known to hold, i.e. the exact state store.tsx's
// `workerKnownStateRef` mirrors on the main-thread side. Populated on every
// reply (full `runTick` or delta `runTickDelta` alike) so a subsequent
// `runTickDelta` request always has a matching base to patch against — see
// simWorkerDelta.ts's header for the full protocol/invariant writeup. A
// fresh worker instance always starts with no cache (main always re-syncs
// from scratch via a full `runTick` the first time — see store.tsx's
// issueTickRequest, which only ever sends `runTickDelta` once it has
// received proof, via `deltaCapable`, that THIS worker instance holds one).
let cachedState: SimState | null = null;

self.onmessage = (ev: MessageEvent<MainToWorkerMessage>) => {
  const msg = ev.data;
  if (msg.type === 'runTick') {
    const preTick = msg.state;
    const nextState = runTick(preTick);
    cachedState = nextState;
    const reply: WorkerToMainMessage = {
      type: 'tickResult',
      state: nextState,
      requestId: msg.requestId,
      deltaCapable: true,
    };
    (self as unknown as Worker).postMessage(reply);
    return;
  }
  if (msg.type === 'runTickDelta') {
    // Protocol invariant (simWorkerDelta.ts's header; enforced by
    // store.tsx's issueTickRequest, which only ever sends this message type
    // once a prior `deltaCapable` reply proved this worker instance holds
    // `cachedState`): `cachedState` is guaranteed non-null here under normal
    // operation. FEAT-2326609777 round follow-up (opus-round-feat777,
    // 2026-09-06): guard it explicitly anyway, and ALSO catch a
    // DeltaBasisMismatchError from applyStateDelta (base.tick disagreeing
    // with delta.baseTick — the round's "no integrity check on the delta
    // basis" finding: a same-shape-but-wrong-VALUE basis previously
    // corrupted silently). Either way, this worker cannot safely compute a
    // tick against a basis it does not actually hold — reset its own cache
    // to null (so its NEXT request of any type is treated as a fresh full
    // sync) and reply with `basisMismatch` instead of attempting the tick,
    // rather than let `worker.onerror`'s blunter "give up on this worker
    // instance forever" fallback be the only recovery path.
    if (!cachedState || msg.delta.baseTick !== cachedState.tick) {
      const actualBaseTick = cachedState ? cachedState.tick : -1;
      cachedState = null;
      const reply: WorkerToMainMessage = {
        type: 'basisMismatch',
        requestId: msg.requestId,
        expectedBaseTick: msg.delta.baseTick,
        actualBaseTick,
      };
      (self as unknown as Worker).postMessage(reply);
      return;
    }
    try {
      const preTick = applyStateDelta(cachedState, msg.delta);
      const nextState = runTick(preTick);
      const delta = diffSimState(preTick, nextState);
      cachedState = nextState;
      const reply: WorkerToMainMessage = { type: 'tickResultDelta', requestId: msg.requestId, delta };
      (self as unknown as Worker).postMessage(reply);
    } catch (err) {
      // Belt-and-braces: the explicit tick-comparison guard above already
      // catches the expected mismatch shape; this only fires if
      // applyStateDelta throws for some OTHER reason (e.g. the building-id
      // corruption guard in applyBuildingsDelta). Same recovery either way.
      if (err instanceof DeltaBasisMismatchError) {
        cachedState = null;
        const reply: WorkerToMainMessage = {
          type: 'basisMismatch',
          requestId: msg.requestId,
          expectedBaseTick: err.expectedBaseTick,
          actualBaseTick: err.actualBaseTick,
        };
        (self as unknown as Worker).postMessage(reply);
        return;
      }
      throw err;
    }
  }
};
