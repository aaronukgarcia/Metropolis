// bug755-restore-refusal-loud-redproof.test.mjs — RED-PROOF companion to
// bug755-restore-refusal-loud.test.tsx (BUG-755 lead ruling part 3): proves
// the MET-V868/placeNotice loud-refusal block in store.tsx's boot initializer
// is load-bearing, not merely present. Follows the SAME pattern as
// bug704-store-wiring.test.mjs (a .tsx target can't be re-invoked as a fresh
// tsx-loaded child through testsupport/mutant.mjs's runMutantSelfReinvoke —
// that helper re-spawns a bare `node --test`, and Node's native loader does
// not understand `.tsx` — so the RED-PROOF instead drives a plain childBody
// script, executed under `--import tsx/esm`, that mounts the real
// SimProvider directly with no node:test involved in the child at all).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

// NOTE: mounting a REAL JSX-bearing component (store.tsx's own
// `<SimContext.Provider>` return) inside a mutant.mjs shadow copy needed a
// fix to mutant.mjs itself (linkNodeModulesIntoShadow's tsconfig copy used to
// carry the real file's `"include": ["src", "test"]`, which — since the
// shadow FLATTENS webconsole/src's contents to its own root — silently
// dropped every shadow file out of the tsconfig's scope and fell back to
// esbuild's classic JSX transform, throwing "ReferenceError: React is not
// defined" the moment SimProvider actually rendered). See that fix's own
// doc comment in testsupport/mutant.mjs for the measured repro.

// Seeds a savepoint with a duplicate building id (a REAL, blocking
// consistency failure — buildings.ids-unique is never in
// RESTORE_NONBLOCKING_CHECK_IDS), mounts the real SimProvider against it, and
// prints one marker line reporting whether MET-V868 was recorded and whether
// the booted state carries the restore-refusal placeNotice.
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
globalThis.Blob = dom.window.Blob;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
dom.window.HTMLAnchorElement.prototype.click = function () {};

const { createSavepoint, persistSavepoint } = await import('./sim/replay.ts');
const { initialState } = await import('./sim/engine.ts');
const { recentErrors } = await import('./sim/backend.ts');
const { SimProvider, useSim } = await import('./sim/store.tsx');
const { resetSaveStoreForTests } = await import('./sim/saveStore.ts');

resetSaveStoreForTests();

