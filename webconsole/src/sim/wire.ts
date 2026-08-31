// wire.ts — FEAT-1972079852 increment 1 (AC-2, AC-6, AC-18): the
// TypeScript-side mirror of int.protocol's wire schema. Every type here is
// a field-for-field, independently-maintained duplicate of a Go type —
// NEVER an import of Go source (there is none to import; this is a
// cross-language JSON boundary) — per GR#20's "engine never imports UI,
// and the reverse is equally true for the wire schema: each side keeps its
// own copy, so a mismatch is a version-handshake refusal (MET-P010), not a
// silent divergence.
//
// Sources this file mirrors:
//   - internal/protocol/envelope.go   (Command, CommandResult, ErrorRef,
//     CorrelationID, Tick, ProtocolVersion)
//   - internal/protocol/deltas.go     (Delta)
//   - internal/protocol/wsserver/server.go (the JSON-RPC 2.0 envelope this
//     package's WebSocket transport frames every message in)
//   - internal/engine/compose/finance_publish.go
//     (financeBalanceSheetWirePatch/financeBalanceSheetView/
//     financeBalanceItem — "f2.finance"'s balanceSheet sub-view, the first
//     view this adapter consumes per the acceptance doc's "start with
//     f2.finance" note)
//
// This module has NO runtime dependencies and performs no I/O — it is
// pure types plus small, pure decode/validate helpers, exactly like
// debugjson.ts's own "pure builder" discipline, so it is trivially
// unit-testable without a socket.

/** Mirrors envelope.go's ProtocolVersion constant. Bump only in lockstep
 * with the Go side — a mismatch here is exactly what the handshake exists
 * to catch, not something this file should silently paper over. */
export const PROTOCOL_VERSION = '1.0';

/** Mirrors wsserver/server.go's normalizeVersion (BAR-4, round-r1 REJECT
 * follow-up). `git describe` (GR#2: version = git describe via ldflags)
 * appends a volatile "-dirty" suffix whenever a build's working tree had
 * uncommitted changes at build time — two builds of the SAME commit can
 * legitimately differ only in that suffix (e.g. the webconsole and
 * metroserve built a few seconds apart, one lane's tree happening to
 * carry an untracked scratch file the other's didn't). Aaron's DD
 * (2026-08-31) is "refuse on mismatch," but a refusal should mean a REAL
 * commit difference — so both sides strip this one well-known suffix
 * before comparing. This does NOT over-loosen the check: the
 * `<tag>-<count>-g<sha>` core still differs for two genuinely different
 * commits and is still refused (see the server's own
 * TestHandshake_DifferentCommit_StillRefuses). Kept here purely for
 * symmetry/testing — the actual accept/refuse decision is made
 * server-side (server.go's handshake); this client never independently
 * overrides that decision. */
export function normalizeProtocolVersion(v: string): string {
  return v.endsWith('-dirty') ? v.slice(0, -'-dirty'.length) : v;
}

// ---------------------------------------------------------------------
// Semver'd wire version + negotiation shape (FEAT-1972079936 Phase 0
// increment 1) — mirrors internal/protocol/version.go's WireVersion and
// wsserver/server.go's extended handshakeParams/handshakeResult, per this
// file's own no-shared-import convention (each side keeps its own copy).
// See version.go's doc comment for the full major/minor rationale; the
// short version: MAJOR is a breaking change (a client whose major
// differs cannot be served at all without a shim, increments 2-3), MINOR
// is additive (an older-minor client keeps working unmodified).
// ---------------------------------------------------------------------

/** Mirrors version.go's WireVersion: a parsed major.minor pair, distinct
 * from the git-describe build string (PROTOCOL_VERSION/clientVersion
 * above stays a separate, purely diagnostic concept per Aaron ruling 5,
 * FEAT-1972079936). */
export interface WireVersion {
  major: number;
  minor: number;
}

