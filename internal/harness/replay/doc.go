// Package replay is H-REPLAY (MOD-013): record a Command/CommandResult/
// Event/Delta stream from any engine (stub or real) into a durable
// fixture file, and replay that fixture either into a UI's Transport
// consumer as canned data (mode a, [UIPlayer]) or into a live engine as a
// regression comparison (mode b, [EnginePlayer]). M0-ENG §2.2: "the save
// format IS the fixture format — one serialisation to rule them all."
//
// Module key: harness.replay (see code.json; GUID 8ce3b7c8-0f7a-4a5a-9ef7-581ee164ebe8)
// Spec ref:   M0-ENG §2.2; docs/design/protocol.md (Command/Event/Delta
//
//	envelope, CorrelationID, Tick); docs/design/save-format.md
//	(StateSerializer, NDJSON+gzip shard encoding, byte-determinism)
//
// # The fixture format IS int.serializer's format — no second format
//
// Every fixture's payload bytes are written and read exclusively through
// int.serializer's [serialize.NDJSONSerializer] ([serialize.StateSerializer]'s
// canonical implementation): [serialize.NDJSONSerializer.WriteShard] /
// [serialize.NDJSONSerializer.ReadShard]. A fixture's header is
// int.serializer's own [serialize.Header] (FormatVersion, WorldSeed,
// AppVersion, ShardIndex) — this package invents no parallel WorldSeed,
// AppVersion, or FormatVersion field (AC-4). This package does NOT,
// however, use int.serializer's [serialize.Bundle] DIRECTORY layout
// (header.json + a shards/ subdirectory) — see "On-disk layout" below for
// why, and why that is still "the same format", not a second one: the
// difference is purely which directory a shard file's bytes and its
// header sit in, never how those bytes are encoded.
//
// # On-disk layout (a deliberate departure from Bundle's nested layout — ASM)
//
// Each fixture <name> is exactly two flat files directly under the
// fixtures/ directory: fixtures/<name>.ndjson.gz (the one shard, written
// via NDJSONSerializer.WriteShard) and fixtures/<name>.header.json (this
// package's own small wrapper around serialize.Header — see fixture.go's
// fixtureHeader). This is DELIBERATELY not serialize.Bundle's
// <dir>/header.json + <dir>/shards/<name>.ndjson.gz nesting: AC-6's own
// check (`Get-ChildItem fixtures/*.ndjson.gz` / `ls fixtures/*.ndjson.gz`,
// both non-recursive globs) requires the shard file to sit directly in
// fixtures/, which Bundle's shards/ subdirectory would never satisfy.
// Logged as an assumption (see this item's dispatch report) rather than
// silently chosen: a reasonable person could have read AC-2/AC-6 as
// requiring the full Bundle layout, in which case AC-6's check would need
// a recursive glob instead. This package still reuses every byte-level
// encoding decision Bundle relies on (NDJSONSerializer, Header, the
// gzip/hash approach) — only the directory arrangement differs.
//
// # Fixture names are untrusted input (AC-2b/AC-16 — weakness pattern #4)
//
// A fixture's own name is untrusted the moment it could have been
// supplied by anything other than this build's own record path — a CLI
// flag, a bug report, a shared directory, a name embedded in another
// already-loaded fixture. Every place this package turns a fixture name
// into a path under fixtures/ calls [serialize.ValidateShardName]
// directly (the SAME function ShardMeta.Name is validated through,
// SEC-001/SEC-013's house model) — never a bespoke reimplementation of
// that rule (the "Out of scope" section of this item's acceptance
// criteria calls out re-implementing it as its own weakness-pattern-#2
// risk). A hostile name is rejected outright — never filepath.Clean'd,
// trimmed, or silently substituted — exactly like ShardMeta.Name.
//
// # Premature-close ambiguity (AC-3c, mirroring BUG-020/codePrematureCommandsClose)
//
// [EnginePlayer] implements the exact CommandSource-shaped surface
// engine.core.RunCommandLoop consumes from any Transport (Commands() +
// SendResult()) — a caller drives replay with
// `go targetEngine.RunCommandLoop(ctx, enginePlayer)`. As of MOD-015
// (harness.headless / engine.headless.md AC-4), RunCommandLoop returns an
// error distinguishing a clean ctx-cancelled stop from its
// CommandSource's Commands() channel closing prematurely — irrelevant to
// EnginePlayer itself (it never closes cmdCh early; see below), but
// worth noting here since this file's own "premature-close ambiguity"
// section predates and is the direct model for that engine.core fix.
// Unlike
// StubEngine.Run's scenario (where SOMETHING ELSE can close the
// Commands() channel out from under it), EnginePlayer owns and is the
// only closer of its own Commands() channel, so the literal "who closed
// it" ambiguity BUG-020 fixes cannot recur here verbatim. The
// structurally identical risk this package faces instead is "did every
// dispatched command get answered before replay stopped waiting" —
// EnginePlayer.Replay tracks exactly that (see player_engine.go) and
// returns the distinct, named ErrReplayTargetClosedEarly (MET-H004)
// whenever ctx is done before a CommandResult was observed for every
// command sent, never conflating that with a clean, fully-answered
// replay. Logged as an assumption: the acceptance criteria's own
// escalation notes flag the exact channel semantics as underspecified
// (BA text: "the exact error code/range is this BA's invention... Bill
// may want the naming/range to visibly match codePrematureCommandsClose's
// pattern") — this is this package's resolution of that gap, not a
// silent reinterpretation.
//
// # Copy-safety (AC-13b)
//
// [Recorder] and [EnginePlayer] each hold a sync.Mutex alongside a
// reference-type field (a slice) and follow the same self-identity
// pattern as every other SEC-020 guarded type in this codebase
// (internal/protocol/transport.go's InProcTransport.self is the
// canonical reference): an atomic.Pointer[T] identity check runs BEFORE
// the mutex is ever touched, rejecting a struct-copied receiver with a
// registry-sourced error rather than risking a permanent silent deadlock
// (SEC-016) or a torn read. [UIPlayer] is the one exported type in this
// package that does NOT get this guard: it holds no mutex at all (its
// channels are fully populated once, at construction, and never mutated
// afterward — reading a copy's aliased channels is exactly as safe as
// reading the original's, the same reasoning StubEngine.World() documents
// for its own unguarded pointer accessor), so there is no lock a copy
// could poison. Stated explicitly here per AC-13b's requirement that a
// package with no mutex+reference-field type say so, rather than leave a
// future Destructive sweep to re-derive the answer from scratch.
//
// # Recording durability (AC-10 — buffer-and-flush-atomically, chosen)
//
// Recorder buffers every captured record in memory (record.go) and
// nothing is written to disk until [Save] is called explicitly, once, at
// the end of a recording session — the "buffer and flush atomically at
// close" option AC-10 leaves open, chosen over "durable as you go"
// because a fixture is a small, short, deliberately-triggered recording
// session (a few Subscribe/AdvanceTicks/Pause/Resume calls, see
// gen/main.go), not a long-running save, so the memory cost of buffering
// the whole session is negligible and the simpler code wins. A killed-
// mid-record process loses the whole in-progress fixture (nothing
// partial is ever written) — this is explicitly OUT OF SCOPE, not a
// silently-accepted gap: recording a permanent test-estate fixture is an
// interactive, supervised action (a developer running gen/main.go or an
// equivalent script), not an unattended background process, so losing an
// interrupted attempt and re-running it is an acceptable cost.
//
// # Determinism (GR#21)
//
// This package never calls time.Now/time.Since in its recording or
// replay logic (grep -rn "time\.Now|time\.Since" internal/harness/replay/*.go,
// excluding _test.go, returns no matches) — record/replay ordering is
// driven entirely by arrival order and fixture order, never wall time.
// [CompareResult] is a pure function of the two CommandResult sequences
// it compares, so replaying the same fixture twice against the same
// engine build/seed produces byte-for-byte identical CompareResult output
// (AC-11).
package replay
