// feat-inc4-reimagine-plan.test.mjs — FEAT-2326609779 inc4, THE RED BOX
// RE-PLAN. Aaron: "the red box needs to defag and reimmagine everything
// within it and optimise join".
//
// These are the acceptance asserts for consolidatorReplan.ts's four
// contracts, run against a SYNTHETIC MESSY BOX built to the task's own
// description: scattered 12 hospitals, 40 nurseries, fragmented minor roads,
// one rail stub, and 3 boundary ports. Before/after ASCII renders are printed
// (set REPLAN_RENDER=1) and asserted structurally.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  planBox,
  findPorts,
  renderBox,
  buildSteps,
  stepsForTick,
  progressOf,
  validatePlan,
  junctionCountOf,
  deadEndCountOf,
  tierLineOffsets,
  planCivicBlocks,
  keyOf,
  componentsOfTier,
  MAX_DEAD_END_TILES,
  REPLAN_STEPS_PER_TICK,
  LATTICE_PHASE_MODULUS,
} from '../src/sim/consolidatorReplan.ts';
import { TIER_ORDER, TIER_SPEC_ID, tileComponents } from '../src/sim/consolidatorLayout.ts';

const BOX = { x0: 100, y0: 100, w: 16, h: 16 };

/**
 * The ladder rungs this suite needs, shaped exactly like consolidator.ts's own
 * derived `consolidationLadder()` output plus the successor's catalogue
 * capacity/footprint. Numbers are the REAL catalogue's (data.ts:1936/1958 —
 * Kindergarten 30 places -> City Kindergarten 1,000 places; hospital 40,000
 * served -> Teaching Hospital 200,000 served), so GR#15 holds: nothing here
 * is an invented balance number.
 */
const RUNGS = [
  { from: 'edu_nursery', to: 'edu_nursery_city', groupSize: Math.floor(1000 / 30), toCapacity: 1000, toW: 3, toH: 3 },
  { from: 'hea_hospital', to: 'hea_teaching', groupSize: Math.floor(200000 / 40000), toCapacity: 200000, toW: 3, toH: 3 },
];

/**
 * The messy box. Deliberately fragmented — this is the "before" the defrag is
 * supposed to reimagine, not a tidy fixture the planner would trivially agree
 * with.
 */
function messyBox() {
  const contents = [];
  let id = 1;
  const add = (spec, x, y, extra = {}) =>
    contents.push({
      id: id++,
      spec,
      x,
      y,
      tier: extra.tier ?? null,
      residents: extra.residents ?? 0,
      jobs: extra.jobs ?? 0,
      capacity: extra.capacity ?? 0,
      protectedFromDemolition: extra.protectedFromDemolition ?? false,
    });

  // 12 scattered hospitals (capacity 40,000 each — the real spec's `served`).
  for (let i = 0; i < 12; i++) {
    add('hea_hospital', BOX.x0 + ((i * 5) % BOX.w), BOX.y0 + ((i * 3) % BOX.h), { capacity: 40000, jobs: 300 });
  }
  // 40 scattered nurseries (capacity 30 each — the real spec's `children`).
  for (let i = 0; i < 40; i++) {
    add('edu_nursery', BOX.x0 + ((i * 7 + 2) % BOX.w), BOX.y0 + ((i * 11 + 1) % BOX.h), { capacity: 30, jobs: 4 });
  }
  // Fragmented minor roads: four disjoint 2-tile stubs, none joined.
  for (const [sx, sy] of [[102, 103], [108, 111], [113, 105], [104, 113]]) {
    add('road', sx, sy, { tier: 'minor' });
    add('road', sx + 1, sy, { tier: 'minor' });
  }
  // One rail stub, floating in the middle, joined to nothing.
  add('rail', 106, 107, { tier: 'rail' });
  add('rail', 107, 107, { tier: 'rail' });
  return contents;
}

/** The 3 boundary ports: rail entering from the left, motorway from the top, a minor road from the right. */
function messyPorts() {
  const outside = new Map();
  outside.set(`${BOX.x0 - 1},${BOX.y0 + 7}`, 'rail');
  outside.set(`${BOX.x0 + 5},${BOX.y0 - 1}`, 'motorway');
  outside.set(`${BOX.x0 + BOX.w},${BOX.y0 + 12}`, 'minor');
  return findPorts(BOX, outside);
}