/** Strict "is this a Go strconv.Atoi-shaped integer" matcher (BUG-470,
 * FEAT-1972079936 Phase 0 inc2): an optional leading sign followed by one
 * or more ASCII digits, nothing else. Used instead of bare `Number()` +
 * `Number.isInteger`, which silently COERCES several shapes Go's
 * ParseWireVersion (internal/protocol/wireversion.go) rejects outright —
 * `Number('')` is `0` (empty string, not "no digits"), `Number(' 1 ')`
 * trims whitespace, `Number('1e2')` accepts exponent notation, and
 * `Number('0x1')` accepts hex — every one of those would let a malformed
 * wire version slip through the pre-inc2 checker (isInteger only rejects
 * a fractional RESULT, not the coercions that produced it). This regex
 * runs FIRST so none of those coercions ever get a chance to fire. */
const STRICT_INTEGER_RE = /^[+-]?\d+$/;

/** Parses one "major"/"minor" part as a strict non-negative integer,
 * mirroring Go's `strconv.Atoi` + the `< 0` check in
 * wireversion.go's ParseWireVersion exactly (BUG-470). Returns null for
 * anything STRICT_INTEGER_RE doesn't match, for a value outside
 * Number.isSafeInteger (never silently truncated), or for a negative
 * result. */
function parseStrictNonNegativeInt(s: string): number | null {
  if (!STRICT_INTEGER_RE.test(s)) return null;
  const n = Number(s);
  if (!Number.isSafeInteger(n) || n < 0) return null;
  return n;
}

/** Mirrors version.go's ParseWireVersion: parses a "major.minor" string
 * into a WireVersion, or returns null for anything malformed (no
 * exception — callers decide how a malformed wire version is surfaced,
 * consistent with this file's other decode helpers).
 *
 * BUG-470 (FEAT-1972079936 Phase 0 inc2): tightened to strictly match
 * the Go side's reject-set — integer-only, exactly two dot-separated
 * parts, no whitespace/exponent/hex coercion via `Number()`. Before this
 * fix, ".1" parsed as {major:0,minor:1} (Number('')===0) and "1." parsed
 * as {major:1,minor:0} (same reason) — both are REJECTED by Go's
 * ParseWireVersion (strconv.Atoi("") errors), so this side silently
 * accepted two shapes the Go side refuses. See wire.test.mjs's
 * TestParseWireVersion_MalformedMirrorsGo for the shared reject-set this
 * now passes. */
export function parseWireVersion(v: string): WireVersion | null {
  const parts = v.split('.');
  // A wire version is exactly two dot-separated parts: major.minor.
  const WIRE_VERSION_PART_COUNT = 2;
  if (parts.length !== WIRE_VERSION_PART_COUNT) return null;
  const major = parseStrictNonNegativeInt(parts[0]);
  const minor = parseStrictNonNegativeInt(parts[1]);
  if (major === null || minor === null) return null;
  return { major, minor };
}

function parseWireVersionOrThrow(v: string): WireVersion {
  const parsed = parseWireVersion(v);
  if (!parsed) {
    // PROTOCOL_VERSION is this file's own literal constant, above — a
    // malformed value here is a bug in THIS file, not a runtime/network
    // condition, so throwing at module-load time (loudly, immediately)
    // is correct rather than silently falling back to some default.
    throw new Error(`wire.ts: PROTOCOL_VERSION ${JSON.stringify(v)} is not a valid "major.minor" wire version`);
  }
  return parsed;
}

/** The wire protocol version this build speaks, parsed from
 * PROTOCOL_VERSION. Mirrors version.go's CurrentWireVersion. Increment 1
 * only ever declares/expects this single value (window/shim serving is
 * increment 2) — this constant exists now so the handshake shape below
 * never has to change when that increment widens it. */
export const CURRENT_WIRE_VERSION: WireVersion = parseWireVersionOrThrow(PROTOCOL_VERSION);

/** Mirrors version.go's IntersectCapabilities: the set intersection of a
 * and b, de-duplicated, never their union and never either side's raw
 * set alone (AC-5). A nil/empty a or b correctly yields an empty array. */
export function intersectCapabilities(a: string[], b: string[]): string[] {
  const inA = new Set(a);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const c of b) {
    if (seen.has(c)) continue;
    seen.add(c);
    if (inA.has(c)) out.push(c);
  }
  return out;
}

/** Mirrors envelope.go's Tick (a plain integer wire type; the "int64" width
 * only matters Go-side — JS numbers are safe for tick counts well past any
 * realistic session length). */
export type Tick = number;

