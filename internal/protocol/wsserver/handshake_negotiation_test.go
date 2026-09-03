package wsserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// handshake_negotiation_test.go — FEAT-1972079936 Phase 0 increment 1,
// AC-2: the handshake round-trips the new negotiated-version and
// capabilities shape end-to-end over a real WebSocket connection (not
// just a struct-level unit test), proving the wire framing actually
// carries the new fields correctly.

// sendHandshakeFull writes a handshake request carrying the full new
// shape (min/max version + capabilities) and returns the decoded
// response's handshakeResult alongside the raw rpcMessage.
func sendHandshakeFull(t *testing.T, conn *websocket.Conn, p handshakeParams) (rpcMessage, handshakeResult) {
	t.Helper()
	params, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal handshakeParams: %v", err)
	}
	id := int64(1)
	req := rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("handshake unexpectedly refused: %+v", resp.Error)
	}
	var result handshakeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal handshakeResult: %v", err)
	}
	return resp, result
}

// TestHandshake_NegotiatesClientCeilingBelowCurrent is AC-2's exact
// mutation: a client declaring a version one minor behind current with an
// empty capability set must get NegotiatedVersion == the CLIENT's
// declared ceiling (not silently the server's own latest), and
// Capabilities == the empty intersection.
func TestHandshake_NegotiatesClientCeilingBelowCurrent(t *testing.T) {
	original := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = original }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 1, Minor: 5}

	// BUG-636: built manually (not via newTestServer) so ServeHTTP can be
	// wrapped in a WaitGroup -- this test mutates the package-global
	// protocol.CurrentWireVersion and restores it via defer, which races
	// the server-side per-connection goroutine's reads of that same global
	// unless the test proves that goroutine has returned first. See
	// closeAndWaitForServer's doc comment in shim_test.go.
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second)
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	clientVersion := protocol.WireVersion{Major: 1, Minor: 4} // one minor behind current
	_, result := sendHandshakeFull(t, conn, handshakeParams{
		ClientVersion:    "v1.2.3",
		ClientMinVersion: &clientVersion,
		ClientMaxVersion: &clientVersion,
		Capabilities:     []string{},
	})

	if result.NegotiatedVersion != clientVersion {
		t.Fatalf("NegotiatedVersion = %+v, want the client's own ceiling %+v (not the server's %+v)",
			result.NegotiatedVersion, clientVersion, protocol.CurrentWireVersion)
	}
	if len(result.Capabilities) != 0 {
		t.Fatalf("Capabilities = %v, want an empty intersection (client declared none)", result.Capabilities)
	}

	closeAndWaitForServer(t, &srvWG, conn)
}

// TestHandshake_NoNewFields_NegotiatesCurrent proves an old client that
// predates this increment (only sends ClientVersion, no ClientMaxVersion)
// still gets served — NegotiatedVersion falls back to the server's
// current version rather than erroring on the absent field.
func TestHandshake_NoNewFields_NegotiatesCurrent(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	_, result := sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3"})

	if result.NegotiatedVersion != protocol.CurrentWireVersion {
		t.Fatalf("NegotiatedVersion = %+v, want the server's current %+v for an old-shape client",
			result.NegotiatedVersion, protocol.CurrentWireVersion)
	}
	if len(result.Capabilities) != 0 {
		t.Fatalf("Capabilities = %v, want empty (client declared none)", result.Capabilities)
	}
}

// TestHandshake_ClientAboveCurrent_FallsBackToCurrent: a client declaring
// a ceiling NEWER than this server's current version cannot be served
// something the server doesn't know how to speak — negotiation falls
// back to the server's own current, not the client's (unserviceable)
// ceiling.
func TestHandshake_ClientAboveCurrent_FallsBackToCurrent(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	future := protocol.WireVersion{Major: protocol.CurrentWireVersion.Major, Minor: protocol.CurrentWireVersion.Minor + 5}
	_, result := sendHandshakeFull(t, conn, handshakeParams{
		ClientVersion:    "v1.2.3",
		ClientMaxVersion: &future,
	})

	if result.NegotiatedVersion != protocol.CurrentWireVersion {
		t.Fatalf("NegotiatedVersion = %+v, want the server's current %+v when the client asks for something newer",
			result.NegotiatedVersion, protocol.CurrentWireVersion)
	}
}

// TestHandshake_CapabilitiesIntersection_NotUnionNorEitherSide is AC-5's
// mechanism proof (introduced in inc1's shape even though full negotiation
// is inc3 scope): server declares {A,B,C}, client declares {B,C,D} — the
// negotiated set must be exactly {B,C}, never the union ({A,B,C,D}) and
// never either side's raw set alone.
func TestHandshake_CapabilitiesIntersection_NotUnionNorEitherSide(t *testing.T) {
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second)
	srv.capabilities = []string{"A", "B", "C"}
	httpSrv := httptest.NewServer(srv)
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, wsURL)
	defer func() { _ = conn.Close() }()

	_, result := sendHandshakeFull(t, conn, handshakeParams{
		ClientVersion: "v1.2.3",
		Capabilities:  []string{"B", "C", "D"},
	})

	want := map[string]bool{"B": true, "C": true}
	if len(result.Capabilities) != len(want) {
		t.Fatalf("Capabilities = %v, want exactly {B, C}", result.Capabilities)
	}
	for _, c := range result.Capabilities {
		if !want[c] {
			t.Fatalf("Capabilities = %v contains unexpected member %q (not in either side's intersection)", result.Capabilities, c)
		}
		if c == "A" {
			t.Fatalf("Capabilities = %v must not contain server-only %q", result.Capabilities, c)
		}
		if c == "D" {
			t.Fatalf("Capabilities = %v must not contain client-only %q", result.Capabilities, c)
		}
	}
}

