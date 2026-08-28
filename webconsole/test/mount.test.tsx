// mount.test.tsx — smoke test for React mount/provider correctness
//
// BUG-412: SimProvider boot-restore regression (null-context first render).
// The webconsole test suite is node --test on pure functions and never mounts React,
// so a provider/mount regression is invisible. This test catches the blank-screen
// class by rendering the provider tree and asserting:
//   (a) renderToString does NOT throw "useSim must be used inside SimProvider";
//   (b) the output is a non-empty string containing the consumer's output.
//
// This test ALWAYS EXECUTES its assertions — it never skips (an earlier version
// silently skipped when import.meta.env was absent under tsx, a false-green the
// BUG-412 round caught). store.tsx now reads import.meta.env?.DEV (optional
// chaining), so the real render path runs under a bare Node/tsx runtime.

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('SimProvider mount: renderToString does not throw and produces consumer output', async () => {
  // SSR needs a minimal window (localStorage for boot-restore, performance for ticks).
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');

  // No try/skip: if store.tsx cannot be imported or rendered, the test FAILS.
  // That is the whole point — a mount regression must turn this red.
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');

  // Minimal consumer: if SimProvider is broken (null-context first render),
  // useSim throws "useSim must be used inside SimProvider" during render here.
  function MinimalConsumer() {
    const { state } = useSim();
    return React.default.createElement('div', null, `tick: ${state.tick}`);
  }

  const html = renderToString(
    React.default.createElement(SimProvider, {
      children: React.default.createElement(MinimalConsumer),
    })
  );

  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(html.includes('tick:'), 'rendered HTML must contain the consumer output');
});

// BUG-421: Aaron debug (5).json captured a 60ms burst of
//   "useSim must be used inside SimProvider" /
//   "render crash: useSim must be used inside SimProvider"
// (window.onerror + ErrorBoundary.componentDidCatch). BUG-412's test only
// mounts MinimalConsumer inside SimProvider, so a sibling of SimProvider that
// called useSim was invisible. App used to render VersionUpgradeToast outside
// SimProvider (same ErrorBoundary). This file always executes — no skip.

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

// FEAT-1972079906 inc3 (FEAT-1972079864): the Waste RightDock panel. WasteTab is
// pure JSX over wasteDisplayModel (a display-only reshaping of the landed waste
// reads). This smoke test renders the REAL component + useSim hook inside the
// real SimProvider and asserts it does not throw and leaks no NaN/Infinity — the
// zero-waste / generation NUMERIC sensibility is unit-tested against
// wasteDisplayModel in test/waste-panel-inc3.test.mjs. It always executes.
test('WasteTab smoke: renders inside SimProvider without throwing and leaks no NaN', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { WasteTab } = await import('../src/components/right/RightDock.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, {
      children: React.default.createElement(WasteTab),
    })
  );
  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(
    !html.includes('useSim must be used inside SimProvider'),
    'WasteTab must render inside the provider without a context error'
  );
  assert.ok(/Diversion rate/.test(html), 'the Waste panel content rendered');
  assert.ok(!/NaN|Infinity/.test(html), 'no NaN/Infinity in the rendered panel');
});

test('BUG-421 RED-prove: useSim in the VersionUpgradeToast slot (outside SimProvider) throws', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');

  function OutsideToastStandIn() {
    useSim();
    return React.default.createElement('div', null, 'toast-slot');
  }

  // Mirrors pre-fix App.tsx: ErrorBoundary > BusyProvider > SimProvider, with
  // the toast as an ErrorBoundary child OUTSIDE SimProvider. SSR does not
  // swallow via ErrorBoundary — renderToString must throw. If this assertion
  // ever stops throwing, the detector is dead and BUG-421 is invisible again.
  assert.throws(
    () =>
      renderToString(
        React.default.createElement(
          ErrorBoundary,
          null,
          React.default.createElement(BusyProvider, {
            children: React.default.createElement(SimProvider, {
              children: React.default.createElement('div', null, 'inside'),
            }),
          }),
          React.default.createElement(OutsideToastStandIn)
        )
      ),
    (err: unknown) => {
      const msg = err instanceof Error ? err.message : String(err);
      assert.match(msg, /useSim must be used inside SimProvider/);
      return true;
    }
  );
});

