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
import { PROTOCOL_ENGINE_KEY, queueDepthTracker, type QueueDepthTracker } from './queueDepth.ts';
import {
  FINANCE_SCHEMA_VERSION,
  PROTOCOL_VERSION,
  type Command,
  type CommandResult,
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
  /** Queue Depth HUD instrumentation (FEAT-1972079938): the tracker
   * sendCommand() reports in-flight asks to. Defaults to the module-level
   * singleton so production wiring needs no changes; tests inject an
   * isolated QueueDepthTracker to assert depth without touching global
   * state other suites might also be observing. */
  queueTracker?: QueueDepthTracker;
  /** Engine/target key to report under. Defaults to PROTOCOL_ENGINE_KEY —
   * override only for a test that wants a distinguishable key. */
  queueEngineKey?: string;
}

/** Rejection shape for a sendCommand() Promise: always a registry-coded
 * Error (GR#7), whether it came from an ack-level failure (decode/
 * validate/send, MET-P01x — see wsserver's replyErrorE) or from the
 * engine's own CommandResult.Error on a later reject. */
export type CommandRejection = Error & { code: string };

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
  /** inc2: JSON-RPC request id -> the pending sendCommand() Promise's
   * reject, so an ack-level error response (decode/validate/send
   * failure, MET-P01x) surfaces immediately rather than leaving the
   * caller waiting forever for a "result" notification that will never
   * arrive (the command never reached the engine at all). Entries are
   * removed once either the ack error fires, or (on a successful ack)
   * once the matching "result" notification resolves/rejects the same
   * Promise via pendingResults below — see sendCommand's doc comment. */
  private readonly pendingAcks = new Map<number, { correlationId: CorrelationID; reject: (e: CommandRejection) => void }>();
  /** inc2: correlationId -> the pending sendCommand() Promise's
   * resolve/reject, settled when the matching "result" notification
   * arrives (protocol.CommandResult.Accepted decides which). */
  private readonly pendingResults = new Map<
    CorrelationID,
    { resolve: (r: CommandResult) => void; reject: (e: CommandRejection) => void }
  >();

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
    this.rejectAllPending('connection closed');
  }

  /** Rejects every sendCommand() Promise still awaiting an ack or a
   * result — called when the socket goes away for any reason (an
   * explicit close() or reportUnreachable's error/close paths) so a
   * caller's `.then/.catch` always settles instead of hanging forever on
   * a command whose fate the engine can now never report. */
  private rejectAllPending(reason: string): void {
    if (this.pendingAcks.size === 0 && this.pendingResults.size === 0) return;
    const err = codedError(ERR_ENGINE_UNREACHABLE, `sendCommand: ${reason}`) as CommandRejection;
    for (const { reject } of this.pendingResults.values()) reject(err);
    this.pendingResults.clear();
    this.pendingAcks.clear();
  }

  /** Sends a Subscribe command for viewName (AC-3 step 1). Returns the
   * minted CorrelationID so a caller can correlate the eventual delta's
   * echoed CorrelationID if it wants to (AC-11) — this client does not
   * itself block waiting for the SubscriptionID; that arrives later as an
   * ordinary Delta/CommandResult the caller observes via onDelta. */
  subscribe(viewName: string, params?: Record<string, string>): CorrelationID {
    const correlationId = this.newCorrelationId();
    this.postCommand({
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
    this.postCommand({
      protocolVersion: PROTOCOL_VERSION,
      correlationId,
      issuedAtTick: 0,
      kind: 'Unsubscribe',
      payload: { subscriptionId },
    });
    return correlationId;
  }

  /**
   * inc2 (FEAT-1972079852): mints a Command envelope for `kind`/`payload`
   * with a fresh CorrelationID, sends it over the wire as a JSON-RPC
   * "command" request, and returns a Promise that settles from the
   * engine's own eventual CommandResult — NOT from the JSON-RPC ack (the
   * ack only means "queued," per wsserver's handleCommand doc comment).
   *
   * The Promise rejects in exactly two situations, both carrying a
   * registry-coded CommandRejection (GR#7):
   *   1. An ack-level JSON-RPC error (decode/validate/send failed
   *      server-side, MET-P01x) — the command never reached the engine
   *      at all, so there will never be a "result" notification to wait
   *      for; rejecting immediately is what stops the caller hanging
   *      forever (see pendingAcks' doc comment).
   *   2. A "result" notification arrives with Accepted:false — the
   *      engine itself refused the command; CommandResult.Error carries
   *      its registry code.
   *
   * It resolves with the full CommandResult when a "result" notification
   * arrives with Accepted:true.
   *
   * Never touches the TS journal (journal.ts) — per Aaron's
   * engine-owns-journal DD (2026-08-31), a command sent over this path is
   * journaled Go-side (when that seam lands — see the inc2 build report),
   * and the TS journal must apply to mock/offline commands only. This
   * method's only side effects are wire I/O and the pending-map
   * bookkeeping above; it deliberately does not call recordAction or any
   * other journal.ts function.
   *
   * # Queue Depth HUD instrumentation (FEAT-1972079938)
   *
   * queueDepthTracker.increment(engine) is reported for every ask that
   * actually reaches the wire, and decrement(engine) fires exactly once
   * per ask on whichever settle path gets there first: an ack-level
   * error, an engine "result" notification (accepted or rejected), or
   * rejectAllPending (socket drop mid-flight) — see resolve/reject being
   * wrapped below before either pending map is populated, so every path
   * that later calls one of those wrapped functions decrements exactly
   * once (both maps are always populated/cleared together, and each is
   * deleted before its resolve/reject fires — see handleMessage/
   * rejectAllPending — so a wrapped function can never run twice for the
   * same ask).
   *
   * THE LEAK FIX: increment() is called only AFTER `socket.send()`
   * returns successfully, never before. A real WebSocket's send() can
   * throw SYNCHRONOUSLY (InvalidStateError when the socket is CONNECTING/
   * CLOSING/CLOSED) — that throw means the ask never reached the wire and
   * this Promise settles (via the catch below) without ever having
   * incremented, so there is no increment left stranded and nothing to
   * decrement for it. Ordering increment after a successful send is what
   * guarantees "counted" and "will settle" can never diverge; the earlier
   * version incremented before send() with no try/catch, so a synchronous
   * throw settled the caller's Promise while leaving the tracker's count
   * elevated forever (the leak an independent round caught and REJECTed).
   */
  sendCommand(kind: string, payload: unknown): Promise<CommandResult> {
    const correlationId = this.newCorrelationId();
    const tracker = this.opts.queueTracker ?? queueDepthTracker;
    const engineKey = this.opts.queueEngineKey ?? PROTOCOL_ENGINE_KEY;
    return new Promise<CommandResult>((resolve, reject) => {
      if (!this.handshakeDone || !this.socket) {
        const err = codedError(ERR_ENGINE_UNREACHABLE, 'sendCommand: not connected to a live engine') as CommandRejection;
        recordError(err.message, { type: 'app', code: err.code });
        reject(err);
        return;
      }
      const id = this.nextId();
      // Wrapped so every settle path below (ack error, result
      // accept/reject, rejectAllPending) decrements exactly once — these
      // are the only functions ever stored in pendingAcks/pendingResults
      // for this ask, and both maps are always deleted before the stored
      // function is invoked (see handleMessage/rejectAllPending), so
      // double-decrement cannot happen.
      const wrappedResolve = (r: CommandResult) => {
        tracker.decrement(engineKey);
        resolve(r);
      };
      const wrappedReject = (e: CommandRejection) => {
        tracker.decrement(engineKey);
        reject(e);
      };
      this.pendingAcks.set(id, { correlationId, reject: wrappedReject });
      this.pendingResults.set(correlationId, { resolve: wrappedResolve, reject: wrappedReject });
      const cmd: Command = { protocolVersion: PROTOCOL_VERSION, correlationId, issuedAtTick: 0, kind, payload };
      const req: RpcMessage = { jsonrpc: '2.0', id, method: 'command', params: cmd };
      try {
        this.socket.send(JSON.stringify(req));
      } catch (sendErr) {
        // THE LEAK FIX: send() threw synchronously — this ask never
        // reached the wire, so there will be no ack and no "result"
        // notification for it. Clean up both pending maps directly (NOT
        // via the wrapped reject — no increment ever happened for this
        // ask, so there is nothing to decrement) and reject the caller's
        // Promise with the plain `reject`, so the settle count and the
        // increment count both stay at zero for this ask.
        this.pendingAcks.delete(id);
        this.pendingResults.delete(correlationId);
        const message = sendErr instanceof Error ? sendErr.message : String(sendErr);
        const err = codedError(ERR_ENGINE_UNREACHABLE, `sendCommand: socket.send failed: ${message}`) as CommandRejection;
        recordError(err.message, { type: 'app', code: err.code });
        reject(err);
        return;
      }
      tracker.increment(engineKey);
    });
  }

  private postCommand(cmd: Command): void {
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

    // inc2: a response keyed by `id` with no `method` is an ack (success
    // or error) for a request THIS client sent — today only sendCommand's
    // "command" requests are tracked this way (subscribe/unsubscribe's
    // postCommand is fire-and-forget, per increment 1's scope). An ack
    // ERROR means the command never reached the engine (decode/validate/
    // send failed server-side), so the pending Promise is rejected right
    // here — there will be no later "result" notification for it.  An ack
    // SUCCESS (`{queued: true}`) is not itself a resolution; the Promise
    // stays pending until the matching "result" notification below.
    if (msg.id !== undefined && !msg.method) {
      const pendingAck = this.pendingAcks.get(msg.id);
      if (pendingAck) {
        this.pendingAcks.delete(msg.id);
        if (msg.error) {
          this.pendingResults.delete(pendingAck.correlationId);
          const err = codedError(msg.error.code, msg.error.message) as CommandRejection;
          recordError(err.message, { type: 'app', code: err.code });
          pendingAck.reject(err);
        }
      }
      return;
    }

    if (msg.method === 'result' && msg.params) {
      const result = msg.params as CommandResult;
      const pending = this.pendingResults.get(result.correlationId);
      if (pending) {
        this.pendingResults.delete(result.correlationId);
        if (result.accepted) {
          pending.resolve(result);
        } else {
          const code = result.error?.code ?? ERR_ENGINE_UNREACHABLE;
          const message = result.error?.display ?? `command ${result.correlationId} rejected by engine`;
          const err = codedError(code, message) as CommandRejection;
          recordError(err.message, { type: 'app', code: err.code });
          pending.reject(err);
        }
      }
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
    this.rejectAllPending(reason);
  }

  private setState(s: ProtocolClientState): void {
    this.state = s;
    this.opts.onStateChange?.(s);
  }
}
