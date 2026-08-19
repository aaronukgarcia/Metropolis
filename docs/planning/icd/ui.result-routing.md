# ICD: UI Result-Routing Seam (CommandResult/Delta/Event → screens)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. FULL 12-section treatment (ASM-1482). The engine already produces `CommandResult`/`Delta`/`Event`; the screens already expose `ApplyResult`/`ApplyDelta`; what does not exist is the transport-owning caller that reads the outbound channels and routes each message to the right screen. This ICD specifies that seam — where it lives, the correlation semantics, the routing-table shape, the drop/error semantics, and the determinism contract — before the build.

---

## 1. Identity

- **GUID:** `fb5bbde6-9be1-4875-b460-d203a6249606` *(`ui.core`'s inbound `RenderScreen` contract GUID — the dedicated result-routing seam has no GUID of its own and no `code.json` module entry yet; `ui.core` is the natural owning module because it already owns T-VIEWS (`ViewsLoop`, `internal/ui/core/views.go`) and is the registered consumer of `int.protocol`. This stands in until the routing seam is registered, see §12 Open Decision 1)*
- **Name:** `ui.result.routing`
- **Owning module (mkey):** `ui.core`
- **code.json edge ref(s):** `ui.core` → `int.protocol` is registered (outbound `b0b339f9-0052-469e-9138-0eb930798f69`, calling `int.protocol` module `19bed5ea-bcfd-4dd5-9be7-71def6c50fc4` inbound `e5f22baa-24a4-41ef-8ca1-41ed677fac9b`) — the seam reads `protocol.Transport.Results()`/`Events()`/`Deltas()` through it. The **router → each screen** edges (`ui.screen.finance`, `ui.screen.build`, `ui.screen.map`, `ui.screen.proj`, `ui.screen.trade`, `ui.screens.chrome`) are NONE YET — the router must be registered before it calls those screens' `ApplyResult`/`ApplyDelta` surfaces (GR#25).

---

## 2. Purpose

ASM-1482 (found by FEAT-014 r2): `internal/ui/screens/finance`'s `ApplyResult(protocol.CommandResult)` is correct but **unreachable** — and every sibling screen (`build`, `map`, `proj`, `trade`, `chrome`) documents in its own `screen.go` that it "does not block on or read any CommandResult/Delta — that is the caller's transport-owning responsibility." No such caller exists anywhere in the codebase: the engine-side composition root (FEAT-082, `internal/engine/compose`) never routes results to screens, `ui/core`'s `ViewsLoop` (the one existing T-VIEWS) consumes **only** `Deltas()` and publishes raw JSON patches to a `ViewStore` it never hands to a screen, and there is no ui-side composition root or chrome event loop. The consequence: every FIN-8-class acceptance criterion ("display the engine's rejection on the screen that issued the command") is stub-verifiable only, across all screens. This integration builds the single transport-owning router that drains the three outbound channels and dispatches each message to the owning screen, so a real rejection/result/event is actually shown.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `int.protocol` (`Transport`) | `Results() <-chan CommandResult` — the direct acknowledgement of one command (echoes the causing `CorrelationID`) | `protocol.CommandResult{CorrelationID, Tick, Accepted, Error *ErrorRef}` |
| `int.protocol` (`Transport`) | `Deltas() <-chan Delta` — per-subscription state patches (keyed by `SubscriptionID`, monotonic `Seq`) | `protocol.Delta{SubscriptionID, Tick, Seq, Patch json.RawMessage, CorrelationID}` |
| `int.protocol` (`Transport`) | `Events() <-chan Event` — discrete named occurrences (keyed by `Kind` prefix + `Severity`/`Crisis`) | `protocol.Event{Kind, Tick, Severity, Crisis, EntityRefs, Fields, CorrelationID}` |

The router reads only the `Transport` interface (never `*InProcTransport` directly, GR#20), so a future gRPC transport is interchangeable. It does **not** interpret `Delta.Patch` payloads — patch schemas belong to the view-owning screen (`ui/core/views.go`'s "this package does not interpret Patch payloads" discipline, extended to routing).

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| `CommandResult` routed to the screen that minted `CorrelationID` | that screen's `ApplyResult(res protocol.CommandResult)` (finance already implements it; build/map/proj/trade/chrome gain the same method) | `protocol.CommandResult` |
| `Delta` routed to the screen bound to `SubscriptionID` | that screen's `ApplyDelta(delta protocol.Delta)` (finance already implements it; `chrome.ApplyFiguresPatch`/`AddAlert` are the chrome-equivalent) | `protocol.Delta` |
| `Event` routed to the ticker/alert surfaces | `ui.screen.ticker` (by `Kind` prefix) and `ui.screens.chrome`'s alert stack (by `Severity`/`Crisis`) | `protocol.Event` |

No conservation-accumulator effect and no world-state mutation: the routing seam lives in the UI process domain (M0-ENG §1.1), which is explicitly presentation-only. A routed message that a screen drops or mishandles can never corrupt the simulation — the engine is the sole owner of sim state.

---

## 5. Update Class

**T2** — result/event/delta delivery to screens is coalescible presentation, not a world-state update. The transport already applies evict-oldest to these channels (`transport.go`'s "last frame stands" policy); a dropped or superseded result is a UX gap the next delta supersedes, never an invariant break. This is the proposal §3's coalescible class, distinct from the critical population/money/conservation tier.

---

## 6. Shard Scope

**Not shard-scoped.** The routing seam runs in the UI process domain on a single dedicated goroutine (the T-VIEWS role, M0-ENG §1.1), consuming channels — not `det` shards. The `SingleShard()`/`det.RunPhase` contract does not apply; the relevant concurrency contract is instead `ui/core`'s single-writer `ViewStore` discipline (the router is the sole writer of the front snapshot, T-RENDER is the sole reader).

---

## 7. Determinism Guarantee

The routing seam never feeds the world-seed determinism domain: it consumes already-computed `CommandResult`/`Delta`/`Event` values and forwards them; it computes nothing that enters a simulation decision. Its own internal iteration order is nevertheless deterministic by house style — the routing table is a slice of `(prefix, handler)` entries applied in registration order, never a map range (the same discipline `compose.go`'s `registrationOrder` and `ui/core`'s sorted-subscription iteration already follow), so a routing test can assert a stable dispatch order. `Delta.Seq`/`CommandResult.CorrelationID` are sim-clock/ID-derived, never wall-clock-derived. **No wall-clock time is read anywhere in this seam** — staleness is measured by comparing `Delta.Tick`/`CommandResult.Tick` against the engine's current tick, never against `time.Now()` (UI-SPEC §1's staleness dot, driven by `SeqTracker`).

---

## 8. Error / Registry Codes

- **`ui.core`** owns `MET-U001` (`ErrScreenInit`, terminal init failure) and `MET-U002` (`ErrMalformedDelta`, the malformed-patch log `ViewsLoop.logMalformed` already raises) — the latter is reused unchanged for a malformed routed delta.
- **Rejected `CommandResult`** carries a `protocol.ErrorRef{Code, Display}` (a registry code + already-interpolated display string, `envelope.go`) — the router passes it through verbatim to the screen; it never reconstructs the message from the code. `protocol.CommandResult.Validate` guarantees `Error` is present iff `Accepted` is false.
- **`MET-U`-range (new, to be registered)** — a routing-table miss (a `CommandResult`/`Delta`/`Event` whose key matches no registered handler) must raise a registry-sourced `MET-U` code rather than silently dropping the message (GR#1/GR#17); the code is added via `/new-error` under `ui.core`'s MET-U block when the router is built, not hand-picked here.

---

## 9. Resilience Behaviour

No retry/backoff: the three channels are receive-only and the transport owns their buffering. The relevant resilience contract is the transport's **evict-oldest drop policy** (`transport.go`): when a channel is full the oldest queued message is evicted for the newest — so a slow/absent UI reader never stalls the engine (M0-ENG §1.1's "no shared pacing"). The router makes that lossy policy observable via `protocol.SeqTracker`: a `Delta` gap is surfaced (staleness dot + `MET-U002`-class log), never silently swallowed. `CommandResult` drop is the worst UX gap of the three (a dropped acceptance/rejection for a specific command) — the transport's own doc comment flags it as an open question; this ICD rules that the router must size `DefaultResultBuffer` and surface result-buffer pressure so an evicted result is at least logged, even though v1 has no resubscribe-to-heal protocol. Catch-up: none — a superseded delta is superseded by design; the last frame stands.

---

## 10. Monitoring Signals

**Status:** the router is a live goroutine; its liveness is observable via whether `Run` is draining (the T-VIEWS role) — a stalled router surfaces as a growing transport buffer + an all-stale `ViewModels.AnyStale()`. **Staleness:** `ViewStore.Front().AnyStale()` is the UI-SPEC §1 staleness dot input, already wired through `ViewsLoop` and reused here. **Drop/gap rate:** `SeqTracker.Observe`'s returned `gap` is the per-subscription drop signal; a result-buffer pressure counter is the `CommandResult`-drop signal. **Peak load:** not a determinism input — purely operational; no new instrument beyond the existing phase/views loop timing.

---

## 11. Required Tests

- **Reachability (the ASM-1482 defect, the one test that actually closes it):** a router-level test that sends a command whose `CorrelationID` belongs to screen X, drives the engine loop, and asserts screen X's `ApplyResult` was invoked with the matching `CommandResult` — proving the "correct but unreachable" method is now reached, not merely present.
- **Correlation-match:** a test asserting a `CommandResult` is routed **only** to the screen whose minted `CorrelationID` equals it, and a `Delta` routed **only** to the screen bound to its `SubscriptionID` (`BindSubscription`/`subs` map) — a wrong-screen delivery fails.
- **Routing-table determinism:** a test asserting a fixed batch of mixed messages dispatches in registration order, byte-identical across runs (no map-range order).
- **Drop semantics:** a test that fills a channel and asserts evict-oldest leaves the newest message (and that `SeqTracker` reports the gap) rather than silently keeping stale data.
- **No-wall-clock:** the deterministic routing test above, run with a faked clock, asserting dispatch order is unaffected by wall-clock speed — the seam's own determinism guarantee, not the world's.

---

## 12. Change Control

Additive-only: a later ICD revision may ADD a route/Input/Output without a version bump provided no existing route's key or semantics changes; any REMOVAL or semantic change to an existing route/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected routing code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Where result routing lives — ruled: a ui-side transport owner, NOT ui.chrome, NOT the engine composition root.** ASM-1482's own description poses the options (`ui.chrome` event loop / a ui-side composition root / extend `feat.compositionroot`). Ruling and rationale: (a) `ui.chrome` is itself a screen that *receives* routed messages — it cannot be its own transport owner; (b) the engine-side `feat.compositionroot` must not route to screens because GR#20 forbids `internal/engine` from importing `internal/ui` (the UI/engine split is a lint-banned direction); (c) therefore the owner is a **ui-side composition root** — either a new `internal/ui/...` router package or an extension of the existing T-VIEWS owner in `ui.core`, consuming `int.protocol` (already registered) and calling each screen's `ApplyResult`/`ApplyDelta`. This ICD names `ui.core` as the owning module for the stand-in GUID but leaves "new package vs. extend `ui.core`" as a build-time choice Bev rules on.
2. **The router → screens `code.json` edges do not exist.** A new ui-side router importing `ui.screen.finance`/`ui.screen.build`/`ui.screen.map`/`ui.screen.proj`/`ui.screen.trade`/`ui.screens.chrome` must register those outbound edges (GR#25) before the imports are added; the screens' `ApplyResult`/`ApplyDelta` methods are already public and need no edge of their own.
3. **Which screens get `ApplyResult`.** `ui.screen.finance` already has it; `build`/`map`/`proj`/`trade`/`chrome` document they do not read results — the router build must add `ApplyResult` to each (a per-screen method addition, not a protocol change), and the exact rejection-display shape each screen uses is that screen's own concern, not this seam's.
4. **`CommandResult` drop policy is deferred, not solved here.** The transport's evict-oldest is least appropriate for `CommandResult`; v1 accepts it (uniform policy) with logging, and a future per-kind drop policy is an open question for the freeze review, not this build.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-19 | Initial ICD (ASM-1482) |
