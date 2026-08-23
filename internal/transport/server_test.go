package transport

import (
	"context"
	"encoding/json"
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

// viewportPatch is this test's minimal decode of the f1.viewport wire
// schema (viewport_publish.go's own copy is compose-unexported; the
// schemaVersion/cells shape is the contract being asserted).
type viewportPatch struct {
	SchemaVersion int `json:"schemaVersion"`
	Extent        struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"extent"`
	Cells []struct {
		X       int    `json:"x"`
		Y       int    `json:"y"`
		Terrain string `json:"terrain"`
	} `json:"cells"`
}

// TestWebSocketEndToEnd_SubscribeZoneDelta spins the real composed
// engine behind a real WebSocket server and drives the S1 smoke path:
// subscribe to f1.viewport (first delta = full terrain snapshot), Buy +
// Zone a cell through protocol Command envelopes, then AdvanceTicks to
// wake the subscription pump and prove a second delta arrives.
func TestWebSocketEndToEnd_SubscribeZoneDelta(t *testing.T) {
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Fatalf("compose.Wire: %v", err)
	}

	srv, err := NewServer(engine)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	defer func() { _ = srv.Close() }()

	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// The f1.viewport snapshot is a ~2MB full-grid patch — far past
	// coder/websocket's 32KiB default read limit.
	conn.SetReadLimit(16 << 20)

	sendCommand := func(t *testing.T, cmd protocol.Command) {
		t.Helper()
		data, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("EncodeCommand(%s): %v", cmd.Kind, err)
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("Write(%s): %v", cmd.Kind, err)
		}
	}

	// readFrame pulls one server frame; readUntil loops past anything else
	// until the wanted type arrives.
	readFrame := func(t *testing.T) wireFrame {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("conn.Read: %v", err)
		}
		var f wireFrame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("frame unmarshal: %v", err)
		}
		return f
	}
	readUntil := func(t *testing.T, want string) wireFrame {
		t.Helper()
		for i := 0; i < 64; i++ {
			f := readFrame(t)
			if f.Type == want {
				return f
			}
		}
		t.Fatalf("no %q frame within 64 frames", want)
		return wireFrame{}
	}

	// 1) Subscribe to the map viewport: its first delta must be the full
	// start-tile terrain snapshot (the bind-on-first-delta path).
	subCID := errs.NewCorrelationID()
	sendCommand(t, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(subCID),
		IssuedAtTick:    0,
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "f1.viewport"},
	})
	res := readUntil(t, "result")
	if res.Result == nil || !res.Result.Accepted || string(res.Result.CorrelationID) != subCID {
		t.Fatalf("subscribe result wrong: %+v", res.Result)
	}
	first := readUntil(t, "delta")
	if first.Delta == nil {
		t.Fatal("first delta missing payload")
	}
	var patch viewportPatch
	if err := json.Unmarshal(first.Delta.Patch, &patch); err != nil {
		t.Fatalf("patch unmarshal: %v", err)
	}
	if patch.SchemaVersion != 1 || len(patch.Cells) == 0 {
		t.Fatalf("unexpected first viewport patch: schemaVersion=%d cells=%d", patch.SchemaVersion, len(patch.Cells))
	}

	// 2) Buy cell (0,0) of the start tile, then Zone it dwelling — both
	// through raw protocol envelopes on the engine's command path.
	cell := protocol.CellRef{X: 0, Y: 0}
	for _, tc := range []struct {
		kind    protocol.Kind
		payload protocol.CommandPayload
	}{
		{protocol.KindBuy, protocol.BuyPayload{Cell: cell}},
		{protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}},
	} {
		cmdCID := errs.NewCorrelationID()
		sendCommand(t, protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(cmdCID),
			IssuedAtTick:    0,
			Kind:            tc.kind,
			Payload:         tc.payload,
		})
		r := readUntil(t, "result")
		if r.Result == nil || !r.Result.Accepted {
			t.Fatalf("%s rejected: %+v", tc.kind, r.Result)
		}
	}

	// 3) Advance one sim day: handleAdvanceTicks signals the subscription
	// pump, so the subscribed session must observe another delta.
	sendCommand(t, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(errs.NewCorrelationID()),
		IssuedAtTick:    0,
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	})
	second := readUntil(t, "delta")
	if second.Delta == nil || second.Delta.SubscriptionID != first.Delta.SubscriptionID {
		t.Fatalf("post-command delta mismatch: %+v vs %+v", second.Delta, first.Delta)
	}
}

// TestWebSocketEndToEnd_MalformedFrameGetsErrorFrame proves the
// fail-loud-not-silent path: a non-Command frame comes back as a
// {"type":"error"} wire frame naming MET-P100, and the connection stays
// usable for a valid command afterwards.
func TestWebSocketEndToEnd_MalformedFrameGetsErrorFrame(t *testing.T) {
	engine := enginecore.NewEngine()
	if _, err := compose.Wire(engine, nil); err != nil {
		t.Fatalf("compose.Wire: %v", err)
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

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"not":"a command"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var f wireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Type != "error" || f.Error == nil || f.Error.Code != ErrInvalidCommandEnvelope {
		t.Fatalf("expected MET-P100 error frame, got %+v", f)
	}
}
