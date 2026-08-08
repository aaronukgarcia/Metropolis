# int.protocol — Engine<->UI protocol v1

Module key: `int.protocol` · GUID `19bed5ea-bcfd-4dd5-9be7-71def6c50fc4` ·
BoW code `INT-001` · path `internal/protocol/` · spec ref §15; UI-SPEC §1,
§6; M0-ENG §1.1; V.2.1.

Sprint-0 **freeze review page**. This is the seam every other M0 module
builds against — MOD-008 (H-STUB), MOD-009 (TUI renderer core), MOD-012
(engine orchestrator), and MOD-013 (H-REPLAY) all block on this contract
being frozen. Design quality matters more than feature count here; the v1
command vocabulary is deliberately the skeleton-era minimum (§4).

Status: **awaiting freeze review.**

(The body below also draws on UI-SPEC §1 and M0-ENG §1.1 for the input/render
loop split and the no-shared-memory rule respectively; the header spec ref
above is kept verbatim to the BOW item's `specRef` field — see Findings for
the lead on this discrepancy with `internal/protocol/doc.go`.)

## Why this module exists

Engine and UI are separate process-domains that share **no memory** —
"channels/protocol only, even in-process. This is what keeps the gRPC flip
a config change" (M0-ENG §1.1). Every value that crosses that seam is a
message defined in this package: a `Command` going in, and a `CommandResult`
/ `Event` / `Delta` coming out. In-process Go channels are the v1 transport;
a gRPC transport is designed-for but not built (GDD §15) — flipping it on
must not change anything upstream of `Transport`.

## 1. Message flow

```
   UI process-domain                          Engine domain
  ┌─────────────────┐                        ┌──────────────────────┐
  │ T-INPUT          │                        │ T-ENGINE              │
  │  (tcell events)   │                        │  (phase pipeline,     │
  │       │           │                        │   owns world state)   │
  │       v           │   Command              │       │                │
  │ T-RENDER ─────────┼───────────────────────>│ commandRegistry-      │
  │  (never blocks)   │  Transport.SendCommand │  decoded, Validate()   │
  │                    │  (bounded, may return  │       │                │
  │                    │   ErrCommandQueueFull) │       v                │
  │                    │                        │  engine module        │
  │                    │   CommandResult        │  processes it          │
  │ T-VIEWS  <─────────┼────────────────────────┤       │                │
  │  (applies deltas    │  echoes CorrelationID  │       v                │
  │   to view models,   │                        │  T-SUBSCR              │
  │   double-buffered)  │   Event (0..n)         │  (per-subscription     │
  │       ^ <───────────┼────────────────────────┤   delta computation)  │
  │       │             │                        │       │                │
  │       │             │   Delta (0..n, live    │       v                │
  │       └─────────────┼────subscriptions only)─┤  POOL-SIM (256 fixed   │
  │                    │                        │  shards, phase barrier)│
  └─────────────────┘                        └──────────────────────┘
        ^                                              ^
        └─────────────── Subscribe / Unsubscribe ──────┘
             (a Command like any other; see §5)
```

Two independent loops on the UI side (UI-SPEC §1): the **input loop**
(`T-INPUT`/`T-RENDER`) sends `Command`s and echoes keystrokes in <10 ms,
never blocking on the engine; the **render loop** (`T-VIEWS`) consumes
`Delta`s at a 10 Hz UI tick. They are decoupled because `Transport` makes
them decoupled — sending a command never waits on a delta, and a slow or
absent view reader never stalls the engine (§3, §6).

## 2. The envelope

Every `Command` (`internal/protocol/envelope.go`):

