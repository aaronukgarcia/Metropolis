// FEAT-webworker-sim-offload — Stage 1 / Landing 2 (2026-09-02): the
// tick-worker offload rollout flag.
//
// FEAT-2326609771 (2026-09-04, Aaron's Q100116 answer + the 2026-09-03
// interview, both ruling "Default ON now"): the flag flips from opt-in/
// default-OFF to a KILL-SWITCH that defaults ON. The opt-in era is over —
// every browser now boots the worker path by default; the flag exists so a
// player (or a future incident) can still force the old synchronous
// main-thread path with one localStorage write, never so they have to opt
// IN to get the new path.
//
// PRECEDENCE (this is the whole contract — read this before changing the
// resolution logic below):
//   1. `typeof Worker === 'undefined'` (no real Worker constructor in this
//      runtime — SSR, node --test, a locked-down browser) -> always OFF,
//      regardless of the stored value. A missing capability is not a user
//      choice.
//   2. Explicit OFF spellings ('off' | '0' | 'false', case/whitespace
//      insensitive) -> OFF. This is the kill switch: it must keep working
//      exactly as before for anyone who already has one of these values
//      sitting in localStorage from the opt-in era (nobody could have
//      written an explicit-off value before this rollout — the default was
//      already off — but the FLAG SPELLING itself is unchanged, so a value
//      a tester set while poking at the opt-in build, or a future
//      hand-written 'off', still means off).
//   3. Explicit ON spellings ('on' | '1' | 'true') -> ON. '1'/'true' are the
//      pre-rollout opt-in spellings (kept working verbatim for anyone who
//      already flipped them on); 'on' is added as the natural spelling now
//      that ON is the default and a user is more likely to write 'on' than
//      '1' if they ever turn the kill switch back on after using it.
//   4. Anything else — UNSET (no value in localStorage at all) or an
//      unrecognized/junk value — resolves to ON. This is the actual default
//      flip: previously "anything not exactly '1'/'true'" meant OFF; now
//      only an EXPLICIT off spelling means OFF. A junk value (a stray typo,
//      a value some completely unrelated code wrote to this key) is treated
//      the same as unset rather than fail-closed to the OLD default, because
//      the old default no longer exists — there is no longer a safe
//      "not sure -> off" fallback to fail into.
//
// Pure, side-effect-free EXCEPT for the localStorage/Worker global reads
// (both environment queries, not simulation state — GR#21 is about the
// reducer/SimState, not this kind of runtime-capability probe).
const WEBWORKER_FLAG_KEY = 'metropolis.webworker';

const EXPLICIT_OFF = new Set(['off', '0', 'false']);
const EXPLICIT_ON = new Set(['on', '1', 'true']);

/**
 * True unless the kill switch is explicitly off, or a real Worker
 * constructor is unavailable in this runtime (AC-8: `typeof Worker ===
 * 'undefined'` — or a throwing construction, handled by the caller — must
 * fall back silently to the main-thread reducer path, never fail to boot).
 * Guards `window`/`localStorage` access too: SSR/mount-smoke-test
 * environments (mount.test.tsx) stub a minimal `globalThis.window` with no
 * `localStorage`, and this must degrade to "disabled" rather than throw.
 *
 * See the file header above for the full unset/on/off/junk precedence table
 * — this function is a direct, literal implementation of it.
 */
export function webWorkerOffloadEnabled(): boolean {
  try {
    if (typeof Worker === 'undefined') return false;
    if (typeof window === 'undefined' || !window.localStorage) return false;
    const raw = window.localStorage.getItem(WEBWORKER_FLAG_KEY);
    if (raw === null) return true; // unset -> default ON (the rollout).
    const normalized = raw.trim().toLowerCase();
    if (EXPLICIT_OFF.has(normalized)) return false;
    if (EXPLICIT_ON.has(normalized)) return true;
    // Junk/unrecognized value: not an explicit off, so fall through to the
    // new default (ON) rather than the old fail-closed-to-off behaviour —
    // there is no "safe old default" left to fail into post-rollout.
    return true;
  } catch {
    // Fail-closed to "disabled" — never let a flag-read error (quota,
    // disabled storage, non-browser test runtime) crash the app; the
    // fallback (main-thread reducer) is always correct, just less fast.
    return false;
  }
}
