package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// F1 regression: two sessions must not be able to steal each other's
// routing by supplying identical client CorrelationIDs. Both dial, both
// Subscribe to f1.viewport with the SAME correlationId; each must get
// its own accepted result AND its own first delta. (Pre-fix,
// last-writer-wins on the pending/awaiting maps meant session B's track
// overwrote A's entries and A starved.)
func TestWebSocketEndToEnd_CorrelationCollisionDoesNotSteal(t *testing.T) {
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Skipf("compose unavailable: %v", err)
	}
	srv, err := NewServer(engine)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	defer func() { _ = srv.Close() }()

	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			t.Fatalf("websocket.Dial: %v", err)
		}
		conn.SetReadLimit(16 << 20)
		return conn
	}
	readUntil := func(name string, conn *websocket.Conn) wireFrame {
		t.Helper()
		for i := 0; i < 64; i++ {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("[%s] conn.Read: %v", name, err)
			}
			var f wireFrame
			if err := json.Unmarshal(data, &f); err != nil {
				t.Fatalf("[%s] frame unmarshal: %v", name, err)
			}
			if f.Type == "result" || f.Type == "delta" {
				return f
			}
		}
		t.Fatalf("[%s] no result/delta within 64 frames", name)
		return wireFrame{}
	}

	a := dial()
	defer func() { _ = a.CloseNow() }()
	b := dial()
	defer func() { _ = b.CloseNow() }()

	const stolen = "collision-cid"
	for _, c := range []*websocket.Conn{a, b} {
		data, err := protocol.EncodeCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(stolen),
			IssuedAtTick:    0,
			Kind:            protocol.KindSubscribe,
			Payload:         protocol.SubscribePayload{ViewName: "f1.viewport"},
		})
		if err != nil {
			t.Fatalf("EncodeCommand: %v", err)
		}
		if err := c.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	for name, c := range map[string]*websocket.Conn{"A": a, "B": b} {
		res := readUntil(name, c)
		if res.Type != "result" || res.Result == nil || !res.Result.Accepted ||
			string(res.Result.CorrelationID) != stolen {
			t.Fatalf("[%s] expected accepted subscribe result echoing %q, got %+v", name, stolen, res)
		}
		delta := readUntil(name, c)
		if delta.Type != "delta" || delta.Delta == nil {
			t.Fatalf("[%s] expected first delta, got %+v", name, delta)
		}
	}
}

// F2 regression: rejected Subscribes must clean their awaiting entry at
// result-delivery time, not wait for disconnect. N rejected subscribes
// leave len(awaiting) == 0.
func TestAwaitingCleanedOnRejectedSubscribe(t *testing.T) {
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Skipf("compose unavailable: %v", err)
	}
	srv, err := NewServer(engine)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	defer func() { _ = srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	const n = 8
	for i := 0; i < n; i++ {
		data, err := protocol.EncodeCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(errs.NewCorrelationID()),
			IssuedAtTick:    0,
			Kind:            protocol.KindSubscribe,
			Payload:         protocol.SubscribePayload{ViewName: "no.such.view"},
		})
		if err != nil {
			t.Fatalf("EncodeCommand: %v", err)
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("conn.Read(%d): %v", i, err)
		}
		var f wireFrame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		if f.Type != "result" || f.Result == nil || f.Result.Accepted {
			t.Fatalf("frame %d: expected rejected result, got %+v", i, f)
		}
	}

	srv.mu.Lock()
	got := len(srv.awaiting)
	srv.mu.Unlock()
	if got != 0 {
		t.Fatalf("awaiting holds %d leaked entries after %d rejected subscribes, want 0", got, n)
	}
}

// F3 regression: the outbound slow-consumer budget is bytes, not frames.
// A session whose write pump cannot keep up (here: none is running) must
// trip MET-P102 once the queued BYTES pass the ceiling, well before the
// old 256-frame channel cap could. Deterministic: the session under test
// has no writePump draining it, so every send stays queued.
func TestOutboundByteBudgetEvictsSlowConsumer(t *testing.T) {
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Skipf("compose unavailable: %v", err)
	}
	srv, err := NewServer(engine)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	connCh := make(chan *websocket.Conn, 1)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connCh <- c
		select {} // hold the handler open; the test closes the conn directly
	}))
	defer hs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer func() { _ = clientConn.CloseNow() }()
	clientConn.SetReadLimit(64 << 20)

	var serverConn *websocket.Conn
	select {
	case serverConn = <-connCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server-side conn never accepted")
	}

	sess := newSession(srv, serverConn)
	if sess == nil {
		t.Fatal("newSession failed")
	}
	closed := make(chan struct{})
	go func() {
		for {
			if _, _, err := clientConn.Read(ctx); err != nil {
				close(closed)
				return
			}
		}
	}()

	frame := make([]byte, 2<<20) // 2MB, viewport-delta sized
	// Old budget: 256 frames of headroom — 200 sends cannot close it.
	// Byte budget (~16MB): closed within the first dozen sends.
	for i := 0; i < 200; i++ {
		select {
		case <-closed:
			return // evicted as a slow consumer — pass
		default:
		}
		sess.send(frame)
	}
	select {
	case <-closed:
		return
	case <-time.After(2 * time.Second):
		t.Fatalf("connection survived %d x 2MB queued sends (%d bytes) without slow-consumer eviction", 200, 200*(2<<20))
	}
}
