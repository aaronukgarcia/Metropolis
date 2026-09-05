// newsFeed.ts — FEAT-2326609784: the scrolling news feed that replaces the
// layered top-centre popup stack (Aaron, 2026-09-04 verbatim: "layered pop
// ups need to be got rid of in stead build a better info news list that
// scrolls").
//
// STATE DISCIPLINE (deliberate, matches the brief): this module is PURE and
// touches NO SimState. It OBSERVES three existing transient notice fields —
// state.notice (LevelUpNotice), state.milestoneNotice (MilestoneNotice) and
// state.placeNotice (string, also covers the "Fix All: built N of M..."
// summary and the BUG-396 cannot-afford message) — and turns each activation
// into a NewsEntry for a render-side ring. It never mutates SimState, is
// never journaled, is never replayed, and carries no determinism surface:
// two callers observing the identical sequence of notice values may end up
// with different NewsEntry.id counters (seq is caller-owned, not derived
// from tick) and that is fine — the feed is ephemeral UI history, not sim
// truth. This is why it lives outside engine.ts/types.ts entirely (the inc3
// lane owns those files right now — see the FEAT-2326609784 brief).
//
// DEDUPE CONTRACT: observeNews appends AT MOST ONE entry per source per
// ACTIVATION — a source field transitioning from null (or a different value)
// into a new non-null value. Re-observing the SAME still-active value (e.g.
// across a React re-render, or React 18 StrictMode's double effect
// invocation) is a no-op, because the "last-active" value is remembered in
// `tracker` and mutated in place by the caller across calls. If the field
// later clears (goes back to null) and then re-activates with the exact same
// content, that IS treated as a new event (tracker resets its per-source
// slot to null on every observed null), matching "notice change", not
// "notice present".

import { gameDate, fmtMoney } from './utils.ts';

export type NewsSeverity = 'info' | 'success' | 'warning' | 'error';

export type NewsSource = 'levelup' | 'milestone' | 'placeNotice' | 'consolidatorCapacityUnknown';

export interface NewsEntry {
  /** Stable within one feed instance: `${source}-${seq}`. Not global/shared. */
  id: string;
  /** Sim tick the entry was observed at (NOT wall-clock — GR#21/determinism-adjacent hygiene). */
  tick: number;
  /** Game-calendar label via the SAME formatter the HUD uses (sim/utils.gameDate). */
  dateLabel: string;
  severity: NewsSeverity;
  text: string;
  source: NewsSource;
}

/** Ring bound (task point 1: "feed ring bounded e.g. last 100"). */
export const NEWS_FEED_MAX_ENTRIES = 100;

/** Minimal shape of the notice fields this module observes — deliberately NOT `SimState`
 *  itself so this file has zero import-time coupling to types.ts beyond structural shape. */
export interface NewsFeedSources {
  notice: { level: number; cash: number; unlocked: string[] } | null | undefined;
  milestoneNotice: { id: string; label: string; cash: number } | null | undefined;
  placeNotice: string | null | undefined;
  /**
   * BUG-742 round P3 (GR#1 "aggressive error trapping" without engine.ts
   * calling backend.recordError from inside the reducer — the BUG-602
   * shape: Date.now/JSON.stringify/localStorage during a monthly pass or
   * journal replay). The consolidator's density-apply gates already record
   * every skip into `consolidatorLog[0].skipped` as plain, journalled
   * SimState (engine.ts) — no new state field needed. This observer just
   * WATCHES that existing field for a 'capacity unknown' skip (the
   * fail-closed reason recorded when a corrupt capacityTier makes a
   * group's — or its family's citywide — capacity unrepresentable as a
   * finite number) and is the render-side place that both surfaces it on
   * the news feed AND records the registry error (MET-V866), exactly
   * mirroring how this file already turns `notice`/`milestoneNotice`/
   * `placeNotice` transitions into feed entries — an OUTBOX pattern, not a
   * reducer-side side effect.
   */
  consolidatorLatestPass?: { id: number; skipped: ReadonlyArray<{ sectionKey: number; reason: string }> } | null;
  tick: number;
}

