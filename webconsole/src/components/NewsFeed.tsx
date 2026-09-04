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

import { useRef, useState } from 'react';
import { useSim } from '../sim/simContext';
import {
  createNewsFeedSeq,
  createNewsFeedTracker,
  observeNews,
  type NewsEntry,
} from '../sim/newsFeed';

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
  } | null>(null);
  const sourcesChanged =
    lastObservedRef.current === null ||
    lastObservedRef.current.notice !== state.notice ||
    lastObservedRef.current.milestoneNotice !== state.milestoneNotice ||
    lastObservedRef.current.placeNotice !== state.placeNotice;
  if (sourcesChanged) {
    lastObservedRef.current = {
      notice: state.notice,
      milestoneNotice: state.milestoneNotice,
      placeNotice: state.placeNotice,
    };
    const nextRing = observeNews(
      {
        notice: state.notice,
        milestoneNotice: state.milestoneNotice,
        placeNotice: state.placeNotice,
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
