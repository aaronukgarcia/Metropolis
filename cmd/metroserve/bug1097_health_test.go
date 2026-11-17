package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

// BUG-1097 AC-5: /health reports buildMismatchHandshakes, counted through
// REAL websocket handshakes (never by poking the counter).

func rawHandshake(t *testing.T, wsURL, clientVersion string) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	params, _ := json.Marshal(map[string]any{"clientVersion": clientVersion})
	id := 1
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "handshake", "params": json.RawMessage(params)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp struct {
		Error *struct{ Code string } `json:"error"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("handshake refused: %s", resp.Error.Code)
	}
}

func fetchHealthRaw(t *testing.T, h http.Handler) map[string]json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	return m
}

func TestBug1097_AC5_HealthCountsOnlyMismatchedHandshakes(t *testing.T) {
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	defer func() { _ = transport.Close() }()
	ws := wsserver.New(transport, "v0.3.0-609-g3b657a7", time.Second)
	srv := httptest.NewServer(ws)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	health := newHealthHandler("single", newHealthRegistry()).withBuildMismatch(ws.BuildMismatchHandshakes)

	// Zero at boot, and the field is ALWAYS present.
	m := fetchHealthRaw(t, health)
	raw, ok := m["buildMismatchHandshakes"]
	if !ok {
		t.Fatal("buildMismatchHandshakes missing from /health at boot")
	}
	if string(raw) != "0" {
		t.Fatalf("boot value %s, want 0", raw)
	}

	// Interleave 3 mismatched and 2 matching handshakes.
	rawHandshake(t, wsURL, "v0.3.0-609-g3b657a70")
	rawHandshake(t, wsURL, "v0.3.0-609-g3b657a7")
	rawHandshake(t, wsURL, "dev")
	rawHandshake(t, wsURL, "v0.3.0-609-g3b657a7-dirty")
	rawHandshake(t, wsURL, "v0.2.0-1-gabc1234")

	var got uint64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = json.Unmarshal(fetchHealthRaw(t, health)["buildMismatchHandshakes"], &got)
		if got >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a wrongly-counted match would land by now
	_ = json.Unmarshal(fetchHealthRaw(t, health)["buildMismatchHandshakes"], &got)
	if got != 3 {
		t.Fatalf("buildMismatchHandshakes=%d, want 3 (matching and -dirty handshakes must not count)", got)
	}
}

// With no counter source installed the field is still present and zero.
func TestBug1097_AC5_HealthFieldPresentWithoutSource(t *testing.T) {
	m := fetchHealthRaw(t, newHealthHandler("single", newHealthRegistry()))
	if string(m["buildMismatchHandshakes"]) != "0" {
		t.Fatalf("got %q, want present and 0", m["buildMismatchHandshakes"])
	}
}
