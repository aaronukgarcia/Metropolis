import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import {
  buildGameSave,
  parseGameSave,
  suggestedSaveName,
  GAME_SAVE_FORMAT,
  gameSaveText,
} from '../src/sim/gamesave.ts';

test('FEAT-1972079920: Save As round-trips tick/funds/buildings', () => {
  const state = initialState();
  const save = buildGameSave({
    state,
    journal: emptyJournal(),
    journalTail: [],
    name: 'Test City',
    buildVersion: 'v0.3.0.141',
    now: new Date('2026-08-29T00:00:00.000Z'),
  });
  assert.equal(save.format, GAME_SAVE_FORMAT);
  const parsed = parseGameSave(gameSaveText(save));
  assert.equal(parsed.ok, true);
  assert.equal(parsed.save.buildVersion, 'v0.3.0.141');
  assert.equal(parsed.save.savepoint.snapshot.tick, state.tick);
  assert.equal(parsed.save.savepoint.snapshot.funds, state.funds);
  assert.equal(parsed.save.savepoint.snapshot.buildings.length, state.buildings.length);
  assert.equal(parsed.save.journal.entries.length, 0);
});

test('FEAT-1972079920/BUG-446: parse rejects debug.json and garbage by THROWING a registry-sourced error', () => {
  // BUG-446: parseGameSave no longer returns { ok: false, reason } for a
  // malformed shape — it THROWS a MET-V850 coded error (GR#1/GR#7). Any code
  // still asserting `.ok === false` here would have silently passed before
  // the fix (the old return WAS ok:false) and would now silently pass again
  // for the wrong reason if it swallowed the throw — assert.throws makes the
  // regression impossible to miss.
  assert.throws(
    () => parseGameSave(JSON.stringify({ format: 'metropolis-debug/1', sim: {} })),
    /debug dump/,
  );
  assert.throws(() => parseGameSave('not json'));
  assert.throws(() => parseGameSave('{}'));
  try {
    parseGameSave(JSON.stringify({ format: 'metropolis-debug/1', sim: {} }));
    assert.fail('expected parseGameSave to throw');
  } catch (e) {
    assert.equal(e.code, 'MET-V850');
  }
});

test('BUG-446 AC-3/AC-8: parseGameSave throws MET-V850 for every malformed-shape scenario (RED proven pre-fix)', () => {
  const validState = initialState();
  const goodSave = buildGameSave({
    state: validState,
    journal: emptyJournal(),
    journalTail: [],
    name: 'Attack City',
    buildVersion: 'v0.3.0.999',
    now: new Date('2026-09-01T00:00:00.000Z'),
  });
  const goodObj = JSON.parse(gameSaveText(goodSave));

  const assertRejects = (mutate, why) => {
    const clone = JSON.parse(JSON.stringify(goodObj));
    mutate(clone);
    assert.throws(() => parseGameSave(JSON.stringify(clone)), (e) => {
      assert.equal(e.code, 'MET-V850', `${why}: expected MET-V850, got ${e.code}`);
      return true;
    }, why);
  };

  // (c) non-object top-level value
  assert.throws(() => parseGameSave(JSON.stringify('just a string')), /object/);
  assert.throws(() => parseGameSave(JSON.stringify(42)), /object/);
  assert.throws(() => parseGameSave(JSON.stringify(null)), /object/);
  assert.throws(() => parseGameSave(JSON.stringify([1, 2, 3])), /object/);

  // (b) missing / wrong-typed required top-level fields
  assertRejects((c) => delete c.name, 'missing name');
  assertRejects((c) => (c.savedAt = 12345), 'wrong-typed savedAt');
  assertRejects((c) => delete c.buildVersion, 'missing buildVersion');
  assertRejects((c) => delete c.savepoint, 'missing savepoint');
  assertRejects((c) => delete c.savepoint.snapshot, 'missing snapshot');
  assertRejects((c) => (c.savepoint.snapshot.tick = 'not a number'), 'wrong-typed tick');
  assertRejects((c) => delete c.savepoint.snapshot.buildings, 'missing buildings array');
  assertRejects((c) => delete c.journal, 'missing journal');
  assertRejects((c) => delete c.journal.entries, 'missing journal.entries');

  // (a) a building array element that is garbage / missing required fields
  assertRejects((c) => c.savepoint.snapshot.buildings.push('garbage-string-element'), 'garbage string element');
  assertRejects((c) => c.savepoint.snapshot.buildings.push({ notABuilding: true }), 'building missing all fields');
  assertRejects((c) => c.savepoint.snapshot.buildings.push({ id: 1, spec: 'res_house' }), 'building missing x/y');
  assertRejects((c) => c.savepoint.snapshot.buildings.push({ id: 'not-a-number', spec: 'res_house', x: 1, y: 1 }), 'wrong-typed id');
  assertRejects((c) => c.savepoint.snapshot.buildings.push({ id: 1, spec: 7, x: 1, y: 1 }), 'wrong-typed spec');
  assertRejects((c) => c.savepoint.snapshot.buildings.push({ id: 1, spec: 'res_house', x: 'nope', y: 1 }), 'wrong-typed x');
  assertRejects(
    (c) => c.savepoint.snapshot.buildings.push({ id: 1, spec: 'res_house', x: 1, y: 1, capacityTier: 'oops' }),
    'wrong-typed optional field',
  );

  // (d) a valid buildGameSave(...) output still parses successfully and
  // round-trips to an equal object (never a false positive from over-eager
  // validation).
  const parsed = parseGameSave(JSON.stringify(goodObj));
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.save, goodObj);
});

