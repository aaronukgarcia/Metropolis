package protocol

// Registry error codes for the protocol package's own source (module key
// "protocol"). Range: P000-P009, declared in data/errors.json's
// "ranges.reserved" table — distinct from P090-P099, which belongs to
// internal/engine/stub (a CONSUMER of this seam, not this package's own
// source; see that package's codes.go doc comment). The code below IS
// registered there with real severity/module/message/remedy fields
// (GR#7); internal/foundation/errs's source-scan test guards against this
// ever drifting out of sync, and against another module's range
// accidentally overlapping this one (BUG-008's root cause).
const (
	// ErrTransportCopied: SendCommand, Close, SendResult, SendEvent,
	// SendDelta, Commands, Results, Events, or Deltas was called on an
	// InProcTransport value that is not the one NewInProcTransport
	// constructed — i.e. a struct copy (SEC-020 wave 1: 't2 := *t' is
	// legal, unsafe-free, reflect-free Go, and defeats closeMu's
	// per-instance safety because the copy gets its OWN closeMu but
	// ALIASES the original's cmdCh/resultCh/eventCh/deltaCh/closed/
	// closeOnce — a copy's Close() can then race the original's in-flight
	// sends and reopen BUG-007's send-on-closed-channel panic). See
	// InProcTransport.self's doc comment (transport.go).
	ErrTransportCopied = "MET-P000"

	// ErrSeqTrackerCopied: Observe or Reset was called on a SeqTracker
	// value that is not the one NewSeqTracker constructed — i.e. a
	// struct copy (SEC-020 wave 1: 't2 := *t' is legal, unsafe-free,
	// reflect-free Go, and defeats mu's per-instance safety because the
	// copy gets its OWN mu but ALIASES the original's last map — fatal
	// concurrent map access, same class as SEC-003/SEC-019). See
	// SeqTracker.self's doc comment (subscription.go).
	ErrSeqTrackerCopied = "MET-P001"

	// ErrSubscriptionAllocatorCopied: Allocate was called on a
	// SubscriptionAllocator value that is not the one
	// NewSubscriptionAllocator constructed — i.e. a struct copy (SEC-023,
	// SEC-020 wave 1's sibling hunt: 'a2 := *a' is legal, unsafe-free,
	// reflect-free Go and produces a second, independently-incrementing
	// counter starting from the same point, so the copy and the original
	// hand out COLLIDING SubscriptionIDs rather than crashing or
	// hanging). See SubscriptionAllocator.self's doc comment
	// (subscription.go).
	ErrSubscriptionAllocatorCopied = "MET-P002"
)