// TestDeterminism_SameCommandDifferentNegotiatedVersion_DecodesIdentically
// is AC-6's invariant, sliced to what increment 1 can actually prove (no
// window/shim exists yet to negotiate a genuinely different SERVED
// version — that's increment 2): two separate connections negotiate
// DIFFERENT NegotiatedVersion values (one at the server's current, one
// at an older-but-same-major ceiling), then the SAME real command is sent
// down both. The DECODED protocol.Command the transport actually
// receives — the only thing that could possibly influence engine
// state/determinism — must be byte-for-byte identical regardless of what
// was negotiated at the handshake layer, proving the negotiated version
// is consumed and "unwrapped away" at the wsserver boundary and never
// threaded into the decoded Command.
//
// False-pass guard: this asserts on the DECODED protocol.Command struct
// (what the transport/engine actually sees), not merely "both connections
// got an ack" — a shim that routed to the wrong internal command could
// still ack successfully while decoding differently, which this
// comparison would catch.
func TestDeterminism_SameCommandDifferentNegotiatedVersion_DecodesIdentically(t *testing.T) {
	original := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = original }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 1, Minor: 5}

	// BUG-636: built manually (not via newTestServer) so ServeHTTP can be
	// wrapped in a WaitGroup -- see the comment on the same pattern in
	// TestHandshake_NegotiatesClientCeilingBelowCurrent above.
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second)
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	connCurrent := dial(t, url)
	defer func() { _ = connCurrent.Close() }()
	older := protocol.WireVersion{Major: 1, Minor: 2}
	connOlder := dial(t, url)
	defer func() { _ = connOlder.Close() }()

	_, resCurrent := sendHandshakeFull(t, connCurrent, handshakeParams{ClientVersion: "v1.2.3"})
	_, resOlder := sendHandshakeFull(t, connOlder, handshakeParams{
		ClientVersion:    "v1.2.3",
		ClientMinVersion: &older,
		ClientMaxVersion: &older,
	})
	if resCurrent.NegotiatedVersion == resOlder.NegotiatedVersion {
		t.Fatalf("test setup invalid: both connections negotiated the SAME version %+v — this test needs them to differ", resCurrent.NegotiatedVersion)
	}

	sendSameCommand := func(conn *websocket.Conn, id int64) {
		t.Helper()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   "det-corr",
			IssuedAtTick:    42,
			Kind:            protocol.KindPause,
			Payload:         protocol.PausePayload{},
		}
		cmdBytes, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("encode command: %v", err)
		}
		if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
			t.Fatalf("write command: %v", err)
		}
	}

	sendSameCommand(connCurrent, 10)
	sendSameCommand(connOlder, 11)

	recv := func() protocol.Command {
		t.Helper()
		select {
		case got := <-transport.Commands():
			return got
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for command to reach the transport")
			return protocol.Command{}
		}
	}

	gotFromCurrent := recv()
	gotFromOlder := recv()

	// Order across the two connections' goroutines is not guaranteed;
	// both must have the SAME CorrelationID/Kind/Payload/ProtocolVersion/
	// IssuedAtTick regardless of which arrived first, since both sent an
	// identical Command. Compared field-by-field (not `!=` on the whole
	// struct) because Payload is a CommandPayload interface value — safer
	// to assert its concrete type explicitly than rely on `!=` across an
	// interface field whose underlying representation this test should
	// not need to know precisely to make its point.
	if gotFromCurrent.ProtocolVersion != gotFromOlder.ProtocolVersion ||
		gotFromCurrent.CorrelationID != gotFromOlder.CorrelationID ||
		gotFromCurrent.IssuedAtTick != gotFromOlder.IssuedAtTick ||
		gotFromCurrent.Kind != gotFromOlder.Kind {
		t.Fatalf("decoded Command differs by negotiated version: from-current=%+v from-older=%+v — the negotiated wire version must never leak into the decoded Command", gotFromCurrent, gotFromOlder)
	}
	if _, ok := gotFromCurrent.Payload.(protocol.PausePayload); !ok {
		t.Fatalf("expected protocol.PausePayload, got %T", gotFromCurrent.Payload)
	}
	if _, ok := gotFromOlder.Payload.(protocol.PausePayload); !ok {
		t.Fatalf("expected protocol.PausePayload, got %T", gotFromOlder.Payload)
	}

	closeAndWaitForServer(t, &srvWG, connCurrent, connOlder)
}
