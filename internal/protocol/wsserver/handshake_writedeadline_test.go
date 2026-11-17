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

// BUG-1204: the handshake reply write must carry a deadline. A peer that
// completes the upgrade and sends a valid handshake but never reads (tiny
// receive buffer + a reply far larger than the socket buffers) must fail the
// write inside the deadline, fire NO lifecycle hooks, and leave the server
// able to serve the next client.
func TestHandshake_StalledReplyWriteFailsWithinDeadline(t *testing.T) {
	const writeDeadline = 400 * time.Millisecond
	// ~12 MB reply: 3000 caps x 4 KB each. Far above any loopback buffering.
	caps := make([]string, 3000)
	for i := range caps {
		caps[i] = fmt.Sprintf("cap-%05d-%s", i, strings.Repeat("x", 4000))
	}

	rec := newLifecycleRec(8)
	var resolves atomic.Int32
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", 30*time.Second,
		WithHandshakeWriteTimeout(writeDeadline),
		WithCapabilities(caps),
		WithTransportResolver(func(tenant, city string) (protocol.Transport, error) {
			resolves.Add(1)
			return transport, nil
		}),
		WithConnectionLifecycle(rec.onOpen, rec.onClose))
	httpSrv := httptest.NewServer(srv)
	defer func() { httpSrv.Close(); _ = transport.Close() }()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	// Stalled client: tiny receive buffer, sends the handshake, never reads.
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

	// Wait past the write deadline, then drain raw bytes: a server that gave
	// up closes its side (EOF/reset reaches us once the buffered bytes are
	// consumed); a server still blocked in the write would leave the drain
	// idle until our own timeout.
	for i := 0; i < 400 && resolves.Load() == 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(writeDeadline + 600*time.Millisecond)
	if resolves.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolves.Load())
	}
	raw := stalled.UnderlyingConn()
	_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 1<<20)
	closed := false
	for i := 0; i < 200; i++ {
		if _, rerr := raw.Read(buf); rerr != nil {
			if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
				break
			}
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("server did not close the stalled connection after the write deadline")
	}

	if o, c := rec.counts(); o != 0 || c != 0 {
		t.Fatalf("failed reply write fired hooks: %d open / %d close", o, c)
	}

	// A second, well-behaved client is served normally afterwards.
	good := dial(t, wsURL)
	defer func() { _ = good.Close() }()
	if resp := sendHandshake(t, good, "v1.2.3"); resp.Error != nil {
		t.Fatalf("second client refused: %+v", resp.Error)
	}
	awaitCall(t, rec.openCh, "onOpen (second client)")
}
