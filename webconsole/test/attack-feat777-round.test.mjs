// attack-feat777-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND for
// FEAT-2326609777 (delta-sync for the Web Worker tick offload).
//
// The lane's own feat-2326609777-delta-sync.test.mjs only ever round-trips
// applyStateDelta(base, diffSimState(base, next)) with the SAME `base` object
// on both sides. That cannot catch the class of defect this protocol is
// actually exposed to, because in production the two sides hold SEPARATE
// object graphs (structuredClone across postMessage) and the diff's
// "unchanged == same reference" premise is evaluated on each side's OWN
// graph. This file models the real two-sided protocol faithfully — main's
// workerKnownStateRef / pendingRequestBasisStateRef and the worker's
// cachedState, with a real structuredClone at every boundary crossing — and
// drives it through the full action mix over 200+ ticks.
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { reducer } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';
import { diffSimState, applyStateDelta, diffBuildings } from '../src/sim/simWorkerDelta.ts';

// ---------------------------------------------------------------------------
// A faithful transcription of the production protocol (store.tsx's
// issueTickRequest + worker.onmessage, simWorker.ts's onmessage). Any
// divergence from production here would make this harness vacuous, so it is
// deliberately written as the same branches in the same order.
// ---------------------------------------------------------------------------
function makeProtocol(initialMain) {
  return {
    main: initialMain,
    workerKnown: null, // store.tsx workerKnownStateRef
    basis: null, // store.tsx pendingRequestBasisStateRef
    workerCache: null, // simWorker.ts cachedState
    bytesToWorker: 0,
    bytesToMain: 0,
    fullClonesToWorker: 0,
    /** One complete tick round trip. `apply` false models a superseded reply. */
    tick(apply = true) {
      const current = this.main;
      this.basis = current;
      let reply;
      if (this.workerKnown) {
        const delta = diffSimState(this.workerKnown, current);
        this.bytesToWorker += Buffer.byteLength(JSON.stringify(delta));
        const wire = structuredClone(delta); // postMessage
        const preTick = applyStateDelta(this.workerCache, wire);
        const nextState = reducer(preTick, { type: 'tick' });
        const outDelta = diffSimState(preTick, nextState);
        this.workerCache = nextState;
        this.bytesToMain += Buffer.byteLength(JSON.stringify(outDelta));
        reply = { type: 'tickResultDelta', delta: structuredClone(outDelta) };
      } else {
        const wire = structuredClone(current);
        this.bytesToWorker += Buffer.byteLength(JSON.stringify(current));
        this.fullClonesToWorker++;
        const nextState = reducer(wire, { type: 'tick' });
        this.workerCache = nextState;
        reply = { type: 'tickResult', state: structuredClone(nextState), deltaCapable: true };
      }
      let resultState;
      if (reply.type === 'tickResult') {
        resultState = reply.state;
        this.workerKnown = resultState;
      } else {
        resultState = applyStateDelta(this.basis, reply.delta);
        this.workerKnown = resultState;
      }
      if (apply) this.main = reducer(this.main, { type: 'hydrate', state: resultState, source: 'tick' });
      return resultState;
    },
    /** A player/UI action landing on the main thread between ticks. */
    act(action) {
      this.main = reducer(this.main, action);
    },
  };
}

/** Deep value comparison that ignores object identity entirely. */
function sameValue(a, b) {
  return JSON.stringify(a) === JSON.stringify(b);
}

