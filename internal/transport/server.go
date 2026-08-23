package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// outboundBuffer is each session's outbound frame budget. When a session
// exceeds it the connection is closed (ErrSlowConsumer), never blocked
// on: one slow browser tab must not stall the shared drain loop that
// serves every other session, mirroring InProcTransport's
// never-block-the-engine drop policy at the session boundary.
const outboundBuffer = 256

// outboundByteBudget is each session's outbound BYTE ceiling on top of
// outboundBuffer's frame count. Viewport deltas are ~2MB apiece, so the
// frame budget alone admits ~512MB of queued payload before a slow
// consumer is noticed — an OOM class of its own. Crossing this ceiling
// trips ErrSlowConsumer (MET-P102) exactly like overflowing the frame
// budget does.
const outboundByteBudget = 16 << 20 // 16 MiB

// inboundCommandLimit bounds each inbound frame (commands are tiny
// envelopes; anything bigger is abuse or a bug, not a command).
const inboundCommandLimit = 1 << 16 // 64 KiB

// Server bridges WebSocket clients onto one composed engine through a
// single protocol.InProcTransport. It owns that transport's two engine-
// side loops (RunCommandLoop + StartSubscriptionPump) for its lifetime,
// drains the shared result/event/delta channels once (GR#21's
// single-reader discipline — cmd/metropolis/boot.go primes and binds the
// same way), and routes every message to the session that asked for it.
//
// The Server adds no engine behaviour of its own: every client frame
// becomes a validated protocol.Command on the same command path the TUI
// uses; every delta is an engine-published patch relayed verbatim.
type Server struct {
	tpt    *protocol.InProcTransport
	engine *enginecore.Engine

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	sessions  map[*session]struct{}
	pending   map[protocol.CorrelationID]*routeEntry // commands awaiting their CommandResult, keyed by SERVER-minted CID
	awaiting  map[protocol.CorrelationID]*session    // Subscribe commands awaiting their FIRST delta, keyed by SERVER-minted CID
	subs      map[protocol.SubscriptionID]*session
	closeOnce sync.Once

	// nextCID mints the server-side routing keys. Client-supplied
	// CorrelationIDs are NEVER used as map keys (SEC round-2 F1): with
	// last-writer-wins on a client-chosen key, a second session replaying
	// session A's correlationId would overwrite A's pending/awaiting
	// entries and steal A's results and subscription bind. Every inbound
	// command is re-keyed to a fresh minted ID; routeResult translates
	// back to the client's own ID at delivery time.
	nextCID atomic.Uint64

	// self holds the address NewServer gave this Server at construction
	// (SEC-020 wave 1, mirroring InProcTransport.self / Engine.self /
	// SubscriptionServer.self): 's2 := *s' is legal Go from outside the
	// package, aliases every map above (reference types) while carrying
	// its OWN independent mu — so the copy's locking protects nothing
	// shared and Close on a copy could close the shared transport out
	// from under in-flight sends. Every exported surface checks identity
	// before touching locks or maps.
	self atomic.Pointer[Server]

	cmdLoopDone chan struct{}
	drainDone   chan struct{}
	pumpDone    <-chan struct{}
}

// NewServer starts the engine-side loops against a fresh transport and
// returns a ready Server. engine must already be compose.Wire'd (or at
// minimum have its views registered); NewServer itself wires nothing.
func NewServer(engine *enginecore.Engine) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	tpt := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	pumpDone, err := engine.StartSubscriptionPump(ctx, tpt)
	if err != nil {
		cancel()
		_ = tpt.Close()
		return nil, errs.Wrap(ErrWSAcceptFailed, errs.NewCorrelationID(), err, map[string]any{
			"cause": "StartSubscriptionPump",
		})
	}
	s := &Server{
		tpt:         tpt,
		engine:      engine,
		ctx:         ctx,
		cancel:      cancel,
		sessions:    make(map[*session]struct{}),
		pending:     make(map[protocol.CorrelationID]*routeEntry),
		awaiting:    make(map[protocol.CorrelationID]*session),
		subs:        make(map[protocol.SubscriptionID]*session),
		cmdLoopDone: make(chan struct{}),
		drainDone:   make(chan struct{}),
		pumpDone:    pumpDone,
	}
	s.self.Store(s)
	go func() {
		// RunCommandLoop's exit contract: cancel-then-join is the only
		// clean shutdown; the return value is discarded here because a
		// cancelled ctx always yields nil.
		defer close(s.cmdLoopDone)
		_ = s.engine.RunCommandLoop(s.ctx, s.tpt)
	}()
	go func() {
		defer close(s.drainDone)
		s.drain()
	}()
	return s, nil
}

