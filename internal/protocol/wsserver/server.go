// Package wsserver is the FEAT-1972079852 increment-1 network transport:
// it serves an existing protocol.Transport (int.protocol; typically the
// UI-facing side of an InProcTransport wired to a running engine within
// the same process, see cmd/metroserve) over a WebSocket, JSON-RPC 2.0
// framed, so an out-of-process client (the webconsole) can reach it.
//
// This package is a thin BRIDGE, not a second implementation of Transport
// (GR#20/GR#3): it never constructs Commands/Deltas itself, it only
// marshals/unmarshals the exact wire types internal/protocol already
// defines (codec.go's Encode/Decode family) across the socket boundary.
//
// # Version handshake (Aaron DD, 2026-08-31)
//
// The FIRST frame a client sends on a new connection MUST be a handshake
// request naming its build/protocol version. If that version does not
// match this server's own (engineVersion, supplied by the caller —
// typically git describe via ldflags, GR#2), the server responds with a
// typed refusal (MET-P010, ErrHandshakeVersionMismatch) and closes the
// connection. This is a REFUSAL, never a silent degrade — a version
// mismatch means the wire schemas may not agree, and serving a
// possibly-incompatible session is exactly the "confident wrong data"
// failure GR#1 exists to prevent. A malformed first frame (MET-P011) or
// one that never arrives within HandshakeTimeout (MET-P012) is refused
// the same way.
//
// # Graceful negotiation is arriving (FEAT-1972079936 Phase 0)
//
// The paragraph above documents this package's engineVersion-STRING
// behaviour, which remains untouched through increment 2 — equality on
// that build string is still a separate, independent accept/refuse gate.
// Aaron's superseding DD (2026-08-31, the compute-offload epic, docs/
// planning/acceptance/feat-1972079936-phase0-protocol-versioning.md) is
// that a refuse-on-any-mismatch rule is too strict for the Azure-hosted
// target topology, where a long-lived server outlives many client-tab
// refresh cycles: an in-window older client should connect and work, not
// be kicked off by every deploy. Phase 0 replaces the WIRE-VERSION half
// of this (the part that COULD be graceful — the build string, being a
// same-commit identity check, is a different concern this phase
// deliberately leaves alone) with a semver'd WireVersion
// (protocol.WireVersion, wireversion.go), a real connect-time negotiation
// (handshakeParams/handshakeResult, below), and a configurable version
// window with compat shims (shim.go).
//
// Increment 1 added ONLY the new fields and a single-version echo.
// Increment 2 (this change) adds the real window: negotiateVersion now
// picks the highest version both the client's declared range and the
// server's window support, an in-window OLDER-major connection is routed
// through shim.go's per-offset versionShim so its Command/CommandResult
// round-trips correctly, and a below-window-floor client is refused at
// the handshake (reusing ErrHandshakeVersionMismatch for now — see
// handshake's own doc comment for the inc3 TODO that gives this its own
// dedicated code). Increment 3 adds capability-gated negotiation in
// earnest and is also where this doc comment's "REFUSAL, never a silent
// degrade" framing above gets corrected to describe the window/
// negotiation behaviour in full, once the engineVersion-string gate
// itself is revisited.
//
// # v1 scope (increment 1)
//
// One WebSocket connection is treated as one UI client of the wrapped
// Transport. The wrapped Transport's own doc comment already notes it
// does not enforce single-reader/single-writer — that discipline is the
// caller's (cmd/metroserve wires exactly one *InProcTransport per running
// engine and this package serves exactly one live connection's pump loop
// against it at a time in v1; a second concurrent connection would race
// the first's read of Results/Events/Deltas, which is out of scope for
// increment 1 and tracked as a follow-up, not silently pretended safe).
package wsserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// DefaultHandshakeTimeout bounds how long a new connection is given to
// send its handshake frame before the server gives up and closes it
// (MET-P012). Chosen generously for a localhost dev loopback connection;
// revisit once this transport is used over a real network (same
// "skeleton-era traffic" caveat protocol.transport.go's buffer constants
// carry).
const DefaultHandshakeTimeout = 5 * time.Second

