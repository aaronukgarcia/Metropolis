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
// # Connect-time handshake: current behaviour (FEAT-1972079936 Phase 0,
// increment 3 — this section supersedes the original 2026-08-31
// "REFUSAL, never a silent degrade" doc, which described the FIRST cut
// of this package (a single build-string equality check with no window)
// and is now retired in favour of what actually ships)
//
// The FIRST frame a client sends on a new connection MUST be a well-formed
// handshake request (MET-P011 refuses a malformed one; MET-P012 refuses a
// connection that never sends one within HandshakeTimeout). Two
// INDEPENDENT checks then run, in order:
//
//  1. Build-string identity (engineVersion, GR#2 git-describe): this check
//     is UNCHANGED since the package's original DD and remains a strict
//     equality refusal (MET-P010, ErrHandshakeVersionMismatch) — it exists
//     to catch a same-commit-expected dev loopback (metroserve + webconsole
//     built from the same tree) drifting apart, which the wire-version
//     window below deliberately does NOT cover (Aaron ruling 5: the build
//     string stays a separate, informational-turned-gating field for this
//     one purpose; normalizeVersion strips only the volatile "-dirty"
//     suffix, BAR-4).
//  2. Wire-version negotiation (protocol.WireVersion, wireversion.go): this
//     is the part Aaron's 2026-08-31 superseding DD (docs/planning/
//     acceptance/feat-1972079936-phase0-protocol-versioning.md) replaced
//     wholesale. A long-lived Azure-hosted server outlives many client-tab
//     refresh cycles, so an in-window OLDER client is negotiated onto its
//     own major and served correctly via shim.go's per-offset compat shim
//     — NOT refused, NOT silently upgraded to current. Only a client whose
//     declared ceiling is older than the server's configured window floor
//     (protocol.WindowFloorMajor, versionWindowDepth) is refused on wire-
//     version grounds, with a DEDICATED code (MET-P020,
//     ErrHandshakeBelowWindowFloor — increment 3 retires increment 2's
//     temporary reuse of MET-P010 for this case) carrying the client's
//     ceiling, the server's current version, and the window floor, so the
//     refusal is actionable ("upgrade to at least major N").
//
// Capability negotiation (AC-5, increment 3) rides the same handshake
// frame: the intersection of the client's and server's declared
// capability sets (protocol.IntersectCapabilities) becomes this
// connection's NEGOTIATED set, threaded into pump/handleCommand below and
// enforced per-command (protocol.RequiredCapability) — a command whose
// Kind needs a capability this connection never negotiated is refused
// (MET-P021, ErrCapabilityRequired) rather than served or silently
// dropped.
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

	// CityID/TenantID (FEAT-1972079936 Phase 2 inc2, AC-1): the city this
	// connection wants to be routed to. Both OPTIONAL and additive: an
	// absent/empty CityID defaults to defaultCityID ("default") and an
	// absent/empty TenantID to defaultTenantID ("local", matching
	// inc1/inc4's placeholder). An OLD client that never sends either field
	// is therefore indistinguishable from one explicitly asking for the
	// "default" city and is routed there -- preserving today's single-city
	// behaviour exactly. These fields are consulted ONLY when a transport
	// resolver is installed (WithTransportResolver); with no resolver the
	// server serves its single wrapped transport regardless of what a client
	// names here (backward-compat, AC-6).
	CityID   string `json:"cityId,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
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

// TransportResolver maps a connection's handshake-declared (tenantID,
// cityID) -- defaults already applied -- to the protocol.Transport that
// connection should be bound to for its whole life (FEAT-1972079936 Phase 2
// inc2, AC-2). It takes two plain strings, NOT a persist.CityKey, ON PURPOSE:
// wsserver lives in internal/protocol (the interface layer) and MUST NOT gain
// an edge to internal/persist (nor, by import direction, to cmd/metroserve
// where CityHost lives). cmd/metroserve supplies a closure that builds its
// own persist.CityKey from these two strings and calls host.GetOrCreate --
// keeping wsserver's dependency surface unchanged (no new GR#25 edge). A
// non-nil error REFUSES the handshake cleanly (MET-P030), never a fallback.
type TransportResolver func(tenantID, cityID string) (protocol.Transport, error)

// defaultCityID / defaultTenantID are the placeholders applied when a
// handshake omits (or empties) the corresponding field (AC-1). "default"
// city + "local" tenant match cmd/metroserve's inc1/inc4 defaults, so an old
// client sending neither is routed to exactly the city metroserve pre-creates.
const (
	defaultCityID   = "default"
	defaultTenantID = "local"
)

// Server bridges HTTP WebSocket connections to a wrapped protocol.Transport.
type Server struct {
	transport        protocol.Transport
	engineVersion    string
	handshakeTimeout time.Duration
	upgrader         websocket.Upgrader
	newCorrelationID func() string
	// resolveTransport, when non-nil (installed via WithTransportResolver,
	// FEAT-1972079936 Phase 2 inc2 AC-2), routes each connection to a
	// per-city transport resolved during the handshake. When nil (the
	// default -- every pre-inc2 caller), the single `transport` field above
	// serves every connection EXACTLY as today: no resolver call, no
	// behaviour change, every existing test unaffected byte-for-byte (AC-6).
	resolveTransport TransportResolver
	// capabilities is this server's own declared capability set (AC-5).
	// Defaults to defaultServerCapabilities (increment 3: this build
	// speaks protocol.CapDebugCommands, Phase 0's one illustrative gated
	// Kind) — increments 1/2 left this nil since nothing was gated yet;
	// override via WithCapabilities (tests use this to exercise a server
	// that does NOT declare a capability the client asks for).
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

// defaultServerCapabilities is what a Server declares unless overridden via
// WithCapabilities (FEAT-1972079936 Phase 0 increment 3, AC-5): this build
// speaks protocol.CapDebugCommands, the one illustrative capability-gated
// Kind Phase 0 ships (protocol/capability.go's doc comment).
var defaultServerCapabilities = []string{protocol.CapDebugCommands}

// WithCapabilities overrides the server's own declared capability set
// (FEAT-1972079936 Phase 0 inc3, AC-5). Tests use this to exercise a
// server that deliberately does NOT declare a capability a client asks
// for (proving the negotiated/intersected set, and the resulting
// enforcement refusal, actually depends on BOTH sides — not just the
// client's request).
func WithCapabilities(caps []string) Option {
	return func(s *Server) { s.capabilities = caps }
}

// WithTransportResolver installs a per-connection transport resolver
// (FEAT-1972079936 Phase 2 inc2, AC-2). With it, each connection's transport
// is resolved during the handshake from the handshake's (tenant, city) --
// binding that connection to its own city's engine for the connection's life.
// Without it, the Server behaves EXACTLY as today (its single wrapped
// transport serves every connection). cmd/metroserve supplies a closure over
// its CityHost here; see TransportResolver's doc comment for why the signature
// is two strings rather than a persist.CityKey (import-direction / no new
// dependency edge).
func WithTransportResolver(r TransportResolver) Option {
	return func(s *Server) { s.resolveTransport = r }
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
		capabilities:       defaultServerCapabilities,
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

	negotiated, negotiatedCaps, connTransport, ok := s.handshake(conn)
	if !ok {
		return
	}
	s.pump(conn, connTransport, negotiated, negotiatedCaps)
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
// version, the negotiated (intersected, AC-5) capability set, and true iff
// the handshake succeeded and the connection should proceed to pump(); on
// any failure it has already written the refusal frame and the caller
// must close the connection without pumping further (the build-string
// check and the below-window-floor check are both still hard refusals;
// graceful downgrade only applies WITHIN the window -- see this file's
// package doc for the current, post-increment-3 framing of this rule).
// The returned protocol.Transport is the transport THIS connection is bound
// to for the rest of its life (FEAT-1972079936 Phase 2 inc2, AC-3): the
// server's single wrapped transport when no resolver is installed, or the
// per-city transport the resolver returned when one is. A resolver error
// refuses the handshake cleanly (MET-P030) and returns ok=false, never a
// fallback transport.
func (s *Server) handshake(conn *websocket.Conn) (protocol.WireVersion, []string, protocol.Transport, bool) {
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
		return protocol.WireVersion{}, nil, nil, false
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
		return protocol.WireVersion{}, nil, nil, false
	}

	var params handshakeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.ClientVersion == "" {
		reason := "handshake params missing clientVersion"
		if err != nil {
			reason = err.Error()
		}
		e := errs.New(protocol.ErrHandshakeInvalid, s.newCorrelationID(), map[string]any{"reason": reason})
		s.writeRefusal(conn, msg.ID, e)
		return protocol.WireVersion{}, nil, nil, false
	}

	if normalizeVersion(params.ClientVersion) != normalizeVersion(s.engineVersion) {
		e := errs.New(protocol.ErrHandshakeVersionMismatch, s.newCorrelationID(), map[string]any{
			"clientVersion": params.ClientVersion,
			"serverVersion": s.engineVersion,
		})
		s.writeRefusal(conn, msg.ID, e)
		return protocol.WireVersion{}, nil, nil, false
	}

	// FEAT-1972079936 Phase 0 inc3 (AC-4, the below-floor half): a client
	// whose declared wire-version CEILING is older than the server's
	// supported window floor cannot be served on ANY major it asked for
	// (there is no shim for it) -- refuse here, before negotiation, rather
	// than silently falling back to current (which would be exactly the
	// "confident wrong data" failure GR#1 exists to prevent: the client
	// asked for something this server cannot speak to it in). This now
	// uses the DEDICATED ErrHandshakeBelowWindowFloor code (MET-P020),
	// retiring inc2's temporary reuse of ErrHandshakeVersionMismatch
	// (MET-P010, whose registered meaning is the separate build-string
	// check above and no longer describes this case).
	if params.ClientMaxVersion != nil {
		current := protocol.CurrentWireVersion
		if !protocol.InVersionWindow(*params.ClientMaxVersion, current, s.versionWindowDepth) &&
			params.ClientMaxVersion.Major <= current.Major {
			e := errs.New(protocol.ErrHandshakeBelowWindowFloor, s.newCorrelationID(), map[string]any{
				"clientMaxVersion":   params.ClientMaxVersion.String(),
				"serverVersion":      current.String(),
				"windowFloorMajor":   protocol.WindowFloorMajor(current, s.versionWindowDepth),
				"versionWindowDepth": s.versionWindowDepth,
			})
			s.writeRefusal(conn, msg.ID, e)
			return protocol.WireVersion{}, nil, nil, false
		}
	}

	negotiated := s.negotiateVersion(params.ClientMaxVersion)
	negotiatedCaps := protocol.IntersectCapabilities(s.capabilities, params.Capabilities)

	// FEAT-1972079936 Phase 2 inc2 (AC-2/AC-3): resolve the connection's
	// transport AFTER version/capability negotiation succeeds. With no
	// resolver installed the connection is bound to the server's single
	// wrapped transport -- today's behaviour, byte-for-byte (AC-6). With a
	// resolver installed, the handshake's (tenant, city) -- defaults applied
	// (AC-1) -- selects the per-city transport; a resolver error REFUSES the
	// handshake cleanly (MET-P030), never falling back to another city (AC-3).
	// The bound city, like the negotiated version, is fixed for the
	// connection's life (no mid-session rebind).
	connTransport := s.transport
	if s.resolveTransport != nil {
		tenantID := params.TenantID
		if tenantID == "" {
			tenantID = defaultTenantID
		}
		cityID := params.CityID
		if cityID == "" {
			cityID = defaultCityID
		}
		resolved, err := s.resolveTransport(tenantID, cityID)
		if err != nil {
			e := errs.New(protocol.ErrHandshakeCityUnavailable, s.newCorrelationID(), map[string]any{
				"tenantId": tenantID,
				"cityId":   cityID,
				"reason":   err.Error(),
			})
			s.writeRefusal(conn, msg.ID, e)
			return protocol.WireVersion{}, nil, nil, false
		}
		connTransport = resolved
	}

	resultBytes, _ := json.Marshal(handshakeResult{
		Accepted:          true,
		ServerVersion:     s.engineVersion,
		NegotiatedVersion: negotiated,
		Capabilities:      negotiatedCaps,
	})
	reply := rpcMessage{JSONRPC: rpcVersion, ID: msg.ID, Result: resultBytes}
	if err := conn.WriteJSON(reply); err != nil {
		return protocol.WireVersion{}, nil, nil, false
	}
	return negotiated, negotiatedCaps, connTransport, true
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
//
// transport is THIS connection's bound transport (FEAT-1972079936 Phase 2
// inc2): the server's single wrapped transport when no resolver is installed,
// or the per-city transport the handshake resolved when one is. Every
// inbound-command send and every outbound Results/Events/Deltas drain in this
// function goes through this per-connection transport, so a connection bound
// to city A can never move city B's engine or observe B's deltas (AC-4).
func (s *Server) pump(conn *websocket.Conn, transport protocol.Transport, negotiated protocol.WireVersion, negotiatedCaps []string) {
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
		results := transport.Results()
		events := transport.Events()
		deltas := transport.Deltas()
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
			s.handleCommand(sc, transport, msg, shim, hasShim, negotiatedCaps)
		default:
			// Unknown method post-handshake: ignored. v1 scope is
			// command-forwarding only (subscribe/unsubscribe travel as
			// Commands, per commands.go's registry) -- no other inbound
			// method exists yet.
		}
	}
}

// handleCommand decodes msg.Params as a protocol.Command and forwards it
// to this connection's bound transport (FEAT-1972079936 Phase 2 inc2: the
// per-city transport when a resolver is installed, else the server's single
// wrapped transport). Any decode or send failure is reported back
// to the caller as a JSON-RPC error response correlated by msg.ID -- the
// actual CommandResult (accepted/rejected, per the engine's own
// processing) arrives later, asynchronously, as a "result" notification
// via pump's outbound goroutine, exactly like every other CommandResult.
func (s *Server) handleCommand(sc *safeConn, transport protocol.Transport, msg rpcMessage, shim versionShim, hasShim bool, negotiatedCaps []string) {
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
		// BUG-471 fix (FEAT-1972079936 Phase 0 inc3): a shimmed connection's
		// legacy client declared ITS OWN protocolVersion on the wire (e.g.
		// "1.0" for an offset-1 client) -- adaptCommandIn above only renamed
		// the correlation-id key, leaving that stale, non-canonical version
		// tag to flow straight into the decoded Command and, from there,
		// into the journal (GR#27/hard-reset-replay FEAT-1972079897), where
		// it breaks old-vs-rebuilt journal DIFFING and is forward-replay-
		// fragile. Normalize it HERE, on the raw wire bytes, before
		// protocol.DecodeCommand ever sees them, so every journaled command
		// carries the CURRENT canonical wire version regardless of which
		// major this connection actually negotiated -- the version tag is
		// pure transport/journal METADATA (AC-6/GR#21: no engine path reads
		// it), so canonicalizing it here changes no sim-state semantics.
		adapted, err = normalizeProtocolVersionField(adapted, protocol.CurrentWireVersion.String())
		if err != nil {
			e := errs.New(protocol.ErrCommandDecodeFailed, s.newCorrelationID(), map[string]any{"reason": "version shim: protocolVersion normalize: " + err.Error()})
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
	// FEAT-1972079936 Phase 0 inc3 (AC-5): fine-grained capability gating.
	// A Kind requiring a capability (protocol.RequiredCapability) that this
	// connection's NEGOTIATED set (the intersection computed at handshake
	// time) does not contain is refused here -- the envelope/payload are
	// perfectly well-formed, the connection simply never negotiated support
	// for this specific feature. This is enforced server-side regardless of
	// whether the client gated itself locally (protocolClient.ts's
	// hasCapability is the client-side convenience; this is the backstop).
	if capability, required := protocol.RequiredCapability(cmd.Kind); required && !protocol.HasCapability(negotiatedCaps, capability) {
		e := errs.New(protocol.ErrCapabilityRequired, s.newCorrelationID(), map[string]any{
			"capability":    capability,
			"kind":          string(cmd.Kind),
			"correlationId": string(cmd.CorrelationID),
		})
		s.replyErrorE(sc, msg.ID, e)
		return
	}
	if err := transport.SendCommand(cmd); err != nil {
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
