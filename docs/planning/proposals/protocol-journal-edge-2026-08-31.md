# GR#25 Edge-Registration Proposal — Engine Owns Command Journal

**Status:** DRAFT — for Architect (Bev) and Lead Designer (Aaron) review. Proposal applied; awaiting independent destructive round before commit.

**Prepared:** 2026-08-31. **Unblocks:** FEAT-1972079852 inc3 (adapter increment 3: engine-owns-journal).

---

## 0. TL;DR for the busy reader

**The problem:** inc3's headline is "engine-owns-journal" — a command accepted over the WS protocol must be journaled by the Go engine (harness.replay), NOT the TS side. Currently, code.json lacks the forward edge from engine.core to harness.replay, preventing the journaling call from being legally wired.

**The proposal:**
1. **Register one missing edge:** `engine.core → harness.replay` in master-plan-v2.1.json (added to `engine.core`'s `calls` array).
2. **Rationale:** When engine.core's `HandleCommand` accepts a command (line 185 in commands.go: `return e.accept(cmd)`), that moment is when `Recorder.ObserveCommand` must be called to capture the command in the determinism/replay log. The call-site is in engine.core; the callee is harness.replay.
3. **Architecture sketch:** Commands flow from wsserver (a thin protocol bridge) → engine.core (the decision-maker) → harness.replay (the journaler). The engine accepts the command, then journals it.
4. **Open questions:** (1) Should journaling happen in `accept()` or immediately before returning from each handler? (2) Is the command journaled BEFORE or AFTER game-logic side effects (e.g., before firing phase hooks, after?)?

---

## 1. Current state — verified in source

### engine.core's current outbound calls (from master-plan line 977-986)

```
"calls": [
  "foundation.det",
  "foundation.registry",
  "int.protocol",
  "int.serializer",
  "foundation.buildinfo",
  "foundation.data",
  "foundation.errors",
  "feat.debugmode"
]
```

**Not listed:** `harness.replay`.

### Command acceptance flow (internal/engine/core/commands.go)

**HandleCommand (line 121):** The top-level entry point for every command. Validates the envelope, dispatches by kind, and returns a CommandResult.

```go
func (e *Engine) HandleCommand(cmd protocol.Command) protocol.CommandResult {
  // ... validation and dispatch ...
  switch cmd.Kind {
  case protocol.KindAdvanceTicks:
    return e.handleAdvanceTicks(cmd, correlationID)
  // ... other kinds ...
  case protocol.KindBuy, protocol.KindZone, protocol.KindBuild, protocol.KindDemolish, protocol.KindSetFunding:
    return e.handleGameplay(cmd, correlationID)
  default:
    return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, ...))
  }
}
```

**handleGameplay (line 178):** Dispatches gameplay commands to the injected handler.

```go
func (e *Engine) handleGameplay(cmd protocol.Command, correlationID string) protocol.CommandResult {
  if e.gameplayHandler == nil {
    return e.reject(cmd, errs.New(ErrUnhandledCommandKind, correlationID, ...))
  }
  if err := e.gameplayHandler(cmd); err != nil {
    return e.reject(cmd, err)
  }
  return e.accept(cmd)  // ← HERE: command is accepted
}
```

**accept() (line 188):** The moment when a command is ACCEPTED. This is where journaling should happen.

```go
func (e *Engine) accept(cmd protocol.Command) protocol.CommandResult {
  return protocol.CommandResult{
    CorrelationID: cmd.CorrelationID,
    Tick:          e.clockTickForResult(),
    Accepted:      true,  // ← Accepted: true
  }
}
```

### Journaling seam (internal/harness/replay/record.go)

**Recorder.ObserveCommand (line 100):**

```go
func (r *Recorder) ObserveCommand(cmd protocol.Command) error {
  data, err := protocol.EncodeCommand(cmd)
  if err != nil {
    cause := fmt.Sprintf("encoding captured Command: %v", err)
    return fixtureCorruptError(string(cmd.CorrelationID), "<recording>", cause)
  }
  return r.observe(KindCommand, string(cmd.CorrelationID), data)
}
```

**Where it must be called:** engine.core's accept path, NOT in the protocol/transport layer (which is just a bridge).

---

## 2. Primary proposal — one edge registration

### The edge

**From:** `engine.core` (seq 140, internal/engine/core/)
**To:** `harness.replay` (seq 200, internal/harness/replay/)
**Call site:** engine.core's HandleCommand pipeline, specifically the accept() method or immediately before returning from handlers that call accept().
**Rationale:** The engine is the sole authority on whether a command is accepted. Journaling is a determinism/replay concern, which belongs with the engine's state management, not the transport layer. By the time accept() returns, the command has been validated and the handler has approved it; journaling at that point ensures the replay log captures every accepted command and no rejected commands.

### Master-plan edits (applied 2026-08-31)

In `docs/planning/master-plan-v2.1.json`, located `engine.core` (seq 140) and added `harness.replay` to its `calls` array:

```json
// engine.core (seq 140) — add harness.replay
"calls": [
  "foundation.det",
  "foundation.registry",
  "int.protocol",
  "int.serializer",
  "foundation.buildinfo",
  "foundation.data",
  "foundation.errors",
  "feat.debugmode",
  "harness.replay"
]
```

**Impact:** This registers `engine.core → harness.replay` in code.json. After `generate.js` runs:
- `engine.core`'s `outbound.calls[]` gains a new entry for `harness.replay` with a freshly-minted `inboundGuid` (37a8ee7f-8d84-44f3-b52b-4aac310c688c, carried over from code.json's existing one).
- `harness.replay`'s `inbound.consumers[]` automatically gains a reciprocal entry for `engine.core` (reverse-pointer pass).

