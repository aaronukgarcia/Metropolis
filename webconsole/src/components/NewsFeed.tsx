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
import { financeStatusTracker, type FinanceStatusSnapshot } from '../sim/financeStatusTracker';

const SEVERITY_LABEL: Record<NewsEntry['severity'], string> = {
  info: 'Info',
  success: 'Good news',
  warning: 'Warning',
  error: 'Error',
};

export function NewsFeed() {
  const { state } = useSim();

  // BUG-756 ROOT CAUSE (confirmed): the ring used to be derived ENTIRELY
  // during render — a plain ref (`lastObservedRef`) recorded "have I already
  // observed this exact source snapshot" and, when it hadn't, the component
  // body mutated `trackerRef`/`seqRef` and called `setRing` directly, all
  // inside the render function. An independent round mounted this component
  // with a REAL `createRoot` + `<React.StrictMode>` + `act` harness and
  // measured DOM ENTRIES: [] for every source (levelup/milestone/
  // placeNotice/consolidatorCapacityUnknown alike) even though the tracker
  // ended up correctly marked as "observed" — i.e. the render-phase
  // bookkeeping ran, but the entry never reached committed state. The same
  // harness with StrictMode OFF rendered correctly. The only prior
  // "StrictMode proof" on file (attack-news-feed-round.test.tsx) was a
  // hand-simulated model whose own comment admitted SSR does not actually
  // double-invoke — it never exercised React's real dev-mode double
  // invocation of render/state-initializers, so it never could have caught
  // this. React's own guidance is explicit: don't write refs or call
  // setState during render except via the sanctioned "adjusting state when
  // a prop changes" / lazy-initialization patterns — and even those must be
  // pure functions of the CURRENT inputs, never something that accumulates
  // across an unpredictable number of invocations.
  //
  // FIX — two pieces:
  //   1. First paint (covers SSR, which never runs effects, AND the very
  //      first client render) uses `useState`'s LAZY INITIALIZER — a
  //      **pure** function of the current notice/milestone/placeNotice/
  //      consolidator/payrollShortfall snapshot: every call starts from a
  //      brand-new tracker+seq+[] and derives the ring from scratch, so it
  //      does not matter whether React invokes it once or twice (React 18
  //      StrictMode calls state initializers twice in dev specifically to
  //      catch impurity) — any invocation's {tracker, ring} pair is
  //      internally consistent and any one of them landing in committed
  //      state is correct. Ref writes inside a lazy initializer are React's
  //      documented exception to "never write refs during render".
  //   2. Every SUBSEQUENT activation (the player levels up, a milestone
  //      fires, a payroll shortfall starts/clears, etc., while the feed is
  //      already mounted) is derived in a `useEffect`, keyed on the exact
  //      source values (React's dependency-array Object.is comparison
  //      replaces the old manual `sourcesChanged` check). React 18
  //      StrictMode's dev-only extra effect cycle calls this effect body
  //      TWICE for one commit; `lastEffectSourcesRef` (below) skips the
  //      second invocation's redundant re-check as a cheap early-out, but it
  //      is NOT what makes the fix correct on its own — see the LOAD-BEARING
  //      note beside the actual `setRing` call below for why. The genuinely
  //      load-bearing piece is that `setRing` is called with a CONCRETE
  //      value, never the functional `setRing(prev => ...)` updater form:
  //      React 18 StrictMode separately double-invokes a FUNCTION passed to
  //      setState's updater form (to catch impure updaters), and
  //      `observeNews` is impure with respect to `prev` because it mutates
  //      the tracker as a side effect — an EARLIER version of this fix
  //      proved, via its own red-proof test, that the purity-checking
  //      second invocation of such an updater sees "already observed" and
  //      returns `prev` unchanged, and React commits THAT result, silently
  //      discarding the real push, even with `lastEffectSourcesRef`-style
  //      gating still in place around the effect body.
  // Net effect: exactly one entry per genuine activation, on first paint AND
  // on every later one, with or without StrictMode, with or without SSR.
  function currentSources() {
    return {
      notice: state.notice,
      milestoneNotice: state.milestoneNotice,
      placeNotice: state.placeNotice,
      consolidatorLatestPass,
      tick: state.tick,
      payrollShortfall: financeStatus,
    };
  }

  // BUG-723 round finding F1: the live-Go-engine payroll-shortfall status,
  // read from the SAME side-channel seam LiveEngineBadge.tsx already uses
  // (financeStatusTracker.ts, mirroring queueDepth.ts's established
  // subscribe-to-a-module-level-singleton pattern). Unlike
  // notice/milestoneNotice/placeNotice, this genuinely CANNOT be derived
  // during render — it arrives asynchronously over a WebSocket, is null on
  // the very first render (including SSR) regardless of what the live
  // engine is doing, and there is no local mock-sim equivalent to read
  // synchronously — so a useEffect subscription feeding real React state is
  // the correct (and only) way to observe it, unlike the
  // render-phase-derivable SimState fields this component otherwise reads.
  const [financeStatus, setFinanceStatus] = useState<FinanceStatusSnapshot>(() => financeStatusTracker.snapshot());
  useEffect(() => financeStatusTracker.subscribe(setFinanceStatus), []);

  const trackerRef = useRef(createNewsFeedTracker());
  const seqRef = useRef(createNewsFeedSeq());
  const lastLineageIdRef = useRef<string | undefined>(state.lineageId);
  // Cheap early-out for React 18 StrictMode's dev-only duplicate invocation
  // of this effect body: a plain "have I already processed this exact
  // source snapshot" ref, checked and set BEFORE any tracker mutation or
  // setRing call. NOT the load-bearing fix by itself (see the comment atop
  // this component) — it exists so the duplicate invocation does no
  // redundant work, but correctness against StrictMode's updater-purity
  // double-invocation comes from `setRing` never being called with the
  // functional form (see the inline comment beside that call).
  const lastEffectSourcesRef = useRef<{
    lineageId: string | undefined;
    notice: unknown;
    milestoneNotice: unknown;
    placeNotice: unknown;
    consolidatorLatestPass: unknown;
    payrollShortfall: unknown;
  } | null>(null);
  // BUG-742 round P3: `state.consolidatorLog[0]` — the newest pass, which
  // may be a 'capacity unknown' skip-only entry — is journalled, plain
  // SimState (engine.ts never re-references consolidatorLog on a tick that
  // logged nothing), so its object identity is stable exactly like
  // notice/milestoneNotice/placeNotice above: it only changes reference
  // when a NEW pass is actually appended.
  const consolidatorLatestPass = state.consolidatorLog?.[0] ?? null;

  const [ring, setRing] = useState<NewsEntry[]>(() => {
    const tracker = createNewsFeedTracker();
    const seq = createNewsFeedSeq();
    const initialRing = observeNews(currentSources(), tracker, [], seq);
    trackerRef.current = tracker;
    seqRef.current = seq;
    return initialRing;
  });
  const [expanded, setExpanded] = useState(false);
  // How many of the CURRENT ring's entries have been seen (from the front —
  // ring is newest-first) — everything beyond this index counts as unread.
  const [seenCount, setSeenCount] = useState(0);

  useEffect(() => {
    // BUG-742 re-verify (opus-reverify-bug742, R3d): NewsFeed never
    // remounts across Load / New Game (it's a persistent HUD element), so
    // trackerRef's consolidatorCapacityUnknownMaxId high-water mark used to
    // survive a city switch too. `state.lineageId` (types.ts) is the opaque
    // per-city identity minted once at every genesis — reset the tracker
    // the instant the observed lineage differs from the last one seen, so a
    // fresh city always starts this dedupe state from scratch. `seqRef`
    // (NewsEntry.id generation) and `recordedIdsRef` (the MET-V866 effect's
    // dedupe, below) are deliberately NOT reset here: they only need
    // session-wide uniqueness, never reset by design, and resetting them
    // could let a new lineage mint an id that COLLIDES with one still
    // sitting in the visible `ring` from the old lineage (this component
    // does not clear the ring on a lineage change).
    const signature = {
      lineageId: state.lineageId,
      notice: state.notice,
      milestoneNotice: state.milestoneNotice,
      placeNotice: state.placeNotice,
      consolidatorLatestPass,
      payrollShortfall: financeStatus,
    };
    const last = lastEffectSourcesRef.current;
    const alreadyProcessed =
      last !== null &&
      last.lineageId === signature.lineageId &&
      last.notice === signature.notice &&
      last.milestoneNotice === signature.milestoneNotice &&
      last.placeNotice === signature.placeNotice &&
      last.consolidatorLatestPass === signature.consolidatorLatestPass &&
      last.payrollShortfall === signature.payrollShortfall;
    if (alreadyProcessed) return; // StrictMode's duplicate invocation of this SAME effect body — no-op, by design.
    lastEffectSourcesRef.current = signature;

    if (lastLineageIdRef.current !== state.lineageId) {
      lastLineageIdRef.current = state.lineageId;
      trackerRef.current = createNewsFeedTracker();
    }
    const sources = currentSources();
    // LOAD-BEARING: deliberately NOT the functional `setRing(prev => ...)`
    // form. React 18 StrictMode double-invokes a FUNCTION passed to
    // setState's updater form too (a SEPARATE mechanism from
    // double-invoking render/effects, to catch impure updaters) — and
    // `observeNews` mutates `trackerRef`/`seqRef` as a side effect, so it is
    // NOT pure with respect to its `prev` argument. Confirmed empirically
    // against a real createRoot+StrictMode+act mount: the updater ran twice
    // with the SAME `prev`, and React kept the SECOND invocation's result —
    // by then the tracker was already mutated by the first call, so the
    // second call saw "already observed" and returned `prev` UNCHANGED,
    // silently discarding the real push (committed ring stayed empty) EVEN
    // THOUGH `lastEffectSourcesRef` above had already gated the effect BODY
    // down to a single real invocation — the attacker neutered that ref
    // (made it a permanent no-op) and nothing about the failure mode
    // changed, which is what proves the ref is not the fix. `ring` (the
    // plain, closed-over component state, read exactly once here) is safe
    // to use directly because `alreadyProcessed` above already guarantees
    // this effect's real body runs at most once per genuine source change —
    // no second call, so no double-invoked-updater surface at all.
    const nextRing = observeNews(sources, trackerRef.current, ring, seqRef.current);
    if (nextRing !== ring) setRing(nextRing);
    // Deliberately NOT keying on `state.tick` — that would re-run this
    // effect (and the observeNews bailout check) every single tick for no
    // behavioural change; the fields below are the only ones that gate a
    // push, and `sources.tick` is only used to LABEL an entry actually
    // pushed on this same call, so reading the live `state.tick` here is
    // still exactly the tick a genuine activation happened on. This ALSO
    // runs once, harmlessly, right after the initial mount (with the SAME
    // sources the lazy initializer already consumed) — trackerRef already
    // marks them observed, so it's a guaranteed no-op that round-trips
    // through the identity bailout above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.lineageId, state.notice, state.milestoneNotice, state.placeNotice, consolidatorLatestPass, financeStatus]);

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
