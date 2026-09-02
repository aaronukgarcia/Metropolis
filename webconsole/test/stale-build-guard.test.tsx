// stale-build-guard.test.tsx — FEAT-2326609725 dev-server staleness guard.
//
// Real incident (2026-09-02): a long-lived vite dev server kept serving an
// OLD module graph 45 commits behind disk. The existing version badge and
// /version.json BOTH looked current (they track a value the hot-upgrade path
// deliberately advances live), so neither caught the drift. This guard
// compares a build-time-FROZEN sha (src/generated/version.ts's
// APP_VERSION_SHA) against a live-polled server sha and warns loudly on a
// mismatch — see src/sim/staleBuildGuard.ts and
// src/components/StaleBuildBanner.tsx.
//
// Coverage:
//   (a) matching shas -> no banner
//   (b) differing shas -> banner shown, with BOTH shas visible in the text
//   (c) unreachable/'dev' sha on either side -> no banner (fail-silent contract)
// Exercises the pure comparison (checkStaleBuild) directly AND the
// presentational component (StaleBuildBannerView) via renderToString, so a
// regression in either the logic or the render wiring turns this red.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkStaleBuild } from '../src/sim/staleBuildGuard';

test('checkStaleBuild: matching shas are NOT stale', () => {
  const r = checkStaleBuild('abc1234', 'abc1234');
  assert.equal(r.stale, false);
});

test('checkStaleBuild: differing shas ARE stale, and both shas are reported', () => {
  const r = checkStaleBuild('abc1234', 'def5678');
  assert.equal(r.stale, true);
  assert.equal(r.runningSha, 'abc1234');
  assert.equal(r.diskSha, 'def5678');
});

test('checkStaleBuild: unreachable poll (null diskSha) is NOT stale', () => {
  const r = checkStaleBuild('abc1234', null);
  assert.equal(r.stale, false);
});

test("checkStaleBuild: 'dev' on the running side is NOT stale (can't compare)", () => {
  const r = checkStaleBuild('dev', 'def5678');
  assert.equal(r.stale, false);
});

test("checkStaleBuild: 'dev' on the disk side is NOT stale (can't compare)", () => {
  const r = checkStaleBuild('abc1234', 'dev');
  assert.equal(r.stale, false);
});

test("checkStaleBuild: 'unknown' disk sha (git describe failed) is NOT stale", () => {
  const r = checkStaleBuild('abc1234', 'unknown');
  assert.equal(r.stale, false);
});

test('checkStaleBuild: empty-string shas are NOT stale', () => {
  assert.equal(checkStaleBuild('', 'abc1234').stale, false);
  assert.equal(checkStaleBuild('abc1234', '').stale, false);
});

// --- Component-level: the presentational view renders (or doesn't) from the
// same comparison result a real poll would produce. ---

test('StaleBuildBannerView: renders nothing when shas match', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner');

  const result = checkStaleBuild('abc1234', 'abc1234');
  const html = renderToString(React.default.createElement(StaleBuildBannerView, { result }));
  assert.equal(html, '', 'no banner markup when the build is current');
});

test('StaleBuildBannerView: renders a visible banner with BOTH shas when they differ', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner');

  const result = checkStaleBuild('abc1234', 'def5678');
  const html = renderToString(React.default.createElement(StaleBuildBannerView, { result }));
  assert.ok(html.includes('STALE BUILD'), 'banner text must be present');
  assert.ok(html.includes('abc1234'), 'the RUNNING sha must be shown');
  assert.ok(html.includes('def5678'), 'the DISK sha must be shown');
  assert.ok(html.includes('role="alert"'), 'must be announced as an alert (accessibility)');
});

test("StaleBuildBannerView: renders nothing for an unreachable poll ('dev'/null)", async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner');

  const unreachable = checkStaleBuild('abc1234', null);
  const html1 = renderToString(React.default.createElement(StaleBuildBannerView, { result: unreachable }));
  assert.equal(html1, '', 'no banner when the endpoint has not answered yet');

  const devBuild = checkStaleBuild('dev', 'def5678');
  const html2 = renderToString(React.default.createElement(StaleBuildBannerView, { result: devBuild }));
  assert.equal(html2, '', "no banner when the running build is the 'dev' fallback");
});

// ---------------------------------------------------------------------------
// BUG (staleness banner UX, refines FEAT-2326609725 — 2026-09-02, folded into
// the FEAT-2326609720 inc1 overlay-discipline pass): the original copy told
// every reader to "restart the dev server (Ctrl+C then npm run dev)" — wrong
// for the common case (server live and current, only the loaded PAGE is
// stale) and dev jargon a player cannot act on. The fix is copy/affordance
// only (checkStaleBuild itself is untouched — see the comparison cases
// above, still green): a player-safe headline + one-click Reload button
// (location.reload()) and a dismiss (×) that hides the banner for the
// session. These two tests mount the REAL component with react-dom/client +
// jsdom (renderToString cannot exercise onClick) and drive real clicks.
//
// RED PROOF: reverting StaleBuildBannerView to the pre-fix single <div> (no
// buttons at all) makes `container.querySelector('.stale-build-banner-reload')`
// and `.stale-build-banner-dismiss` both null, failing the precondition
// assertions in both tests below before the click-behaviour assertions are
// even reached — verified against a scratch copy of the pre-fix component
// (GR#24 — no git revert used).

function installJsdom() {
  return import('jsdom').then(({ JSDOM }) => {
    const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
      url: 'http://localhost/',
      pretendToBeVisual: true,
    });
    const { window } = dom;
    (globalThis as any).window = window;
    (globalThis as any).document = window.document;
    Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
    (globalThis as any).HTMLElement = window.HTMLElement;
    (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
    (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
    (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
    return dom;
  });
}

test('StaleBuildBannerView: clicking Reload fires the reload callback exactly once', async () => {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner');

    let reloadCalls = 0;
    // jsdom's `location.reload` is a non-configurable, non-writable OWN
    // property with no prototype indirection to stub (verified directly:
    // Object.getOwnPropertyDescriptor(window.location, 'reload').configurable
    // === false) — so the component accepts an injectable `onReload` prop
    // (defaulting to a real window.location.reload() in production) and this
    // test verifies the button's click wiring through that seam instead.
    const result = checkStaleBuild('abc1234', 'def5678');
    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(
        React.default.createElement(StaleBuildBannerView, {
          result,
          onReload: () => {
            reloadCalls++;
          },
        }),
      );
    });

    const reloadBtn = container.querySelector('.stale-build-banner-reload');
    assert.ok(reloadBtn, 'precondition: the Reload button must be rendered while stale');

    await act(async () => {
      reloadBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });

    assert.equal(reloadCalls, 1, 'clicking Reload must fire the reload callback exactly once');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('StaleBuildBannerView: clicking dismiss (×) hides the banner for this session', async () => {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { StaleBuildBannerView } = await import('../src/components/StaleBuildBanner');

    const result = checkStaleBuild('abc1234', 'def5678');
    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(StaleBuildBannerView, { result }));
    });

    assert.ok(container.querySelector('.stale-build-banner'), 'precondition: banner must be visible while stale and not yet dismissed');
    const dismissBtn = container.querySelector('.stale-build-banner-dismiss');
    assert.ok(dismissBtn, 'precondition: the dismiss (×) button must be rendered');

    await act(async () => {
      dismissBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });

    assert.equal(
      container.querySelector('.stale-build-banner'),
      null,
      'the banner must no longer render after dismiss is clicked',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
