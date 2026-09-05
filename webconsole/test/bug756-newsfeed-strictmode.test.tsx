// bug756-newsfeed-strictmode.test.tsx — BUG-756 (P1): under a REAL
// React.StrictMode mount (createRoot + <React.StrictMode> + act), NewsFeed
// used to render ZERO entries for EVERY source — a render-phase derivation
// (setRing called directly in the component body) that an independent round
// proved dead under StrictMode's dev-only double invocation, while the exact
// same harness with StrictMode off rendered correctly. The only prior
// "StrictMode proof" on file (attack-news-feed-round.test.tsx) was a
// hand-simulated model whose own comment admits SSR does not double-invoke —
// it never mounted a real component tree, so it never could have caught
// this. This file drives a REAL mount for all four absorbed sources
// (levelup notice, milestone notice, placeNotice, and the
// consolidatorCapacityUnknown outbox source) through the real SimContext
// provider, both WITH and WITHOUT StrictMode, and asserts each source
// renders EXACTLY ONCE (no zero, no double) in both configurations.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
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
}

const skipPass = (id: number) => ({
  id,
  tick: id * 10,
  transactions: [],
  skipped: [{ sectionKey: 7, reason: 'capacity unknown' }],
});

/**
 * Mounts NewsFeed (via the real SimContext provider, exactly as MapView
 * wires it) with all four absorbed sources simultaneously active, either
 * wrapped in <React.StrictMode> or not, and returns the rendered entry
 * texts (newest-first, matching the ring) plus the raw entry count.
 */
async function mountAndCollectEntries(strict: boolean) {
  await installJsdom();
  const React: any = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { NewsFeed } = await import('../src/components/NewsFeed.tsx');
  const { initialState } = await import('../src/sim/engine.ts');

  const state: any = {
    ...initialState(),
    lineageId: 'bug756-lineage',
    tick: 200,
    notice: { level: 5, cash: 15000, unlocked: ['Penthouse Tower'] },
    milestoneNotice: { id: 'first-100k-pop', label: 'Metropolis', cash: 300000 },
    placeNotice: 'Fix All: built 3 of 5 planned — click Fix All again for the rest',
    consolidatorLog: [skipPass(7)],
  };
  const ctx: any = {
    state,
    dispatch: () => {},
    cityName: 'Bug756ville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => {},
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => true,
  };

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  const provider = React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(NewsFeed));
  const tree = strict ? React.default.createElement(React.default.StrictMode, null, provider) : provider;

  await act(async () => {
    root.render(tree);
  });
  // Expand the panel so every ring entry renders as its own <li>, not just
  // the collapsed ticker's single "latest" slot.
  await act(async () => {
    (container.querySelector('.news-feed-ticker') as HTMLButtonElement).click();
  });

  const entries = Array.from(container.querySelectorAll('.news-feed-entry')).map((el: any) => el.textContent);
  await act(async () => {
    root.unmount();
  });
  return entries;
}

for (const strict of [true, false]) {
  test(`BUG-756 real mount (StrictMode=${strict}): all four absorbed sources render exactly once each, none zero, none doubled`, async () => {
    const entries = await mountAndCollectEntries(strict);

    const countMatching = (re: RegExp) => entries.filter((t) => re.test(t)).length;

    assert.equal(countMatching(/Level 5 reached/), 1, `levelup entry must render exactly once (StrictMode=${strict})`);
    assert.equal(
      countMatching(/Milestone reached: Metropolis/),
      1,
      `milestone entry must render exactly once (StrictMode=${strict})`
    );
    assert.equal(
      countMatching(/Fix All: built 3 of 5 planned/),
      1,
      `placeNotice entry must render exactly once (StrictMode=${strict})`
    );
    assert.equal(
      countMatching(/Consolidator skipped section 7: capacity unknown/),
      1,
      `consolidatorCapacityUnknown entry must render exactly once (StrictMode=${strict})`
    );
    assert.equal(entries.length, 4, `exactly four total entries, no extras (StrictMode=${strict})`);
  });
}

// RED-PROOF childBody: mounts NewsFeed directly under a REAL
// createRoot+StrictMode+act harness, THEN activates all four absorbed
// sources via a SEPARATE subsequent render (matching the real gameplay
// shape: the feed is already mounted, then a notice activates) — a
// single-shot mount with every source ALREADY active on the first render
// does not reproduce this defect (measured directly: React's dev-mode
// double-invocation of a component's render body shares refs cleanly for a
// single commit), but a genuine mid-session activation does, because it is
// React's SEPARATE double-invocation of the setState UPDATER FUNCTION
// (purity-checking) that discards the push — see NewsFeed.tsx's own
// "BUG-756 (2nd finding)" comment. Runs as a plain script (`--import
// tsx/esm`, no node:test) inside the mutant shadow copy — mirrors
// bug755-restore-refusal-loud-redproof.test.mjs's own precedent for
// mounting a REAL JSX-bearing component through runWithMutant/
// runBaselineProbe (runMutantSelfReinvoke re-spawns a bare `node --test`,
// which cannot load a JSX-bearing `.tsx` file at all).
const PROBE_CHILD_BODY = `
import { JSDOM } from 'jsdom';
import React from 'react';
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true, writable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const { SimContext } = await import('./sim/simContext.ts');
const { NewsFeed } = await import('./components/NewsFeed.tsx');
const { initialState } = await import('./sim/engine.ts');

const base = initialState();
const mk = (over) => ({
  state: { ...base, lineageId: 'bug756-lineage', ...over },
  dispatch: () => {}, cityName: 'Bug756ville',
  listSaves: () => [], listRecent: () => [], saveGame: async () => true,
  saveGameAs: async () => {}, loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => true,
});

const container = dom.window.document.getElementById('root');
const root = createRoot(container);
const render = async (ctx) => {
  await act(async () => {
    root.render(
      React.createElement(React.StrictMode, null,
        React.createElement(SimContext.Provider, { value: ctx }, React.createElement(NewsFeed)))
    );
  });
};

// Render 1: feed mounts with NOTHING active (the realistic starting point —
// the feed is a persistent HUD element mounted long before any notice
// fires).
await render(mk({ tick: 1 }));
await act(async () => {
  container.querySelector('.news-feed-ticker').click();
});

// Render 2: a level-up notice activates — a SEPARATE subsequent render on
// the SAME mounted root, exactly like a real gameplay tick crossing a level
// boundary.
await render(mk({ tick: 5, notice: { level: 5, cash: 15000, unlocked: ['Penthouse Tower'] } }));

const entries = Array.from(container.querySelectorAll('.news-feed-entry')).map((el) => el.textContent);
console.log('ENTRY_COUNT:' + entries.length);
console.log('HAS_LEVELUP:' + entries.some((t) => /Level 5 reached/.test(t)));

await act(async () => { root.unmount(); });
`;

