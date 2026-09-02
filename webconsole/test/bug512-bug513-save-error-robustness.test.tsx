// bug512-bug513-save-error-robustness.test.tsx
//
// RED-PROVE coverage for a small save-error-robustness cluster found by a
// verification pass over BUG-445's named-save collision gate and BUG-513's
// load-error-surfacing gaps:
//
//   BUG-512 (P3): BUG-445 wired the named-save collision check ONLY at the
//     Save-As / renameCity UI vectors. The plain saveGame() and load-time
//     rememberOpened() call sites reach the exact same writeNamedSave, but
//     ungated — so a plain save or a load onto a name that slug-collides a
//     DIFFERENT existing city's slot silently clobbered it. Fixed in
//     store.tsx's rememberOpened() by routing it through the same
//     checkNamedSaveCollision() BUG-445 already uses, threading an
//     already-confirmed overwrite through from saveGameAs so that vector
//     isn't double-blocked.
//
//   BUG-513 GAP 2 (P2): the load handler caught the coded MET-V850 parse
//     error but called recordError() without `code`, so the code never
//     reached the ring/debug.json (gap-1 already renders it when present).
//
//   BUG-513 GAP 3 (P2): the captureError banner is hard-worded for the
//     Start-Over/reset flow but is ALSO fired for load failures, so a load
//     refusal rendered the wrong ("Start Over aborted...") wording.
//
//   BUG-513 GAP-1 NIT: RightDock's error-list row used `e.code ?? e.type`,
//     so a record with code:'' (empty-but-present) rendered a blank `[]`
//     instead of falling back to the type.
//
// Uses the same jsdom + react-dom/client + act() mounting recipe proven out
// in store-dispatch.test.tsx / store-reset-capture.test.tsx (SSR renderToString
// never re-renders, so real state transitions need a real client root).
//
// TEARDOWN DISCIPLINE (post-hang-fix): SimProvider's autosave/tick-loop
// useEffects register real `setInterval` timers (store.tsx ~382, ~464) that
// are Node-global, not tied to jsdom's `window` — closing the jsdom window
// does NOT clear them. Only `root.unmount()` (which runs React's effect
// cleanups, calling `clearInterval`) does. The first version of this file put
// `root.unmount()` as the LAST statement in the outer try block, so an
// assertion failure ANYWHERE above it skipped the unmount, leaked the
// interval, and hung the whole `tsx --test` process (a real Node timer keeps
// the event loop alive forever) instead of just failing the one test. Fixed
// by nesting an inner try/finally around every mounted-root test so
// `root.unmount()` runs unconditionally, exactly like store-dispatch.test.tsx.
// Each test also carries an explicit `timeout` as a second line of defence.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const MOUNTED_TEST_TIMEOUT_MS = 20_000;

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test(
  'BUG-512: a plain saveGame() onto a DIFFERENT city name that slug-collides is blocked, not silently overwritten; same-city re-save proceeds',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const React = await import('react');
      const { createRoot } = await import('react-dom/client');
      const { act } = await import('react-dom/test-utils');
      const { SimProvider, useSim } = await import('../src/sim/store.tsx');
      const { initialState } = await import('../src/sim/engine.ts');
      const { buildGameSave } = await import('../src/sim/gamesave.ts');
      const { emptyJournal } = await import('../src/sim/journal.ts');
      const { writeNamedSave, readNamedSave, cityNameToSlug } = await import('../src/sim/namedsaves.ts');
      const { recentErrors } = await import('../src/sim/backend.ts');

      function Probe() {
        const sim = useSim();
        (Probe as any)._latest = sim;
        return null;
      }
      function latest(): any {
        const v = (Probe as any)._latest;
        if (!v) throw new Error('Probe has not rendered yet');
        return v;
      }

      const container = dom.window.document.getElementById('root')!;
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
        });

        // Rename the live city to a name that will slug-collide with a DIFFERENT
        // pre-existing save (cityNameToSlug collapses whitespace/hyphens alike,
        // so "Bug512 City" and "Bug512-City" share a slug but are different
        // *displayed* names — exactly the BUG-445 collision definition).
        // renameCity triggers a setState (setCityName) — MUST be wrapped in act()
        // so `cityName` is committed before saveGame() reads it via closure; the
        // unwrapped call let a stale cityName silently pass (no collision seen).
        let renameResult: any;
        await act(async () => {
          renameResult = latest().renameCity('Bug512 City');
        });
        assert.equal(renameResult.ok, true, 'renaming to a not-yet-collided name must succeed');
        const slug = cityNameToSlug('Bug512 City');
        assert.equal(slug, cityNameToSlug('Bug512-City'), 'fixture assumption: these two names share a slug');

        // Seed a DIFFERENT city's save directly at that slug (as if it were saved
        // from another session/device) using the SAME real save-building helpers
        // store.tsx itself uses.
        const foreignSave = buildGameSave({
          state: initialState(),
          journal: emptyJournal(),
          journalTail: [],
          name: 'Bug512-City',
          buildVersion: 'test-build',
        });
        const seeded = writeNamedSave(dom.window.localStorage as any, foreignSave);
        assert.ok(seeded, 'fixture: seeding the foreign save must succeed');
        const beforeErrorCount = recentErrors().length;

        // Plain saveGame() — BUG-512's ungated path. Before the fix this silently
        // clobbers the foreign save at `slug`; after the fix it must refuse.
        await act(async () => {
          const ok = await latest().saveGame();
          assert.equal(ok, true, 'saveGame() itself still reports success (the session save is unaffected)');
        });

        const afterCollisionAttempt = readNamedSave(dom.window.localStorage as any, slug);
        assert.ok(afterCollisionAttempt, 'the named slot must still exist');
        assert.equal(
          afterCollisionAttempt!.name,
          'Bug512-City',
          'BUG-512: a DIFFERENT city collision must NOT be silently overwritten by a plain saveGame()',
        );
        assert.ok(
          recentErrors().length > beforeErrorCount,
          'BUG-512: the refused overwrite must be reported (GR#1), not swallowed',
        );

        // Now prove the SAME-city path is unaffected: rename to a brand-new,
        // never-before-used name (no collision at all) and confirm saveGame()
        // writes it normally, then a SECOND saveGame() (a true same-city re-save)
        // still succeeds and is not blocked by its own prior write.
        const freshName = 'Bug512 Fresh City';
        let freshRename: any;
        await act(async () => {
          freshRename = latest().renameCity(freshName);
        });
        assert.equal(freshRename.ok, true);
        const freshSlug = cityNameToSlug(freshName);

        await act(async () => {
          const ok = await latest().saveGame();
          assert.equal(ok, true);
        });
        const firstWrite = readNamedSave(dom.window.localStorage as any, freshSlug);
        assert.ok(firstWrite, 'first save onto a free slot must succeed');
        assert.equal(firstWrite!.name, freshName);

        const errsBeforeResave = recentErrors().filter((e) => e.code === 'MET-V851').length;
        await act(async () => {
          const ok = await latest().saveGame();
          assert.equal(ok, true, 'a same-city re-save must proceed');
        });
        const secondWrite = readNamedSave(dom.window.localStorage as any, freshSlug);
        assert.ok(secondWrite, 'the slot must still exist after the re-save');
        assert.equal(secondWrite!.name, freshName, 'a same-city re-save must still write through');
        const errsAfterResave = recentErrors().filter((e) => e.code === 'MET-V851').length;
        assert.equal(errsAfterResave, errsBeforeResave, 'a same-city re-save must NOT be reported as a collision');
      } finally {
        // MUST run even when an assertion above throws — otherwise SimProvider's
        // setInterval timers (autosave/tick-loop) never get their effect cleanup
        // and leak past the test, hanging the whole tsx --test process.
        await act(async () => {
          root.unmount();
        });
      }
    } finally {
      dom.window.close();
    }
  },
);

