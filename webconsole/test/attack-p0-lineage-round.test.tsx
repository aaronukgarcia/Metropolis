// attack-p0-lineage-round.test.tsx — INDEPENDENT DESTRUCTIVE ROUND (GR#23),
// MOUNT LEVEL, against BUG-687 (P0) + FEAT-2326609780.
//
// Contains the two tests the build rounds were asked for and did NOT write:
//   (a) export via gameSaveText -> import via parseGameSave/applyLoadedSave
//       through a REAL SimProvider mount: lineageId survives verbatim and the
//       imported city persists under ITS OWN namespace.
//   (b) saveGameAs against a storage that REFUSES the savepoint write: the
//       journal is NOT cleared, the refusal surfaces via recordError, and no
//       rename / no file download happens.
// Plus THE P0 KILL-SHOT: Aaron's exact double-city scenario reconstructed end
// to end, in both orders.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

async function loadFakeIndexedDBFactory(): Promise<(backing?: Map<string, Map<string, string>>) => any> {
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  return mod.createFakeIndexedDBFactory;
}

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).Blob = window.Blob;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  // Anchor navigation is unimplemented in jsdom and only produces noise here.
  window.HTMLAnchorElement.prototype.click = function () {};
  return dom;
}

function pin(fn: () => void) {
  try {
    fn();
  } catch (e) {
    console.error('FINDING >>> ' + ((e as Error).message || String(e)));
    throw e;
  }
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

/**
 * Roots mounted by this file. A FAILED assertion must never leave one mounted:
 * SimProvider installs a wall-clock autosave interval on the ambient timer, so
 * an un-unmounted root keeps the node event loop alive forever and the whole
 * test FILE hangs instead of reporting the finding. Every test unmounts these
 * in a `finally`.
 */
const openRoots: Array<{ root: any; act: any }> = [];
async function closeAllRoots() {
  while (openRoots.length) {
    const { root, act } = openRoots.pop()!;
    try {
      await act(async () => {
        root.unmount();
      });
    } catch {
      /* already gone */
    }
  }
}

/** Mount SimProvider against the CURRENT globals and hand back the live context. */
async function mountProvider(dom: JSDOM) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');

  const seen: { ctx: any; state: any } = { ctx: null, state: null };
  function Probe() {
    const ctx = useSim();
    seen.ctx = ctx;
    seen.state = ctx.state;
    return null;
  }
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
  });
  await waitFor(() => !!seen.state, 8000);
  openRoots.push({ root, act });
  return { seen, root, act };
}

/** Mirrors replay.ts's private savepointKey. */
function keyOf(prefix: string, slot: number, lineageId?: string, legacyId = 'legacy') {
  if (!lineageId || lineageId === legacyId) return `${prefix}.${slot}`;
  return `${prefix}.${lineageId}.${slot}`;
}

