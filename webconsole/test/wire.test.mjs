// wire.test.mjs — FEAT-1972079852 increment 1 (AC-2/AC-6/AC-18): the
// TypeScript mirror of int.protocol's wire schema decodes a sample
// protocol.Delta JSON carrying "f2.finance"'s balanceSheet patch, and
// rejects a structurally wrong/foreign patch rather than silently
// accepting it.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  PROTOCOL_VERSION,
  FINANCE_VIEW_NAME,
  FINANCE_SCHEMA_VERSION,
  isFinanceBalanceSheetPatch,
  decodeFinanceBalanceSheetPatch,
  normalizeProtocolVersion,
  parseWireVersion,
} from '../src/sim/wire.ts';

describe('wire.ts constants', () => {
  test('PROTOCOL_VERSION mirrors envelope.go', () => {
    assert.equal(PROTOCOL_VERSION, '1.0');
  });
  test('FINANCE_VIEW_NAME mirrors compose.go financeViewSubscriptionName value', () => {
    assert.equal(FINANCE_VIEW_NAME, 'f2.finance');
  });
  test('FINANCE_SCHEMA_VERSION mirrors finance_publish.go financeWireSchemaVersion', () => {
    assert.equal(FINANCE_SCHEMA_VERSION, 1);
  });
});

// A baseline fixture mirroring exactly what finance_publish.go's
// buildFinanceBalanceSheetPatch produces (json.Marshal'd, then
// JSON.parse'd — a real protocol.Delta's `patch` field arrives already
// parsed by the wire layer, per json.RawMessage's nested-object
// convention over the wsserver JSON-RPC transport).
function sampleDeltaJSON() {
  return {
    subscriptionId: 'sub-1',
    tick: 42,
    seq: 1,
    patch: {
      schemaVersion: 1,
      balanceSheet: {
        assets: [
          { label: 'Treasury', valueMicropounds: 5_000_000_000 },
          { label: 'Reserves', valueMicropounds: 0 },
        ],
        liabilities: [{ label: 'Outstanding Debt', valueMicropounds: 1_000_000_000 }],
        netWorth: 4_000_000_000,
      },
    },
  };
}

describe('decodeFinanceBalanceSheetPatch (AC-2)', () => {
  test('decodes a well-formed sample Delta.patch matching the baseline fixture', () => {
    const delta = sampleDeltaJSON();
    const decoded = decodeFinanceBalanceSheetPatch(delta.patch);
    assert.ok(decoded);
    assert.equal(decoded.schemaVersion, 1);
    assert.equal(decoded.balanceSheet.netWorth, 4_000_000_000);
    assert.equal(decoded.balanceSheet.assets.length, 2);
    assert.equal(decoded.balanceSheet.assets[0].label, 'Treasury');
    assert.equal(decoded.balanceSheet.liabilities[0].valueMicropounds, 1_000_000_000);
  });

  test('every field this code reads survives a JSON round-trip byte-for-byte', () => {
    const delta = sampleDeltaJSON();
    const roundTripped = JSON.parse(JSON.stringify(delta));
    const decoded = decodeFinanceBalanceSheetPatch(roundTripped.patch);
    assert.deepEqual(decoded, delta.patch);
  });

  test('rejects null/undefined patch', () => {
    assert.equal(decodeFinanceBalanceSheetPatch(null), null);
    assert.equal(decodeFinanceBalanceSheetPatch(undefined), null);
  });

  test('rejects a patch missing schemaVersion (foreign/malformed shape)', () => {
    assert.equal(decodeFinanceBalanceSheetPatch({ balanceSheet: { assets: [], liabilities: [], netWorth: 0 } }), null);
  });

  test('rejects a patch whose balanceSheet is missing required numeric netWorth', () => {
    assert.equal(
      decodeFinanceBalanceSheetPatch({ schemaVersion: 1, balanceSheet: { assets: [], liabilities: [] } }),
      null,
    );
  });

  test('rejects a patch whose assets/liabilities are not arrays', () => {
    assert.equal(
      decodeFinanceBalanceSheetPatch({
        schemaVersion: 1,
        balanceSheet: { assets: 'nope', liabilities: [], netWorth: 0 },
      }),
      null,
    );
  });

  test('accepts a patch with no balanceSheet populated yet (valid per the Go comment: additive fast-follow fields)', () => {
    const decoded = decodeFinanceBalanceSheetPatch({ schemaVersion: 1 });
    assert.ok(decoded);
    assert.equal(decoded.balanceSheet, undefined);
  });

  test('RED-proof: a patch from a foreign view (wrong shape entirely) is rejected, not coerced', () => {
    // e.g. what "f4.services" or "engine.status" would actually send —
    // structurally different, must not be silently treated as finance data.
    assert.equal(isFinanceBalanceSheetPatch({ tick: 5, month: 1, speed: 1, paused: false, modules: [] }), false);
  });

  // AC-7 / BAR-1 (round-r1 REJECT): a structurally well-formed patch
  // whose schemaVersion does not equal FINANCE_SCHEMA_VERSION must be
  // rejected here, not merely typeof-checked. MUTATION-PROVE: remove the
  // `p.schemaVersion !== FINANCE_SCHEMA_VERSION` compare in wire.ts and
  // this test goes RED (the old typeof-only check would accept it).
  test('AC-7: rejects a structurally valid patch whose schemaVersion does not match FINANCE_SCHEMA_VERSION', () => {
    const mismatched = {
      schemaVersion: FINANCE_SCHEMA_VERSION + 1,
      balanceSheet: { assets: [], liabilities: [], netWorth: 0 },
    };
    assert.equal(isFinanceBalanceSheetPatch(mismatched), false);
    assert.equal(decodeFinanceBalanceSheetPatch(mismatched), null);
  });

  test('AC-7: accepts a patch whose schemaVersion equals FINANCE_SCHEMA_VERSION exactly', () => {
    const matched = {
      schemaVersion: FINANCE_SCHEMA_VERSION,
      balanceSheet: { assets: [], liabilities: [], netWorth: 0 },
    };
    assert.ok(decodeFinanceBalanceSheetPatch(matched));
  });
});

