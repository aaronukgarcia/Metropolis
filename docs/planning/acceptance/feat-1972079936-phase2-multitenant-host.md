---
mkey: FEAT-1972079936
phase: 2 of 5 (Compute Offload to Azure, Path A)
title: Multi-tenant engine host + session routing + failover-by-replay
status: DRAFT for Aaron review
author: BA analyst pass, 2026-08-31
depends_on: docs/planning/compute-offload-architecture.md (§5 Phase 2); docs/planning/acceptance/feat-1972079936-phase1-durable-persistence.md (Store, CityKey, AC-6 rehydrate primitive)
spec_ref: docs/planning/compute-offload-architecture.md; feat-1972079936-phase1-durable-persistence.md AC-1/AC-6; FEAT-1972079852 (engine-owns-journal DD, adapter/subscribe path); FEAT-1972079938 (Queue Depth HUD, per-engine load signal)
---

# FEAT-1972079936 Phase 2 — Multi-Tenant Engine Host + Session Routing + Failover-by-Replay

## Overview

Today `cmd/metroserve` is **single-session, full stop**. `main.go:74-100`
constructs exactly one `core.Engine` (`e := core.NewEngine(...)`), wires it
once (`compose.Wire(e, nil)`), and mounts exactly one `wsserver.Server`
(`wsserver.New(transport, ...)` at `/ws`) over exactly one
`protocol.InProcTransport`. There is no loop, no registry, no per-connection
lookup. `internal/protocol/wsserver/server.go`'s own package doc
(lines 58-67) says this explicitly for v1: *"One WebSocket connection is
treated as one UI client of the wrapped Transport... a second concurrent
connection would race the first's read of Results/Events/Deltas... out of
scope for increment 1."* The `Server` struct (server.go:197) holds a single
`transport protocol.Transport` field — no session id, no city id, nothing to
key on. `ServeHTTP` → `handshake` → `pump` (lines 273-462, 558-646) only ever
negotiates build/wire version at connect; identity is not a concept that
exists yet.

Nothing in `engine.core` blocks running more than one `Engine` in a process.
`core.Engine` (`internal/engine/core/engine.go:186`) is an ordinary struct
(`mu sync.Mutex`, `sealed bool`, plus hooks/world/citizens state as instance
fields) — no package-level singleton was found. `compose.Wire(e *core.Engine,
deps *Deps) (*Composition, error)` (`compose.go:476`) guards against
double-wiring the **same** engine (`e.HookCount() > 0`, line 491) but nothing
stops calling `Wire` on two independent `*core.Engine` instances; its
`registrationOrder`/`viewRegistrationOrder` config (`compose.go:233,326`) are
read-only package-level slices, not shared mutable state — each `Wire` call
closes over a fresh `simState` per engine. **This is genuinely greenfield**:
a repo-wide search for session/tenant/multi-instance constructs returned
nothing but incidental word matches (test file names, unrelated prose) —
there is no partial implementation to reconcile with.

Phase 1 already shaped its `Store` for this moment: `CityKey{TenantID,
CityID}` (Phase 1 AC-1) is the namespace **every** operation below keys on,
and Phase 1's AC-6 ("rehydrate on any instance, fresh-process proof") is
explicitly flagged there as *"the structural basis Phase 2 cites for
failover-by-replay... Phase 1 does not implement failover itself (no
dead-instance detection, no session registry), only proves the primitive it
depends on"* (Phase 1 doc, AC-6 closing note). Phase 2's job is exactly that
remainder: the session registry, the routing, and the dead-instance
detection Phase 1 explicitly left undone.

**What Phase 2 does:** introduce a session-registry layer that lets one
`metroserve` process host M independent `core.Engine` instances (one per
active city), route a WS connection's commands/subscriptions to the right
one, expose a load signal for routing/scaling decisions, and prove that a
session killed on one process instance resumes byte-identical on another via
Phase 1's `Store`.