// ===========================================================================
// (a) EXPORT -> IMPORT ROUND TRIP THROUGH A REAL MOUNT.
//
// The claim under attack: lineageId rides a GameSave verbatim and the imported
// city then autosaves under ITS OWN namespace — not the importing session's,
// and not the shared legacy one. If it did not, importing a friend's (or your
// own) exported city would immediately re-create the RCA's two-cities-one-
// keyspace collision.
// ===========================================================================
test('(a) EXPORT -> IMPORT through a real SimProvider mount: lineageId survives gameSaveText/parseGameSave verbatim and the imported city persists under ITS OWN namespace', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { gameSaveText, parseGameSave, buildGameSave } = await import('../src/sim/gamesave.ts');
    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { mintLineageId, readCurrentLineageId, SAVEPOINT_KEY_PREFIX, readAllSavepoints, mostRecentSavepoint, LEGACY_LINEAGE_ID } =
      await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    // --- The EXPORTED city: a real lineage, a real journal, real buildings. ---
    const exportedLineage = mintLineageId();
    let city: any = { ...initialState(), lineageId: exportedLineage, unlockedAll: true, funds: 5_000_000_000 };
    let journal = emptyJournal();
    for (let i = 0; i < 8; i++) {
      const a = { type: 'place', spec: 'road', x: 6 + i, y: 6 } as any;
      journal = recordAction(journal, city.tick, a);
      city = reducer(city, a);
    }
    const exportedBuildings = city.buildings.length;
    assert.ok(exportedBuildings > 0, 'test setup: the exported city must have buildings');

    // Exactly what exportCity/saveGameAs serialize (gamesave.ts is the SSOT).
    const outgoing = buildGameSave({
      state: city,
      journal,
      journalTail: [],
      name: 'Exported City',
      buildVersion: versionBadgeLabel(),
      camera: null,
    });
    const text = gameSaveText(outgoing);

    pin(() =>
      assert.equal(
        JSON.parse(text).savepoint.lineageId,
        exportedLineage,
        'THE EXPORTED FILE DROPPED THE LINEAGE: gameSaveText must carry savepoint.lineageId verbatim, or an imported city has no identity of its own ' +
          'and lands in whatever namespace the importing session happens to be in — the RCA collision, recreated on every import.',
      ),
    );

    // --- Round trip through the SAME validator the import path uses. ---
    const parsed = parseGameSave(text);
    assert.equal(parsed.ok, true, `parseGameSave rejected our own gameSaveText output: ${parsed.reason}`);
    pin(() =>
      assert.equal(
        parsed.save!.savepoint.lineageId,
        exportedLineage,
        'parseGameSave STRIPPED the lineage on the way back in — the round trip is not identity-preserving',
      ),
    );
    assert.equal(parsed.save!.savepoint.snapshot.lineageId, exportedLineage, 'the SimState inside the save must carry it too');

    // --- Now import it into a REAL mount that is in a DIFFERENT lineage. ---
    // Boot the provider fresh (it mints its own lineage), then import.
    const { seen, act } = await mountProvider(dom);
    const bootLineage = readCurrentLineageId(dom.window.localStorage as any);
    assert.notEqual(bootLineage, exportedLineage, 'test setup: the importing session must start in a DIFFERENT lineage');

    // importCity reads through pickAnyFile -> showOpenFilePicker.
    (dom.window as any).showOpenFilePicker = async () => [{ getFile: async () => ({ text: async () => text }) }];

    let importOk = false;
    await act(async () => {
      importOk = await seen.ctx.importCity();
    });
    assert.equal(importOk, true, 'importCity refused a file this very test produced through the SSOT serializer');
    // applyLoadedSave finishes on a 50ms timeout + a few render passes.
    await waitFor(() => seen.state.buildings.length === exportedBuildings, 8000);

    // 1. The pointer moved to the IMPORTED city's lineage.
    const afterPointer = readCurrentLineageId(dom.window.localStorage as any);
    pin(() =>
      assert.equal(
        afterPointer,
        exportedLineage,
        'IMPORT DID NOT ADOPT THE IMPORTED CITY\'S LINEAGE: metropolis.currentLineage still points at the importing session\'s own lineage, so every ' +
          'subsequent autosave of the imported city writes into a namespace the next boot will not read — the imported city is silently never saved.',
      ),
    );

    // 2. Its savepoint physically lives under ITS OWN namespaced key.
    const ownKey = keyOf(SAVEPOINT_KEY_PREFIX, 0, exportedLineage, LEGACY_LINEAGE_ID);
    const legacyKey = keyOf(SAVEPOINT_KEY_PREFIX, 0, undefined, LEGACY_LINEAGE_ID);
    pin(() =>
      assert.ok(
        dom.window.localStorage.getItem(ownKey) !== null,
        `the imported city was not persisted under its own namespace (${ownKey}) — keys present: ${Object.keys(dom.window.localStorage as any)
          .filter((k) => k.startsWith(SAVEPOINT_KEY_PREFIX))
          .join(', ')}`,
      ),
    );
    assert.equal(dom.window.localStorage.getItem(legacyKey), null, 'the imported city leaked into the shared LEGACY keyspace');

    // 3. And it round-trips back out of storage with the lineage intact.
    const back = mostRecentSavepoint(readAllSavepoints(dom.window.localStorage as any, new Date(), exportedLineage));
    assert.equal(back!.lineageId, exportedLineage);
    assert.equal(back!.snapshot.buildings.length, exportedBuildings, 'the persisted imported city is not the city we imported');

  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// ===========================================================================
// (b) SAVE-AS AGAINST A REFUSING STORAGE.
//
// The RCA called saveGameAs ignoring its own write result "the aggravator
// inside the P0": it cleared the journal and reported success regardless. The
// estate claims that is fixed. Attack it with a storage that refuses the
// savepoint write (quota shim — the shape a real wedged browser has) and check
// ALL FOUR consequences, not just the return value.
// ===========================================================================
test('(b) saveGameAs against a REFUSING storage: the journal is NOT cleared, the refusal is recorded, and no rename / no download happens', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { JOURNAL_KEY, loadJournal } = await import('../src/sim/journal.ts');
    const { SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');

    const { seen, act } = await mountProvider(dom);
    const startName = seen.ctx.cityName;

    // Build a real journal by playing.
    await act(async () => {
      for (let i = 0; i < 6; i++) seen.ctx.dispatch({ type: 'place', spec: 'road', x: 8 + i, y: 8 });
    });
    await act(async () => {
      // JOURNAL_PERSIST_DEBOUNCE_MS is 1s — wait past it so the write lands.
      await new Promise((r) => setTimeout(r, 1600));
    });
    const journalBefore = loadJournal(dom.window.localStorage as any);
    assert.ok(journalBefore.entries.length > 0, 'test setup: there must be a real persisted journal to lose');

    // THE REFUSING STORAGE: every savepoint write throws QuotaExceededError.
    // Everything else still works, so this isolates the savepoint refusal.
    const real = dom.window.localStorage;
    const shim = {
      getItem: (k: string) => real.getItem(k),
      removeItem: (k: string) => real.removeItem(k),
      key: (i: number) => real.key(i),
      get length() {
        return real.length;
      },
      clear: () => real.clear(),
      setItem: (k: string, v: string) => {
        if (k.startsWith(SAVEPOINT_KEY_PREFIX)) {
          const e: any = new Error('QuotaExceededError');
          e.name = 'QuotaExceededError';
          throw e;
        }
        real.setItem(k, v);
      },
    };
    Object.defineProperty(dom.window, 'localStorage', { value: shim, configurable: true });

    // No download, no rename must occur.
    let downloadCalls = 0;
    (dom.window as any).showSaveFilePicker = async () => {
      downloadCalls += 1;
      return { createWritable: async () => ({ write: async () => {}, close: async () => {} }) };
    };
    const createObjectURL = dom.window.URL.createObjectURL;
    (dom.window.URL as any).createObjectURL = (b: any) => {
      downloadCalls += 1;
      return createObjectURL ? createObjectURL.call(dom.window.URL, b) : 'blob:x';
    };
    (globalThis as any).URL = dom.window.URL;

    const errorsBefore = recentErrors().length;
    let result: any;
    await act(async () => {
      result = await seen.ctx.saveGameAs('A Brand New Name');
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });

    pin(() =>
      assert.equal(
        result?.ok,
        false,
        'saveGameAs reported SUCCESS while the savepoint write was refused — the RCA\'s "aggravator inside the P0" (store.tsx ignoring its own persist result) is alive.',
      ),
    );

    const journalAfter = loadJournal(shim as any);
    pin(() =>
      assert.equal(
        journalAfter.entries.length,
        journalBefore.entries.length,
        `A REFUSED Save As CLEARED THE JOURNAL: ${journalBefore.entries.length} entries before, ${journalAfter.entries.length} after. The journal is ` +
          'the ONLY record of play since the last good checkpoint; discarding it on a refusal destroys exactly the history the refusal was meant to protect.',
      ),
    );
    assert.notEqual(dom.window.localStorage.getItem(JOURNAL_KEY), null, 'the journal key was removed outright');

    pin(() =>
      assert.equal(
        downloadCalls,
        0,
        'a REFUSED Save As still wrote a file — the player gets a downloaded save of a city the app just told itself it could not save.',
      ),
    );
    pin(() =>
      assert.equal(
        seen.ctx.cityName,
        startName,
        `a REFUSED Save As still RENAMED the city (${startName} -> ${seen.ctx.cityName}) — the rename outlives the save that justified it.`,
      ),
    );

    const added = recentErrors().slice(0, Math.max(0, recentErrors().length - errorsBefore));
    pin(() =>
      assert.ok(
        added.some((e: any) => /Save (failed|refused)/i.test(e.msg) && /NOT being saved|quota/i.test(e.msg)),
        'THE REFUSAL WAS SILENT (GR#1/GR#17): no recordError names the failure. Recorded since the attempt: ' +
          JSON.stringify(added.map((e: any) => e.msg)),
      ),
    );

    Object.defineProperty(dom.window, 'localStorage', { value: real, configurable: true });
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// ===========================================================================
// THE P0 KILL-SHOT, FORWARD ORDER.
//
// Aaron's exact sequence: an old, long-played city sitting in the LEGACY
// (pre-lineage) namespace; Start Over; play the new city; 40 saves; reload.
// The NEW city must boot, every one of its saves must have SUCCEEDED under its
// own namespace, and the old city must still be intact under the legacy keys.
// ===========================================================================
test('KILL-SHOT (forward): old Y455-scale city in the LEGACY namespace -> Start Over -> 40 saves -> reload boots the NEW city, all 40 saves landed, the old city is intact', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const {
      createSavepoint,
      persistSavepoint,
      readAllSavepoints,
      mostRecentSavepoint,
      readCurrentLineageId,
      SAVEPOINT_CAP,
      LEGACY_LINEAGE_ID,
    } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const storage = dom.window.localStorage as unknown as Storage;

    // --- The OLD city: high tick (Y455 scale), many buildings, NO lineageId
    //     (written before the fix existed), occupying EVERY rotation slot. ---
    let oldCity: any = { ...initialState(), unlockedAll: true, funds: 5_000_000_000, tick: 455 * 12 * 30 };
    for (let i = 0; i < 60; i++) {
      const x = 4 + (i % 20) * 3;
      const y = 4 + Math.floor(i / 20) * 3;
      oldCity = reducer(oldCity, { type: 'place', spec: 'road', x, y: y + 1 } as any);
      oldCity = reducer(oldCity, { type: 'place', spec: 'res_hut', x, y } as any);
    }
    const OLD_TICK = oldCity.tick;
    const OLD_BUILDINGS = oldCity.buildings.length;
    assert.ok(OLD_BUILDINGS > 30, 'test setup: the old city must be substantial');
    // NOTE: the timestamps must be INSIDE AUTOSAVE_RETENTION_MS (30 days) —
    // BUG-469's purge-on-read would otherwise delete the seed before boot.
    for (let s = 0; s < SAVEPOINT_CAP; s++) {
      const at = new Date(Date.now() - (SAVEPOINT_CAP - s) * 60_000);
      assert.ok(
        persistSavepoint(storage, createSavepoint({ ...oldCity, tick: OLD_TICK + s }, [], at, running, null, 900 + s), at),
        'seed the old city into every legacy rotation slot',
      );
    }
    assert.equal(readCurrentLineageId(storage), LEGACY_LINEAGE_ID, 'a pre-fix install has no currentLineage pointer');

    // --- BOOT 1: the old city comes up (the migration stamps it in place). ---
    let m = await mountProvider(dom);
    await waitFor(() => m.seen.state.buildings.length >= OLD_BUILDINGS, 8000);
    assert.ok(m.seen.state.tick >= OLD_TICK, `boot 1 must restore the OLD city (tick ${m.seen.state.tick})`);

    // --- START OVER (the reset that mints the new lineage). ---
    await m.act(async () => {
      m.seen.ctx.dispatch({ type: 'reset' });
    });
    await m.act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });
    const newLineage = readCurrentLineageId(storage);
    pin(() =>
      assert.notEqual(
        newLineage,
        LEGACY_LINEAGE_ID,
        'START OVER DID NOT MINT A LINEAGE: the currentLineage pointer is still the legacy one, so the new city is about to write into the OLD city\'s ' +
          'slots — the P0, verbatim.',
      ),
    );
    assert.ok(m.seen.state.buildings.length < OLD_BUILDINGS, 'the reset must actually wipe the city');

    // --- Play the new city, then 40 saves. ---
    await m.act(async () => {
      for (let i = 0; i < 10; i++) m.seen.ctx.dispatch({ type: 'place', spec: 'road', x: 10 + i, y: 20 });
    });
    const NEW_BUILDINGS = m.seen.state.buildings.length;
    const NEW_TICK = m.seen.state.tick;
    assert.ok(NEW_BUILDINGS > 0 && NEW_TICK < OLD_TICK, `test setup: the new city must be LOW tick (${NEW_TICK}) — that is what the old gate refused`);

    let landed = 0;
    for (let i = 0; i < 40; i++) {
      let ok = false;
      await m.act(async () => {
        ok = await m.seen.ctx.saveGame();
      });
      if (ok) landed += 1;
    }
    pin(() =>
      assert.equal(
        landed,
        40,
        `ONLY ${landed}/40 SAVES OF THE NEW CITY LANDED. This is Aaron's P0: a brand-new low-tick city cannot beat the old high-tick one for a slot. ` +
          'Every refusal here is a stretch of play that exists nowhere on disk.',
      ),
    );

    // The new city's saves are physically in ITS namespace; the old city's
    // legacy slots are untouched.
    const newSlots = readAllSavepoints(storage, new Date(), newLineage);
    pin(() => assert.ok(newSlots.length > 0, 'the new city has NO savepoints at all under its own lineage'));
    assert.equal(mostRecentSavepoint(newSlots)!.snapshot.buildings.length, NEW_BUILDINGS);

    const legacySlots = readAllSavepoints(storage, new Date(), LEGACY_LINEAGE_ID);
    pin(() =>
      assert.equal(
        legacySlots.length,
        SAVEPOINT_CAP,
        'THE NEW CITY ATE THE OLD CITY\'S SLOTS — the fix is meant to make the two keyspaces physically disjoint, so the old save must survive untouched.',
      ),
    );
    assert.ok(mostRecentSavepoint(legacySlots)!.snapshot.buildings.length >= OLD_BUILDINGS, 'the old city\'s content was overwritten');


    // --- BOOT 2: THE RELOAD. The NEW city must come up. ---
    resetSaveStoreForTests();
    m = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 1500)); // let any post-mount IDB swap / tail replay settle
    pin(() =>
      assert.ok(
        m.seen.state.buildings.length === NEW_BUILDINGS,
        `THE OLD CITY RESURRECTED: the reload booted a city with ${m.seen.state.buildings.length} buildings at tick ${m.seen.state.tick}; the new ` +
          `city has ${NEW_BUILDINGS} at tick ${NEW_TICK} and the old one had ${OLD_BUILDINGS} at tick ${OLD_TICK}. This is BUG-687 verbatim.`,
      ),
    );
    assert.ok(m.seen.state.tick < OLD_TICK, `the reload restored the OLD city's tick (${m.seen.state.tick})`);
    assert.equal(readCurrentLineageId(storage), newLineage, 'the pointer drifted off the new city across the reload');

    // ...and the old city is STILL there, recoverable.
    assert.ok(
      mostRecentSavepoint(readAllSavepoints(storage, new Date(), LEGACY_LINEAGE_ID))!.snapshot.buildings.length >= OLD_BUILDINGS,
      'after the reload the old city must still be intact under the legacy keys',
    );

  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// ===========================================================================
