# FEAT-9995385597 acceptance criteria: server.waking / server.draining / server.hibernating and browser reconnect

**Spec refs:** a2.md s2.1 (wake, client behaviour), s2.3 (deploy drain), s3.2 seam 6; A2Bev001 v23 (Q100153 wake p95 <= 15 s behind a "Waking city" screen; Q100157 idle hibernation); BUG-1097 (build string never refuses); BUG-1196 (session gate, MET-P043); BUG-1197 (snapshot on SIGTERM/eviction); FEAT-9995385595 (idle timers, owns *when* to hibernate); FEAT-9995385594 inc2 (journal dedup, AC-J4/J5). Rules: GR#1, GR#20 (contract first), GR#21 (determinism: wall-clock never reaches the engine), GR#25.

**Placeholder codes** (real codes via `tools/plan/add-error.js` at build): `E-SERVER-DRAINING` (retryable command refusal), `E-SERVER-WAKE-FAILED` (city load failed after an accepted handshake), `E-WAKE-TIMEOUT` (client), `E-COMMAND-OUTCOME-UNKNOWN` (client, in-flight command not retryable), `E-CITY-OPEN-ELSEWHERE` (client, terminal after bounded busy retries), `E-RECONNECT-GIVEUP` (client). Existing: MET-P012, MET-P013, MET-P017, MET-P021, MET-P030, MET-P043 (session gate, owned by BUG-1196).

**Scope:** protocol additions, wsserver handshake reorder, metroserve drain, protocolClient reconnect, badge states. **Not in scope:** the idle timers and Page Visibility disconnect (FEAT-9995385595), the dedup implementation (FEAT-9995385594), host adapters, the offline mock sim.

## Corrections to the brief (stated plainly)

1. **The 5 s deadline does not bound the resolver today.** `handshake()` sets a 5 s *read* deadline for the client's first frame, and clears it on return. The resolver then runs with no deadline, and the session gate explicitly lets a resolver run "arbitrarily long". The real defect is that the handshake reply is withheld until the city is built and the client has no handshake or wake timeout at all (MET-P012 is declared and never used). The ACs therefore define a wake timeout on the client rather than "extend the 5 s".
2. **Moving the resolver after the reply breaks BUG-1196's refusal path.** The gate claim and its MET-P043 refusal live in the resolver, before the reply. If the whole resolver moves after the reply, a busy city could no longer be refused at the handshake. The ACs split the resolver: a synchronous claim before the reply (cheap, in memory, can refuse), an asynchronous city load after it (ASM-1600).
3. **Cloud Run is not a hosting tier in a2.md/a4.md.** The tiers are pix, then Spot VMs on AWS/GCP, then Azure Container Apps. The ACs treat the 60-minute WebSocket limit as one instance of "infrastructure closes the socket" and are host-agnostic (ASM-1607). If Cloud Run is genuinely planned, that is a new decision for Aaron.
4. **A retried command is only safe after inc2 dedup lands.** The brief says retried commands reuse their correlationId. Today the server has no dedup, so a retry of a command that already reached the engine applies it twice. This item can ship before inc2, but only with automatic retry switched off (AC-R9, ASM-1603).
5. **inc2's dedup key is `(session, correlationId)` but the wire has no session.** The handshake carries no session identity. This item adds a client-generated `clientSessionId` (AC-P5) so inc2 has something to key on (ASM-1602).

## A. Protocol additions (AC-P)

