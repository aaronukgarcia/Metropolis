// financeStatusTracker.ts — BUG-723 round finding F1: the live-Go-engine
// finance status seam. Mirrors queueDepth.ts's EXACT established pattern
// (a framework-free, module-level singleton observable store, instrumented
// where engine-fed data already flows and subscribed to by any component
// that wants it) rather than inventing a new architecture: queueDepth.ts is
// "PURE TELEMETRY... NEVER reads or mutates SimState... React wiring lives
// in components/right/QueueDepthHud.tsx" — this file is the same shape for
// FinanceAPI's payroll-shortfall status surface (BUG-548/BUG-723).
//
// WHY NOT ROUTE THROUGH SimState/the reducer: newsFeed.ts's own header is
// explicit that it "never mutates SimState, is never journaled, is never
// replayed, and carries no determinism surface" — the mock local sim engine
// (engine.ts/store.tsx) is deterministic, journaled and replayed (GR#21),
// and the Go live engine's finance data must never touch it. This tracker
// is the SAME kind of side-channel LiveEngineBadge.tsx already reads from
// (a real WebSocket-fed value with zero sim/journal coupling), just made
// OBSERVABLE by more than one component instead of being trapped inside
// LiveEngineBadge's own local useState.
//
// Instrumented from: components/LiveEngineBadge.tsx's onDelta handler (the
// only current decoder of "f2.finance" patches — see wire.ts's
// decodeFinanceBalanceSheetPatch). Consumed by: components/NewsFeed.tsx.

import type { FinancePayrollShortfallView } from './wire.ts';

/** Null means "no live-engine finance data has arrived this session" —
 *  distinct from a populated view whose amountMicropounds is 0 (a real
 *  "no shortfall this month" reading from a connected engine). */
export type FinanceStatusSnapshot = FinancePayrollShortfallView | null;

type Listener = (snapshot: FinanceStatusSnapshot) => void;

/**
 * FinanceStatusTracker: an observable holder of the most recently decoded
 * "f2.finance" payrollShortfall view. setPayrollShortfall is the only
 * mutator a protocol-client consumer needs; subscribe() drives any UI
 * surface (NewsFeed today, others later) without those surfaces needing
 * their own WebSocket/decode plumbing.
 */
export class FinanceStatusTracker {
  private snapshotValue: FinanceStatusSnapshot = null;
  private listeners = new Set<Listener>();

  /** Record the latest decoded payrollShortfall view (or null to mean "no
   *  live data" — e.g. on disconnect). Never throws; a null/undefined
   *  argument is normalised to null rather than left ambiguous. */
  setPayrollShortfall(view: FinanceStatusSnapshot | undefined): void {
    this.snapshotValue = view ?? null;
    this.emit();
  }

  snapshot(): FinanceStatusSnapshot {
    return this.snapshotValue;
  }

  /** Subscribe to every update. Fires the listener immediately with the
   *  current snapshot on subscribe (matches queueDepth.ts's own
   *  subscribe() convention). Returns an unsubscribe function. */
  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    listener(this.snapshotValue);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** Test/dev-only reset back to "no live data" — mirrors queueDepth's
   *  resetAll(), used by tests so one suite's writes never leak into the
   *  next via the shared module-level singleton. */
  reset(): void {
    this.snapshotValue = null;
    this.emit();
  }

  private emit(): void {
    for (const l of this.listeners) l(this.snapshotValue);
  }
}

/** Module-level singleton — mirrors queueDepth.ts's queueDepthTracker
 *  convention. LiveEngineBadge.tsx writes to this instance; any component
 *  (NewsFeed.tsx today) reads from it via subscribe(). */
export const financeStatusTracker = new FinanceStatusTracker();
