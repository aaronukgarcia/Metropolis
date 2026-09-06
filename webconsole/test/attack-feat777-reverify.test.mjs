// attack-feat777-reverify.test.mjs — INDEPENDENT RE-VERIFY of the
// FEAT-2326609777 round follow-up (baseTick integrity stamp + basisMismatch
// recovery + RESYNC_EVERY_TICKS periodic full resync).
//
// The original round (attack-feat777-round.test.mjs) filed "no integrity
// check on the delta basis" as a P2: a same-shape-but-wrong-VALUE basis
// corrupted SILENTLY. These are the probes that must now behave differently.
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { reducer } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';
import {
  diffSimState,
  applyStateDelta,
  DeltaBasisMismatchError,
  RESYNC_EVERY_TICKS,
} from '../src/sim/simWorkerDelta.ts';

describe('RE-VERIFY: the same-shape-wrong-basis probe must now THROW, not corrupt', () => {
  test('a delta applied against a one-tick-stale basis of the SAME shape throws DeltaBasisMismatchError', () => {
    const s0 = buildScaleFixture({ buildingCount: 60, targetPopulation: 4_000, settleTicks: 1 });
    const s1 = reducer(s0, { type: 'tick' });
    const s2 = reducer(s1, { type: 'tick' });
    const delta = diffSimState(s1, s2); // basis is s1
    // Applying against s0 — same shape, same ids, same length, only values
    // (and the tick) differ. This is the exact shape a lost/skipped reply
    // leaves behind, and the shape the original round proved corrupted
    // silently.
    assert.throws(
      () => applyStateDelta(s0, delta),
      (err) => err instanceof DeltaBasisMismatchError && err.expectedBaseTick === s1.tick && err.actualBaseTick === s0.tick,
      'a stale basis must be REFUSED, never silently patched'
    );
    // And the honest control: the correct basis still works.
    assert.equal(applyStateDelta(s1, delta).tick, s2.tick);
  });

  test('HONEST LIMIT: two DIFFERENT states that share a tick number are NOT caught by baseTick alone', () => {
    const a = buildScaleFixture({ buildingCount: 40, targetPopulation: 3_000, settleTicks: 1 });
    const b = reducer(a, { type: 'debugFunds', amount: 12_345_00 }); // same tick, different state
    assert.equal(a.tick, b.tick, 'precondition: same tick');
    const next = reducer(a, { type: 'tick' });
    const delta = diffSimState(a, next);
    assert.doesNotThrow(() => applyStateDelta(b, delta), 'baseTick cannot see a same-tick divergence');
    // This is exactly why RESYNC_EVERY_TICKS exists as an independent bound.
    assert.ok(Number.isInteger(RESYNC_EVERY_TICKS) && RESYNC_EVERY_TICKS > 0);
  });
});

// ---------------------------------------------------------------------------
// A protocol harness carrying the NEW fields: baseTick, the basisMismatch
// reply, and the resync counter — transcribed from store.tsx's
// issueTickRequest/worker.onmessage and simWorker.ts's onmessage.
// ---------------------------------------------------------------------------
function makeProtocol(initialMain, { lieOnRequest = -1 } = {}) {
  return {
    main: initialMain,
    workerKnown: null,
    basis: null,
    workerCache: null,
    deltaSinceResync: 0,
    requestNo: 0,
    v891Count: 0,
    requestKinds: [],
    fullBytes: [],
    tick(apply = true) {
      this.requestNo++;
      const current = this.main;
      this.basis = current;
      const useDelta = this.workerKnown !== null && this.deltaSinceResync < RESYNC_EVERY_TICKS;
      let msg;
      if (useDelta) {
        const delta = diffSimState(this.workerKnown, current);
        // LyingWorker injection: corrupt the baseTick the worker will see.
        if (this.requestNo === lieOnRequest) delta.baseTick = delta.baseTick + 7;
        msg = { type: 'runTickDelta', delta: structuredClone(delta) };
        this.deltaSinceResync += 1;
      } else {
        msg = { type: 'runTick', state: structuredClone(current) };
        this.deltaSinceResync = 0;
        this.fullBytes.push(Buffer.byteLength(JSON.stringify(current)));
      }
      this.requestKinds.push(msg.type);

      // ---- worker side (simWorker.ts) ----
      let reply;
      if (msg.type === 'runTick') {
        const nx = reducer(msg.state, { type: 'tick' });
        this.workerCache = nx;
        reply = { type: 'tickResult', state: structuredClone(nx), deltaCapable: true };
      } else if (!this.workerCache || msg.delta.baseTick !== this.workerCache.tick) {
        const actualBaseTick = this.workerCache ? this.workerCache.tick : -1;
        this.workerCache = null;
        reply = { type: 'basisMismatch', expectedBaseTick: msg.delta.baseTick, actualBaseTick };
      } else {
        const pre = applyStateDelta(this.workerCache, msg.delta);
        const nx = reducer(pre, { type: 'tick' });
        const od = diffSimState(pre, nx);
        this.workerCache = nx;
        reply = { type: 'tickResultDelta', delta: structuredClone(od) };
      }

      // ---- main side (store.tsx worker.onmessage) ----
      if (reply.type === 'basisMismatch') {
        this.v891Count++;
        this.workerKnown = null;
        this.deltaSinceResync = 0;
        return { applied: false, kind: 'basisMismatch' };
      }
      let resultState;
      if (reply.type === 'tickResult') {
        resultState = reply.state;
        this.workerKnown = resultState;
      } else {
        try {
          resultState = applyStateDelta(this.basis, reply.delta);
        } catch (err) {
          if (!(err instanceof DeltaBasisMismatchError)) throw err;
          this.v891Count++;
          this.workerKnown = null;
          this.deltaSinceResync = 0;
          return { applied: false, kind: 'mainSideMismatch' };
        }
        this.workerKnown = resultState;
      }
      if (apply) this.main = reducer(this.main, { type: 'hydrate', state: resultState, source: 'tick' });
      return { applied: apply, kind: reply.type };
    },
  };
}