// rpcVersion is the fixed "jsonrpc" field value this package emits and
// requires, per JSON-RPC 2.0 (int.protocol's INT-005 transport shape,
// Architect DD 2026-08-31).
const rpcVersion = "2.0"

// Method names this package's JSON-RPC framing uses. Requests (client ->
// server) carry an "id"; notifications (server -> client, one-way) do
// not.
const (
	methodHandshake = "handshake" // client request, id required
	methodCommand   = "command"   // client request, id required
	methodResult    = "result"    // server notification (CommandResult)
	methodEvent     = "event"     // server notification (protocol.Event)
	methodDelta     = "delta"     // server notification (protocol.Delta)
)

// rpcMessage is the wire envelope for every frame this package sends or
// receives, deliberately a superset covering requests, notifications,
// results, and errors (JSON-RPC 2.0's shape allows this: absent fields
// are simply omitted on the wire via omitempty).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a registry-sourced error surfaced over the wire (GR#7):
// Code is always a MET-<layer><NNN> code, Message is the already-resolved
// display string, Data carries any additional structured context (never a
// second, ad hoc error shape).
type rpcError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// handshakeParams is methodHandshake's request payload.
type handshakeParams struct {
	// ClientVersion is the connecting build's own version string
	// (buildinfo.Version / git-describe, GR#2). Kept for diagnostics and
	// as this increment's ACTUAL accept/refuse gate (see this file's
	// package doc, "Graceful negotiation is arriving") — Aaron ruling 5
	// (FEAT-1972079936 Phase 0) keeps the engine build string
	// client-visible even once the wire version below is what governs
	// negotiation.
	ClientVersion string `json:"clientVersion"`

	// ClientMinVersion/ClientMaxVersion (FEAT-1972079936 Phase 0 inc1,
	// AC-1/AC-2): the wire protocol version RANGE this client speaks,
	// decoupled from ClientVersion above. Increment 1 always sets both
	// to the same single concrete value it supports (window/range
	// serving is increment 2 — see version.go's WireVersion doc comment,
	// "Window design note"); shaping the field as a min/max pair NOW,
	// rather than a single value, means increment 2 only has to widen
	// what ClientMinVersion can legitimately be, never restructure this
	// message. Optional (a *WireVersion, not a plain value) so an older
	// client that predates this field entirely is distinguishable from
	// one explicitly declaring version 0.0 — see WireVersion.IsZero's
	// doc comment for why that distinction matters at this boundary.
	ClientMinVersion *protocol.WireVersion `json:"clientMinVersion,omitempty"`
	ClientMaxVersion *protocol.WireVersion `json:"clientMaxVersion,omitempty"`

	// Capabilities this client understands (AC-5's mechanism). Increment
	// 1 introduces the field and the intersection helper
	// (protocol.IntersectCapabilities) it is run through; no real
	// capability tokens exist yet for either side to declare (Phase 0
	// has no new feature gated behind one), so this is exercised today
	// only via the empty-set case (AC-2's mutation test).
	Capabilities []string `json:"capabilities,omitempty"`
}

// handshakeResult is methodHandshake's successful response payload.
type handshakeResult struct {
	Accepted bool `json:"accepted"`
	// ServerVersion is this build's own version string — informational
	// only (Aaron ruling 5: the engine build string stays client-visible
	// for diagnostics/support even though it no longer gates accept vs.
	// refuse once increment 3 lands). Unchanged meaning from before this
	// phase.
	ServerVersion string `json:"serverVersion"`

	// NegotiatedVersion (AC-1/AC-2): the wire protocol version this
	// connection will actually speak — the highest version both the
	// client's declared range and this server's currently-served window
	// support. Increment 1's window has depth 0/1 (this server only ever
	// serves protocol.CurrentWireVersion), so "negotiation" today means:
	// echo the client's own ceiling when it is at or below current (AC-2's
	// mutation proves this is NOT simply always the server's own latest),
	// clipped down to current when the client asks for something newer
	// than this build knows. A nil ClientMaxVersion in the request (an
	// old/increment-0 client) always gets protocol.CurrentWireVersion
	// here, preserving today's single-version behaviour exactly.
	NegotiatedVersion protocol.WireVersion `json:"negotiatedVersion"`

	// Capabilities is the negotiated (intersected, AC-5) capability set —
	// see handshakeParams.Capabilities's doc comment for why this is
	// exercised today only via the empty-set case.
	Capabilities []string `json:"capabilities,omitempty"`
}

