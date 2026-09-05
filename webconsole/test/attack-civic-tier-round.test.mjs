// attack-civic-tier-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against FEAT-2326609761's civic-tier estate (CAPACITY_FIELD_ORDER jobs-last
// reorder, edu_nursery_city, hea_teaching served 120k->200k).
// Attacker: opus-round-civic-tier. NOT the author.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS } from '../src/sim/data.ts';
import {
  capacityFieldOf,
  familyKeyOf,
  capacityOf,
  consolidationLadder,
  isConsolidationSuccessor,
  groupSizeOf,
  CONSOLIDATOR_MIN_GROUP,
} from '../src/sim/consolidator.ts';

const OLD_ORDER = ['residents', 'jobs', 'children', 'served', 'mw', 'wasteCapacity', 'processCapacity', 'tourism', 'capacity'];
const NEW_ORDER = ['residents', 'children', 'served', 'mw', 'wasteCapacity', 'processCapacity', 'tourism', 'capacity', 'jobs'];

function fieldUnder(order, sp) {
  for (const f of order) {
    const v = sp[f];
    if (typeof v === 'number' && v !== 0) return f;
  }
  return null;
}

// ATTACK 1 — blast radius of the jobs-last reorder.
// Enumerate EVERY spec carrying `jobs` plus at least one other capacity
// field, and diff capacityFieldOf before/after. Any change on a spec where
// `jobs` is the TRUE capacity (offices, factories) is a regression.
test('ATTACK-1: jobs-last reorder blast radius is exactly the intended set', () => {
  const changed = [];
  for (const sp of Object.values(SPECS)) {
    const before = fieldUnder(OLD_ORDER, sp);
    const after = fieldUnder(NEW_ORDER, sp);
    if (before !== after) {
      changed.push({ id: sp.id, kind: sp.kind, before, after, jobs: sp.jobs });
    }
  }
  // eslint-disable-next-line no-console
  console.log('BLAST RADIUS (raw field order, ignoring kind exemptions):');
  for (const c of changed) console.log('  ', JSON.stringify(c));

  // Only specs where jobs is a SECONDARY field may change.
  for (const c of changed) {
    assert.equal(c.before, 'jobs', `${c.id}: only jobs->X changes are possible`);
    assert.notEqual(c.after, null, `${c.id}: must fall through to a real field`);
  }
});

// ATTACK 1b — the specs whose EFFECTIVE (post-exemption) capacityFieldOf
// changed, i.e. the ones that actually alter consolidation families.
test('ATTACK-1b: effective capacityFieldOf changes only for non-exempt specs', () => {
  const rows = [];
  for (const sp of Object.values(SPECS)) {
    const before = fieldUnder(OLD_ORDER, sp);
    const after = capacityFieldOf(sp); // live (new order + kind exemptions)
    const jobsIsOnly = typeof sp.jobs === 'number' && sp.jobs !== 0 && fieldUnder(NEW_ORDER.filter((f) => f !== 'jobs'), sp) == null;
    if (typeof sp.jobs === 'number' && sp.jobs !== 0 && !jobsIsOnly) {
      rows.push({ id: sp.id, kind: sp.kind, jobs: sp.jobs, rawBefore: before, effectiveAfter: after, family: familyKeyOf(sp) });
    }
  }
  // eslint-disable-next-line no-console
  console.log('SPECS WITH jobs + another capacity field:');
  for (const r of rows) console.log('  ', JSON.stringify(r));
  assert.ok(rows.length > 0, 'expected at least hea_teaching/uni');
});

// ATTACK 1c — jobs-only specs (offices/factories) MUST be untouched.
test('ATTACK-1c: jobs-only specs still key on jobs', () => {
  let n = 0;
  for (const sp of Object.values(SPECS)) {
    const others = fieldUnder(NEW_ORDER.filter((f) => f !== 'jobs'), sp);
    if (typeof sp.jobs === 'number' && sp.jobs !== 0 && others == null) {
      n++;
      const eff = capacityFieldOf(sp);
      assert.ok(eff === 'jobs' || eff === null, `${sp.id} jobs-only but capacityFieldOf=${eff}`);
    }
  }
  assert.ok(n > 5, `expected many jobs-only specs, got ${n}`);
});