test(
  'BUG-513 GAP 2 + GAP 3: a load of a malformed save records the MET-V850 code and shows a load-worded banner (not "Start Over")',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const React = await import('react');
      const { createRoot } = await import('react-dom/client');
      const { act } = await import('react-dom/test-utils');
      const { SimProvider, useSim } = await import('../src/sim/store.tsx');
      const { recentErrors } = await import('../src/sim/backend.ts');

      function Probe() {
        const sim = useSim();
        (Probe as any)._latest = sim;
        return null;
      }
      function latest(): any {
        const v = (Probe as any)._latest;
        if (!v) throw new Error('Probe has not rendered yet');
        return v;
      }

      const container = dom.window.document.getElementById('root')!;
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
        });

        // Stub the File System Access API's open-file picker (pickOpenFile's
        // preferred path in store.tsx) so loadGame() resolves a malformed blob
        // without touching a real OS file dialog.
        (dom.window as any).showOpenFilePicker = async () => [
          {
            getFile: async () => ({
              text: async () => 'this is not { valid json',
            }),
          },
        ];

        await act(async () => {
          await latest().loadGame();
        });

        const coded = recentErrors().find((e) => e.code === 'MET-V850');
        assert.ok(
          coded,
          'BUG-513 GAP 2: a malformed-save load must record an error whose code is MET-V850 (parseGameSave\'s registry code)',
        );

        const alertEl = container.querySelector('[role="alert"]');
        assert.ok(alertEl, 'the captureError banner must render after a load refusal');
        const text = alertEl!.textContent ?? '';
        assert.doesNotMatch(
          text,
          /Start Over/,
          'BUG-513 GAP 3: a LOAD failure banner must not claim "Start Over" (nothing was wiped)',
        );
        assert.match(text, /MET-V850/, 'BUG-513 GAP 3: the banner must surface the registry code for a load failure');
      } finally {
        await act(async () => {
          root.unmount();
        });
      }
    } finally {
      dom.window.close();
    }
  },
);

