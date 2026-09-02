// FEAT-webworker-sim-offload — Stage 1 / Landing 2 (2026-09-02): the
// opt-in-able rollout flag for the tick-worker offload (AC-8 fallback,
// spec §7 Q1/Q3 — Aaron has not yet ruled on a default-ON rollout, so this
// ships OPT-IN/default-OFF for safety: the responsiveness win is real but
// unverified in real dogfood sessions, and the untouched fallback path is
// exactly today's code — see simWorkerProtocol.ts's scope note). Flip on
// with `localStorage.setItem('metropolis.webworker', '1')` (or 'true').
//
// Pure, side-effect-free EXCEPT for the localStorage/Worker global reads
// (both environment queries, not simulation state — GR#21 is about the
// reducer/SimState, not this kind of runtime-capability probe).
const WEBWORKER_FLAG_KEY = 'metropolis.webworker';

/**
 * True only when BOTH the opt-in flag is set AND a real Worker constructor
 * is available in this runtime (AC-8: `typeof Worker === 'undefined'` — or a
 * throwing construction — must fall back silently to the main-thread
 * reducer path, never fail to boot). Guards `window`/`localStorage` access
 * too: SSR/mount-smoke-test environments (mount.test.tsx) stub a minimal
 * `globalThis.window` with no `localStorage`, and this must degrade to
 * "disabled" rather than throw.
 */
export function webWorkerOffloadEnabled(): boolean {
  try {
    if (typeof Worker === 'undefined') return false;
    if (typeof window === 'undefined' || !window.localStorage) return false;
    const raw = window.localStorage.getItem(WEBWORKER_FLAG_KEY);
    return raw === '1' || raw === 'true';
  } catch {
    // Fail-closed to "disabled" — never let a flag-read error (quota,
    // disabled storage, non-browser test runtime) crash the app; the
    // fallback (main-thread reducer) is always correct, just less fast.
    return false;
  }
}
