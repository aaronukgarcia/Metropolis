package wsserver

import (
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1209: when the transport ends (city unload), the server must close the
// socket and fire onClose exactly once; before the fix the inbound loop stayed
// blocked in ReadMessage and neither happened.

func newTransportCloseServer(t *testing.T, rec *lifecycleRec) (string, *protocol.InProcTransport, func()) {
	t.Helper()
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithConnectionLifecycle(rec.onOpen, rec.onClose))
	httpSrv := httptest.NewServer(srv)
	return "ws" + strings.TrimPrefix(httpSrv.URL, "http"), transport, httpSrv.Close
}

func settledGoroutines(base int, within time.Duration) int {
	deadline := time.Now().Add(within)
	n := runtime.NumGoroutine()
	for n > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

func TestBug1209_TransportCloseClosesSocketAndFiresOnCloseOnce(t *testing.T) {
	rec := newLifecycleRec(8)
	before := runtime.NumGoroutine()
	url, transport, closeSrv := newTransportCloseServer(t, rec)

	conn := dial(t, url)
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}
	awaitCall(t, rec.openCh, "onOpen")

	_ = transport.Close() // the city unloads: transport ends under a live client

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatalf("server never closed the socket after the transport ended: %v", err)
			}
			break
		}
	}
	awaitCall(t, rec.closeCh, "onClose")
	time.Sleep(100 * time.Millisecond)
	if o, c := rec.counts(); o != 1 || c != 1 {
		t.Fatalf("want exactly 1 open + 1 close, got %d open / %d close", o, c)
	}

	_ = conn.Close()
	closeSrv()
	if n := settledGoroutines(before, 3*time.Second); n > before {
		t.Fatalf("goroutine leak: %d before, %d after", before, n)
	}
}

// Client close and transport close racing: onClose still fires exactly once
// per connection, under -race.
func TestBug1209_ClientCloseRacesTransportClose(t *testing.T) {
	const n = 30
	rec := newLifecycleRec(2 * n)
	url, transport, closeSrv := newTransportCloseServer(t, rec)
	defer closeSrv()

	conns := make([]*websocket.Conn, n)
	for i := range conns {
		conns[i] = dial(t, url)
		if resp := sendHandshake(t, conns[i], "v1.2.3"); resp.Error != nil {
			t.Fatalf("handshake refused: %+v", resp.Error)
		}
		awaitCall(t, rec.openCh, "onOpen")
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, c := range conns {
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			<-start
			_ = c.Close()
		}(c)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_ = transport.Close()
	}()
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		awaitCall(t, rec.closeCh, "onClose")
	}
	time.Sleep(150 * time.Millisecond)
	if o, c := rec.counts(); o != n || c != n {
		t.Fatalf("want exactly %d open + %d close, got %d open / %d close", n, n, o, c)
	}
}
