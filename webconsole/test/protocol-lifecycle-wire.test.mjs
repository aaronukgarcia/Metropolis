// protocol-lifecycle-wire.test.mjs -- FEAT-9995385597 AC-T5: Go and TS agree
// field for field on the lifecycle wire. The four golden frames written by
// internal/protocol (testdata/lifecycle) are decoded with the TS decoders and
// compared to the literal values the Go test builds the same files from; the
// closed enum sets and capability tokens are read out of lifecycle.go itself
// so a one-sided edit fails here.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { READ_COMMAND_KINDS, WRITE_COMMAND_KINDS } from '../src/sim/protocolClient.ts';
import { dirname, join } from 'node:path';
import {
  decodeWakingParams,
  decodeDrainingParams,
  decodeHibernatingParams,
  WAKE_PHASES,
  DRAIN_REASONS,
  HIBERNATE_REASONS,
  CAP_LIFECYCLE_WAKING,
  CAP_LIFECYCLE_DRAINING,
  CAP_LIFECYCLE_HIBERNATING,
  CAP_COMMAND_DEDUP,
} from '../src/sim/wire.ts';

const here = dirname(fileURLToPath(import.meta.url));
const goDir = join(here, '..', '..', 'internal', 'protocol');
const golden = (name) => JSON.parse(readFileSync(join(goDir, 'testdata', 'lifecycle', name), 'utf8'));
const goSource = readFileSync(join(goDir, 'lifecycle.go'), 'utf8');

/** Values of every `Name Type = "literal"` constant of one Go type. */
function goConsts(type) {
  const re = new RegExp(String.raw`^\s*\w+\s+${type}\s*=\s*"([^"]+)"`, 'gm');
  return [...goSource.matchAll(re)].map((m) => m[1]);
}

describe('Go golden frames decode identically in TS', () => {
  test('waking.golden.json', () => {
    assert.deepEqual(decodeWakingParams(golden('waking.golden.json')), { phase: 'replaying', pct: 62 });
  });
  test('waking_failed.golden.json carries code and ctx', () => {
    assert.deepEqual(decodeWakingParams(golden('waking_failed.golden.json')), {
      phase: 'failed',
      pct: 40,
      code: 'MET-P030',
      ctx: { city: 'default' },
    });
  });
  test('draining.golden.json', () => {
    assert.deepEqual(decodeDrainingParams(golden('draining.golden.json')), { reconnectAfterMs: 4000, reason: 'deploy' });
  });
  test('hibernating.golden.json', () => {
    assert.deepEqual(decodeHibernatingParams(golden('hibernating.golden.json')), { reason: 'idle' });
  });
  test('every golden key is a field the decoder knows (no silently dropped field)', () => {
    const known = {
      'waking.golden.json': ['phase', 'pct', 'code', 'ctx'],
      'waking_failed.golden.json': ['phase', 'pct', 'code', 'ctx'],
      'draining.golden.json': ['reconnectAfterMs', 'reason'],
      'hibernating.golden.json': ['reason'],
    };
    for (const [f, keys] of Object.entries(known)) {
      for (const k of Object.keys(golden(f))) assert.ok(keys.includes(k), `${f}: key ${k} unknown to the TS mirror`);
    }
  });
});

describe('closed sets and capability tokens match lifecycle.go', () => {
  test('wake phases (same members, same order)', () => {
    const go = goConsts('WakePhase');
    assert.ok(go.length >= 7, 'parsed the Go phases');
    assert.deepEqual([...WAKE_PHASES], go);
  });
  test('drain reasons', () => {
    const go = goConsts('DrainReason');
    assert.ok(go.length >= 4);
    assert.deepEqual([...DRAIN_REASONS], go);
  });
  test('hibernate reasons', () => {
    const go = goConsts('HibernateReason');
    assert.ok(go.length >= 2);
    assert.deepEqual([...HIBERNATE_REASONS], go);
  });
  test('capability tokens', () => {
    const go = goConsts('Capability');
    for (const c of [CAP_LIFECYCLE_WAKING, CAP_LIFECYCLE_DRAINING, CAP_LIFECYCLE_HIBERNATING, CAP_COMMAND_DEDUP]) {
      assert.ok(go.includes(c), `Go declares ${c}`);
    }
  });
});

