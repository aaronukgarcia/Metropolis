package wsserver

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// capability_gating_test.go — FEAT-1972079936 Phase 0 increment 3, AC-5:
// fine-grained capability-gated command ENFORCEMENT (not just the
// intersection mechanism, which handshake_negotiation_test.go's
// TestHandshake_CapabilitiesIntersection_NotUnionNorEitherSide already
// covers). This file proves the client-facing outcome: a connection that
// never negotiated a Kind's required capability cannot invoke it, while a
// connection that did, can.

// sendDebugCommand writes a well-formed KindDebug command and returns the
// decoded ack/error response.
func sendDebugCommand(t *testing.T, conn *websocket.Conn, id int64) rpcMessage {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("debug-corr"),
		IssuedAtTick:    0,
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: "noop"},
	}
	cmdBytes, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode debug command: %v", err)
	}
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
		t.Fatalf("write debug command: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read command response: %v", err)
	}
	return resp
}

// TestCapabilityGate_KindDebug_RefusedWithoutCapability proves a
// connection that did NOT negotiate protocol.CapDebugCommands cannot
// invoke KindDebug -- the server declares it by default
// (defaultServerCapabilities) but this client deliberately declares an
// EMPTY capability set, so the negotiated (intersected) set is empty.
func TestCapabilityGate_KindDebug_RefusedWithoutCapability(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	_, result := sendHandshakeFull(t, conn, handshakeParams{
		ClientVersion: "v1.2.3",
		Capabilities:  []string{}, // deliberately does NOT declare CapDebugCommands
	})
	if len(result.Capabilities) != 0 {
		t.Fatalf("test setup invalid: expected an empty negotiated set, got %v", result.Capabilities)
	}

	resp := sendDebugCommand(t, conn, 2)
	if resp.Error == nil {
		t.Fatal("expected KindDebug to be refused on a connection that never negotiated CapDebugCommands")
	}
	if resp.Error.Code != protocol.ErrCapabilityRequired {
		t.Fatalf("expected code %s, got %s (%+v)", protocol.ErrCapabilityRequired, resp.Error.Code, resp.Error)
	}
	if resp.Error.Data["capability"] != protocol.CapDebugCommands {
		t.Fatalf("expected error context capability=%q, got %+v", protocol.CapDebugCommands, resp.Error.Data)
	}
}

// TestCapabilityGate_KindDebug_AllowedWithCapability is the other half of
// AC-5's mutation: the SAME command, on a connection that DID declare
// (and thus negotiate, since the server also declares it by default)
// CapDebugCommands, must be accepted and actually reach the transport --
// proving the gate depends on the NEGOTIATED set, not a blanket refusal
// of KindDebug regardless of capability.
func TestCapabilityGate_KindDebug_AllowedWithCapability(t *testing.T) {
	url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	_, result := sendHandshakeFull(t, conn, handshakeParams{
		ClientVersion: "v1.2.3",
		Capabilities:  []string{protocol.CapDebugCommands},
	})
	if len(result.Capabilities) != 1 || result.Capabilities[0] != protocol.CapDebugCommands {
		t.Fatalf("test setup invalid: expected negotiated capabilities [%q], got %v", protocol.CapDebugCommands, result.Capabilities)
	}

	resp := sendDebugCommand(t, conn, 2)
	if resp.Error != nil {
		t.Fatalf("expected KindDebug to be accepted on a connection that negotiated CapDebugCommands, got error %+v", resp.Error)
	}

	select {
	case got := <-transport.Commands():
		if got.Kind != protocol.KindDebug {
			t.Fatalf("expected the Debug command to reach the transport, got Kind %q", got.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the allowed Debug command to reach the transport")
	}
}

// TestCapabilityGate_ServerDoesNotDeclareCapability_StillRefused proves the
// gate is a true intersection, not merely "did the client ask for it": a
// server constructed WITHOUT CapDebugCommands in its own declared set
// refuses KindDebug even for a client that DID declare it -- the feature
// requires BOTH sides, per AC-5's own definition ("intersection... never
// either side's raw set alone").
func TestCapabilityGate_ServerDoesNotDeclareCapability_StillRefused(t *testing.T) {
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithCapabilities(nil))
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
		Capabilities:  []string{protocol.CapDebugCommands},
	})
	if len(result.Capabilities) != 0 {
		t.Fatalf("test setup invalid: expected an empty negotiated set (server declares none), got %v", result.Capabilities)
	}

	resp := sendDebugCommand(t, conn, 2)
	if resp.Error == nil {
		t.Fatal("expected KindDebug to be refused when the SERVER never declared CapDebugCommands, regardless of the client's own declaration")
	}
	if resp.Error.Code != protocol.ErrCapabilityRequired {
		t.Fatalf("expected code %s, got %s", protocol.ErrCapabilityRequired, resp.Error.Code)
	}
}

// TestCapabilityGate_UngatedKind_UnaffectedByEmptyCapabilities is the
// false-pass guard: an ordinary, ungated command (Pause) must keep working
// on a connection with an EMPTY negotiated capability set -- proving the
// gate is scoped to Kinds that actually require a capability, not a
// blanket "no capabilities negotiated => refuse everything" regression.
func TestCapabilityGate_UngatedKind_UnaffectedByEmptyCapabilities(t *testing.T) {
	url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3", Capabilities: []string{}})

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "pause-corr",
		IssuedAtTick:    0,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	cmdBytes, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode pause command: %v", err)
	}
	id := int64(2)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
		t.Fatalf("write pause command: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read command response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("expected an ungated command to succeed regardless of the empty negotiated capability set, got error %+v", resp.Error)
	}

	select {
	case got := <-transport.Commands():
		if got.Kind != protocol.KindPause {
			t.Fatalf("expected the Pause command to reach the transport, got Kind %q", got.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the ungated command to reach the transport")
	}
}
