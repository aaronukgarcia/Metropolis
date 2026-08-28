BOW code: FEAT-1972079852

# Acceptance criteria — FEAT-1972079852 (Engine Protocol Adapter)

**Feature:** React webconsole: engine protocol adapter — replace mock sim store with live Go engine feed.

**Mkey:** FEAT-1972079852

**Northstar ref:** docs/planning/northstar.md §2.3 — "Engine convergence: the Go engine is the product. The webconsole's mock sim is replaced by the live Go engine feed."

**Overview:** The webconsole currently runs a fully self-contained TypeScript simulation (mock sim store in `sim/`). This item wires it to consume live state deltas from the real Go engine instead, using the `int.protocol` wire format. The mock sim and live feed are behind a single adapter boundary (`sim/backend.ts` seam), allowing incremental adoption (one view at a time, starting with `f2.finance`) and graceful fallback to mock when engine is unreachable.

---

## Design Decisions Flagged for Lead

### DD1: Transport Layer — HTTP/WebSocket or Future Architecture
**What transport carries deltas from Go engine to React webconsole?**

**Scope: DESIGN-DECISION, NOT in this item's scope.** Currently:
- Go side: protocol.InProcTransport exists only in-process (subscribe.go line 456, DeltaSink interface line 58-59). 
- Cmd/metropolis: runs engine and TUI together in one binary, no network layer.
- Webconsole: runs independently in the browser, no I/O to a running engine yet.

Options:
- **HTTP long-poll** — simple, no extra dependencies, polling latency ~ 100ms+
- **WebSocket** — bidirectional, lower latency, requires server adoption
- **gRPC-web** — mentioned in int.protocol's doc comment as "dormant behind a flag", not built in v1
- **Direct embedding** — run Go engine as WASM in browser (future, complex)
- **Defer to engine lifecycle** — for now, accept that webconsole and engine are separate processes, connect them when server/deployment architecture is decided

**No decision needed from Aaron for THIS item** — the protocol seam (int.protocol) is transport-agnostic, proven by finance_publish.go/chrome_publish.go working with the in-process transport. This item's ACs assume the transport EXISTS and is reachable at a TBD URL/address (placeholder: `http://localhost:9999/`); the transport's own implementation is a follow-up.

**Flag for Architect:** The transport layer is a significant decision tree (security, latency, deployment shape). Recommend sketching the shape in `docs/planning/engine-transport-choice.md` before a dev lane gets dispatch.

---

### DD2: Version Mismatch Behaviour
**When a delta arrives with a schemaVersion the webconsole doesn't recognize, what happens?**

**Options:**
- **Option A (chosen unless overruled):** Log a LOUD error, do not apply the delta, stay at last-known-good state. Display a "Version mismatch" banner to the player (not a crash, not silent stale data).
- **Option B:** Silently skip unrecognized versions (risks rendering stale data as fresh).
- **Option C:** Attempt a best-effort decode, best-guess missing fields.

**Rationale for A:** GR#1 (aggressive error trapping). A schema mismatch is a deployment issue (new engine, old webconsole, or vice versa), not a transient error. Silence it and the player sees numbers that don't match the engine's computation — the exact "confident wrong number" failure that finance_publish.go's sourcing ledger exists to prevent.

**Flag for Aaron:** Confirm Option A, or if deployed separately, whether the banner strategy is acceptable (vs. refusing the connection entirely).

---

### DD3: Replay Journal Interaction
**How do player actions (Buy/Zone/Build/Demolish, speed changes, pause) reach the Go engine?**

The journal (sim/journal.ts) records player actions in TypeScript, but the Go engine has its own command loop (engine.core/commands.go). 

**Options:**
- **Option A (straw man, would NOT work):** Journal remains TypeScript-only; Go engine never sees user actions.
- **Option B:** User actions in the webconsole are sent as `protocol.Command` to the engine via the transport; engine consumes them via `core.RunCommandLoop`, responds with `CommandResult` echoing the same `CorrelationID`. Journal records BOTH the outbound command and the engine's response.
- **Option C:** Dual-path: actions sent to Go engine AND applied locally to mock state (the "optimistic update" pattern). Journal records the local effect; engine response is checked for divergence.

**Chosen: Option B.** This item is "replace mock sim with live feed"; Option B is what "replace" means. Actions originate in the UI, are validated by the engine, and only then does the UI update. The journal's record is the canonical wire trace, not the mock state.

