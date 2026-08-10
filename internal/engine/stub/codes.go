package stub

// maxAdvanceTicksPerCall bounds a single AdvanceTicks command against
// StubEngine, mirroring engine.core.AdvanceTicks's MaxAdvanceTicksPerCall
// (internal/engine/core/engine.go) deliberately (SEC-006): the same
// limit, the same reject-don't-clamp behaviour, and the same "N confused
// with a tick-rate or monthly count" misuse case guarded against. This
// package intentionally does NOT import internal/engine/core to obtain
// the constant — GR#20's contract-first/stub-forever posture keeps
// engine.stub decoupled from engine.core's (concurrently in-flight)
// internals, and the two packages are meant to converge on the same
// protocol-level behaviour independently, not share Go symbols across
// the module boundary. The value itself (10 in-game years of daily
// ticks, 10*12*30) is copied verbatim rather than re-derived, since
// engine.core's own DailyTicksPerMonth constant lives in that package.
// See ASM- item logged against this file for the "kept in sync by hand"
// risk this duplication carries.
const maxAdvanceTicksPerCall int64 = 10 * 12 * 30

// Registry error codes (AC-9, AC-10), module key "engine.stub". Range:
// P090-P099, declared in data/errors.json's "ranges.reserved" table
// under the "P" (protocol) layer per docs/design/protocol.md's
// "ErrorRef... Code is a data/errors.json registry code" note, since
// StubEngine speaks the protocol seam even though it physically lives
// under internal/engine/stub. All codes below ARE registered with real
// severity/module/message/remedy fields (GR#7; closed under BUG-008 and,
// for MET-P092, SEC-006).
// The internal/foundation/errs source-scan test guards against this
// ever drifting out of sync again.
const (
	// codeUnknownKind is requested when StubEngine's dispatch switch sees
	// a Command.Kind it does not handle. In practice this is defensive:
	// every Kind reaching the engine already passed
	// protocol.DecodeCommand/commandRegistry, so an unhandled Kind here
	// means the protocol package's vocabulary grew (commands.go) faster
	// than this stub's switch — a real gap, not attacker input, but
	// still handled as a rejection rather than a panic (AC-9).
	codeUnknownKind = "MET-P090"

	// codeInvalidPayload is requested when a Command's envelope validates
	// (protocol.Command.Validate) but its payload fails StubEngine's own
	// semantic checks — e.g. AdvanceTicksPayload.N <= 0, an unsupported
	// SetSpeedPayload.Speed, an SubscribePayload.ViewName that fails
	// protocol.ValidateViewName, an UnsubscribePayload for an unknown
	// subscription, or an empty InspectEntityPayload.EntityRef/
	// DebugPayload.Op. protocol.Command.Validate's own doc comment
	// explicitly assigns this job to the receiving engine ("It does NOT
	// validate payload-internal invariants... that is the engine's job").
	codeInvalidPayload = "MET-P091"

	// codeAdvanceTicksOutOfBounds is requested when
	// AdvanceTicksPayload.N is well-formed (positive) but exceeds
	// maxAdvanceTicksPerCall, or when applying it to the current tick
	// counter would overflow protocol.Tick (int64) — SEC-006. Kept as
	// its own code (distinct from the generic codeInvalidPayload) so a
	// client sees the offending value and the limit, not just "payload
	// invalid" (this item's brief: "names the offending value and the
	// limit").
	codeAdvanceTicksOutOfBounds = "MET-P092"

	// codeStubEngineCopied is requested by checkNotCopied (engine.go)
	// when a StubEngine method is called on a struct-copied value — not
	// the *StubEngine NewStubEngine returned (SEC-020 wave 2, mirroring
	// ErrEngineCopied/ErrSubscriptionServerCopied/ErrTransportCopied in
	// engine.core and internal/protocol). See self's doc comment on
	// StubEngine for the full shape of the attack this guards.
	codeStubEngineCopied = "MET-P093"

	// codePrematureCommandsClose is requested by Run (engine.go) when
	// s.transport.Commands() reports its channel closed (ok=false)
	// while ctx has NOT been cancelled — BUG-020: a clean shutdown always
	// cancels ctx before the transport closes, so a channel closure
	// observed with ctx still live means something else closed the
	// transport (or handed Run a struct-copied/already-closed transport)
	// out from under the intentional shutdown sequence. Run must not
	// return nil for this — nil is reserved for ctx.Err() on an
	// intentional quit, and the two are otherwise indistinguishable to
	// any caller inspecting Run's return value alone.
	codePrematureCommandsClose = "MET-P094"
)