/** Per-source "currently active value" memory. Caller owns one instance for the feed's lifetime. */
export interface NewsFeedTracker {
  levelup: number | null;
  milestone: string | null;
  placeNotice: string | null;
  /**
   * BUG-742 round-2 finding: a bare "last-seen pass id" dedupe key has TWO
   * distinct failure modes once Undo can pop consolidatorLog entries:
   *   (a) ID REUSE — the player undoes the pass that earned this id; the
   *       NEXT real pass re-derives its own id from whatever now sits at
   *       log[0] and can legitimately land on the SAME NUMBER again for a
   *       genuinely NEW skip. A bare `lastSeenId !== pass.id` check would
   *       wrongly treat this as "already seen" and SWALLOW a real notice.
   *   (b) STALE RE-SURFACING — undo pops the newest entries off the front;
   *       an OLDER entry (already long past, its own notice — if any —
   *       already fired ages ago) becomes log[0] again. A bare
   *       `lastSeenId !== pass.id` check would treat this never-before-seen
   *       id as brand new and wrongly RE-FIRE a stale notice.
   * Fixed with two pieces of memory: `key` (pass.id + the CURRENT
   * observation tick — StrictMode-safe re-render dedupe, handles (a)
   * because a genuine re-use always happens at a LATER tick than the
   * original) and `maxNotifiedId` (a monotonic high-water mark — handles
   * (b) because an id below the mark can only be an OLDER entry
   * resurfacing, never a new one: ids only ever increase going forward).
   */
  consolidatorCapacityUnknownKey: string | null;
  consolidatorCapacityUnknownMaxId: number;
}

export function createNewsFeedTracker(): NewsFeedTracker {
  return { levelup: null, milestone: null, placeNotice: null, consolidatorCapacityUnknownKey: null, consolidatorCapacityUnknownMaxId: -Infinity };
}

/** Caller-owned monotonic counter so NewsEntry.id is stable and collision-free
 *  within one feed instance without this module holding any mutable module-level state. */
export interface NewsFeedSeq {
  next: number;
}

export function createNewsFeedSeq(): NewsFeedSeq {
  return { next: 0 };
}

/**
 * `observeNews`'s 4th parameter accepts EITHER the classic mutable counter
 * object above OR a plain `() => number` "monotonic per-mount sequence"
 * callback (round-2: an equally-valid alternative the coordinator floated,
 * and the shape opus-reround2-bug742's own attack file drives it with).
 * Both produce a strictly-increasing number per call; NewsEntry.id only
 * ever needs uniqueness, never a specific numbering scheme.
 */
export type NewsFeedSeqSource = NewsFeedSeq | (() => number);

function nextSeqValue(seq: NewsFeedSeqSource): number {
  return typeof seq === 'function' ? seq() : seq.next++;
}

// placeNotice is one free-text string field covering several distinct
// upstream events (BUG-396 cannot-afford, road/rail auto-connect failure
// text funnelled through the same field by callers, and the "Fix All: built
// X of Y..." / "Fix All: nothing built..." summaries — engine.ts:5219-5229).
// Severity is derived from the text itself since there is no separate
// upstream enum; every branch here is data-derived (GR#15), never a guess.
export function placeNoticeSeverity(text: string): NewsSeverity {
  if (/insufficient|nothing built|no road access|no rail route|locked/i.test(text)) return 'warning';
  if (/^fix all: built/i.test(text)) return 'success';
  return 'info';
}

/**
 * Observe one SimState-shaped snapshot, mutate `tracker` in place, and
 * return a NEW ring array (newest-first, bounded to NEWS_FEED_MAX_ENTRIES).
 * Never mutates the input `ring` — when nothing new is observed the SAME
 * array reference is returned so a React setState(prev => observeNews(...))
 * caller gets an identity-stable bailout (no redundant re-render).
 */