// routeEntry is one in-flight command's registration: the session that
// owns it plus the client's ORIGINAL correlationId, which routeResult
// restores before delivery (the engine only ever sees the server-minted
// key — see Server.nextCID).
type routeEntry struct {
	sess      *session
	clientCID protocol.CorrelationID
}

func (s *Server) mintCID() protocol.CorrelationID {
	return protocol.CorrelationID(fmt.Sprintf("srv-%016x", s.nextCID.Add(1)))
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Server value (SEC-020 wave 1, mirroring InProcTransport's
// identically-named guard — transport.go there for the full rationale).
// Deliberately lock-free (a single atomic.Pointer.Load) so it is always
// safe to call before any mutex is touched.
func (s *Server) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrServerCopied, correlationID, ctx)
	}
	return nil
}

// Handler returns the http.Handler serving the WebSocket endpoint. Any
// upgradeable request becomes a session; everything else is rejected by
// websocket.Accept with ErrWSAcceptFailed logged server-side.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Handler"}); err != nil {
			errs.New(ErrWSAcceptFailed, errs.NewCorrelationID(), map[string]any{"cause": err.Error()})
			http.Error(w, "server misconfigured", http.StatusInternalServerError)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errs.New(ErrWSAcceptFailed, errs.NewCorrelationID(), map[string]any{"cause": err.Error()})
			return
		}
		s.serveSession(conn)
	})
}

// initSessionLimits caps inbound frames; outbound (delta) frames are
// engine-published patches and may legitimately be megabytes.
func initSessionLimits(conn *websocket.Conn) {
	conn.SetReadLimit(inboundCommandLimit)
}

// Close shuts the bridge down in RunCommandLoop's documented order:
// cancel ctx, join the command loop, then close the transport (never the
// reverse — see core/commands.go's exit contract). Idempotent.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Close"}); err != nil {
			return
		}
		s.cancel()
		<-s.cmdLoopDone
		<-s.pumpDone
		_ = s.tpt.Close()
		<-s.drainDone

		// Force-close any still-open sessions so their read pumps unblock.
		s.mu.Lock()
		conns := make([]*websocket.Conn, 0, len(s.sessions))
		for sess := range s.sessions {
			conns = append(conns, sess.conn)
		}
		s.mu.Unlock()
		for _, c := range conns {
			_ = c.CloseNow()
		}
	})
	return nil
}

// drain is the ONE reader of the transport's Results/Events/Deltas
// channels (single-writer rule, GR#21): it routes results to the session
// whose command is pending, binds and routes deltas to subscribing
// sessions, and broadcasts events to everyone.
func (s *Server) drain() {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "drain"}); err != nil {
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case r := <-s.tpt.Results():
			s.routeResult(r)
		case ev := <-s.tpt.Events():
			s.broadcast(eventFrame(ev))
		case d := <-s.tpt.Deltas():
			s.routeDelta(d)
		}
	}
}

