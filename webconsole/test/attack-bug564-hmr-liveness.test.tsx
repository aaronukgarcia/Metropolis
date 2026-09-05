// attack-bug564-hmr-liveness.test.tsx — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against the BUG-564 estate (staleBuildGuard.resolveStaleBuild +
// hmrLiveness.ts + the re-mounted StaleBuildBanner).
//
// Why these tests exist (the gap the author's 7 tests left):
// hmrLiveness.ts shipped with ZERO tests. It is the single highest-risk file
// in the estate, because it reads `import.meta.hot` directly (no injectable
// seam) and hard-codes two Vite event-name STRINGS. If either string is
// wrong — a typo, or a rename in a future Vite major — the disconnect
// listener never fires, `connected` stays true forever, and
// resolveStaleBuild's dev branch then returns stale:false FOREVER. That is a
// PERMANENT SILENT FALSE NEGATIVE: Aaron dogfoods a dead dev server with no
// banner at all, which is the expensive failure this whole guard exists to
// prevent. A hand-mocked `hot` object cannot catch it (the mock would use the
// same typo'd name), so the check below derives the valid event names from
// the INSTALLED vite client bundle instead (GR#15: validators derive from
// data, never from hardcoded constants).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import React from 'react';
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { JSDOM } from 'jsdom';

import { checkStaleBuild, resolveStaleBuild } from '../src/sim/staleBuildGuard';
import { hasHmr, subscribeHmrConnection } from '../src/sim/hmrLiveness';

const require_ = createRequire(import.meta.url);

/** Every `vite:*` custom event the INSTALLED vite client actually emits,
 *  scraped from vite's own shipped browser bundle. Derived, never hardcoded —
 *  a Vite upgrade that renames or drops an event makes the assertions below
 *  red instead of silently disarming the guard. */
function viteEmittedEventNames(): Set<string> {
  const clientPath = require_.resolve('vite/dist/client/client.mjs');
  const src = readFileSync(clientPath, 'utf8');
  const found = src.match(/vite:[a-zA-Z:]+/g) ?? [];
  return new Set(found);
}

/** The `vite:*` event-name string literals hmrLiveness.ts actually subscribes
 *  to, scraped from the source so a future edit is covered automatically. */
