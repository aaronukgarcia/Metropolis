// consolidatorFocus.ts — FEAT-2326609761 inc1: a dumb last-write-wins mailbox
// for "which section is the top-ranked consolidator opportunity in right
// now", mirroring uistate.ts's publishMapUi/currentMapUi pattern EXACTLY
// (same reasoning: two sibling components — ConsolidatorTab and MapView —
// need to share one piece of UI-only state, and neither can reach the other
// through props without threading it through App).
//
// WHY A MAILBOX AND NOT A LIVE COMPUTATION IN MapView: Aaron's ask was to
// highlight the top-ranked opportunity's section "if you can do it cheaply".
// Computing it requires topOpportunities(state), which runs a full
// O(buildings) section audit — on his real 49,174-building city that is
// unaffordable inside a canvas draw loop that already has to run every
// frame (his own report: 6.1s/tick blocking at that scale). ConsolidatorTab
// already computes this on its own 5-SECOND throttle (consolidatorTab.tsx's
// REFRESH_MS) for its own display; this mailbox lets MapView reuse that
// already-paid-for result instead of recomputing it. If ConsolidatorTab has
// never mounted (or the consolidator is off), the mailbox stays null and
// MapView simply skips the highlight — an honest, cheap degradation.
//
// Pure data, no React — node --test can exercise it directly. Publishing
// this is a UI-layer side effect, NOT a SimState mutation: it carries no
// weight in the journal, savepoint, or determinism story.

let currentTopSectionKey: number | null = null;

/** ConsolidatorTab calls this whenever its own (throttled) topOpportunities() recompute changes the top-ranked section — or with null when it unmounts / the consolidator is off. */
export function publishConsolidatorFocus(topSectionKey: number | null): void {
  currentTopSectionKey = topSectionKey;
}

/** MapView's draw loop reads this — O(1), never a building scan. Null before ConsolidatorTab has ever published, or once it explicitly clears. */
export function currentConsolidatorFocus(): number | null {
  return currentTopSectionKey;
}