func (s *Server) routeResult(r protocol.CommandResult) {
	if err := s.checkNotCopied(string(r.CorrelationID), map[string]any{"method": "routeResult"}); err != nil {
		return
	}
	s.mu.Lock()
	entry := s.pending[r.CorrelationID]
	delete(s.pending, r.CorrelationID)
	// F2: a REJECTED Subscribe will never produce the first delta its
	// awaiting entry waits for — clean it here at result-delivery time
	// instead of leaking until disconnect. (An accepted Subscribe keeps
	// its awaiting entry: the pump publishes the first delta
	// asynchronously AFTER this result, and bind-on-first-delta needs it.)
	if entry != nil && !r.Accepted {
		delete(s.awaiting, r.CorrelationID)
	}
	s.mu.Unlock()
	if entry == nil {
		// A rejection built before registration cannot happen (we register
		// pending BEFORE SendCommand), so this is genuinely "the session is
		// gone" or an engine-initiated result — dropped, loudly.
		errs.New(ErrRouteMiss, errs.NewCorrelationID(), map[string]any{"kind": "result", "key": string(r.CorrelationID)})
		return
	}
	r.CorrelationID = entry.clientCID
	entry.sess.send(resultFrame(r))
}

func (s *Server) routeDelta(d protocol.Delta) {
	if err := s.checkNotCopied(string(d.CorrelationID), map[string]any{"method": "routeDelta"}); err != nil {
		return
	}
	// Bind-on-first-delta: subscribe.go echoes the Subscribe command's own
	// CorrelationID on exactly the first delta of a fresh subscription —
	// which, since the F1 fix, is always a SERVER-minted ID (serveSession
	// re-keys every inbound command), so the awaiting lookup can never be
	// poisoned by a second session replaying another session's
	// correlationId.
	//
	// The binding candidate lives in awaiting, NOT pending, deliberately:
	// the pump publishes the first delta ASYNCHRONOUSLY off handleSubscribe,
	// so the drain loop usually observes the Subscribe's CommandResult
	// BEFORE that first delta — and routeResult has already consumed the
	// pending entry by then (the original implementation bound out of
	// pending here and dropped every subscribe-time snapshot as a route
	// miss). awaiting survives until the first delta arrives or the
	// session disconnects.
	var sess *session
	s.mu.Lock()
	sess = s.subs[d.SubscriptionID]
	if sess == nil && d.CorrelationID != "" {
		if cand := s.awaiting[d.CorrelationID]; cand != nil {
			s.subs[d.SubscriptionID] = cand
			delete(s.awaiting, d.CorrelationID)
			sess = cand
		}
	}
	s.mu.Unlock()
	if sess == nil {
		errs.New(ErrRouteMiss, errs.NewCorrelationID(), map[string]any{"kind": "delta", "key": string(d.SubscriptionID)})
		return
	}
	sess.send(deltaFrame(d))
}

func (s *Server) broadcast(frame []byte) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "broadcast"}); err != nil {
		return
	}
	s.mu.Lock()
	targets := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		targets = append(targets, sess)
	}
	s.mu.Unlock()
	for _, sess := range targets {
		sess.send(frame)
	}
}