// Registry error codes for the WebSocket JSON-RPC transport
// (FEAT-1972079852 increment 1, mkey FEAT-1972079852). Range: P010-P019,
// claimed via tools/plan/add-error.js claim-range (BUG-273) rather than
// hand-edited into data/errors.json. See wstransport.go for the version
// handshake this package implements: Aaron's 2026-08-31 DD ruled a
// version mismatch at connect is a REFUSAL, never a degrade — these three
// codes are the typed reasons a connection is refused.
const (
	// ErrHandshakeVersionMismatch: the client's first frame (the
	// handshake) declared a build/protocol version that does not match
	// what this server build speaks. The server writes a refusal frame
	// carrying this code and closes the connection immediately — it does
	// NOT attempt to serve a possibly-incompatible session.
	ErrHandshakeVersionMismatch = "MET-P010"

	// ErrHandshakeInvalid: the first frame received on a new connection
	// was not a well-formed handshake message (bad JSON, wrong Kind,
	// missing ClientVersion). Distinct from a version mismatch: this is a
	// malformed client, not simply an older/newer build.
	ErrHandshakeInvalid = "MET-P011"

	// ErrHandshakeTimeout: no handshake frame arrived within the server's
	// configured handshake deadline. A connection that never completes
	// its handshake would otherwise hang a server-side goroutine forever;
	// this bounds that wait and reports it as a typed, registry-sourced
	// refusal rather than a silent timeout.
	ErrHandshakeTimeout = "MET-P012"

	// MET-P013 is reserved for the webconsole (TS) protocol client's own
	// "engine connection lost/unreachable" code -- see
	// webconsole/src/sim/protocolClient.ts's ERR_ENGINE_UNREACHABLE. It is
	// never constructed Go-side (nothing here sends it over the wire), so
	// there is deliberately no Go constant for it; data/errors.json still
	// carries its registration under this package's mkey.

	// ErrCommandDecodeFailed: a post-handshake "command" request's Params
	// could not be decoded as a protocol.Command (BAR-2, round-r1 REJECT:
	// this used to hand-build an rpcError carrying ErrHandshakeInvalid,
	// which conflated "malformed handshake frame" with "malformed command
	// frame" -- two different failure classes needing their own codes and
	// their own correlation IDs, GR#1/GR#7). Distinct from
	// ErrHandshakeInvalid: this can only happen AFTER a successful
	// handshake, on a per-command basis, and never closes the connection.
	ErrCommandDecodeFailed = "MET-P014"

	// ErrCommandValidationFailed: a successfully-decoded Command failed
	// its own Validate() (commands.go). Distinct from ErrCommandDecodeFailed
	// (the frame parsed fine; the command's own rules rejected it).
	ErrCommandValidationFailed = "MET-P015"

	// ErrCommandSendFailed: a valid Command could not be forwarded to the
	// wrapped protocol.Transport (e.g. SendCommand returned an error --
	// closed/full transport). Distinct from the two codes above: decoding
	// and validation both succeeded, the failure is in the transport hop.
	ErrCommandSendFailed = "MET-P016"

	// ErrDeltaSchemaMismatch: the webconsole (TS) protocol client received
	// a Delta whose patch carries a schemaVersion the client does not
	// recognise for that view (AC-7/DD2, Aaron 2026-08-31: refuse to
	// apply, never silently skip). Never constructed Go-side -- see
	// webconsole/src/sim/protocolClient.ts's ERR_SCHEMA_MISMATCH; kept
	// here purely so the registration lives under this package's mkey
	// alongside every other MET-P01x code in this reservation.
	ErrDeltaSchemaMismatch = "MET-P017"

	// ErrSafeConnCopied: wsserver's per-connection safeConn.writeJSON (or
	// its checkNotCopied guard) was called on a struct copy rather than
	// the *safeConn newSafeConn constructed -- see
	// internal/protocol/wsserver/server.go's safeConn.self doc comment
	// for the exact hazard (a copy would alias the original's
	// *websocket.Conn while getting its own, independently-locked mu,
	// defeating the single-writer serialization safeConn exists to
	// provide). Registered under this package's mkey per this file's own
	// convention, even though the type itself lives in the wsserver
	// subpackage.
	ErrSafeConnCopied = "MET-P018"
)

// Registry error codes for FEAT-1972079936 Phase 0 increment 3 (capability
// gating + the dedicated below-floor refusal). Range: P020-P029, claimed
// via tools/plan/add-error.js claim-range for mkey "protocol" (the P010-
// P019 block above was already fully occupied — 9 codes plus MET-P013's
// TS-only reservation — so this is a fresh 10-wide block, not a reuse).
const (
	// ErrHandshakeBelowWindowFloor: the client's declared wire-version
	// ceiling (handshakeParams.ClientMaxVersion) is strictly older than the
	// server's supported window floor (protocol.WindowFloorMajor). This is
	// a DEDICATED code, deliberately distinct from ErrHandshakeVersionMismatch
	// (MET-P010, whose registered meaning is the separate build-string
	// equality check) -- inc2 reused MET-P010 for this refusal as a
	// documented TODO; inc3 retires that reuse (server.go's handshake) and
	// carries clientMaxVersion/serverVersion/windowFloorMajor/
	// versionWindowDepth context so the message is actionable ("upgrade
	// required, minimum supported version is X"), per AC-4.
	ErrHandshakeBelowWindowFloor = "MET-P020"

	// ErrCapabilityRequired: a post-handshake command was refused because
	// it requires a fine-grained capability (AC-5, Aaron ruling 3: one flag
	// per individual feature, not a coarse per-area flag) that was not in
	// this connection's NEGOTIATED capability set (the intersection of
	// client-declared and server-declared capabilities, protocol.
	// IntersectCapabilities). Distinct from ErrCommandValidationFailed
	// (MET-P015): the envelope/payload are perfectly well-formed here, the
	// connection simply never negotiated support for this specific
	// feature.
	ErrCapabilityRequired = "MET-P021"
)
