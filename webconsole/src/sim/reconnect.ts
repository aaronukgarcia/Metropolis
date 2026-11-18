// reconnect.ts -- FEAT-9995385597 inc1 section E: the pure pieces of the
// browser reconnect state machine (AC-R1 backoff, AC-P5 session id, the
// injectable timer seam). protocolClient.ts owns the state machine; this
// module holds what can be tested without a socket.
//
// Determinism: nothing here reads Date.now or Math.random. The random source
// and the timers are injected (tests pass a scripted RNG and a fake timer
// wheel); the production defaults are crypto.getRandomValues and the global
// setTimeout, resolved lazily so importing this module in plain Node is safe.

/** Tunables for the reconnect machine. Every field is optional on the
 * caller side; unspecified ones take RECONNECT_DEFAULTS. */
export interface ReconnectConfig {
  /** AC-R1: jitter window = min(capMs, baseMs * 2^attempt). */
  baseMs: number;
  capMs: number;
  /** AC-R1: a delay is never below this. */
  minDelayMs: number;
  /** AC-R3: retries scheduled before giving up. */
  maxAttempts: number;
  /** AC-R7: MET-P043 retries before the terminal "open in another tab". */
  busyMaxRetries: number;
  /** AC-R1: live this long before the attempt counter resets. */
  stableLiveMs: number;
  /** AC-W9: socket open -> handshake reply. */
  handshakeTimeoutMs: number;
  /** AC-W9: silence while waking (resets on every server.waking). */
  wakeTimeoutMs: number;
  /** r3 ruling 3: OVERALL deadline from entering `waking`. server.waking
   * progress frames reset wakeTimeoutMs but can never extend this. ASSUMPTION:
   * the AC doc (AC-W9) gives only the 60 s silence timer and the 15 s wake p95
   * target (unverified); 120 s is 2x the silence timer, 8x the p95 target. */
  wakeDeadlineMs: number;
  /** r4 ruling 3 (BACKSTOP): absolute deadline for ONE command, armed at its
   * FIRST send and never restarted by a hold, a re-send or a reconnect. When it
   * fires the command is settled MET-V937 (outcome unknown), wherever it is.
   * ASSUMPTION (for Aaron): 120 s -- twice the wake silence timer, longer than
   * any healthy command round trip; the AC doc gives no figure. */
  commandDeadlineMs: number;
  /** r4 ruling 4: the server.draining reconnectAfterMs floor is clamped to this.
   * ASSUMPTION (for Aaron): 60 s; the AC doc names no client-side maximum. */
  maxFloorMs: number;
}

export const RECONNECT_DEFAULTS: Readonly<ReconnectConfig> = Object.freeze({
  baseMs: 1000,
  capMs: 30000,
  minDelayMs: 250,
  maxAttempts: 12,
  busyMaxRetries: 5,
  stableLiveMs: 10000,
  handshakeTimeoutMs: 10000,
  wakeTimeoutMs: 60000,
  wakeDeadlineMs: 120000,
  commandDeadlineMs: 120000,
  maxFloorMs: 60000,
});

/** AC-R1: full-jitter exponential backoff.
 *
 *   delay = random(0, min(capMs, baseMs * 2^attempt)), then clamped up to
 *   max(minDelayMs, floorMs)
 *
 * `floorMs` is the last server.draining reconnectAfterMs (AC-R2/AC-D4).
 * `random` returns [0,1) and is injected so tests are deterministic. The
 * result is an integer number of milliseconds. */
export function backoffDelayMs(
  attempt: number,
  random: () => number,
  floorMs: number,
  cfg: ReconnectConfig = RECONNECT_DEFAULTS,
): number {
  const a = Math.max(0, Math.floor(attempt));
  // 2 ** a overflows to Infinity for huge a; Math.min with capMs absorbs it.
  const window = Math.min(cfg.capMs, cfg.baseMs * 2 ** a);
  const r = random();
  const jittered = Math.floor((Number.isFinite(r) ? Math.min(Math.max(r, 0), 0.999999999) : 0) * window);
  // r4 ruling 4: a hostile or buggy floor (1 h) is clamped to cfg.maxFloorMs; NaN counts as none.
  const floor = Number.isFinite(floorMs) ? Math.min(Math.max(0, floorMs), cfg.maxFloorMs) : 0;
  return Math.max(jittered, cfg.minDelayMs, floor);
}

/** The timer seam: the production default is the global timers; a test
 * injects a fake wheel it advances by hand (AC-T1: no real timers). */
export interface TimerApi {
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

export function defaultTimers(): TimerApi {
  return {
    setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
    clearTimeout: (h) => globalThis.clearTimeout(h as ReturnType<typeof setTimeout>),
  };
}

let pageSessionId: string | null = null;

/** AC-P5: a random 128-bit token (32 hex chars) from crypto.getRandomValues.
 * Never Math.random. Throws when no CSPRNG exists rather than minting a weak
 * id: the caller (protocolClient) then omits the field, which the server
 * accepts as a legacy client. */
export function mintClientSessionId(
  getRandomValues: (a: Uint8Array) => Uint8Array = (a) => globalThis.crypto.getRandomValues(a),
): string {
  const bytes = getRandomValues(new Uint8Array(16));
  let out = '';
  for (let i = 0; i < bytes.length; i++) out += bytes[i].toString(16).padStart(2, '0');
  return out;
}

/** The page-lifetime id: minted lazily ONCE per page load and held in module
 * memory only. Deliberately never written to sessionStorage/localStorage:
 * sessionStorage is copied when a tab is duplicated, so two tabs would share
 * an id and supersede each other (AC-P5 review amendment, ASM-1630). Returns
 * undefined when no CSPRNG is available. */
export function pageClientSessionId(): string | undefined {
  if (pageSessionId === null) {
    try {
      pageSessionId = mintClientSessionId();
    } catch {
      return undefined;
    }
  }
  return pageSessionId;
}

/** Default jitter source for production: a uniform [0,1) from
 * crypto.getRandomValues, falling back to Math.random only where no CSPRNG
 * exists. Backoff jitter never reaches the engine (GR#21), and tests always
 * inject their own source, so neither branch is on a tested path. */
export function defaultRandom(): number {
  try {
    const a = globalThis.crypto.getRandomValues(new Uint32Array(1));
    return a[0] / 4294967296;
  } catch {
    return Math.random();
  }
}
