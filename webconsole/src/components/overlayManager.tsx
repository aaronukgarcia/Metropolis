// overlayManager.tsx — FEAT-2326609720 inc1: the single owner of blocking-
// overlay stacking (Aaron: "no ui is to go over the top of another").
//
// Problem this closes: today InsolvencyPopup, ForcedAssetSalesPanel and
// DeclineScreen (MapView.tsx) each independently decide whether to render,
// purely from reading their own slice of SimState. Nothing stops two of them
// wanting to show at once — the CLASS of bug (BUG-496/497/498/499/500/501)
// kept recurring because there was no single arbiter, only ad-hoc CSS
// z-index numbers and per-component "return null" guards that each had to
// individually remember every OTHER overlay's existence to stay correct.
//
// The fix: a small React context that any candidate overlay REGISTERS with
// (id + priority) while it wants to be visible. The context recomputes, via
// the pure `resolveTopOverlay` function below, which single registered id is
// allowed to actually render. `useBlockingOverlay` is the hook components
// call; it returns true ONLY for the winning id, so a component that would
// otherwise render (its own SimState condition is true) is structurally
// FORCED to render nothing while a higher-priority overlay is up — the
// invariant is enforced by the resolver, not by every component remembering
// every sibling.
//
// Deliberately generalised beyond the four named BLOCKING_OVERLAY_ID
// candidates (string id + numeric priority, not a closed union) so a future
// overlay (e.g. a save-conflict dialog) can join the invariant without
// editing this file.
//
// Fail-open outside a provider: `useBlockingOverlay` falls back to returning
// its own `active` flag verbatim when no OverlayManagerProvider is mounted
// above it. This matters for the existing unit-test suite (bug498/499/500/
// 501, mount.test.tsx, imf-insolvency-inc*) which mounts MapView directly
// under SimContext.Provider + BusyProvider with NO OverlayManagerProvider —
// exactly today's behaviour, preserved. The real app (App.tsx) wraps the
// whole tree in OverlayManagerProvider, so real gameplay gets the full
// cross-tree invariant (it spans store.tsx's RebuildPrompt and MapView.tsx's
// three modals, which live in different component subtrees).

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

/** A snapshot of {id: priority} for every candidate currently wanting to show. */
export type OverlayRegistry = Readonly<Record<string, number>>;

/**
 * Pure resolver: lower priority number wins. Ties break on id string
 * (deterministic — GR#21, no reliance on object/Map insertion order).
 * Exported standalone so it is directly unit-testable with plain objects,
 * no React rendering required.
 */
export function resolveTopOverlay(registry: OverlayRegistry): string | null {
  let winner: string | null = null;
  let winnerPriority = Number.POSITIVE_INFINITY;
  for (const id of Object.keys(registry)) {
    const priority = registry[id];
    if (
      priority < winnerPriority ||
      (priority === winnerPriority && winner !== null && id < winner)
    ) {
      winner = id;
      winnerPriority = priority;
    }
  }
  return winner;
}

interface OverlayManagerContextValue {
  register: (id: string, priority: number) => void;
  unregister: (id: string) => void;
  topId: string | null;
}

const OverlayManagerContext = createContext<OverlayManagerContextValue | null>(null);

export function OverlayManagerProvider({ children }: { children: ReactNode }) {
  const [registry, setRegistry] = useState<OverlayRegistry>({});

  const register = useCallback((id: string, priority: number) => {
    setRegistry((prev) => (prev[id] === priority ? prev : { ...prev, [id]: priority }));
  }, []);

  const unregister = useCallback((id: string) => {
    setRegistry((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  const topId = useMemo(() => resolveTopOverlay(registry), [registry]);

  const value = useMemo(() => ({ register, unregister, topId }), [register, unregister, topId]);

  return <OverlayManagerContext.Provider value={value}>{children}</OverlayManagerContext.Provider>;
}

/**
 * useBlockingOverlay — registers this component as WANTING to show a
 * blocking overlay while `active` is true, at the given `priority` (lower
 * wins). Returns true ONLY while this id is the resolver's single winner.
 *
 * Callers MUST call this unconditionally (rules-of-hooks) and check the
 * returned boolean (together with their own `active` condition) before
 * their early `return null` — see InsolvencyPopup/ForcedAssetSalesPanel/
 * DeclineScreen in MapView.tsx for the pattern.
 */
export function useBlockingOverlay(id: string, priority: number, active: boolean): boolean {
  const ctx = useContext(OverlayManagerContext);

  useEffect(() => {
    if (!ctx) return;
    if (active) ctx.register(id, priority);
    else ctx.unregister(id);
    return () => ctx.unregister(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx, id, priority, active]);

  if (!ctx) return active;
  return active && ctx.topId === id;
}

/**
 * useEscapeKey — I-4 / AC-4/5/6/13 helper: while `active` is true, pressing
 * Escape calls `onEscape` exactly once per keypress. Intentionally a
 * SEPARATE listener from MapView's global keydown handler (keyhandler.ts),
 * which owns Escape's map-tool-cancel semantics — this hook only ever fires
 * for a component that has explicitly asked to own Escape while it is the
 * active blocking overlay, so a stray cancelToSelect() alongside a modal
 * dismiss is a harmless no-op (clearing tool/selection state that is not
 * visible while the modal's backdrop covers the map), never a conflicting
 * action. Unifying the two into one priority chain is a reasonable
 * follow-up once the tab-tree replan lands and MapView's global handler is
 * being touched anyway; flagged in the build report for the lead.
 */
export function useEscapeKey(active: boolean, onEscape: () => void): void {
  useEffect(() => {
    if (!active) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onEscape();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, onEscape]);
}
