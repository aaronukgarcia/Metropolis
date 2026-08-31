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

/** Mirrors wsserver's handshakeParams. */
export interface HandshakeParams {
  clientVersion: string;
}

/** Mirrors wsserver's handshakeResult. */
export interface HandshakeResult {
  accepted: boolean;
  serverVersion: string;
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
