import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, coerceBuildingCapacityTier } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { consolidationLadder } from '../src/sim/consolidator.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import { buildGameSave, gameSaveText, parseGameSave } from '../src/sim/gamesave.ts';
import { createSavepoint, restoreFromSavepoint, restampSavepointsBuildVersion, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';
import { recentErrors } from '../src/sim/backend.ts';
import { observeNews, createNewsFeedTracker } from '../src/sim/newsFeed.ts';

const RUNG = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city');
const G = RUNG.groupSize;
const memStorage = (init = {}) => {
  const m = new Map(Object.entries(init));
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), _map: m };
};
const V865 = () => recentErrors().filter((e) => e.code === 'MET-V865');
const V866 = () => recentErrors().filter((e) => e.code === 'MET-V866');

describe('R2-N5 — boundary coercion accounting and self-heal', () => {
  test('N5a: one savepoint load records MET-V865 once (count, not entries)', () => {
    const state = { ...initialState(), buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }] };
    const raw = JSON.stringify(createSavepoint(state, [], new Date()));
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: raw });
    const before = recentErrors().map((e) => `${e.code}|${e.msg}`);
    const res = restoreFromSavepoint(storage);
    assert.equal(res.success, true);
    const after = recentErrors().map((e) => `${e.code}|${e.msg}`);
    const added = after.filter((m) => !before.includes(m));
    console.log('N5a new error entries on one restore:', JSON.stringify(added, null, 0).slice(0, 400));
    console.log('N5a V865 count field:', V865().map((e) => e.count));
    console.log('N5a tier:', res.state.buildings.find((b) => b.id === 1).capacityTier);
  });

  test('N5b: repeated loads of the SAME poisoned savepoint keep re-recording (bytes never self-heal on read)', () => {
    const state = { ...initialState(), buildings: [{ id: 2, spec: 'edu_primary', x: 5, y: 5, builtTick: -1000, capacityTier: 2.5 }] };
    const raw = JSON.stringify(createSavepoint(state, [], new Date()));
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: raw });
    const c0 = V865().reduce((n, e) => n + e.count, 0);
    restoreFromSavepoint(storage);
    const c1 = V865().reduce((n, e) => n + e.count, 0);
    restoreFromSavepoint(storage);
    const c2 = V865().reduce((n, e) => n + e.count, 0);
    console.log('N5b V865 total count after 0/1/2 loads:', c0, c1, c2);
    console.log('N5b stored bytes still poisoned?', storage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`).includes('2.5'));
  });

  test('N5c: restamp write-back self-heals the stored bytes', () => {
    const state = { ...initialState(), buildings: [{ id: 3, spec: 'edu_primary', x: 5, y: 5, builtTick: -1000, capacityTier: 2.5 }] };
    const sp = createSavepoint(state, [], new Date(), 'v-old');
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(sp) });
    restampSavepointsBuildVersion(storage, 'v-new');
    const after = storage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`);
    console.log('N5c bytes still contain 2.5?', after.includes('2.5'), ' contains "capacityTier":2?', /"capacityTier":2[,}]/.test(after));
  });

  test('N5d: file save -> load -> save -> load converges; SECOND load records nothing', () => {
    const st = { ...initialState(), buildings: [{ id: 4, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }] };
    const s1 = buildGameSave({ state: st, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date('2026-01-01') });
    const p1 = parseGameSave(gameSaveText(s1));
    const c1 = V865().reduce((n, e) => n + e.count, 0);
    const s2 = buildGameSave({ state: p1.save.savepoint.snapshot, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date('2026-01-01') });
    const before = V865().reduce((n, e) => n + e.count, 0);
    const p2 = parseGameSave(gameSaveText(s2));
    const after = V865().reduce((n, e) => n + e.count, 0);
    console.log('N5d tiers:', p1.save.savepoint.snapshot.buildings[0].capacityTier, p2.save.savepoint.snapshot.buildings[0].capacityTier, ' second-load delta:', after - before, '(c1', c1, ')');
    assert.equal(p2.save.savepoint.snapshot.buildings[0].capacityTier, 8);
    assert.equal(after - before, 0, 'a clean save must record nothing on load');
  });

  test('N5e: the helper never mutates its input', () => {
    const b = { id: 1, spec: 'edu_nursery', capacityTier: 8.7 };
    const out = coerceBuildingCapacityTier(b, 0);
    assert.equal(b.capacityTier, 8.7, 'input untouched');
    assert.equal(out.capacityTier, 8);
    assert.notEqual(out, b);
  });
});

// ---------------------------------------------------------------------------
// (a) log-cap ageing: a real pass unconsumed, then enough skip-only passes to
// push it off the end of the 32-entry log.
// ---------------------------------------------------------------------------
import { CONSOLIDATOR_LOG_CAP } from '../src/sim/engine.ts';

