// bug501-second-bailout-banner.test.tsx — BUG-501 (P2): second-bailout
// double/contradictory banner.
//
// During the auto-triggered SECOND IMF bailout, exposed insolvencyState ===
// 'bailout_second' (engine.ts). Before this fix, InsolvencyBanner
// (MapView.tsx) only returned null for 'solvent'/'administration' — for
// 'bailout_second' it fell through to the generic crisis-check, which is
// FALSE for the literal string 'bailout_second', so it rendered the
// 'warning' variant text ("funds are approaching the insolvency threshold
// ... before the IMF steps in") — which is FALSE: the IMF is already two
// bailouts deep. SecondBailoutBanner renders at the SAME TIME, at the SAME
// anchor (.insolvency-banner, top:8px right:8px, z-index:18), so the player
// saw two contradictory banners stacked on top of each other.
//
// This test renders the REAL MapView component (via renderToString, same
// pattern as mount.test.tsx's renderMapView helper) with a hand-built state
// exposing insolvencyState === 'bailout_second', and asserts:
//   (a) exactly ONE '.insolvency-banner' element mounts;
//   (b) its text is NOT the approaching-threshold warning copy;
//   (c) it IS the dedicated second-bailout copy.
//
// RED PROOF: before the MapView.tsx fix (InsolvencyBanner's early-return only
// covering 'solvent'/'administration'), this state renders TWO
// '.insolvency-banner' elements (InsolvencyBanner's stale warning variant +
// SecondBailoutBanner) — assertion (a) fails, and assertion (b) also fails
// since the warning text IS present. Verified by a scratch copy of
// MapView.tsx with the old (pre-fix) guard restored and re-running this file
// (GR#24 — no git revert used to prove it).

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

async function renderMapView(state: any) {
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

// Hand-built fixture: a city sitting in the exposed 'bailout_second' overlay
// (see engine.ts's exposedInsolvencyState precedence: decline > administration
// > bailout_second > raw funds band). bailoutState is null (the first bailout
// has ended) and bailoutSecondState carries the entry tick, mirroring what
// engine.ts's reducer actually produces on auto-trigger (see
// imf-insolvency-inc4.test.mjs's rideFirstBailoutToSecond()).
async function secondBailoutState() {
  const { initialState } = await import('../src/sim/engine.ts');
  const s = initialState();
  return {
    ...s,
    insolvencyState: 'bailout_second' as const,
    insolvencyRawBand: 'crisis' as const,
    bailoutState: null,
    administrationState: null,
    declineState: null,
    bailoutSecondState: { state: 'crisis' as const, enteredAt: s.tick },
  };
}

test('BUG-501 precondition: the fixture actually exposes bailout_second with no administration/decline overlay racing it', async () => {
  const s: any = await secondBailoutState();
  assert.equal(s.insolvencyState, 'bailout_second');
  assert.equal(s.administrationState, null);
  assert.equal(s.declineState, null);
  assert.ok(s.bailoutSecondState, 'bailoutSecondState must be set for SecondBailoutBanner to mount at all');
});

test('BUG-501: exactly ONE insolvency-banner mounts during the second bailout', async () => {
  const s = await secondBailoutState();
  const html = await renderMapView(s);
  const matches = html.match(/class="insolvency-banner[^"]*"/g) ?? [];
  assert.equal(
    matches.length,
    1,
    `expected exactly one .insolvency-banner element, found ${matches.length}: ${JSON.stringify(matches)} — ` +
      'InsolvencyBanner must return null once the bailout_second overlay is active, leaving ' +
      'SecondBailoutBanner as the sole banner.',
  );
});

test('BUG-501: the single banner is NOT the false "approaching insolvency threshold" warning copy', async () => {
  const s = await secondBailoutState();
  const html = await renderMapView(s);
  assert.ok(
    !/approaching the insolvency threshold/.test(html),
    'the second-bailout state must never render the pre-crisis warning copy — the IMF is already ' +
      'two bailouts deep, so "before the IMF steps in" is false at this point',
  );
});

test('BUG-501: the single banner IS the dedicated SECOND IMF BAILOUT copy', async () => {
  const s = await secondBailoutState();
  const html = await renderMapView(s);
  assert.ok(
    /SECOND IMF BAILOUT/.test(html),
    'SecondBailoutBanner\'s dedicated copy must be the one and only banner shown',
  );
});

// MUTATION-PROVE target: a state that has NOT reached bailout_second must not
// trip the same assertions vacuously — the ordinary 'crisis' (first bailout)
// band still renders exactly one banner (InsolvencyBanner's crisis copy,
// which doubles as the first-bailout banner — there is no separate
// first-bailout banner component).
test('BUG-501 MUTATION-PROVE target: plain crisis (first bailout) band also renders exactly one banner', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const s: any = {
    ...initialState(),
    insolvencyState: 'crisis',
    insolvencyRawBand: 'crisis',
    bailoutState: { enteredAt: 0 },
    administrationState: null,
    bailoutSecondState: null,
    declineState: null,
  };
  const html = await renderMapView(s);
  const matches = html.match(/class="insolvency-banner[^"]*"/g) ?? [];
  assert.equal(matches.length, 1, `expected exactly one banner during the plain first bailout, found ${matches.length}`);
  assert.ok(/BAILOUT: You have 1 year/.test(html), 'the first-bailout crisis copy must still render');
});

// Also cover 'administration' and 'decline' — both already had (or now also
// get) a dedicated overlay, and InsolvencyBanner must stay silent for them too.
test('BUG-501 companion: administration overlay renders no insolvency-banner-warning-style duplicate', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const s: any = {
    ...initialState(),
    insolvencyState: 'administration',
    insolvencyRawBand: 'crisis',
    bailoutState: null,
    bailoutSecondState: null,
    declineState: null,
    administrationState: { enteredAt: 0 },
  };
  const html = await renderMapView(s);
  assert.ok(!/approaching the insolvency threshold/.test(html), 'no stale warning copy during administration');
  const matches = html.match(/class="insolvency-banner[^"]*"/g) ?? [];
  assert.equal(matches.length, 1, `expected exactly one banner (the administration banner), found ${matches.length}`);
});