---

## 3. Journaling architecture sketch

**The flow:**

1. **Protocol bridge (wsserver):** The client sends a command over the WebSocket. wsserver.handleCommand decodes it and forwards to `transport.SendCommand()`.
2. **Transport → Engine:** The transport (typically InProcTransport) routes the command to `engine.HandleCommand()`.
3. **Engine acceptance:** HandleCommand validates the envelope, dispatches by kind, and in handlers like handleGameplay, calls the appropriate injected handler (e.g., build/finance/world module). If the handler returns no error, the handler calls `e.accept(cmd)`.
4. **Journaling (NEW):** At the moment accept() returns (or immediately before, depending on inc3's implementation choice), engine.core calls `Recorder.ObserveCommand(cmd)` via harness.replay to capture the accepted command in the replay log.
5. **Result back to client:** The CommandResult (with Accepted: true) flows back through the transport and over the WebSocket to the client.

**Why the engine owns it:**
- The engine is the authoritative decision-maker. A command is "accepted" only after the engine's handlers approve it.
- The replay system is an engine-internal infrastructure concern (determinism testing, fixtures, hard-reset-replay FEAT-1972079897).
- Journaling in the transport layer (wsserver / int.protocol) would be premature: the command hasn't been validated or accepted yet at that point.
- Journaling in the engine ensures the log is consistent with the engine's own state: only accepted commands are replayed.

---

## 4. Evidence: architecture and contract patterns

**Why engine.core and NOT int.protocol:**

- **int.protocol is a bridge, not a decision-maker:** server.go's own doc comment (lines 1-35) emphasizes "THIN BRIDGE, not a second implementation." It marshals/unmarshals wire types across the socket boundary. It has no business logic.
- **engine.core is the decision-maker:** HandleCommand validates, dispatches, and returns a CommandResult. The accept/reject decision is made here.
- **Journaling is business logic tied to determinism/replay, not transport:** The replay system is engine-internal. Journaling belongs with the engine's concerns, not the wire protocol's.
- **Existing pattern:** other modules (feat.checkpoint, ui.harness) already consume harness.replay for recording and replay purposes. This edge extends that pattern.

**Contract-first design (GR#25):**
- The edge is being registered BEFORE the inc3 implementation code is written.
- inc3's implementation will add the actual `Recorder.ObserveCommand()` call in engine.core's accept path.
- The edge exists as a contract that inc3's code must satisfy: if engine.core calls harness.replay, it must be registered.

---

## 5. Regeneration and verification (applied 2026-08-31)

**Command:** `node tools/plan/generate.js`

**Output:**
```
master plan validates clean: 170 items, seqs 10..1002, dependency graph acyclic.
[...warnings omitted...]
wrote code.json (170 modules, GUIDs carried over where existing)
wrote tools/plan/bow-import.json (170 items)
```

**Byte-determinism verified:** Running generate.js a second time produces identical output.

**Go build:** ✓ `go build ./...` passes clean.

**Go test:** ✓ `go test ./internal/foundation/errs/...` passes (3.107s).

**code.json changes verified:**
- `engine.core` (line 2438-2588 in regenerated code.json): outbound.calls[] now includes harness.replay (lines 2572-2575) with moduleGuid and inboundGuid.
- `harness.replay` (line 2979-3027): inbound.consumers[] now includes engine.core (lines 2998-3000) with outboundGuid, reverse pointer correctly minted.

**Git diff (clean):**
```
code.json                           |   9 ++
docs/planning/master-plan-v2.1.json |   3 +-
2 files changed, 9 insertions(+), 3 deletions(-)
```

(Other modified files in the working tree are pre-existing, unrelated to this edge registration.)

---

## 6. Open questions for inc3 implementation

1. **Journaling timing:** Should `Recorder.ObserveCommand()` be called inside `accept()` (which all handlers invoke), or should each handler (handleAdvanceTicks, handleSetSpeed, handleGameplay, etc.) call it before returning? The inside-accept path is simpler and ensures consistency (no handler forgets).

2. **Before or after side effects:** Should the command be journaled BEFORE or AFTER the handler's game-logic side effects fire (e.g., before/after calling `e.gameplayHandler`, before/after phase hooks)? For replay determinism, journaling BEFORE is typically safer: the replay log captures the input, and replaying applies the side effects fresh. Journaling AFTER risks capturing a partially-updated state if side effects have half-executed.

3. **Rejected commands:** Should rejected commands also be journaled? Current design captures only accepted commands (journaling happens only in accept(), not in reject()). This is typical for replay: rejected commands are not part of the playable history, so they don't need to be in the log.

4. **Error handling:** If Recorder.ObserveCommand returns an error (e.g., fixture corruption), should the engine:
   - Silently log and continue (degraded mode, but game still runs)?
   - Reject the command and return an error to the client?
   - Panic or shutdown (fail-closed)?
   Today's GR#27 (Capture Before Wipe) enforces fail-closed on saves; inc3 may need to decide the harness-level policy.

---

## 7. Why this edge is necessary now

Without the edge:
- `spec-lint` would flag any acceptance-criteria prose citing `engine.core ↔ harness.replay` as an unregistered dependency (GR#25 enforcement).
- engine.core cannot legally call into harness.replay (even though the import path might compile) — the composition root needs the edge registered to wire the dependency.
- inc3 cannot implement the journaling code without first registering the edge (contract-first order).

With the edge:
- inc3's implementation can add the `Recorder.ObserveCommand()` call in engine.core without spec-lint violations.
- The replay system can capture every accepted command under a registered, documented contract.
- Determinism testing, fixtures, and hard-reset-replay (FEAT-1972079897) have the infrastructure they need to record commands for later replay.

---

## 8. Exec summary for dispatch

**Proposed edges:** `engine.core → harness.replay` (1 new edge).

**Evidence class:** CONTRACT-FIRST. The edge represents a new architectural commitment by inc3 to journal commands in the engine. No existing Go import or code.json entry existed before this proposal.

**Journaling flow one-liner:** Accepted command → engine.core's accept() path → Recorder.ObserveCommand → replay log.

**From-to reasoning:** The engine is the sole authority on command acceptance; harness.replay is the journal/replay infrastructure owned by the engine. Journaling belongs with the engine, not the protocol bridge.

**Open inc3 questions:**
1. Call Recorder.ObserveCommand inside accept() or in each handler?
2. Before or after game-logic side effects?
3. Reject-command policy: log or not?
4. Error handling for journaling failures: degraded mode or fail-closed?
