// overlayLayers.ts — FEAT-2326609720 inc1: the OVERLAY DISCIPLINE foundation.
//
// Aaron's hard requirement, verbatim: "no ui is to go over the top of
// another." This module is the SINGLE SOURCE OF TRUTH (GR#3) for:
//
//   1. The z-index SCALE every HUD overlay must draw from — no component or
//      stylesheet rule is to invent a new magic z-index number. Add a named
//      constant here first, then reference it.
//   2. The BLOCKING-OVERLAY PRIORITY ORDER — when more than one full-screen
//      blocking overlay WANTS to be visible at once (Rebuild Prompt,
//      Decline Screen, Insolvency Popup, Forced Asset Sales), exactly one
//      may render as a blocking overlay. `resolveBlockingOverlay` is the
//      pure, deterministic (GR#21 — no Math.random, no wall-clock, no map
//      iteration order) function that decides which one, given a snapshot of
//      which candidates currently want to show.
//
// Scope note for the lead (Bev): the FEAT-2326609720 acceptance-criteria doc
// (docs/planning/acceptance/FEAT-2326609720-hud-overlay-replan.md) proposes a
// FUTURE canonical scale (canvas=0, left-tab column=100, right gutter=110,
// modal backdrop=500, modal content=501, toast=200) that assumes the tab-tree
// replan has landed. This increment is EXPLICITLY the structural invariant
// only — tab grouping and RAG colours are out of scope pending Aaron's
// sign-off on that taxonomy (see the doc's "Assumptions for Aaron/Bev" §3).
// Renumbering every existing overlay to the future scale now would touch far
// more files than this increment's blast radius should, and several EXISTING
// regression tests (bug500-advisor-click-overlap.test.tsx line 85,
// mount.test.tsx's "BUG-497 (3)" test) assert LITERAL z-index numbers against
// styles.css via regex — a blind renumber would turn tests that currently
// prove real fixes into false negatives. So: this file promotes the CURRENT,
// already-monotonic z-index ordering (verified below) to be the ratified v1
// SSOT scale, with room reserved for the future canonical numbers once the
// tab-tree lands. Every value below must equal the literal z-index still
// declared in styles.css for that selector — see
// test/hud-overlay-discipline.test.tsx's "Z_INDEX registry stays in sync with
// styles.css" case, which fails loudly the moment they drift apart.

/** Named z-index scale (v1 — see the scope note above). Higher wins. */
export const Z_INDEX = {
  /** .forced-asset-sales-panel — the lowest-priority floating dialog. */
  FORCED_ASSET_SALES_PANEL: 17,
  /** .insolvency-banner (warning/crisis/administration/second-bailout). */
  INSOLVENCY_BANNER: 18,
  /** .news-feed — FEAT-2326609784: the scrolling news feed. Replaces the old
   *  .place-notice-banner (19) / .levelup-banner (20) stacked popups; kept at
   *  the higher of the two retired slots since it is now the only occupant. */
  NEWS_FEED: 20,
  /** .busy-chip — the "working…" indicator. */
  BUSY_CHIP: 99,
  /** .about-backdrop / .spec-card-backdrop / .help-overlay-backdrop — the
   *  shared "informational dialog" toast/panel tier. */
  INFO_DIALOG: 200,
  /** .insolvency-popup-overlay — the one-shot bailout-entry blocking popup. */
  INSOLVENCY_POPUP: 210,
  /** .perf-hud — dev-only performance panel, deliberately above ordinary
   *  gameplay dialogs so it is never accidentally buried while debugging. */
  PERF_HUD: 1000,
  /** .decline-screen-overlay — the hard game-over blocking screen. Must beat
   *  every ordinary gameplay overlay (mount.test.tsx BUG-497(3) proves this
   *  mechanically against styles.css). */
  DECLINE_SCREEN: 2000,
  /** .decline-reopen-chip — the small non-blocking affordance shown after the
   *  player closes the Decline Screen without resolving it (AC-6/I-4); one
   *  below DECLINE_SCREEN so re-opening the real overlay always wins. */
  DECLINE_REOPEN_CHIP: 1999,
  /** .playmode-banner — persistent "not a simulation" banner, pinned above
   *  even the decline screen so it is never buried by a future overlay. */
  PLAYMODE_BANNER: 3000,
  /** .stale-build-banner — dev-server staleness guard, the single most
   *  urgent developer-facing fact when it fires. */
  STALE_BUILD_BANNER: 3001,
  /** RebuildPrompt's inline overlayStyle — the boot-time engine-version
   *  decision gate. Must win over literally everything else in the app,
   *  including the decline screen, because the game state underneath may
   *  not even be trustworthy until this resolves. */
  REBUILD_PROMPT: 10000,
} as const;

