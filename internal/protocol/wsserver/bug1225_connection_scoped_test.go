package wsserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// scopedTransport counts ConnectionEnded calls (BUG-1225).
type scopedTransport struct {
	protocol.Transport
	ended *atomic.Int32
}

func (s *scopedTransport) ConnectionEnded() { s.ended.Add(1) }

// BUG-1225: a handshake that fails AFTER the resolver succeeded (reply write
// deadline) must still end the connection-scoped transport exactly once, with
// no lifecycle hook fired; a normal connection ends it exactly once on close.
func TestBug1225_ConnectionScopedEndedOnEveryExitPath(t *testing.T) {
	const writeDeadline = 400 * time.Millisecond
	caps := make([]string, 3000)
	for i := range caps {
		caps[i] = fmt.Sprintf("cap-%05d-%s", i, strings.Repeat("x", 4000))
	}
	var ended, resolves atomic.Int32
	base := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	rec := newLifecycleRec(8)
	srv := New(base, "v1.2.3", 30*time.Second,
		WithHandshakeWriteTimeout(writeDeadline),
		WithCapabilities(caps),
		WithTransportResolver(func(tenant, city string) (protocol.Transport, error) {
			resolves.Add(1)
			return &scopedTransport{Transport: base, ended: &ended}, nil
		}),
		WithConnectionLifecycle(rec.onOpen, rec.onClose))
	httpSrv := httptest.NewServer(srv)
	defer func() { httpSrv.Close(); _ = base.Close() }()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	dialer := websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			c, err := net.Dial(network, addr)
			if err == nil {
				if tc, ok := c.(*net.TCPConn); ok {
					_ = tc.SetReadBuffer(1024)
				}
			}
			return c, err
		},
	}
	stalled, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = stalled.Close() }()
	params, _ := json.Marshal(handshakeParams{ClientVersion: "v1.2.3", Capabilities: caps})
	id := int64(1)
	if err := stalled.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if resolves.Load() != 1 || ended.Load() != 1 {
		t.Fatalf("failed handshake: resolves=%d ended=%d, want 1/1", resolves.Load(), ended.Load())
	}
	if o, c := rec.counts(); o != 0 || c != 0 {
		t.Fatalf("hooks fired on failed handshake: %d/%d", o, c)
	}

	// Normal connection: ended once, after the close.
	good := dial(t, wsURL)
	if resp := sendHandshake(t, good, "v1.2.3"); resp.Error != nil {
		t.Fatalf("refused: %+v", resp.Error)
	}
	awaitCall(t, rec.openCh, "onOpen")
	_ = good.Close()
	awaitCall(t, rec.closeCh, "onClose")
	time.Sleep(100 * time.Millisecond)
	if ended.Load() != 2 {
		t.Fatalf("normal close: ended=%d, want 2 in total", ended.Load())
	}
}
