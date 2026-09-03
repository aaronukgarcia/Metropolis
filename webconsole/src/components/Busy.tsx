import { createContext, useCallback, useContext, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { codedError, normalizeThrowable, recordError } from '../sim/backend';

interface BusyContextValue {
  busy: boolean;
  run: (fn: () => void | Promise<void>) => void;
}

const Ctx = createContext<BusyContextValue | null>(null);

// BUG-604 (Aaron Q100080 = A): the "Working…" chip must show ONLY when an
// action blocks the UI for longer than this many ms — a chip flashed for
// every wrapped action (including ones that settle in a handful of ms) is
// noise, not signal. ⚠ PLACEHOLDER: 250ms is Aaron's stated ruling, not a
// balance-tuned number; revisit if dogfood shows it wrong in either
// direction.
const WORKING_CHIP_DELAY_MS = 250;

export function BusyProvider({ children }: { children: ReactNode }) {
  const [busy, setBusy] = useState(false);
  // BUG-614: overlapping run() calls each had their own unconditional
  // setBusy(false) at the end of their 60ms linger — the LAST call to settle
  // would win, but a call that settles EARLIER (while a sibling call is
  // still in flight) would flip busy=false out from under the still-running
  // sibling, flickering the chip off mid-action. activeRef is a plain
  // in-flight counter (not React state — it must not itself trigger a
  // render, and reads/writes here are always inside the same macrotask
  // callback so there is no concurrent-mutation hazard): every run() call
  // increments it once it starts (after the existing 30ms defer, matching
  // where its chip timer starts) and decrements it once its own 60ms linger
  // elapses. setBusy(false) fires only when the decrement brings the count
  // to zero, i.e. only the LAST outstanding call's linger can hide the chip.
  const activeRef = useRef(0);
  const run = useCallback((fn: () => void | Promise<void>) => {
    // UNCHANGED scheduling semantics: this 30ms defer lets React paint
    // before the (possibly synchronous, blocking) fn runs — it has nothing
    // to do with chip visibility, and fn's execution timing is untouched by
    // the BUG-604 fix below.
    setTimeout(() => {
      // BUG-604: the chip's VISIBILITY is gated behind its own timer,
      // separate from fn's actual execution. If fn settles before this
      // fires, the timer is cancelled and setBusy(true) never runs — the
      // chip never appears for a fast (<250ms) action. If it fires first,
      // the chip shows and stays up through the existing 60ms linger below.
      let settled = false;
      activeRef.current += 1;
      const chipTimer = setTimeout(() => {
        if (!settled) setBusy(true);
      }, WORKING_CHIP_DELAY_MS);

      void (async () => {
        try {
          await fn();
        } catch (e) {
          // BUG-619: fn() throwing sync or rejecting async used to escape
          // this IIFE uncaught — a genuine unhandled promise rejection.
          // Caught and reported through the same registry-coded pattern
          // backend.ts's window/unhandledrejection handlers already use
          // (BAR-F1: a NAMED code on the thrown value, e.g. from
          // codedError(), wins; otherwise no code is forced — there is no
          // registered MET- code yet for "a Busy-wrapped background action
          // failed" specifically, and GR#7 forbids inventing one here).
          // The finally block below still runs unconditionally, so the
          // BUG-604/614 chip-timer-cancel + refcount/linger semantics are
          // byte-identical whether fn() succeeded, threw, or rejected.
          const normalized = normalizeThrowable(e);
          recordError(normalized.message, {
            type: 'app',
            stack: normalized.stack,
            action: 'busy-run',
            code: (normalized as any)?.code,
          });
        } finally {
          settled = true;
          clearTimeout(chipTimer);
          // Existing 60ms linger — unchanged. BUG-614: the setBusy(false) at
          // the end of it is now guarded by the refcount — it only fires
          // when THIS call's decrement is the one that brings the in-flight
          // count to zero, i.e. no sibling call is still outstanding.
          setTimeout(() => {
            activeRef.current = Math.max(0, activeRef.current - 1);
            if (activeRef.current === 0) setBusy(false);
          }, 60);
        }
      })();
    }, 30);
  }, []);
  return <Ctx.Provider value={{ busy, run }}>{children}</Ctx.Provider>;
}

export function useBusy(): BusyContextValue {
  const v = useContext(Ctx);
  // FEAT-1972079916/GR#7 (BAR-F1): real registry-sourced code MET-V806 via .code.
  if (!v) throw codedError('MET-V806', 'useBusy must be used inside BusyProvider');
  return v;
}

export function BusyIndicator() {
  const { busy } = useBusy();
  if (!busy) return null;
  return (
    <div className="busy-chip">
      <span className="spinner" />
      Working…
    </div>
  );
}
