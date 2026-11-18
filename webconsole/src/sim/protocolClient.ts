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
  backoffDelayMs,
  defaultRandom,
  defaultTimers,
  pageClientSessionId,
  RECONNECT_DEFAULTS,
  type ReconnectConfig,
  type TimerApi,
} from './reconnect.ts';
import {
  CAP_COMMAND_DEDUP,
  CAP_LIFECYCLE_DRAINING,
  CAP_LIFECYCLE_HIBERNATING,
  CAP_LIFECYCLE_WAKING,
  CURRENT_WIRE_VERSION,
  FINANCE_SCHEMA_VERSION,
  PROTOCOL_VERSION,
  decodeDrainingParams,
  decodeHibernatingParams,
  decodeWakingParams,
  normalizeProtocolVersion,
  type WakePhase,
  type Command,
  type CommandResult,
  type CorrelationID,
  type Delta,
  type HandshakeResult,
  type RpcMessage,
  type WireVersion,
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
/** FEAT-1972079936 Phase 0 increment 3 (AC-4): the DEDICATED below-window-
 * floor handshake refusal — a client whose declared wire-version ceiling
 * is older than the server's supported window floor. Distinct from
 * ERR_HANDSHAKE_VERSION_MISMATCH (MET-P010, the separate build-string
 * equality check) — increment 2 temporarily reused that code for this
 * case; increment 3 retires the reuse server-side (wsserver/server.go)
 * and mirrors the new code here, same convention as every other MET-P0xx
 * literal in this block (never a Go import, per this file's own
 * doc-comment rule). */
export const ERR_HANDSHAKE_BELOW_WINDOW_FLOOR = 'MET-P020';
/** FEAT-1972079936 Phase 0 increment 3 (AC-5): a post-handshake command
 * was refused because it requires a fine-grained capability this
 * connection never negotiated (protocol/capability.go's
 * RequiredCapability). Never constructed client-side today (this client
 * has no gated command to send yet, per hasCapability's own doc comment)
 * — mirrored here purely so the registration is visible alongside every
 * other MET-P0xx code this file already tracks. */
export const ERR_CAPABILITY_REQUIRED = 'MET-P021';
/** BUG-1196: the city is already held by another connection (session gate).
 * Retryable on a bounded schedule (AC-R7), then terminal ERR_CITY_OPEN_ELSEWHERE. */
export const ERR_CITY_BUSY = 'MET-P043';
/** The city load failed after an accepted handshake (AC-W6). Terminal. */
export const ERR_CITY_LOAD_FAILED = 'MET-P030';
/** FEAT-9995385597 client codes (registry MET-V936..V939; the AC doc's
 * placeholder names E-WAKE-TIMEOUT, E-COMMAND-OUTCOME-UNKNOWN,
 * E-CITY-OPEN-ELSEWHERE, E-RECONNECT-GIVEUP respectively). */
export const ERR_WAKE_TIMEOUT = 'MET-V936';
export const ERR_COMMAND_OUTCOME_UNKNOWN = 'MET-V937';
export const ERR_CITY_OPEN_ELSEWHERE = 'MET-V938';
export const ERR_RECONNECT_GIVEUP = 'MET-V939';

/** AC-R6: a handshake refusal (or waking{failed}) with one of these codes can
 * never succeed on retry, so the client enters `refused` and stops. Any
 * other refusal is terminal too UNLESS it is ERR_CITY_BUSY (AC-R7) or the
 * server marks it retryable (error.data.retryable === true). MET-P021 is a
 * per-Command capability rejection, not a handshake refusal, so it is absent. */
export const TERMINAL_REFUSAL_CODES: readonly string[] = [
  ERR_HANDSHAKE_INVALID,
  ERR_HANDSHAKE_BELOW_WINDOW_FLOOR,
  ERR_CITY_LOAD_FAILED,
];

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
  | 'error'
  // FEAT-9995385597 AC-B1: only entered when the `lifecycle` option is set.
  | 'waking'
  | 'reconnecting'
  | 'asleep';

/** FEAT-9995385597 sections E/G: opting in turns on the lifecycle protocol
 * (declares lifecycle.* capabilities, sends clientSessionId, understands
 * waking/draining/hibernating) AND the reconnect state machine. Without it
 * the client behaves exactly as it did before this item (one connection, a
 * loss is a terminal `error`), which is what the pre-existing tests pin. */
export interface LifecycleOptions extends Partial<ReconnectConfig> {
  /** Uniform [0,1) source for backoff jitter. Injected in tests. */
  random?: () => number;
  /** Timer seam. Injected in tests; defaults to the global timers. */
  timers?: TimerApi;
  /** Page-lifetime session id (AC-P5). Defaults to the module's per-page-load
   * in-memory id; tests inject one. `null` means send none. */
  clientSessionId?: string | null;
}

/** Lifecycle snapshot of an in-progress wake (AC-W8). */
export interface WakeProgress {
  phase: WakePhase;
  pct: number;
}

/** What a terminal stop was about (refused / give-up / busy). */
export interface TerminalReason {
  code: string;
  message: string;
}

/** Extra info the client passes to onLive on a (re)connect. */
export interface LiveInfo {
  /** True when this `live` follows a lost connection (subscriptions have
   * already been re-issued by the client; the caller must NOT subscribe again). */
  reconnected: boolean;
}

export interface ProtocolClientOptions {
  /** Opt in to the lifecycle protocol + reconnect machine. */
  lifecycle?: LifecycleOptions;
  /** Called with each server.waking progress message (AC-W8). */
  onWakeProgress?: (progress: WakeProgress) => void;
  url: string;
  /** This build's own version string (versionRaw). */
  clientVersion: string;
  /** Socket factory — defaults to the global WebSocket; tests inject a fake. */
  createSocket?: (url: string) => WireSocket;
  onStateChange?: (state: ProtocolClientState) => void;
  /** Called once per accepted handshake with the engine's own build
   * string (unchanged meaning, Aaron ruling 5), the NEGOTIATED wire
   * version, and the negotiated (intersected, AC-5) capability set
   * (FEAT-1972079936 Phase 0 inc1, AC-2). Existing callers that only
   * read the first argument are unaffected — the two new ones are
   * additive. */
  onLive?: (serverVersion: string, negotiatedVersion?: WireVersion, capabilities?: string[], info?: LiveInfo) => void;
  /** Called on every refusal/failure, AFTER recordError has already run —
   * purely a UI hook (e.g. show a banner), never the only place the
   * failure is trapped. */
  onRefused?: (code: string, message: string) => void;
  /** Called for each Delta whose Seq passed gap tracking (AC-3/AC-15). The
   * gap (0 if none) is passed alongside so a caller can decide whether to
   * surface a staleness note — a gap is logged, never treated as fatal
   * (GR#17/GR#21: gaps are a valid, documented outcome of the drop
   * policy, not a bug in themselves). */
  onDelta?: (delta: Delta, gap: number, handle?: CorrelationID) => void;
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
export type CommandRejection = Error & { code: string; retryable?: boolean; state?: ProtocolClientState };

/** One caller-visible subscription, remembered so a reconnect can re-issue it
 * (AC-R5). `handle` is the CorrelationID the first subscribe() returned and
 * stays the caller's stable name for the view across reconnects;
 * `correlationId` is the CURRENT connection's Subscribe id (fresh on every
 * re-subscribe, never reused: under dedup a reused id would return the old
 * result and the old SubscriptionID). `subscriptionId` is unset until that
 * connection's first Delta (which echoes the correlationId) binds it. */
interface SubEntry {
  handle: CorrelationID;
  viewName: string;
  params: Record<string, string>;
  correlationId: CorrelationID;
  subscriptionId?: SubscriptionID;
}

/** FEAT-9995385597 r2 ruling 5: the command Kinds, classified EXPLICITLY and
 * mirrored from internal/protocol/commands.go's Kind constants (a parity test
 * parses that Go file and fails on any Kind named in neither list, so a new
 * server Kind forces a classification here instead of silently defaulting).
 * READ kinds are excluded from retry and dedup (AC-R9): they use a fresh
 * correlationId and reject retryably when the socket drops. */
export const READ_COMMAND_KINDS: readonly string[] = ['InspectEntity', 'QueryCitizen', 'QueryOccupants'];
/** WRITE (or control) kinds: held and re-sent once under dedup, otherwise
 * rejected outcome-unknown on a loss. Subscribe/Unsubscribe are controls the
 * client re-issues itself (never via sendCommand's held store). */
export const WRITE_COMMAND_KINDS: readonly string[] = [
  'AdvanceTicks', 'SetSpeed', 'Pause', 'Resume', 'Subscribe', 'Unsubscribe', 'Debug',
  'Buy', 'Zone', 'Build', 'Demolish', 'SetFunding', 'EnactPolicy', 'RepealPolicy',
  'BidForGrant', 'RespondToMandate',
];
function isReadKind(kind: string): boolean {
  return READ_COMMAND_KINDS.includes(kind); // an unknown kind is treated as a write (never blindly retried as a read)
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

/** Copies only the ReconnectConfig numbers that were actually given, so an
 * explicit `undefined` never overrides a default. */
function stripUndefined(l: LifecycleOptions): Partial<ReconnectConfig> {
  const out: Partial<ReconnectConfig> = {};
  for (const k of Object.keys(RECONNECT_DEFAULTS) as (keyof ReconnectConfig)[]) {
    const v = l[k];
    if (typeof v === 'number') out[k] = v;
  }
  return out;
}

type TimerSlot = 'retryTimer' | 'handshakeTimer' | 'wakeTimer' | 'stableTimer' | 'connectTimer' | 'wakeDeadlineTimer';

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
  /** r5: live has been entered on the current socket (at most once per socket). */
  private liveEntered = false;
  /** r5: a server.waking(ready) arrived before the handshake reply on this socket. */
  private readyBeforeReply = false;
  /** test-only observers (r5). */
  get _testHandshakeDone(): boolean { return this.handshakeDone; }
  /** Set once an accepted handshake response arrives (FEAT-1972079936
   * Phase 0 inc1). null until then. */
  private negotiatedVersion: WireVersion | null = null;
  /** BUG-1097 / Q100145: the engine build string from the accepted handshake
   * response, or null before one has completed. Informational only. */
  private serverVersion: string | null = null;
  private buildMismatch = false;
  /** Negotiated (intersected) capability set from the accepted handshake
   * response — see getCapabilities/hasCapability below. Empty (never
   * null) so callers never need a null-check to iterate/query it. */
  private capabilities: string[] = [];
  private readonly seqTracker = new SeqTracker();
  private readonly newCorrelationId: () => string;
  /** inc2: JSON-RPC request id -> the correlationId of the command it carried,
   * so an ack-level error response (decode/validate/send failure, MET-P01x)
   * surfaces immediately rather than leaving the caller waiting forever for a
   * "result" notification that will never arrive (the command never reached the
   * engine at all). The ack handler settles THROUGH settleCommand; this map
   * owns no resolve/reject of its own (r4 ruling 1). */
  private readonly pendingAcks = new Map<number, CorrelationID>();
  /** correlationId -> the pending sendCommand() Promise. Entries are added in
   * sendCommand and removed ONLY by settleCommand (r4 ruling 1; a source-scan
   * test pins that `pendingResults.delete` and every `.resolve(`/`.reject(` on a
   * pending command appear nowhere else). */
  private readonly pendingResults = new Map<
    CorrelationID,
    {
      resolve: (r: CommandResult) => void;
      reject: (e: CommandRejection) => void;
      /** Lifecycle mode: the sent envelope + whether it was already re-sent
       * once (AC-R9), kept so a dedup-capable reconnect can re-send it. */
      cmd?: Command;
      resent?: boolean;
      /** Lost with its socket and waiting for the NEXT handshake to decide. */
      held?: boolean;
      /** r3 ruling 2: the connection this write was SENT on had negotiated
       * command.dedup (snapshot at send time). Only such a write may be held. */
      dedupAtSend?: boolean;
      /** Queue-depth bookkeeping: true once increment() ran for this ask, so
       * settleCommand decrements exactly once and never for an unsent ask. */
      counted: boolean;
      /** r4 ruling 3: the one absolute deadline timer, armed at FIRST send. */
      deadline: unknown;
    }
  >();
  // SETTLEMENT INVARIANTS (r4: STRUCTURAL; r1-r3 each rejected "one more path
  // forgot to settle").
  //  1. ONE settle function. settleCommand is the only code that removes an
  //     entry from pendingResults, the only code that calls a command's
  //     resolve/reject, and it is idempotent (a second call is a no-op), clears
  //     the command's deadline timer and does the queue-depth bookkeeping once.
  //  2. ONE exit function. exitConnection is the only way out of a connection
  //     (socket loss, connect/handshake/wake timers, every refusal incl. the
  //     legacy not-accepted branch, asleep, give-up, retry(), close()).
  //     HOLD RULE: on an exit that will be retried, a write is held for re-send
  //     only if the connection it was SENT on had negotiated command.dedup and
  //     it was never re-sent; every other write, and every write on a terminal
  //     exit, settles MET-V937 (outcome unknown). A held write is re-sent once,
  //     with the same correlationId, only if the NEW handshake also advertises
  //     command.dedup, else it settles V937.
  //  3. BACKSTOP. Every command has one absolute deadline (commandDeadlineMs,
  //     armed at first send, not restarted by a hold, re-send or reconnect).
  //
  // WORST CASE (one number): no command promise stays pending more than
  // commandDeadlineMs after its first send, in ANY state -- live, half-open,
  // held, re-sent, waking, reconnecting -- because the deadline timer settles it
  // MET-V937 (reads: MET-P013 retryable) irrespective of what the connection
  // machine does. Default 120 s (an ASSUMPTION for Aaron). The reconnect machine
  // itself is still bounded (connect, handshake, wake silence, wake overall
  // deadline, retry timers; floor clamped to maxFloorMs), but nothing relies on
  // that for the promise guarantee. Tests: 'r4 backstop', 'r4 random walks'.
  /** Stale-subscription delta accounting (r2 ruling 3): counted in memory,
   * logged once per subscription id, never a storage write per delta. */
  private staleDeltaCount = 0;
  private readonly staleDeltaLogged = new Set<string>();
  getStaleDeltaCount(): number {
    return this.staleDeltaCount;
  }

  // ---- FEAT-9995385597 lifecycle / reconnect state (all inert unless
  // opts.lifecycle is set; `lc` null means the pre-existing behaviour). ----
  private readonly lc: {
    cfg: ReconnectConfig;
    random: () => number;
    timers: TimerApi;
    sessionId: string | undefined;
  } | null;
  /** Retries already scheduled in this failure run (AC-R1/R3); resets only
   * after the client has stayed live for cfg.stableLiveMs. */
  private attempt = 0;
  private busyRetries = 0;
  /** Last server.draining reconnectAfterMs: the floor for the next attempt. */
  private floorMs = 0;
  private retryTimer: unknown = null;
  private handshakeTimer: unknown = null;
  private wakeTimer: unknown = null;
  private stableTimer: unknown = null;
  private connectTimer: unknown = null;
  private wakeDeadlineTimer: unknown = null;
  private wake: WakeProgress | null = null;
  private terminal: TerminalReason | null = null;
  /** True from a lost connection until the next `live`. */
  private reconnectCycle = false;
  private subs: SubEntry[] = [];

  constructor(opts: ProtocolClientOptions) {
    this.opts = opts;
    this.newCorrelationId = opts.newCorrelationId ?? defaultCorrelationId;
    const l = opts.lifecycle;
    if (l) {
      const sid = l.clientSessionId === undefined ? pageClientSessionId() : (l.clientSessionId ?? undefined);
      this.lc = {
        cfg: { ...RECONNECT_DEFAULTS, ...stripUndefined(l) },
        random: l.random ?? defaultRandom,
        timers: l.timers ?? defaultTimers(),
        sessionId: sid,
      };
    } else {
      this.lc = null;
    }
  }

  /** In-progress wake {phase, pct}, or null when not waking (AC-W8). */
  getWakeProgress(): WakeProgress | null {
    return this.wake;
  }

  /** 1-based number of the reconnect attempt now pending or in flight (0 when
   * none) -- the badge title's "attempt N" (AC-B2). */
  getReconnectAttempt(): number {
    return this.reconnectCycle ? this.attempt : 0;
  }

  /** True from a lost connection until the next `live`: the badge shows
   * "Reconnecting..." for connecting/handshaking in that window (AC-B3). */
  isReconnectCycle(): boolean {
    return this.reconnectCycle;
  }

  /** Why the client stopped for good (refused, give-up, open elsewhere), or
   * null. Cleared by connect()/retry(). */
  getTerminal(): TerminalReason | null {
    return this.terminal;
  }

  getState(): ProtocolClientState {
    return this.state;
  }

  /** The negotiated wire version from the last accepted handshake, or
   * null before a handshake has completed (FEAT-1972079936 Phase 0 inc1,
   * AC-1/AC-2). */
  getNegotiatedVersion(): WireVersion | null {
    return this.negotiatedVersion;
  }

  /** The engine build string the last accepted handshake reported, or null
   * before one has completed (BUG-1097). */
  getServerVersion(): string | null {
    return this.serverVersion;
  }

  /** True when the engine build differs from this client build (compared
   * after stripping the volatile -dirty suffix, mirroring the server). A
   * WARNING signal only: the handshake never refuses on it (Q100145). */
  isBuildMismatch(): boolean {
    return this.buildMismatch;
  }

  /** The negotiated (intersected, AC-5) capability set — empty before a
   * handshake completes or when neither side declared anything. */
  getCapabilities(): readonly string[] {
    return this.capabilities;
  }

  /** Whether `name` is in the negotiated capability set (AC-5's gate: a
   * caller checks this BEFORE using a feature gated behind a capability,
   * rather than sending the command and relying on the server to reject
   * it after the fact). Phase 0 declares no real capability tokens yet,
   * so this always returns false today — the mechanism is what this
   * increment introduces. */
  hasCapability(name: string): boolean {
    return this.capabilities.includes(name);
  }

  connect(): void {
    if (this.lc) {
      // AC-R11: one attempt in flight at a time. A connect() while a socket is
      // already connecting/handshaking/waking/live is a no-op; a pending
      // retry timer is superseded by this explicit attempt.
      if (this.socket) return;
      this.clearTimer('retryTimer');
      this.terminal = null;
    }
    const factory = this.opts.createSocket ?? ((url: string) => new WebSocket(url) as unknown as WireSocket);
    const socket = factory(this.opts.url);
    this.socket = socket;
    this.handshakeDone = false;
    this.liveEntered = false;
    this.readyBeforeReply = false;
    this.setState('connecting');
    if (this.lc) {
      // r3 ruling 1 (INVARIANT): every attempt, first or reconnect, arms a
      // connect timer, so a socket that never opens and never errors cannot
      // leave the client in `connecting` with no timer. Expiry is a counted
      // failed attempt through the same loss path as an error. Cleared on
      // open (the handshake timer takes over), on loss and on close().
      this.armTimer('connectTimer', this.lc.cfg.handshakeTimeoutMs, () => {
        if (socket !== this.socket) return;
        const err = codedError(ERR_ENGINE_UNREACHABLE, `socket did not open within ${this.lc?.cfg.handshakeTimeoutMs} ms`);
        recordError(err.message, { type: 'app', code: err.code });
        this.handleLoss(false, 'connect timeout');
      });
    }

    socket.addEventListener('open', () => {
      if (this.lc && socket !== this.socket) return; // a socket we no longer own
      this.clearTimer('connectTimer');
      this.setState('handshaking');
      if (this.lc) {
        this.armTimer('handshakeTimer', this.lc.cfg.handshakeTimeoutMs, () => {
          // AC-W9: socket open -> handshake reply timed out. MET-P012.
          if (socket !== this.socket) return;
          const err = codedError(ERR_HANDSHAKE_TIMEOUT, `handshake reply did not arrive within ${this.lc?.cfg.handshakeTimeoutMs} ms`);
          recordError(err.message, { type: 'app', code: err.code });
          this.handleLoss(false, 'handshake timeout');
        });
      }
      const req: RpcMessage = {
        jsonrpc: '2.0',
        id: 1,
        method: 'handshake',
        params: {
          clientVersion: this.opts.clientVersion,
          // FEAT-1972079936 Phase 0 inc1 (AC-1/AC-2): this client only
          // ever declares the single version it currently speaks — the
          // min/max SHAPE exists now so increment 2 can widen the range
          // without a message restructure, see wire.ts's HandshakeParams
          // doc comment.
          clientMinVersion: CURRENT_WIRE_VERSION,
          clientMaxVersion: CURRENT_WIRE_VERSION,
          // Legacy mode declares nothing (AC-2's empty-intersection case).
          // Lifecycle mode declares the FEAT-9995385597 flags (AC-P1);
          // the server only acts on the ones it also advertises.
          capabilities: this.lc
            ? [CAP_LIFECYCLE_WAKING, CAP_LIFECYCLE_DRAINING, CAP_LIFECYCLE_HIBERNATING, CAP_COMMAND_DEDUP]
            : [],
          // AC-P5: the page-lifetime id, reused by every reconnect.
          ...(this.lc?.sessionId ? { clientSessionId: this.lc.sessionId } : {}),
        },
      };
      socket.send(JSON.stringify(req));
    });

    socket.addEventListener('message', (ev: { data: string }) => {
      if (this.lc && socket !== this.socket) return; // stale socket
      this.handleMessage(ev.data);
    });

    socket.addEventListener('close', (ev: { code?: number; reason?: string } | undefined) => {
      if (this.lc) {
        this.onSocketGone(socket, ev?.code, ev?.reason);
        return;
      }
      if (this.state !== 'refused') {
        this.reportUnreachable('connection closed');
      }
    });

    socket.addEventListener('error', () => {
      if (this.lc) {
        this.onSocketGone(socket, undefined, undefined);
        return;
      }
      this.reportUnreachable('socket error');
    });
  }

  /** User-driven retry (badge click after give-up or a terminal stop, AC-R3):
   * resets the schedule and connects at once. Lifecycle mode only. */
  retry(): void {
    if (!this.lc) return;
    // r4 ruling 4: retry() never resets the attempt counter while a write is
    // held, so a caller's retry() loop cannot keep the schedule (and the held
    // write) alive for ever. It may still trigger an immediate attempt below.
    const keepSchedule = this.hasHeldWrite();
    if (!keepSchedule) {
      this.attempt = 0;
      this.busyRetries = 0;
      this.floorMs = 0;
    }
    this.clearTimer('retryTimer');
    if (this.socket) {
      // r3 A2: a socket in hand leaves through the one exit function so
      // in-flight writes settle (or are held for the new handshake) and
      // subscriptions are restored by the reconnect.
      this.handleLoss(false, 'retry');
      if (!keepSchedule) this.attempt = 0;
    }
    this.connect();
  }

  /** Click-to-wake (AC-H3): the only way out of `asleep`. Alias of connect()
   * kept as its own name so a stray visibility handler is easy to spot. */
  wakeFromAsleep(): void {
    if (this.state !== 'asleep') return;
    this.connect();
  }

  /** Client-initiated sleep (AC-H3: the Page Visibility timeout, owned by
   * FEAT-9995385595). Closes the socket with 1000, no server frame expected,
   * and enters `asleep`: no automatic reconnect. */
  enterAsleep(reason = 'hidden'): void {
    if (!this.lc) return;
    this.exitConnection('stop', reason);
    this.reconnectCycle = false;
    this.setState('asleep');
  }

  close(): void {
    if (this.lc) {
      // AC-R12: an explicit close cancels every timer; no further attempt.
      this.exitConnection('stop', 'client close');
      this.subs = [];
      this.setState('closed');
      return;
    }
    this.socket?.close();
    this.setState('closed');
    this.rejectAllPending('connection closed');
  }

  // ------------------------------------------------------------------
  // FEAT-9995385597 reconnect machinery (lifecycle mode only)
  // ------------------------------------------------------------------

  private armTimer(slot: TimerSlot, ms: number, fn: () => void): void {
    if (!this.lc) return;
    this.clearTimer(slot);
    this[slot] = this.lc.timers.setTimeout(() => {
      this[slot] = null;
      fn();
    }, ms);
  }

  private clearTimer(slot: TimerSlot): void {
    if (this[slot] !== null && this.lc) this.lc.timers.clearTimeout(this[slot]);
    this[slot] = null;
  }

  /** True while any write is held for the next handshake's decision. */
  private hasHeldWrite(): boolean {
    for (const p of this.pendingResults.values()) if (p.held) return true;
    return false;
  }

  /** THE ONE EXIT FUNCTION (r4 ruling 2). Every way out of a connection --
   * socket loss, connect/handshake/wake timer expiry, every refusal, asleep,
   * give-up, retry(), close() -- passes through here, and nowhere else clears the
   * connection's timers, detaches/closes its socket or settles its commands.
   *
   *  - clears every connection timer (and the retry timer too when stopping), so
   *    an exit leaves nothing armed that belongs to the connection it left;
   *  - detaches the socket BEFORE closing it, so the close/error events it fires
   *    are recognised as stale (AC-R11: no double report);
   *  - applies the HOLD RULE: next === 'retry' holds a write only if it was SENT
   *    with command.dedup negotiated and was never re-sent; everything else
   *    (reads, non-dedup writes, every write when next === 'stop') settles now
   *    through settleCommand. */
  private exitConnection(next: 'retry' | 'stop', closeReason: string): void {
    this.clearTimer('handshakeTimer');
    this.clearTimer('wakeTimer');
    this.clearTimer('stableTimer');
    this.clearTimer('connectTimer');
    this.clearTimer('wakeDeadlineTimer');
    if (next === 'stop') this.clearTimer('retryTimer');
    const sock = this.socket;
    this.socket = null;
    this.handshakeDone = false;
    this.liveEntered = false;
    this.readyBeforeReply = false;
    this.capabilities = []; // cleared on exit; the next handshake decides
    this.pendingAcks.clear();
    this.subAcks.clear();
    this.wake = null;
    for (const [cid, p] of [...this.pendingResults.entries()]) {
      if (next === 'retry' && p.cmd && !isReadKind(p.cmd.kind) && !p.resent && p.dedupAtSend === true) {
        p.held = true;
        continue;
      }
      this.settleLost(cid, 'connection lost before the engine answered');
    }
    if (sock) {
      try {
        sock.close(1000, closeReason);
      } catch {
        // closing an already-closed socket is not an error worth surfacing
      }
    }
  }

  /** The single funnel for 'close' and 'error' on the owned socket. */
  private onSocketGone(socket: WireSocket, code: number | undefined, reason: string | undefined): void {
    if (socket !== this.socket) return; // stale, or already handled (error then close)
    this.socket = null; // already gone: the exit function has nothing to close
    const st = this.state;
    if (st === 'closed' || st === 'refused' || st === 'asleep') {
      this.exitConnection('stop', 'gone');
      return;
    }
    if (code === 4000) {
      // AC-G1: superseded by another tab of this page's session. Terminal.
      this.stopTerminal(ERR_CITY_OPEN_ELSEWHERE, 'City is open in another tab', 'refused');
      return;
    }
    if (code === 1000 && reason === 'hibernate') {
      this.enterAsleepFromServer();
      return;
    }
    const drain = code === 1012;
    if (!drain) {
      const err = codedError(ERR_ENGINE_UNREACHABLE, `engine connection lost: close ${code ?? 'n/a'}`);
      recordError(err.message, { type: 'app', code: err.code });
    }
    this.handleLoss(drain, 'loss');
  }

  private enterAsleepFromServer(): void {
    this.exitConnection('stop', 'hibernate');
    this.reconnectCycle = false;
    this.setState('asleep');
  }

  /** A non-terminal loss: leave the connection (hold rule) and schedule the retry. */
  private handleLoss(_drain: boolean, reason: string): void {
    this.exitConnection('retry', reason);
    this.reconnectCycle = true;
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (!this.lc) return;
    if (this.retryTimer !== null) return; // AC-R11: exactly one retry timer
    if (this.attempt >= this.lc.cfg.maxAttempts) {
      this.stopTerminal(
        ERR_RECONNECT_GIVEUP,
        `Offline: gave up reconnecting after ${this.attempt} attempts`,
        'error',
      );
      return;
    }
    const delay = backoffDelayMs(this.attempt, this.lc.random, this.floorMs, this.lc.cfg);
    this.attempt += 1;
    this.setState('reconnecting');
    this.armTimer('retryTimer', delay, () => this.connect());
  }

  /** Stops for good: records, reports once, enters `state`. Never retries and
   * never falls back to the offline mock sim (AC-R8): that is a player action
   * outside this client. */
  private stopTerminal(code: string, message: string, state: 'refused' | 'error'): void {
    this.exitConnection('stop', 'terminal');
    this.terminal = { code, message };
    this.reconnectCycle = false;
    const err = codedError(code, message);
    recordError(err.message, { type: 'app', code: err.code });
    this.setState(state);
    this.opts.onRefused?.(code, message);
  }

  /** Lifecycle-mode handshake refusal / waking{failed} routing (AC-R6/R7). Every
   * branch leaves through the exit function. */
  private handleRefusalCode(code: string, message: string, retryable: boolean): void {
    if (!this.lc) return;
    if (code === ERR_CITY_BUSY) {
      this.busyRetries += 1;
      if (this.busyRetries > this.lc.cfg.busyMaxRetries) {
        this.stopTerminal(ERR_CITY_OPEN_ELSEWHERE, 'City is open in another tab', 'refused');
        return;
      }
      this.handleLoss(false, 'refused');
      return;
    }
    if (retryable && !TERMINAL_REFUSAL_CODES.includes(code)) {
      this.handleLoss(false, 'refused');
      return;
    }
    // A terminal refusal: the same stop as stopTerminal. The badge offers a way
    // out of every refused state (click to retry), so it is never a dead end.
    this.stopTerminal(code, message, 'refused');
  }

  /** Settles one command as lost: a read rejects retryable MET-P013, a write
   * rejects MET-V937 (outcome unknown). Always through settleCommand. */
  private settleLost(cid: CorrelationID, detail: string): void {
    const p = this.pendingResults.get(cid);
    if (!p) return;
    const kind = p.cmd?.kind ?? '';
    if (p.cmd && !isReadKind(kind)) {
      const e = codedError(ERR_COMMAND_OUTCOME_UNKNOWN, `command ${cid} (${kind}): ${detail}; outcome unknown`) as CommandRejection;
      recordError(e.message, { type: 'app', code: e.code });
      this.settleCommand(cid, { error: e });
    } else {
      const e = codedError(ERR_ENGINE_UNREACHABLE, `command ${cid} (${kind}): ${detail}`) as CommandRejection;
      e.retryable = true;
      this.settleCommand(cid, { error: e });
    }
  }

  /** THE ONE SETTLE FUNCTION (r4 ruling 1). The only code that removes an entry
   * from pendingResults and the only code that resolves or rejects a command's
   * promise. Idempotent: a command already settled is a no-op (returns false).
   * Clears the command's deadline timer, drops its ack bookkeeping and does the
   * queue-depth decrement exactly once (only if it was counted at send). */
  private settleCommand(cid: CorrelationID, outcome: { result: CommandResult } | { error: CommandRejection }): boolean {
    const p = this.pendingResults.get(cid);
    if (!p) return false;
    this.pendingResults.delete(cid);
    if (p.deadline !== null && this.lc) this.lc.timers.clearTimeout(p.deadline);
    p.deadline = null;
    for (const [rpcId, c] of this.pendingAcks) if (c === cid) this.pendingAcks.delete(rpcId);
    if (p.counted) {
      p.counted = false;
      (this.opts.queueTracker ?? queueDepthTracker).decrement(this.opts.queueEngineKey ?? PROTOCOL_ENGINE_KEY);
    }
    if ('result' in outcome) p.resolve(outcome.result);
    else p.reject(outcome.error);
    return true;
  }

  /** After `ready` on a RE-connection: re-issue subscriptions in order with
   * FRESH correlationIds (AC-R5), then re-send held commands once with the
   * same correlationId (AC-R9). */
  private restoreAfterReconnect(): void {
    // Capabilities here are the NEW handshake's (cleared on exit). The subs and
    // the held commands are snapshotted before anything is written, so a
    // command/subscription issued from the live announcement is not restored.
    const dedup = this.hasCapability(CAP_COMMAND_DEDUP);
    const subSnapshot = [...this.subs];
    const heldSnapshot = [...this.pendingResults.entries()].filter(([, p]) => p.held && p.cmd);
    for (const entry of subSnapshot) {
      if (entry.subscriptionId !== undefined) this.seqTracker.reset(entry.subscriptionId); // AC-P6
      entry.subscriptionId = undefined;
      entry.correlationId = this.newCorrelationId();
      try {
        this.postCommand({
          protocolVersion: PROTOCOL_VERSION,
          correlationId: entry.correlationId,
          issuedAtTick: 0,
          kind: 'Subscribe',
          payload: { viewName: entry.viewName, params: entry.params },
        });
      } catch (e) {
        // One view failing must not stop the others (AC-R5).
        const err = codedError(ERR_ENGINE_UNREACHABLE, `re-subscribe ${entry.viewName} failed: ${e instanceof Error ? e.message : String(e)}`);
        recordError(err.message, { type: 'app', code: err.code });
      }
    }
    for (const [cid, p] of heldSnapshot) {
      if (!p.cmd || this.pendingResults.get(cid) !== p) continue; // settled meanwhile (deadline, exit)
      if (!dedup) {
        this.settleLost(cid, 'server does not dedup');
        continue;
      }
      const sock = this.socket;
      if (!sock || !this.isLive()) {
        // r3 A1: the socket is gone (a Subscribe write above killed it): a write
        // that was NOT written is not marked re-sent; it settles outcome-unknown.
        this.settleLost(cid, 'connection lost before the re-send');
        continue;
      }
      const id = this.nextId();
      this.pendingAcks.set(id, cid);
      p.resent = true;
      p.held = false;
      try {
        sock.send(JSON.stringify({ jsonrpc: '2.0', id, method: 'command', params: p.cmd } satisfies RpcMessage));
      } catch {
        this.settleLost(cid, 're-send failed');
      }
    }
  }

  /** LEGACY (non-lifecycle) mode only: rejects every sendCommand() Promise still
   * pending, through settleCommand, when the socket goes away. */
  private rejectAllPending(reason: string): void {
    for (const cid of [...this.pendingResults.keys()]) {
      this.settleCommand(cid, { error: codedError(ERR_ENGINE_UNREACHABLE, `sendCommand: ${reason}`) as CommandRejection });
    }
    this.pendingAcks.clear();
  }

  /** Sends a Subscribe command for viewName (AC-3 step 1). Returns the
   * minted CorrelationID so a caller can correlate the eventual delta's
   * echoed CorrelationID if it wants to (AC-11) — this client does not
   * itself block waiting for the SubscriptionID; that arrives later as an
   * ordinary Delta/CommandResult the caller observes via onDelta. */
  subscribe(viewName: string, params?: Record<string, string>): CorrelationID {
    const correlationId = this.newCorrelationId();
    const sent = this.postCommand({
      protocolVersion: PROTOCOL_VERSION,
      correlationId,
      issuedAtTick: 0,
      kind: 'Subscribe',
      payload: { viewName, params: params ?? {} },
    });
    // Lifecycle mode remembers each subscription actually sent so a reconnect
    // can re-issue it (AC-R5). The first correlationId stays the handle.
    if (this.lc && sent) {
      this.subs.push({ handle: correlationId, viewName, params: { ...(params ?? {}) }, correlationId });
    }
    return correlationId;
  }

  unsubscribe(subscriptionId: SubscriptionID): CorrelationID {
    const correlationId = this.newCorrelationId();
    this.seqTracker.reset(subscriptionId);
    // An unsubscribed view must not be re-issued by a later reconnect.
    this.subs = this.subs.filter((e) => e.subscriptionId !== subscriptionId && e.handle !== subscriptionId);
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
      if (!this.isLive()) {
        // AC-R10: rejected immediately, never queued; same MET-P013 as today
        // with {retryable, state} added.
        const err = codedError(ERR_ENGINE_UNREACHABLE, 'sendCommand: not connected to a live engine') as CommandRejection;
        if (this.lc) {
          err.retryable = true;
          err.state = this.state;
        }
        recordError(err.message, { type: 'app', code: err.code });
        reject(err);
        return;
      }
      const id = this.nextId();
      const cmd: Command = { protocolVersion: PROTOCOL_VERSION, correlationId, issuedAtTick: 0, kind, payload };
      this.pendingAcks.set(id, correlationId);
      this.pendingResults.set(correlationId, {
        resolve,
        reject,
        counted: false,
        deadline: null,
        ...(this.lc ? { cmd, dedupAtSend: this.hasCapability(CAP_COMMAND_DEDUP) } : {}),
      });
      const req: RpcMessage = { jsonrpc: '2.0', id, method: 'command', params: cmd };
      try {
        (this.socket as WireSocket).send(JSON.stringify(req)); // non-null: isLive() above
      } catch (sendErr) {
        // THE LEAK FIX: send() threw synchronously -- this ask never reached the
        // wire, so there will be no ack and no "result" notification. It settles
        // through the one settle function with counted === false, so nothing is
        // decremented that was never incremented.
        const message = sendErr instanceof Error ? sendErr.message : String(sendErr);
        const err = codedError(ERR_ENGINE_UNREACHABLE, `sendCommand: socket.send failed: ${message}`) as CommandRejection;
        recordError(err.message, { type: 'app', code: err.code });
        this.settleCommand(correlationId, { error: err });
        return;
      }
      // send() itself may have re-entered the client and settled the ask; then
      // there is nothing to count or time.
      const entry = this.pendingResults.get(correlationId);
      if (!entry) return;
      tracker.increment(engineKey);
      entry.counted = true;
      this.armCommandDeadline(correlationId, entry);
    });
  }

  /** r4 ruling 3 (BACKSTOP): the one absolute deadline for a command, armed at its
   * FIRST send and never restarted by a hold, a re-send or a reconnect. Lifecycle
   * mode only (legacy mode has no timer seam and no reconnect to outlive). */
  private armCommandDeadline(cid: CorrelationID, entry: { deadline: unknown }): void {
    if (!this.lc) return;
    const ms = this.lc.cfg.commandDeadlineMs;
    entry.deadline = this.lc.timers.setTimeout(() => {
      entry.deadline = null;
      if (this.pendingResults.get(cid) !== entry) return;
      this.settleLost(cid, `no answer within ${ms} ms of the first send`);
    }, ms);
  }

  /** Live means: accepted handshake AND (lifecycle mode) `ready` reached.
   * While `waking` the connection is accepted but commands are not allowed
   * yet (AC-W5/W8). */
  private isLive(): boolean {
    if (!this.socket || !this.handshakeDone) return false;
    return this.lc ? this.state === 'live' : true;
  }

  /** Returns true when the frame was actually written. */
  private postCommand(cmd: Command): boolean {
    if (!this.isLive() || !this.socket) return false; // AC-4: silently a no-op pre-handshake; caller stays on mock
    const id = this.nextId();
    const req: RpcMessage = { jsonrpc: '2.0', id, method: 'command', params: cmd };
    if (this.lc && cmd.kind === 'Subscribe') this.subAcks.set(id, cmd.correlationId);
    this.socket.send(JSON.stringify(req));
    return true;
  }

  /** Lifecycle mode: rpc id of an in-flight Subscribe -> its correlationId,
   * so an ack ERROR is reported against its own view (AC-R5). */
  private readonly subAcks = new Map<number, CorrelationID>();

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

    if (typeof msg !== 'object' || msg === null || Array.isArray(msg)) {
      recordError('protocol client: non-object frame from engine ignored', { type: 'app', code: ERR_ENGINE_UNREACHABLE });
      return;
    }

    // r4 ruling 2: in lifecycle mode a server.* notification that arrives BEFORE
    // the handshake reply is handled as that notification (waking -> waking,
    // draining -> loss with the floor, hibernating -> asleep), never as a refusal.
    if (this.lc && !this.handshakeDone && typeof msg.method === 'string' && msg.method.startsWith('server.')) {
      this.handleLifecycleMessage(msg);
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
      const subCid = this.subAcks.get(msg.id);
      if (subCid !== undefined) {
        this.subAcks.delete(msg.id);
        if (msg.error) {
          // AC-R5: this view's Subscribe failed; report it on its own and
          // forget the entry. Other views are untouched.
          this.subs = this.subs.filter((e) => e.correlationId !== subCid);
          const err = codedError(msg.error.code, msg.error.message);
          recordError(err.message, { type: 'app', code: err.code });
        }
        return;
      }
      const ackCid = this.pendingAcks.get(msg.id);
      if (ackCid !== undefined) {
        this.pendingAcks.delete(msg.id);
        if (msg.error && this.pendingResults.has(ackCid)) {
          const err = codedError(msg.error.code, msg.error.message) as CommandRejection;
          recordError(err.message, { type: 'app', code: err.code });
          this.settleCommand(ackCid, { error: err });
        }
      }
      return;
    }

    if (msg.method === 'result' && msg.params) {
      const result = msg.params as CommandResult;
      if (this.pendingResults.has(result.correlationId)) {
        if (result.accepted) {
          this.settleCommand(result.correlationId, { result });
        } else {
          const code = result.error?.code ?? ERR_ENGINE_UNREACHABLE;
          const message = result.error?.display ?? `command ${result.correlationId} rejected by engine`;
          const err = codedError(code, message) as CommandRejection;
          recordError(err.message, { type: 'app', code: err.code });
          this.settleCommand(result.correlationId, { error: err });
        }
      }
      return;
    }

    if (this.lc && this.handleLifecycleMessage(msg)) return;

    if (msg.method === 'delta' && msg.params) {
      const delta = msg.params as Delta;
      let handle: CorrelationID | undefined;
      if (this.lc) {
        // AC-R5: map the delta to the caller's handle. A Delta for a
        // SubscriptionID this client does not hold is ignored and logged,
        // never fed to the SeqTracker.
        let entry = this.subs.find((e) => e.subscriptionId === delta.subscriptionId);
        if (!entry && delta.correlationId) {
          entry = this.subs.find((e) => e.subscriptionId === undefined && e.correlationId === delta.correlationId);
          if (entry) {
            entry.subscriptionId = delta.subscriptionId;
            this.seqTracker.reset(delta.subscriptionId); // AC-P6: Seq restarts per subscription
          }
        }
        if (!entry) {
          // Hot path: count in memory; log (once per id) only the first time.
          this.staleDeltaCount += 1;
          if (!this.staleDeltaLogged.has(delta.subscriptionId)) {
            if (this.staleDeltaLogged.size >= 256) this.staleDeltaLogged.clear();
            this.staleDeltaLogged.add(delta.subscriptionId);
            recordError(`protocol client: ignored delta for unknown subscription ${delta.subscriptionId} (counted, logged once)`, { type: 'app' });
          }
          return;
        }
        handle = entry.handle;
      }
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
      this.opts.onDelta?.(delta, gap, handle);
      return;
    }
    // 'result'/'event' notifications: no consumer in increment 1 beyond
    // the handshake and delta paths above — v1 scope per the acceptance
    // doc's AC-8 (finance-only) and Out of Scope section.
  }

  /** Lifecycle-mode notifications (AC-W8, AC-D8, AC-H3, AC-P4). Returns true
   * when the message was consumed. */
  private handleLifecycleMessage(msg: RpcMessage): boolean {
    const method = msg.method as string | undefined;
    if (method === undefined) return false;
    if (method === 'delta' || method === 'result' || method === 'event' || method === 'command' || method === 'handshake') {
      return false;
    }
    if (method === 'server.waking') {
      const p = decodeWakingParams(msg.params);
      if (!p) {
        recordError('protocol client: malformed server.waking ignored', { type: 'app' });
        return true;
      }
      // r5: 'before the reply' is decided on handshakeDone ALONE (the first
      // waking frame changes the state, so a state test is false for the next).
      const preHandshake = !this.handshakeDone;
      if (preHandshake) {
        // r4 ruling 2: waking before the handshake reply. `ready` is not
        // actionable until the reply has negotiated the connection; the reply
        // (or the handshake timer) decides.
        if (p.phase === 'ready') {
          // r5: remembered as 'the city is awake'; never enters live here.
          this.readyBeforeReply = true;
          return true;
        }
        if (p.phase !== 'failed') {
          this.wake = { phase: p.phase, pct: p.pct };
          this.setState('waking');
          if (this.wakeDeadlineTimer === null) this.armTimer('wakeDeadlineTimer', this.lc?.cfg.wakeDeadlineMs ?? 0, () => this.onWakeTimeout(true));
        }
      } else if (this.state !== 'waking') return true; // late/duplicate: nothing to do
      this.armTimer('wakeTimer', this.lc?.cfg.wakeTimeoutMs ?? 0, () => this.onWakeTimeout(false));
      if (p.phase === 'failed') {
        // AC-W6: like a handshake refusal with that code.
        const code = p.code ?? ERR_CITY_LOAD_FAILED;
        this.handleRefusalCode(code, `city load failed (${code})`, false);
        return true;
      }
      if (p.phase === 'ready') {
        this.wake = { phase: 'ready', pct: 100 };
        this.opts.onWakeProgress?.(this.wake);
        this.clearTimer('wakeTimer');
        this.clearTimer('wakeDeadlineTimer');
        this.wake = null;
        this.enterLive();
        return true;
      }
      this.wake = { phase: p.phase, pct: p.pct };
      this.opts.onWakeProgress?.(this.wake);
      return true;
    }
    if (method === 'server.draining') {
      const p = decodeDrainingParams(msg.params);
      if (!p) {
        recordError('protocol client: malformed server.draining ignored', { type: 'app' });
        return true;
      }
      // AC-R2/D8: honour the floor, show Reconnecting (not an error), close our
      // end and reconnect quietly. Results the server wrote before this frame
      // were already delivered (AC-D3); nothing follows but the close.
      // r4 ruling 4: the floor is clamped (backoffDelayMs clamps to cfg.maxFloorMs).
      this.floorMs = p.reconnectAfterMs;
      this.handleLoss(true, 'server draining');
      return true;
    }
    if (method === 'server.hibernating') {
      const p = decodeHibernatingParams(msg.params);
      if (!p) {
        recordError('protocol client: malformed server.hibernating ignored', { type: 'app' });
        return true;
      }
      this.enterAsleepFromServer();
      return true;
    }
    // AC-P4: an unknown notification is ignored and logged, never a protocol error.
    recordError(`protocol client: ignored unknown notification method ${method}`, { type: 'app' });
    return true;
  }

  private onWakeTimeout(overall: boolean): void {
    if (this.state !== 'waking') return;
    const err = codedError(ERR_WAKE_TIMEOUT, overall ? `wake did not finish within ${this.lc?.cfg.wakeDeadlineMs} ms` : `no server.waking message for ${this.lc?.cfg.wakeTimeoutMs} ms`);
    recordError(err.message, { type: 'app', code: err.code });
    this.handleLoss(false, 'wake timeout');
  }

  /** Reached `ready` (or a legacy accepted handshake): live. */
  private enterLive(): void {
    // r5 INVARIANT (enforced): live only after the reply, at most once per
    // socket; onLive fires at most once per socket.
    if (!this.handshakeDone || this.liveEntered || !this.socket) {
      recordError('protocol client: enterLive refused (handshakeDone=' + String(this.handshakeDone) + ', alreadyLive=' + String(this.liveEntered) + ')', { type: 'app' });
      return;
    }
    this.liveEntered = true;
    const reconnected = this.reconnectCycle;
    this.reconnectCycle = false;
    if (!this.lc) {
      this.setState('live');
      this.opts.onLive?.(this.serverVersion ?? '', this.negotiatedVersion ?? CURRENT_WIRE_VERSION, this.capabilities, { reconnected });
      return;
    }
    this.clearTimer('wakeTimer');
    this.clearTimer('wakeDeadlineTimer');
    this.wake = null;
    // r2 ruling 2: the state flips to live (so writes are allowed) but is NOT
    // announced until restore has been computed and written from its snapshot.
    this.state = 'live';
    {
      this.armTimer('stableTimer', this.lc.cfg.stableLiveMs, () => {
        // AC-R1: only a live that LASTS resets the schedule.
        this.attempt = 0;
        this.busyRetries = 0;
        this.floorMs = 0;
      });
      if (reconnected) this.restoreAfterReconnect();
    }
    this.opts.onStateChange?.('live');
    this.opts.onLive?.(this.serverVersion ?? '', this.negotiatedVersion ?? CURRENT_WIRE_VERSION, this.capabilities, { reconnected });
  }

  private handleHandshakeResponse(msg: RpcMessage): void {
    if (this.lc && msg.error) {
      const retryable = (msg.error.data as { retryable?: unknown } | undefined)?.retryable === true;
      this.handleRefusalCode(msg.error.code, msg.error.message, retryable);
      return;
    }
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
      if (this.lc) {
        // r4 ruling 2: a not-accepted reply is a terminal refusal through the
        // one exit function (no raw close, nothing left pending).
        this.handleRefusalCode(ERR_HANDSHAKE_INVALID, 'handshake response missing acceptance', false);
        return;
      }
      const err = codedError(ERR_HANDSHAKE_INVALID, 'handshake response missing acceptance');
      recordError(err.message, { type: 'app', code: err.code });
      this.opts.onRefused?.(err.code, err.message);
      this.setState('refused');
      this.socket?.close();
      return;
    }
    this.handshakeDone = true;
    // FEAT-1972079936 Phase 0 inc1 (AC-1/AC-2): an OLD-shape server that
    // predates this increment simply won't send these two fields — they
    // are genuinely optional on the wire (wire.ts's HandshakeResult
    // doc), so falling back to this client's own current/empty values
    // here means a same-commit dev loopback with a not-yet-upgraded
    // server still behaves exactly as it did before this change.
    this.negotiatedVersion = result.negotiatedVersion ?? CURRENT_WIRE_VERSION;
    this.capabilities = result.capabilities ?? [];
    this.serverVersion = result.serverVersion;
    this.buildMismatch =
      normalizeProtocolVersion(result.serverVersion) !== normalizeProtocolVersion(this.opts.clientVersion);
    if (this.lc) {
      this.clearTimer('handshakeTimer');
      if (result.waking === true) {
        // AC-W1/W8: accepted but the city is still loading. Subscriptions and
        // commands wait for ready; the wake timer runs (AC-W9).
        if (this.state !== 'waking') {
          this.wake = { phase: 'queued', pct: 0 };
          this.setState('waking');
          this.opts.onWakeProgress?.(this.wake);
        }
        if (this.readyBeforeReply) {
          // r5: the ready already arrived; the reply lets us go live, once.
          this.readyBeforeReply = false;
          this.clearTimer('wakeTimer');
          this.clearTimer('wakeDeadlineTimer');
          this.wake = null;
          this.enterLive();
          return;
        }
        this.armTimer('wakeTimer', this.lc.cfg.wakeTimeoutMs, () => this.onWakeTimeout(false));
        // r3 ruling 3: the overall deadline is armed ONCE (here, or earlier by a
        // pre-handshake server.waking) and never re-armed.
        if (this.wakeDeadlineTimer === null) this.armTimer('wakeDeadlineTimer', this.lc.cfg.wakeDeadlineMs, () => this.onWakeTimeout(true));
        return;
      }
      this.enterLive();
      return;
    }
    this.setState('live');
    this.opts.onLive?.(result.serverVersion, this.negotiatedVersion, this.capabilities);
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
