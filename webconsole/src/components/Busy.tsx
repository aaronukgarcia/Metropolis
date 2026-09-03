import { createContext, useCallback, useContext, useState } from 'react';
import type { ReactNode } from 'react';
import { codedError } from '../sim/backend';

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
      const chipTimer = setTimeout(() => {
        if (!settled) setBusy(true);
      }, WORKING_CHIP_DELAY_MS);

      void (async () => {
        try {
          await fn();
        } finally {
          settled = true;
          clearTimeout(chipTimer);
          // Existing 60ms linger — unchanged. A harmless no-op setBusy(false)
          // when the chip was never shown (fast path).
          setTimeout(() => setBusy(false), 60);
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