function plan(seed = 7) {
  return planBox({ box: BOX, contents: messyBox(), ports: messyPorts(), rungs: RUNGS, seed });
}

function show(title, text) {
  if (process.env.REPLAN_RENDER) {
    console.log(`\n--- ${title} ---\n${text}\n`);
  }
}

describe('inc4 A: the box HAS 3 ports and they are found deterministically', () => {
  test('findPorts locates all three crossings, sorted, with the right tiers', () => {
    const ports = messyPorts();
    assert.equal(ports.length, 3, 'three boundary crossings');
    const tiers = ports.map((p) => p.tier).sort();
    assert.deepEqual(tiers, ['minor', 'motorway', 'rail']);
    // Every port's inside tile really is inside the box and adjacent to its outside tile.
    for (const p of ports) {
      assert.ok(p.inside.x >= BOX.x0 && p.inside.x < BOX.x0 + BOX.w, 'inside x in box');
      assert.ok(p.inside.y >= BOX.y0 && p.inside.y < BOX.y0 + BOX.h, 'inside y in box');
      assert.equal(Math.abs(p.inside.x - p.outside.x) + Math.abs(p.inside.y - p.outside.y), 1, 'orthogonally adjacent');
    }
    // Deterministic ordering (GR#21): re-running yields an identical list.
    assert.deepEqual(messyPorts(), ports);
  });
});

describe('inc4 B: DEFRAG/REIMAGINE — one coherent whole-box plan', () => {
  test('the plan is a re-plan, not an extension of the mess', () => {
    const p = plan();
    show('BEFORE (current contents)', renderBox(BOX, { contents: messyBox() }));
    show('AFTER (target plan)', renderBox(BOX, { tierTiles: p.tierTiles, civic: p.civic }));

    assert.ok(p.metrics.planTiles > 0, 'the plan wants tiles');
    // THE re-plan proof: the tier target is computed from (box, ports, seed)
    // ALONE — it is byte-identical when the box's current contents are
    // deleted entirely. An INCREMENTAL EXTENDER (which is precisely what
    // inc3's consolidatorLayout.ts is, and precisely what the round-13/14
    // verdicts named) cannot have this property: its output is a function of
    // what is already standing. This is the assert that distinguishes a
    // re-plan from an extension.
    const empty = planBox({ box: BOX, contents: [], ports: messyPorts(), rungs: RUNGS, seed: 7 });
    assert.deepEqual(p.tierTiles, empty.tierTiles, 'the tier plan ignores the mess entirely — it is a RE-plan');
    // RETUNED 2026-09-06 (LEAD RULING: RAIL AND MOTORWAY ARE CITY-WIDE LINES).
    // These pins encoded port-ANCHORING — "rail runs a full-span line through
    // its own port row". That behaviour is DELETED: it was a self-feeding loop
    // (every line laid became a port for the next box, which invented another
    // line) measured at motorway/rail crossing 14x in one box and line capex
    // GBP 47M -> GBP 307M. The city-wide lattice (rail 64, motorway 32) is now
    // the ONLY source of those lines, and this 16-tile test box contains no
    // lattice multiple, so rail correctly plans NOTHING here and its port is a
    // pass-through. The pins assert the NEW contract.
    assert.deepEqual(p.tierTiles.rail, [], 'no rail lattice line falls in this box, so no rail is planned');
    assert.deepEqual(p.invariantFailures, [], 'a pass-through rail port is NOT a plan failure');
    // ...and the old fragmented mess is NOT reproduced: the four disjoint
    // minor stubs and the floating rail pair are not what the plan wants.
    const minorKeys = new Set(p.tierTiles.minor.map(keyOf));
    const oldStubTiles = ['102,103', '103,103', '108,111', '109,111', '113,105', '114,105', '104,113', '105,113'];
    const reproduced = oldStubTiles.filter((k) => minorKeys.has(k)).length;
    assert.ok(reproduced < oldStubTiles.length, 'the plan does not simply re-bless every old fragment');
  });

  test('the plan is a pure function of its inputs (GR#21 determinism)', () => {
    const a = plan(7);
    const b = plan(7);
    assert.deepEqual(a.tierTiles, b.tierTiles, 'same seed, identical tier tiles');
    assert.deepEqual(a.civic, b.civic, 'same seed, identical civic blocks');
    assert.deepEqual(a.steps, b.steps, 'same seed, identical step list');
    assert.deepEqual(a.metrics, b.metrics, 'same seed, identical metrics');
  });

  test('the plan carries no invariant failures', () => {
    const p = plan();
    assert.deepEqual(p.invariantFailures, [], `plan invariants: ${p.invariantFailures.join(' | ')}`);
  });
});

