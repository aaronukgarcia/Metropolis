package wsserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestCommandRoundTrip_RealEngine_AcceptAndReject is FEAT-1972079852
// increment 2's item 2: it proves the WHOLE path from an inbound WS
// "command" frame through to a REAL, composed *core.Engine
// (compose.Wire, the same composition root cmd/metroserve and
// cmd/metropolis use) and back out as an asynchronous "result"
// notification carrying the engine's own accept/reject decision — not
// just "the frame reached the wrapped Transport's channel"
// (TestCommandRoundTrip_AfterAcceptedHandshake, above, already proved
// that narrower claim for increment 1).
//
// SetSpeed is the chosen command (per the inc2 build brief's "the
// simplest safe one"): it needs no gameplayHandler/build-module wiring
// to exercise both outcomes engine.core itself already implements —
// SetSpeed(2) is accepted, SetSpeed(99) is rejected with
// core.ErrInvalidSpeed (commands.go's handleSetSpeed) — so this test
// proves both the accept AND the reject leg reach a real engine and
// come back with the engine's own decision, not a stub's.
//
// NOTE on journaling (inc2 item 3, Aaron's engine-owns-journal DD): this
// test does NOT assert the command was journaled Go-side. The Go
// journaling seam (internal/harness/replay.Recorder) has no registered
// code.json edge from int.protocol/engine.core/cmd.metroserve today —
// only harness.replay -> int.protocol (the reverse direction) is
// registered. Per GR#25, adding that call here without first getting
// the edge registered would be an unregistered-dependency violation, so
// this increment deliberately stops short of it — see the build
// report's "what inc3 owes" note.
func TestCommandRoundTrip_RealEngine_AcceptAndReject(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1))
	if _, err := compose.Wire(e, nil); err != nil {
		t.Fatalf("compose.Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pumpDone, err := e.StartSubscriptionPump(ctx, transport)
	if err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	loopDone := make(chan error, 1)
	go func() { loopDone <- e.RunCommandLoop(ctx, transport) }()

	srv := New(transport, "v1.2.3", time.Second)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, wsURL)
	defer func() { _ = conn.Close() }()
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake failed: %+v", resp.Error)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	sendSetSpeed := func(id int64, speed int) rpcMessage {
		t.Helper()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(t.Name() + "-" + time.Now().Format(time.RFC3339Nano)),
			Kind:            protocol.KindSetSpeed,
			Payload:         protocol.SetSpeedPayload{Speed: speed},
		}
		cmdBytes, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("encode command: %v", err)
		}
		if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
			t.Fatalf("write command: %v", err)
		}
		var ack rpcMessage
		if err := conn.ReadJSON(&ack); err != nil {
			t.Fatalf("read ack: %v", err)
		}
		if ack.Error != nil {
			t.Fatalf("expected ack, got error %+v", ack.Error)
		}
		return waitForResult(t, conn, cmd.CorrelationID)
	}

	t.Run("accepted", func(t *testing.T) {
		resultMsg := sendSetSpeed(10, 2)
		var res protocol.CommandResult
		if err := json.Unmarshal(resultMsg.Params, &res); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if !res.Accepted {
			t.Fatalf("expected the real engine to ACCEPT SetSpeed(2), got rejected: %+v", res.Error)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		resultMsg := sendSetSpeed(11, 99)
		var res protocol.CommandResult
		if err := json.Unmarshal(resultMsg.Params, &res); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected the real engine to REJECT an invalid SetSpeed(99), got accepted")
		}
		if res.Error == nil || res.Error.Code == "" {
			t.Fatalf("expected a registry-coded rejection (GR#7), got %+v", res.Error)
		}
	})

	cancel()
	<-loopDone
	<-pumpDone
	_ = transport.Close()
}

// waitForResult reads frames off conn until it finds a "result"
// notification whose CorrelationID matches want, or the read deadline
// (already set by the caller) fires. Delta/event notifications for
// other subscriptions are possible in principle and are skipped, not
// treated as failures.
func waitForResult(t *testing.T, conn *websocket.Conn, want protocol.CorrelationID) rpcMessage {
	t.Helper()
	for {
		var msg rpcMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for result notification (correlation %s): %v", want, err)
		}
		if msg.Method != methodResult {
			continue
		}
		var res protocol.CommandResult
		if err := json.Unmarshal(msg.Params, &res); err != nil {
			t.Fatalf("decode result notification: %v", err)
		}
		if res.CorrelationID != want {
			continue
		}
		return msg
	}
}