describe('ATTACK FEAT-2326609777: two-sided protocol with a real cross-boundary clone', () => {
  test('200 ticks with placements, bulldozes, consolidator, level-up funds, a mid-run LOAD: main == control, worker cache == main belief', () => {
    const seed = buildScaleFixture({ buildingCount: 900, targetPopulation: 60_000, settleTicks: 2 });
    const p = makeProtocol(seed);
    let control = seed;

    // A savepoint captured mid-run, replayed later as a Load Save (hydrate
    // source 'load') to prove a state hand-off that is NOT a tick result
    // cannot desync the two caches.
    let savepoint = null;

    const actions = [];
    const doBoth = (action) => {
      p.act(action);
      control = reducer(control, action);
      actions.push(action.type);
    };

    for (let i = 0; i < 200; i++) {
      // The control chain must see exactly the ticks the protocol applies.
      p.tick(true);
      control = reducer(control, { type: 'tick' });
      control = reducer(control, { type: 'hydrate', state: control, source: 'tick' });

      if (i === 10) doBoth({ type: 'debugFunds', amount: 5_000_000_00 });
      if (i === 12) doBoth({ type: 'toggleConsolidator' });
      if (i === 20) doBoth({ type: 'debugXp', amount: 50_000 });
      if (i === 25) doBoth({ type: 'unlockAll' });
      // Placements (append -> orderChanged fallback) and bulldozes
      // (filter -> removal fallback), spread through the run.
      if (i > 30 && i % 17 === 0) doBoth({ type: 'place', spec: 'road', x: 400 + i, y: 400 });
      if (i > 40 && i % 23 === 0) doBoth({ type: 'bulldoze', x: 400 + (i - 6), y: 400 });
      if (i === 60) doBoth({ type: 'tax', which: 'residential', rate: 0.12 });
      if (i === 80) doBoth({ type: 'loan' });
      if (i === 100) savepoint = structuredClone(p.main);
      if (i === 130 && savepoint) {
        // Load Save: BOTH sides' main-thread state jumps to a state whose
        // building objects are all brand-new references with the same ids.
        doBoth({ type: 'hydrate', state: structuredClone(savepoint), source: 'load' });
      }
      if (i === 150) doBoth({ type: 'consolidatorUndo' });

      assert.ok(
        sameValue(p.main, control),
        `tick ${i}: delta-protocol main state diverged from the direct reducer control chain (actions so far: ${actions.join(',')})`
      );
      assert.ok(
        sameValue(p.workerKnown, p.workerCache),
        `tick ${i}: main's belief about the worker's cache diverged from the worker's ACTUAL cache`
      );
    }
    // The protocol must have genuinely exercised the fallback + full paths.
    assert.equal(p.fullClonesToWorker, 1, 'only the very first request may be a full clone');
    assert.ok(p.bytesToWorker > 0 && p.bytesToMain > 0);
    console.log(
      `[attack] 200 ticks: main->worker ${p.bytesToWorker} bytes, worker->main ${p.bytesToMain} bytes, ` +
        `full-state clone would have been ~${200 * Buffer.byteLength(JSON.stringify(p.main))}`
    );
  });

  test('superseded replies (worker result discarded) self-heal: 60 ticks, half discarded', () => {
    const seed = buildScaleFixture({ buildingCount: 400, targetPopulation: 20_000, settleTicks: 1 });
    const p = makeProtocol(seed);
    for (let i = 0; i < 60; i++) {
      const applyIt = i % 2 === 0;
      p.tick(applyIt);
      if (i % 7 === 3) p.act({ type: 'place', spec: 'road', x: 300 + i, y: 300 });
      assert.ok(
        sameValue(p.workerKnown, p.workerCache),
        `tick ${i}: after a ${applyIt ? 'applied' : 'DISCARDED'} reply the two caches diverged`
      );
    }
    // And after all that, the two sides still agree and a normal tick lands.
    const before = p.main.tick;
    p.tick(true);
    assert.equal(p.main.tick, before + 1);
  });
});

describe('ATTACK FEAT-2326609777: in-place mutation is INVISIBLE to a reference diff', () => {
  test('MUTATION-PROVE: an in-place field write on a shared building object silently desyncs the two sides', () => {
    const base = [
      { id: 1, spec: 'road', x: 1, y: 1 },
      { id: 2, spec: 'road', x: 2, y: 2 },
    ];
    // The reducer's contract is `map` + spread. Simulate a hypothetical future
    // reducer path that instead mutates in place and returns the SAME array.
    const next = base;
    next[1].capacityTier = 9;
    const delta = diffBuildings(base, next);
    assert.equal(delta.changed.length, 0, 'an in-place mutation produces an EMPTY diff — this is the load-bearing risk');
    // The worker's own (cloned) copy would therefore never learn about it.
    const workerCopy = structuredClone([
      { id: 1, spec: 'road', x: 1, y: 1 },
      { id: 2, spec: 'road', x: 2, y: 2 },
    ]);
    const reconstructed = workerCopy.map((b) => b); // apply of an empty delta
    assert.notEqual(reconstructed[1].capacityTier, 9, 'confirmed: the receiver never sees an in-place mutation');
  });
});