test('BUG-756 RED-PROOF baseline: the real fix renders the entry exactly once for an activation that happens AFTER mount, under real StrictMode', () => {
  const output = runBaselineProbe({
    targetRelPath: 'components/NewsFeed.tsx',
    childBody: PROBE_CHILD_BODY,
    extraArgs: ['--import', 'tsx/esm'],
    timeoutMs: 60000,
  });
  assert.match(output, /ENTRY_COUNT:1/, `baseline must render exactly 1 entry under StrictMode: ${output}`);
  assert.match(output, /HAS_LEVELUP:true/, output);
});

test('BUG-756 RED-PROOF: reverting NewsFeed.tsx to the render-phase derivation reproduces the zero-entries defect under real StrictMode', () => {
  // Non-vacuity: the mutation below restores the ORIGINAL defective shape —
  // a direct render-phase `setRing` call plus a ref-identity
  // "sourcesChanged" guard, no useState-lazy-init/useEffect split — inside
  // the component body, exactly the code this fix replaced (see git history
  // of this file / the BUG-756 root-cause comment atop NewsFeed.tsx).
  const mutantOutput = runWithMutant({
    targetRelPath: 'components/NewsFeed.tsx',
    mutate: (original) => {
      const startMarker = 'const trackerRef = useRef(createNewsFeedTracker());';
      const endMarker =
        "}, [state.lineageId, state.notice, state.milestoneNotice, state.placeNotice, consolidatorLatestPass, financeStatus, insolvencyStatus]);";
      const startIdx = original.indexOf(startMarker);
      const endIdx = original.indexOf(endMarker);
      if (startIdx === -1 || endIdx === -1) {
        throw new Error('BUG-756 mutant setup: could not locate the derivation block — has NewsFeed.tsx moved?');
      }
      const before = original.slice(0, startIdx);
      const after = original.slice(endIdx + endMarker.length);
      // The pre-fix render-phase pattern: mutate refs and call setRing
      // directly in the component body, gated only by a ref-identity check
      // (no useEffect, no lazy useState initializer). `financeStatus` is
      // still in scope (declared BEFORE this block, untouched by the
      // mutation) so this reproduces the exact BUG-723-era shape that also
      // threaded payrollShortfall through the render-phase derivation.
      const replacement = `const trackerRef = useRef(createNewsFeedTracker());
  const seqRef = useRef(createNewsFeedSeq());
  const [ring, setRing] = useState([]);
  const [expanded, setExpanded] = useState(false);
  const [seenCount, setSeenCount] = useState(0);
  const lastLineageIdRef = useRef(state.lineageId);
  if (lastLineageIdRef.current !== state.lineageId) {
    lastLineageIdRef.current = state.lineageId;
    trackerRef.current = createNewsFeedTracker();
  }
  const lastObservedRef = useRef(null);
  const consolidatorLatestPass = state.consolidatorLog?.[0] ?? null;
  const sourcesChanged =
    lastObservedRef.current === null ||
    lastObservedRef.current.notice !== state.notice ||
    lastObservedRef.current.milestoneNotice !== state.milestoneNotice ||
    lastObservedRef.current.placeNotice !== state.placeNotice ||
    lastObservedRef.current.consolidatorLatestPass !== consolidatorLatestPass ||
    lastObservedRef.current.payrollShortfall !== financeStatus;
  if (sourcesChanged) {
    lastObservedRef.current = { notice: state.notice, milestoneNotice: state.milestoneNotice, placeNotice: state.placeNotice, consolidatorLatestPass, payrollShortfall: financeStatus };
    const nextRing = observeNews({ notice: state.notice, milestoneNotice: state.milestoneNotice, placeNotice: state.placeNotice, consolidatorLatestPass, tick: state.tick, payrollShortfall: financeStatus }, trackerRef.current, ring, seqRef.current);
    if (nextRing !== ring) setRing(nextRing);
  }`;
      return before + replacement + after;
    },
    childBody: PROBE_CHILD_BODY,
    extraArgs: ['--import', 'tsx/esm'],
    timeoutMs: 60000,
  });

  assert.doesNotMatch(mutantOutput, /SETUP-BROKEN/, `mutant probe setup must not be broken: ${mutantOutput}`);
  assert.match(
    mutantOutput,
    /ENTRY_COUNT:0/,
    `RED-PROOF: reverting to the render-phase derivation must reproduce the zero-entries defect (a post-mount activation lost under real StrictMode): ${mutantOutput}`
  );
});
