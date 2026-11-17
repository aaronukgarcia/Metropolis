// ux2-demand-legibility.test.tsx — FEAT-9995385592 (UI overhaul UX-2).
// Tests are named by acceptance-criterion id from
// docs/planning/acceptance/feat-ui-overhaul.md section 2 (UX-2).
import { test } from 'node:test';
import assert from 'node:assert/strict';

function installJsdom() {
  return import('jsdom').then(({ JSDOM }) => {
    const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
      url: 'http://localhost/',
      pretendToBeVisual: true,
    });
    const g: any = globalThis;
    const saved: Record<string, any> = {};
    for (const k of ['window', 'document', 'navigator', 'HTMLElement', 'Node', 'MouseEvent', 'Event', 'IS_REACT_ACT_ENVIRONMENT']) saved[k] = g[k];
    g.window = dom.window;
    g.document = dom.window.document;
    try { g.navigator = dom.window.navigator; } catch { /* read-only on newer node */ }
    g.HTMLElement = dom.window.HTMLElement;
    g.Node = dom.window.Node;
    g.MouseEvent = dom.window.MouseEvent;
    g.Event = dom.window.Event;
    g.IS_REACT_ACT_ENVIRONMENT = true;
    (dom as any).__restore = () => {
      for (const k of Object.keys(saved)) { try { g[k] = saved[k]; } catch { /* ignore */ } }
      dom.window.close();
    };
    return dom as any;
  });
}

async function mountDock(state: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { DemandDock } = await import('../src/components/left/DemandDock.tsx');
  const ctx: any = {
    state, dispatch: () => {}, cityName: 'X', listSaves: () => [], listRecent: () => [],
    saveGame: async () => true, saveGameAs: async () => {}, loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => true,
  };
  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(BusyProvider, { children: React.default.createElement(DemandDock) })));
  });
  return { container, root, act };
}

const P = () => import('../src/components/demandPresentation.ts');

test('AC-2.1: colour law — a service shortfall is never green, a covered service never red/amber, zone demand never uses the health green', async () => {
  const { serviceTone, zoneTone, isActionTone } = await P();
  for (let v = -100; v <= 100; v++) {
    for (const hasFix of [false, true]) {
      for (const inProgress of [false, true]) {
        const t = serviceTone({ value: v, hasFix, inProgress });
        if (v > 0 || hasFix || inProgress) assert.notEqual(t, 'healthy', `v=${v} fix=${hasFix} prog=${inProgress} must not read green`);
        else assert.equal(t, 'healthy', `v=${v} covered must be green`);
        if (t === 'healthy') assert.ok(!isActionTone(t));
      }
    }
    assert.notEqual(zoneTone(v), 'healthy', `zone ${v} must never use the health green`);
    assert.notEqual(zoneTone(v), 'action');
  }
  assert.equal(serviceTone({ value: 0, alert: true, hasFix: false, inProgress: false }), 'action', 'brownout is red');
});

test('AC-2.1: the colour law is ONE exported table with five distinct colours', async () => {
  const { DEMAND_COLOUR_LAW } = await P();
  const colours = Object.values(DEMAND_COLOUR_LAW).map((x: any) => x.colour);
  assert.equal(new Set(colours).size, 5);
  assert.equal(DEMAND_COLOUR_LAW.action.colour, 'red');
  assert.equal(DEMAND_COLOUR_LAW.healthy.colour, 'green');
});

