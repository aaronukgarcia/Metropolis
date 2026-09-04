// news-feed.test.mjs — FEAT-2326609784: pure-function tests for
// sim/newsFeed.ts, the observer that turns state.notice /
// state.milestoneNotice / state.placeNotice activations into scrolling
// NewsFeed entries. These tests exercise observeNews() directly (no React,
// no DOM) so the dedupe/ordering/bound/severity contract is provable without
// a renderer.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createNewsFeedTracker,
  createNewsFeedSeq,
  observeNews,
  placeNoticeSeverity,
  NEWS_FEED_MAX_ENTRIES,
} from '../src/sim/newsFeed.ts';
import { gameDate } from '../src/sim/utils.ts';

function emptySources(tick = 0) {
  return { notice: null, milestoneNotice: null, placeNotice: null, tick };
}

test('observeNews appends nothing when every source is null', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const ring = observeNews(emptySources(5), tracker, [], seq);
  assert.deepEqual(ring, [], 'no notices active -> empty ring');
});

test('observeNews appends exactly ONE entry when a level-up notice activates', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const sources = { ...emptySources(12), notice: { level: 2, cash: 5000, unlocked: ['Penthouse Tower'] } };
  const ring = observeNews(sources, tracker, [], seq);
  assert.equal(ring.length, 1, 'exactly one entry for the activation');
  assert.equal(ring[0].source, 'levelup');
  assert.equal(ring[0].tick, 12);
  assert.equal(ring[0].dateLabel, gameDate(12), 'must stamp the SAME game-date formatter the HUD uses');
  assert.match(ring[0].text, /Level 2 reached/);
  assert.match(ring[0].text, /Penthouse Tower/);
  assert.equal(ring[0].severity, 'success', 'a cash>0 level-up reads as good news');
});

// RED-PROOF (dedupe across re-renders / React 18 StrictMode double effect
// invoke): observing the IDENTICAL still-active level-up notice a second
// time — same level, same object shape but a DIFFERENT object reference
// (simulating a fresh render passing a structurally-equal-but-not-===
// value) — must NOT append a second entry. Before this dedupe existed (a
// naive "append whenever notice is non-null" observer), this assertion
// fails because the ring gains a duplicate second entry.
test('RED-PROOF: re-observing the SAME active level (different object reference) does not duplicate', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const notice1 = { level: 3, cash: 1000, unlocked: [] };
  const notice2 = { level: 3, cash: 1000, unlocked: [] }; // structurally equal, different reference
  let ring = observeNews({ ...emptySources(1), notice: notice1 }, tracker, [], seq);
  ring = observeNews({ ...emptySources(1), notice: notice2 }, tracker, ring, seq);
  ring = observeNews({ ...emptySources(1), notice: notice2 }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'the same active level must never produce more than one feed entry');
});

test('a DIFFERENT level after the first clears to null DOES append a new entry (real activation, not a re-render)', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews({ ...emptySources(1), notice: { level: 1, cash: 0, unlocked: [] } }, tracker, [], seq);
  ring = observeNews(emptySources(2), tracker, ring, seq); // dismissed -> null
  ring = observeNews({ ...emptySources(3), notice: { level: 2, cash: 0, unlocked: [] } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'two distinct level activations -> two entries');
  assert.equal(ring[0].text.includes('Level 2'), true, 'newest-first ordering');
  assert.equal(ring[1].text.includes('Level 1'), true);
});

test('milestone notices dedupe by id the same way and use success severity', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const m = { id: 'first-100k-pop', label: 'Metropolis', cash: 300000 };
  let ring = observeNews({ ...emptySources(9), milestoneNotice: m }, tracker, [], seq);
  ring = observeNews({ ...emptySources(9), milestoneNotice: { ...m } }, tracker, ring, seq); // re-render, same id
  assert.equal(ring.length, 1, 'same milestone id must not duplicate across re-renders');
  assert.equal(ring[0].source, 'milestone');
  assert.equal(ring[0].severity, 'success');
  assert.match(ring[0].text, /Milestone reached: Metropolis/);
  assert.match(ring[0].text, /£300,000/);
});

test('placeNotice dedupes by exact string and re-activates on a genuinely new value', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews({ ...emptySources(1), placeNotice: 'Insufficient funds — £500 needed' }, tracker, [], seq);
  ring = observeNews({ ...emptySources(1), placeNotice: 'Insufficient funds — £500 needed' }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'identical placeNotice string across renders must not duplicate');
  ring = observeNews({ ...emptySources(2), placeNotice: 'Fix All: built 3 of 5 — click Fix All again for the rest' }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'a genuinely different placeNotice text must append');
  assert.equal(ring[0].severity, 'success', 'Fix All: built ... reads as success');
  assert.equal(ring[1].severity, 'warning', 'Insufficient funds reads as a warning');
});

test('placeNoticeSeverity classifies the known text families', () => {
  assert.equal(placeNoticeSeverity('Insufficient funds — £1,000 needed'), 'warning');
  assert.equal(placeNoticeSeverity('Fix All: nothing built — insufficient funds'), 'warning');
  assert.equal(placeNoticeSeverity('no road access'), 'warning');
  assert.equal(placeNoticeSeverity('Fix All: built Fire Station, Clinic'), 'success');
  assert.equal(placeNoticeSeverity('some other free-text notice'), 'info');
});

// RED-PROOF (ring bound): pump NEWS_FEED_MAX_ENTRIES + 20 distinct placeNotice
// activations through and prove the ring never exceeds the documented cap
// and always keeps the NEWEST entries (not the oldest).
test('RED-PROOF: the ring is bounded to NEWS_FEED_MAX_ENTRIES, newest survive', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = [];
  const total = NEWS_FEED_MAX_ENTRIES + 20;
  for (let i = 0; i < total; i++) {
    ring = observeNews({ ...emptySources(i), placeNotice: `notice #${i}` }, tracker, ring, seq);
  }
  assert.equal(ring.length, NEWS_FEED_MAX_ENTRIES, `ring must be capped at ${NEWS_FEED_MAX_ENTRIES}`);
  assert.equal(ring[0].text, `notice #${total - 1}`, 'newest entry must be at the front');
  assert.equal(
    ring[ring.length - 1].text,
    `notice #${total - NEWS_FEED_MAX_ENTRIES}`,
    'the oldest surviving entry must be exactly the cap-th most recent, proving old entries were evicted not new ones'
  );
});

test('ordering is always newest-first across mixed sources', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews({ ...emptySources(1), notice: { level: 1, cash: 0, unlocked: [] } }, tracker, [], seq);
  ring = observeNews({ ...emptySources(2), milestoneNotice: { id: 'm1', label: 'Milestone One', cash: 0 } }, tracker, ring, seq);
  ring = observeNews({ ...emptySources(3), placeNotice: 'Insufficient funds' }, tracker, ring, seq);
  assert.equal(ring.length, 3);
  assert.equal(ring[0].source, 'placeNotice');
  assert.equal(ring[1].source, 'milestone');
  assert.equal(ring[2].source, 'levelup');
});

// RED-PROOF: identity-stable bailout. When nothing changes, observeNews must
// return the SAME array reference so a React setState(prev => observeNews(...))
// caller does not trigger a redundant re-render every tick.
test('RED-PROOF: observeNews returns the SAME ring reference when nothing new is observed', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews({ ...emptySources(1), placeNotice: 'hello' }, tracker, [], seq);
  const before = ring;
  const after = observeNews({ ...emptySources(2), placeNotice: 'hello' }, tracker, before, seq);
  assert.equal(after, before, 'unchanged sources must return the identical ring array reference');
});
