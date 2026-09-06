// saveCodecWorker.ts — BUG-798: the actual Web Worker entry point for
// off-main-thread save compression. Deliberately THIN, mirroring
// simWorker.ts's convention exactly: every line of real logic lives in
// saveCodec.ts's `encode()` (pure, already unit-tested on its own), which is
// the SAME function the synchronous fallback path in saveCodecAsync.ts calls
// — no forked compression logic (GR#21).
//
// Constructed by saveCodecAsync.ts via the standard Vite worker pattern:
//   new Worker(new URL('./saveCodecWorker.ts', import.meta.url), { type: 'module' })
// — this is what makes Vite bundle it as a separate worker chunk in both dev
// and `vite build`, matching simWorker.ts's existing wiring (no new
// vite.config worker configuration needed).
//
// jsdom/node --test cannot construct a real Worker, so this file itself
// carries no direct test coverage (same as simWorker.ts) — the LZ step it
// calls (saveCodec.encode) and the orchestration around it
// (saveCodecAsync.ts) are both covered directly.
import { encode } from './saveCodec.ts';

export interface SaveCodecWorkerRequest {
  requestId: number;
  json: string;
}

export interface SaveCodecWorkerReply {
  requestId: number;
  encoded: string;
}

self.onmessage = (ev: MessageEvent<SaveCodecWorkerRequest>) => {
  const { requestId, json } = ev.data;
  // encode() itself is fail-safe (never throws — see its own doc comment),
  // so no try/catch is needed here; a hostile/corrupt `json` degrades to the
  // uncompressed passthrough exactly as it would on the main thread.
  const encoded = encode(json);
  const reply: SaveCodecWorkerReply = { requestId, encoded };
  (self as unknown as Worker).postMessage(reply);
};