// BUG-470 (FEAT-1972079936 Phase 0 inc2): parseWireVersion must reject
// exactly the same set internal/protocol/wireversion_test.go's
// TestParseWireVersion_Malformed asserts against ParseWireVersion — this
// list is the Go test's malformed-cases slice, copied verbatim so a
// future edit to either side that drifts the two reject-sets apart shows
// up as a diff between these two files, not a silent divergence.
describe('parseWireVersion (BUG-470: strict mirror of Go ParseWireVersion)', () => {
  const goMalformedCases = [
    '', '1', '1.', '.1', '1.0.0', 'a.b', '1.a', 'a.1', '-1.0', '1.-1',
    'v1.0', '1.0-dirty', '1.0-153-gABCD',
  ];
  for (const c of goMalformedCases) {
    test(`rejects ${JSON.stringify(c)} (mirrors Go's ParseWireVersion reject-set)`, () => {
      assert.equal(parseWireVersion(c), null);
    });
  }

  // MUTATION-PROVE (BUG-470): before the fix, Number('')===0 made ".1"
  // parse as {major:0,minor:1} and "1." parse as {major:1,minor:0} —
  // both silently ACCEPTED despite being in the Go reject-set above.
  // These two are called out explicitly so a regression back to
  // Number()-based coercion is caught even if the loop above were ever
  // trimmed.
  test('rejects ".1" specifically (empty major must not coerce to 0)', () => {
    assert.equal(parseWireVersion('.1'), null);
  });
  test('rejects "1." specifically (empty minor must not coerce to 0)', () => {
    assert.equal(parseWireVersion('1.'), null);
  });

  // Additional JS-specific coercion shapes Number() would otherwise let
  // through even though they were never in the original malformed list
  // (Go's strconv.Atoi already rejects all of these; this is purely
  // guarding the TS side's OWN coercion surface, BUG-470's stated scope).
  test('rejects whitespace-padded parts', () => {
    assert.equal(parseWireVersion(' 1.0'), null);
    assert.equal(parseWireVersion('1. 0'), null);
    assert.equal(parseWireVersion('1.0 '), null);
  });
  test('rejects exponent notation', () => {
    assert.equal(parseWireVersion('1e2.0'), null);
    assert.equal(parseWireVersion('1.0e2'), null);
  });
  test('rejects hexadecimal notation', () => {
    assert.equal(parseWireVersion('0x1.0'), null);
  });
  test('rejects Infinity/NaN-shaped strings', () => {
    assert.equal(parseWireVersion('Infinity.0'), null);
    assert.equal(parseWireVersion('NaN.0'), null);
  });

  test('still accepts well-formed versions (no over-tightening)', () => {
    assert.deepEqual(parseWireVersion('1.0'), { major: 1, minor: 0 });
    assert.deepEqual(parseWireVersion('2.3'), { major: 2, minor: 3 });
    assert.deepEqual(parseWireVersion('0.0'), { major: 0, minor: 0 });
    assert.deepEqual(parseWireVersion('10.42'), { major: 10, minor: 42 });
  });
});

describe('normalizeProtocolVersion (BAR-4, mirrors wsserver/server.go normalizeVersion)', () => {
  test('strips a trailing "-dirty" suffix', () => {
    assert.equal(normalizeProtocolVersion('v0.3.0-153-gABCD-dirty'), 'v0.3.0-153-gABCD');
  });
  test('a clean version is unchanged', () => {
    assert.equal(normalizeProtocolVersion('v0.3.0-153-gABCD'), 'v0.3.0-153-gABCD');
  });
  test('two genuinely different commits still normalize to different strings (no over-loosening)', () => {
    assert.notEqual(normalizeProtocolVersion('v0.3.0-153-gABCD-dirty'), normalizeProtocolVersion('v0.3.0-154-gEFGH'));
  });
});
