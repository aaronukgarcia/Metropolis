// protocolClient.ts — FEAT-1972079852 increment 1: the webconsole's
// connection seam to a running Go engine over internal/protocol/wsserver's
// WebSocket JSON-RPC transport.
//
// This module consumes protocol.Delta and protocol.SubscriptionID from
// int.protocol (wire.ts's mirrored types) via wsserver's JSON-RPC framing.
// It replaces the earlier mock-only pattern (engine.ts's pure `reducer`,
// which remains the offline fallback — see backend.ts's error-capture
// convention this module reuses for every refusal/failure) with a real,
// out-of-process feed. View subscriptions are independent (AC-8): this
// module tracks each by its own SubscriptionID and Seq stream, so one
// view's delta never blocks or depends on another's. Fallback to mock is
// automatic on transport failure — this module never touches the mock
// reducer itself, it only reports state via callbacks so a caller (the
// store) can decide to keep running mock ticks alongside it.
//
// # Version handshake (Aaron DD, 2026-08-31; Architect DD: WebSocket
// JSON-RPC per INT-005)
//
// The FIRST message sent after the socket opens is a "handshake" JSON-RPC
// request carrying this build's own version (versionRaw, from
// generated/version.ts — the same git-describe string the Go binary's
// buildinfo.Version carries when both are built from the same monorepo
// commit). A refusal (mismatch, malformed, or timeout) is a REFUSAL, never
// a degrade (DD2/DD1): this client closes the socket and reports the
// registry-coded error via backend.ts's recordError/codedError, exactly as
// every other trapped error in this codebase is reported (GR#1/GR#7). The
// mock sim is untouched either way — connecting or refusing never mutates
// SimState directly.
//
// Kept deliberately free of React: this is the seam, testable with a fake
// socket against plain Node (per the acceptance doc's Tester note), with
// React wiring (the LIVE ENGINE badge) living in its own small component.

import { codedError, recordError } from './backend.ts';
import {
  FINANCE_SCHEMA_VERSION,
  PROTOCOL_VERSION,
  type Command,
  type CorrelationID,
  type Delta,
  type HandshakeResult,
  type RpcMessage,
  type SubscriptionID,
} from './wire.ts';

/** Registry-sourced codes this client surfaces verbatim from the server's
 * own refusal (internal/protocol/codes.go) — duplicated here as string
 * literals per this codebase's existing convention (backend.ts/
 * simContext.ts/captureBeforeWipe.ts all reference MET-xxxx codes as
 * literals, never importing Go source). See wsserver/server.go's doc
 * comment for what each one means server-side. */
export const ERR_HANDSHAKE_VERSION_MISMATCH = 'MET-P010';
export const ERR_HANDSHAKE_INVALID = 'MET-P011';
export const ERR_HANDSHAKE_TIMEOUT = 'MET-P012';
/** This client's own code for "the socket closed/errored before or during
 * a live session" — distinct from the three server-refusal codes above,
 * which always carry a specific server-issued ErrorRef. Claimed alongside
 * them under the same FEAT-1972079852 P010-P019 reservation. */
export const ERR_ENGINE_UNREACHABLE = 'MET-P013';
/** AC-7/DD2 (BAR-1, round-r1 REJECT): a Delta whose patch carries a
 * schemaVersion this client doesn't recognise. Never applied to state —
 * see handleMessage's delta branch. Claimed alongside the other P01x
 * codes under the same FEAT-1972079852 reservation. */
export const ERR_SCHEMA_MISMATCH = 'MET-P017';

/** The minimal WebSocket surface this module needs — satisfied by the
 * real browser `WebSocket`, and by test/protocol-client fakes. Kept
 * narrow deliberately so a plain-Node fake socket (no real network) can
 * implement it without pulling in a WebSocket polyfill. */
export interface WireSocket {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: 'open' | 'message' | 'close' | 'error', listener: (ev: any) => void): void;
}

export type ProtocolClientState =
  | 'connecting'
  | 'handshaking'
  | 'live'
  | 'refused'
  | 'closed'
  | 'error';