test('FEAT-1972079920: exploded funds in a save snapshot are sanitised', () => {
  const state = { ...initialState(), funds: -2.9e35 };
  const save = buildGameSave({
    state,
    journal: emptyJournal(),
    journalTail: [],
    name: 'Wreck',
    buildVersion: 'v0.3.0.141',
  });
  assert.equal(save.savepoint.snapshot.funds, 0);
  const parsed = parseGameSave(gameSaveText(save));
  assert.equal(parsed.ok, true);
  assert.equal(parsed.save.savepoint.snapshot.funds, 0);
});

test('FEAT-1972079920: suggestedSaveName uses gameDate', () => {
  assert.equal(suggestedSaveName(0), 'Metropolis-Y1-D1-M1.json');
  assert.equal(suggestedSaveName(0, 'My City!'), 'My-City-Y1-D1-M1.json');
});

test('recent opened list keeps newest 10 and dedupes by slug', async () => {
  const { recordRecentOpened, listRecentOpened, RECENT_OPENED_CAP } = await import('../src/sim/recentfiles.ts');
  const map = new Map();
  const storage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => {
      map.set(k, String(v));
    },
  };
  for (let i = 0; i < 12; i++) {
    recordRecentOpened(storage, { name: `City ${i}`, tick: i, population: 0, funds: 1 });
  }
  const list = listRecentOpened(storage);
  assert.equal(list.length, RECENT_OPENED_CAP);
  assert.equal(list[0].name, 'City 11');
  recordRecentOpened(storage, { name: 'City 5', tick: 99, population: 1, funds: 2 });
  const again = listRecentOpened(storage);
  assert.equal(again.length, RECENT_OPENED_CAP);
  assert.equal(again[0].name, 'City 5');
  assert.equal(again.filter((r) => r.slug === again[0].slug).length, 1);
});

test('Dev-city1 fixture is a loadable snapshot', async () => {
  const { readFileSync } = await import('node:fs');
  const fixture = JSON.parse(readFileSync(new URL('../src/sim/fixtures/Dev-city1.json', import.meta.url), 'utf8'));
  assert.equal(typeof fixture.tick, 'number');
  assert.equal(typeof fixture.funds, 'number');
  assert.ok(Array.isArray(fixture.buildings));
  assert.ok(fixture.buildings.length > 0);
});