describe('TS validation rejects what the Go Validate rejects', () => {
  test('waking: unknown phase, pct out of range, ready not at 100, non-object', () => {
    assert.equal(decodeWakingParams({ phase: 'nope', pct: 1 }), null);
    assert.equal(decodeWakingParams({ phase: 'queued', pct: 101 }), null);
    assert.equal(decodeWakingParams({ phase: 'queued', pct: -1 }), null);
    assert.equal(decodeWakingParams({ phase: 'ready', pct: 99 }), null);
    assert.equal(decodeWakingParams(null), null);
    assert.deepEqual(decodeWakingParams({ phase: 'ready', pct: 100 }), { phase: 'ready', pct: 100 });
  });
  test('draining: negative or fractional delay, unknown reason', () => {
    assert.equal(decodeDrainingParams({ reconnectAfterMs: -1, reason: 'deploy' }), null);
    assert.equal(decodeDrainingParams({ reconnectAfterMs: 1.5, reason: 'deploy' }), null);
    assert.equal(decodeDrainingParams({ reconnectAfterMs: 0, reason: 'hidden' }), null);
    assert.deepEqual(decodeDrainingParams({ reconnectAfterMs: 0, reason: 'budget' }), { reconnectAfterMs: 0, reason: 'budget' });
  });
  test('hibernating: hidden is client-initiated, never a server reason', () => {
    assert.equal(decodeHibernatingParams({ reason: 'hidden' }), null);
    assert.equal(decodeHibernatingParams({}), null);
  });
});

// FEAT-9995385597 r2 ruling 5: every Kind declared in Go's commands.go must be
// classified by the TS client as read or write (explicit lists, no prefix guess).
// r3 A4: matches both the grouped form (`KindX Kind = "X"` inside a const
// block) and the single-line form (`const KindX Kind = "X"`).
const parseGoKinds = (src) => [...src.matchAll(/^\s*(?:const\s+)?Kind\w+\s+Kind\s*=\s*"(\w+)"/gm)].map((m) => m[1]);

describe('command Kind classification parity (r2 ruling 5)', () => {
  test('r3 A4: the parser matches the grouped AND the single-line const form', () => {
    const src = 'const (\n\tKindA Kind = "A"\n\tKindB   Kind = "B"\n)\nconst KindC Kind = "C"\n  const  KindD  Kind="D"\n';
    assert.deepEqual(parseGoKinds(src), ['A', 'B', 'C', 'D']);
  });

  const commandsGo = readFileSync(join(goDir, 'commands.go'), 'utf8');
  const goKinds = parseGoKinds(commandsGo);

  test('the Go Kind list was actually parsed', () => {
    assert.ok(goKinds.length >= 18, `parsed only ${goKinds.length} Kinds`);
    assert.ok(goKinds.includes('Subscribe') && goKinds.includes('QueryCitizen'));
  });

  test('every Go Kind is named in exactly one of the TS read/write lists, and TS names no Kind Go lacks', () => {
    const named = [...READ_COMMAND_KINDS, ...WRITE_COMMAND_KINDS];
    for (const k of goKinds) {
      const n = named.filter((x) => x === k).length;
      assert.equal(n, 1, `Go Kind ${k} is named ${n} times by the TS classification (want exactly 1)`);
    }
    for (const k of named) assert.ok(goKinds.includes(k), `TS names ${k} which Go does not declare`);
  });

  test('the read set is the Go read-only kinds (inspect and query)', () => {
    assert.deepEqual([...READ_COMMAND_KINDS].sort(), goKinds.filter((k) => k === 'InspectEntity' || k.startsWith('Query')).sort());
  });
});