test('BUG-421: App tree (ErrorBoundary + VersionUpgradeToast as App does) does not throw useSim', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { VersionUpgradeToast } = await import('../src/sim/liveVersion.tsx');

  function MinimalConsumer() {
    const { state } = useSim();
    return React.default.createElement('div', null, `tick: ${state.tick}`);
  }

  // Same nesting as App.tsx after the fix: toast is inside SimProvider (so a
  // useSim call there cannot throw) and outside the inner ErrorBoundary (so a
  // sim-tree crash does not unmount it).
  const html = renderToString(
    React.default.createElement(
      ErrorBoundary,
      null,
      React.default.createElement(BusyProvider, {
        children: React.default.createElement(SimProvider, {
          children: [
            React.default.createElement(ErrorBoundary, {
              key: 'sim',
              children: React.default.createElement(MinimalConsumer),
            }),
            React.default.createElement(VersionUpgradeToast, { key: 'toast' }),
          ],
        }),
      })
    )
  );

  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(html.includes('tick:'), 'rendered HTML must contain the consumer output');
  assert.ok(
    !html.includes('useSim must be used inside SimProvider'),
    'App tree must not surface a useSim provider error'
  );
});

// FEAT-1972079860 AC-4: Locked spec buttons must be clickable (disabled=false)
// to open the requirements card. This test catches the regression where locked
// specs were disabled, blocking their onClick handler.
//
// CRITICAL: This test must FAIL RED if locked is re-added to the disabled expression.
// It specifically parses the HTML to find locked and placeholder buttons by spec
// name and asserts disabled attribute presence/absence per button type.
test('FEAT-1972079860 AC-4: locked-spec buttons NOT disabled (clickable), placeholder buttons ARE disabled', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BottomBar } = await import('../src/components/bottom/BottomBar.tsx');

  // Render BottomBar inside SimProvider (required for useSim context).
  // At level 1, we know:
  // - res_hut (unlock 1) is AVAILABLE
  // - pow_coal (unlock 3) is LOCKED
  // - rail_branch is a PLACEHOLDER
  const html = renderToString(
    React.default.createElement(SimProvider, {
      children: React.default.createElement(BottomBar),
    })
  );

  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');

  // CRITICAL: Test the key regression — the disabled attribute logic.
  // The test specifically checks that:
  // 1. Locked buttons have aria-label="Locked — unlocks at city level X" (accessibility)
  // 2. Placeholder buttons are disabled
  // 3. Locked buttons do NOT appear with disabled attribute before their closing >
  //
  // If someone re-adds locked to the disabled expression (like disabled={isPh || locked || ...}),
  // the pattern in assertion #3 will fail to match.

  // Assertion 1: At least one locked button has aria-label with "Locked"
  const hasAriaLabelLocked = /aria-label="[^"]*Locked[^"]*"/.test(html);
  assert.ok(
    hasAriaLabelLocked,
    'BottomBar must render at least one locked button with aria-label containing "Locked"'
  );

  // Assertion 2: At least one button has locked CSS class
  const hasLockedClass = /class="[^"]*locked[^"]*"/.test(html);
  assert.ok(
    hasLockedClass,
    'BottomBar must render at least one locked button with pal-item locked CSS class'
  );

  // Assertion 3: CRITICAL - Locked buttons must NOT be disabled.
  // Strategy: Find button tags that contain BOTH:
  // - aria-label with "Locked"
  // - class with "locked"
  // Then verify the button tag does NOT contain "disabled" attribute.
  //
  // Split into two checks: (1) find buttons with locked class and aria-label="Locked",
  // (2) verify they don't have disabled.
  //
  // If locked is added back to disabled expression, this will fail.

  // Extract all button tags and check each one
  const buttons = html.match(/<button[^>]*>/g) || [];
  let foundLockedNotDisabled = false;
  for (const button of buttons) {
    const hasLockedLabel = /aria-label="[^"]*Locked[^"]*"/.test(button);
    const hasLockedClass = /class="[^"]*locked[^"]*"/.test(button);
    const hasDisabled = /\bdisabled\b/.test(button);

    if (hasLockedLabel && hasLockedClass && !hasDisabled) {
      foundLockedNotDisabled = true;
      break;
    }
  }

  assert.ok(
    foundLockedNotDisabled,
    'CRITICAL: locked buttons (with aria-label="Locked" and locked class) must NOT have disabled attribute. ' +
      'If this fails, locked was re-added to the disabled expression in BottomBar.tsx line 86.'
  );

  // Assertion 4: Placeholder buttons must be disabled
  const placeholderDisabledRegex = /class="[^"]*placeholder[^"]*"[^/]*disabled/;
  const placeholderDisabled = placeholderDisabledRegex.test(html);
  assert.ok(
    placeholderDisabled,
    'placeholder buttons (with pal-item placeholder class) must have disabled attribute'
  );

  // Assertion 5: The HTML should contain Build tab labels (smoke test)
  assert.ok(
    html.includes('Build'),
    'BottomBar must render successfully and contain Build tab'
  );
});