describe('RE-VERIFY: LyingWorker + recovery', () => {
  test('a lied-about baseTick is DETECTED, records exactly one MET-V891, does not advance the tick, and the next request is FULL', () => {
    const seed = buildScaleFixture({ buildingCount: 120, targetPopulation: 8_000, settleTicks: 1 });
    const p = makeProtocol(seed, { lieOnRequest: 4 });
    for (let i = 0; i < 3; i++) p.tick(true);
    const tickBefore = p.main.tick;
    assert.equal(p.v891Count, 0, 'no false positives on the honest ticks');

    const r = p.tick(true); // request #4 — the lie
    assert.equal(r.kind, 'basisMismatch', 'the lie must be caught, not computed');
    assert.equal(p.v891Count, 1, 'exactly ONE MET-V891 per detection, not a storm');
    assert.equal(p.main.tick, tickBefore, 'the sim tick must NOT advance (and must not double-advance) on a refused round trip');
    assert.equal(p.workerKnown, null, "main's cache belief is reset");
    assert.equal(p.workerCache, null, "the worker's own cache is reset");

    const r2 = p.tick(true); // request #5 — recovery
    assert.equal(p.requestKinds[4], 'runTick', 'the request after a basisMismatch must be a FULL runTick');
    assert.equal(r2.applied, true);
    assert.equal(p.main.tick, tickBefore + 1, 'the refused tick is re-run one interval later — no tick silently lost forever');
    assert.equal(p.v891Count, 1, 'recovery must not record a second error');

    // And the protocol keeps working afterwards.
    for (let i = 0; i < 5; i++) p.tick(true);
    assert.equal(p.main.tick, tickBefore + 6);
    assert.equal(p.v891Count, 1);
    assert.equal(JSON.stringify(p.workerKnown), JSON.stringify(p.workerCache));
  });
});

describe('RE-VERIFY: RESYNC_EVERY_TICKS forces a real full clone', () => {
  test(`request #1 and request #${RESYNC_EVERY_TICKS + 2} are full runTicks; every request between them is a delta`, () => {
    // Bound the pin so a mutated/disabled constant reds by ASSERTION in
    // milliseconds rather than by hanging the loop below (a hang is a red,
    // but a useless one). 512 is a generous ceiling: any value above it
    // would make the periodic-resync safety net too slow to catch a desync
    // inside a single dogfood session, which is its whole purpose.
    assert.ok(
      RESYNC_EVERY_TICKS <= 512,
      `RESYNC_EVERY_TICKS is ${RESYNC_EVERY_TICKS} — the periodic full-resync bound is effectively disabled, so a silent desync that baseTick cannot see would persist indefinitely`
    );
    const seed = buildScaleFixture({ buildingCount: 300, targetPopulation: 15_000, settleTicks: 1 });
    const p = makeProtocol(seed);
    for (let i = 0; i < RESYNC_EVERY_TICKS + 3; i++) p.tick(true);
    assert.equal(p.requestKinds[0], 'runTick', 'bootstrap is full');
    for (let i = 1; i <= RESYNC_EVERY_TICKS; i++) {
      assert.equal(p.requestKinds[i], 'runTickDelta', `request #${i + 1} must be a delta`);
    }
    assert.equal(
      p.requestKinds[RESYNC_EVERY_TICKS + 1],
      'runTick',
      `request #${RESYNC_EVERY_TICKS + 2} must be the forced periodic FULL resync`
    );
    // The resync must be a genuine full-state clone, not a token message.
    assert.equal(p.fullBytes.length, 2, 'exactly two full requests in this window');
    const fullBytes = p.fullBytes[1];
    assert.ok(fullBytes > 100_000, `the periodic resync must carry the whole state (measured ${fullBytes} bytes)`);
    // ...and the amortised cost of that resync is small.
    console.log(
      `[reverify] periodic resync payload = ${fullBytes} bytes, amortised over ${RESYNC_EVERY_TICKS} delta requests = ` +
        `${Math.round(fullBytes / RESYNC_EVERY_TICKS)} bytes/tick`
    );
    assert.equal(JSON.stringify(p.workerKnown), JSON.stringify(p.workerCache));
    assert.equal(p.v891Count, 0, 'a scheduled resync is NOT an error and must record nothing');
  });
});