export type ZIndexName = keyof typeof Z_INDEX;

// ---------------------------------------------------------------------------
// Single-blocking-overlay invariant
// ---------------------------------------------------------------------------

/**
 * Every full-screen / stranding-capable overlay in the app gets a stable id
 * and a priority RANK (lower rank number = higher priority = wins). This is
 * intentionally a plain string id, not a closed union, so a future overlay
 * can register itself without editing this file's type — but the four
 * current candidates are named here as the canonical ids other components
 * should import rather than re-typing string literals.
 */
export const BLOCKING_OVERLAY_ID = {
  REBUILD_PROMPT: 'rebuildPrompt',
  DECLINE_SCREEN: 'declineScreen',
  INSOLVENCY_POPUP: 'insolvencyPopup',
  FORCED_ASSET_SALES: 'forcedAssetSales',
} as const;

export type BlockingOverlayId = (typeof BLOCKING_OVERLAY_ID)[keyof typeof BLOCKING_OVERLAY_ID];

/**
 * Priority order, back to front conceptually: index 0 is the id that wins
 * whenever it is active, regardless of what else wants to show.
 *   RebuildPrompt   — the engine-version decision gate; state underneath may
 *                      not even be trustworthy yet.
 *   DeclineScreen   — hard game-over; nothing about the running city matters
 *                      once this fires.
 *   InsolvencyPopup — one-shot bailout-entry notice.
 *   ForcedAssetSales— the ongoing bailout-year side panel; lowest of the
 *                      four because it is not a full-screen backdrop and the
 *                      player can keep it open for the whole bailout year.
 */
export const BLOCKING_OVERLAY_PRIORITY: readonly BlockingOverlayId[] = [
  BLOCKING_OVERLAY_ID.REBUILD_PROMPT,
  BLOCKING_OVERLAY_ID.DECLINE_SCREEN,
  BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP,
  BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES,
];

/**
 * Numeric priority for each known id, DERIVED from its position in
 * BLOCKING_OVERLAY_PRIORITY (never hand-duplicated — GR#3) — pass
 * `BLOCKING_OVERLAY_RANK.declineScreen` etc. into `useBlockingOverlay` rather
 * than a magic number.
 */
export const BLOCKING_OVERLAY_RANK: Readonly<Record<BlockingOverlayId, number>> = Object.fromEntries(
  BLOCKING_OVERLAY_PRIORITY.map((id, idx) => [id, idx]),
) as Record<BlockingOverlayId, number>;

/**
 * Pure priority resolver (GR#21: deterministic, no iteration-order
 * dependence — driven entirely by BLOCKING_OVERLAY_PRIORITY's fixed array
 * order, never by object/Map key insertion order). Given a map of which
 * candidate ids currently WANT to render as a blocking overlay, returns the
 * single id that is allowed to — or null if none want to.
 *
 * Any id not present in BLOCKING_OVERLAY_PRIORITY is ranked below every
 * known id (so a newly-registered, not-yet-triaged overlay never
 * accidentally outranks the hard-stop screens), tie-broken by id string so
 * the result is still deterministic.
 */
export function resolveBlockingOverlay(
  active: Readonly<Record<string, boolean>>,
): BlockingOverlayId | string | null {
  const wanting = Object.keys(active).filter((id) => active[id]);
  if (wanting.length === 0) return null;
  const rank = (id: string): number => {
    const idx = BLOCKING_OVERLAY_PRIORITY.indexOf(id as BlockingOverlayId);
    return idx === -1 ? BLOCKING_OVERLAY_PRIORITY.length : idx;
  };
  wanting.sort((a, b) => {
    const ra = rank(a);
    const rb = rank(b);
    if (ra !== rb) return ra - rb;
    return a < b ? -1 : a > b ? 1 : 0;
  });
  return wanting[0];
}