// Server bridges HTTP WebSocket connections to a wrapped protocol.Transport.
type Server struct {
	transport        protocol.Transport
	engineVersion    string
	handshakeTimeout time.Duration
	upgrader         websocket.Upgrader
	newCorrelationID func() string
	// capabilities is this server's own declared capability set (AC-5).
	// nil in increment 1 — Phase 0 introduces no new feature gated behind
	// one yet, so there is nothing real to declare; the negotiation
	// mechanism (protocol.IntersectCapabilities, handshakeResult.
	// Capabilities) is exercised today purely via the empty-intersection
	// case. A future increment can populate this via a constructor
	// option once a real capability token exists.
	capabilities []string
	// versionWindowDepth (FEAT-1972079936 Phase 0 inc2, AC-3): how many
	// MAJOR versions back from protocol.CurrentWireVersion this server
	// simultaneously serves via shim.go's shimRegistry. Defaults to
	// protocol.DefaultVersionWindowDepth; override via WithVersionWindowDepth.
	versionWindowDepth int
}

// Option configures a Server at construction time (New's variadic
// parameter). Added in increment 2 so New's existing 3-argument call
// shape (cmd/metroserve/main.go's only real caller) stays source-compatible
// -- opts is purely additive.
type Option func(*Server)

// WithVersionWindowDepth overrides the default version window depth
// (FEAT-1972079936 Phase 0 inc2, AC-3) -- how many majors back from
// protocol.CurrentWireVersion this server serves via a compat shim. Tests
// use this to exercise a deliberately narrow window (e.g. N=1) without
// mutating the package-level protocol.CurrentVersionWindowDepth global.
func WithVersionWindowDepth(n int) Option {
	return func(s *Server) { s.versionWindowDepth = n }
}