/** Mirrors envelope.go's CorrelationID (opaque, non-empty string). */
export type CorrelationID = string;

/** Mirrors subscription.go's SubscriptionID (opaque, engine-allocated). */
export type SubscriptionID = string;

/** Mirrors envelope.go's ErrorRef: a registry-sourced code plus a
 * pre-resolved display string — never a raw Go error, never an ad hoc
 * shape (GR#7). */
export interface ErrorRef {
  code: string;
  display: string;
}

/** Mirrors envelope.go's Command envelope. Payload is left as `unknown`
 * here (this increment sends commands opaquely, forwarded by the Go
 * wsserver without this side needing to decode every payload kind — see
 * protocolClient.ts's sendCommand). */
export interface Command {
  protocolVersion: string;
  correlationId: CorrelationID;
  issuedAtTick: Tick;
  kind: string;
  payload: unknown;
}

/** Mirrors envelope.go's CommandResult. */
export interface CommandResult {
  correlationId: CorrelationID;
  tick: Tick;
  accepted: boolean;
  error?: ErrorRef;
}

/** Mirrors deltas.go's Delta. `patch` is left as `unknown` at this layer —
 * callers narrow it per view name using the per-view Patch types below
 * (e.g. isFinanceBalanceSheetPatch). */
export interface Delta {
  subscriptionId: SubscriptionID;
  tick: Tick;
  seq: number;
  patch: unknown;
  correlationId?: CorrelationID;
}

/** Mirrors events.go's Event envelope shape closely enough for this
 * increment's needs (v1 does not decode Event payloads at all — no
 * consumer yet — so this stays a minimal opaque shape rather than
 * speculatively mirroring fields nothing here reads). */
export interface EngineEvent {
  correlationId?: CorrelationID;
  tick: Tick;
  kind: string;
  payload: unknown;
}

// ---------------------------------------------------------------------
// wsserver's JSON-RPC 2.0 framing (internal/protocol/wsserver/server.go)
// ---------------------------------------------------------------------

/** Method names wsserver's framing uses — mirrors server.go's constants
 * field-for-field (methodHandshake/methodCommand/methodResult/methodEvent/
 * methodDelta). */
export type RpcMethod = 'handshake' | 'command' | 'result' | 'event' | 'delta';

/** Mirrors wsserver's rpcError. */
export interface RpcError {
  code: string;
  message: string;
  data?: Record<string, unknown>;
}

/** Mirrors wsserver's rpcMessage envelope. */
export interface RpcMessage {
  jsonrpc: '2.0';
  id?: number;
  method?: RpcMethod;
  params?: unknown;
  result?: unknown;
  error?: RpcError;
}

/** Mirrors wsserver's handshakeParams. clientVersion remains the build
 * string (diagnostic-only, and still this increment's actual accept/
 * refuse gate per server.go's doc comment). clientMinVersion/
 * clientMaxVersion + capabilities are new (FEAT-1972079936 Phase 0 inc1,
 * AC-1/AC-2): the wire-version RANGE and capability set this client
 * declares. Increment 1 always sets min===max===CURRENT_WIRE_VERSION
 * (window/range serving is increment 2) and an empty capabilities array
 * (Phase 0 has no real capability tokens yet) — shaped this way now so a
 * later increment only has to widen what's sent, never restructure the
 * message. */
export interface HandshakeParams {
  clientVersion: string;
  clientMinVersion?: WireVersion;
  clientMaxVersion?: WireVersion;
  capabilities?: string[];
}

/** Mirrors wsserver's handshakeResult. serverVersion remains the build
 * string (Aaron ruling 5: stays client-visible for diagnostics even
 * though it no longer gates accept/refuse once increment 3 lands).
 * negotiatedVersion + capabilities are new: the wire version and
 * capability set this connection actually negotiated (AC-1/AC-2/AC-5) —
 * see protocolClient.ts's handleHandshakeResponse and getCapabilities/
 * hasCapability for how a caller consumes them. */
export interface HandshakeResult {
  accepted: boolean;
  serverVersion: string;
  negotiatedVersion?: WireVersion;
  capabilities?: string[];
}

// ---------------------------------------------------------------------
// "f2.finance" balanceSheet sub-view — the first view this adapter
// consumes (finance_publish.go).
// ---------------------------------------------------------------------

