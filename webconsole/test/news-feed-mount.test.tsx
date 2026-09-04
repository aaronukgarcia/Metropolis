// news-feed-mount.test.tsx — FEAT-2326609784 absorb proof.
//
// Renders the REAL MapView tree (not a mock) through a real reducer-driven
// city so the milestone/level-up/placeNotice fields carry real values, and
// asserts:
//   (a) the OLD stacked overlays (.levelup-banner / .milestone-banner /
//       .place-notice-banner, and their "Dismiss" buttons) are GONE from the
//       rendered markup — the class selectors from before FEAT-2326609784;
//   (b) the NEW .news-feed ticker IS present and shows the milestone text;
//   (c) a TRUE modal (InsolvencyPopup) still renders when its state is
//       active — proves the absorb did not accidentally sweep up a real
//       modal.
//
// SSR (renderToString) only, matching this repo's existing mount.test.tsx
// pattern — no jsdom/click simulation needed since NewsFeed's collapsed
// ticker already renders the latest entry's text without any interaction.

import { test } from 'node:test';
import assert from 'node:assert/strict';

function ensureMountWindow() {
  if (typeof globalThis.window === 'undefined') {
    globalThis.window = {
      localStorage: {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
        clear: () => {},
        key: () => null,
        length: 0,
      },
      performance: { now: () => 0 },
    } as any;
  }
}

async function renderMapViewWithState(state: any) {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');

  const ctx: any = {
    state,
    dispatch: () => {},
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => {},
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => true,
  };
  return renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value: ctx },
      React.default.createElement(BusyProvider, {
        children: React.default.createElement(MapView),
      })
    )
  );
}

test('ABSORB PROOF: a city with an active milestoneNotice renders the news feed, NOT the old milestone-banner overlay', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const state: any = {
    ...initialState(),
    milestoneNotice: { id: 'first-100k-pop', label: 'Metropolis', cash: 300000 },
  };
  const html = await renderMapViewWithState(state);

  assert.ok(html.length > 0, 'MapView must actually render');
  assert.ok(
    !/class="[^"]*milestone-banner/.test(html),
    'the OLD milestone-banner overlay class must not appear — it is retired'
  );
  assert.ok(
    !/class="[^"]*levelup-banner/.test(html),
    'the OLD levelup-banner overlay class must not appear — it is retired'
  );
  assert.ok(
    !/Milestone reached: Metropolis[\s\S]{0,80}Dismiss/.test(html),
    'the old dismissible banner markup (text immediately followed by a Dismiss button) must be gone'
  );
  assert.ok(/class="[^"]*news-feed[^"]*"/.test(html), 'the NEW .news-feed component must render');
  assert.ok(/Milestone reached: Metropolis/.test(html), 'the milestone text must surface via the feed instead');
  assert.ok(/£300,000/.test(html), 'the cash amount must still be shown');
});

test('ABSORB PROOF: an active level-up notice renders via the news feed, not the old levelup-banner', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const state: any = {
    ...initialState(),
    notice: { level: 4, cash: 15000, unlocked: ['Penthouse Tower', 'The Folkestone Eye'] },
  };
  const html = await renderMapViewWithState(state);

  assert.ok(!/class="[^"]*levelup-banner/.test(html), 'the OLD levelup-banner overlay class must not appear');
  assert.ok(/class="[^"]*news-feed[^"]*"/.test(html), 'the news feed must render');
  assert.ok(/Level 4 reached/.test(html), 'level-up text surfaces via the feed');
  assert.ok(/Penthouse Tower/.test(html) && /The Folkestone Eye/.test(html), 'the unlock names carry through');
});

test('ABSORB PROOF: an active placeNotice ("Fix All" summary) renders via the news feed, not the old place-notice-banner', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const state: any = {
    ...initialState(),
    placeNotice: 'Fix All: built 3 of 5 planned — click Fix All again for the rest',
  };
  const html = await renderMapViewWithState(state);

  assert.ok(!/class="[^"]*place-notice-banner/.test(html), 'the OLD place-notice-banner overlay class must not appear');
  assert.ok(/class="[^"]*news-feed[^"]*"/.test(html), 'the news feed must render');
  assert.ok(/Fix All: built 3 of 5 planned/.test(html), 'the Fix All summary text surfaces via the feed');
});

test('MODAL UNAFFECTED: InsolvencyPopup (a TRUE modal, not absorbed) still renders when active', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const state: any = {
    ...initialState(),
    insolvencyPopup: { enteredAt: 12 },
    insolvencyState: 'crisis',
  };
  const html = await renderMapViewWithState(state);

  assert.ok(/insolvency-popup-overlay/.test(html), 'the real modal overlay must still mount — absorb must not touch true modals');
  assert.ok(/BAILOUT: 1 Game-Year Intervention/.test(html), 'the modal content must still render');
});

test('a fresh, notice-free city shows the collapsed "No news yet." ticker (feed present but empty), no stacked banners', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const html = await renderMapViewWithState(initialState());

  assert.ok(/class="[^"]*news-feed[^"]*"/.test(html), 'the news feed component always mounts');
  assert.ok(/No news yet\./.test(html), 'an empty feed shows the empty-ticker copy, not nothing');
  assert.ok(!/class="[^"]*levelup-banner/.test(html));
  assert.ok(!/class="[^"]*milestone-banner/.test(html));
  assert.ok(!/class="[^"]*place-notice-banner/.test(html));
});