**Out of scope for Phase 2:** real auth/identity (placeholder token only —
Phase 4 owns the real model per the epic doc's open question 3); Azure
deploy/gateway/NLB wiring itself (Phase 4 — Phase 2 defines the abstract
affinity contract the real NLB will satisfy); engine convergence of any
gameplay domain (Phase 3); protocol version negotiation (Phase 0).

---

## AC-1 — session registry: creation, lookup, and isolated per-session engine

**Criterion:** a new package (proposed `internal/engine/session`, name TBD by
the Architect — see GR#25 section) defines a `Registry` that maps a
`persist.CityKey` (Phase 1 AC-1's type, reused verbatim — never a parallel
key type) to a live `*Session`, where each `Session` owns exactly one
`*core.Engine` + its `compose.Composition` (from a fresh `compose.Wire` call)
+ its own `Store`-backed `CommandJournaler` (Phase 1 AC-2). Creation is
on-demand: the first command/subscription referencing a `CityKey` with no
live `Session` triggers `Registry.GetOrCreate(ctx, key)`, which either
returns the existing in-memory `Session` or rehydrates one from `Store`
(Phase 1 AC-6's algorithm, reused verbatim) before returning it.

- **Check:** two different `CityKey`s (distinct `TenantID` or `CityID`)
  routed through the same `Registry` produce two distinct `*core.Engine`
  pointers, each independently tickable, each with its own `sealed`/`mu`
  state (`core.Engine`, engine.go:186); a third call with a `CityKey` already
  live returns the **same** `*Session` pointer (no duplicate engine spun up
  for one city).
- **Mutation:** make `GetOrCreate` always construct a new `Session` (skip the
  existing-session check) — the test must catch it: sending two commands to
  the same `CityKey` in sequence must now observe them landing on two
  different engines (e.g. the second command's effect is invisible to a
  read issued against the first engine), which the test asserts against and
  fails.
- **False-pass guard:** the test must issue commands with **observable
  side-effects on engine state** (not just check `Session` pointer equality
  in isolation) — a test that only compares pointers can be fooled by a
  registry that returns the same pointer while silently routing commands
  elsewhere.

## AC-2 — tenant isolation: no cross-session leakage (security + determinism boundary)

**Criterion:** a command or subscription addressed to `CityKey` A can
**never** read or mutate `CityKey` B's state, observe B's events/deltas, or
influence B's tick outcome — even under concurrent load, even if A and B
share a `TenantID` is irrelevant (isolation is per-`CityID`, not per-tenant
account). This is the load-bearing property that makes "M cities in one
process" safe rather than merely convenient.

- **Check:** run N sessions concurrently (N ≥ 4, mixed `TenantID`s), each
  driven by an independent command stream with distinguishable payloads
  (e.g. building placements tagged with the issuing `CityKey`); after the
  run, assert every session's world/citizen/finance state contains **only**
  effects from commands addressed to that session's own `CityKey` — zero
  cross-contamination. Also assert no session's `Store.AppendCommand` calls
  (Phase 1 AC-2) ever target another session's `CityKey` namespace.
- **Mutation:** share one `sync.Mutex` (or one command queue) across two
  `Session`s that should be independent, or route by a truncated/collision-
  prone key (e.g. hash `CityKey` to a shared bucket instead of comparing it
  exactly) — the test must now observe a command issued to A landing in B's
  state or B's journal, and fail.
- **False-pass guard:** do not test isolation with a single session running
  alone — the property only means something under **concurrent** multi-
  session load; a serial single-session test passes trivially regardless of
  whether isolation is actually enforced.
- This AC is also the -race gate: `go test -race` across the concurrent
  multi-session test must be clean — a shared-state leak is exactly the
  class of bug `-race` is best at catching (mirrors GR#21's existing
  determinism-gate posture).

## AC-3 — command/subscription routing to the addressed session

**Criterion:** every inbound WS message (command or subscribe/adapter
request, FEAT-1972079852's path) carries (or is resolved to, via AC-4's
connect-time binding) a `CityKey`, and the routing layer dispatches it to
that `CityKey`'s `Session` — never to "whichever session happens to be the
process's only one," which is the entire behaviour being replaced.
`wsserver.Server` (or a thin routing shim in front of it — Architect's call,
flagged in GR#25) no longer assumes a single fixed `transport
protocol.Transport` field; it resolves the transport/engine pair per
connection via the `Registry`.

- **Check:** a test harness opens two WS connections, each bound (per AC-4)
  to a different `CityKey`, sends a command down each, and asserts each
  command's `CommandResult`/subsequent `Event`/`Delta` stream is observed
  only on the connection that sent it — no cross-talk between the two WS
  pumps.
- **Mutation:** hardcode the routing to always resolve to the first-created
  `Session` (reintroducing today's single-session behaviour under the new
  API surface) — the two-connection test must now fail: connection 2's
  command effects appear on connection 1's stream or vice versa.
- **False-pass guard:** the two `CityKey`s in the test must produce
  **distinguishable** effects (e.g. different building types at different
  coordinates) — a test using identical commands on both connections cannot
  tell correct routing from accidental sharing.

## AC-4 — session identity at connect (placeholder token) and sticky affinity contract

**Criterion:** at WS connect, the client presents a `CityKey`-resolving
credential — **for Phase 2, a placeholder bearer token/city-id pair passed
in the connect handshake payload** (real auth is Phase 4's scope per the
epic doc's open question 3; this AC defines only the shape the placeholder
must have so Phase 4 can swap the verification step without touching the
routing contract above it). The server resolves the token to a `CityKey`
during `handshake` (extending, not replacing, the existing
build/wire-version negotiation at server.go:273-462) and binds the
connection to that `Session` for its lifetime — this binding IS the sticky
affinity: once bound, a connection's commands go to the same in-memory
`Session` for as long as both the connection and the session live.

The gateway/NLB-level affinity mechanism (source-IP vs app-token — epic doc
open question 4) is **out of scope here and deliberately abstracted**: this
AC only requires that whatever mechanism the NLB uses to pin a TCP/WS
connection to a process instance, that instance's own `Registry` must
independently be able to resolve the SAME `CityKey` from the connect
payload — i.e., affinity is a hint for routing to the right process, never
the sole source of truth for which city a connection means.

- **Check:** a connect handshake carrying a valid placeholder token resolves
  to the expected `CityKey` and binds the connection; a second connect using
  the same token resolves to the same `CityKey` (idempotent resolution, not
  a fresh identity each time).
- **Mutation:** derive the `CityKey` from connection-transient data instead
  of the token (e.g. from the WS connection's local port or a request
  counter) — a reconnect test (same token, new connection) must now resolve
  to a *different* `CityKey` than the first connection, and the test must
  catch that divergence.
- **False-pass guard:** the test must actually **reconnect** (close and
  re-open the WS) rather than only checking resolution within one live
  connection — resolution-token stability only matters across the
  disconnect/reconnect boundary that a real client hits on every network
  blip.

## AC-5 — failover-by-replay: a killed session resumes byte-identical elsewhere

**Criterion:** if the process instance holding a `Session` dies (crash, kill,
graceful drain), that city is NOT lost: a second process instance's
`Registry.GetOrCreate` for the same `CityKey`, given only Phase 1's `Store`
contents, rehydrates the session (Phase 1 AC-6's algorithm) to the exact
same tick/state the dead instance last held, and a reconnecting client
resumes play with no visible discontinuity beyond the reconnect gap itself.
Sticky affinity (AC-4) is a **performance** optimisation only — it keeps hot
state resident on one node so most requests never pay a rehydrate cost — and
must NOT be load-bearing for correctness. This AC is the testable statement
of that claim.

- **Check:** an integration test (two `metroserve` processes — or two
  independent `Registry` + `Store`-on-shared-root instances in one test
  binary, mirroring Phase 1 AC-6's harness shape) drives a city on instance
  A to a non-trivial tick, kills/discards instance A without a graceful
  session handoff message, then has instance B's `Registry` resolve the same
  `CityKey` and asserts B's rehydrated state is byte-identical to A's
  last-known state (same assertion style as Phase 1 AC-3/AC-6 — full state
  hash or `CompareResult`, zero diff).
- **Mutation:** have instance B's `Registry` construct a session from a
  **fresh in-memory `Store` handle** that never reads the shared root (the
  Phase 1 AC-6 mutation, repeated here at the registry layer instead of the
  raw `Store` layer) — the test must fail: B produces a blank city or errors,
  proving the test can detect a registry that isn't actually wired to shared
  durable storage.
- **False-pass guard:** instance A must NOT perform a graceful
  shutdown/flush call that a real crash would skip — the test must simulate
  an ungraceful kill (discard the process/struct without calling any
  `Close`/`Drain` method) so the proof covers the actual failure mode
  (instance dies unexpectedly), not merely "a cleanly stopped session
  reloads," which Phase 1 AC-6 already covers and would make this AC
  redundant.
- This AC directly operationalises the epic doc's Architect-input claim
  (§3.1): *"sticky is an optimisation... not a hard requirement... failover
  becomes 'rehydrate the city on another instance from the durable
  journal.'"*

## AC-6 — capacity signal and per-process session cap

**Criterion:** a `Registry` exposes a load signal — at minimum, live session
count and a per-session queue-depth/backlog figure — that a routing layer
(and, later, the FEAT-1972079938 Queue Depth HUD) can read to decide whether
a process instance has headroom for a new session. A named, non-magic
constant (`SessionsPerProcessCap`, a BALANCE PLACEHOLDER per the standing
balance-number regime, mirroring Phase 1's `SnapshotCadenceTicks` precedent)
bounds how many sessions one process will accept; `GetOrCreate` for a new
`CityKey` on a process already at cap returns a typed "at capacity" error
(GR#7 registry error, not an ad hoc string) rather than silently degrading
every existing session's performance.

- **Check:** drive a `Registry` to exactly `SessionsPerProcessCap` live
  sessions, then attempt to create one more — assert the typed capacity
  error is returned and the existing sessions are unaffected (no session is
  evicted to make room silently).
- **Mutation:** remove the cap check (always create) — the test must catch
  it by observing a session count exceeding the configured cap with no
  error returned.
- **False-pass guard:** the test must assert the **existing** sessions are
  untouched after the rejected attempt (not just that an error was
  returned) — a cap check that silently evicts an idle session to make room
  would pass an error-only assertion while violating the "sessions are not
  silently evicted for capacity" property this AC also requires.

## AC-7 — idle eviction persists before discarding in-memory state

**Criterion:** a `Session` with no activity for `IdleEvictionTicks` (another
named BALANCE PLACEHOLDER) is evicted from the in-memory `Registry` to free
capacity — but eviction MUST flush any unpersisted state through Phase 1's
`Store` (a final snapshot + journal flush) before the in-memory `*core.Engine`
is discarded, so an evicted-then-reconnected city rehydrates exactly as
AC-1/AC-5 describe rather than losing the tail between "last journaled
command" and "eviction."

- **Check:** drive a session to idle timeout, assert eviction occurs, then
  reconnect (triggering `GetOrCreate`) and assert the rehydrated state
  matches the state at the moment of eviction exactly (byte-identical, same
  style as AC-5).
- **Mutation:** evict without the pre-eviction flush (discard the in-memory
  engine directly) — if any commands were journaled only in the in-process
  `replay.Recorder` buffer and not yet durably appended (a race the eviction
  path must close), the reconnect test must now be able to observe a gap;
  the test must be constructed so such a gap is possible to create (i.e. not
  always naturally flushed by AC-2's fail-closed append), and must catch it.
- **False-pass guard:** the idle-timeout test must not coincide with a
  normal Phase 1 AC-2 append boundary (e.g. don't test eviction only
  immediately after a command completes, when everything is already
  flushed) — trigger eviction after a period of true inactivity to prove the
  flush-on-evict path itself, not Phase 1's per-command durability guarantee
  doing the work.

## AC-8 — concurrent multi-session determinism

**Criterion:** M sessions ticking concurrently in one process must each
remain independently deterministic — the same command sequence against the
same `CityKey` produces the same state whether that city runs alone in a
process or alongside 9 others, and whether the process is running under
`-race` or not. No session's tick may read wall-clock, goroutine-scheduling
order, or any other session's memory to decide its own outcome (extends
Phase 1's determinism section, GR#21, to the multi-session dimension).

- **Check:** run the same recorded command journal for one `CityKey` twice —
  once as the sole live session in a process, once concurrently alongside N-1
  other unrelated sessions under load — and assert the resulting state
  hashes/`CompareResult` are identical between the two runs. Run under
  `go test -race`; assert clean.
- **Mutation:** introduce a shared mutable counter/clock read across sessions
  (e.g. a package-level tick counter used by more than one `Session` instead
  of each `Session` owning its own) — the two-run comparison (alone vs.
  alongside N-1 others) must now diverge under load, and `-race` must flag
  the shared access; the test must catch at least one of the two.
- **False-pass guard:** the "alongside N-1 others" run must generate genuine
  concurrent load on those other sessions (real ticking/commands, not idle
  placeholders) — idle neighbour sessions never exercise scheduler
  interleaving, so a bug that only manifests under real concurrent load
  would pass unnoticed.

## Determinism section (GR#21)

- Extends Phase 1's determinism section to the multi-session dimension: a
  city's replay is byte-identical **regardless of how many other sessions
  share its process**, regardless of scheduling order between sessions, and
  regardless of which physical instance rehydrates it after failover (AC-5).
  Sim state must never branch on session count, other sessions' state, or
  wall-clock — only on that session's own journal/command sequence.
- The map-range-with-break class (GR#21, Vestige
  `metropolis-map-range-break-gotcha`) applies with extra force here: any new
  iteration over the `Registry`'s live-session map (for eviction sweeps,
  load-signal aggregation, or a broadcast/shutdown-drain path) must be
  order-independent — no "first session found" logic, no early break that
  depends on map iteration order for correctness.
- `Registry`/`Session` bookkeeping (creation, eviction, load-signal
  computation) is explicitly a **routing-and-lifecycle** concern, never a
  gameplay-computation one — no `Session`'s tick outcome may be conditioned
  on `Registry`-level facts (how many other sessions exist, whether the
  process is near its capacity cap). This mirrors Phase 1's "the Store never
  influences sim computation" rule one layer up: the registry never
  influences sim computation either.
- Failover (AC-5) must reproduce **byte-identical** state independent of
  which physical process performs the rehydrate — this is Phase 1 AC-6's
  guarantee, and Phase 2 must not introduce anything (e.g. a
  process-specific random seed, a hostname baked into any derived id) that
  would break that guarantee across instances.

## GR#25 edge-audit (for the Architect — NOT registered here)

New cross-module edges this phase's design implies, to be reviewed and (if
approved) registered in `code.json`/`master-plan-v2.1.json` by the Architect
**before** any acceptance-criteria prose or code depending on them lands
(GR#25 — this document itself must not smuggle unregistered edges into
implementation; the list below is the audit deliverable, not a
registration):

1. **`<new session-registry module>` → `engine.core`** — the registry
   constructs and holds `*core.Engine` instances (via `compose.Wire`, see
   edge 2); needs its own edge unless folded into an existing module.
2. **`<new session-registry module>` → `engine.compose`** — `Registry.
   GetOrCreate` calls `compose.Wire(e, deps)` per session; today no module
   other than `cmd/metroserve` (which code.json has no key for — see below)
   calls `Wire` at all, so this is a genuinely new edge regardless of where
   the registry module lands.
3. **`<new session-registry module>` → `<Phase 1's persist module>`** —
   rehydrate-on-create (AC-1) and rehydrate-on-failover (AC-5) both call
   `Store.LoadLatest`; this edge is **contingent on Phase 1's own open
   module-key decision** (Phase 1 doc's GR#25 section, item 4 — `Store` may
   land as its own module, or fold into `harness.replay`/`feat.checkpoint`).
   Phase 2's edge cites whichever key the Architect resolves that to; it
   cannot be finalised independently of Phase 1's decision.
4. **`int.protocol` (today's home for `cmd/metroserve` and
   `internal/protocol/wsserver` — main.go:30-32 states "Module key:
   int.protocol... not a new module in its own right") → `<new
   session-registry module>`** — `wsserver`'s routing layer (AC-3) resolves
   a connection's `CityKey` and calls into the registry per-message; today
   `int.protocol`'s edges are all internal to the protocol/transport layer,
   none reach an engine-lifecycle concern because none existed before this
   phase.
5. **Possible new module key** — no `wsserver` or `metroserve` module key
   exists in `code.json` today (confirmed: only `engine.core` and
   `int.protocol` are the relevant existing keys in this area, each already
   carrying ~60 edge rows). The session-registry layer is new enough
   (engine-lifecycle + routing, neither purely protocol nor purely engine)
   that the Architect should decide whether it merits its own key (e.g.
   `mod.session`/`engine.host`) rather than being folded into `int.protocol`
   or `engine.core` — this decision gates edges 1-4 above and should be made
   before, not during, inc1 (mirrors Phase 1's identical open item for
   `Store`).

**Count: 4 candidate new edges + 1 open module-key decision.**

## Increments

- **inc1 — session registry + isolation + per-session engine, single
  process instance.** AC-1, AC-2, AC-8. Ships: one `metroserve` process can
  host M independent, isolated, concurrently-ticking cities — the core
  multi-tenancy claim — with no cross-instance failover yet (that's inc3).
  This increment alone already replaces today's hardcoded single-`Engine`
  wiring in `main.go:74-100` and is independently valuable (it is the
  prerequisite for load-testing "10 players share one engine" at all).
- **inc2 — session routing/affinity + capacity + idle eviction.** AC-3,
  AC-4, AC-6, AC-7. Ships: WS connections route to the right session via the
  placeholder token, a process advertises load and enforces its session cap,
  and idle sessions evict without losing state — the full single-instance
  routing story, still no cross-instance failover.
- **inc3 — failover-by-replay across instances.** AC-5. Ships: the
  dead-instance-recovery guarantee that is Phase 2's headline deliverable
  per the epic doc (§5: *"a killed instance recovers its cities
  elsewhere"*) — requires inc1 (isolated sessions) and Phase 1's Store to
  already be wired; does not require inc2's routing/affinity refinements to
  be complete, only that `Registry.GetOrCreate` (inc1) exists on the second
  instance.

Each increment is independently shippable and GR#23-round-able on its own.
inc1 can land before Phase 1's own module-key/edge decisions are fully
resolved for the *persist* side, provided inc1's tests use Phase 1's
in-memory fake `Store` (Phase 1 AC-1) rather than blocking on the real
disk/Blob implementation.

## Open questions for Aaron

1. **Sessions-per-process cap (M) default value.** `SessionsPerProcessCap`
   (AC-6) is a placeholder per the standing balance-number regime (Vestige
   `metropolis-balance-number-regime`) — the epic doc's illustrative "10
   players share one engine" is a worked example, not a ruling; needs
   Aaron's row-by-row approval alongside real perf numbers once inc1 lands
   and can be load-tested.
2. **Idle-eviction policy.** `IdleEvictionTicks` (AC-7) needs the same
   placeholder-then-approval treatment; also needs a ruling on whether
   eviction should be time-based only, or also triggered proactively under
   capacity pressure (evict-oldest-idle to make room for a new session
   rather than only rejecting the new one per AC-6) — AC-6 as written treats
   these as separate policies (no silent eviction for capacity); confirm
   that's the intended posture rather than an LRU-style capacity policy.
3. **Placeholder session/auth token shape (AC-4).** This document assumes an
   opaque bearer-token-to-`CityKey` mapping is an acceptable Phase 2
   placeholder (mirrors Phase 1's single-fixed-`TenantID` placeholder,
   Phase 1 open question 3) with real auth deferred to Phase 4. Confirm
   Phase 2 can close without real identity verification — i.e., anyone
   holding a valid-shaped token for a `CityKey` can connect to it in this
   phase, no ownership check yet.
4. **inc1 (single-instance multi-session) before any cross-instance
   failover — confirm ordering.** This document sequences inc1 → inc2 →
   inc3 so the multi-tenancy claim is provable before the harder
   cross-instance recovery claim. Confirm Aaron wants that order (vs.,
   e.g., prioritising inc3's failover proof earlier because it's the
   headline "resilience" deliverable, accepting that it would then need to
   stand up two processes/registries before single-instance isolation is
   even proven).
5. **How does a session id tie to a saved/named city?** `namedsaves.ts`-style
   naming exists webconsole-side (per the current lane's uncommitted
   `webconsole/src/sim/namedsaves.ts`); Phase 1's `CityKey.CityID` is an
   opaque string. Does Phase 2 need a lookup/listing surface (extending
   Phase 1 AC-1's `Store.ListCities`) so a reconnecting client can present a
   human-readable city name and have it resolve to the right `CityKey`, or
   is that purely a Phase 3/webconsole-side concern layered on top of the
   opaque id this phase defines? If in scope, it needs its own AC (not
   written here, pending this ruling).
6. **Queue Depth HUD integration timing (AC-6).** FEAT-1972079938 is named
   as the consumer of the per-process load signal — confirm whether Phase 2
   needs to ship the actual HUD wiring (cross-feature dependency) or only
   the load-signal API surface, with the HUD's own consumption tracked and
   scheduled under FEAT-1972079938 itself.