export interface ProtocolClientOptions {
  url: string;
  /** This build's own version string (versionRaw). */
  clientVersion: string;
  /** Socket factory — defaults to the global WebSocket; tests inject a fake. */
  createSocket?: (url: string) => WireSocket;
  onStateChange?: (state: ProtocolClientState) => void;
  /** Called once per accepted handshake with the engine's own version. */
  onLive?: (serverVersion: string) => void;
  /** Called on every refusal/failure, AFTER recordError has already run —
   * purely a UI hook (e.g. show a banner), never the only place the
   * failure is trapped. */
  onRefused?: (code: string, message: string) => void;
  /** Called for each Delta whose Seq passed gap tracking (AC-3/AC-15). The
   * gap (0 if none) is passed alongside so a caller can decide whether to
   * surface a staleness note — a gap is logged, never treated as fatal
   * (GR#17/GR#21: gaps are a valid, documented outcome of the drop
   * policy, not a bug in themselves). */
  onDelta?: (delta: Delta, gap: number) => void;
  /** Called when a Delta's patch declares a schemaVersion that does not
   * match what this client understands for the only view increment 1
   * ships (AC-7/DD2, BAR-1). The delta is NOT forwarded to onDelta and
   * state is NOT mutated — the caller's job here is purely the UI
   * surface (a banner, "view frozen at last-known-good"), the refusal
   * itself has already happened by the time this fires. */
  onSchemaMismatch?: (subscriptionId: SubscriptionID, expected: number, got: number) => void;
  /** Correlation id generator override, for deterministic tests. */
  newCorrelationId?: () => string;
}

/** Per-subscription Seq gap tracker — mirrors internal/protocol/
 * subscription.go's SeqTracker.Observe contract exactly (AC-15): ok=true
 * with gap=N (N>=0) for an in-order arrival (possibly with N skipped in
 * between); ok=false, gap=0 for a duplicate or out-of-order Seq. */
export class SeqTracker {
  private last = new Map<SubscriptionID, number>();

  observe(sub: SubscriptionID, seq: number): { gap: number; ok: boolean } {
    const prev = this.last.get(sub);
    if (prev === undefined) {
      this.last.set(sub, seq);
      return { gap: 0, ok: true };
    }
    if (seq <= prev) return { gap: 0, ok: false };
    const gap = seq - prev - 1;
    this.last.set(sub, seq);
    return { gap, ok: true };
  }

  reset(sub: SubscriptionID): void {
    this.last.delete(sub);
  }
}

let correlationCounter = 0;
function defaultCorrelationId(): string {
  correlationCounter += 1;
  return `webconsole-${Date.now()}-${correlationCounter}`;
}

/**
 * ProtocolClient owns one WebSocket connection to a running metroserve
 * instance: performs the version handshake, then relays subscribe/
 * unsubscribe commands out and Delta/CommandResult traffic back, tracking
 * per-subscription Seq via SeqTracker. Never touches SimState itself —
 * callers own how a Delta is applied (AC-1's single-ingress-point
 * discipline lives in the store, not here).
 */
export class ProtocolClient {
  private readonly opts: ProtocolClientOptions;
  private socket: WireSocket | null = null;
  private state: ProtocolClientState = 'connecting';
  private handshakeDone = false;
  private readonly seqTracker = new SeqTracker();
  private readonly newCorrelationId: () => string;

  constructor(opts: ProtocolClientOptions) {
    this.opts = opts;
    this.newCorrelationId = opts.newCorrelationId ?? defaultCorrelationId;
  }

  getState(): ProtocolClientState {
    return this.state;
  }

  connect(): void {
    const factory = this.opts.createSocket ?? ((url: string) => new WebSocket(url) as unknown as WireSocket);
    const socket = factory(this.opts.url);
    this.socket = socket;
    this.setState('connecting');

    socket.addEventListener('open', () => {
      this.setState('handshaking');
      const req: RpcMessage = {
        jsonrpc: '2.0',
        id: 1,
        method: 'handshake',
        params: { clientVersion: this.opts.clientVersion },
      };
      socket.send(JSON.stringify(req));
    });

    socket.addEventListener('message', (ev: { data: string }) => {
      this.handleMessage(ev.data);
    });

    socket.addEventListener('close', () => {
      if (this.state !== 'refused') {
        this.reportUnreachable('connection closed');
      }
    });

    socket.addEventListener('error', () => {
      this.reportUnreachable('socket error');
    });
  }

  close(): void {
    this.socket?.close();
    this.setState('closed');
  }

  /** Sends a Subscribe command for viewName (AC-3 step 1). Returns the
   * minted CorrelationID so a caller can correlate the eventual delta's
   * echoed CorrelationID if it wants to (AC-11) — this client does not
   * itself block waiting for the SubscriptionID; that arrives later as an
   * ordinary Delta/CommandResult the caller observes via onDelta. */
  subscribe(viewName: string, params?: Record<string, string>): CorrelationID {
    const correlationId = this.newCorrelationId();
    this.sendCommand({
      protocolVersion: PROTOCOL_VERSION,
      correlationId,
      issuedAtTick: 0,
      kind: 'Subscribe',
      payload: { viewName, params: params ?? {} },
    });
    return correlationId;
  }

  unsubscribe(subscriptionId: SubscriptionID): CorrelationID {
    const correlationId = this.newCorrelationId();
    this.seqTracker.reset(subscriptionId);
    this.sendCommand({
      protocolVersion: PROTOCOL_VERSION,
      correlationId,
      issuedAtTick: 0,
      kind: 'Unsubscribe',
      payload: { subscriptionId },
    });
    return correlationId;
  }

