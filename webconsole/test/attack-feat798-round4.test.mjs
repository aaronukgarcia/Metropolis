// attack-feat798-round4.test.mjs — INDEPENDENT Destructive ROUND 4 against
// FEAT-2326609798 "unhappiness coupling" (attacker opus-round4-feat798-inc5,
// never the author). Round 3 REJECTed on BUG-892/893/894/895 (row 7613); the
// r4 rework closed all four. These pins are the attacker's own, added because
// the rework's own suite did NOT pin them:
//
//  (1) BUG-895 SURVIVOR — the medianCommuteMinutes clamp to the data-sourced
//      mental.commuteMinutesClampMax was implemented but UNPINNED: a scratch
//      mutant that deleted `Math.min(MENTAL.commuteMinutesClampMax, ...)`
//      from sanitizeTrafficSnapshot left the whole trafficWellbeing suite
//      GREEN (scoped runner exit 0). Test 1 below reds that exact mutant.
//  (2) THE DEFAULT-TRUE HOLE — emergencyPenaltyOf/trafficPenaltyWithConfig/
//      earlyGameScaledTrafficPenaltyWithConfig all default `ambulanceUnlocked`
//      to TRUE, so any FUTURE production call site that forgets the argument
//      silently re-enables the penalty BUG-892 removed (the exact regression
//      that cost round 3). Test 2 is a source guard: those three raw
//      functions must stay confined to trafficWellbeing.ts, and both
//      engine-facing wrappers must pass the real specUnlocked gate.
//  (3) THE CYCLE — trafficWellbeing.ts now imports specUnlocked from
//      engine.ts while engine.ts imports trafficWellbeing.ts. Test 3 loads
//      the pair in BOTH orders in a child process and fails on any TDZ /
//      partially-initialised-binding error.
//
// Run: node tools/test/scoped.mjs webconsole/test/attack-feat798-round4.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

import { sanitizeTrafficSnapshot } from '../src/sim/trafficWellbeing.ts';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SIM = path.join(HERE, '..', 'src', 'sim');
const TRAFFIC_WELLBEING_SRC = readFileSync(path.join(SIM, 'trafficWellbeing.ts'), 'utf8');

// GR#15: the expected cap is read from the mirrored SSOT, never typed here.
const MIRRORED_MENTAL = JSON.parse(
  readFileSync(path.join(SIM, 'traffic-data', 'wellbeing.json'), 'utf8')
).mental;

// ---------------------------------------------------------------------------
// (1) BUG-895 — the round-3 clamp fix, pinned (it was a live survivor)
// ---------------------------------------------------------------------------

test('BUG-895 SURVIVOR PIN: sanitizeTrafficSnapshot clamps medianCommuteMinutes to the DATA-SOURCED mental.commuteMinutesClampMax (a deleted Math.min left the whole r4 suite green)', () => {
  const cap = MIRRORED_MENTAL.commuteMinutesClampMax;
  assert.equal(typeof cap, 'number', 'precondition: the mirror must carry commuteMinutesClampMax');
  assert.ok(cap > 0 && Number.isFinite(cap), 'precondition: the cap must be a finite positive number');

  // The mutant this reds: `medianCommuteMinutes: Math.max(0, medianCommuteMinutes)`
  // (Math.min(MENTAL.commuteMinutesClampMax, ...) removed). Verified by
  // scratch copy during round 4: with the clamp removed this assertion is the
  // ONLY one in the repo that goes red.
  const huge = sanitizeTrafficSnapshot({
    tick: 1,
    medianCommuteMinutes: 1e9,
    gridlockShare: 0,
    coverageShare: null,
  });
  assert.equal(huge.medianCommuteMinutes, cap, 'an oversized medianCommuteMinutes must clamp to the sourced cap before it can reach debug.json');

  // Not a blanket overwrite: a value BELOW the cap must pass through exactly,
  // so a mutant that simply returns the cap unconditionally also reds.
  const normal = sanitizeTrafficSnapshot({
    tick: 1,
    medianCommuteMinutes: 41.5,
    gridlockShare: 0,
    coverageShare: null,
  });
  assert.equal(normal.medianCommuteMinutes, 41.5, 'a value below the cap must pass through unchanged (the clamp must not be a constant)');

  // The cap must genuinely come from the mirror, not a hardcoded literal that
  // happens to match: a value strictly between cap and 1e9 still clamps to cap.
  const justOver = sanitizeTrafficSnapshot({
    tick: 1,
    medianCommuteMinutes: cap + 0.5,
    gridlockShare: 0,
    coverageShare: null,
  });
  assert.equal(justOver.medianCommuteMinutes, cap, 'a value marginally over the cap must clamp to the cap exactly');

  // The floor half of the same clamp (pre-existing behaviour, kept honest).
  const negative = sanitizeTrafficSnapshot({
    tick: 1,
    medianCommuteMinutes: -7,
    gridlockShare: 0,
    coverageShare: null,
  });
  assert.equal(negative.medianCommuteMinutes, 0, 'a negative medianCommuteMinutes must floor at 0');
});

