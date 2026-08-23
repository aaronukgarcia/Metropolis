package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// dialBareSession hands back the server-side session built directly on
// an accepted WebSocket pair (no writePump started, no handler loops)
// plus the client end, so tests can drive send/writePump and drain
// themselves. Mirrors the F3 harness in security_round2_test.go.
func dialBareSession(t *testing.T) (*Server, *session, *websocket.Conn, func()) {
	t.Helper()
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Skipf("compose unavailable: %v", err)
	}
	srv, err := NewServer(engine)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	connCh := make(chan *websocket.Conn, 1)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connCh <- c
		select {} // hold the handler open; cleanup closes the conn
	}))
	var serverConn *websocket.Conn
	cleanup := func() {
		if serverConn != nil {
			_ = serverConn.CloseNow()
		}
		hs.Close()
		_ = srv.Close()
	}
	clientCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	clientConn, _, err := websocket.Dial(clientCtx, "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		cleanup()
		t.Fatalf("websocket.Dial: %v", err)
	}
	select {
	case serverConn = <-connCh:
	case <-time.After(5 * time.Second):
		_ = clientConn.CloseNow()
		cancel()
		cleanup()
		t.Fatal("server-side conn never accepted")
	}
	sess := newSession(srv, serverConn)
	if sess == nil {
		_ = clientConn.CloseNow()
		cancel()
		cleanup()
		t.Fatal("newSession failed")
	}
	return srv, sess, clientConn, cleanup
}

// F-N1 regression: outBytes is a precise ledger of the bytes currently
// sitting in the outbound queue. A stream of N frames that is FULLY
// drained by the write pump must leave outBytes == 0 — any residue means
// the add/subtract pairing drifted and the MET-P102 byte ceiling is now
// judging the client on phantom bytes. The pairing only stays balanced
// if each frame's addition is ordered BEFORE its enqueue (so the pump's
// subtraction can never run ahead of the credit it reverses); that
// ordering is what this test pins.
func TestOutBytesZeroAfterFullyDrainedStream(t *testing.T) {
	_, sess, clientConn, cleanup := dialBareSession(t)
	defer cleanup()

	// Stream length stays under the outboundBuffer frame cap (256) so
	// every send lands in the queue and the run exercises pure ledger
	// accounting — an overflow would legitimately evict the session
	// mid-stream and strand the tail's debits.
	const n = 200
	frame := make([]byte, 2048)

	// Drain synchronization: the pump debits each frame BEFORE writing
	// it, so once the client has received all n frames every credit and
	// every debit has executed and the ledger must be exactly zero.
	received := make(chan struct{}, n)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for i := 0; i < n; i++ {
			if _, _, err := clientConn.Read(ctx); err != nil {
				return
			}
			received <- struct{}{}
		}
	}()

	go sess.writePump()
	for i := 0; i < n; i++ {
		sess.send(frame)
	}
	for i := 0; i < n; i++ {
		select {
		case <-received:
		case <-time.After(10 * time.Second):
			t.Fatalf("client received only %d of %d frames", i, n)
		}
	}

	if got := sess.outBytes.Load(); got != 0 {
		t.Fatalf("fully drained %d-frame stream left outBytes=%d, want 0 (accounting drift)", n, got)
	}
}

// F-N2 regression: once the session has begun closing (budget tripped,
// buffer overflowed, or closeNow otherwise invoked) send must reject
// immediately — every frame accepted past the eviction decision queues
// more bytes onto a socket that is already being torn down and re-logs
// ErrSlowConsumer for a verdict already rendered.
func TestSendRejectedOnceClosing(t *testing.T) {
	_, sess, _, cleanup := dialBareSession(t)
	defer cleanup()

	sess.closeNow()
	frame := make([]byte, 128)
	for i := 0; i < 10; i++ {
		sess.send(frame)
	}
	if got := len(sess.out); got != 0 {
		t.Fatalf("%d frames enqueued after closeNow began closing, want 0 (send must reject immediately)", got)
	}
	if got := sess.outBytes.Load(); got != 0 {
		t.Fatalf("outBytes=%d after post-close sends were rejected, want 0", got)
	}
}
