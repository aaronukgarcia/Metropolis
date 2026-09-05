// NewsFeed.tsx — FEAT-2326609784: replaces the layered top-centre popup
// stack (LevelUpBanner + MilestoneBanner + PlaceNoticeBanner, which included
// the "Fix All: built N of M... click Fix All again for the rest" summary)
// with ONE scrolling news feed. Aaron, 2026-09-04, verbatim: "layered pop ups
// need to be got rid of in stead build a better info news list that
// scrolls."
//
// STATE DISCIPLINE: the ring lives ENTIRELY in this component's React state
// (useState/useRef) — it is never written to SimState, never journaled,
// never replayed, and has no determinism surface (sim/newsFeed.ts is a pure
// observer of state.notice / state.milestoneNotice / state.placeNotice; see
// that file's header for the full contract). This keeps the feed out of the
// inc3 lane's current ownership of engine.ts/types.ts entirely.
//
// The absorbed sources keep their EXISTING clearing semantics untouched —
// this component only READS them, it never dispatches dismissNotice /
// dismissMilestoneNotice / dismissPlaceNotice. Those actions still exist and
// still fire from elsewhere in the reducer lifecycle (e.g. placeNotice is
// overwritten by the next `place`); the feed just also records history, it
// does not gate or replace the underlying clear.
//
// NOT absorbed (true modals / ambient status, left exactly as they were):
//   InsolvencyPopup, DeclineScreen, RebuildPrompt, AffordabilityConfirm —
//   decisions, not news. InsolvencyBanner/AdministrationBanner/
//   SecondBailoutBanner — persistent ambient status (pointer-events:none,
//   never dismissed), not a transient toast. The "Placing: X" tool chip and
//   the "Needs a clear NxN area" hover hint — tied to the live cursor, not a
//   stacked notice.

import { useEffect, useRef, useState } from 'react';
import { useSim } from '../sim/simContext';
import {
  createNewsFeedSeq,
  createNewsFeedTracker,
  observeNews,
  type NewsEntry,
} from '../sim/newsFeed';
import { recordError } from '../sim/backend';

const SEVERITY_LABEL: Record<NewsEntry['severity'], string> = {
  info: 'Info',
  success: 'Good news',
  warning: 'Warning',
  error: 'Error',
};

