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
