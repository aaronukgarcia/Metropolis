package wsserver

// BUG-1204 r1 (sonnet-round-r1-bug1204): the handshake write deadline must be
// CLEARED after the reply, or every later pump write to a long-lived client
// would fail once the deadline passes. The builder's suite could not detect a
// forgotten clear; this test, adopted from the round, does: with a 300 ms
// handshake write deadline, a normal client must still receive deltas sent
// well after that deadline has elapsed.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestHandshakeWriteDeadline_ClearedForLongLivedClient(t *testing.T) {
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", 30*time.Second, WithHandshakeWriteTimeout(300*time.Millisecond))
	hs := httptest.NewServer(srv)
	defer func() { hs.Close(); _ = transport.Close() }()
	conn := dial(t, "ws"+strings.TrimPrefix(hs.URL, "http"))
	defer func() { _ = conn.Close() }()
	if r := sendHandshake(t, conn, "v1.2.3"); r.Error != nil {
		t.Fatal(r.Error)
	}
	// Wait well past the 300 ms handshake write deadline before the pump writes.
	time.Sleep(1200 * time.Millisecond)
	for i := 1; i <= 3; i++ {
		if !transport.SendDelta(protocol.Delta{SubscriptionID: "s", Tick: 5, Seq: uint64(i), Patch: json.RawMessage(`{"x":1}`)}) {
			t.Fatal("send")
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var m rpcMessage
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("delta %d after the handshake write deadline: %v", i, err)
		}
		time.Sleep(400 * time.Millisecond)
	}
}
