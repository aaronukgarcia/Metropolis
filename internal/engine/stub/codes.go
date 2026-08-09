package stub

// Placeholder registry error codes (AC-9, AC-10).
//
// These are NOT yet entries in data/errors.json. Per GR#7 / the errs
// package contract (internal/foundation/errs/errs.go), errs.New/errs.Wrap
// never panic or bypass the registry for an unregistered code — they
// degrade to the well-formed MET-F003 "unregistered error code" fallback,
// carrying the originally-requested code (below) in the resulting *errs.E's
// context and, via renderTemplate, in its Display() string. That means
// today a StubEngine rejection's ErrorRef.Code is actually "MET-F003" on
// the wire, not one of the codes below — the codes below are what get
// requested, and are the identifiers to register once data/errors.json
// grows a harness.stub or protocol range for them.
//
// Registering these for real is future work (out of scope for this item;
// see the dispatch report for MOD-008) — reserve them under the "P"
// (protocol) layer per docs/design/protocol.md's "ErrorRef... Code is a
// data/errors.json registry code (MET-P### for this package's own
// errors)" note, since StubEngine speaks the protocol seam even though it
// physically lives under internal/engine/stub.
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
