package transport

// Registry error codes for int.transport (MOD-086 / INT-005, the
// WebSocket bridge between a browser front end and the composed engine).
// Range: P100-P199, claimed via tools/plan/add-error.js claim-range on
// 2026-08-23 — this package speaks the protocol seam (like engine.stub's
// P090-P099 precedent) rather than owning engine state, so it lives in
// the P layer. Every code below is registered in data/errors.json with
// real severity/message/remedy fields (GR#7); every error this package
// raises goes through errs.New/errs.Wrap with one of these codes — never
// a bare fmt.Errorf.
const (
	// ErrInvalidCommandEnvelope: a WebSocket frame was not decodable as a
	// protocol.Command envelope, or failed Command.Validate. The frame is
	// dropped and an {"type":"error"} wire frame naming MET-P100 is sent
	// back; nothing reaches the engine command path.
	ErrInvalidCommandEnvelope = "MET-P100"

	// ErrRouteMiss: an outbound message (a CommandResult or Delta) could
	// not be routed to any connected session — the client that issued the
	// command or opened the subscription disconnected before the response
	// arrived. The message is dropped; the engine side is unaffected.
	ErrRouteMiss = "MET-P101"

	// ErrSlowConsumer: a session's outbound buffer overflowed because its
	// WebSocket reader fell behind the delta stream. The connection is
	// closed rather than blocking the shared drain loop (the same
	// never-block-the-pump reasoning as protocol.InProcTransport's
	// evict-oldest policy, taken to the session boundary).
	ErrSlowConsumer = "MET-P102"

	// ErrWSAcceptFailed: the HTTP request could not be upgraded to a
	// WebSocket connection (not a GET, missing upgrade headers, etc.).
	ErrWSAcceptFailed = "MET-P103"

	// ErrServerCopied: a Server method was reached on a struct-copied
	// Server value (SEC-020 class). A copy aliases every routing map while
	// carrying its own independent mu, so its locking protects nothing
	// shared; guarded exactly like InProcTransport/Engine.
	ErrServerCopied = "MET-P104"
)