describe('inc4 C: OPTIMISE JOIN — ports, components, dead ends, junctions', () => {
  test('every tier inside the box is exactly ONE 4-connected component', () => {
    const p = plan();
    for (const tier of TIER_ORDER) {
      const tiles = p.tierTiles[tier];
      if (tiles.length === 0) continue;
      // Connectivity is asked WITH grade separation (a rail bridge over an
      // A-road does not cut the A-road) — see consolidatorReplan.ts's
      // resolveWholeBoxConflicts. Asking without it is the wrong question:
      // it counts a bridge as a severance.
      const comps = componentsOfTier(tiles, p.passThrough[tier]);
      const distinct = new Set(Array.from(comps.values())).size;
      assert.equal(distinct, 1, `${tier} must be ONE component, got ${distinct}`);
      assert.equal(p.metrics.componentsByTier[tier], 1, `${tier} metric agrees`);
    }
  });

  test('rail is one component and arterial components are <= 2', () => {
    const p = plan();
    // RETUNED 2026-09-06 (city-wide lines): rail is absent from this box by
    // design, so there is no rail component to count here.
    assert.equal(p.metrics.componentsByTier.rail, 0, 'no rail planned in this box');
    const arterials = p.metrics.componentsByTier.motorway + p.metrics.componentsByTier.dual;
    assert.ok(arterials <= 2, `arterial components ${arterials} must be <= 2`);
  });

  test('every port is connected — the outside network still reaches the box', () => {
    const p = plan();
    assert.equal(p.metrics.portsTotal, 3);
    assert.equal(p.metrics.portsConnected, 3, 'ALL ports connected');
    const portFailures = validatePlan({ box: BOX, tierTiles: p.tierTiles, civic: p.civic, ports: p.ports }).filter((f) =>
      f.startsWith('port '),
    );
    assert.deepEqual(portFailures, [], `port failures: ${portFailures.join(' | ')}`);
  });

  test('zero dead ends longer than one tile', () => {
    const p = plan();
    assert.equal(p.metrics.deadEnds, 0, 'no dead ends > 1 tile');
    assert.equal(deadEndCountOf(p.tierTiles, BOX), 0, 're-measured independently');
  });

  test('junction count is reported and is bounded by the plan size', () => {
    const p = plan();
    const j = junctionCountOf(p.tierTiles);
    assert.equal(p.metrics.junctionCount, j, 'metric matches an independent recount');
    assert.ok(j < p.metrics.planTiles, 'junctions are a minority of plan tiles, not every tile');
  });

  test('RED-PROOF: the port assert can actually fail', () => {
    // A plan with the rail spine deleted must FAIL the rail port's check —
    // proving the assert above is not vacuous.
    const p = plan();
    // RETUNED 2026-09-06: deleting rail can no longer orphan its port (a tier
    // with no line in the box is a pass-through by ruling). The RED-PROOF is
    // re-pointed at a tier the lattice DOES plan here — deleting minor, which
    // has real lines and a real minor port, must still be caught.
    // The RED-PROOF cannot work by EMPTYING a tier any more: an empty tier is
    // a pass-through by the 2026-09-06 ruling, so the port check is skipped by
    // design. It must instead keep the tier PRESENT but route it away from the
    // port — which is a genuinely bad plan, and must still be caught.
    // RETUNED AGAIN 2026-09-06 (the lattice-phase ruling): a port whose
    // OUTSIDE tile sits on the city-wide lattice is satisfied by the lattice
    // itself and is exempt from the port invariant by design (planPortSpurs
    // rule (f) and validatePlan agree, deliberately). The dogfood-shaped
    // fixture's own minor ports are all on-lattice, so re-using one here made
    // this RED-PROOF vacuous. It now strands a port that is genuinely
    // OFF-LATTICE — the only kind the invariant still speaks about — so the
    // proof measures the rule that is actually live.
    const offLattice = (t) => t.x % LATTICE_PHASE_MODULUS !== 0 && t.y % LATTICE_PHASE_MODULUS !== 0;
    const minorPort =
      p.ports.find((x) => x.tier === 'minor' && offLattice(x.outside)) ??
      // None in the fixture: synthesise one on the box's own west edge, one
      // row down from a lattice line so both its coordinates are off-lattice.
      { outside: { x: BOX.x0 - 1, y: BOX.y0 + 1 }, inside: { x: BOX.x0, y: BOX.y0 + 1 }, tier: 'minor' };
    assert.ok(minorPort, 'the fixture has a minor port to strand');
    assert.ok(offLattice(minorPort.outside), 'the stranded port is off-lattice, so the invariant still applies to it');
    const farCorner = [
      { x: BOX.x0, y: BOX.y0 },
      { x: BOX.x0 + 1, y: BOX.y0 },
      { x: BOX.x0 + 2, y: BOX.y0 },
    ].filter((t) => Math.abs(t.x - minorPort.inside.x) + Math.abs(t.y - minorPort.inside.y) > 2);
    const broken = {
      box: BOX,
      tierTiles: { rail: [], motorway: [], dual: [], aroad: [], minor: farCorner },
      civic: p.civic,
      ports: [minorPort],
    };
    const failures = validatePlan(broken).filter((f) => f.startsWith('port '));
    assert.ok(failures.length > 0, 'a PLANNED tier routed away from its port MUST be caught');
  });

  test('RED-PROOF: the dead-end assert can actually fail', () => {
    // A three-tile interior spur hanging off nothing is a real dead end.
    const spur = {
      rail: [],
      motorway: [],
      dual: [],
      aroad: [
        { x: BOX.x0 + 5, y: BOX.y0 + 5 },
        { x: BOX.x0 + 6, y: BOX.y0 + 5 },
        { x: BOX.x0 + 7, y: BOX.y0 + 5 },
      ],
      minor: [],
    };
    assert.ok(deadEndCountOf(spur, BOX) > 0, 'a floating interior spur MUST count as a dead end');
  });
});