| Field             | Type             | Rule                                                                 |
|---|---|---|
| `ProtocolVersion` | `string`         | Must equal the `ProtocolVersion` const (`"1.0"`). Rejected otherwise. |
| `CorrelationID`   | `CorrelationID`  | **Mandatory, non-empty.** Minted by the initiating side (`NewCorrelationID()` or caller-supplied). Propagates to every `CommandResult`/`Event`/`Delta` it causes, and into logs and error records (GR#1). |
| `IssuedAtTick`    | `Tick` (`int64`) | Simulation time. **Never wall clock** — this package contains no `time.Now()` call anywhere, enforced by review and by the "no time import" convention in every file. |
| `Kind`            | `Kind` (`string`)| One of the registered command kinds (§4). Decoding an unregistered kind is a typed `*UnknownKindError`, never a panic. |
| `Payload`         | `CommandPayload` | The typed struct for `Kind` (commands.go). `Command.Validate()` checks the payload's registered kind agrees with the envelope's `Kind` field. |

Outbound messages echo causality, not the full envelope: `CommandResult`,
`Event`, and `Delta` each carry `CorrelationID` (empty when nothing
specific caused them — most `Delta`s are caused by the ordinary passage of
simulation time, not a single command) and a `Tick`. They do **not** carry
`ProtocolVersion` — only the UI->engine direction needs a version gate,
since the engine that decoded a `Command` at version 1.0 is, by
construction, the same engine emitting 1.0-shaped output.

`ErrorRef{Code, Display}` — not a Go `error` — is what a rejected
`CommandResult` carries. The two domains share no memory, so nothing that
crosses the seam may be a live Go error value; `Code` is a `data/errors.json`
registry code (`MET-P###` for this package's own errors) and `Display` is
the already-resolved human string (GR#1: "every user-visible failure shows
its registry code + correlation ID").

## 3. Command vocabulary (v1, skeleton-era)

| Kind             | Payload fields                          | Purpose |
|---|---|---|
| `AdvanceTicks`   | `N int64`                               | Advance the sim by N logistics day-ticks (GDD §3), then yield — the headless/replay/single-step primitive. |
| `SetSpeed`       | `Speed int`                             | Set the running speed multiplier (GDD §3: 1/2/3, 8 in debug). Distinct from `Pause` on purpose (§3.1). |
| `Pause`          | *(none)*                                | Pause the clock. Idempotent. |
| `Resume`         | *(none)*                                | Resume at the previously set speed. Idempotent. |
| `Subscribe`      | `ViewName string`, `Params map[string]string` | Open a live view subscription (§5). |
| `Unsubscribe`    | `SubscriptionID SubscriptionID`         | Close one. Deltas stop the instant this is processed. |
| `InspectEntity`  | `EntityRef string`                      | Life-write / detail-resolve one entity (GDD §5.2). |
| `Debug`          | `Op string`, `Args map[string]string`   | Debug-mode-only escape hatch (F12 panel ops), still fully versioned/correlated/registry-errored. |

This is **not** the gameplay vocabulary (`BuyLand`, `SetBudget`, zone/build
commands from GDD §15's example list) — those belong to the engine modules
that own them and arrive as those modules go real, one at a time, behind
the module registry (M0-ENG §2). Freezing the vocabulary at
protocol-control-plane commands only, rather than guessing gameplay
commands early, is a deliberate scope decision — see §7's open questions.

### 3.1 Why `Pause`/`Resume` aren't `SetSpeed(0)`

`Pause` is bound to a single, urgent key (Space, UI-SPEC §3) and must be
distinguishable in logs/replay from "the player chose zero speed" (which
isn't a real option in GDD §3's speed table anyway). Collapsing them would
also mean `SetSpeed` needs a magic sentinel value, which fights the
table-driven decode's assumption that a payload's shape fully describes the
command.

## 4. Extension rules — how v1.1 adds a command without breaking v1

1. Add a new `Kind` constant in `commands.go`, named exactly like the
   command.
2. Add its payload struct implementing `CommandPayload` (a `commandKind()`
   method returning the new constant).
3. Register it in `commandRegistry`.
4. Never remove or renumber an existing `Kind` constant, and never reuse a
   retired string value — `H-REPLAY` fixtures (MOD-013) must keep decoding
   forever.

Adding a **field** to an existing payload is additive and safe (Go's
`encoding/json` gives a missing field its zero value on decode, which must
mean "old behaviour"). Removing or repurposing a field, or changing what a
`Kind` *means*, is a breaking change and gets a **new** `Kind`
(`AdvanceTicksV2`, not a silent redefinition) — never bump `ProtocolVersion`
for an additive change; reserve that for a genuinely incompatible envelope
change (e.g. adding a new mandatory envelope field with no safe zero
value).

## 5. View subscriptions (UI-SPEC §6)

"UI subscribes to named projections of state... engine pushes deltas only
for live subscriptions — this is also exactly the remote-play seam."

- **Naming scheme** (`subscription.go`, `ValidateViewName`):
  `<screen-or-scope>.<projection>[.<id>[.<sub-projection>]]`, lowercase,
  dot-separated. Segment 1 is either an F-screen key (`f1`..`f12`) for
  screen-scoped dashboards, or an engine-domain noun (`junction`, `citizen`,
  `district`) for entity-scoped views addressed by ID. Examples: `f1.viewport`,
  `f2.ledger`, `junction.14.approaches`, `citizen.482913.detail`.
- **Allocation:** `SubscriptionAllocator` hands out sequential
  `SubscriptionID`s (`"sub-1"`, `"sub-2"`, ...) — deliberately not
  time-based or random, so allocation is cheap, allocation-free, and
  reproducible from a fixed command order (useful for `H-REPLAY` fixtures).
- **Liveness:** deltas flow **only** for subscriptions between an accepted
  `Subscribe` and the processing of the matching `Unsubscribe` — not "until
  the UI stops reading," which is a transport-buffer condition, not a
  subscription-lifecycle one.
- **Ordering & gaps:** `Delta.Seq` is monotonic per subscription, starting
  at 1. `SeqTracker.Observe` detects gaps (transport dropped a delta under
  the full-buffer policy, §6) and duplicate/out-of-order arrivals (a
  transport bug in v1's single-writer design). v1 has **no resync
  protocol** — a detected gap is surfaced (log + UI-SPEC §1 staleness dot),
  not auto-healed. See §7.
- **The remote-play seam:** because the UI never reaches into engine state
  directly — only ever through named, subscribed projections — the same
  subscription contract is what a hypothetical remote/cloud UI would speak
  over the gRPC transport (§6). No second design is needed for remote play;
  it falls out of this one.

## 6. Transport (`transport.go`)

`Transport` is the interface both `InProcTransport` (built) and a future
gRPC transport (not built, see mapping below) satisfy:

```go
type Transport interface {
    SendCommand(cmd Command) error          // never blocks; error = "not sent"
    Results() <-chan CommandResult
    Events()  <-chan Event
    Deltas()  <-chan Delta
    Close() error
}
```

`SendCommand` is synchronous-return, not channel-based, because its caller
(`T-INPUT`/`T-RENDER`) must never block (UI-SPEC §5's <10 ms echo budget).
`Results`/`Events`/`Deltas` are channels because their consumer (`T-VIEWS`)
is a dedicated loop built to range over them.

### Drop/stale policy

UI-SPEC §1: *"if a delta is late, the last frame stands and a staleness
dot shows in the status bar."* `InProcTransport`'s outbound sends
(`SendResult`/`SendEvent`/`SendDelta`, called from the engine side) are
**non-blocking**: the engine must never stall waiting on a slow or absent
UI reader. When a channel is full, the **oldest** queued message of that
kind is evicted to make room for the newest — freshness wins, so "the last
frame" really is the last one, not whichever frame happened to be queued
first. `Commands()` (UI -> engine) is the mirror image: **never dropped
silently** — a full command queue returns `ErrCommandQueueFull` to the
caller, because a lost player action is a worse failure mode than a lost
delta (the next delta always supersedes; a lost `Pause` does not).

This evict-oldest policy is applied uniformly to `Results`, `Events`, and
`Deltas` in v1 for implementation simplicity — see §7 for why that's
flagged as an open question rather than settled.

### gRPC mapping (documented now, not built)

| `Transport` concept | gRPC shape |
|---|---|
| `SendCommand` | Unary RPC (`Engine.Send`); `ErrCommandQueueFull` etc. map to `codes.ResourceExhausted` and friends. |
| `Results`/`Events`/`Deltas` | One server-streaming RPC per channel (or one multiplexed stream carrying a `oneof`, if cross-kind head-of-line blocking turns out to matter). |
| Drop/stale policy | Moves to the **server** side of the streaming RPC — a slow client's flow-control window fills, and the server makes the same evict-oldest choice `InProcTransport` makes locally. |
| `Close` | Closing the client connection / cancelling the streaming RPCs' contexts. |
| Wire codec | `codec.go`'s JSON encode/decode is reusable as-is if the gRPC transport stays JSON-over-gRPC; switching to native protobuf messages is deferred to when gRPC is actually switched on (GDD §15). |

No gRPC package or dependency exists in `internal/protocol` — this table is
a mapping sketch, not an implementation.

## 7. Open questions for freeze review

1. **CommandResult under the drop policy.** §6's evict-oldest policy is
   applied to `CommandResult` for implementation simplicity, but a dropped
   acceptance/rejection is arguably a worse UX gap than a dropped `Delta`
   (which the next delta supersedes) or a dropped `Event` (which the
   ticker can live without). Should `CommandResult` get its own,
   stricter policy — e.g. a larger dedicated buffer, or backpressure onto
   `SendCommand` — instead of sharing `Delta`'s policy?
2. **Gap resync.** `SeqTracker` detects gaps but v1 has no protocol
   message to request a resync or a fresh subscription snapshot. Is a
   silent gap (logged + staleness dot) acceptable through M3, or does the
   TUI need an explicit "resubscribe on gap" client-side rule before then?
3. **`Debug` payload shape.** `Op`/`Args` as free-form strings keeps the
   protocol from needing to know every debug operation, but it also means
   no compile-time check that a given `Op` got the `Args` it expects. Worth
   a typed `Debug` sub-vocabulary once the F12 panel's real operation list
   stabilizes?
4. **View `Params` typing.** Same trade-off as `Debug.Args`:
   `map[string]string` is maximally flexible and protocol-change-free for
   new views, at the cost of no compile-time shape checking per view. Revisit
   once the first few real views (viewport, ledger, junction approaches)
   show whether their params converge on a common shape.
5. **Backpressure visibility.** `InProcTransport`'s `SendResult`/`SendEvent`/
   `SendDelta` return `bool` (sent vs. dropped) but nothing currently
   aggregates that into a metric the F12 panel could show ("N deltas
   dropped this session"). Worth wiring before M3's TUI work starts, so
   drop-policy tuning isn't done blind?

## 8. What this package deliberately does not do

- No gRPC/protobuf code or dependency (§6).
- No gameplay command vocabulary (§3) — that's each owning engine module's
  job as it goes real.
- No resync/replay-request protocol (§7.2).
- No validation of payload-internal invariants (e.g. `AdvanceTicksPayload.N
  > 0`) — `Command.Validate()` only guards the envelope contract; payload
  invariants are the receiving engine module's job, using registry-sourced
  errors (GR#7).
