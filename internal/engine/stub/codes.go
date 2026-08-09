package stub

// Registry error codes (AC-9, AC-10), module key "engine.stub". Range:
// P090-P099, declared in data/errors.json's "ranges.reserved" table
// under the "P" (protocol) layer per docs/design/protocol.md's
// "ErrorRef... Code is a data/errors.json registry code" note, since
// StubEngine speaks the protocol seam even though it physically lives
// under internal/engine/stub. Both codes below ARE registered with real
// severity/module/message/remedy fields (GR#7; closed under BUG-008).
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
)