// New constructs a Server wrapping transport. engineVersion is this
// build's own version string (GR#2: callers pass the git-describe-derived
// value, never a hand-maintained literal) — it is what every connecting
// client's handshake is compared against. A zero handshakeTimeout uses
// DefaultHandshakeTimeout. opts is additive (FEAT-1972079936 Phase 0 inc2)
// -- every pre-existing 3-argument call site is unaffected.
func New(transport protocol.Transport, engineVersion string, handshakeTimeout time.Duration, opts ...Option) *Server {
	if handshakeTimeout <= 0 {
		handshakeTimeout = DefaultHandshakeTimeout
	}
	s := &Server{
		transport:          transport,
		engineVersion:      engineVersion,
		handshakeTimeout:   handshakeTimeout,
		versionWindowDepth: protocol.DefaultVersionWindowDepth,
		upgrader: websocket.Upgrader{
			// Dev-loopback tool, not a public-facing service (localhost
			// port per the acceptance doc's DD1 note) -- origin checking
			// is deliberately permissive in v1; tightening this is a
			// follow-up once a real deployment shape exists (DD1 remains
			// open per the acceptance doc).
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		newCorrelationID: func() string { return string(errs.NewCorrelationID()) },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ErrNoHandshake is returned internally (never sent over the wire as a
// Go error — see envelope.go's ErrorRef doc comment for why the engine/UI
// seam only ever carries data, not Go error values) when the socket
// closes before a handshake frame arrives.
var ErrNoHandshake = errors.New("wsserver: connection closed before handshake")

// ServeHTTP implements http.Handler: upgrades the request to a
// WebSocket, performs the version handshake, then pumps
// commands/results/events/deltas until the connection closes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade itself already wrote an HTTP error response; nothing
		// further to do (GR#1: the failure is visible to the caller via
		// the HTTP status, and gorilla/websocket logs the cause).
		return
	}
	defer func() { _ = conn.Close() }()

	negotiated, ok := s.handshake(conn)
	if !ok {
		return
	}
	s.pump(conn, negotiated)
}

// normalizeVersion strips the volatile "-dirty" suffix `git describe`
// appends when a build's working tree had uncommitted changes (GR#2:
// app version = git describe via ldflags). BAR-4 (round-r1 follow-up):
// the handshake's job is to refuse a REAL commit difference, not to
// false-refuse two builds of the SAME commit where one lane built
// clean and another built a few seconds later with, say, an untracked
// scratch file still sitting in the tree -- the "-dirty" suffix is
// exactly that volatility and nothing else (git describe's own format
// is "<tag>-<count>-g<sha>[-dirty]"; the tag/count/sha core is what
// actually identifies the commit). Stripping only this one well-known
// suffix is deliberately NOT over-loosening: two genuinely different
// commits still differ in the count/sha core and are still refused
// (see the mismatch test case using different -g<sha> values). Mirrored
// TS-side by wire.ts's normalizeProtocolVersion for the same reason
// (Aaron DD, 2026-08-31: refuse on mismatch stands; this only narrows
// what counts as a "real" mismatch).
func normalizeVersion(v string) string {
	return strings.TrimSuffix(v, "-dirty")
}

// negotiateVersion computes the wire version this connection will speak
// (FEAT-1972079936 Phase 0 inc1/inc2, AC-1/AC-2/AC-3). clientMax is the
// client's declared ceiling (handshakeParams.ClientMaxVersion); nil means
// an old client that predates this field entirely. windowDepth is the
// server's configured version-window depth (Server.versionWindowDepth).
//
// Increment 1 only ever actually served protocol.CurrentWireVersion
// (single-version echo). Increment 2 (AC-3) widens this to real window
// negotiation across MAJORS: an in-window older-major client (its
// declared ceiling's major is within windowDepth majors behind current,
// inclusive of the floor) negotiates onto ITS OWN major — not silently
// current's — because that older major is what its shim (shim.go) will
// actually serve; Phase 0 tracks no per-minor history for an older major,
// so the negotiated minor for an older major is always 0 (that major's
// canonical/only servable point today). Same-major negotiation is
// unchanged from inc1: the lower of the two minors. A client whose
// ceiling is NEWER than current, or OLDER than the window floor, falls
// back to current (inc1's existing permissive fallback for "newer than
// we understand"; the below-floor half of this is now actually refused
// earlier, at the handshake's window check, before negotiateVersion is
// even called — see handshake's callsite).
func negotiateVersion(clientMax *protocol.WireVersion, windowDepth int) protocol.WireVersion {
	current := protocol.CurrentWireVersion
	if clientMax == nil {
		return current
	}
	if clientMax.Major == current.Major {
		if clientMax.Minor < current.Minor {
			return protocol.WireVersion{Major: current.Major, Minor: clientMax.Minor}
		}
		return current
	}
	if clientMax.Major < current.Major && protocol.InVersionWindow(*clientMax, current, windowDepth) {
		// In-window older major: negotiate onto the CLIENT's own major
		// (AC-3's "an in-window client negotiates ITS major, not the
		// current one"), minor pinned to 0 -- see doc comment above.
		return protocol.WireVersion{Major: clientMax.Major, Minor: 0}
	}
	// Newer than current, or below the window floor: fall back to
	// current (inc1 behaviour). A below-floor client should already have
	// been refused by handshake's own window check before reaching here;
	// this branch is the belt-and-braces fallback for a caller that
	// calls negotiateVersion directly (as the AC-2 struct-level tests do)
	// without going through that refusal path.
	return current
}

// negotiateVersion is a method purely so call sites read naturally
// alongside s.transport/s.capabilities; it reads s.versionWindowDepth
// (increment 2 — increment 1 had no per-instance window config yet).
func (s *Server) negotiateVersion(clientMax *protocol.WireVersion) protocol.WireVersion {
	return negotiateVersion(clientMax, s.versionWindowDepth)
}

// handshake reads the connection's first frame, validates it is a
// well-formed handshake naming a matching ClientVersion and an in-window
// wire version, and responds accordingly. Returns the negotiated wire
// version and true iff the handshake succeeded and the connection should
// proceed to pump(); on any failure it has already written the refusal
// frame and the caller must close the connection without pumping further
// (Aaron DD: refuse, never degrade -- still true below the window floor,
// AC-4; graceful downgrade only applies WITHIN the window).
func (s *Server) handshake(conn *websocket.Conn) (protocol.WireVersion, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(s.handshakeTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }() // no deadline for the steady-state pump

	_, data, err := conn.ReadMessage()
	if err != nil {
		// Covers both a hard close and the read-deadline firing
		// (net.Error.Timeout()) -- both are "no handshake arrived in
		// time," MET-P012. There is nothing to send the client at this
		// point in the timeout case (nothing to write a response frame
		// to), so this failure is only visible server-side; still
		// registry-logged (GR#1) rather than swallowed.
		_ = errs.Wrap(protocol.ErrHandshakeTimeout, s.newCorrelationID(), err, map[string]any{"timeoutMs": s.handshakeTimeout.Milliseconds()})
		return protocol.WireVersion{}, false
	}

	var msg rpcMessage
	if err := json.Unmarshal(data, &msg); err != nil || msg.Method != methodHandshake || msg.Params == nil {
		reason := "not a well-formed handshake request"
		if err != nil {
			reason = err.Error()
		} else if msg.Method != methodHandshake {
			reason = "first frame method was " + msg.Method + ", want " + methodHandshake
		}
		e := errs.New(protocol.ErrHandshakeInvalid, s.newCorrelationID(), map[string]any{"reason": reason})
		s.writeRefusal(conn, msg.ID, e)
		return protocol.WireVersion{}, false
	}

	var params handshakeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.ClientVersion == "" {
		reason := "handshake params missing clientVersion"
		if err != nil {
			reason = err.Error()
		}
		e := errs.New(protocol.ErrHandshakeInvalid, s.newCorrelationID(), map[string]any{"reason": reason})
		s.writeRefusal(conn, msg.ID, e)
		return protocol.WireVersion{}, false
	}

	if normalizeVersion(params.ClientVersion) != normalizeVersion(s.engineVersion) {
		e := errs.New(protocol.ErrHandshakeVersionMismatch, s.newCorrelationID(), map[string]any{
			"clientVersion": params.ClientVersion,
			"serverVersion": s.engineVersion,
		})
		s.writeRefusal(conn, msg.ID, e)
		return protocol.WireVersion{}, false
	}

	// FEAT-1972079936 Phase 0 inc2 (AC-3/AC-4, "in-window-connects" half):
	// a client whose declared wire-version CEILING is older than the
	// server's supported window floor cannot be served on ANY major it
	// asked for (there is no shim for it) -- refuse here, before
	// negotiation, rather than silently falling back to current (which
	// would be exactly the "confident wrong data" failure GR#1 exists to
	// prevent: the client asked for something this server cannot speak to
	// it in). TODO(FEAT-1972079936 inc3, AC-4): this reuses
	// ErrHandshakeVersionMismatch for now; inc3 claims a DEDICATED
	// below-floor registry code (never MET-P010, whose meaning is the
	// separate build-string mismatch above) carrying clientVersion/
	// serverVersion/windowFloor context and a "minimum supported version"
	// message, and retires this file's now-superseded package doc comment
	// (lines 12-24) in the same increment.
	if params.ClientMaxVersion != nil {
		current := protocol.CurrentWireVersion
		if !protocol.InVersionWindow(*params.ClientMaxVersion, current, s.versionWindowDepth) &&
			params.ClientMaxVersion.Major <= current.Major {
			e := errs.New(protocol.ErrHandshakeVersionMismatch, s.newCorrelationID(), map[string]any{
				"clientMaxVersion":   params.ClientMaxVersion.String(),
				"serverVersion":      current.String(),
				"windowFloorMajor":   protocol.WindowFloorMajor(current, s.versionWindowDepth),
				"versionWindowDepth": s.versionWindowDepth,
			})
			s.writeRefusal(conn, msg.ID, e)
			return protocol.WireVersion{}, false
		}
	}

	negotiated := s.negotiateVersion(params.ClientMaxVersion)
	resultBytes, _ := json.Marshal(handshakeResult{
		Accepted:          true,
		ServerVersion:     s.engineVersion,
		NegotiatedVersion: negotiated,
		Capabilities:      protocol.IntersectCapabilities(s.capabilities, params.Capabilities),
	})
	reply := rpcMessage{JSONRPC: rpcVersion, ID: msg.ID, Result: resultBytes}
	if err := conn.WriteJSON(reply); err != nil {
		return protocol.WireVersion{}, false
	}
	return negotiated, true
}

// writeRefusal sends a JSON-RPC error response carrying e's registry code
// + display message, and (best-effort) closes the connection with a
// matching WebSocket close code. Never blocks indefinitely -- a write
// failure here just means the client already hung up, which is fine: the
// point is refusal, not guaranteed delivery.
func (s *Server) writeRefusal(conn *websocket.Conn, id *int64, e *errs.E) {
	reply := rpcMessage{
		JSONRPC: rpcVersion,
		ID:      id,
		Error: &rpcError{
			Code:    e.Code,
			Message: e.Display(),
			Data:    e.Ctx,
		},
	}
	_ = conn.WriteJSON(reply)
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, e.Code),
		time.Now().Add(time.Second))
}