describe('inc4 D: CONSERVATION — capacity never falls, and never mid-job', () => {
  test('the 12 scattered hospitals collapse into teaching hospitals per the LADDER', () => {
    const p = plan();
    const teaching = p.civic.filter((c) => c.spec === 'hea_teaching');
    const rung = RUNGS.find((r) => r.from === 'hea_hospital');
    // groupSize = floor(200,000 / 40,000) = 5, so 12 hospitals yield
    // floor(12/5) = 2 full groups (10 absorbed, 2 left standing until the box
    // gains more). The expectation is DERIVED from the catalogue's own ladder,
    // never a hardcoded count (GR#15).
    assert.equal(teaching.length, Math.floor(12 / rung.groupSize), 'group count derived from the ladder');
    for (const t of teaching) {
      assert.equal(t.replaces.length, rung.groupSize, 'each teaching hospital absorbs a full group');
      assert.equal(t.capacityProvided, 200000);
      assert.equal(t.capacityAbsorbed, rung.groupSize * 40000);
      assert.ok(t.capacityProvided >= t.capacityAbsorbed, 'capacity never falls');
    }
    // Aaron's own sentence: FEWER, BIGGER. 10 district hospitals become 2.
    assert.ok(teaching.length * rung.groupSize > teaching.length, '12 hospitals -> far fewer, far bigger');
  });

  test('the consolidated set is SMALLER than the originals and never loses capacity', () => {
    const p = plan();
    assert.ok(p.civic.length > 0, 'the plan consolidates something');
    assert.ok(p.metrics.civicOriginals > p.metrics.civicBlocks, 'fewer buildings after than before');
    for (const c of p.civic) {
      assert.ok(
        c.capacityProvided >= c.capacityAbsorbed,
        `${c.spec} provides ${c.capacityProvided} but absorbs ${c.capacityAbsorbed}`,
      );
    }
    assert.ok(p.metrics.capacityDelta >= 0, 'net capacity never falls');
  });

  test('40 nurseries collapse toward City Kindergartens at 1,000 places each', () => {
    const p = plan();
    const kinder = p.civic.filter((c) => c.spec === 'edu_nursery_city');
    const rung = RUNGS.find((r) => r.from === 'edu_nursery');
    assert.equal(kinder.length, Math.floor(40 / rung.groupSize), 'groups derived from the ladder, not hardcoded');
    for (const k of kinder) {
      assert.equal(k.replaces.length, rung.groupSize);
      assert.equal(k.capacityProvided, 1000);
      assert.ok(k.capacityProvided >= k.capacityAbsorbed);
    }
  });

  test('EVERY demolish step is blocked by its own replacement place step, OR is an explicit capacity-neutral sweep', () => {
    // FEAT-2326609779 inc4 close-out (BOW ruling, 2026-09-06 ~14:53/~17:15):
    // planStaleGridRemovals' orphan SWEEP (BUG-808) emits road-family
    // demolitions with no replacement place step to block on — a road tile
    // carries no residents/jobs, so it is capacity-neutral by construction,
    // unlike every OTHER demolish step here (a civic original), which always
    // waits on its consolidated successor. consolidatorReplan.ts now marks a
    // sweep explicitly (`sweep: true`, `blockedBy: null`) instead of merely
    // omitting the field, so the two cases can be told apart structurally
    // rather than by absence. Both branches are exercised by this fixture's
    // four fragmented minor stubs + floating rail pair, which the plan does
    // not reproduce (see inc4 B's own 'not simply re-bless every old
    // fragment' pin) and which the sweep therefore removes.
    const p = plan();
    const demolitions = p.steps.filter((s) => s.kind === 'demolish');
    assert.ok(demolitions.length > 0, 'there are demolitions to order');
    const sweeps = demolitions.filter((s) => s.sweep === true);
    const capacityBearing = demolitions.filter((s) => s.sweep !== true);
    assert.ok(sweeps.length > 0, 'setup: the fixture exercises the sweep branch');
    assert.ok(capacityBearing.length > 0, 'setup: the fixture exercises the capacity-bearing branch');
    for (let i = 0; i < p.steps.length; i++) {
      const s = p.steps[i];
      if (s.kind !== 'demolish') continue;
      if (s.sweep) {
        assert.equal(s.blockedBy, null, 'a sweep demolition never names a blocker — it has no place step to wait on');
        continue;
      }
      assert.equal(typeof s.blockedBy, 'number', 'a capacity-bearing demolition always names its blocker');
      assert.ok(s.blockedBy < i, 'the blocking place step comes STRICTLY earlier in the total order');
      assert.equal(p.steps[s.blockedBy].kind, 'place', 'the blocker is a place step');
    }
  });

  test('EVERY prefix of the step list is capacity-safe (never homeless mid-pass)', () => {
    const p = plan();
    // Walk every prefix and check the running capacity ledger never dips
    // below zero: a demolition may only ever be counted once its blocker has
    // already been counted. A sweep demolition (see the previous test) is
    // capacity-NEUTRAL by construction — a road-family tile, never a
    // residents/jobs-bearing building — so it is excluded from the
    // blocked-by-a-place-step walk entirely; only capacity-bearing
    // demolitions are required to have already had their blocker counted.
    for (let cut = 0; cut <= p.steps.length; cut++) {
      const done = new Set();
      let capacity = 0;
      for (let i = 0; i < cut; i++) {
        const s = p.steps[i];
        done.add(i);
        if (s.kind === 'place') {
          const block = p.civic.find((c) => c.spec === s.spec && c.x === s.x && c.y === s.y);
          capacity += block ? block.capacityProvided : 0;
        } else if (s.kind === 'demolish' && !s.sweep) {
          assert.ok(done.has(s.blockedBy), `prefix ${cut}: demolition at step ${i} ran before its replacement`);
        }
      }
      assert.ok(capacity >= 0, `prefix ${cut}: capacity ledger went negative`);
    }
  });
});

