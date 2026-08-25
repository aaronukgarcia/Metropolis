import { createContext, useCallback, useContext, useState } from 'react';
import type { ReactNode } from 'react';

interface BusyContextValue {
  busy: boolean;
  run: (fn: () => void | Promise<void>) => void;
}

const Ctx = createContext<BusyContextValue | null>(null);

export function BusyProvider({ children }: { children: ReactNode }) {
  const [busy, setBusy] = useState(false);
  const run = useCallback((fn: () => void | Promise<void>) => {
    setBusy(true);
    setTimeout(() => {
      void (async () => {
        try {
          await fn();
        } finally {
          setTimeout(() => setBusy(false), 60);
        }
      })();
    }, 30);
  }, []);
  return <Ctx.Provider value={{ busy, run }}>{children}</Ctx.Provider>;
}

export function useBusy(): BusyContextValue {
  const v = useContext(Ctx);
  if (!v) throw new Error('useBusy must be used inside BusyProvider');
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