// ATTACK 2 — the new/changed ladder rungs, printed in full for adjudication.
test('ATTACK-2: ladder rungs touching health + nursery families', () => {
  const ladder = consolidationLadder();
  const interesting = ladder.filter((e) => e.to === 'hea_teaching' || e.to === 'edu_nursery_city' || e.from === 'edu_nursery' || e.from === 'hea_clinic' || e.from === 'hea_ambulance' || e.from === 'hea_hospital');
  // eslint-disable-next-line no-console
  console.log('HEALTH/NURSERY RUNGS:');
  for (const e of interesting) console.log('  ', `${e.from} -> ${e.to} x${e.groupSize}`);
  const has = (f, t) => ladder.some((e) => e.from === f && e.to === t);
  assert.ok(has('hea_hospital', 'hea_teaching'), 'Aaron rung missing');
  assert.ok(has('edu_nursery', 'edu_nursery_city'), 'kindergarten rung missing');
});

// ATTACK 2b — SIDE-EFFECT RUNGS: clinic/ambulance -> teaching hospital.
// Adjudication evidence for the lead: report group sizes.
test('ATTACK-2b: side-effect rungs enumerated with group sizes', () => {
  const ladder = consolidationLadder();
  const side = ladder.filter((e) => e.to === 'hea_teaching' && e.from !== 'hea_hospital');
  // eslint-disable-next-line no-console
  console.log('SIDE-EFFECT RUNGS INTO hea_teaching:');
  for (const e of side) {
    const a = SPECS[e.from];
    console.log('  ', `${e.from}(${a.desc}) cap=${capacityOf(a)} -> hea_teaching x${e.groupSize}`);
  }
});

// ATTACK 3 — full ladder diff vs a re-derivation under the OLD field order.
// Proves the reorder's ONLY ladder consequences are the health/nursery ones.
test('ATTACK-3: full ladder delta under old vs new field order', () => {
  const EXEMPT_KINDS = new Set(['road', 'rail', 'landmark', 'water_body', 'park_path']);
  // Re-derive using the module's own rules but the OLD order. We cannot
  // re-import the module with a patched constant, so replicate the five
  // rules against fieldUnder(OLD_ORDER) — the only differing input.
  const specs = Object.values(SPECS);
  const oldField = (sp) => (capacityFieldOf(sp) === null ? null : fieldUnder(OLD_ORDER, sp));
  // F1 (2026-09-05): `careTier` is folded into BOTH the old- and new-order
  // family derivations here — it is an orthogonal data-driven discriminator
  // (the role-separation fix), not a consequence of the field-order reorder
  // this test is isolating. Including it symmetrically keeps this test's
  // original purpose intact (prove the REORDER alone removes nothing) while
  // still reflecting the real, intentional hea_clinic->hea_hospital removal
  // that F1 causes independently — see HAND RECOST 3 SUPERSEDED in
  // attack-consolidator-inc1-round.test.mjs for that removal's own proof.
  const oldFamily = (sp) => `${sp.kind}|${oldField(sp) ?? ''}|${sp.tag ?? ''}|${sp.stage ?? ''}|${sp.careTier ?? ''}`;
  const oldCap = (sp) => {
    if (sp.capacityTiers && sp.capacityTiers.length > 0) return sp.capacityTiers[0];
    const f = oldField(sp);
    return f == null ? 0 : (typeof sp[f] === 'number' ? sp[f] : 0);
  };
  const EXEMPT_IDS = new Set(['hea_eldercare']);
  const oldLadder = [];
  for (const a of specs) {
    if (oldField(a) == null) continue;
    for (const b of specs) {
      if (oldField(b) == null) continue;
      if (a.id === b.id) continue;
      if (EXEMPT_IDS.has(a.id) || EXEMPT_IDS.has(b.id)) continue;
      if (oldFamily(a) !== oldFamily(b)) continue;
      const ca = oldCap(a), cb = oldCap(b);
      if (ca <= 0 || cb <= 0) continue;
      if (cb < CONSOLIDATOR_MIN_GROUP * ca) continue;
      const da = ca / (a.w * a.h), db = cb / (b.w * b.h);
      if (db < da) continue;
      if (b.tag === 'pollution' && a.tag !== 'pollution') continue;
      oldLadder.push(`${a.id}->${b.id}`);
    }
  }
  const newLadder = consolidationLadder().map((e) => `${e.from}->${e.to}`);
  const oldSet = new Set(oldLadder), newSet = new Set(newLadder);
  const added = newLadder.filter((r) => !oldSet.has(r));
  const removed = oldLadder.filter((r) => !newSet.has(r));
  // eslint-disable-next-line no-console
  console.log('LADDER ADDED:', added);
  // eslint-disable-next-line no-console
  console.log('LADDER REMOVED:', removed);
  assert.equal(removed.length, 0, `reorder REMOVED rungs: ${removed.join(', ')}`);
});

