// bug-684-newsfeed.test.mjs — BUG-684 (P1) "the wait is recorded once per
// group": the density-merge apply lane's new 'funds floor' skip reason
// (engine.ts's applyConsolidatorPass) is drained into the news feed AND a
// registry error (MET-V895) by newsFeed.ts's OUTBOX pattern — the SAME
// dedupe shape BUG-742's own 'capacity unknown' skip already uses (see
// attack-bug742-r3.test.mjs's "R3 — newsFeed dedupe re-verify" describe,
// reused here almost verbatim for the new source/reason pair).
//
// Scope: this file tests newsFeed.ts's pure `observeNews` directly (no
// React/DOM) — the actual recordError(MET-V895) call site lives in
// NewsFeed.tsx's useEffect (a thin, already-covered dispatch keyed off
// entry.source, see attack-bug742-newsfeed-effect.test.tsx for that
// component-level half); this file's job is proving the OUTBOX itself never
// duplicates and never drops the 'funds floor' skip.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { observeNews, createNewsFeedTracker, createNewsFeedSeq } from '../src/sim/newsFeed.ts';

const pass = (id, sections = [7]) => ({
  id,
  skipped: sections.map((sectionKey) => ({ sectionKey, reason: 'funds floor' })),
});
const src = (p, tick) => ({ notice: null, milestoneNotice: null, placeNotice: null, consolidatorLatestPass: p, tick });
const fundsFloorEntries = (ring) => ring.filter((e) => e.source === 'consolidatorFundsFloor');

describe('BUG-684: consolidatorFundsFloor outbox dedupe (mirrors capacity-unknown\'s own proven shape)', () => {
  test('a single refused pass fires exactly ONE entry, once — not per section', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    const r = observeNews(src(pass(1, [3, 7, 9]), 30), t, [], s);
    assert.equal(fundsFloorEntries(r).length, 1, 'one entry, listing all three refused sections in its text');
    assert.match(fundsFloorEntries(r)[0].text, /3 sections/);
    assert.match(fundsFloorEntries(r)[0].text, /3, 7, 9/);
    assert.equal(fundsFloorEntries(r)[0].severity, 'warning', "a wait is informational, not an error — nothing was merged OR lost");
  });

  test('re-observing the SAME pass id at the SAME tick (React re-render) does not duplicate', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    let r = observeNews(src(pass(5), 60), t, [], s);
    r = observeNews(src(pass(5), 60), t, r, s);
    assert.equal(fundsFloorEntries(r).length, 1);
  });

  test('the SAME pass id resurfacing at a LATER tick (still stuck waiting next month) fires again — a genuinely new observation', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    let r = observeNews(src(pass(11), 100), t, [], s);
    r = observeNews(src(pass(11), 130), t, r, s); // same pass id, later tick: a real re-check, not a re-render
    assert.equal(fundsFloorEntries(r).length, 2, 'id reuse at a later tick still fires (mirrors capacity-unknown R3a)');
  });

  test('an OLDER pass id resurfacing after Undo pops the ring is suppressed (high-water mark)', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    let r = observeNews(src(pass(20), 100), t, [], s);
    r = observeNews(src(pass(9), 300), t, r, s); // 9 < the high-water mark 20: a stale entry resurfacing, not new
    assert.equal(fundsFloorEntries(r).length, 1, 'stale resurface suppressed (mirrors capacity-unknown R3c)');
  });

  test('a pass with BOTH capacity-unknown and funds-floor skips fires ONE entry per source, independently deduped', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    const mixedPass = {
      id: 1,
      skipped: [
        { sectionKey: 3, reason: 'capacity unknown' },
        { sectionKey: 7, reason: 'funds floor' },
      ],
    };
    const r = observeNews(src(mixedPass, 30), t, [], s);
    assert.equal(r.filter((e) => e.source === 'consolidatorCapacityUnknown').length, 1);
    assert.equal(fundsFloorEntries(r).length, 1);
  });

  test('a pass with no funds-floor skip (e.g. only "action budget" or "one per city") never fires this source', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    const otherSkips = { id: 1, skipped: [{ sectionKey: 3, reason: 'action budget' }, { sectionKey: 4, reason: 'one per city' }] };
    const r = observeNews(src(otherSkips, 30), t, [], s);
    assert.equal(fundsFloorEntries(r).length, 0);
  });

  test('no consolidatorLatestPass source at all is a safe no-op', () => {
    const t = createNewsFeedTracker();
    const s = createNewsFeedSeq();
    const r = observeNews({ notice: null, milestoneNotice: null, placeNotice: null, consolidatorLatestPass: null, tick: 1 }, t, [], s);
    assert.equal(fundsFloorEntries(r).length, 0);
  });
});