// track registers the session as awaiting cid's CommandResult (and, for
// a Subscribe command, its first delta too) under a fresh SERVER-minted
// correlation ID, returned to the caller for SendCommand. Both
// registrations MUST happen before SendCommand: the single drain
// goroutine is the only consumer of Results()/Deltas(), so an outbound
// message can never overtake these registrations. The client's own
// correlationId never becomes a routing key (F1: cross-session replay
// of one session's correlationId must not be able to steal another
// session's results or subscription bind).
func (s *Server) track(clientCID protocol.CorrelationID, kind protocol.Kind, sess *session) protocol.CorrelationID {
	minted := s.mintCID()
	if err := s.checkNotCopied(string(minted), map[string]any{"method": "track"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[minted] = &routeEntry{sess: sess, clientCID: clientCID}
	if kind == protocol.KindSubscribe {
		s.awaiting[minted] = sess
	}
	return minted
}

// untrack removes an in-flight command's registrations after a
// SendCommand failure (nothing will ever come back for it). Cleans BOTH
// tables — the awaiting entry too (F2): a Subscribe whose send failed
// must not leak its first-delta waiter until disconnect.
func (s *Server) untrack(minted protocol.CorrelationID) {
	if err := s.checkNotCopied(string(minted), map[string]any{"method": "untrack"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, minted)
	delete(s.awaiting, minted)
}

// remove drops a disconnected session from every routing table.
func (s *Server) remove(sess *session) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "remove"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sess)
	for cid, entry := range s.pending {
		if entry.sess == sess {
			delete(s.pending, cid)
		}
	}
	for cid, owner := range s.awaiting {
		if owner == sess {
			delete(s.awaiting, cid)
		}
	}
	for sub, owner := range s.subs {
		if owner == sess {
			delete(s.subs, sub)
		}
	}
}

// serveSession runs one client connection to completion: it registers
// the session, starts its write pump, and reads frames until the client
// disconnects. Each readable frame must be a protocol.Command envelope;
// valid ones are forwarded to the engine command path with the session
// registered as the pending result owner first.
func (s *Server) serveSession(conn *websocket.Conn) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "serveSession"}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "copied server")
		return
	}
	initSessionLimits(conn)
	sess := newSession(s, conn)
	if sess == nil {
		_ = conn.Close(websocket.StatusInternalError, "copied server")
		return
	}
	s.mu.Lock()
	s.sessions[sess] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.remove(sess)
		_ = conn.Close(websocket.StatusGoingAway, "server closing")
	}()

	go sess.writePump()

	for {
		_, data, err := conn.Read(s.ctx)
		if err != nil {
			return
		}
		cmd, decErr := protocol.DecodeCommand(data)
		if decErr != nil {
			sess.sendError(ErrInvalidCommandEnvelope, "", decErr.Error())
			continue
		}
		if valErr := cmd.Validate(); valErr != nil {
			sess.sendError(ErrInvalidCommandEnvelope, string(cmd.CorrelationID), valErr.Error())
			continue
		}
		clientCID := cmd.CorrelationID
		cmd.CorrelationID = s.track(clientCID, cmd.Kind, sess)
		if sendErr := s.tpt.SendCommand(cmd); sendErr != nil {
			s.untrack(cmd.CorrelationID)
			sess.sendError(ErrInvalidCommandEnvelope, string(clientCID), sendErr.Error())
		}
	}
}

// --- wire frames ---

// wireFrame is the JSON-RPC-style server->client envelope. Exactly one
// payload field is non-nil, matching Type.
type wireFrame struct {
	Type   string                  `json:"type"` // "result" | "delta" | "event" | "error"
	Result *protocol.CommandResult `json:"result,omitempty"`
	Delta  *protocol.Delta         `json:"delta,omitempty"`
	Event  *protocol.Event         `json:"event,omitempty"`
	Error  *wireError              `json:"error,omitempty"`
}

// wireError carries a registry-sourced code plus its rendered display
// string across the seam — data, never a Go error type (the same
// ErrorRef discipline protocol/envelope.go documents).
type wireError struct {
	Code          string `json:"code"`
	Display       string `json:"display"`
	CorrelationID string `json:"correlationId,omitempty"`
}

func marshalOrDrop(frame wireFrame) []byte {
	data, err := json.Marshal(frame)
	if err != nil {
		// Unreachable for these plain structs; degrade loudly per GR#1.
		errs.New(ErrInvalidCommandEnvelope, errs.NewCorrelationID(), map[string]any{"cause": err.Error()})
		return nil
	}
	return data
}

func resultFrame(r protocol.CommandResult) []byte {
	return marshalOrDrop(wireFrame{Type: "result", Result: &r})
}

func deltaFrame(d protocol.Delta) []byte {
	return marshalOrDrop(wireFrame{Type: "delta", Delta: &d})
}

func eventFrame(e protocol.Event) []byte {
	return marshalOrDrop(wireFrame{Type: "event", Event: &e})
}