  private sendCommand(cmd: Command): void {
    if (!this.handshakeDone || !this.socket) return; // AC-4 fallback: silently a no-op pre-handshake; caller stays on mock
    const req: RpcMessage = { jsonrpc: '2.0', id: this.nextId(), method: 'command', params: cmd };
    this.socket.send(JSON.stringify(req));
  }

  private idCounter = 1;
  private nextId(): number {
    this.idCounter += 1;
    return this.idCounter;
  }

  private handleMessage(raw: string): void {
    let msg: RpcMessage;
    try {
      msg = JSON.parse(raw);
    } catch (e) {
      recordError('protocol client: malformed frame from engine', {
        type: 'app',
        code: ERR_ENGINE_UNREACHABLE,
      });
      return;
    }

    if (!this.handshakeDone) {
      this.handleHandshakeResponse(msg);
      return;
    }

    if (msg.method === 'delta' && msg.params) {
      const delta = msg.params as Delta;
      const { gap, ok } = this.seqTracker.observe(delta.subscriptionId, delta.seq);
      if (!ok) {
        // AC-15: a duplicate/out-of-order Seq is logged, not fatal — the
        // caller still receives nothing for this one (state is unchanged,
        // matching Go SeqTracker.Observe's own "treat as a bug, don't
        // apply" contract at the consumer layer).
        recordError(`protocol client: out-of-order/duplicate Seq ${delta.seq} for subscription ${delta.subscriptionId}`, {
          type: 'app',
        });
        return;
      }
      // AC-7/DD2 (BAR-1, round-r1 REJECT): a delta whose patch carries a
      // schemaVersion must be REFUSED — never applied — if that version
      // doesn't match what this client understands. Increment 1 ships
      // exactly one view (f2.finance, AC-8), so FINANCE_SCHEMA_VERSION is
      // the only schema this client currently knows; any patch shaped
      // like a versioned wire patch (a numeric schemaVersion field, per
      // every view publisher's documented convention — see wire.ts's
      // FinanceBalanceSheetPatch doc) but NOT matching it is a schema
      // mismatch, not silently forwarded to onDelta/state. This is
      // deliberately checked here (not only inside
      // decodeFinanceBalanceSheetPatch) because THIS is "the
      // protocolClient delta-apply path" — the seam a bad delta must
      // never get past, regardless of which view-specific decoder a
      // caller later applies.
      const rawPatch = delta.patch as Record<string, unknown> | null | undefined;
      if (rawPatch && typeof rawPatch === 'object' && typeof rawPatch.schemaVersion === 'number') {
        const gotVersion = rawPatch.schemaVersion;
        if (gotVersion !== FINANCE_SCHEMA_VERSION) {
          const err = codedError(
            ERR_SCHEMA_MISMATCH,
            `schema mismatch: subscription ${delta.subscriptionId} expected schemaVersion ${FINANCE_SCHEMA_VERSION}, got ${gotVersion} — patch not applied`,
          );
          recordError(err.message, { type: 'app', code: err.code });
          this.opts.onSchemaMismatch?.(delta.subscriptionId, FINANCE_SCHEMA_VERSION, gotVersion);
          return; // AC-7: never applied; view stays frozen at last-known-good
        }
      }
      this.opts.onDelta?.(delta, gap);
      return;
    }
    // 'result'/'event' notifications: no consumer in increment 1 beyond
    // the handshake and delta paths above — v1 scope per the acceptance
    // doc's AC-8 (finance-only) and Out of Scope section.
  }

  private handleHandshakeResponse(msg: RpcMessage): void {
    if (msg.error) {
      this.handshakeDone = false;
      const err = codedError(msg.error.code, msg.error.message);
      recordError(err.message, { type: 'app', code: err.code });
      this.opts.onRefused?.(msg.error.code, msg.error.message);
      this.setState('refused');
      this.socket?.close();
      return;
    }
    const result = msg.result as HandshakeResult | undefined;
    if (!result?.accepted) {
      const err = codedError(ERR_HANDSHAKE_INVALID, 'handshake response missing acceptance');
      recordError(err.message, { type: 'app', code: err.code });
      this.opts.onRefused?.(err.code, err.message);
      this.setState('refused');
      this.socket?.close();
      return;
    }
    this.handshakeDone = true;
    this.setState('live');
    this.opts.onLive?.(result.serverVersion);
  }

  private reportUnreachable(reason: string): void {
    if (this.state === 'closed' || this.state === 'refused') return;
    const err = codedError(ERR_ENGINE_UNREACHABLE, `engine connection lost: ${reason}`);
    recordError(err.message, { type: 'app', code: err.code });
    this.opts.onRefused?.(err.code, err.message);
    this.setState('error');
  }

  private setState(s: ProtocolClientState): void {
    this.state = s;
    this.opts.onStateChange?.(s);
  }
}