- **AC-P1 (capabilities, no version bump).** Given `wireversion.go` and Aaron's ruling 3 (fine-grained flags), when this item lands, then `CurrentWireVersion` stays 1.0 and three capability tokens are added to the capability registry: `lifecycle.waking`, `lifecycle.draining`, `lifecycle.hibernating`; a fourth, `command.dedup`, is declared by the server only once FEAT-9995385594 dedup is live. (ASM-1599.) Justification recorded in the code comment: the new handshake order (AC-W1) would break an older client, so the change cannot ride a plain additive minor bump.
- **AC-P2 (wire shape).** Given the JSON-RPC notification framing (`methodResult/Event/Delta`), when the server emits a lifecycle message, then it is a no-id notification with method `server.waking`, `server.draining` or `server.hibernating`, and params exactly: waking `{phase: string, pct: 0..100, code?, ctx?}`; draining `{reconnectAfterMs: int >= 0, reason: "deploy"|"shutdown"|"limit"}`; hibernating `{reason: "idle"|"hidden"|"operator"}`. The TS mirror and fixtures in `internal/protocol/testdata` carry all three (GR#20).
- **AC-P3 (capability gating).** Given a connection whose negotiated set lacks `lifecycle.X`, when that lifecycle event occurs, then the server sends no `server.X` frame on that connection and still performs the close (AC-D6, AC-H2), so an older client sees a plain close, never an unknown method.
- **AC-P4 (unknown method tolerance).** Given the TS client receives a notification method it does not know, when it is handled, then it is ignored and logged (not treated as a protocol error), proven with a frame `server.future`.
- **AC-P5 (clientSessionId).** Given a handshake, when the client sends `clientSessionId` (a random 128-bit token minted once per tab and kept in `sessionStorage`, with an in-memory fallback if storage throws), then the server records it for the connection, and an absent field is accepted (legacy client, today's behaviour). (ASM-1602.)
- **AC-P6 (Delta.Seq).** Given a re-subscribe on a new connection, when the first delta arrives, then `Seq` is 1 for that subscription and the client's SeqTracker is reset for the new SubscriptionID, so no false gap is reported. (ASM-1609.)

## B. Waking: the handshake must not wait for city load (AC-W)

- **AC-W1 (new order).** Given a client that negotiated `lifecycle.waking`, when it handshakes, then the server: validates version and capabilities, takes the BUG-1196 gate claim synchronously, writes the handshake reply `accepted:true` (with `waking:true` when the city is not yet loaded), then loads the city asynchronously and sends progress. The reply is written within 1 s of the handshake frame regardless of city size (tested with a resolver that blocks for 30 s). (ASM-1600.)
- **AC-W2 (legacy order retained).** Given a client without `lifecycle.waking`, when it handshakes, then today's order applies (resolve, then reply), byte for byte, so the existing handshake tests pass unchanged.
- **AC-W3 (busy refusal unchanged).** Given the city already holds a confirmed connection and the incoming client has a different `clientSessionId`, when it handshakes, then it is refused at the handshake with MET-P043 before any waking message, exactly as BUG-1196 specifies; the first connection is untouched.
- **AC-W4 (progress).** Given the load is running, when each phase starts, then `server.waking {phase, pct}` is sent. Phases are an ordered, closed set: `queued`, `fetching`, `decoding`, `indexing`, `replaying`, `ready`. `pct` is monotonic non-decreasing and a message is sent at least every 1 s while the load runs (heartbeat repeats the last value). `ready` carries pct 100 and is the single wake-complete signal. (ASM-1601.)
- **AC-W5 (no commands before ready).** Given the connection is accepted but not `ready`, when a Command frame arrives, then it is rejected with a retryable `E-SERVER-WAKING` ack error and not forwarded to any transport; no engine state changes.
- **AC-W6 (load failure).** Given the city load fails after the accepted handshake, when the resolver returns an error, then the server sends `server.waking {phase:"failed", code}` (code is the registry code, `MET-P030` or its pass-through via `resolverRefusal`), closes with 1011, releases the session gate claim, and fires no `onConnOpen`/`onConnClose` pair imbalance.
- **AC-W7 (lifecycle hooks and gate).** Given an accepted-but-waking connection, when it closes before `ready`, then the gate claim is released exactly once and `onConnOpen`/`onConnClose` are paired exactly once (BUG-1196 and FEAT-1972079942 AC-1 invariants hold; tested with a client that hangs up at each phase).
- **AC-W8 (client state).** Given `waking`, when the client receives `server.waking`, then its state becomes `waking` with `{phase, pct}` exposed, the badge shows "Waking city N%", and `ready` moves it to `live`. Subscriptions are issued only after `ready`.
- **AC-W9 (separate wake timeout).** Given the client is `waking`, when 60 s pass with no `server.waking` message (the timer resets on every message), then it reports `E-WAKE-TIMEOUT`, closes the socket and enters the reconnect schedule (AC-R1). The handshake timeout (new, default 10 s from socket open to reply) is a separate timer and reports MET-P012 on expiry. (ASM-1606.)
- **AC-W10 (wake budget evidence).** Given inc1 measurement is not yet available, when the item is verified, then it asserts only the protocol behaviour (reply within 1 s, heartbeat), not the 15 s city wake p95, which belongs to FEAT-9995385594/inc1; the 15 s figure is recorded as an UNVERIFIED target.

## C. Draining on SIGTERM (AC-D)

- **AC-D1 (sequence).** Given SIGTERM, when metroserve shuts down, then in order: (1) stop accepting new WebSocket upgrades and new handshakes (refused `E-SERVER-DRAINING`, retryable); (2) reject new Commands on live connections with the retryable `E-SERVER-DRAINING` ack error; (3) finish the current tick; (4) flush the journal batch; (5) write the snapshot (BUG-1197); (6) send `server.draining {reconnectAfterMs, reason}`; (7) close with code 1012; (8) `httpSrv.Shutdown`. Order is asserted by an in-process test with a recording fake.
- **AC-D2 (budget).** Given the host's SIGTERM grace period G (a flag, default 25 s, UNVERIFIED per host), when the drain runs, then steps 3-5 are bounded by G minus a 3 s margin; on overrun the server skips the remaining flush/snapshot, logs a warning registry code, still sends `server.draining` and closes 1012, and never exceeds G. (ASM-1607.) The existing hard-coded 5 s `Shutdown` timeout is replaced by this.
- **AC-D3 (in-flight commands).** Given a Command was accepted before step 2, when the drain runs, then its CommandResult is written before `server.draining`; a Command rejected in step 2 is never applied.
- **AC-D4 (reconnectAfterMs).** Given draining for a deploy, when the message is built, then `reconnectAfterMs` is a non-negative integer configurable by flag (default 1000); the client treats it as a floor for its next attempt (AC-R2).
- **AC-D5 (no engine impact).** Given a drain, when it completes, then the saved state equals an uninterrupted run saved at the same tick (determinism, GR#21): wall-clock grace values are never read by engine code.
- **AC-D6 (gate release).** Given the drain closed a connection, when the pump returns, then the gate claim is released exactly once, so an in-process successor (tests, hibernate then wake) is not refused.
- **AC-D7 (second signal).** Given SIGTERM arrives twice, when the second arrives mid-drain, then the drain continues (idempotent); only SIGKILL aborts it.
- **AC-D8 (client reaction).** Given close code 1012 or a `server.draining` frame, when the client sees either, then it shows "Reconnecting..." (not an error), reconnects quietly with the AC-R schedule, and records no `ERR_ENGINE_UNREACHABLE` for this close.

## D. Hibernating (AC-H)

- **AC-H1 (message).** Given a server-decided hibernate (idle timers belong to FEAT-9995385595), when it triggers, then the server snapshots (BUG-1197), sends `server.hibernating {reason}`, and closes with code 1000 and reason string `hibernate`.
- **AC-H2 (no capability).** Given a connection without `lifecycle.hibernating`, when hibernate triggers, then the close 1000 `hibernate` is still sent without the notification (AC-P3).
- **AC-H3 (client state).** Given `server.hibernating` or close 1000 with reason `hibernate`, when received, then the client enters state `asleep`, makes no automatic reconnect, and the badge shows "City asleep - click to wake". A click calls `connect()` which proceeds through the normal waking path (AC-W).
- **AC-H4 (distinguish from error).** Given a close 1000 with any other reason, or 1001/1006, when received, then the client treats it as a lost connection and reconnects (AC-R), not as asleep.
- **AC-H5 (no race on wake).** Given the user clicks wake while the server is still unloading the hibernated city, when the handshake arrives, then it is accepted, `waking` reports `queued` until unload finishes, then loads; it is never refused as busy (the old connection is already closed and the gate released).

## E. Client reconnect (AC-R)

- **AC-R1 (backoff).** Given a lost connection that is not terminal, when the client schedules attempts, then delay = random(0, min(30 s, 1 s * 2^attempt)) (full jitter; attempt starts at 0), never below 250 ms and never below the last `reconnectAfterMs`; the schedule resets to attempt 0 only after the client reaches `live` and stays live for 10 s. (ASM-1605.) The random source and clock are injected so tests are deterministic.
- **AC-R2 (reconnectAfterMs honoured).** Given `server.draining {reconnectAfterMs: 4000}`, when the client reconnects, then the first attempt is no earlier than 4000 ms after the message.
- **AC-R3 (give up).** Given 12 consecutive failed attempts (about 5 minutes at the cap), when attempt 12 fails, then the client stops, reports `E-RECONNECT-GIVEUP`, and the badge shows "Offline - click to retry"; a click resets the schedule. Terminal refusals (AC-R6) never retry.
- **AC-R4 (re-handshake).** Given a reconnect, when the socket opens, then the client sends a fresh handshake with the same `clientSessionId`, current versions and capabilities, and the state machine passes `connecting -> handshaking -> waking? -> live` again.
- **AC-R5 (re-subscribe).** Given N active subscriptions before the drop, when the client reaches `ready` on the new connection, then it re-issues each Subscribe (viewName, params) once, in the original order, maps the new SubscriptionIDs to the callers' handles, and resets the SeqTracker per new id (AC-P6). A view whose re-subscribe fails reports its own error and does not stop the others. (ASM-1609.)
- **AC-R6 (terminal refusals).** Given a handshake refusal MET-P011, MET-P020, MET-P021 or MET-P030, when received, then the client enters `refused` and does not retry (a retry cannot succeed); the badge shows the code.
- **AC-R7 (busy is bounded).** Given MET-P043, when received on a reconnect, then the client retries on the AC-R1 schedule up to 5 times (ASM-1608), then enters terminal `E-CITY-OPEN-ELSEWHERE` ("City is open in another tab").
- **AC-R8 (never offline mock).** Given any failure, wake timeout, give-up or refusal, when the client has no live connection, then it never swaps in the offline mock sim, never renders mock data as if live, and offers no automatic fallback; any switch to offline is an explicit player action outside this item. A test asserts the mock factory is not invoked on each failure path.
- **AC-R9 (in-flight commands).** Given commands sent but without a CommandResult when the socket drops: if the negotiated set contains `command.dedup`, then each is re-sent once after `ready` with the same correlationId (journal dedup returns the original result if it had applied); if it does not, then each rejects with `E-COMMAND-OUTCOME-UNKNOWN` (never retried, so no double-apply). (ASM-1603.)
- **AC-R10 (commands while down).** Given the client is not `live`, when `sendCommand` is called, then it rejects immediately with the retryable `E-ENGINE-NOT-LIVE`, not queued (ASM-1604); the existing `ERR_ENGINE_UNREACHABLE` behaviour for this case is preserved.
- **AC-R11 (single reconnect loop).** Given overlapping triggers (error event followed by close event, a timer firing during a manual retry), when they occur, then exactly one connection attempt is in flight at a time and exactly one retry timer is armed (the current `error` + `close` double-report is deduplicated).
- **AC-R12 (explicit close).** Given the caller invokes `close()`, when a reconnect timer is pending, then the timer is cancelled and no further attempt is made.
- **AC-R13 (infrastructure limit).** Given the host closes a long-lived connection (a 60-minute WebSocket limit, an idle proxy, a Spot reclaim), when the close is 1001/1006 or 1012 with no draining frame, then it follows the same path as AC-R1-R5 with no special casing; the first attempt is allowed at attempt-0 jitter, and a proxy-limit close that arrives while healthy is silent to the player (state shows "Reconnecting..." for under 3 s in the fake-socket test).

## F. Session gate interaction (AC-G)

- **AC-G1 (supersede).** Given an old connection that has not yet been noticed closed (half-open) and a new handshake carries the same `clientSessionId` and city, when it arrives, then the gate hands the claim to the new connection, the old socket is closed with code 4000 `superseded` (the client ignores 4000 on a socket it no longer owns), and the new connection is not refused. (ASM-1602.)
- **AC-G2 (different session still refused).** Given a different `clientSessionId` (or none), when it handshakes against a held city, then MET-P043 stands (AC-W3, AC-R7).
- **AC-G3 (supersede is atomic).** Given 16 concurrent reconnect attempts with the same `clientSessionId`, when they race, then exactly one is accepted and the rest are refused busy; the gate invariants (one confirmed connection, token-matched release) hold under `-race`.
- **AC-G4 (late release is harmless).** Given the superseded socket's pump returns after the takeover, when it calls the close hook, then it does not release the new connection's claim (token or generation match).
- **AC-G5 (drain frees the gate).** Given a draining close, when the old process or in-process server has finished its pump, then the claim is released (AC-D6); a successor in the same process connecting within `reconnectAfterMs` is accepted.

## G. Badge and states (AC-B)

- **AC-B1 (state set).** The client state union gains `waking`, `reconnecting`, `asleep`; the existing six are unchanged. `connState` data attribute on the badge carries the new value.
- **AC-B2 (labels).** `waking` shows "Waking city N%" with the phase in the title; `reconnecting` shows "Reconnecting..." with the attempt number in the title; `asleep` shows "City asleep - click to wake" (clickable); `error` after give-up shows "Offline - click to retry"; busy-terminal shows "City is open in another tab".
- **AC-B3 (no flicker).** Given a drain and quick reconnect under 1 s, when the state changes, then the label does not flash an error state at any point (`reconnecting` only).
- **AC-B4 (existing behaviour).** Given `live`, `refused` and build-mismatch cases, when rendered, then labels and `data-` attributes are byte-identical to today (regression).

## H. Tests (AC-T)

- **AC-T1.** Client tests use the existing injected `createSocket` fake-WebSocket seam in `protocolClient.ts`; the clock and RNG are injected; no real timers or sleeps (no wall-clock bounds in CI).
- **AC-T2.** Server tests are in-process `wsserver` tests (httptest, no laptop listener, GGR94) with a blocking resolver and a controllable clock; each AC-W, AC-D, AC-H, AC-G case is covered at least once.
- **AC-T3 (can fail).** Each test is shown to fail when its behaviour is removed (revert the reorder, the 1012 close, the jitter, the supersede) before it is accepted as evidence.
- **AC-T4.** Run under `-race`; gate worktree `--webconsole-ci` plus the tsx-all job (catalogue note: bounded set misses tsx-all); 30-minute watchdog applies.
- **AC-T5 (fixtures).** `internal/protocol/testdata` gains golden frames for the three messages, and a Go-to-TS parity test decodes each in both languages.

## Unstated assumptions (BOW)

ASM-1599 flags not a minor bump; ASM-1600 resolver split; ASM-1601 `ready` is a waking phase; ASM-1602 `clientSessionId` and supersede; ASM-1603 retry gated on `command.dedup`; ASM-1604 no queueing while down; ASM-1605 backoff numbers; ASM-1606 wake timeout 60 s resets on progress; ASM-1607 drain budget host-agnostic; ASM-1608 busy retries 5; ASM-1609 re-subscribe by replay; ASM-1610 idle timers and Page Visibility out of scope.

## Open questions for Aaron

- **Q-1.** Three lifecycle flags plus `command.dedup`, or one `server.lifecycle` flag? (ASM-1599; the three-flag form follows ruling 3.)
- **Q-2.** Is a distinct `server.ready` preferable to the terminal `waking{phase:"ready"}`? (ASM-1601)
- **Q-3.** Is a client-minted `clientSessionId` acceptable as the dedup session and takeover key, given that it is not authenticated (any holder of the token can supersede)? An authenticated session arrives with real tenants. (ASM-1602)
- **Q-4.** Should a second tab be refused forever (today), or may a newer tab take over from an older one? This item assumes refuse, with bounded retry. (ASM-1608)
- **Q-5.** Is Cloud Run actually a planned host? If not, the 60-minute limit is only a hypothetical. (ASM-1607)
- **Q-6.** Ship before inc2 dedup with retry off (this doc's recommendation), or hold the reconnect work until dedup lands? (ASM-1603)
