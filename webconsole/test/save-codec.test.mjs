// save-codec.test.mjs — FEAT-1972079935: compress large localStorage save
// payloads so a dogfood city fits under the 5 MB quota.
//
// Covers: lossless round-trip, backward-compat with legacy uncompressed
// values, a measured compression ratio on a large synthetic city-save-shaped
// fixture, and integration proof that a compressed savepoint / named save
// restores byte-identical SimState through the real save/load paths.
//
// Run with `npm test` (node --test).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { encode, decode, LZ_MAGIC } from '../src/sim/saveCodec.ts';
import { persistSavepoint, readAllSavepoints, createSavepoint } from '../src/sim/replay.ts';
import { writeNamedSave, readNamedSave, cityNameToSlug } from '../src/sim/namedsaves.ts';
import { buildGameSave } from '../src/sim/gamesave.ts';
import { captureBeforeWipe, readPreWipeArchive, PREWIPE_ARCHIVE_KEY } from '../src/sim/captureBeforeWipe.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

class MockStorage {
  constructor() {
    this.data = new Map();
  }
  getItem(k) {
    return this.data.has(k) ? this.data.get(k) : null;
  }
  setItem(k, v) {
    this.data.set(k, String(v));
  }
  removeItem(k) {
    this.data.delete(k);
  }
}

/**
 * Build a large, city-save-shaped JSON string: thousands of repetitive
 * building-like objects, the same real-world pattern that makes actual
 * dogfood saves (namedSave.Dev-city2 1.77 MB etc.) compress so well.
 */
function bigCitySaveJson(buildingCount = 8_000) {
  const buildings = [];
  const specs = ['res_hut', 'res_block', 'shop_small', 'factory_light', 'road', 'power_plant', 'water_tower'];
  for (let i = 0; i < buildingCount; i++) {
    buildings.push({
      id: i,
      spec: specs[i % specs.length],
      x: i % 200,
      y: Math.floor(i / 200),
      tick: i * 3,
      colour: '#a1b2c3',
      jobs: 4,
      residents: 6,
      online: true,
      district: 'central',
      wearLevel: 0.12,
    });
  }
  return JSON.stringify({
    format: 'metropolis-save/1',
    name: 'Dev-city-fixture',
    tick: 123456,
    funds: 5_000_000,
    buildings,
  });
}

describe('saveCodec: encode/decode round-trip + backward-compat', () => {
  test('lossless round-trip on representative city-save JSON', () => {
    const json = bigCitySaveJson();
    const encoded = encode(json);
    assert.ok(encoded.startsWith(LZ_MAGIC), 'encode() must prefix the magic marker');
    const decoded = decode(encoded);
    assert.equal(decoded, json, 'decode(encode(x)) must equal x exactly (lossless)');
    assert.deepEqual(JSON.parse(decoded), JSON.parse(json));
  });

  test('lossless round-trip on small/edge-case strings (empty, unicode, whitespace-only)', () => {
    for (const s of ['', '{}', '   ', JSON.stringify({ emoji: '🏙️🚗', accented: 'café' })]) {
      assert.equal(decode(encode(s)), s);
    }
  });

  test('backward-compat: a legacy PLAIN-JSON value (no LZv1: prefix) decodes UNCHANGED', () => {
    const legacy = JSON.stringify({ format: 'metropolis-save/1', tick: 1, buildings: [] });
    assert.equal(decode(legacy), legacy, 'a legacy uncompressed value must pass through decode() untouched');
    assert.doesNotThrow(() => JSON.parse(decode(legacy)));
  });

  test('decode() never throws on garbage input (corrupt/foreign-prefixed string)', () => {
    assert.doesNotThrow(() => decode(LZ_MAGIC + '###not-valid-lz-data###'));
    assert.doesNotThrow(() => decode('random unrelated string'));
  });

  test('measured compression ratio on the large fixture is at least 3x', () => {
    const json = bigCitySaveJson();
    const encoded = encode(json);
    const ratio = json.length / encoded.length;
    assert.ok(ratio >= 3, `expected >=3x compression, got ${ratio.toFixed(2)}x (${json.length} -> ${encoded.length} chars)`);
  });
});

