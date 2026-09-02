// unlock-all.test.mjs — FEAT-1972079899: god-mode "Unlock all" control.
//
// Covers the unlockAll action + the shared catalogue gate:
//  - sufficient funds  → unlockedAll true, funds reduced by exactly UNLOCK_ALL_COST,
//    a previously-locked high-level spec becomes available via the gate
//  - insufficient funds → no change (all-or-nothing: funds unchanged, flag stays false)
//  - the gate: s.unlockedAll makes every spec available regardless of s.level
//  - journal: unlockAll classified state-affecting (so genesis-replay reproduces it)
//  - determinism: same state → identical result (pure reducer)
//  - conservation: the unlock spend is a between-tick mutation and must not break
//    the tick-boundary conservation invariant
//
// RED proof (via scratch-copy, NEVER git): breaking specUnlocked to ignore
// unlockedAll makes the "all specs available" test go RED — see engine.ts.bak dance
// documented in the task report.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  reducer,
  initialState,
  levelOf,
  specUnlocked,
  UNLOCK_ALL_COST,
} from '../src/sim/engine.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { SPECS } from '../src/sim/data.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const topBarSource = fs.readFileSync(path.join(here, '..', 'src', 'components', 'TopBar.tsx'), 'utf8');

/** First catalogue spec whose unlock level is above the player's current level. */
function aLockedSpec(s) {
  const lv = levelOf(s.xp);
  const sp = Object.values(SPECS).find((x) => x.unlock > lv);
  assert.ok(sp, 'precondition: at least one spec is locked at the seed level');
  return sp;
}

test('unlockAll with sufficient funds: flag set, exact cost charged, locked spec becomes available', () => {
  const s = initialState();
  assert.equal(s.unlockedAll, false, 'precondition: fresh city is not fully unlocked');
  assert.ok(s.funds >= UNLOCK_ALL_COST, 'precondition: seed treasury can afford the gate');

  const locked = aLockedSpec(s);
  assert.equal(specUnlocked(s, locked), false, 'precondition: spec is gated before unlock');

  const s1 = reducer(s, { type: 'unlockAll' });
  assert.equal(s1.unlockedAll, true, 'unlockedAll flips true');
  assert.equal(s1.funds, s.funds - UNLOCK_ALL_COST, 'funds reduced by EXACTLY UNLOCK_ALL_COST');
  assert.equal(specUnlocked(s1, locked), true, 'the previously-locked spec is now available');
});

test('unlockAll with insufficient funds: no change (all-or-nothing)', () => {
  const s0 = initialState();
  const poor = { ...s0, funds: UNLOCK_ALL_COST - 1 };
  const s1 = reducer(poor, { type: 'unlockAll' });
  assert.equal(s1.funds, poor.funds, 'funds unchanged when unaffordable');
  assert.equal(s1.unlockedAll, false, 'flag stays false when unaffordable');
  assert.deepEqual(s1, poor, 'state is returned untouched (no partial apply)');
});

test('gate: s.unlockedAll makes EVERY spec available regardless of s.level', () => {
  const s = initialState();
  const lv = levelOf(s.xp);
  // Sanity: without the flag, some specs are genuinely locked.
  const lockedCount = Object.values(SPECS).filter((sp) => sp.unlock > lv).length;
  assert.ok(lockedCount > 0, 'precondition: some specs locked at seed level');

  const unlocked = { ...s, unlockedAll: true };
  for (const sp of Object.values(SPECS)) {
    assert.equal(specUnlocked(unlocked, sp), true, `${sp.id} available under unlockedAll`);
  }
});

test('gate: without unlockedAll, availability is exactly unlock <= level (behaviour-preserving)', () => {
  const s = initialState();
  const lv = levelOf(s.xp);
  for (const sp of Object.values(SPECS)) {
    assert.equal(specUnlocked(s, sp), sp.unlock <= lv, `${sp.id} gate matches unlock<=level`);
  }
});

