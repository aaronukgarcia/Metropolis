BOW code: MOD-013

# Acceptance criteria — harness.replay (MOD-013)

**BOW code:** MOD-013
**Spec refs:** M0-ENG §2.2 (`docs/METROPOLIS-MASTER-v2.1.md` line 847: "H-REPLAY — recorded fixtures... Delta streams and command logs recorded from any engine (stub or real) into `fixtures/*.ndjson.gz`; replayable into the UI... and into the engine... The save format IS the fixture format — one serialisation to rule them all."); M0-ENG §6 point 4 (working agreement, walking skeleton / permanent-fixture doctrine, line 844: "all permanent fixtures — they never get deleted; they become the test estate"); §15/A9 determinism rule (line 179: same seed + same commands ⇒ bit-identical world); code.json `harness.replay` entry; consumed contracts `docs/design/protocol.md` (Command/Event/Delta envelope, CorrelationID, Tick) and `docs/design/save-format.md` (StateSerializer, NDJSON+gzip shard encoding, byte-determinism guarantee).
**Date:** 2026-08-08
**Status:** draft-ahead
**Package under test:** `internal/harness/replay/` + `fixtures/` (path from `node claude-bow.js show MOD-013`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/harness/replay/...`.

## User stories

- **US-1.** As the H-REPLAY harness, I need to record every `Command` sent to an engine (stub or real) and every `Event`/`Delta` it emits into a durable fixture file, so that a UI developer can replay stable, reproducible data without a live engine (M0-ENG §2.2).
- **US-2.** As the H-REPLAY harness, I need to replay a recorded command log back into a real engine and compare the resulting state/delta stream against the original recording, so that regressions are caught automatically (M0-ENG §2.2's "regression: replay commands, compare snapshots").
- **US-3.** As `int.serializer`'s only fixture-format consumer named this early, I need to reuse the exact same bundle/shard encoding as saves, so that "the save format IS the fixture format" is literally true rather than a parallel format that drifts (M0-ENG §2.2; `docs/design/save-format.md`).
- **US-4.** As a future Tester/CI job, I need fixtures to be self-describing (protocol version, engine identity, seed) so that a stale or incompatible fixture fails loudly at load time rather than silently replaying garbage (GR#1/#7; `docs/design/protocol.md` §2 envelope rules).

## Scope

Recording engine command/event/delta streams to `fixtures/*.ndjson.gz` via `int.serializer`, and replaying those fixtures into either the UI (static data feed) or an engine (regression comparison).

## Acceptance criteria

### Functional

- **AC-1.** A `Recorder` (or equivalent) exists that, given a `protocol.Transport` (or a tap on one), captures every `Command` sent and every `CommandResult`/`Event`/`Delta` received, in strict arrival order. Check: `go doc ./internal/harness/replay Recorder` shows a type whose methods accept/observe `protocol.Command`, `protocol.Event`, `protocol.Delta`.
- **AC-2.** Recorded streams are written as fixture bundles under `fixtures/*.ndjson.gz` using `int.serializer`'s `StateSerializer`/`NDJSONSerializer` (not a bespoke encoder) — one `Record` per captured message, `Kind` set to the message's protocol `Kind`/type name. Check: `grep -n "serialize\.\(NDJSONSerializer\|StateSerializer\|WriteShard\)" internal/harness/replay/*.go` matches; `grep -rn "json.Marshal\|json.NewEncoder" internal/harness/replay/record.go` (if any) is limited to constructing `Record.Data`, not a second on-disk format.
- **AC-3.** A `Player` (or equivalent) exists that reads a fixture bundle and replays it either (a) into a UI's `Transport` consumer as a canned `Deltas()`/`Events()` source, or (b) as a `Command` sequence sent to a live engine for regression comparison. Check: `go doc ./internal/harness/replay Player` lists both replay modes (or two distinct exported types, e.g. `UIPlayer`/`EnginePlayer`).
- **AC-4.** Fixture headers carry, at minimum, the protocol version the commands were recorded against (`protocol.ProtocolVersion`), the world seed, and an engine identity/build string, reusing `int.serializer`'s `Header` fields where they already exist (`FormatVersion`, `WorldSeed`, `AppVersion`) rather than inventing parallel fields. Check: `grep -n "WorldSeed\|FormatVersion\|AppVersion" internal/harness/replay/*.go` shows the fixture writer populating these via `serialize.Header`, not local duplicates.
- **AC-5.** Regression replay (mode b of AC-3) compares the live engine's re-emitted `Event`/`Delta` stream (or resulting state snapshot) against the recorded one and reports a pass/fail diff, not just "replayed without crashing". Check: `go doc ./internal/harness/replay CompareResult` (or equivalent) exists, and a passing test asserts a deliberately mismatched fixture produces a non-empty diff (`grep -rn "func Test.*[Mm]ismatch\|func Test.*[Dd]iverge" internal/harness/replay/*_test.go` finds coverage).
- **AC-6.** At least one recorded fixture exists in the repo under `fixtures/` (e.g. a short H-STUB session against "Folkestone-64") usable by `ui.harness` (MOD-014) as its inbound contract. Check: `Get-ChildItem fixtures/*.ndjson.gz` (or `ls fixtures/*.ndjson.gz`) is non-empty, and `go test ./internal/harness/replay/... -race -count=1` includes a test that loads and replays it successfully.
- **AC-7.** `CorrelationID` is preserved verbatim through record and replay for every message that carried one (`docs/design/protocol.md` §2: correlation propagates to every CommandResult/Event/Delta). Check: a passing test asserts a recorded `Command`'s `CorrelationID` matches the replayed copy's (`grep -rn "func Test.*Correlation" internal/harness/replay/*_test.go` finds it).

### Error handling

- **AC-8 (GR#7).** Loading a fixture whose `FormatVersion` major differs from the reader's supported major, or whose recorded `protocol.ProtocolVersion` differs from the running build's, fails loudly with a registry-sourced error (a new `MET-F3xx`-or-appropriate-range code added to `data/errors.json`, per `errs.New`), never a silent best-effort replay. Check: `grep -n "MET-" internal/harness/replay/*.go` finds at least one registry code reference, and a passing test exercises the version-mismatch path (`grep -rn "func Test.*[Vv]ersion" internal/harness/replay/*_test.go`).
- **AC-9.** A corrupt or truncated fixture (bad gzip stream, hash mismatch via `int.serializer`'s `ValidateBundle`) produces a typed error naming the fixture path and the underlying cause, never a panic. Check: `go test ./internal/harness/replay/... -race -count=1` passes including a corrupt-fixture test (`grep -rn "func Test.*[Cc]orrupt" internal/harness/replay/*_test.go`).
- **AC-10.** Recording continues to a partial, still-loadable fixture if the engine/UI process is interrupted mid-session (no requirement to buffer the whole session in memory before any bytes are durable) — or, if the design instead buffers and flushes atomically at close, that choice is documented and a killed-mid-record scenario is explicitly out of scope (see Escalations). Check: `internal/harness/replay/doc.go` states which of the two behaviours was chosen and why.

### Determinism & safety

- **AC-11 (GR#21).** Replaying the same fixture twice into the same engine build/seed produces byte-identical comparison results (`AC-5`'s diff is empty both times, or if mismatched, identically mismatched). Check: a passing test replays the same fixture twice and asserts identical `CompareResult` output (`grep -rn "func Test.*[Dd]eterminis" internal/harness/replay/*_test.go`).
- **AC-12 (SG-7 scoped; GR#21).** `grep -rn "time.Now\|time.Since" internal/harness/replay/*.go` (excluding `_test.go`) returns no matches, or every match is confined to a clearly-labelled recording-session-wallclock field that is diagnostic-only and never fed into `Command.IssuedAtTick` or any replayed value (per `docs/design/protocol.md` §2: "Never wall clock" for `IssuedAtTick`).
- **AC-13.** `go test ./internal/harness/replay/... -race -count=1` passes with no data race when recording concurrently with normal engine/UI operation (recording must not require pausing either loop). Check: `grep -n "go func()" internal/harness/replay/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-14.** `internal/harness/replay/doc.go` states the module key `harness.replay`, cites M0-ENG §2.2, and states explicitly that the fixture format is `int.serializer`'s bundle format (no second format). Check: `grep -n "harness.replay" internal/harness/replay/doc.go` and `grep -n "M0-ENG" internal/harness/replay/doc.go` and `grep -n "int.serializer\|StateSerializer" internal/harness/replay/doc.go` all match.
- **AC-15.** `fixtures/README.md` (or `doc.go`) documents fixture naming convention and how to regenerate the checked-in sample fixture from H-STUB. Check: file exists and is non-empty.

## Out of scope

- H-STUB itself (`MOD-008`) — this item only records/replays whatever transport it is pointed at; it does not build the stub engine.
- `ui.harness`'s (MOD-014) snapshot-assert machinery and latency-budget CI — that item consumes this one's fixtures but owns its own comparison logic.
- The binary serializer format (`BinarySerializer`, reserved in `int.serializer`, not implemented) — fixtures stay NDJSON+gzip until that lands.
- A resync/replay-request protocol extension to `int.protocol` — v1 has none (`docs/design/protocol.md` §7.2); this item works within the existing envelope.
- Cross-machine fixture portability testing beyond what `int.serializer`'s determinism guarantee already covers.

## Escalations

- **For Bill.** AC-10's interrupted-recording behaviour (durable-as-you-go vs. buffer-and-flush-atomically) is not specified anywhere in M0-ENG §2.2 or the serializer design doc. The BA has left both options open with a documentation requirement rather than picking one — this is a real design decision for whoever builds the item (or Bill, at brief time) to make and record, not something the BA should silently resolve.
- **Assumption flagged (per BA instructions §3).** This item depends on `INT-001`/`INT-002` (status `in_progress` at BA time, sprint 0) being frozen v1 by Sprint 2's start, and on `MOD-008` H-STUB (Sprint 1) existing to produce the sample fixture in AC-6. Both are Sprint 0/1 exit-gate deliverables per the sprint plan; if either contract shifts before dispatch, the owning BA must refresh this file's ACs that cite `protocol.Command`/`Header` fields by name.