test('AC-2.1: mounted dock — a service in shortfall renders red/amber tone, and the bar fill class is the tone, not the sign', async () => {
  const dom = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { serviceDemandOf } = await import('../src/sim/data.ts');
    const state = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
    const rows = serviceDemandOf(state).filter((r: any) => r.value > 0);
    assert.ok(rows.length > 0, 'precondition: some service in shortfall');
    const { container, root, act } = await mountDock(state);
    const tones = Array.from(container.querySelectorAll('.demand-row')).map((e: any) => e.getAttribute('data-tone'));
    assert.ok(tones.includes('action') || tones.includes('watch'));
    for (const el of Array.from(container.querySelectorAll('.demand-row')) as any[]) {
      const fill = el.querySelector('.d-fill');
      assert.ok(fill.classList.contains(`tone-${el.getAttribute('data-tone')}`), 'fill carries the row tone');
      assert.ok(!fill.classList.contains('pos') && !fill.classList.contains('neg'), 'no sign-coloured classes remain');
    }
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('AC-2.2: industry -100 reads oversupplied in words, never as unmet demand', async () => {
  const { zonePhrase, zoneTone } = await P();
  const p = zonePhrase('industrial', -100);
  assert.match(p, /Oversupplied/);
  assert.match(p, /no more factories needed/);
  assert.equal(zoneTone(-100), 'idle');
  assert.match(zonePhrase('residential', 80, { jobs: 500, workers: 100 }), /too many jobs for the workforce/);
  assert.match(zonePhrase('commercial', 0), /Balanced/);
  assert.match(zonePhrase('commercial', 70), /zone more shops/);
});

test('AC-2.2: service rows say "Need N, have M" from the SAME numbers demandFixPlan uses', async () => {
  const { serviceQuantitiesOf, needHaveText } = await P();
  const { initialState } = await import('../src/sim/engine.ts');
  const { demandFixPlan } = await import('../src/sim/data.ts');
  const { fmtNum } = await import('../src/sim/utils.ts');
  const state = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
  const q = serviceQuantitiesOf(state);
  const plan = demandFixPlan(state);
  assert.ok(plan.length > 0);
  for (const item of plan) {
    const got = q.get(item.serviceKey);
    assert.ok(got, item.serviceKey);
    assert.equal(got!.need, item.need, `${item.serviceKey} need agrees with demandFixPlan`);
    assert.equal(got!.have, item.have, `${item.serviceKey} have agrees with demandFixPlan`);
  }
  const water = plan.find((p: any) => p.serviceKey === 'cleanwater')!;
  const txt = needHaveText('cleanwater', q.get('cleanwater'));
  assert.ok(txt.startsWith(`Need ${fmtNum(water.need)}, have ${fmtNum(water.have)}`), txt);
});

test('AC-2.2: mounted dock prints the direction phrase and need/have line per row', async () => {
  const dom = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
    const { container, root, act } = await mountDock(state);
    const qtys = Array.from(container.querySelectorAll('.d-qty')).map((e: any) => e.textContent);
    assert.ok(qtys.some((t: string) => /^Need [\d,.]+.*, have /.test(t)), qtys.join(' | '));
    const phrases = Array.from(container.querySelectorAll('.d-phrase')).map((e: any) => e.textContent);
    assert.ok(phrases.some((t: string) => /Needs more/.test(t)), phrases.join(' | '));
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('AC-2.3: every meter carries labelled end caps and midpoint', async () => {
  const dom = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
    const { container, root, act } = await mountDock(state);
    const rows = Array.from(container.querySelectorAll('.demand-row')) as any[];
    assert.ok(rows.length >= 4);
    for (const r of rows) assert.equal(r.querySelectorAll('.d-caps span').length, 3);
    const zoneCaps = rows[0].querySelector('.d-caps').textContent;
    assert.match(zoneCaps, /oversupplied.*balanced.*build more/);
    assert.ok(/zone|jobs|workers/i.test(rows[0].querySelector('.demand').title), 'hover shows model inputs');
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('AC-2.4: the Fix button is an action phrase naming the build, only on action-needed rows', async () => {
  const dom = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
    const { container, root, act } = await mountDock(state);
    const btns = Array.from(container.querySelectorAll('.demand-fix-btn')) as any[];
    assert.ok(btns.length > 0);
    for (const b of btns) {
      assert.match(b.textContent, /^Build (up to )?\d[\d,]* x /, b.textContent);
      assert.ok(!/^Fix \(/.test(b.textContent));
      const tone = b.closest('.demand-row').getAttribute('data-tone');
      assert.ok(tone === 'action' || tone === 'watch', `Fix button on a ${tone} row`);
    }
    for (const row of Array.from(container.querySelectorAll('.demand-row')) as any[]) {
      const t = row.getAttribute('data-tone');
      if (t === 'healthy' || t === 'idle' || t === 'build') assert.equal(row.querySelector('.demand-fix-btn'), null, `${t} row must not carry Fix`);
    }
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('AC-2.5: collapsed one-line summary when nothing is action-needed; expands on click; expanded when something is', async () => {
  const dom = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const calm = { ...initialState(), population: 0 };
    const a = await mountDock(calm);
    const sum = a.container.querySelector('.demand-summary') as any;
    assert.ok(sum);
    assert.equal(sum.getAttribute('aria-expanded'), 'false');
    assert.equal(a.container.querySelectorAll('.demand-row').length, 0, 'collapsed: no rows');
    assert.match(sum.textContent, /All services covered/);
    await a.act(async () => { sum.dispatchEvent(new (globalThis as any).MouseEvent('click', { bubbles: true })); });
    assert.ok(a.container.querySelectorAll('.demand-row').length > 0, 'click expands');
    await a.act(async () => { a.root.unmount(); });

    const busy = { ...initialState(), population: 13_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
    const b = await mountDock(busy);
    const sum2 = b.container.querySelector('.demand-summary') as any;
    assert.equal(sum2.getAttribute('aria-expanded'), 'true');
    assert.match(sum2.textContent, /\(\+\d+ more\)|Needs more|Brownout/);
    await b.act(async () => { b.root.unmount(); });
  } finally { dom.__restore(); }
});

test('AC-2.5: demandSummary names the worst item and counts the others', async () => {
  const { demandSummary } = await P();
  const s = demandSummary([
    { label: 'A', tone: 'watch', value: 10, phrase: 'pa' },
    { label: 'B', tone: 'action', value: 40, phrase: 'pb' },
    { label: 'C', tone: 'healthy', value: -5, phrase: 'pc' },
  ]);
  assert.equal(s.actionCount, 2);
  assert.equal(s.text, 'B: pb (+1 more)');
});

// ---------------------------------------------------------------- r2 rework

async function baseState(pop: number, funds = 1_000_000_000, extra: Record<string, unknown> = {}) {
  const { initialState } = await import('../src/sim/engine.ts');
  return { ...initialState(), population: pop, unlockedAll: true, funds, administrationState: null, ...extra } as any;
}

/** Parse "Build 4 x A + 1 x B" into { A: 4, B: 1 }. */
function parseLabel(label: string): Record<string, number> {
  const out: Record<string, number> = {};
  for (const part of label.replace(/^Build (up to )?/, '').replace(/ \(as funds allow\)$/, '').split(' + ')) {
    const m = /^(\d+) x (.+)$/.exec(part);
    assert.ok(m, `label part "${part}" of "${label}" must be "N x Name"`);
    out[m![2]] = (out[m![2]] ?? 0) + Number(m![1]);
  }
  return out;
}

test('AC-2.4 / F1 (r4): with funds >= planCost the label is "Build up to <mix>" and resolveDemand never places more per spec, nor a spec outside the plan', async () => {
  const { reducer } = await import('../src/sim/engine.ts');
  const D = await import('../src/sim/data.ts');
  const { fixLabelOf, fixTitleOf } = await P();
  let checked = 0;
  let multi = 0;
  for (const pop of [80_000, 200_000, 500_000]) {
    for (const unlockedAll of [false, true]) {
      const base = { ...(await baseState(pop, 1e9)), unlockedAll } as any;
      for (const fix0 of D.demandFixPlan(base)) {
        for (const mult of [1, 1.005, 1.01, 1.02, 1.06, 1.09, 1.1, 'ample'] as const) {
          const funds = mult === 'ample' ? 1e9 : Math.ceil(fix0.planCost * mult);
          const s = { ...base, funds };
          const plan = D.demandFixPlan(s).find((p: any) => p.serviceKey === fix0.serviceKey);
          if (!plan || !(plan.planCost <= funds)) continue;
          const label = fixLabelOf(plan, funds);
          assert.match(label, /^Build up to /, label);
          assert.ok(!/as funds allow/.test(label));
          assert.match(fixTitleOf(plan, funds), /roads may use some of the funds/);
          const before = new Map<string, number>();
          for (const b of s.buildings) before.set(b.spec, (before.get(b.spec) ?? 0) + 1);
          const s2 = reducer(s, { type: 'resolveDemand', serviceKey: plan.serviceKey } as any);
          const after = new Map<string, number>();
          for (const b of s2.buildings) after.set(b.spec, (after.get(b.spec) ?? 0) + 1);
          const want: Record<string, number> = {};
          for (const m of plan.mix) want[m.specId] = (want[m.specId] ?? 0) + m.count;
          for (const [k, n] of after) {
            const d = n - (before.get(k) ?? 0);
            if (d <= 0 || !D.SPECS[k] || D.isRoadOrTrunkSpec(D.SPECS[k])) continue;
            assert.ok(d <= (want[k] ?? 0), `pop ${pop} unlockedAll ${unlockedAll} ${plan.serviceKey} funds x${mult}: placed ${d} x ${k} but the plan has ${want[k] ?? 0} ("${label}")`);
          }
          // the label's own numbers are the plan's numbers (it is the bound being asserted above)
          const parsed = parseLabel(label);
          for (const [k, n] of Object.entries(want)) assert.equal(parsed[D.SPECS[k].name], n, label);
          checked++;
          if (plan.mix.length > 1) multi++;
        }
      }
    }
  }
  assert.ok(checked > 50, `checked ${checked}`);
  assert.ok(multi >= 3, `the sweep must include multi-spec mixes (got ${multi})`);
});

test('AC-2.4 / F1 (r4): below planCost the label says "as funds allow" and the title says a cheaper mix may be substituted', async () => {
  const D = await import('../src/sim/data.ts');
  const { fixLabelOf, fixTitleOf } = await P();
  const s = await baseState(80_000);
  const plan = D.demandFixPlan(s).find((p: any) => p.mix.length > 1)!;
  assert.ok(plan);
  assert.match(fixLabelOf(plan, plan.planCost - 1), /^Build .* \(as funds allow\)$/);
  assert.ok(!/up to/.test(fixLabelOf(plan, plan.planCost - 1)));
  assert.match(fixTitleOf(plan, plan.planCost - 1), /cheaper mix may be substituted/);
  assert.match(fixLabelOf(plan, plan.planCost), /^Build up to /);
  assert.ok(!/funds/.test(fixLabelOf(plan, plan.planCost)));
  assert.ok(fixTitleOf(plan, plan.planCost).includes(fixLabelOf(plan, plan.planCost).replace(/^Build up to /, '')));
});

test('F1: demandFixMessage names the whole mix, not count x mix[0]', async () => {
  const D = await import('../src/sim/data.ts');
  const { demandFixMessage, formatMixText } = await import('../src/components/demandFixUi.ts');
  const s = await baseState(13_000);
  const plan = D.demandFixPlan(s).find((p: any) => p.serviceKey === 'parks' && p.mix.length > 1)!;
  assert.ok(plan, 'fixture has a multi-spec parks plan');
  const msg = demandFixMessage(plan);
  assert.ok(msg.includes(formatMixText(plan)), msg);
  assert.ok(!msg.includes(`${plan.count} x ${D.SPECS[plan.specId].name}`), `must not claim the mix total of the first spec: ${msg}`);
});

test('F2: "shortfall is being bought in" only when an import/contract really substitutes (GP 20,001 vs 20,000)', async () => {
  const D = await import('../src/sim/data.ts');
  const { serviceRowView, serviceQuantitiesOf, isBoughtIn } = await P();
  for (const pop of [20_000, 20_001]) {
    const s = await baseState(pop, 1_000_000_000, { buildings: [] });
    const gp = D.serviceDemandOf(s).find((r: any) => r.id === 'gp')!;
    const q = serviceQuantitiesOf(s).get('gp');
    assert.equal(isBoughtIn(s, 'gp'), false, 'GP can never be bought in');
    const v = serviceRowView('gp', gp.value, gp.alert, q, true, 0, isBoughtIn(s, 'gp'));
    assert.ok(!/bought in/.test(v.phrase), `pop ${pop}: ${v.phrase}`);
  }
  // The exact r1 case: a 1-person GP shortfall whose index rounds to 0, no import anywhere.
  const tiny = serviceRowView('gp', 0, false, { need: 20_001, have: 20_000 }, true, 0, false);
  assert.ok(!/bought in/.test(tiny.phrase), tiny.phrase);
  assert.match(tiny.phrase, /small shortfall/);
  assert.match(serviceRowView('power', 0, false, { need: 10, have: 9 }, true, 0, true).phrase, /bought in/);
  // water: shortfall + import ON reads bought in; import OFF does not
  const on = await baseState(13_000, 1_000_000_000, { buildings: [], waterImportEnabled: true });
  const off = await baseState(13_000, 1_000_000_000, { buildings: [], waterImportEnabled: false });
  const rowOf = (s: any) => {
    const r = D.serviceDemandOf(s).find((x: any) => x.id === 'cleanwater')!;
    return serviceRowView('cleanwater', r.value, r.alert, serviceQuantitiesOf(s).get('cleanwater'), true, 0, isBoughtIn(s, 'cleanwater'));
  };
  assert.equal(isBoughtIn(on, 'cleanwater'), true);
  assert.match(rowOf(on).phrase, /bought in/);
  assert.equal(isBoughtIn(off, 'cleanwater'), false);
  assert.ok(!/bought in/.test(rowOf(off).phrase), rowOf(off).phrase);
});

test('F3: zone phrases state only causes the inputs support', async () => {
  const { zonePhrase } = await P();
  assert.ok(!/more workers than jobs/.test(zonePhrase('residential', -100, { jobs: 0, workers: 0 })));
  assert.ok(!/more workers than jobs/.test(zonePhrase('residential', -100, { jobs: 500, workers: 100 })), 'jobs exceed workers: cause is false');
  assert.match(zonePhrase('residential', -100, { jobs: 100, workers: 500 }), /more workers than jobs/);
  assert.ok(!/too many jobs/.test(zonePhrase('residential', 80, { jobs: 0, workers: 0 })));
  assert.ok(!/too many jobs/.test(zonePhrase('residential', 80)), 'no inputs -> neutral');
  assert.match(zonePhrase('residential', 80, { jobs: 500, workers: 100 }), /too many jobs for the workforce/);
});

test('F4: the summary strip takes the WORST row tone (amber-only is amber)', async () => {
  const { demandSummary } = await P();
  assert.equal(demandSummary([{ label: 'A', tone: 'watch', value: 5, phrase: 'p' }]).tone, 'watch');
  assert.equal(demandSummary([{ label: 'A', tone: 'watch', value: 5, phrase: 'p' }, { label: 'B', tone: 'action', value: 1, phrase: 'q' }]).tone, 'action');
  assert.equal(demandSummary([{ label: 'A', tone: 'healthy', value: -5, phrase: 'p' }]).tone, 'healthy');
  assert.equal(demandSummary([]).tone, 'healthy');
});

test('F4: mounted dock - an amber-only dock renders an amber summary strip', async () => {
  const dom = await installJsdom();
  try {
    // Fix All with ample funds leaves every shortfall covered by providers under construction: rows are amber, none red.
    const { reducer } = await import('../src/sim/engine.ts');
    const state = reducer(await baseState(13_000, 1e12), { type: 'resolveDemandAll' } as any);
    const { container, root, act } = await mountDock(state);
    const sum = container.querySelector('.demand-summary') as any;
    assert.ok(container.querySelectorAll('.demand-row[data-tone="watch"]').length > 0, 'precondition: amber rows');
    assert.equal(container.querySelectorAll('.demand-row[data-tone="action"]').length, 0, 'precondition: no red rows');
    assert.ok(sum.classList.contains('tone-watch'), sum.className);
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('NaN: a non-finite index is neutral/unknown, never green', async () => {
  const { serviceTone, zoneTone, zonePhrase, zoneArrow, serviceRowView } = await P();
  assert.equal(serviceTone({ value: NaN, hasFix: false, inProgress: false }), 'idle');
  assert.equal(serviceTone({ value: Infinity, hasFix: false, inProgress: false }), 'idle');
  assert.equal(zoneTone(NaN), 'idle');
  assert.match(zonePhrase('industrial', NaN), /unknown/i);
  assert.match(zoneArrow(NaN), /unknown/);
  const v = serviceRowView('gp', NaN, false, undefined, false, 0);
  assert.equal(v.tone, 'idle');
  assert.match(v.phrase, /unknown/);
});

test('F5/F6: styles.css pins every tone class to its law colour variable, scoped to the demand dock', async () => {
  const { readFileSync } = await import('node:fs');
  const { DEMAND_COLOUR_LAW } = await P();
  const css = readFileSync(new URL('../src/styles.css', import.meta.url), 'utf8');
  const VAR: Record<string, string> = { red: '--danger', amber: '--warn', green: '--done', blue: '--accent', grey: '--muted' };
  for (const [tone, def] of Object.entries(DEMAND_COLOUR_LAW) as [string, any][]) {
    const re = new RegExp(`\\.tone-${tone}\\s*\\{\\s*--tone:\\s*var\\(${VAR[def.colour]}\\)\\s*;`);
    assert.match(css, re, `.tone-${tone} must be ${def.colour} (${VAR[def.colour]})`);
  }
  // the colour variables themselves exist in :root
  for (const v of Object.values(VAR)) assert.match(css, new RegExp(`${v}:\\s*#[0-9a-fA-F]{3,8}`), v);
  // F5: the tone colour applies to demand-dock rows only; Transport/other tabs' .d-val keep their own colour
  assert.match(css, /\.demand-row \.d-val\s*\{\s*color:\s*var\(--tone/);
  assert.ok(!/(^|\n)\.d-val\s*\{[^}]*color\s*:/.test(css), 'no global .d-val colour rule');
  assert.match(css, /\.d-fill\.pos\s*\{\s*background:\s*var\(--done\)/, 'other tabs keep their pos fill');
  assert.match(css, /\.d-fill\.neg\s*\{\s*background:\s*var\(--danger\)/, 'other tabs keep their neg fill');
  assert.ok(!/(^|\n)\.d-fill\s*\{[^}]*background/.test(css), 'no global .d-fill background rule');
});

test('summary unknown: a non-finite row is never reported as "All services covered" / healthy', async () => {
  const { demandSummary } = await P();
  const s = demandSummary([{ label: 'GP', tone: 'idle', value: NaN, phrase: 'unknown' }, { label: 'Power', tone: 'healthy', value: -5, phrase: 'ok' }]);
  assert.equal(s.tone, 'unknown');
  assert.equal(s.unknownCount, 1);
  assert.match(s.text, /unknown/i);
  assert.ok(!/All services covered/.test(s.text));
  // a real action row still wins over unknown
  assert.equal(demandSummary([{ label: 'GP', tone: 'idle', value: NaN, phrase: 'u' }, { label: 'B', tone: 'action', value: 40, phrase: 'p' }]).tone, 'action');
  assert.equal(demandSummary([{ label: 'A', tone: 'healthy', value: -5, phrase: 'p' }]).unknownCount, 0);
});

test('summary unknown: the mounted dock is not collapsed and not green when a service row is unknown', async () => {
  const dom = await installJsdom();
  try {
    const D = await import('../src/sim/data.ts');
    const state = await baseState(13_000, 1e9);
    const rows = D.serviceDemandOf(state);
    assert.ok(rows.length > 0);
    // Make one service demand unknown through the real selector path: a NaN population poisons every ratio.
    const poisoned = { ...state, population: NaN };
    const poisonedRows = D.serviceDemandOf(poisoned);
    assert.ok(poisonedRows.some((r: any) => !Number.isFinite(r.value)), 'precondition: NaN population yields unknown service rows');
    const { container, root, act } = await mountDock(poisoned);
    const sum = container.querySelector('.demand-summary') as any;
    assert.ok(!/All services covered/.test(sum.textContent), sum.textContent);
    assert.ok(!sum.classList.contains('tone-healthy'), sum.className);
    assert.equal(sum.getAttribute('aria-expanded'), 'true', 'must not collapse by default');
    await act(async () => { root.unmount(); });
  } finally { dom.__restore(); }
});

test('NaN + alert: tone and phrase agree (a brownout alert is action, whatever the index)', async () => {
  const { serviceTone, serviceRowView } = await P();
  assert.equal(serviceTone({ value: NaN, alert: true, hasFix: false, inProgress: false }), 'action');
  const v = serviceRowView('power', NaN, true, undefined, false, 0);
  assert.equal(v.tone, 'action');
  assert.match(v.phrase, /Brownout/);
  assert.equal(serviceRowView('power', NaN, false, undefined, false, 0).tone, 'idle');
});