// safeConn serializes every WriteJSON call made against one WebSocket
// connection behind a single mutex. gorilla/websocket requires "one
// writer at a time" (pump's own doc comment already says so) — but
// increment 1 only actually satisfied that for the THREE outbound
// notification writers (results/events/deltas), which all funnel
// through writeNotification on pump's one outbound goroutine. It missed
// a FOURTH writer: handleCommand's own ack/error responses, written
// directly from the INBOUND goroutine (pump's caller-side read loop).
// Those two goroutines racing conn.WriteJSON is a real, -race-provable
// data race (found by TestCommandRoundTrip_RealEngine_AcceptAndReject,
// FEAT-1972079852 increment 2 — inc1's own tests never drove a real
// engine's asynchronous CommandResult arriving concurrently with a
// command ack, so the race never fired under go test -race before).
// safeConn is the minimal fix: every writer (handleCommand's ack,
// replyErrorE, and writeNotification) now goes through the same
// instance's writeJSON, so at most one goroutine ever calls
// conn.WriteJSON at a time, matching the invariant pump's doc comment
// already claimed but did not fully enforce.
type safeConn struct {
	mu   sync.Mutex
	conn *websocket.Conn

	// self mirrors InProcTransport.self/Recorder.self/Engine.self exactly
	// (SEC-020 wave 1's copy-guard convention this codebase uses for
	// every mutex-guarded type reachable from more than one goroutine):
	// 'c2 := *c' is legal, unsafe-free Go, and would give c2 its OWN mu
	// while ALIASING the original's conn — two independently-locked
	// writers racing the same *websocket.Conn is exactly the hazard this
	// type exists to prevent. atomic.Pointer so the check is race-safe
	// and callable BEFORE mu is ever touched (a copy taken while the
	// original's mu was held would otherwise look permanently "locked").
	self atomic.Pointer[safeConn]
}

