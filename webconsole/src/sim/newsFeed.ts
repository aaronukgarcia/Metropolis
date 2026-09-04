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

export type NewsSource = 'levelup' | 'milestone' | 'placeNotice';

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
  tick: number;
}

/** Per-source "currently active value" memory. Caller owns one instance for the feed's lifetime. */
export interface NewsFeedTracker {
  levelup: number | null;
  milestone: string | null;
  placeNotice: string | null;
}

export function createNewsFeedTracker(): NewsFeedTracker {
  return { levelup: null, milestone: null, placeNotice: null };
}

/** Caller-owned monotonic counter so NewsEntry.id is stable and collision-free
 *  within one feed instance without this module holding any mutable module-level state. */
export interface NewsFeedSeq {
  next: number;
}

export function createNewsFeedSeq(): NewsFeedSeq {
  return { next: 0 };
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
  seq: NewsFeedSeq
): NewsEntry[] {
  let out = ring;
  const push = (source: NewsSource, severity: NewsSeverity, text: string) => {
    const entry: NewsEntry = {
      id: `${source}-${seq.next++}`,
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

  return out;
}