function hmrLivenessSubscribedEventNames(): string[] {
  const src = readFileSync(new URL('../src/sim/hmrLiveness.ts', import.meta.url), 'utf8');
  // Only the hot.on(...) call sites — not the prose in the header comment.
  const calls = src.match(/hot\.(?:on|off)\(\s*'([^']+)'/g) ?? [];
  return [...new Set(calls.map((c) => c.replace(/^hot\.(?:on|off)\(\s*'/, '').replace(/'$/, '')))];
}

test('ATTACK A1: every vite event name hmrLiveness subscribes to is REALLY emitted by the installed vite client', () => {
  const emitted = viteEmittedEventNames();
  const subscribed = hmrLivenessSubscribedEventNames();

  assert.ok(
    emitted.size > 0,
    'sanity: could not scrape any vite:* event names from vite/dist/client/client.mjs — the derivation itself is broken, do not trust a pass',
  );
  assert.ok(
    subscribed.length > 0,
    'sanity: hmrLiveness.ts has no hot.on(\'vite:...\') call sites — the liveness wiring has been removed, which permanently pins connected=true',
  );

  for (const name of subscribed) {
    assert.ok(
      emitted.has(name),
      `hmrLiveness.ts subscribes to '${name}', which the installed vite client NEVER emits. ` +
        `A listener on a non-existent event never fires => connected stays true forever => ` +
        `resolveStaleBuild's dev branch returns stale:false forever (permanent silent false negative). ` +
        `vite actually emits: ${[...emitted].sort().join(', ')}`,
    );
  }
});

test('ATTACK A2: BOTH halves of the liveness signal are wired — losing the disconnect listener is the false-negative mode, losing connect is the stuck-banner mode', () => {
  const subscribed = hmrLivenessSubscribedEventNames();
  assert.ok(
    subscribed.includes('vite:ws:disconnect'),
    'the disconnect listener is the ONLY thing that can ever flip hmrConnected to false; without it the dev banner can never fire',
  );
  assert.ok(
    subscribed.includes('vite:ws:connect'),
    'the connect listener is what clears the banner once the server is back',
  );
});

test('ATTACK A3: vite exposes hot.off — the unsubscribe path is not silently a no-op leak', () => {
  // vite's package.json `exports` does not expose ./types/*, so resolve the
  // package root (which IS exported) and walk to the type declaration.
  const vitePkg = require_.resolve('vite/package.json');
  const typesPath = new URL('./types/hot.d.ts', new URL(`file:///${vitePkg.replace(/\\/g, '/')}`));
  const src = readFileSync(typesPath, 'utf8');
  assert.match(
    src,
    /\boff\s*</,
    'ViteHotContext no longer declares off(); hmrLiveness\'s unsubscribe would degrade to a permanent listener leak on every remount',
  );
});

// ---------------------------------------------------------------------------
// PROD-SHAPED IMPORT: under tsx/node there is no import.meta.hot, exactly like
// a production bundle. The module must import, evaluate and subscribe without
// throwing, and must NOT report a disconnect that would fire the banner.

test('ATTACK B1: prod-shaped runtime (no import.meta.hot) — hasHmr() is false and nothing throws at import time', () => {
  assert.equal(hasHmr(), false, 'no vite HMR client present under node/tsx, so hasHmr must be false');
});

test('ATTACK B2: prod-shaped runtime — subscribe reports connected=true EXACTLY ONCE and returns a callable no-op unsubscribe', () => {
  const seen: boolean[] = [];
  const unsub = subscribeHmrConnection((c) => seen.push(c));
  assert.deepEqual(seen, [true], 'must report true once and never again with no HMR client');
  assert.equal(typeof unsub, 'function');
  assert.doesNotThrow(() => unsub(), 'unsubscribe must be safe to call with no hot object');
  assert.doesNotThrow(() => unsub(), 'unsubscribe must be idempotent');
  assert.deepEqual(seen, [true], 'unsubscribing must not emit anything further');
});

test('ATTACK B3: prod-shaped runtime — a connected=true report can NEVER suppress the PROD staleness banner (gate ORDER proof)', () => {
  // The danger: hmrLiveness reports true in prod, and the component feeds that
  // straight into resolveStaleBuild. If the dev gate were ordered wrongly
  // (checking hmrConnected before isDevServer) the production banner would be
  // silently disabled. Prove isDevServer dominates on the exact value
  // hmrLiveness produces in prod.
  let reported: boolean | null = null;
  subscribeHmrConnection((c) => {
    reported = c;
  })();
  assert.equal(reported, true, 'precondition: prod hmrLiveness reports true');

  const r = resolveStaleBuild({
    runningSha: 'aaaaaaa',
    diskSha: 'bbbbbbb',
    isDevServer: false,
    hmrConnected: reported as unknown as boolean,
  });
  assert.equal(r.stale, true, 'prod staleness MUST still fire even though hmrLiveness said connected=true');
});

test('ATTACK B4: prod byte-identity across a full input matrix — resolveStaleBuild(isDevServer:false) is deepEqual checkStaleBuild for every shape, both hmrConnected values', () => {
  const shas: (string | null | undefined)[] = [
    'abc1234',
    'def5678',
    'dev',
    'unknown',
    '',
    null,
    undefined,
  ];
  let compared = 0;
  for (const running of shas) {
    for (const disk of shas) {
      const expected = checkStaleBuild(running, disk);
      for (const hmrConnected of [true, false]) {
        const actual = resolveStaleBuild({ runningSha: running, diskSha: disk, isDevServer: false, hmrConnected });
        assert.deepEqual(
          actual,
          expected,
          `prod path diverged for running=${String(running)} disk=${String(disk)} hmrConnected=${hmrConnected}`,
        );
        compared++;
      }
    }
  }
  assert.equal(compared, shas.length * shas.length * 2, 'sanity: the matrix actually ran');
});

test('ATTACK B5: dev + HMR DOWN is byte-identical to the prod path too — the dead-server fallback loses no detection', () => {
  const shas: (string | null | undefined)[] = ['abc1234', 'def5678', 'dev', 'unknown', '', null, undefined];
  for (const running of shas) {
    for (const disk of shas) {
      assert.deepEqual(
        resolveStaleBuild({ runningSha: running, diskSha: disk, isDevServer: true, hmrConnected: false }),
        checkStaleBuild(running, disk),
        `dead-server dev fallback diverged for running=${String(running)} disk=${String(disk)}`,
      );
    }
  }
});

test('ATTACK B6: the dev-quiet branch reports the SAME sha fields as the comparison path — no field drift between the two return sites', () => {
  // The dev branch hand-builds its result object rather than reusing
  // checkStaleBuild's. Prove the non-`stale` fields still agree, or a reader
  // of runningSha/diskSha (the banner text) would show different values
  // depending on which branch ran.
  const shas: (string | null | undefined)[] = ['abc1234', 'dev', '', null, undefined];
  for (const running of shas) {
    for (const disk of shas) {
      const quiet = resolveStaleBuild({ runningSha: running, diskSha: disk, isDevServer: true, hmrConnected: true });
      const compared = checkStaleBuild(running, disk);
      assert.equal(quiet.stale, false, 'dev + HMR up must always be quiet');
      assert.equal(quiet.runningSha, compared.runningSha, `runningSha drift for ${String(running)}`);
      assert.equal(quiet.diskSha, compared.diskSha, `diskSha drift for ${String(disk)}`);
    }
  }
});

// ---------------------------------------------------------------------------
// STRICTMODE: src/main.tsx mounts the app inside <React.StrictMode>, which
// double-invokes every effect (mount -> cleanup -> mount). The banner's
// subscription effect must survive that without leaking, double-firing into a
// render loop, or crashing.

test('ATTACK C1: StrictMode double-invoke of the banner container — mounts clean, subscribes/unsubscribes without throwing, renders nothing when the poll fails', async () => {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost:5173/',
  });
  const g = globalThis as any;
  const prev = {
    window: g.window,
    document: g.document,
    fetch: g.fetch,
    IS_REACT_ACT_ENVIRONMENT: g.IS_REACT_ACT_ENVIRONMENT,
  };
  g.window = dom.window;
  g.document = dom.window.document;
  // globalThis.navigator is a getter-only own property in modern node — the
  // same defineProperty idiom the existing suite uses (stale-build-guard.test.tsx).
  Object.defineProperty(g, 'navigator', {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
  g.HTMLElement = dom.window.HTMLElement;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  // Production/no-dev-server shape: the poll is unreachable. Fail-silent
  // contract says this must render NOTHING, never a false banner.
  let fetchCalls = 0;
  g.fetch = async () => {
    fetchCalls++;
    throw new Error('ECONNREFUSED (no dev server)');
  };

  try {
    const { StaleBuildBanner } = await import('../src/components/StaleBuildBanner');
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(React.createElement(React.StrictMode, null, React.createElement(StaleBuildBanner)));
    });
    await act(async () => {
      await Promise.resolve();
    });

    assert.equal(
      container.querySelector('.stale-build-banner'),
      null,
      'fail-silent contract: an unreachable /version.json must never render a banner',
    );
    assert.ok(fetchCalls >= 1, 'sanity: the poll effect actually ran');

    // Unmount must not throw (the hmr unsubscribe runs here).
    await act(async () => {
      root.unmount();
    });
    assert.equal(container.innerHTML, '', 'unmount left DOM behind');
  } finally {
    g.window = prev.window;
    g.document = prev.document;
    g.fetch = prev.fetch;
    g.IS_REACT_ACT_ENVIRONMENT = prev.IS_REACT_ACT_ENVIRONMENT;
    dom.window.close();
  }
});

test('ATTACK C2: repeated subscribe/unsubscribe cycles (StrictMode remounts) never emit a spurious DISCONNECT', () => {
  // A remount must not manufacture a stale verdict out of nothing. Every
  // cycle must report connected=true (the safe/quiet value) and nothing else.
  for (let i = 0; i < 5; i++) {
    const seen: boolean[] = [];
    const unsub = subscribeHmrConnection((c) => seen.push(c));
    unsub();
    assert.deepEqual(seen, [true], `cycle ${i} emitted ${JSON.stringify(seen)} instead of exactly [true]`);
  }
});