describe('saveCodec wired into the big write paths', () => {
  test('savepoint: persistSavepoint stores a COMPRESSED value, restoreFromSavepoint/readAllSavepoints returns identical state', () => {
    const storage = new MockStorage();
    let state = initialState();
    state = reducer(state, { type: 'debugFunds', amount: 10_000 });
    state = reducer(state, { type: 'place', spec: 'res_hut', x: 5, y: 5 });

    const savepoint = createSavepoint(state, []);
    const ok = persistSavepoint(storage, savepoint);
    assert.ok(ok, 'persist must succeed');

    const raw = storage.getItem('metropolis.savepoint.0');
    assert.ok(raw.startsWith(LZ_MAGIC), 'the stored savepoint string must be compressed');

    const [restored] = readAllSavepoints(storage);
    // Compare against a plain JSON.parse(JSON.stringify(state)) round-trip
    // (not `state` directly) — this isolates the CODEC's correctness from
    // JSON's own normalization quirks (e.g. -0 -> 0) which apply identically
    // whether or not compression sits in front of localStorage.
    assert.deepEqual(restored.snapshot, JSON.parse(JSON.stringify(state)), 'decompressed snapshot must be byte-identical to the JSON-round-tripped saved state');
  });

  test('savepoint: a LEGACY plain-JSON value already in storage still reads back correctly', () => {
    const storage = new MockStorage();
    let state = initialState();
    state = reducer(state, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
    const savepoint = createSavepoint(state, []);
    // Simulate a save written before compression shipped: plain JSON, no prefix.
    storage.setItem('metropolis.savepoint.0', JSON.stringify(savepoint));

    const [restored] = readAllSavepoints(storage);
    assert.ok(restored, 'legacy uncompressed savepoint must still be readable');
    assert.deepEqual(restored.snapshot, JSON.parse(JSON.stringify(state)));
  });

  test('named save: writeNamedSave stores a COMPRESSED slot, readNamedSave restores identical SimState', () => {
    const storage = new MockStorage();
    let state = initialState();
    state = reducer(state, { type: 'debugFunds', amount: 25_000 });
    state = reducer(state, { type: 'place', spec: 'shop_small', x: 8, y: 3 });

    const save = buildGameSave({
      state,
      journal: { entries: [] },
      journalTail: [],
      name: 'Dev-city2',
      buildVersion: 'v1.2.3-test',
    });

    const ok = writeNamedSave(storage, save);
    assert.ok(ok, 'writeNamedSave must succeed');

    const slug = cityNameToSlug('Dev-city2');
    const raw = storage.getItem(`metropolis.namedSave.${slug}`);
    assert.ok(raw, 'named save slot must exist');
    assert.ok(raw.startsWith(LZ_MAGIC), 'the stored named-save slot must be compressed');

    const restored = readNamedSave(storage, slug);
    assert.ok(restored, 'readNamedSave must return the save');
    assert.deepEqual(
      restored.savepoint.snapshot,
      JSON.parse(JSON.stringify(save.savepoint.snapshot)),
      'restored snapshot must be byte-identical',
    );
  });

  test('named save: a LEGACY plain-JSON slot still reads back correctly', () => {
    const storage = new MockStorage();
    let state = initialState();
    const save = buildGameSave({
      state,
      journal: { entries: [] },
      journalTail: [],
      name: 'Legacy-city',
      buildVersion: 'v1.0.0-test',
    });
    // Simulate a save slot written before compression shipped.
    const slug = cityNameToSlug('Legacy-city');
    storage.setItem(`metropolis.namedSave.${slug}`, JSON.stringify(save));

    const restored = readNamedSave(storage, slug);
    assert.ok(restored, 'legacy uncompressed named save must still be readable');
    assert.deepEqual(restored.savepoint.snapshot, JSON.parse(JSON.stringify(state)));
  });

  test('the index and currentCityName keys stay PLAIN (not compressed) — small keys not worth it', () => {
    const storage = new MockStorage();
    const state = initialState();
    const save = buildGameSave({
      state,
      journal: { entries: [] },
      journalTail: [],
      name: 'Small-city',
      buildVersion: 'v1.0.0-test',
    });
    writeNamedSave(storage, save);
    const indexRaw = storage.getItem('metropolis.namedSaves');
    const cityNameRaw = storage.getItem('metropolis.currentCityName');
    assert.ok(indexRaw, 'index must exist');
    assert.ok(!indexRaw.startsWith(LZ_MAGIC), 'the small index key must not be compressed');
    assert.ok(cityNameRaw, 'currentCityName must exist');
    assert.ok(!cityNameRaw.startsWith(LZ_MAGIC), 'the small currentCityName key must not be compressed');
    assert.doesNotThrow(() => JSON.parse(indexRaw), 'index must still be plain JSON');
  });

  test('pre-wipe archive (GR#27): captureBeforeWipe stores a COMPRESSED archive, readPreWipeArchive decompresses it', () => {
    const storage = new MockStorage();
    let state = initialState();
    state = reducer(state, { type: 'debugFunds', amount: 50_000 });
    state = reducer(state, { type: 'place', spec: 'res_hut', x: 5, y: 5 });

    captureBeforeWipe(state, 'v9.9.9-test', storage, 1_701_234_567_890);

    const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
    assert.ok(raw.startsWith(LZ_MAGIC), 'the pre-wipe archive must be compressed');

    const archive = readPreWipeArchive(storage);
    assert.equal(archive.length, 1);
    assert.equal(archive[0].tick, state.tick);
  });

  test('pre-wipe archive (GR#27): a LEGACY plain-JSON archive still reads back correctly', () => {
    const storage = new MockStorage();
    const legacyEntry = { capturedAtMs: 1000, tick: 42, debug: { meta: { tick: 42 }, sim: { tick: 42 } } };
    storage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify([legacyEntry]));

    const archive = readPreWipeArchive(storage);
    assert.equal(archive.length, 1);
    assert.equal(archive[0].tick, 42);
  });
});

describe('GR#27 fail-closed contract still holds with compression', () => {
  test('a setItem that ALWAYS throws still blocks the wipe (compression cannot mask a quota failure)', () => {
    const alwaysThrows = {
      getItem() {
        return null;
      },
      setItem() {
        throw new Error('QuotaExceededError: setItem blocked');
      },
    };
    let state = initialState();
    state = reducer(state, { type: 'debugFunds', amount: 1_000 });
    assert.throws(
      () => captureBeforeWipe(state, 'v9.9.9-test', alwaysThrows, 1000),
      /setItem blocked/,
      'compression must not swallow a genuine, un-recoverable quota failure',
    );
  });
});