const corrupt = {
  ...initialState(),
  buildings: [
    { id: 1, spec: 'res_hut', x: 5, y: 5 },
    { id: 1, spec: 'res_hut', x: 8, y: 8 },
  ],
};
const ok = persistSavepoint(window.localStorage, createSavepoint(corrupt, [], new Date(), 'test-build', null));
if (!ok) {
  console.log('SETUP-BROKEN-SEED');
} else {
  const seen = { ctx: null, state: null };
  function Probe() {
    const ctx = useSim();
    seen.ctx = ctx;
    seen.state = ctx.state;
    return null;
  }
  const container = dom.window.document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(React.createElement(SimProvider, { children: React.createElement(Probe) }));
  });
  for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 15));

  const hasV868 = recentErrors().some((e) => e.code === 'MET-V868');
  const hasNotice = typeof seen.state?.placeNotice === 'string' && /could not be restored/i.test(seen.state.placeNotice);
  console.log('V868:' + (hasV868 ? 'RECORDED' : 'MISSING'));
  console.log('NOTICE:' + (hasNotice ? 'PRESENT' : 'MISSING'));

  await act(async () => {
    root.unmount();
  });
}
`;

describe('BUG-755 part 3 RED-PROOF: the MET-V868/placeNotice loud-refusal block in store.tsx is load-bearing', () => {
  test('baseline (unmutated) sanity: a refused-but-existing savepoint records MET-V868 and sets the placeNotice', () => {
    const output = runBaselineProbe({
      targetRelPath: 'sim/store.tsx',
      childBody: PROBE_CHILD_BODY,
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.doesNotMatch(output, /SETUP-BROKEN/, `probe setup must not be broken: ${output}`);
    assert.match(output, /V868:RECORDED/, `baseline must record MET-V868: ${output}`);
    assert.match(output, /NOTICE:PRESENT/, `baseline must set the restore-refusal placeNotice: ${output}`);
  });

  test('RED-PROOF: removing the loud-refusal block makes the fallback silent again (no MET-V868, no placeNotice)', () => {
    const mutantOutput = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => {
        // BUG-755 P1/P2 (independent round): the loud-refusal detection now
        // lives inside a `try { if (most) {...} else {...} } catch {...}`
        // wrapper (P2's never-crash guarantee) — strip the WHOLE try/catch,
        // not just the old bare `if (most)` block, or the mutant leaves a
        // dangling `else`/`catch` behind (a syntax error, not a behavioural
        // mutation).
        const marker = 'try {\n      if (most) {\n        const reasonText =';
        const idx = src.indexOf(marker);
        if (idx === -1) {
          throw new Error('RED-PROOF setup is broken: the BUG-755 loud-refusal try/catch block was not found — has it moved?');
        }
        function matchBrace(openBraceIdx) {
          let depth = 0;
          let i = openBraceIdx;
          for (; i < src.length; i++) {
            if (src[i] === '{') depth++;
            else if (src[i] === '}') {
              depth--;
              if (depth === 0) return i;
            }
          }
          throw new Error('RED-PROOF setup is broken: unbalanced braces while locating the block to strip');
        }
        // Close the `try { ... }` block first...
        const tryCloseIdx = matchBrace(src.indexOf('{', idx));
        // ...then find and close the FOLLOWING `catch { ... }` too, so no
        // dangling `catch` is left behind (this file's src always writes
        // `try {...} catch {` with no space before the brace — see the
        // literal in the P2 fix).
        const catchOpenIdx = src.indexOf('{', tryCloseIdx + 1);
        const afterTry = src.slice(tryCloseIdx + 1, catchOpenIdx);
        if (!/^\s*catch\s*$/.test(afterTry)) {
          throw new Error(`RED-PROOF setup is broken: expected a bare "catch {" right after the try block, got: ${JSON.stringify(afterTry)}`);
        }
        const catchCloseIdx = matchBrace(catchOpenIdx);
        // Reproduce BUG-755's original defect: the refusal falls straight
        // through to a mute fresh boot again.
        return src.slice(0, idx) + '/* BUG-755 mutant: loud-refusal try/catch block removed */' + src.slice(catchCloseIdx + 1);
      },
      childBody: PROBE_CHILD_BODY,
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.doesNotMatch(mutantOutput, /SETUP-BROKEN/, `mutant probe setup must not be broken: ${mutantOutput}`);
    assert.match(mutantOutput, /V868:MISSING/, `RED-PROOF: without the block, MET-V868 must NOT be recorded: ${mutantOutput}`);
    assert.match(mutantOutput, /NOTICE:MISSING/, `RED-PROOF: without the block, the placeNotice must NOT be set: ${mutantOutput}`);
  });

  // ── P2 (independent round opus-round-bug755): the [boot] initializer's
  // contract is NEVER-CRASH. recordError() (backend.ts) and the ring/
  // getAppVersion bookkeeping it does are themselves UNGUARDED — a throw
  // from inside them, reached through the new BUG-755 loud-refusal block,
  // must not take the whole boot down with it. Proven by mutating
  // recordError() itself to throw unconditionally and showing the SAME
  // corrupt-savepoint boot still mounts (never crashes) — the P2 try/catch
  // wrapper is what makes this true.
  test('P2: recordError() throwing inside the loud-refusal block must NOT crash the [boot] initializer', () => {
    const output = runWithMutant({
      targetRelPath: 'sim/backend.ts',
      mutate: (src) => {
        const needle = 'export function recordError(msg: string, meta?: RecordErrorMeta): { correlationId: number; code?: string; tick?: number } {';
        if (!src.includes(needle)) {
          throw new Error('RED-PROOF setup is broken: recordError signature not found — has it moved?');
        }
        return src.replace(needle, needle + "\n  throw new Error('BUG-755 P2 mutant: recordError always throws');");
      },
      childBody: PROBE_CHILD_BODY.replace(
        "console.log('NOTICE:' + (hasNotice ? 'PRESENT' : 'MISSING'));",
        "console.log('NOTICE:' + (hasNotice ? 'PRESENT' : 'MISSING'));\n  console.log('MOUNTED_DESPITE_THROW:true');\n  console.log('BUILDINGS:' + (seen.state ? seen.state.buildings.length : -1));",
      ),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.doesNotMatch(output, /SETUP-BROKEN/, `probe setup must not be broken: ${output}`);
    assert.match(
      output,
      /MOUNTED_DESPITE_THROW:true/,
      `P2: the boot must still complete and mount even when recordError() throws inside the loud-refusal block: ${output}`,
    );
    // A real state must have come back too (the fresh city), not an
    // exception that unwound past `[boot]`'s useState initializer.
    assert.match(output, /BUILDINGS:\d+/, `P2: a real booted state must exist despite the throw: ${output}`);
  });
});
