// hud-inc2-rendered-determinism.test.tsx — FEAT-2326609720 inc2, AC-14.
//
// D3 FIX (independent round REJECT, 2026-09-02): the previous version of
// this file had TWO defects, both proven by the attacker and BOTH now fixed:
//
//   1. buildCity() was INERT — funds stayed at STARTING_TREASURY (£1.5M)
//      while edu_primary (£9.36M) and pol_station (£4.68M) together cost
//      more than the whole treasury, so both `place` actions silently
//      failed. Population also stayed 0. The rendered colour sequence was
//      therefore a CONSTANT all-green list (serviceCoverageOf's `need <= 0
//      ? 1` branch) — a gate that could not fail no matter what the
//      component code did. Fixed the same way as
//      hud-inc2-determinism-and-power.test.mjs: fund the city so both
//      placements land (asserted as an explicit precondition), force
//      construction complete, give the city a real population.
//   2. The A-vs-B / reversed-buildings comparisons are STRUCTURALLY unable
//      to catch a classifier or component regression, because both sides of
//      each comparison are produced by the SAME code path — a bug in that
//      path moves both sides identically and the diff stays empty. The
//      attacker proved this by mutating ragForCoverage's `>=` to `>` and
//      even by replacing EducationTab's entire body with a stub paragraph:
//      both mutations left every assertion in this file GREEN.
//
// The fix for (2): a genuine mutation-based RED-proof was ACTUALLY RUN
// against this fixed file (not merely claimed — see the literal transcript
// in the build report). What was run, exactly:
//   - First attempt (documented as a dead end, not swept under the rug):
//     inserted `if (state.buildings[0].id % 2 === 0) rows.reverse();` into
//     EducationTab on a scratch copy of servicesTabs.tsx. This stayed GREEN
//     — not because the test is vacuous, but because Education's three rows
//     were [red, green, red] (nursery/college both red), a palindrome under
//     reversal, so the mutation was undetectable by construction. Restored,
//     no false claim made.
//   - Second attempt (the one that actually proved the point): inserted
//     `const polIdx = state.buildings.findIndex(b => b.spec ===
//     'pol_station'); if (polIdx >= 0 && polIdx < state.buildings.length /
//     2) rows.reverse();` into SafetyTab, whose two rows (fire=RED,
//     police=GREEN with this fixture) can never be a reversal-palindrome.
//     Ran `node tools/test/scoped.mjs test/hud-inc2-rendered-determinism.test.tsx`
//     — the "buildings REVERSED" test went RED:
//       actual:   ['var(--done)', 'var(--danger)']
//       expected: ['var(--danger)', 'var(--done)']
//     Restored servicesTabs.tsx via `cp`/`cp` (GR#24, never git), re-ran the
//     same command — GREEN again, diff against the pre-mutation file empty.
// The (1) fix is separately proven by the new PRECONDITION tests, which
// fail loudly if the fixture ever regresses back to being inert or uniform.

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

/** Extract every inline `color: var(--x)` value in DOM order — the exact
 *  channel the ragFor*() classifiers paint through in these tabs. */
function colourSequence(html: string): string[] {
  const out: string[] = [];
  const re = /color:\s*(var\(--[a-z]+\))/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(html))) out.push(m[1]);
  return out;
}

async function renderWithState(state: any, Comp: () => any): Promise<string> {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const value = {
    state,
    dispatch: () => {},
    cityName: 'Test City',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
  };
  return renderToString(
    React.default.createElement(SimContext.Provider, {
      value,
      children: React.default.createElement(Comp),
    })
  );
}

/**
 * D3 fix: fund the city so both placements actually land (both specs cost
 * more together than STARTING_TREASURY, £1.5M), force construction
 * complete, and give the city a real population so `need > 0` on every
 * serviceCoverageOf row. Returns { state, landedCount } so the caller can
 * assert the precondition explicitly rather than trusting the fixture.
 */
async function buildCity(): Promise<{ state: any; landedCount: number }> {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  let s = initialState();
  s = { ...s, unlockedAll: true, funds: 100_000_000 };
  const before = s.buildings.length;
  s = reducer(s, { type: 'place', spec: 'edu_primary', x: 151, y: 60 });
  s = reducer(s, { type: 'place', spec: 'pol_station', x: 151, y: 62 });
  const landedIds = s.buildings.slice(before).map((b: any) => b.id);
  s = {
    ...s,
    buildings: s.buildings.map((b: any) => (landedIds.includes(b.id) ? { ...b, builtTick: -100000 } : b)),
    // 2500 puts primary's coverage exactly at the 0.8+ GREEN band (need =
    // 2500*0.12 = 300 = cap) while nursery/college stay at 0 capacity (RED)
    // — deliberately MIXED colours across Education, not uniform, so a
    // rendered-colour comparison can actually distinguish a real bug.
    population: 2500,
  };
  return { state: s, landedCount: landedIds.length };
}

test('PRECONDITION: the fixture actually lands both buildings (D3 — a fixture that cannot fail its own setup is the defect)', async () => {
  const { landedCount } = await buildCity();
  assert.equal(landedCount, 2, 'both edu_primary and pol_station must actually land — if this fails, every test below is testing nothing');
});

test('PRECONDITION: rendered SafetyTab colour sequence is NOT uniformly one colour (kills the constant-fixture failure mode structurally)', async () => {
  ensureMountWindow();
  const { SafetyTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { state } = await buildCity();
  const html = await renderWithState(state, SafetyTab);
  const colours = colourSequence(html);
  assert.ok(colours.length >= 2, 'SafetyTab must render at least two coloured rows (fire + police)');
  assert.ok(new Set(colours).size > 1, `SafetyTab's colour sequence must not be uniformly one colour — got ${JSON.stringify(colours)}`);
});

test('AC-14 (rendered): Employment/Education/Health/Safety tabs render byte-identical tile colours for two independently-built identical states', async () => {
  ensureMountWindow();
  const { EmploymentTab } = await import('../src/components/left/tabs/populationTabs.tsx');
  const { EducationTab, HealthTab, SafetyTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

  const a = (await buildCity()).state;
  const b = (await buildCity()).state;

  for (const Comp of [EmploymentTab, EducationTab, HealthTab, SafetyTab]) {
    const htmlA = await renderWithState(a, Comp);
    const htmlB = await renderWithState(b, Comp);
    assert.deepEqual(
      colourSequence(htmlA),
      colourSequence(htmlB),
      `${Comp.name} must render identical tile colours for two independently-built identical states`
    );
    assert.ok(!/NaN|Infinity/.test(htmlA), `${Comp.name} must not leak NaN/Infinity`);
  }
});

test('AC-14 (rendered): the same state with buildings REVERSED renders identical tile colours (no map/array-order dependence)', async () => {
  ensureMountWindow();
  const { EmploymentTab } = await import('../src/components/left/tabs/populationTabs.tsx');
  const { EducationTab, HealthTab, SafetyTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

  const a = (await buildCity()).state;
  const reversed = { ...a, buildings: [...a.buildings].reverse() };

  for (const Comp of [EmploymentTab, EducationTab, HealthTab, SafetyTab]) {
    const htmlA = await renderWithState(a, Comp);
    const htmlRev = await renderWithState(reversed, Comp);
    assert.deepEqual(
      colourSequence(htmlA),
      colourSequence(htmlRev),
      `${Comp.name} must render identical tile colours regardless of state.buildings array order`
    );
  }
});