// newSafeConn constructs a safeConn ready for concurrent writeJSON calls.
// Always use this — never a bare safeConn{} literal or a copy of an
// existing instance (checkNotCopied rejects both).
func newSafeConn(conn *websocket.Conn) *safeConn {
	c := &safeConn{conn: conn}
	c.self.Store(c)
	return c
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other safeConn value. See the self field's doc comment for why this
// exists and why it must run before mu is ever touched.
func (c *safeConn) checkNotCopied() error {
	if c.self.Load() != c {
		return errs.New(protocol.ErrSafeConnCopied, string(errs.NewCorrelationID()), nil)
	}
	return nil
}

func (c *safeConn) writeJSON(v any) error {
	if err := c.checkNotCopied(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(); err != nil {
		return err
	}
	return c.conn.WriteJSON(v)
}

// pump relays commands inbound from the socket to s.transport and
// relays results/events/deltas outbound from s.transport to the socket,
// until either side closes. Two goroutines: the caller's own goroutine
// reads inbound frames (ServeHTTP's goroutine, one per HTTP connection,
// already the natural place for a blocking read loop); a second goroutine
// drains the transport's three outbound channels. Both goroutines write
// to the connection only through the shared safeConn (sc, below) — see
// its doc comment for why a second writer existed and raced it.
func (s *Server) pump(conn *websocket.Conn, negotiated protocol.WireVersion) {
	sc := newSafeConn(conn)
	// FEAT-1972079936 Phase 0 inc2 (AC-3): the shim (if any) this
	// connection's negotiated major requires, resolved ONCE per
	// connection at pump start rather than per-message -- a connection's
	// negotiated version never changes mid-session (renegotiation is out
	// of scope for Phase 0). offset<=0 (current major, or a client ahead
	// of current that fell back to current) correctly yields ok=false --
	// current-major traffic is never shimmed.
	offset := protocol.CurrentWireVersion.Major - negotiated.Major
	shim, hasShim := shimForOffset(offset)
	done := make(chan struct{})
	// closeOnce guards `done` being closed exactly once. Both the inbound
	// (ServeHTTP's own goroutine, below) and outbound (the goroutine
	// started just below) sides call closeDone on their own exit path, so
	// a plain bool flag here would be a genuine, -race-provable data race
	// (found by this file's own -race gate: two goroutines racing a bare
	// bool read/write around close(done)) — sync.Once is the correct,
	// minimal fix, not a mutex-guarded bool.
	var once sync.Once
	closeDone := func() {
		once.Do(func() { close(done) })
	}

	// Outbound: transport -> socket. A single goroutine writes to conn so
	// gorilla/websocket's "one writer at a time" requirement is honoured
	// (WriteJSON is not safe for concurrent use across goroutines).
	go func() {
		defer closeDone()
		results := s.transport.Results()
		events := s.transport.Events()
		deltas := s.transport.Deltas()
		for {
			select {
			case <-done:
				return
			case r, ok := <-results:
				if !ok {
					return
				}
				if !s.writeNotification(sc, methodResult, r, shim, hasShim) {
					return
				}
			case e, ok := <-events:
				if !ok {
					return
				}
				// Events/deltas are not the concrete shim this increment
				// ships (see shim.go's doc comment) -- only the
				// Command/CommandResult round-trip is shimmed today, so
				// these two notification kinds are passed through
				// unshimmed regardless of the connection's negotiated
				// version.
				if !s.writeNotification(sc, methodEvent, e, versionShim{}, false) {
					return
				}
			case d, ok := <-deltas:
				if !ok {
					return
				}
				if !s.writeNotification(sc, methodDelta, d, versionShim{}, false) {
					return
				}
			}
		}
	}()

	// Inbound: socket -> transport, on this goroutine (the caller's).
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			closeDone()
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // malformed frame post-handshake: drop, not fatal (GR#1 logs at decode site below if it matters)
		}
		switch msg.Method {
		case methodCommand:
			s.handleCommand(sc, msg, shim, hasShim)
		default:
			// Unknown method post-handshake: ignored. v1 scope is
			// command-forwarding only (subscribe/unsubscribe travel as
			// Commands, per commands.go's registry) -- no other inbound
			// method exists yet.
		}
	}
}

