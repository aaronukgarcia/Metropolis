import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, CONSOLIDATOR_LOG_CAP } from '../src/sim/engine.ts';
import { consolidationLadder } from '../src/sim/consolidator.ts';
import { observeNews, createNewsFeedTracker, createNewsFeedSeq } from '../src/sim/newsFeed.ts';

const G = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city').groupSize;
const pass = (id) => ({ id, skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });
const src = (p, tick) => ({ notice: null, milestoneNotice: null, placeNotice: null, consolidatorLatestPass: p, tick });
const cnt = (ring) => ring.filter((e) => e.source === 'consolidatorCapacityUnknown').length;

describe('R3 — newsFeed dedupe re-verify', () => {
  test('R3a: id REUSE at a later tick still fires (old N2c)', () => {
    const t = createNewsFeedTracker(); const s = createNewsFeedSeq();
    let r = observeNews(src(pass(11), 100), t, [], s);
    r = observeNews(src(pass(11), 200), t, r, s);
    console.log('R3a entries:', cnt(r), '(want 2)');
    assert.equal(cnt(r), 2);
  });
  test('R3b: same observation twice (StrictMode) fires once', () => {
    const t = createNewsFeedTracker(); const s = createNewsFeedSeq();
    let r = observeNews(src(pass(6), 50), t, [], s);
    r = observeNews(src(pass(6), 50), t, r, s);
    console.log('R3b entries:', cnt(r), '(want 1)');
    assert.equal(cnt(r), 1);
  });
  test('R3c: stale older entry resurfacing after undo is suppressed (old N2e)', () => {
    const t = createNewsFeedTracker(); const s = createNewsFeedSeq();
    let r = observeNews(src(pass(20), 100), t, [], s);
    r = observeNews(src(pass(9), 300), t, r, s);
    console.log('R3c entries:', cnt(r), '(want 1)');
    assert.equal(cnt(r), 1);
  });
  test('R3d: HIGH-WATER MARK vs Load into an OLDER save / New Game (ids go backwards legitimately)', () => {
    const t = createNewsFeedTracker(); const s = createNewsFeedSeq();
    let r = observeNews(src(pass(20), 100), t, [], s);          // session city, id 20
    // player loads an older save (or starts a new game): pass ids restart low
    r = observeNews(src(pass(1), 500), t, r, s);
    r = observeNews(src(pass(2), 530), t, r, s);
    r = observeNews(src(pass(3), 560), t, r, s);
    console.log('R3d entries after 3 genuine post-load capacity-unknown passes:', cnt(r), '(1 = ALL post-load notices suppressed)');
    console.log('R3d maxId mark:', t.consolidatorCapacityUnknownMaxId);
  });
  test('R3e: () => number seq source accepted', () => {
    const t = createNewsFeedTracker(); let n = 0;
    const r = observeNews(src(pass(3), 10), t, [], () => n++);
    console.log('R3e id:', r[0]?.id);
    assert.ok(r[0].id.startsWith('consolidatorCapacityUnknown-'));
  });
});

describe('R3 — undo notice under a real run', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  function fixture(logSeed, consumed) {
    const buildings = [];
    for (let x = 0; x <= 20; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    tiers.forEach((t, i) => buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: t, builtTick: -1000 }));
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' });
    return withConn({
      ...initialState(), unlockedAll: true, funds: 5e8, nextId: 5000, buildings,
      roadMonitors: [], buildingMonitors: [], population: 0, tick: 0, consolidatorEnabled: false,
      xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL), lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
      consolidatorLog: logSeed, consolidatorUndoConsumed: consumed,
    });
  }
  const realOld = {
    id: 1, tick: 1, skipped: [],
    transactions: [{ sectionKey: 999, kind: 'consolidate',
      removed: [{ id: 20, spec: 'edu_nursery', x: 300, y: 300, builtTick: -1000, placedBy: 'player' }],
      added: [{ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' }],
      buildCost: 1000, scrapRecovered: 100, netCost: 900 }],
  };
  test('R3f: a cap-aged unconsumed pass yields a NOTICE, not a silent no-op (old N1)', () => {
    let s = reducer(fixture([realOld], false), { type: 'toggleConsolidator' });
    for (let i = 0; i < TICKS_PER_MONTH * 3; i++) s = reducer(s, { type: 'tick' });
    const log = s.consolidatorLog ?? [];
    console.log(`R3f cap=${CONSOLIDATOR_LOG_CAP} len=${log.length} realStillThere=${log.some((p) => p.transactions.length > 0)} consumed=${s.consolidatorUndoConsumed}`);
    const u = reducer(s, { type: 'consolidatorUndo' });
    console.log('R3f notice:', JSON.stringify(u.placeNotice), ' consumedAfter:', u.consolidatorUndoConsumed, ' id20 resurrected:', u.buildings.some((b) => b.id === 20));
    const u2 = reducer(u, { type: 'consolidatorUndo' });
    console.log('R3f second press identity no-op:', u2 === u);
  });
  test('R3g: a PRISTINE empty log stays a reference-identity no-op (AC-26)', () => {
    const s = fixture([], false);
    const u = reducer(s, { type: 'consolidatorUndo' });
    console.log('R3g identity preserved:', u === s, ' notice:', JSON.stringify(u.placeNotice));
    assert.equal(u, s);
  });
  test('R3h: a log of ONLY skip-only entries, unconsumed -> notice path, then idempotent', () => {
    const skipOnly = { id: 5, tick: 50, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] };
    const s = fixture([skipOnly], false);
    const u = reducer(s, { type: 'consolidatorUndo' });
    console.log('R3h notice:', JSON.stringify(u.placeNotice), ' log preserved:', (u.consolidatorLog ?? []).length, ' consumed:', u.consolidatorUndoConsumed);
    const u2 = reducer(u, { type: 'consolidatorUndo' });
    console.log('R3h idempotent:', u2 === u);
  });
});