/** View name this adapter subscribes to first (AC-8: incremental
 * adoption, one view at a time). Mirrors compose.go's
 * financeViewSubscriptionName constant's VALUE (not the Go symbol
 * itself — see that constant's own doc comment for why this is a
 * deliberate, independently-maintained string duplication). */
export const FINANCE_VIEW_NAME = 'f2.finance';

/** The schemaVersion this client understands for "f2.finance". A delta
 * whose schemaVersion differs is a version/schema mismatch (AC-7, DD2)
 * and must NOT be applied. */
export const FINANCE_SCHEMA_VERSION = 1;

/** Mirrors finance_publish.go's financeBalanceItem field-for-field. */
export interface FinanceBalanceItem {
  label: string;
  valueMicropounds: number;
}

/** Mirrors finance_publish.go's financeBalanceSheetView field-for-field. */
export interface FinanceBalanceSheetView {
  assets: FinanceBalanceItem[];
  liabilities: FinanceBalanceItem[];
  netWorth: number;
}

/** Mirrors finance_publish.go's financeBalanceSheetWirePatch
 * field-for-field. Every field beyond balanceSheet is a documented
 * fast-follow on the Go side (PL/loans/creditRating/taxSliders/
 * publicPayroll/sankey) and is therefore optional here too — additive,
 * no schemaVersion bump needed when they land, mirroring the Go
 * comment's own claim exactly. */
export interface FinanceBalanceSheetPatch {
  schemaVersion: number;
  balanceSheet?: FinanceBalanceSheetView;
}

/** Runtime shape check for a decoded Delta.patch claiming to be
 * "f2.finance"'s balanceSheet patch (AC-2/AC-6/AC-7): every field this
 * code actually reads is present and the right JS typeof — NOT a full
 * JSON Schema validator, just enough to make a malformed/foreign patch
 * fail loudly (via the caller's schema-mismatch handling) instead of
 * NaN-ing or throwing deep inside a render.
 *
 * AC-7 / round-r1 REJECT (BAR-1): this used to accept ANY numeric
 * schemaVersion — a delta declaring schemaVersion:2 structurally passed
 * as long as the rest of the shape happened to still match, which is
 * exactly the "confident wrong data" failure DD2 exists to prevent (a
 * newer engine's v2 patch could silently be read as v1). A schemaVersion
 * that does not EQUAL FINANCE_SCHEMA_VERSION is now rejected here, at the
 * decode boundary, not left for a caller to notice later.
 */
export function isFinanceBalanceSheetPatch(v: unknown): v is FinanceBalanceSheetPatch {
  if (!v || typeof v !== 'object') return false;
  const p = v as Record<string, unknown>;
  if (typeof p.schemaVersion !== 'number') return false;
  if (p.schemaVersion !== FINANCE_SCHEMA_VERSION) return false;
  if (p.balanceSheet === undefined) return true; // valid: no sub-view populated this delta
  const bs = p.balanceSheet as Record<string, unknown>;
  if (!bs || typeof bs !== 'object') return false;
  if (typeof bs.netWorth !== 'number') return false;
  if (!Array.isArray(bs.assets) || !Array.isArray(bs.liabilities)) return false;
  return true;
}

/**
 * Decode a raw Delta.patch (already JSON.parse'd, per Go's json.RawMessage
 * convention — the wire carries it as a nested JSON value, not a
 * double-encoded string) as a FinanceBalanceSheetPatch, returning null if
 * it doesn't structurally match OR its schemaVersion doesn't equal
 * FINANCE_SCHEMA_VERSION (AC-2's "a passing test decodes a sample
 * protocol.Delta JSON... and asserts the decoded state matches a baseline
 * fixture"; AC-7's "a delta whose schemaVersion does not match... is NOT
 * applied"). Callers that need to distinguish "malformed" from
 * "well-formed but wrong version" (to surface AC-7's distinct
 * schema-mismatch banner/recordError) should read `patch.schemaVersion`
 * themselves before calling this — see protocolClient.ts's delta handler.
 */
export function decodeFinanceBalanceSheetPatch(patch: unknown): FinanceBalanceSheetPatch | null {
  return isFinanceBalanceSheetPatch(patch) ? patch : null;
}
