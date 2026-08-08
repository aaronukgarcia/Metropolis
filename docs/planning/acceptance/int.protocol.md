BOW code: INT-001

# Acceptance criteria — int.protocol (INT-001)

**BOW code:** INT-001
**Spec refs:** §15 (Architecture, `docs/METROPOLIS-MASTER-v2.1.md` lines 261-267); UI-SPEC §1 (rendering architecture, lines 722-729) and §6 (view subscriptions, lines 779-781); M0-ENG §1.1 (process/thread topology, lines 792-817); code.json `int.protocol` entry; code.json `conventions.errorHandling.correlation`.
**Date:** 2026-08-08
**Status:** active
**Package under test:** `internal/protocol/` (path from `node claude-bow.js show INT-001`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/protocol/...`.

## Scope

The versioned engine<->UI command/event/delta protocol: envelope types, view-subscription contract, and the v1 in-process transport (gRPC dormant behind a flag, not built in this item).

## Acceptance criteria

### Functional

- **AC-1.** A `Command` carries a mandatory, non-empty `CorrelationID`. Check: `go doc ./internal/protocol Command` shows a `CorrelationID` field, and `go doc ./internal/protocol Command.Validate` (or equivalent) rejects an empty one — `grep -n "CorrelationID" internal/protocol/envelope.go` shows both the field and a `Validate` method returning an error (e.g. `ErrMissingCorrelationID`) when it is empty; a passing test exercises this (`grep -rn "ErrMissingCorrelationID" internal/protocol/*_test.go` finds coverage).
- **AC-2.** `Delta` carries a `Seq` field that is monotonically increasing **per subscription**, starting at 1 for the first delta after `Subscribe` is accepted. Check: `grep -n "Seq " internal/protocol/deltas.go` shows the field with a doc comment stating per-subscription monotonicity, and a `SeqTracker`-style gap detector exists (`grep -n "type SeqTracker" internal/protocol/subscription.go`) with a passing test asserting sequential Observe calls report `gap==0` and a skipped Seq reports `gap>0` (`grep -rn "func TestSeqTracker" internal/protocol/*_test.go` finds a test, and `go test ./internal/protocol/... -run TestSeqTracker -race -count=1 -v` passes).
- **AC-3.** Deltas are only produced for **live** subscriptions — a subscription that has been unsubscribed must not still receive deltas. Check: this is documented as a sending-side invariant (`grep -n "must stop pushing the instant Unsubscribe" internal/protocol/deltas.go` or equivalent wording matches), and `SeqTracker.Reset` (or equivalent) exists to forget a subscription's sequence state on unsubscribe (`grep -n "func.*Reset" internal/protocol/subscription.go` matches) with a passing test.
- **AC-4 (GR#20).** The v1 in-process transport (`InProcTransport` or equivalent) implements a `Transport` interface with `SendCommand`, `Results()`, `Events()`, `Deltas()`, `Close()`. Check: `go doc ./internal/protocol Transport` lists exactly these methods (names may differ slightly but must cover send-command, receive-results, receive-events, receive-deltas, close). This interface IS the GR#20 (Contract-First, Stub-Forever) seam every engine/UI module must consume through — `InProcTransport` and any future gRPC implementation both satisfying `Transport` unchanged is the whole point.
- **AC-5.** The in-process transport's full-buffer policy is explicit and documented in the package (not left implicit). Check: `grep -n "full-buffer\|evict\|drop policy" internal/protocol/transport.go` finds a doc comment naming the policy (e.g. evict-oldest), and a passing test exercises a full buffer and asserts the documented behaviour (`grep -rn "func Test.*[Bb]uffer\|func Test.*[Ee]vict\|func Test.*[Dd]rop" internal/protocol/*_test.go` finds at least one such test, and it passes).
- **AC-6.** `SendCommand` never blocks the caller: check the doc comment states this explicitly (`grep -n "never blocks\|non-blocking\|does not block" internal/protocol/transport.go` matches) and it returns an error (not silently swallowed) when the command cannot be accepted (invalid, buffer exhausted per AC-5's policy, or transport closed).
- **AC-7.** `Command.Validate()` (or equivalent) rejects a `ProtocolVersion` other than the package's current constant. Check: `grep -n "ProtocolVersion" internal/protocol/envelope.go` shows the constant, and a passing test asserts `Validate()` errors on a mismatched version (`grep -rn "ProtocolVersion" internal/protocol/*_test.go` finds a version-mismatch test case).
- **AC-8.** A `NewCorrelationID()`-style helper exists and produces a non-empty, valid identifier using a non-wall-clock, non-map-iteration source (crypto-random or equivalent). Check: `grep -n "func NewCorrelationID" internal/protocol/envelope.go` matches and its body does not call `time.Now()` (`grep -n "time.Now" internal/protocol/envelope.go` returns nothing).

### Error handling

- **AC-9.** Sending on a closed transport returns a distinct, checkable error (e.g. `ErrTransportClosed`) rather than panicking or blocking forever. Check: `grep -n "ErrTransportClosed" internal/protocol/transport.go` matches, and a passing test asserts `SendCommand` after `Close()` returns this error (`go test ./internal/protocol/... -run TestClose -race -count=1 -v` or equivalent passes).
- **AC-10.** `Close()` is idempotent — calling it twice does not panic. Check: `grep -rn "func Test.*Close" internal/protocol/*_test.go` finds a test calling `Close()` more than once, and it passes.
- **AC-11.** No exported constructor or method in this package can return a bare `error` from the standard library without going through the package's own typed errors/sentinel errors where the item's own docs promise a specific error (correlation-ID / version / view-name validation) — spot check: `grep -n "ErrMissingCorrelationID\|ErrInvalidViewName\|ErrTransportClosed" internal/protocol/*.go` shows these sentinels are actually returned from their respective `Validate`/`Send*` functions, not just declared.

### Determinism & safety

- **AC-12 (SG-7 scoped; GR#21).** `grep -rn "time.Now" internal/protocol/*.go` (excluding `_test.go`) returns **no matches at all** — this package must never call the wall clock anywhere (stricter than foundation.errors: the protocol package has no injectable-clock carve-out). A regression here is GR#21 (Red Determinism Gate Stops the Line) territory once this package sits on the tick path.
- **AC-13 (GR#21).** `Tick` is an explicit simulation-time type (not wall-clock-derived) used for staleness detection. Check: `grep -n "type Tick" internal/protocol/envelope.go` matches, and its doc comment states it is never derived from wall time.
- **AC-14 (GR#20).** This package imports nothing from `internal/engine` or `internal/ui`. Check: `go list -deps ./internal/protocol/... | grep -E "internal/engine|internal/ui"` outputs nothing. This is GR#20's "internal/ui → internal/engine imports lint-banned" rule applied to the seam package itself: `int.protocol` must stay import-neutral so both sides consume it only via the interface, never each other directly.
- **AC-15 (GR#21).** No `range` over a Go map is used to produce ordering-sensitive output on the send/receive hot path (A8 lint discipline). Check: manual scan of `transport.go`, `subscription.go`, `deltas.go`, `events.go`, `commands.go` for `for _, v := range someMap` where the loop's output order affects wire bytes or channel send order — none found (Tester records any instance found as a FAIL with file:line).
- **AC-16 (GR#21).** `SeqTracker` and `SubscriptionAllocator` (or equivalents) are safe for concurrent use, matching their doc comments. Check: `go test ./internal/protocol/... -race -count=1` reports no data race, and at least one test drives them from multiple goroutines (`grep -n "go func()" internal/protocol/*_test.go` finds one). A data race is a determinism hazard under GR#21, not a routine bug.

### Documentation

- **AC-17.** `internal/protocol/doc.go` states the module key `int.protocol` and cites §15 / UI-SPEC §6 in its package comment. Check: `grep -n "int.protocol" internal/protocol/doc.go` and `grep -n "§15\|UI-SPEC" internal/protocol/doc.go` both match.
- **AC-18.** The view-name grammar (UI-SPEC §6 "named projections") is documented in the package with at least one worked example. Check: `grep -n "ViewName\|view name" internal/protocol/subscription.go` shows a doc comment with a naming grammar and example strings.

## Out of scope

- The gRPC transport implementation — spec explicitly says "gRPC transport implemented but dormant behind a config flag" as a later step; this item only needs the in-process transport and an interface shape that a gRPC implementation could satisfy later.
- Engine-side (`T-ENGINE`, `T-SUBSCR`) and UI-side (`T-VIEWS`, `T-RENDER`) consumers of this protocol — those are `MOD-008`/`MOD-009`/`MOD-012`/`MOD-013`, separate items that depend on this one.
- Resubscribe-to-heal-a-gap protocol for `SeqTracker`-detected gaps — spec explicitly defers this ("left as an open question for the freeze review").
- Remote/cloud transport security (auth, TLS) — out of v1 scope per §15's cloud path being "designed, unbuilt v1".

## Escalations

- **Resolved.** See `foundation.errors.md`'s Escalations section — GR#20/GR#21 exist (`CLAUDE.md` lines 51-52) and are now cited above (AC-4/AC-14 for GR#20; AC-12/AC-13/AC-15/AC-16 for GR#21).