test('journal: unlockAll is classified state-affecting (so it journals for replay)', () => {
  assert.equal(isStateAffecting({ type: 'unlockAll' }), true);
});

test('determinism: same state + unlockAll → identical result (pure reducer)', () => {
  const s = initialState();
  const a = reducer(s, { type: 'unlockAll' });
  const b = reducer(structuredClone(s), { type: 'unlockAll' });
  assert.deepEqual(a, b, 'unlockAll is deterministic');
});

test('idempotent: a second unlockAll does not re-charge', () => {
  const s = initialState();
  const s1 = reducer(s, { type: 'unlockAll' });
  const s2 = reducer(s1, { type: 'unlockAll' });
  assert.equal(s2.funds, s1.funds, 'no double charge');
  assert.equal(s2.unlockedAll, true);
  assert.deepEqual(s2, s1, 'already-unlocked state is untouched');
});

test('conservation: unlockAll spend does not break the tick-boundary invariant', () => {
  // The spend is a between-tick mutation (like debugFunds / place cost) — conservation
  // uses fundsAtTickStart/End snapshots, so it must still pass after a spend + tick.
  const s = initialState();
  const s1 = reducer(s, { type: 'unlockAll' });
  const s2 = reducer(s1, { type: 'tick' });
  const report = runConsistencyChecks(s2);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(check, 'conservation check exists');
  assert.equal(check.ok, true, 'conservation passes after unlockAll + advance');
});

test('Q100047 c/C2: the Unlock All BUTTON is DEV-gated, same idiom as DevFundsButton (not rendered in production)', () => {
  // Aaron ruling Q100047 c = C2: Unlock All must stop being a permanently-visible
  // priced gameplay button (a cash god-mode sink) and be gated exactly like the
  // dev-only +£10m / +£1B buttons -- rendered only when import.meta.env.DEV is
  // true, so a production `vite build` (DEV=false) omits it. The reducer action
  // (unlockAll) and its cost logic are UNCHANGED -- only the button's visibility.
  const startOverBody = topBarSource.split('export function StartOverButton()')[1] ?? '';
  assert.ok(startOverBody.includes('unlock-all'), 'precondition: the unlock-all button markup exists');
  // The className marker must appear strictly inside an `import.meta.env?.DEV &&` guard,
  // not merely somewhere in the file (which would pass even if gating were removed).
  // BUG-584: the gate now reads `import.meta.env?.DEV` (optional-chain idiom,
  // codebase-wide, so a runtime without `import.meta.env` itself — SSR-style
  // render, the tsx test runner — doesn't throw). The regex is retargeted to
  // match the `?.` form; intent (Unlock-All is wrapped in the DEV guard) unchanged.
  const guardMatch = /\{import\.meta\.env\?\.DEV\s*&&\s*\(([\s\S]*?)\)\s*\}/.exec(startOverBody);
  assert.ok(guardMatch, 'the Unlock All button must be wrapped in an `{import.meta.env?.DEV && (...)}` guard');
  assert.ok(guardMatch[1].includes('unlock-all'),
    'the import.meta.env.DEV guard must wrap the unlock-all button specifically (not an unrelated block)');
  assert.ok(guardMatch[1].includes("dispatch({ type: 'unlockAll' })"),
    'the guarded block must still dispatch the unchanged unlockAll action (UI-only gate, reducer untouched)');
});

test('journal replay: an unlockAll in the action stream reproduces the unlocked state', () => {
  // Replaying the same actions through the reducer must land the flag + funds.
  const s = initialState();
  const actions = [{ type: 'unlockAll' }, { type: 'tick' }];
  const live = actions.reduce((acc, a) => reducer(acc, a), s);
  const replayed = actions.reduce((acc, a) => reducer(acc, a), initialState());
  assert.deepEqual(replayed, live, 'replay lands identical state');
  assert.equal(replayed.unlockedAll, true, 'flag survives replay');
});
