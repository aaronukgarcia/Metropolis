package transport

import (
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// session is one connected WebSocket client: its read loop runs on the
// HTTP handler goroutine (serveSession), its write pump on its own
// goroutine, and every engine-originated frame it receives is queued
// through send's bounded buffer.
//
// Every method identity-checks sess.srv (the SEC-020 discipline the
// Server type carries): a session is always constructed against the one
// *Server NewServer returned, and a copied Server must never be reached,
// even transitively through a session.
type session struct {
	srv  *Server
	conn *websocket.Conn
	out  chan []byte

	// outBytes tracks the queued outbound payload against the server's
	// byte ceiling (outboundByteBudget): the frame budget alone admits
	// ~512MB of ~2MB viewport deltas before a slow consumer is noticed.
	// Written by send and drained by writePump — hence atomic, not
	// mu-guarded. The pairing is ordered so the ledger stays exact:
	// send credits each frame BEFORE enqueueing it (the channel's
	// happens-before edge then guarantees the pump's debit for that
	// frame can never run ahead of its credit), and debits happen only
	// where a credited frame fails to reach the queue. Every credited
	// byte therefore ends up either queued-and-debited-on-drain or
	// dropped-and-debited-at-drop — no interleaving leaves residue.
	outBytes atomic.Int64

	// closing flips true the moment closeNow begins tearing the session
	// down. send checks it so a connection already condemned by the
	// byte-budget verdict stops accepting frames immediately instead of
	// queueing up to the frame cap past the eviction decision.
	closing atomic.Bool

	writeOnce sync.Once
}

func newSession(srv *Server, conn *websocket.Conn) *session {
	if err := srv.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "newSession"}); err != nil {
		return nil
	}
	return &session{
		srv:  srv,
		conn: conn,
		out:  make(chan []byte, outboundBuffer),
	}
}

// send queues a pre-marshalled frame for the write pump without ever
// blocking. An overflowing buffer means the client cannot keep up with
// the delta stream, so the connection is closed (ErrSlowConsumer) — the
// same "never stall the pump for a slow reader" reasoning as
// InProcTransport's evict-oldest policy, applied at the session boundary.
func (sess *session) send(frame []byte) {
	if frame == nil {
		return
	}
	// The correlation ID minted below is bookkeeping for logs and error
	// display only, never a capability: possessing or guessing it grants
	// no authority over this session or the engine.
	if err := sess.srv.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "send"}); err != nil {
		return
	}
	if sess.closing.Load() {
		return
	}
	// Credit the ledger before the frame is visible to the pump: once
	// the frame is in the channel, a concurrent dequeue may debit it at
	// any instant, and the credit must already be on the books for that
	// debit to reverse.
	n := sess.outBytes.Add(int64(len(frame)))
	select {
	case sess.out <- frame:
	default:
		sess.outBytes.Add(-int64(len(frame)))
		errs.New(ErrSlowConsumer, errs.NewCorrelationID(), map[string]any{})
		sess.closeNow()
		return
	}
	if n > outboundByteBudget {
		errs.New(ErrSlowConsumer, errs.NewCorrelationID(), map[string]any{
			"queuedBytes": n,
			"budget":      outboundByteBudget,
		})
		sess.closeNow()
	}
}

// writePump drains the outbound buffer onto the socket until the server
// context is cancelled or writes start failing.
func (sess *session) writePump() {
	if err := sess.srv.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "writePump"}); err != nil {
		return
	}
	for {
		select {
		case <-sess.srv.ctx.Done():
			return
		case frame := <-sess.out:
			sess.outBytes.Add(-int64(len(frame)))
			if err := sess.conn.Write(sess.srv.ctx, websocket.MessageText, frame); err != nil {
				return
			}
		}
	}
}

// sendError emits an {"type":"error"} frame naming a registry code and
// its rendered display string. correlationID may be empty when no
// envelope was decodable at all.
func (sess *session) sendError(code, correlationID, cause string) {
	if err := sess.srv.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "sendError"}); err != nil {
		return
	}
	e := errs.New(code, errs.NewCorrelationID(), map[string]any{"cause": cause})
	sess.send(marshalOrDrop(wireFrame{Type: "error", Error: &wireError{
		Code:          code,
		Display:       e.Display(),
		CorrelationID: correlationID,
	}}))
}

// closeNow force-closes the socket exactly once; the read loop observes
// it as a Read error and unwinds via serveSession's deferred cleanup.
func (sess *session) closeNow() {
	sess.writeOnce.Do(func() {
		if err := sess.srv.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "closeNow"}); err != nil {
			return
		}
		sess.closing.Store(true)
		_ = sess.conn.CloseNow()
	})
}