test('BUG-513 GAP-1 NIT: an error record with code:\'\' renders [type] in RightDock, not a blank [ ]', { timeout: MOUNTED_TEST_TIMEOUT_MS }, async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { renderToString } = await import('react-dom/server');
    const { SimProvider } = await import('../src/sim/store.tsx');
    const { BusyProvider } = await import('../src/components/Busy.tsx');
    const { DebugTab } = await import('../src/components/right/RightDock.tsx');
    const { recordError } = await import('../src/sim/backend.ts');

    // An empty-but-present code (distinct from "no code at all", which the
    // component already falls back on via `e.code ? ... : ...` in the title).
    recordError('BUG-513 gap-1 nit fixture: empty code', { type: 'app', code: '' });

    // DebugTab calls useBusy() (Commit-snapshot button), so it needs the same
    // ErrorBoundary > BusyProvider > SimProvider nesting App.tsx uses (see
    // mount.test.tsx's DebugTab-adjacent smoke tests for the same pattern).
    // renderToString runs no effects/timers (SSR), so there is nothing to
    // unmount/clear here — the outer dom.window.close() finally is sufficient.
    const html = renderToString(
      React.default.createElement(BusyProvider, {
        children: React.default.createElement(SimProvider, {
          children: React.default.createElement(DebugTab),
        }),
      }),
    );

    assert.ok(html.length > 0, 'rendered HTML must be non-empty');
    assert.ok(!html.includes('useSim must be used inside SimProvider'), 'DebugTab must render inside the provider');
    assert.ok(/gap-1 nit fixture: empty code/.test(html), 'the fixture error row must have rendered');
    // Scope the check to the SPECIFIC <li> row containing our fixture's own
    // message — a fresh error ring can contain OTHER rows first (e.g. this
    // very DebugTab render can itself trip a persistErrorRing failure under a
    // stubbed jsdom localStorage, which unshifts its own MET-V805 row ahead of
    // ours). A bare first-match regex over the whole page would grab that
    // unrelated row's err-code instead of the fixture's.
    const rowMatch = html.match(/<li[^>]*>((?:(?!<\/li>)[\s\S])*gap-1 nit fixture: empty code(?:(?!<\/li>)[\s\S])*)<\/li>/);
    assert.ok(rowMatch, 'the fixture row (<li> containing its own message) must be present in the markup');
    const errCodeMatch = rowMatch![1].match(/class="err-code"[^>]*>((?:(?!<\/strong>)[\s\S])*)<\/strong>/);
    assert.ok(errCodeMatch, 'the fixture row\'s err-code <strong> must be present');
    // React SSR inserts `<!-- -->` hydration-boundary comments around inline
    // expressions — strip them before comparing the visible text.
    const errCodeText = errCodeMatch![1].replace(/<!--\s*-->/g, '');
    assert.notEqual(
      errCodeText,
      '[]',
      "GAP-1 NIT: an empty code must fall back to the type, never render a blank '[]' in the code slot",
    );
    assert.equal(errCodeText, '[app]', 'the fallback must show the type ("app") in the code slot');
  } finally {
    dom.window.close();
  }
});