// ---------------------------------------------------------------------------
// (2) BUG-892 — the unlock gate cannot be re-opened by a defaulted argument
// ---------------------------------------------------------------------------

test('BUG-892 GATE GUARD: the three ambulanceUnlocked-defaulting functions stay confined to trafficWellbeing.ts, and BOTH engine-facing wrappers pass the real specUnlocked gate (a defaulted call site silently re-enables the r3 regression)', () => {
  const RAW = ['emergencyPenaltyOf', 'trafficPenaltyWithConfig', 'earlyGameScaledTrafficPenaltyWithConfig'];

  // (a) No OTHER src module may reference the raw, default-true functions.
  // Anything that needs the penalty must go through trafficPenaltyOf /
  // emergencyWellbeingPartOf, which apply the gate.
  function walk(dir, out) {
    for (const entry of readdirSync(dir)) {
      const p = path.join(dir, entry);
      const st = statSync(p);
      if (st.isDirectory()) walk(p, out);
      else if (p.endsWith('.ts') || p.endsWith('.tsx')) out.push(p);
    }
    return out;
  }
  const srcRoot = path.join(SIM, '..');
  const offenders = [];
  for (const file of walk(srcRoot, [])) {
    if (path.resolve(file) === path.resolve(path.join(SIM, 'trafficWellbeing.ts'))) continue;
    const text = readFileSync(file, 'utf8');
    for (const name of RAW) {
      // Ignore prose mentions inside comments by requiring a call/import shape.
      const re = new RegExp('(^|[^\\w.])' + name + '\\s*\\(', 'm');
      if (re.test(text)) offenders.push(path.relative(srcRoot, file) + ' -> ' + name);
    }
  }
  assert.deepEqual(
    offenders,
    [],
    'BUG-892: a src module outside trafficWellbeing.ts calls a raw penalty function whose ambulanceUnlocked argument defaults to TRUE — that silently re-enables the emergency penalty for a city that cannot yet build an ambulance station: ' +
      offenders.join(', ')
  );

  // (b) Both engine-facing wrappers must pass the REAL unlock check, not a
  // literal. Source-level pin: a future edit dropping the argument reds here
  // as well as in the behavioural BUG-892 test.
  const gate = /specUnlocked\(s,\s*SPECS\.hea_ambulance\)/g;
  const gateCount = (TRAFFIC_WELLBEING_SRC.match(gate) ?? []).length;
  assert.equal(gateCount, 2, 'BUG-892: exactly two call sites (trafficPenaltyOf and emergencyWellbeingPartOf) must pass specUnlocked(s, SPECS.hea_ambulance); got ' + gateCount);
  assert.ok(
    !/emergencyPenaltyOf\(\s*coverageShare\s*\)/.test(TRAFFIC_WELLBEING_SRC),
    'BUG-892: emergencyPenaltyOf must never be called with the unlock argument omitted inside trafficWellbeing.ts itself'
  );
});

// ---------------------------------------------------------------------------
// (3) The new engine.ts <-> trafficWellbeing.ts cycle loads in BOTH orders
// ---------------------------------------------------------------------------

test('BUG-892 CYCLE: engine.ts <-> trafficWellbeing.ts (the new specUnlocked import) initialises cleanly in BOTH module-load orders — no TDZ / partially-initialised binding', () => {
  const cwd = path.join(HERE, '..');
  const engineFirst =
    "const e = await import('./src/sim/engine.ts'); const t = await import('./src/sim/trafficWellbeing.ts'); " +
    "if (typeof t.trafficPenaltyOf !== 'function' || typeof e.initialState !== 'function') throw new Error('bindings missing'); " +
    "t.trafficPenaltyOf(e.initialState()); console.log('LOADED');";
  const wellbeingFirst =
    "const t = await import('./src/sim/trafficWellbeing.ts'); const e = await import('./src/sim/engine.ts'); " +
    "if (typeof t.trafficPenaltyOf !== 'function' || typeof e.initialState !== 'function') throw new Error('bindings missing'); " +
    "t.trafficPenaltyOf(e.initialState()); console.log('LOADED');";

  for (const [label, script] of [['engine-first', engineFirst], ['trafficWellbeing-first', wellbeingFirst]]) {
    const out = execFileSync(process.execPath, ['--input-type=module', '-e', script], {
      cwd,
      encoding: 'utf8',
      timeout: 120000,
    });
    assert.match(out, /LOADED/, `${label}: the cyclic import pair must evaluate without a TDZ error`);
  }
});
