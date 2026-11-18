// engineMoney.ts — BUG-767: the ONE webconsole-side statement of the Go
// engine's money scale, so no display re-derives it as a magic number.
// Mirrors internal/foundation/det/money.go's MicropoundsPerPound
// (const MicropoundsPerPound Micropounds = 1_000): since BUG-452 (2026-09-01)
// the engine's "micropound" unit is really 1e-3 GBP, whatever its name, and
// the wire fields called ...Micropounds carry that scale.
export const MICROPOUNDS_PER_POUND = 1_000;

/** Converts an engine money amount (wire "...Micropounds" field) to pounds. */
export function engineMoneyToPounds(engineMoney: number): number {
  return engineMoney / MICROPOUNDS_PER_POUND;
}