describe('ATTACK FEAT-2326609777: no integrity check on the delta basis', () => {
  test('a delta applied against a one-tick-stale basis of the SAME shape corrupts SILENTLY (no throw, no detection)', () => {
    // Same ids, same length, same order — only field VALUES differ. This is
    // exactly the shape a lost/skipped reply would leave behind.
    const staleBase = [
      { id: 1, spec: 'road', x: 1, y: 1, capacityTier: 0 },
      { id: 2, spec: 'road', x: 2, y: 2, capacityTier: 0 },
    ];
    const trueBase = [
      { id: 1, spec: 'road', x: 1, y: 1, capacityTier: 5 },
      { id: 2, spec: 'road', x: 2, y: 2, capacityTier: 0 },
    ];
    const next = [trueBase[0], { ...trueBase[1], capacityTier: 1 }];
    const delta = diffBuildings(trueBase, next);
    // Applied against the WRONG (stale) base: no throw, no signal — building 1
    // silently keeps capacityTier 0 instead of 5, forever.
    const wrong = staleBase.map((b) => (delta.changed.find((c) => c.id === b.id) ?? b));
    assert.equal(wrong[0].capacityTier, 0);
    assert.notDeepEqual(wrong, next, 'silent corruption: same-shape wrong basis is NOT detected by the delta protocol');
  });
});

describe('ATTACK FEAT-2326609777: cost of the O(n) diff scan at dogfood scale', () => {
  test('diffBuildings on ~38k buildings is algorithmically cheap (reference scan only)', () => {
    const big = [];
    for (let i = 0; i < 38_000; i++) big.push({ id: i, spec: 'road', x: i % 200, y: (i / 200) | 0 });
    const next = big.map((b, i) => (i === 17 ? { ...b, capacityTier: 1 } : b));
    const t0 = process.hrtime.bigint();
    let d;
    for (let r = 0; r < 20; r++) d = diffBuildings(big, next);
    const ms = Number(process.hrtime.bigint() - t0) / 1e6 / 20;
    assert.equal(d.changed.length, 1);
    assert.equal(d.orderChanged, false);
    console.log(`[attack] diffBuildings over 38,000 buildings: ${ms.toFixed(2)} ms/call (informational, not a gate)`);
    // Structural (non-wall-clock) assertion: the delta carries ONE building,
    // not 38,000 — that is the property the feature actually promises.
    assert.ok(Buffer.byteLength(JSON.stringify(d)) < Buffer.byteLength(JSON.stringify(next)) / 1000);
  });

  test('the reorder fallback ships an id-order array of full length — quantify it at 38k', () => {
    const big = [];
    for (let i = 0; i < 38_000; i++) big.push({ id: i, spec: 'road', x: i % 200, y: (i / 200) | 0 });
    const next = [...big, { id: 999_999, spec: 'road', x: 0, y: 0 }]; // one placement
    const d = diffBuildings(big, next);
    assert.equal(d.orderChanged, true);
    assert.equal(d.order.length, 38_001);
    const orderBytes = Buffer.byteLength(JSON.stringify(d.order));
    const fullBytes = Buffer.byteLength(JSON.stringify(next));
    console.log(
      `[attack] placement tick at 38k: order array ${orderBytes} bytes vs full array ${fullBytes} bytes ` +
        `(${((orderBytes / fullBytes) * 100).toFixed(1)}% of a full clone)`
    );
    assert.ok(orderBytes < fullBytes / 5, 'the fallback must still be a large win over a full clone');
  });
});
