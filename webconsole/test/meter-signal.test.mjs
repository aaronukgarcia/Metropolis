// meter-signal.test.mjs — BUG-398 (power meter sign) + BUG-399 (police
// spec-drift) service-coverage meter regressions.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped formulas.
//
// Context — the Y148 live dump reported:
//   * Power (56512/23997 MW): a 2.35× SURPLUS, yet claimed to render as a
//     +100 shortfall.  (BUG-398)
//   * School/College/GP/Hospital/Police pegged at +100 while wellbeing read 90.
//     (BUG-399 — the CONTRADICTION facet is structurally impossible in current
//     code, proven in coverage.test.mjs; this file pins the SEPARATE, real
//     spec-drift facet: newer service buildings pay upkeep but do not move the
//     meter because coverage keyed on a hardcoded building id.)
//
// SIGN CONVENTION (data.ts demandIndexOf, DemandMeter): a POSITIVE meter value
// is a SHORTFALL (unmet demand), NEGATIVE is a SURPLUS. coverage = cap/need;
// value ≈ 100·(1 - coverage), clamped ±100. So a surplus (coverage > 1) MUST
// read negative and MUST NOT raise the brownout `alert`.
//
// DIRECTIONAL / STRUCTURAL TESTS (balance-number regime): they pin the SIGN
// and the count-by-capability shape, not tuned magnitudes.
//
// RED proofs (documented; re-runnable via a scratch copy — never git):
//   * BUG-398 sign: scratch-swapping the power row args in serviceCoverageOf to
//     `row('power', label, pw.cap, pw.need, ...)` (the classic need/cap swap)
//     makes coverage = need/cap < 1 → the deficit branch fires → value pegs
//     +100 with alert=true, failing 'BUG-398 …surplus reads as a surplus'.
//   * BUG-399 police: scratch-reverting the police count in serviceCoverageOf
//     back to `sp.id === 'pol_station'` makes a Divisional-HQ-only city read
//     cap 0 → meter +100, failing 'BUG-399 …pol_hq raises the Police meter'.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, serviceCoverageOf, serviceDemandOf, powerStats } from '../src/sim/data.ts';
import { initialState, wellbeingOf } from '../src/sim/engine.ts';

/** Build a city: initial starter map + population + extra buildings. */
function city(pop, specCounts = {}) {
  const s = initialState();
  s.population = pop;
  // FEAT-2326609711 inc1 (AC-12 regression pin, r2 fix follow-on): initialState()
  // defaults gridImportEnabled to TRUE (Design Ruling — new cities start on
  // external cover), and a COVERED deficit no longer raises the brownout alert
  // (isBrownoutActive() reads false while cover is on, data.ts SSOT). This
  // file's BUG-398 test below asserts the LEGACY deficit->alert boundary —
  // explicitly disable the toggle here, exactly like brownout.test.mjs's
  // city() fixture, so it keeps testing the toggle-off (legacy) path.
  s.gridImportEnabled = false;
  let id = 60000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return s;
}

const meter = (s, svcId) => {
  const m = serviceDemandOf(s).find((d) => d.id === svcId);
  assert.ok(m, `no demand meter for service "${svcId}"`);
  return m;
};
const covRow = (s, svcId) => {
  const r = serviceCoverageOf(s).find((c) => c.id === svcId);
  assert.ok(r, `no coverage row for service "${svcId}"`);
  return r;
};
const wbPart = (s, label) => {
  const p = wellbeingOf(s).parts.find((p) => p.label === label);
  assert.ok(p, `no wellbeing part "${label}"`);
  return p.value;
};

// ------------------------------------------------------------------
// BUG-398 — power meter sign matches physical reality
// ------------------------------------------------------------------

test('BUG-398: a big power SURPLUS reads as a surplus (negative), never a +100 shortfall', () => {
  // Reproduce the Y148 dump shape: pop ~314,000; capacity far exceeds need.
  // need = round(pop*0.012 + industrial*6) — add industry to lift need near
  // the dumped 23,997 MW; fusion plants supply a ~56,000 MW cap (a ~2.3× glut).
  const s = city(314000, { pow_fusion: 70, ind_factory: 3371 });
  const pw = powerStats(s);
  assert.ok(pw.cap > pw.need, `precondition: capacity must exceed need (${pw.cap} vs ${pw.need})`);
  assert.ok(pw.cap / pw.need >= 2, `precondition: a >2× glut like the dump (got ${(pw.cap / pw.need).toFixed(2)}×)`);

  const row = covRow(s, 'power');
  assert.equal(row.need, pw.need, 'power row need must be pw.need (not swapped)');
  assert.equal(row.cap, pw.cap, 'power row cap must be pw.cap (not swapped)');
  assert.ok(row.coverage > 1, `surplus ⇒ coverage cap/need > 1 (got ${row.coverage.toFixed(2)})`);

  const m = meter(s, 'power');
  assert.ok(m.value < 0, `a surplus MUST read negative (surplus), got ${m.value}`);
  assert.equal(m.value, -100, 'a >2× glut sits at the surplus end of the ±100 clamp');
  assert.ok(!m.alert, 'a surplus MUST NOT raise the brownout alert');
});

test('BUG-398: the power meter sign tracks the deficit/surplus boundary', () => {
  // Genuine deficit → positive shortfall + brownout alert.
  const deficit = city(314000, { pow_wind: 1, ind_factory: 3371 }); // 8 MW cap vs ~24,000 need
  const dm = meter(deficit, 'power');
  assert.ok(dm.value > 0, `a deficit MUST read positive (shortfall), got ${dm.value}`);
  assert.ok(dm.alert, 'a deficit MUST raise the brownout alert');

  // Exact balance (cap ≈ need) → no alert, non-positive.
  const balanced = city(1000, { pow_wind: 2 }); // need = round(12) = 12 MW; cap = 16 MW → slight surplus
  const bm = meter(balanced, 'power');
  assert.ok(!bm.alert, 'no deficit ⇒ no brownout alert');
  assert.ok(bm.value <= 0, `covered power must not read a shortfall, got ${bm.value}`);
});

// ------------------------------------------------------------------
// BUG-399 (facet c) — coverage counts by capability, not a hardcoded id
// ------------------------------------------------------------------

test('BUG-399: a Divisional HQ (pol_hq) raises the Police meter — spec-drift fix', () => {
  // pol_hq: kind 'police', served 60,000. A city policed ONLY by HQs must NOT
  // read a pegged shortfall just because the meter used to key on pol_station.
  const s = city(120000, { pol_hq: 2 }); // 2 × 60,000 = 120,000 served = pop
  const row = covRow(s, 'police');
  assert.equal(row.cap, 120000, 'pol_hq served must count toward police capacity');
  assert.ok(row.coverage >= 1, `full HQ coverage ⇒ coverage ≥ 1 (got ${row.coverage.toFixed(2)})`);

  const m = meter(s, 'police');
  assert.ok(m.value <= 0, `fully-policed city must NOT read a shortfall, got ${m.value}`);

  // And wellbeing (shared SSOT) must agree — Safety high, not floored at 0.
  assert.ok(wbPart(s, 'Safety') >= 95, `Safety wellbeing must track the same coverage, got ${wbPart(s, 'Safety')}`);
});

test('BUG-399: mixed station + HQ police capacity sums by kind', () => {
  const s = city(100000, { pol_station: 4, pol_hq: 1 }); // 4×10,000 + 60,000 = 100,000
  assert.equal(covRow(s, 'police').cap, 100000, 'station and HQ served must both count');
  assert.ok(meter(s, 'police').value <= 0, 'exactly-covered police reads no shortfall');
});
