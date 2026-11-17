package wsserver

import (
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUG-1212: ending the transport must deliver the 1001 going-away close frame
// to a client that is reading; the server may not hard-close the TCP
// connection straight after writing the frame (a peer then sees a bare abort).

func TestBug1212_TransportCloseDeliversGoingAwayFrame(t *testing.T) {
	rec := newLifecycleRec(4)
	url, transport, closeSrv := newTransportCloseServer(t, rec)
	defer closeSrv()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}
	awaitCall(t, rec.openCh, "onOpen")

	_ = transport.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.CloseGoingAway {
		t.Fatalf("client must read a 1001 close frame, got %v", err)
	}
	awaitCall(t, rec.closeCh, "onClose")
}

// A client that never reads or answers must still be closed, bounded, with
// onClose firing exactly once.
func TestBug1212_NeverAnsweringClientIsClosedWithinBound(t *testing.T) {
	rec := newLifecycleRec(4)
	url, transport, closeSrv := newTransportCloseServer(t, rec)
	defer closeSrv()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}
	awaitCall(t, rec.openCh, "onOpen")

	_ = transport.Close() // client never reads again

	awaitCall(t, rec.closeCh, "onClose")
	time.Sleep(150 * time.Millisecond)
	if o, c := rec.counts(); o != 1 || c != 1 {
		t.Fatalf("want exactly 1 open + 1 close, got %d open / %d close", o, c)
	}
}