describe('inc4 E: INCREMENTAL, CONVERGENT EXECUTION', () => {
  test('work is bounded per tick and converges to the whole plan', () => {
    const p = plan();
    let done = 0;
    let ticks = 0;
    const seen = [];
    while (done < p.steps.length && ticks < 10000) {
      const slice = stepsForTick(p, done, REPLAN_STEPS_PER_TICK);
      assert.ok(slice.length <= REPLAN_STEPS_PER_TICK, 'never more than the per-tick budget');
      assert.ok(slice.length > 0, 'a non-converged job always has work');
      seen.push(...slice);
      done += slice.length;
      ticks += 1;
    }
    assert.ok(ticks > 1, 'the job genuinely takes multiple ticks (it is incremental, not one thunderous tick)');
    assert.equal(done, p.steps.length, 'it CONVERGES — every step eventually runs');
    assert.deepEqual(seen, p.steps, 'the slices reassemble into the exact plan order');
  });

  test('progress is reportable at every tick and is monotonic', () => {
    const p = plan();
    let last = progressOf(p, 0);
    assert.equal(last.converged, false);
    assert.equal(last.stepsTotal, p.steps.length);
    assert.equal(last.portsTotal, 3);
    assert.equal(last.portsVerified, 3);
    for (let done = 1; done <= p.steps.length; done++) {
      const now = progressOf(p, done);
      assert.ok(now.stepsDone >= last.stepsDone, 'stepsDone never goes backwards');
      assert.ok(now.tilesDone >= last.tilesDone, 'tilesDone never goes backwards');
      assert.ok(now.tilesDone <= now.planTiles, 'tilesDone never exceeds the plan');
      last = now;
    }
    assert.equal(last.converged, true, 'converged once every step is done');
  });

  test('a save/load mid-job is identical — the cursor is the whole state', () => {
    const p1 = plan();
    const done = 17;
    // "Save": nothing but the cursor. "Load": re-derive the plan from the
    // same inputs and resume. This is the property that makes the job
    // save-safe without persisting the plan itself.
    const p2 = plan();
    assert.deepEqual(p2.steps, p1.steps, 're-derived plan is identical');
    assert.deepEqual(stepsForTick(p2, done), stepsForTick(p1, done), 'the resumed slice is identical');
    assert.deepEqual(progressOf(p2, done), progressOf(p1, done), 'the resumed progress is identical');
  });
});

