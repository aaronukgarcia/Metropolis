// bug499-queue-depth-overlap.test.tsx — BUG-499 (P2): the build/queue-depth
// indicator overdraws other HUD info.
//
// Aaron's dogfood report (A2Bev001.md, screenshot 2026-09-01 22:34): "a thin
// green vertical line on the far right lower half ... it's over the top of
// other information ... points to it being the queue but I don't understand
// it and it just crashes into the other information."
//
// Root cause: QueueDepthHud.tsx (FEAT-1972079938) rendered `.queue-depth-hud`
// with `position: fixed; bottom: 20px; right: 20px; z-index: 900`, anchoring
// it to the viewport's bottom-right corner. The .app grid tiles the ENTIRE
// viewport (see styles.css's `.app { grid-template-areas: "top top top"
// "left map right" "left bottom right"; }`), so the fixed corner the HUD
// claimed was always already occupied by the right-col fiscal panel
// (LeftDock) — the HUD painted directly over it, exactly the reported
// overdraw, and its only explanation (a `title` tooltip) required a
// mouseover to see, matching "I don't understand it".
//
// jsdom does not implement real layout/hit-testing (no elementFromPoint —
// same limitation documented in bug500-advisor-click-overlap.test.tsx), so
// this suite proves the fix the same way that file does: by parsing the
// shipped styles.css for the actual anchor declarations, PLUS a live mount
// proving the accessible label exists without a hover.
//
// RED PROOF: reverting `.queue-depth-hud` in styles.css to the pre-fix
// `position: fixed; bottom: 20px; right: 20px;` (scratch cp/mv, never a git
// revert — GR#24) turns test 1 red (fixed anchor reappears) and reverting
// QueueDepthHud.tsx's root div to drop `aria-label`/`role` (keeping only the
// old bare `title`) turns test 3 red (no accessible name without hover).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const stylesPath = path.resolve(here, '../src/styles.css');
const appPath = path.resolve(here, '../src/App.tsx');

function ruleBody(css: string, selector: string): string {
  const re = new RegExp(selector.replace(/[.]/g, '\\.') + '\\s*\\{([^}]*)\\}', 's');
  const m = css.match(re);
  assert.ok(m, `${selector} CSS rule must exist`);
  return m![1];
}

function decl(body: string, prop: string): string | null {
  const re = new RegExp('(?:^|[;{\\s])' + prop + '\\s*:\\s*([^;]+);');
  const m = body.match(re);
  return m ? m[1].trim() : null;
}

test('BUG-499: .queue-depth-hud must NOT be a fixed viewport-corner overlay any more', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const body = ruleBody(css, '.queue-depth-hud');
  const position = decl(body, 'position');
  assert.notEqual(
    position,
    'fixed',
    '.queue-depth-hud must not declare position:fixed — a fixed viewport-corner ' +
      'anchor is exactly the BUG-499 mechanism (it always lands on top of ' +
      'whatever the .app grid already placed at that corner)',
  );
  assert.equal(decl(body, 'z-index'), null, '.queue-depth-hud must not need an overlay z-index once it is laid out in-flow');
});

test('BUG-499: .queue-depth-hud does not share the bottom-right fixed anchor its neighbour panels occupy', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const hudBody = ruleBody(css, '.queue-depth-hud');
  // The neighbour it used to overdraw: the right-col fiscal panel, which owns
  // the entire "right" grid area (including its own lower half) per
  // `.app { grid-template-areas: ... "left map right" "left bottom right"; }`.
  // A fixed bottom:20px;right:20px anchor sits inside that grid area for any
  // viewport, which is the overlap. Assert the HUD no longer claims a fixed
  // bottom+right pixel anchor at all.
  const hudPos = { position: decl(hudBody, 'position'), bottom: decl(hudBody, 'bottom'), right: decl(hudBody, 'right') };
  const claimsFixedCorner = hudPos.position === 'fixed' && hudPos.bottom !== null && hudPos.right !== null;
  assert.ok(
    !claimsFixedCorner,
    `.queue-depth-hud must not claim a fixed (bottom, right) pixel anchor — found position:${hudPos.position} ` +
      `bottom:${hudPos.bottom} right:${hudPos.right}, which is the BUG-499 overlap`,
  );
});

test('BUG-499: the Queue Depth HUD is mounted inside .right-col (its own reserved flex slot), not as a bare App-level overlay', async () => {
  const app = await fs.readFile(appPath, 'utf-8');
  const rightColMatch = app.match(/<div className="col-wrap right-col">([\s\S]*?)<\/div>/);
  assert.ok(rightColMatch, '.right-col wrapper must exist in App.tsx');
  assert.ok(
    /<QueueDepthHud\s*\/>/.test(rightColMatch![1]),
    '<QueueDepthHud /> must be rendered INSIDE the .right-col wrapper so it is a normal flex sibling of ' +
      'DemandDock/LeftDock (a reserved slot in that column) instead of a free-floating overlay',
  );
});

// ---------------------------------------------------------------------------
// Live-mount companion: prove the HUD carries an accessible name reachable
// WITHOUT a hover (Aaron: "I don't understand it") and still renders driven
// by the real queueDepthTracker state (GR#15 — no hardcoded numbers).

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
    if (typeof (globalThis as any).ResizeObserver === 'undefined') {
      (globalThis as any).ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      };
    }
    return dom;
  });
}

test('BUG-499 companion: QueueDepthHud exposes an accessible label without a hover, and reflects real tracker state', async () => {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { QueueDepthHud } = await import('../src/components/right/QueueDepthHud.tsx');
    const { queueDepthTracker } = await import('../src/sim/queueDepth.ts');

    // Drive the tracker with REAL state before mounting — GR#15: the row
    // shown must come from queueDepthTracker, never a hardcoded literal.
    queueDepthTracker.resetAll();
    queueDepthTracker.increment('protocol');
    queueDepthTracker.increment('protocol');

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(QueueDepthHud));
    });

    const hud = container.querySelector('.queue-depth-hud');
    assert.ok(hud, 'the HUD root element must render');
    const ariaLabel = hud!.getAttribute('aria-label');
    assert.ok(
      ariaLabel && ariaLabel.trim().length > 0,
      'the HUD must carry a non-empty aria-label — an accessible name reachable without hovering the title tooltip',
    );
    assert.match(ariaLabel!, /queue/i, 'the accessible label must actually name the queue, not a generic string');

    const depthEl = hud!.querySelector('[data-engine="protocol"] .qd-depth');
    assert.ok(depthEl, 'the protocol row must render');
    assert.equal(depthEl!.textContent, '2', 'the depth shown must come from the real tracker state, not a hardcoded value');

    await act(async () => {
      root.unmount();
    });
    queueDepthTracker.resetAll();
  } finally {
    dom.window.close();
  }
});
