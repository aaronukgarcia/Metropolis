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