// handleCommand decodes msg.Params as a protocol.Command and forwards it
// to s.transport.SendCommand. Any decode or send failure is reported back
// to the caller as a JSON-RPC error response correlated by msg.ID -- the
// actual CommandResult (accepted/rejected, per the engine's own
// processing) arrives later, asynchronously, as a "result" notification
// via pump's outbound goroutine, exactly like every other CommandResult.
func (s *Server) handleCommand(sc *safeConn, msg rpcMessage, shim versionShim, hasShim bool) {
	// Defence in depth (astgate's copyguard convention): sc.writeJSON
	// itself checks this before ever touching sc.mu/sc.conn, but this
	// package's other reachable-type entry points (handleCommand,
	// replyErrorE, writeNotification below) each check directly too,
	// exactly like every other checkNotCopied call site in this codebase
	// checks at its own entry rather than relying on a callee's check
	// being visible to astgate's syntactic, no-call-graph analysis
	// (doc.go's documented blind spot).
	if err := sc.checkNotCopied(); err != nil {
		// A copied safeConn cannot safely write anything (that's the
		// whole point of the guard) -- nothing to reply with here, same
		// as writeJSON's own silent-fail-safe on this condition.
		return
	}
	params := msg.Params
	// FEAT-1972079936 Phase 0 inc2 (AC-3/AC-6): an in-window OLDER-major
	// connection's raw wire bytes are rewritten into the CURRENT major's
	// shape here, BEFORE protocol.DecodeCommand ever sees them -- this is
	// the "unwrapped away at the wsserver boundary" step AC-6's
	// determinism invariant requires: the decoded protocol.Command below
	// must be byte-identical to what a current-major client would have
	// produced, regardless of which major this connection negotiated.
	if hasShim && shim.adaptCommandIn != nil {
		adapted, err := shim.adaptCommandIn(params)
		if err != nil {
			e := errs.New(protocol.ErrCommandDecodeFailed, s.newCorrelationID(), map[string]any{"reason": "version shim: " + err.Error()})
			s.replyErrorE(sc, msg.ID, e)
			return
		}
		params = adapted
	}
	cmd, err := protocol.DecodeCommand(params)
	if err != nil {
		e := errs.New(protocol.ErrCommandDecodeFailed, s.newCorrelationID(), map[string]any{"reason": err.Error()})
		s.replyErrorE(sc, msg.ID, e)
		return
	}
	if err := cmd.Validate(); err != nil {
		e := errs.New(protocol.ErrCommandValidationFailed, s.newCorrelationID(), map[string]any{
			"reason":        err.Error(),
			"correlationId": string(cmd.CorrelationID),
		})
		s.replyErrorE(sc, msg.ID, e)
		return
	}
	if err := s.transport.SendCommand(cmd); err != nil {
		e := errs.New(protocol.ErrCommandSendFailed, s.newCorrelationID(), map[string]any{
			"reason":        err.Error(),
			"correlationId": string(cmd.CorrelationID),
		})
		s.replyErrorE(sc, msg.ID, e)
		return
	}
	ackBytes, _ := json.Marshal(map[string]any{"queued": true})
	_ = sc.writeJSON(rpcMessage{JSONRPC: rpcVersion, ID: msg.ID, Result: ackBytes})
}