**Consequence:** Every command must carry a CorrelationID (protocol.CorrelationID, GR#1). The webconsole's `recordAction` function needs to mint these. The journal is no longer a replay of "state snapshots" but a replay of "commands and their responses" — this is the whole point of FEAT-1972079897's hard-reset-replay.

**Flag for Aaron:** Confirm Option B is the intent. This is a significant change to what "replay" means in the webconsole.

---

## Acceptance Criteria

### Functional — Adapter Boundary

- **AC-1 (Seam: mock sim + engine feed, one interface).** The `SimState` update path has exactly ONE ingress point — a single adapter function or method name (illustrative: `applyEngineDelta(delta: Delta)` on the store) — that accepts both mock-sim reducer actions AND incoming `protocol.Delta` messages. Existing pure-reducer calls from the UI (Build, Zone, Demolish, SetSpeed) are NOT changed; they continue to dispatch via the existing Action path until the engine feed is live. Check: `grep -n "applyEngineDelta\|adaptDelta\|feedDelta" webconsole/src/sim/store.tsx` (or equivalent) finds one entry point; no second `dispatch` path for "engine results" exists parallel to the original.

- **AC-2 (Decoding wire schema to store state).** `protocol.Delta` messages are decoded using the wire schema defined in compose's publishers (e.g., `financeBalanceSheetWirePatch` in finance_publish.go, replicated as a TypeScript type — mirrors finance_publish.go's explicit "duplicated independently, never importing from Go" pattern per GR#20). Each view's patch shape is documented in a TypeScript type (illustrative: `type FinanceBalanceSheetPatch = { balanceSheet: BalanceItem[] }`). Check: `grep -n "BalanceSheetPatch\|BalanceItem\|wirescope" webconsole/src/sim/wire.ts` (or types.ts) finds at least the first arriving view's wire types; a passing test decodes a sample `protocol.Delta` JSON carrying that view's patch and asserts the decoded state matches a baseline fixture.

- **AC-3 (View subscription lifecycle).** A view subscription (e.g., `f2.finance`) follows this lifecycle:
  1. **Subscribe** — engine receives a `SubscribeCommand` with viewName="f2.finance" (optional params per ViewSubscriptionName grammar in internal/protocol/subscription.go).
  2. **Subscription allocated** — engine returns a `SubscriptionID` (unique, opaque string).
  3. **Deltas flow** — engine calls `sink.SendDelta` for each delta until unsubscribe (each carrying the same SubscriptionID, incrementing Seq, stamped with Tick).
  4. **Unsubscribe** — webconsole sends `UnsubscribeCommand` with that SubscriptionID; engine deletes the subscription and stops sending deltas.
  
  Check: Sequence these four steps in a manual integration test: Subscribe via the transport, receive a live SubscriptionID, collect 3+ deltas with proper Seq progression, unsubscribe, verify no more deltas arrive. False-pass: a test that only checks "deltas arrive" without verifying Seq is monotonic, or verifying they stop after unsubscribe.

- **AC-4 (Graceful fallback to mock when engine unreachable).** If the engine transport (DD1's TBD address) does not respond within a configured timeout (placeholder: 2 seconds), or returns an HTTP error (4xx/5xx), or the connection is closed:
  1. A LOUD error is recorded (backend.recordError with type 'engine-unreachable').
  2. The UI displays a banner: "Engine connection lost — using cached state" (exact wording TBD by Aaron).
  3. The existing mock reducer continues to run for subsequent player actions (Build, Zone, etc.) — the city remains playable, just not synced to real engine state.
  4. Every 5 seconds (placeholder), attempt to reconnect to the engine and re-subscribe to live views.
  
  Check: Kill the engine / block its port, trigger a player action (e.g., place a building). Verify the error banner appears, the building is placed in local mock state, and 5 seconds later a reconnect is attempted. A false-pass would be: the UI crashes instead of falling back, or the banner is only a console.warn.

- **AC-5 (Determinism — same inputs produce same outputs).** A deterministic city built on the mock sim produces the same save/load/replay result whether it was computed under the TypeScript mock OR after being rebuilt under the Go engine via hard-reset-replay (FEAT-1972079897). The journal's recorded commands are the input; deterministic replay means the output (buildings, citizens, money, month, speed) is byte-identical. Check: Save a city under the mock sim at tick 500 (capture buildings, funds, population). Export the journal. Boot a new game, hard-reset with that journal, rebuild under Go engine to tick 500. Verify buildings, funds, population are identical. False-pass: a test that only checks "buildings exist" without comparing exact positions, quantities, or funds.

### Protocol & Schema

- **AC-6 (protocol.Delta wire format consumed correctly).** A `protocol.Delta` (internal/protocol/envelope.go, see subscribe.go's `Publish` pass 4) carries: `SubscriptionID`, `Tick`, `Seq` (monotonic per subscription), `Patch` (JSON-encoded, view-specific), and optionally `CorrelationID`. The webconsole's adapter parses each field and validates:
  - `Seq` is monotonically increasing per SubscriptionID (same check as int.protocol AC-2, but consumer-side).
  - `Patch` is the expected schema for that subscription's view name (e.g., `f2.finance` patch carries `{ balanceSheet: ..., schemaVersion: 1 }`).
  - `Tick` is the engine's real simulation tick, allowing staleness checks (AC-12).
  
  Check: Parse a hand-crafted `protocol.Delta` JSON, verify each field is accessible and type-correct. A test that only checks "no exception was thrown" is not sufficient — verify the Seq is read as uint64, the Patch is JSON-unmarshalled, the Tick is accessible.

- **AC-7 (Version/schema mismatch detection and handling, per DD2).** When a delta's `schemaVersion` does not match the expected version for that view (e.g., delta carries schemaVersion 2, but the decoder expects 1):
  1. The delta is NOT applied to state.
  2. An error is recorded: `recordError("schema mismatch: f2.finance expected v1, got v2", { type: 'schema-mismatch', action: 'applyEngineDelta' })`.
  3. A banner appears: "Finance view schema changed — please refresh" (or wording per Aaron's DD2 ruling).
  4. The view stays frozen at last-known-good state.
  
  Check: Construct a delta with a mismatched schemaVersion, feed it through the adapter. Verify the error is recorded, the banner condition is set, and the state does not change. A false-pass would be: silently ignoring the mismatch, or crashing.

- **AC-8 (Incremental adoption — one view at a time, finance f2 first).** The adapter is structured so new views can be added without changing the subscription/delta-handling machinery:
  1. Each view subscription (e.g., "f2.finance", "f4.services", "engine.status") is independent.
  2. Subscribing to one view does not require subscribing to all.
  3. The first shipped view is `f2.finance` (finance_publish.go exists; FEAT-208 increment 2 is complete).
  4. Additional views (f4.services, engine.status) land as separate PRs, reusing the same adapter infra.
  
  Check: Grep for hard-coded view names in the adapter code — should be minimal (only in a subscriber registry, not sprinkled everywhere). A test subscribes to "f2.finance" alone, without "f4.services" being registered, and deltas arrive correctly. False-pass: code that assumes all views are subscribed together.

### Latency & Staleness

- **AC-9 (Display staleness honestly — cite GR#15, BUG-424 lesson).** Every rendered value sourced from an engine delta carries a freshness indicator:
  - If the delta's `Tick` < `state.tick` by more than 1 tick (placeholder threshold), OR the delta hasn't arrived in 1 second (placeholder staleness window), render the value with a **[STALE]** marker or dim the text.
  - The "dogfood hot-upgrade" lesson (BUG-424): a badge showed "v1.4.2" (latest) while the engine was still running v1.4.0 code (old binary, hot-reloaded). This item prevents that by making staleness VISIBLE.
  
  Check: Inject a delta with Tick 100, then set state.tick to 105. Verify the value is marked [STALE] in the rendered output. A test that only checks "the value is rendered" is not sufficient — verify the staleness marker appears.

- **AC-10 (Latency display — show poll rate, not pretend real-time).** The status bar or debug view shows: "Engine feed: [status: live/reconnecting/stale] [seq: 42] [latency: 240ms]" (exact layout TBD). This is honest about the connection state, not showing "live" when the last delta was 5 seconds ago. Check: Fire up engine and webconsole, observe the latency number. Kill the engine, verify it flips to "reconnecting". False-pass: a status that says "live" even after the engine is down.

### Player Actions & Journal

- **AC-11 (Commands carry CorrelationID — GR#1).** Every user action (Buy, Zone, Build, Demolish, SetSpeed, Pause) mints a new `protocol.CorrelationID` (via `protocol.NewCorrelationID()` or equivalent) before sending to the engine. The journal records this ID alongside the action. The engine's response echoes it back. Check: Perform a Build action, capture the minted ID, subscribe to a delta that responds to that action (e.g., buildings added = command accepted), verify the delta's CorrelationID matches. False-pass: actions sent without IDs, or IDs not echoed in responses.

- **AC-12 (Journal records wire trace, not mock state).** The journal's `recordAction` function now accepts `protocol.Command` (outbound) and `protocol.CommandResult` (inbound response) instead of (or in addition to) TypeScript Action objects. On replay, commands are sent to the engine in order; the engine's responses update the journal's recorded outcomes. Check: Export a saved game's journal, inspect its JSON — see both outbound commands and their engine responses, not just TypeScript state snapshots. Replaying that journal into a fresh engine produces the same city. A false-pass would be: journal still records TypeScript state, not wire commands.

### Error Handling & Registry

- **AC-13 (Registry-sourced errors, GR#7).** Any new error this item introduces (transport failure, schema mismatch, subscription error) is claimed from the protocol package's reserved error range (internal/protocol/codes.go, e.g., `MET-P0xx` codes, see FEAT-042 AC-32 precedent). Check: `grep -n "ErrSchemaVersionMismatch\|ErrEngineUnreachable" internal/protocol/codes.go` shows the error constants; they are listed in data/errors.json's reserved range. A dynamically constructed error message is wrapped with one of these codes, never used as the error itself.

- **AC-14 (Subscription errors are visible, not silent).** If the engine rejects a Subscribe command (unknown view, invalid parameters, engine sealed), the error is:
  1. Recorded as a correlation-linked error (the command's CorrelationID is preserved in the error).
  2. Displayed on-screen: "Failed to subscribe to [view]: [reason]" (e.g., "Failed to subscribe to f2.finance: engine not ready").
  3. The UI falls back to mock state for that view only.
  
  Check: Attempt to subscribe to an unregistered view name, e.g., "f2.fantasy". Verify the error appears on screen, the correlation ID is preserved, and the UI doesn't crash. False-pass: error only in console.log, not visible to player.

### Testing & Determinism

- **AC-15 (Unit test: Delta sequence validation).** A test validates that the adapter correctly tracks per-subscription Seq. Subscribe to f2.finance, feed 5 deltas (Seq 1–5), then feed a delta with Seq 3 (a gap, a repeat, or a duplicate). Verify the adapter detects and logs the gap/repeat. GR#21 discipline: the Seq gap itself is a valid state (engine may skip a delta if a producer errors on pass 2 of subscribe.go's Publish, per subscribe.go line 555 comment — "subscription pass 1 saw but Unsubscribe removed before this lock re-acquires is simply absent"). The test proves the check exists, not that gaps never happen. Check: Call the validator with Seq=[1,2,3,3,5] (duplicate 3, missing 4). Verify the adapter logs the duplicate and the gap (not as a fatal error, just a note — GR#17 silent-failure detection).

- **AC-16 (Integration test: Mock fallback).** A test suite (or manual scenario) starts with mock sim running, subscribes to f2.finance, places buildings and runs 10 ticks. Then "kill" the engine (block the port / close the connection), run 5 more ticks under mock, then "restart" the engine and reconnect. Verify: (1) cities built under both phases; (2) state at tick 15 differs between "mock-only" (what the webconsole did) and "engine-rebuilt" (what would happen on hard-reset-replay), making the divergence visible; (3) the journal's commands allow replaying the full sequence on the real engine to rebuild what the mock computed.

### Documentation

- **AC-17 (Protocol adapter documented as the int.protocol consumer).** `webconsole/src/sim/backend.ts` (or the adapter's file) includes a doc comment stating:
  - It consumes `protocol.Delta` and `protocol.SubscriptionID` from int.protocol.
  - It replaces the earlier mock-only pattern (cite engine.ts's now-deprecated `reducer`).
  - View subscriptions are independent; one view's delta does not block another's.
  - Fallback to mock is automatic on transport failure.
  
  Check: `grep -n "protocol.Delta\|SubscriptionID\|Seq\|Patch" webconsole/src/sim/backend.ts` shows at least one doc comment citing the protocol and explaining the seam.

- **AC-18 (Wire schema types documented, field-for-field mirror of Go publishers).** Each view's wire type (e.g., `FinanceBalanceSheetPatch`) is defined in TypeScript as a comment-documented interface, with a top-line doc citing the Go mirror (e.g., "mirrors compose.financeBalanceSheetWirePatch from finance_publish.go"). Field names and types match exactly, with a note explaining why duplication is necessary (GR#20: engine never imports UI, so schemas are independently maintained). Check: `grep -n "financeBalanceSheetWirePatch" webconsole/src/sim/wire.ts` finds the TypeScript mirror and its doc comment naming the Go original.

---

## Out of Scope

- The transport layer itself (HTTP, WebSocket, gRPC — DD1). This item assumes it exists and is reachable; implementing the actual server/client bridge is a separate build task.
- Resubscribe-to-heal-a-gap protocol (int.protocol already defers this; this item simply doesn't implement it either).
- Views beyond `f2.finance` — `f4.services`, `engine.status`, etc. are follow-up PRs, not this item.
- Rendering the finance balance sheet itself — AC-2 proves the wire schema decodes; the UI screen that renders it is a separate task (likely already exists in internal/ui/screens/finance, this item only wires its backing data).
- Bidirectional push (engine initiating commands to the browser) — not in v1 scope.
- Offline-first / conflict resolution (CAP theorem decisions) — deferred.

---

## References

- **Protocol spec:** internal/protocol/envelope.go, commands.go, deltas.go, subscription.go; code.json `int.protocol` entry; docs/planning/acceptance/int.protocol.md (INT-001).
- **Engine publishers:** internal/engine/compose/finance_publish.go (the first view this item feeds from), chrome_publish.go, services_publish.go; see subscribe.go's ViewPatchFunc for the pattern.
- **Transport:** int.protocol notes a "gRPC dormant behind a flag"; the actual production transport is a DD (DD1 above). In-process transport lives in protocol.InProcTransport (internal/protocol/transport.go); the webconsole will use a network transport (HTTP/WebSocket/etc.) instead.
- **Webconsole mock sim:** webconsole/src/sim/engine.ts (pure reducer), store.tsx (React provider), backend.ts (error capture, to be extended to adapter seam).
- **Journal & replay:** webconsole/src/sim/journal.ts (records actions), replay.ts (savepoint/restore), genesisReplay.ts (hard-reset-replay — FEAT-1972079897, related to this item's replay contract).
- **Determinism & staleness lessons:** docs/planning/northstar.md §3 (waypoints), memory entries "dogfood-hotupgrade-vs-engine.md" (BUG-424), "built-but-not-wired.md" (the dominant defect class this item avoids by making delta flow explicit).

---

## Design Decisions Summary

| DD | Title | Option A (Default) | Option B | Flag For |
|----|----|----|----|---|
| DD1 | Transport layer | HTTP/WebSocket/gRPC (defer choice) | Direct WASM embed | Architect — shape the decision tree in docs/planning/engine-transport-choice.md |
| DD2 | Version mismatch | Log LOUD, show banner, freeze state | Silently skip | Aaron — confirm error-display strategy |
| DD3 | Replay + action flow | Commands sent to engine (B) | Dual-path optimistic (C) | Aaron — confirm "replay = wire trace" is the intent |

---

## Escalations

- **For Architect (DD1):** The transport layer is foundational. Without a decision on HTTP vs. WebSocket vs. gRPC, a dev lane can implement the adapter logic (ACs 1–18) but cannot integrate end-to-end. Recommend sketching DD1's choice before dispatch.

- **For Aaron (DD2, DD3):** DD2 and DD3 are design-level decisions affecting what the UI shows (staleness banner, version-mismatch behaviour) and what "replay" means going forward. Recommend confirming these before dev begins (they're low-cost to decide now, high-cost to redo if misunderstood).

- **For Bill:** Before dispatch, confirm DD1 shape is acceptable (i.e., the dev lane's scope does NOT include building the transport, only the adapter logic). If transport is in scope, scope the item accordingly.

- **For Tester:** AC-4 (graceful fallback) is fragile to test without a controllable transport mock. Recommend building a test double that can simulate "transport unreachable" and "delayed response" scenarios. AC-15 (Seq validation) requires mocking the delta stream. Test fixtures are provided (or can be generated from existing finance_publish.go outputs).

- **Assumptions logged separately (ASM-xxx TBD by the BA):** View names are stable (no renaming "f2.finance" to "f2.ledger" without a migration), schemaVersion is an integer (not a semantic version string), Seq wrapping is not expected within a single session (Seq is uint64, would wrap after ~18 billion deltas per subscription, acceptable for a session lasting hours).

---

# Implementation Notes for Dev

- **Start with `f2.finance`** — it's the only view currently published (finance_publish.go exists, FEAT-208 inc2 complete). This narrows scope for the first build.
- **Adapter entry point:** A single function `applyEngineDelta(delta: protocol.Delta)` in `backend.ts` (or a method on the store) is the seam. Existing reducer calls (Build, Zone, etc.) are NOT changed. The adapter quietly swaps the source of truth from mock to engine.
- **Transport placeholder:** For local testing, HTTP long-poll to `http://localhost:9999/subscribe/{viewName}` and `http://localhost:9999/command` is sufficient. (Details per DD1's eventual ruling.)
- **Error handling:** Use `recordError` from backend.ts (GR#1 already in place). Every transport/subscription error goes through it.
- **Journal:** The journal currently records TypeScript actions. A modest refactor records wire commands instead. On replay, wire commands are sent to the engine.

