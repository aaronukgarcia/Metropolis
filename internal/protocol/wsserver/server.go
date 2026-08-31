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
	ClientVersion string `json:"clientVersion"`
}

// handshakeResult is methodHandshake's successful response payload.
type handshakeResult struct {
	Accepted      bool   `json:"accepted"`
	ServerVersion string `json:"serverVersion"`
}

// Server bridges HTTP WebSocket connections to a wrapped protocol.Transport.
type Server struct {
	transport        protocol.Transport
	engineVersion    string
	handshakeTimeout time.Duration
	upgrader         websocket.Upgrader
	newCorrelationID func() string
}

// New constructs a Server wrapping transport. engineVersion is this
// build's own version string (GR#2: callers pass the git-describe-derived
// value, never a hand-maintained literal) — it is what every connecting
// client's handshake is compared against. A zero handshakeTimeout uses
// DefaultHandshakeTimeout.
func New(transport protocol.Transport, engineVersion string, handshakeTimeout time.Duration) *Server {
	if handshakeTimeout <= 0 {
		handshakeTimeout = DefaultHandshakeTimeout
	}
	return &Server{
		transport:        transport,
		engineVersion:    engineVersion,
		handshakeTimeout: handshakeTimeout,
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
	defer conn.Close()

	if !s.handshake(conn) {
		return
	}
	s.pump(conn)
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

// handshake reads the connection's first frame, validates it is a
// well-formed handshake naming a matching ClientVersion, and responds
// accordingly. Returns true iff the handshake succeeded and the
// connection should proceed to pump(); on any failure it has already
// written the refusal frame and the caller must close the connection
// without pumping further (Aaron DD: refuse, never degrade).
func (s *Server) handshake(conn *websocket.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(s.handshakeTimeout))
	defer conn.SetReadDeadline(time.Time{}) // no deadline for the steady-state pump

	_, data, err := conn.ReadMessage()
	if err != nil {
		// Covers both a hard close and the read-deadline firing
		// (net.Error.Timeout()) -- both are "no handshake arrived in
		// time," MET-P012. There is nothing to send the client at this
		// point in the timeout case (nothing to write a response frame
		// to), so this failure is only visible server-side; still
		// registry-logged (GR#1) rather than swallowed.
		_ = errs.Wrap(protocol.ErrHandshakeTimeout, s.newCorrelationID(), err, map[string]any{"timeoutMs": s.handshakeTimeout.Milliseconds()})
		return false
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
		return false
	}

	var params handshakeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.ClientVersion == "" {
		reason := "handshake params missing clientVersion"
		if err != nil {
			reason = err.Error()
		}
		e := errs.New(protocol.ErrHandshakeInvalid, s.newCorrelationID(), map[string]any{"reason": reason})
		s.writeRefusal(conn, msg.ID, e)
		return false
	}

	if normalizeVersion(params.ClientVersion) != normalizeVersion(s.engineVersion) {
		e := errs.New(protocol.ErrHandshakeVersionMismatch, s.newCorrelationID(), map[string]any{
			"clientVersion": params.ClientVersion,
			"serverVersion": s.engineVersion,
		})
		s.writeRefusal(conn, msg.ID, e)
		return false
	}

	resultBytes, _ := json.Marshal(handshakeResult{Accepted: true, ServerVersion: s.engineVersion})
	reply := rpcMessage{JSONRPC: rpcVersion, ID: msg.ID, Result: resultBytes}
	if err := conn.WriteJSON(reply); err != nil {
		return false
	}
	return true
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

// pump relays commands inbound from the socket to s.transport and
// relays results/events/deltas outbound from s.transport to the socket,
// until either side closes. Two goroutines: the caller's own goroutine
// reads inbound frames (ServeHTTP's goroutine, one per HTTP connection,
// already the natural place for a blocking read loop); a second goroutine
// drains the transport's three outbound channels.
func (s *Server) pump(conn *websocket.Conn) {
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
				if !s.writeNotification(conn, methodResult, r) {
					return
				}
			case e, ok := <-events:
				if !ok {
					return
				}
				if !s.writeNotification(conn, methodEvent, e) {
					return
				}
			case d, ok := <-deltas:
				if !ok {
					return
				}
				if !s.writeNotification(conn, methodDelta, d) {
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
			s.handleCommand(conn, msg)
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
func (s *Server) handleCommand(conn *websocket.Conn, msg rpcMessage) {
	cmd, err := protocol.DecodeCommand(msg.Params)
	if err != nil {
		e := errs.New(protocol.ErrCommandDecodeFailed, s.newCorrelationID(), map[string]any{"reason": err.Error()})
		s.replyErrorE(conn, msg.ID, e)
		return
	}
	if err := cmd.Validate(); err != nil {
		e := errs.New(protocol.ErrCommandValidationFailed, s.newCorrelationID(), map[string]any{
			"reason":        err.Error(),
			"correlationId": string(cmd.CorrelationID),
		})
		s.replyErrorE(conn, msg.ID, e)
		return
	}
	if err := s.transport.SendCommand(cmd); err != nil {
		e := errs.New(protocol.ErrCommandSendFailed, s.newCorrelationID(), map[string]any{
			"reason":        err.Error(),
			"correlationId": string(cmd.CorrelationID),
		})
		s.replyErrorE(conn, msg.ID, e)
		return
	}
	ackBytes, _ := json.Marshal(map[string]any{"queued": true})
	_ = conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: msg.ID, Result: ackBytes})
}

// replyErrorE writes a JSON-RPC error response carrying e's real,
// registry-sourced code, its already-resolved Display() message, and its
// context map -- the same shape writeRefusal uses for handshake-time
// refusals (BAR-2, round-r1 REJECT: the three post-handshake failure
// sites here used to hand-build an ad hoc rpcError{Code: "MET-P011", ...}
// literal instead of going through errs.New, losing both a distinct code
// per failure class and a fresh correlation ID per GR#1/GR#7).
func (s *Server) replyErrorE(conn *websocket.Conn, id *int64, e *errs.E) {
	_ = conn.WriteJSON(rpcMessage{
		JSONRPC: rpcVersion,
		ID:      id,
		Error:   &rpcError{Code: e.Code, Message: e.Display(), Data: e.Ctx},
	})
}

// writeNotification marshals payload as method's params and writes a
// one-way (no id) JSON-RPC notification. Returns false on any write
// failure (the caller's outbound goroutine treats that as "connection
// gone" and stops).
func (s *Server) writeNotification(conn *websocket.Conn, method string, payload any) bool {
	paramsBytes, err := json.Marshal(payload)
	if err != nil {
		return true // should never happen for these concrete wire types; skip this one frame rather than killing the connection
	}
	msg := rpcMessage{JSONRPC: rpcVersion, Method: method, Params: paramsBytes}
	return conn.WriteJSON(msg) == nil
}