export function observeNews(
  sources: NewsFeedSources,
  tracker: NewsFeedTracker,
  ring: NewsEntry[],
  seq: NewsFeedSeqSource
): NewsEntry[] {
  let out = ring;
  const push = (source: NewsSource, severity: NewsSeverity, text: string) => {
    const entry: NewsEntry = {
      id: `${source}-${nextSeqValue(seq)}`,
      tick: sources.tick,
      dateLabel: gameDate(sources.tick),
      severity,
      text,
      source,
    };
    out = [entry, ...out].slice(0, NEWS_FEED_MAX_ENTRIES);
  };

  const n = sources.notice;
  if (n == null) {
    tracker.levelup = null;
  } else if (tracker.levelup !== n.level) {
    tracker.levelup = n.level;
    const cashText = n.cash > 0 ? ` Cash injection ${fmtMoney(n.cash)} granted.` : '';
    const unlockText = n.unlocked.length > 0 ? ` Unlocked: ${n.unlocked.join(', ')}.` : '';
    push('levelup', n.cash > 0 ? 'success' : 'info', `Level ${n.level} reached.${cashText}${unlockText}`);
  }

  const m = sources.milestoneNotice;
  if (m == null) {
    tracker.milestone = null;
  } else if (tracker.milestone !== m.id) {
    tracker.milestone = m.id;
    const cashText = m.cash > 0 ? ` — ${fmtMoney(m.cash)} awarded.` : '';
    push('milestone', 'success', `Milestone reached: ${m.label}${cashText}`);
  }

  const p = sources.placeNotice;
  if (p == null) {
    tracker.placeNotice = null;
  } else if (tracker.placeNotice !== p) {
    tracker.placeNotice = p;
    push('placeNotice', placeNoticeSeverity(p), p);
  }

  // BUG-742 round P3/round-2: the OUTBOX drain for the consolidator's
  // fail-closed 'capacity unknown' skip — see NewsFeedSources.
  // consolidatorLatestPass's own doc comment for why this lives here
  // instead of engine.ts calling backend.recordError from inside the
  // reducer. The registry error itself is NOT recorded in this function —
  // round-2 finding (4): recordError is a side effect (localStorage ring
  // write) and this function runs during RENDER (NewsFeed.tsx's documented
  // render-phase derivation); the caller fires it from a useEffect instead,
  // keyed off the pushed NewsEntry's own id, so it survives React 18
  // StrictMode's double-render without double-recording. See
  // NewsFeedTracker.consolidatorCapacityUnknownKey's doc comment for the
  // (id, tick) + high-water-mark dedupe this needs (round-2 finding (2)).
  const pass = sources.consolidatorLatestPass;
  const capacityUnknownSections = pass?.skipped.filter((k) => k.reason === 'capacity unknown') ?? [];
  if (pass && capacityUnknownSections.length > 0) {
    const key = `${pass.id}:${sources.tick}`;
    const isSameObservation = tracker.consolidatorCapacityUnknownKey === key;
    const isStaleResurface = pass.id < tracker.consolidatorCapacityUnknownMaxId;
    if (!isSameObservation && !isStaleResurface) {
      tracker.consolidatorCapacityUnknownKey = key;
      tracker.consolidatorCapacityUnknownMaxId = Math.max(tracker.consolidatorCapacityUnknownMaxId, pass.id);
      const sectionList = capacityUnknownSections.map((k) => k.sectionKey).join(', ');
      const text =
        capacityUnknownSections.length === 1
          ? `Consolidator skipped section ${sectionList}: capacity unknown (a corrupt capacityTier made it unrepresentable) — nothing was merged or lost.`
          : `Consolidator skipped ${capacityUnknownSections.length} sections (${sectionList}): capacity unknown — nothing was merged or lost.`;
      push('consolidatorCapacityUnknown', 'warning', text);
    }
  }

  return out;
}