describe('inc4 F: ASCII render', () => {
  test('renders the box at the right size with a legible legend', () => {
    const before = renderBox(BOX, { contents: messyBox() });
    const p = plan();
    const after = renderBox(BOX, { tierTiles: p.tierTiles, civic: p.civic });
    for (const [name, text] of [['before', before], ['after', after]]) {
      const rows = text.split('\n');
      assert.equal(rows.length, BOX.h, `${name} has ${BOX.h} rows`);
      for (const r of rows) assert.equal(r.length, BOX.w, `${name} rows are ${BOX.w} wide`);
    }
    // The AFTER render must actually show the hierarchy glyphs.
    // RETUNED 2026-09-06 (city-wide lines): no rail lattice line falls in this
    // box, so the render legitimately shows none.
    assert.ok(!after.includes('='), 'no rail in a box the city-wide lattice does not route through');
    assert.ok(after.includes('A'), 'A-road grid visible');
    assert.ok(after.includes('C'), 'consolidated civic blocks visible');
    // The BEFORE render must show the mess it replaces.
    assert.ok(before.includes('#'), 'scattered civic buildings visible before');
    show('BEFORE', before);
    show('AFTER', after);
  });
});

describe('inc4 G: planner primitives', () => {
  test('tierLineOffsets anchors on a port when one exists', () => {
    const ports = messyPorts();
    // RETUNED 2026-09-06 (city-wide lines): ports no longer influence which
    // lines exist AT ALL — the lattice is the only source. This now pins that
    // ports are ignored, which is the property the ruling requires.
    assert.deepEqual(tierLineOffsets(BOX, 'rail', 'h', ports, 7), [], 'ports never create a rail line');
    assert.deepEqual(tierLineOffsets(BOX, 'motorway', 'v', ports, 7), [], 'ports never create a motorway line');
    assert.deepEqual(
      tierLineOffsets(BOX, 'minor', 'h', ports, 7),
      tierLineOffsets(BOX, 'minor', 'h', [], 7),
      'a tier the lattice DOES plan is unaffected by ports',
    );
  });

  // RETUNED 2026-09-05 (inc4 WIRING, measured on the real engine): this test
  // used to pin a BOX-CENTRE fallback ("no ports? put the spine down the
  // middle"). That behaviour is now DELETED, not merely retuned, because the
  // engine e2e proved it is a defect: the red box is the glide window, which
  // slides one tile per game day, so a box-relative centre asks for a
  // different column every day and the executor paints a fresh m20 spine one
  // tile over, daily — measured as a solid 7x9 block of motorway and a
  // 900-tick treasury at 18.8% of the layout-OFF control. Offsets are now an
  // ABSOLUTE-MAP-COORDINATE lattice so overlapping box positions AGREE, which
  // is what makes the job converge instead of repaint. The pin now asserts
  // the new contract in BOTH directions.
  test('tierLineOffsets uses an ABSOLUTE lattice, never a box-relative centre', () => {
    // rail spacing is 64 and no multiple of 64 falls inside y=100..115, so an
    // unported box legitimately gets NO rail line — never a minted centre one.
    const rows = tierLineOffsets(BOX, 'rail', 'h', [], 7);
    assert.deepEqual(rows, [], 'no lattice line in range and no port: no line at all');
    // Two OVERLAPPING boxes must agree on where a line goes — the property
    // that makes a sliding window converge rather than repaint.
    const a = tierLineOffsets({ x0: 100, y0: 100, w: 16, h: 16 }, 'aroad', 'v', [], 7);
    const b = tierLineOffsets({ x0: 101, y0: 100, w: 16, h: 16 }, 'aroad', 'v', [], 7);
    const overlap = a.filter((v) => v >= 101 && v < 116);
    assert.deepEqual(overlap, b.filter((v) => v < 116), 'overlapping boxes agree on every shared column');
    // The seed does NOT move the lattice (a seed-varying phase is the same
    // disagreement defect in another costume).
    assert.deepEqual(tierLineOffsets(BOX, 'aroad', 'v', [], 999), a, 'the lattice is seed-independent');
  });

  test('planCivicBlocks refuses a rung that would DELETE capacity', () => {
    const contents = [
      { id: 1, spec: 'x', x: 100, y: 100, tier: null, residents: 0, jobs: 0, capacity: 100, protectedFromDemolition: false },
      { id: 2, spec: 'x', x: 101, y: 100, tier: null, residents: 0, jobs: 0, capacity: 100, protectedFromDemolition: false },
    ];
    const bad = [{ from: 'x', to: 'y', groupSize: 2, toCapacity: 150, toW: 1, toH: 1 }];
    const out = planCivicBlocks(contents, bad, () => ({ x: 100, y: 100 }));
    assert.deepEqual(out, [], 'a successor smaller than the group it absorbs is REFUSED');
    const good = [{ from: 'x', to: 'y', groupSize: 2, toCapacity: 250, toW: 1, toH: 1 }];
    assert.equal(planCivicBlocks(contents, good, () => ({ x: 100, y: 100 })).length, 1, 'a sound rung is accepted');
  });

  test('protected content is never scheduled for demolition', () => {
    const contents = messyBox().map((c, i) => (i < 5 ? { ...c, protectedFromDemolition: true } : c));
    const p = planBox({ box: BOX, contents, ports: messyPorts(), rungs: RUNGS, seed: 7 });
    const protectedIds = new Set(contents.filter((c) => c.protectedFromDemolition).map((c) => c.id));
    for (const s of p.steps) {
      if (s.kind !== 'demolish') continue;
      assert.equal(protectedIds.has(s.id), false, `protected building ${s.id} was scheduled for demolition`);
    }
  });

  test('buildSteps skips tiles that already carry the right spec (no wasted work)', () => {
    const p = plan();
    const minorSpec = TIER_SPEC_ID.minor;
    const alreadyRight = p.tierTiles.minor.slice(0, 3).map((t, i) => ({
      id: 9000 + i,
      spec: minorSpec,
      x: t.x,
      y: t.y,
      tier: 'minor',
      residents: 0,
      jobs: 0,
      capacity: 0,
      protectedFromDemolition: false,
    }));
    const currentByKey = new Map(alreadyRight.map((c) => [keyOf(c), c]));
    const steps2 = buildSteps({ box: BOX, tierTiles: p.tierTiles, civic: [], contents: alreadyRight, currentByKey });
    // RETUNED 2026-09-06: rail is absent from this box, so the
    // already-correct-tile skip is exercised on minor, which the lattice does
    // plan here. The property under test is unchanged.
    const minorLays = steps2.filter((s) => s.kind === 'lay' && s.tier === 'minor');
    assert.equal(
      minorLays.length,
      p.tierTiles.minor.length - alreadyRight.length,
      'every already-correct tile costs no work',
    );
  });
});
