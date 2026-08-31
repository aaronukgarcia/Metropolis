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

test('FEAT-1972079920: parse rejects debug.json and garbage', () => {
  const debug = parseGameSave(JSON.stringify({ format: 'metropolis-debug/1', sim: {} }));
  assert.equal(debug.ok, false);
  assert.match(debug.reason ?? '', /debug dump/);
  const garbage = parseGameSave('not json');
  assert.equal(garbage.ok, false);
  const empty = parseGameSave('{}');
  assert.equal(empty.ok, false);
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