// THE P0 KILL-SHOT, REVERSE ORDER.
//
// New game FIRST, then load the legacy city through applyLoadedSave, play it,
// reload. Neither direction may contaminate the other.
// ===========================================================================
test('KILL-SHOT (reverse): new game first -> load the LEGACY city via applyLoadedSave -> play -> reload boots the LOADED city, and the new city\'s own namespace is untouched', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { buildGameSave, gameSaveText } = await import('../src/sim/gamesave.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { readAllSavepoints, mostRecentSavepoint, readCurrentLineageId, LEGACY_LINEAGE_ID } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');
    const storage = dom.window.localStorage as unknown as Storage;

    // The legacy city as an on-disk save FILE (no lineageId anywhere — a save
    // exported before the fix existed).
    let legacyCity: any = { ...initialState(), unlockedAll: true, funds: 5_000_000_000, tick: 455 * 12 * 30 };
    let legacyJournal = emptyJournal();
    for (let i = 0; i < 30; i++) {
      const a = { type: 'place', spec: 'res_hut', x: 4 + (i % 15) * 3, y: 4 + Math.floor(i / 15) * 3 } as any;
      legacyJournal = recordAction(legacyJournal, legacyCity.tick, a);
      legacyCity = reducer(legacyCity, a);
    }
    const LEGACY_BUILDINGS = legacyCity.buildings.length;
    const legacyText = gameSaveText(
      buildGameSave({ state: legacyCity, journal: legacyJournal, journalTail: [], name: 'Old City', buildVersion: versionBadgeLabel(), camera: null }),
    );
    assert.equal(JSON.parse(legacyText).savepoint.lineageId, undefined, 'test setup: the legacy save file carries NO lineage');

    // --- Boot 1: a brand-new game (its own minted lineage), played + saved. ---
    let m = await mountProvider(dom);
    const newLineage = readCurrentLineageId(storage);
    await m.act(async () => {
      for (let i = 0; i < 8; i++) m.seen.ctx.dispatch({ type: 'place', spec: 'road', x: 12 + i, y: 30 });
    });
    const NEW_BUILDINGS = m.seen.state.buildings.length;
    let ok = false;
    await m.act(async () => {
      ok = await m.seen.ctx.saveGame();
    });
    assert.equal(ok, true, 'the brand-new game must be able to save');
    const newSlotsBefore = readAllSavepoints(storage, new Date(), newLineage).length;
    assert.ok(newSlotsBefore > 0);

    // --- Now LOAD the legacy city (File -> Open, the applyLoadedSave path). ---
    (dom.window as any).showOpenFilePicker = async () => [{ getFile: async () => ({ text: async () => legacyText }) }];
    await m.act(async () => {
      await m.seen.ctx.loadGame();
    });
    await waitFor(() => m.seen.state.buildings.length === LEGACY_BUILDINGS, 8000);

    // The pointer is checked LAST (below), after the reload has demonstrated
    // the actual consequence — a mis-pointed pointer only matters because of
    // which city comes back.
    const afterLoadPointer = readCurrentLineageId(storage);

    // --- Play the loaded city and save it. ---
    await m.act(async () => {
      for (let i = 0; i < 5; i++) m.seen.ctx.dispatch({ type: 'place', spec: 'road', x: 40 + i, y: 40 });
    });
    const LOADED_PLAYED = m.seen.state.buildings.length;
    for (let i = 0; i < 10; i++) {
      let saved = false;
      await m.act(async () => {
        saved = await m.seen.ctx.saveGame();
      });
      pin(() => assert.equal(saved, true, `save ${i} of the LOADED city was refused — cross-contamination between the two lineages`));
    }

    // The new game's own namespace must be untouched by all of that.
    const newAfter = readAllSavepoints(storage, new Date(), newLineage);
    pin(() =>
      assert.equal(
        newAfter.length,
        newSlotsBefore,
        'LOADING THE OLD CITY DESTROYED THE NEW GAME\'S SAVEPOINTS — the two keyspaces are supposed to be physically disjoint.',
      ),
    );
    assert.equal(mostRecentSavepoint(newAfter)!.snapshot.buildings.length, NEW_BUILDINGS, 'the new game\'s saved city was overwritten');


    // --- Boot 2: the LOADED city must come back, not the new game. ---
    resetSaveStoreForTests();
    m = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 1500));
    pin(() =>
      assert.equal(
        m.seen.state.buildings.length,
        LOADED_PLAYED,
        `THE RELOAD BOOTED THE WRONG CITY: got ${m.seen.state.buildings.length} buildings; the loaded-and-played city has ${LOADED_PLAYED} and the ` +
          `abandoned new game has ${NEW_BUILDINGS}. ROOT CAUSE: store.tsx's applyLoadedSave writes the current-lineage pointer only inside ` +
          '`if (savepointToPersist.lineageId)`. A LEGACY save (any city saved before this fix — i.e. every existing save file and named slot) ' +
          "carries NO lineageId, so the pointer is LEFT on whatever lineage was current before the Load. The loaded city's savepoints then go to " +
          `the legacy keys while the pointer still says '${afterLoadPointer}', and the next boot restores the ABANDONED city. This is BUG-687's ` +
          'own mechanism, reached through Load instead of Start Over. Fix: write the pointer unconditionally, normalising an absent lineageId to ' +
          "LEGACY_LINEAGE_ID (the same normalizeLineageId the freshness comparator already uses).",
      ),
    );
    // And the new game is still recoverable under its own lineage.
    assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(), newLineage))!.snapshot.buildings.length, NEW_BUILDINGS);

    // The pointer itself — the root cause the assertion above is a consequence of.
    pin(() =>
      assert.equal(
        afterLoadPointer,
        LEGACY_LINEAGE_ID,
        `LOADING A LEGACY CITY LEFT THE POINTER ON THE PREVIOUS LINEAGE (${afterLoadPointer}) — see the reload assertion above for the consequence.`,
      ),
    );

  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});