// replyErrorE writes a JSON-RPC error response carrying e's real,
// registry-sourced code, its already-resolved Display() message, and its
// context map -- the same shape writeRefusal uses for handshake-time
// refusals (BAR-2, round-r1 REJECT: the three post-handshake failure
// sites here used to hand-build an ad hoc rpcError{Code: "MET-P011", ...}
// literal instead of going through errs.New, losing both a distinct code
// per failure class and a fresh correlation ID per GR#1/GR#7).
func (s *Server) replyErrorE(sc *safeConn, id *int64, e *errs.E) {
	if err := sc.checkNotCopied(); err != nil {
		return // see handleCommand's identical guard for why this is a silent no-op
	}
	_ = sc.writeJSON(rpcMessage{
		JSONRPC: rpcVersion,
		ID:      id,
		Error:   &rpcError{Code: e.Code, Message: e.Display(), Data: e.Ctx},
	})
}

// writeNotification marshals payload as method's params and writes a
// one-way (no id) JSON-RPC notification. Returns false on any write
// failure (the caller's outbound goroutine treats that as "connection
// gone" and stops).
func (s *Server) writeNotification(sc *safeConn, method string, payload any, shim versionShim, hasShim bool) bool {
	if err := sc.checkNotCopied(); err != nil {
		return false // see handleCommand's identical guard for why this is a silent no-op
	}
	paramsBytes, err := json.Marshal(payload)
	if err != nil {
		return true // should never happen for these concrete wire types; skip this one frame rather than killing the connection
	}
	// FEAT-1972079936 Phase 0 inc2 (AC-3): the mirror-image shim for the
	// method this negotiated-older-major connection expects its response
	// shaped as (shim.go's adaptResultOut) -- applied only for "result"
	// notifications today, matching this increment's one concrete shim
	// (a CommandResult's correlation-id field rename); events/deltas are
	// called with hasShim=false by pump and never reach this branch.
	if hasShim && method == methodResult && shim.adaptResultOut != nil {
		adapted, err := shim.adaptResultOut(paramsBytes)
		if err != nil {
			return true // malformed-for-shimming payload: skip this one frame, don't kill the connection over it
		}
		paramsBytes = adapted
	}
	msg := rpcMessage{JSONRPC: rpcVersion, Method: method, Params: paramsBytes}
	return sc.writeJSON(msg) == nil
}