export function NewsFeed() {
  const { state } = useSim();
  const trackerRef = useRef(createNewsFeedTracker());
  const seqRef = useRef(createNewsFeedSeq());
  const [ring, setRing] = useState<NewsEntry[]>([]);
  const [expanded, setExpanded] = useState(false);
  // How many of the CURRENT ring's entries have been seen (from the front —
  // ring is newest-first) — everything beyond this index counts as unread.
  const [seenCount, setSeenCount] = useState(0);

  // BUG-742 re-verify (opus-reverify-bug742, R3d): NewsFeed never remounts
  // across Load / New Game (it's a persistent HUD element), so trackerRef's
  // consolidatorCapacityUnknownMaxId high-water mark used to survive a city
  // switch too. Loading an OLDER save, or starting a New Game, legitimately
  // restarts consolidatorLog pass ids at 1 — every genuine post-load
  // capacity-unknown notice was then silently suppressed as "stale" until
  // ids climbed back past whatever the PREVIOUS city's mark had reached.
  // `state.lineageId` (types.ts) is the opaque per-city identity minted
  // once at every genesis — reset the tracker the instant the observed
  // lineage differs from the last one seen, so a fresh city always starts
  // this dedupe state from scratch. `seqRef` (NewsEntry.id generation) and
  // `recordedIdsRef` (the MET-V866 effect's dedupe, below) are deliberately
  // NOT reset here: they only need session-wide uniqueness, never reset by
  // design, and resetting them could let a new lineage mint an id that
  // COLLIDES with one still sitting in the visible `ring` from the old
  // lineage (this component does not clear the ring on a lineage change).
  // This is a RENDER-PHASE reset (matches the render-phase derivation
  // pattern below, and is StrictMode-safe by the same "second invocation
  // sees no change" argument), not a remount — resetting a ref in place,
  // not re-creating the component.
  const lastLineageIdRef = useRef<string | undefined>(state.lineageId);
  if (lastLineageIdRef.current !== state.lineageId) {
    lastLineageIdRef.current = state.lineageId;
    trackerRef.current = createNewsFeedTracker();
  }

  // Deliberately a RENDER-PHASE state derivation (React's documented
  // "adjusting state when a prop changes" pattern — see "You Might Not Need
  // an Effect"), NOT a useEffect. Two reasons:
  //   1. useEffect never runs under SSR (renderToString) — the very first
  //      paint of an already-active milestone/level-up/placeNotice would
  //      render an empty "No news yet." ticker until hydration, a visible
  //      flash-of-missing-news regression the render-phase form avoids
  //      entirely (the entry is in `ring` on the SAME render that first
  //      sees the source field non-null).
  //   2. lastObservedRef is a plain ref mutated synchronously inside the
  //      component body, so React 18 StrictMode's double render-invocation
  //      is naturally deduped: the first invocation advances
  //      lastObservedRef/trackerRef/seqRef and calls setRing (a
  //      conditional, terminating render-phase update — the pattern React
  //      explicitly supports); the second invocation (identical props) sees
  //      `sourcesChanged` false and does nothing. No two-invocation double
  //      append is possible.
  const lastObservedRef = useRef<{
    notice: unknown;
    milestoneNotice: unknown;
    placeNotice: unknown;
    consolidatorLatestPass: unknown;
  } | null>(null);
  // BUG-742 round P3: `state.consolidatorLog[0]` — the newest pass, which
  // may be a 'capacity unknown' skip-only entry — is journalled, plain
  // SimState (engine.ts never re-references consolidatorLog on a tick that
  // logged nothing), so its object identity is stable exactly like
  // notice/milestoneNotice/placeNotice above: it only changes reference
  // when a NEW pass is actually appended.
  const consolidatorLatestPass = state.consolidatorLog?.[0] ?? null;
  const sourcesChanged =
    lastObservedRef.current === null ||
    lastObservedRef.current.notice !== state.notice ||
    lastObservedRef.current.milestoneNotice !== state.milestoneNotice ||
    lastObservedRef.current.placeNotice !== state.placeNotice ||
    lastObservedRef.current.consolidatorLatestPass !== consolidatorLatestPass;
  if (sourcesChanged) {
    lastObservedRef.current = {
      notice: state.notice,
      milestoneNotice: state.milestoneNotice,
      placeNotice: state.placeNotice,
      consolidatorLatestPass,
    };
    const nextRing = observeNews(
      {
        notice: state.notice,
        milestoneNotice: state.milestoneNotice,
        placeNotice: state.placeNotice,
        consolidatorLatestPass,
        tick: state.tick,
      },
      trackerRef.current,
      ring,
      seqRef.current
    );
    if (nextRing !== ring) {
      setRing(nextRing);
    }
  }

  const unreadCount = Math.max(0, ring.length - seenCount);
  const latest = ring[0] ?? null;

  // BUG-742 round-2 finding (4): backend.recordError does a localStorage
  // ring write — a side effect — and previously fired directly inside
  // observeNews's render-phase call above. React's own guidance is side
  // effects belong in useEffect, not render (render can run more than once
  // per commit, or be thrown away, under Concurrent Mode); newsFeed.ts no
  // longer calls recordError at all (see its own doc comment). This effect
  // is the ONE place that does. Scans the WHOLE ring (bounded to
  // NEWS_FEED_MAX_ENTRIES, cheap) rather than only `ring[0]` — a
  // capacity-unknown entry can be buried under a LATER levelup/milestone/
  // placeNotice push in the same or a later render, so watching only the
  // top entry would silently miss it. `recordedIdsRef` is the dedupe: keyed
  // on each entry's own stable `id` (observeNews's (id,tick)+high-water-mark
  // logic already guarantees at most one push per real event), so this
  // fires exactly once per genuine event even across React 18 StrictMode's
  // documented double-invoke of the same effect body.
  const recordedIdsRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    for (const entry of ring) {
      if (entry.source !== 'consolidatorCapacityUnknown') continue;
      if (recordedIdsRef.current.has(entry.id)) continue;
      recordedIdsRef.current.add(entry.id);
      recordError(entry.text, { type: 'app', action: 'consolidator', code: 'MET-V866' });
    }
  }, [ring]);

  function toggleExpanded() {
    setExpanded((v) => {
      const next = !v;
      if (next) setSeenCount(ring.length); // opening the feed marks everything read
      return next;
    });
  }

  function clearAll() {
    setRing([]);
    setSeenCount(0);
  }

  return (
    <div className="news-feed" role="region" aria-label="News">
      <button
        type="button"
        className={`news-feed-ticker${unreadCount > 0 ? ' has-unread' : ''}`}
        onClick={toggleExpanded}
        aria-expanded={expanded}
      >
        {latest ? (
          <>
            <span className={`news-dot news-${latest.severity}`} aria-hidden="true" />
            <span className="news-feed-ticker-text">
              <span className="news-feed-date mono">{latest.dateLabel}</span> {latest.text}
            </span>
          </>
        ) : (
          <span className="news-feed-ticker-text muted">No news yet.</span>
        )}
        {unreadCount > 0 && <span className="news-feed-badge">{unreadCount}</span>}
      </button>
      {expanded && (
        <div className="news-feed-panel">
          <div className="news-feed-panel-head">
            <b>News</b>
            <div className="news-feed-panel-actions">
              <button className="btn tiny" onClick={clearAll} disabled={ring.length === 0}>
                Clear
              </button>
              <button className="btn tiny" onClick={toggleExpanded} aria-label="Collapse news feed">
                ×
              </button>
            </div>
          </div>
          {ring.length === 0 ? (
            <p className="muted news-feed-empty">No news yet.</p>
          ) : (
            <ul className="news-feed-list">
              {ring.map((entry) => (
                <li key={entry.id} className={`news-feed-entry news-${entry.severity}`}>
                  <span className="news-dot" aria-hidden="true" title={SEVERITY_LABEL[entry.severity]} />
                  <span className="news-feed-date mono">{entry.dateLabel}</span>
                  <span className="news-feed-text">{entry.text}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