// ATTACK 4 — determinism (GR#21): ladder derivation is order-independent.
test('ATTACK-4: ladder is stable and order-independent', () => {
  const a = consolidationLadder().map((e) => `${e.from}->${e.to}:${e.groupSize}`);
  const b = consolidationLadder().map((e) => `${e.from}->${e.to}:${e.groupSize}`);
  assert.deepEqual(a, b);
  // Independently re-derive by iterating a shuffled spec list.
  const specs = Object.values(SPECS);
  const shuffled = [...specs].sort((x, y) => (x.id > y.id ? -1 : 1));
  const rederived = [];
  for (const x of shuffled) {
    if (capacityFieldOf(x) == null) continue;
    for (const y of shuffled) {
      if (capacityFieldOf(y) == null) continue;
      if (isConsolidationSuccessor(x, y)) rederived.push(`${x.id}->${y.id}:${groupSizeOf(x, y)}`);
    }
  }
  rederived.sort();
  assert.deepEqual([...a].sort(), rederived);
});

// ATTACK 5 — City Kindergarten catalogue invariants.
test('ATTACK-5: edu_nursery_city catalogue integrity', () => {
  const sp = SPECS.edu_nursery_city;
  assert.ok(sp, 'spec missing');
  assert.equal(sp.stage, 'nursery');
  assert.equal(sp.kind, 'school');
  assert.equal(sp.children, 1000);
  assert.equal(sp.w * sp.h, 9);
  assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length > 0);
  assert.equal(sp.capacityTiers[0], 1000, 'tier-0 must equal base children');
  // must NOT be a successor of a different stage
  assert.equal(isConsolidationSuccessor(SPECS.edu_primary, sp), false);
  assert.equal(isConsolidationSuccessor(SPECS.edu_city, sp), false);
  // density never falls
  assert.ok(1000 / 9 > SPECS.edu_nursery.children / (SPECS.edu_nursery.w * SPECS.edu_nursery.h));
});

// ATTACK 6 — hea_teaching 200k consistency.
test('ATTACK-6: hea_teaching served/tiers/desc/jobs consistent', () => {
  const sp = SPECS.hea_teaching;
  assert.equal(sp.served, 200000);
  assert.equal(sp.capacityTiers[0], 200000, 'ladder tier-0 must track served');
  assert.equal(sp.jobs, 1450, 'jobs must stay grandfathered flat');
  assert.match(sp.blurb, /200,000/, `blurb still says: ${sp.blurb}`);
  assert.equal(capacityFieldOf(sp), 'served');
  assert.equal(capacityOf(sp), 200000);
});

// ATTACK 7 — no stale 120000 hea_teaching literal anywhere in src.
test('ATTACK-7: no stale hea_teaching 120k capacity assumption in src', async () => {
  const fs = await import('node:fs');
  const path = await import('node:path');
  const root = path.resolve(new URL('../src', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'));
  const hits = [];
  const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      // src/generated/version.ts is a GENERATED git-log dump — commit
      // subjects there are history, not live code assumptions.
      if (e.isDirectory()) { if (e.name !== 'generated') walk(p); }
      else if (/\.(ts|tsx)$/.test(e.name)) {
        const txt = fs.readFileSync(p, 'utf8');
        txt.split('\n').forEach((line, i) => {
          if (/120000|120,000/.test(line) && /teaching/i.test(line)) hits.push(`${p}:${i + 1}: ${line.trim().slice(0, 120)}`);
        });
      }
    }
  };
  walk(root);
  // eslint-disable-next-line no-console
  console.log('STALE 120k+teaching HITS:', hits);
  const nonComment = hits.filter((h) => !/:\s*(\*|\/\/|\/\*)/.test(h));
  assert.deepEqual(nonComment, [], `live code still assumes 120k: ${nonComment.join('\n')}`);
});
