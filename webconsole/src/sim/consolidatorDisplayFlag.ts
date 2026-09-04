// consolidatorDisplayFlag.ts — FEAT-2326609761 inc2 (Aaron's ruling,
// 2026-09-04): "the red focus box... display can be toggled on/off
// independently of the consolidator itself." A pure localStorage reader/
// writer, mirroring liveEngineFlag.ts's split-out-of-the-.tsx pattern
// exactly, so it's testable without a browser and without JSX in the module
// graph.
//
// WHY LOCALSTORAGE HERE, UNLIKE consolidatorEnabled/consolidatorMode/etc:
// this flag changes NOTHING about the simulation — it is purely "does the
// player currently want to SEE the overlay box", identical in kind to every
// other display-only flag in this codebase (liveEngineFlag.ts itself,
// debugBuildSpeed.ts). The consolidator keeps running, gliding/rotating and
// (once the mutation lane lands) mutating identically whether or not the
// box is drawn — two machines loading the SAME save would compute the SAME
// city with this flag in either state, so it carries no replay-determinism
// risk the way consolidatorEnabled/consolidatorMode/consolidatorSectionMetres/
// consolidatorSliders do (those flow into what the sim state itself becomes,
// this one flows into nothing but a canvas draw call). Per-browser/per-
// machine persistence (which localStorage gives, and journalled sim state
// deliberately does NOT) is exactly the right behaviour for a personal
// "clutter or no clutter" display preference.

/** localStorage key gating the consolidator's red focus-box overlay. Absent or any value other than '0' means SHOWN (visible-by-default, matching Aaron's ask to actually see the tool working — an opt-OUT flag, not opt-in). */
export const CONSOLIDATOR_BOX_VISIBLE_KEY = 'metropolis.consolidatorBoxVisible';

/** Reads the display flag from the given storage-like object (defaults to window.localStorage). Never throws — private-mode/disabled storage degrades to the safe default (shown) rather than crashing the map draw loop. */
export function isConsolidatorBoxVisible(storage?: { getItem(key: string): string | null }): boolean {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return true;
  try {
    return s.getItem(CONSOLIDATOR_BOX_VISIBLE_KEY) !== '0';
  } catch {
    return true;
  }
}

/** Writes the display flag. Never throws (mirrors the reader's fail-safe contract) — a write failure (quota, private mode) just means the preference doesn't persist, not a crash. */
export function setConsolidatorBoxVisible(visible: boolean, storage?: { setItem(key: string, value: string): void }): void {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return;
  try {
    s.setItem(CONSOLIDATOR_BOX_VISIBLE_KEY, visible ? '1' : '0');
  } catch {
    // Silent no-op, matching liveEngineFlag.ts's degrade-not-crash contract.
  }
}