describe('R2-N1 — undo vs the log cap', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  test('N1: a real pass can age OUT of the log while the flag still says unconsumed', () => {
    const buildings = [];
    for (let x = 0; x <= 20; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    tiers.forEach((t, i) => buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: t, builtTick: -1000 }));
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' });
    const realOld = {
      id: 1, tick: 1, skipped: [],
      transactions: [{
        sectionKey: 999, kind: 'consolidate',
        removed: [{ id: 20, spec: 'edu_nursery', x: 300, y: 300, builtTick: -1000, placedBy: 'player' }],
        added: [{ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' }],
        buildCost: 1000, scrapRecovered: 100, netCost: 900,
      }],
    };
    let s = withConn({
      ...initialState(), unlockedAll: true, funds: 5e8, nextId: 5000, buildings,
      roadMonitors: [], buildingMonitors: [], population: 0, tick: 0,
      consolidatorEnabled: false,
      xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL), lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
      consolidatorLog: [realOld], consolidatorUndoConsumed: false,
    });
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let i = 0; i < TICKS_PER_MONTH * 3; i++) s = reducer(s, { type: 'tick' });
    const log = s.consolidatorLog ?? [];
    const realStillThere = log.some((p) => p.transactions.length > 0);
    console.log(`N1 cap=${CONSOLIDATOR_LOG_CAP} logLen=${log.length} realPassStillInLog=${realStillThere} undoConsumed=${s.consolidatorUndoConsumed}`);
    const u = reducer(s, { type: 'consolidatorUndo' });
    console.log('N1 undo changed state?', u !== s, ' id-20 resurrected?', u.buildings.some((b) => b.id === 20), ' funds delta:', u.funds - s.funds, ' consumedAfter:', u.consolidatorUndoConsumed);
  });
});

// ---------------------------------------------------------------------------
// (b) the render-side outbox.
// ---------------------------------------------------------------------------
describe('R2-N2 — newsFeed outbox for capacity unknown', () => {
  const pass = (id) => ({ id, skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });
  const src = (p, tick = 100) => ({ notice: null, milestoneNotice: null, placeNotice: null, consolidatorLatestPass: p, tick });

  test('N2a: a capacity-unknown pass surfaces once and records MET-V866 once', () => {
    const t = createNewsFeedTracker();
    const before = V866().reduce((n, e) => n + e.count, 0);
    let ring = observeNews(src(pass(5)), t, [], () => 1);
    const after1 = V866().reduce((n, e) => n + e.count, 0);
    console.log('N2a entries:', ring.length, 'v866 delta:', after1 - before, ring[0]?.text?.slice(0, 60));
  });

  test('N2b: StrictMode double-observe of the SAME pass does not double-record', () => {
    const t = createNewsFeedTracker();
    const b = V866().reduce((n, e) => n + e.count, 0);
    let ring = observeNews(src(pass(6)), t, [], () => 1);
    ring = observeNews(src(pass(6)), t, ring, () => 2);
    const a = V866().reduce((n, e) => n + e.count, 0);
    console.log('N2b entries after double observe:', ring.length, ' v866 delta:', a - b);
  });

  test('N2c: a REUSED pass id (undo pops log[0], next pass re-mints the same id) is silently swallowed', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(11)), t, [], () => 1); // mixed pass id 11, capacity unknown seen
    const b = V866().reduce((n, e) => n + e.count, 0);
    // player undoes pass 11 -> it is popped -> the NEXT pass re-mints id 11
    ring = observeNews(src(pass(11), 200), t, ring, () => 2);
    const a = V866().reduce((n, e) => n + e.count, 0);
    console.log('N2c entries after the re-minted id-11 pass:', ring.length, ' v866 delta:', a - b, '(expect 2 entries / delta 1 if not swallowed)');
  });

  test('N2d: a NEW GAME (log ids restart at 1) with a retained tracker', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(1)), t, [], () => 1);
    const b = V866().reduce((n, e) => n + e.count, 0);
    ring = observeNews(src(pass(1), 5), t, ring, () => 2); // brand-new city, first pass is id 1 again
    const a = V866().reduce((n, e) => n + e.count, 0);
    console.log('N2d entries:', ring.length, ' v866 delta:', a - b);
  });

  test('N2e: an OLD entry becoming log[0] after undo re-fires a stale notice', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(20)), t, [], () => 1);
    ring = observeNews(src(pass(9), 300), t, ring, () => 2); // undo popped 20; log[0] is now the older id-9 skip pass
    console.log('N2e entries:', ring.length, ring.map((e) => e.text.slice(0, 40)));
  });
});
