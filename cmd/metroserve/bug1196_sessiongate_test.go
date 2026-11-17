package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

// BUG-1196: a city serves one live connection at a time until session
// fan-out exists. These tests drive the REAL hosted wiring (hostedWSOptions,
// the exact option set runHosted installs) over real WebSockets.

func newGatedHostedServer(t *testing.T) string {
	t.Helper()
	host := newTestHost(t, "")
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	return "ws" + strings.TrimPrefix(httpSrv.URL, "http")
}

// dialHandshake dials and handshakes naming cityID; it returns the open
// connection (caller closes) and the refusal code ("" when accepted).
func dialHandshake(t *testing.T, url, cityID string) (*websocket.Conn, string) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hs, _ := json.Marshal(map[string]any{"clientVersion": buildinfo.Version, "tenantId": persistTenantID, "cityId": cityID})
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": int64(1), "method": "handshake", "params": json.RawMessage(hs)}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	var resp struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake reply: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if resp.Error != nil {
		return conn, resp.Error.Code
	}
	return conn, ""
}

// setSpeedRoundTrip proves conn is still served by the engine: SetSpeed(2)
// is accepted and its CommandResult comes back on the connection.
func setSpeedRoundTrip(t *testing.T, conn *websocket.Conn, tag string) {
	t.Helper()
	corr := protocol.CorrelationID(t.Name() + "-" + tag)
	b, err := protocol.EncodeCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corr,
		Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": int64(2), "method": "command", "params": json.RawMessage(b)}); err != nil {
		t.Fatalf("write command: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for {
		var msg struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("first connection stopped receiving after the second was refused: %v", err)
		}
		if msg.Method != "result" {
			continue
		}
		var res protocol.CommandResult
		if err := json.Unmarshal(msg.Params, &res); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if res.CorrelationID == corr {
			if !res.Accepted {
				t.Fatalf("first connection's command rejected: %+v", res.Error)
			}
			return
		}
	}
}

func TestBug1196_SecondConnectionToSameCityRefused(t *testing.T) {
	url := newGatedHostedServer(t)

	first, code := dialHandshake(t, url, "alpha")
	if code != "" {
		t.Fatalf("first connection refused: %s", code)
	}
	defer func() { _ = first.Close() }()

	second, code := dialHandshake(t, url, "alpha")
	defer func() { _ = second.Close() }()
	if code != ErrCitySessionBusy {
		t.Fatalf("second connection to the same city: refusal code = %q, want %s", code, ErrCitySessionBusy)
	}
	// The refused socket is closed by the server.
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := second.ReadMessage(); err != nil {
			break
		}
	}

	// The first connection is untouched and still served by the engine.
	setSpeedRoundTrip(t, first, "first-after-refusal")
}

func TestBug1196_CityClaimableAgainAfterFirstCloses(t *testing.T) {
	url := newGatedHostedServer(t)

	first, code := dialHandshake(t, url, "alpha")
	if code != "" {
		t.Fatalf("first connection refused: %s", code)
	}
	_ = first.Close()

	// onClose fires when the server notices the close; allow a short window.
	deadline := time.Now().Add(5 * time.Second)
	var third *websocket.Conn
	for {
		c, code := dialHandshake(t, url, "alpha")
		if code == "" {
			third = c
			break
		}
		_ = c.Close()
		if code != ErrCitySessionBusy {
			t.Fatalf("unexpected refusal %s", code)
		}
		if time.Now().After(deadline) {
			t.Fatal("city never became claimable after the first connection closed")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer func() { _ = third.Close() }()
	setSpeedRoundTrip(t, third, "third")
}

func TestBug1196_DifferentCitiesBothAccepted(t *testing.T) {
	url := newGatedHostedServer(t)

	a, code := dialHandshake(t, url, "alpha")
	if code != "" {
		t.Fatalf("alpha refused: %s", code)
	}
	defer func() { _ = a.Close() }()
	b, code := dialHandshake(t, url, "bravo")
	if code != "" {
		t.Fatalf("bravo refused while alpha is live: %s", code)
	}
	defer func() { _ = b.Close() }()
	setSpeedRoundTrip(t, a, "a")
	setSpeedRoundTrip(t, b, "b")
}

// An unconfirmed claim lapses claimPendingTTL AFTER its resolver returned,
// never while the resolver runs; a confirmed connection holds the city
// regardless of age.
func TestBug1196_GateUnconfirmedClaimLapses(t *testing.T) {
	g := newSessionGate()
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	tok, ok := g.claim("t", "c")
	if !ok {
		t.Fatal("first claim refused")
	}
	now = now.Add(time.Hour) // resolver still running: no expiry
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("claim admitted while the first resolver is still running")
	}
	g.resolved("t", "c", tok)
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("second claim admitted inside the post-resolve TTL")
	}
	now = now.Add(claimPendingTTL + time.Second)
	if _, ok := g.claim("t", "c"); !ok {
		t.Fatal("an unconfirmed claim must lapse after claimPendingTTL from resolve return")
	}
	g.resolved("t", "c", 2)
	g.opened("t", "c")
	now = now.Add(time.Hour)
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("a confirmed live connection must hold the city regardless of age")
	}
	g.closed("t", "c")
	if _, ok := g.claim("t", "c"); !ok {
		t.Fatal("closed city must be claimable")
	}
}

// F1: the TTL must not run while the resolver is slow.
func TestBug1196_SlowResolverKeepsClaim(t *testing.T) {
	g := newSessionGate()
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	tok, _ := g.claim("t", "c")
	now = now.Add(10 * claimPendingTTL)
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("F1: claim expired while its resolver was still running")
	}
	g.abandon("t", "c", tok)
	if _, ok := g.claim("t", "c"); !ok {
		t.Fatal("abandon by the owning token must free the city")
	}
}

// F2: a stale token cannot abandon a newer claim, and closing one of two
// confirmed connections must not free the city under the other.
func TestBug1196_ClaimsAreOwned(t *testing.T) {
	g := newSessionGate()
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	old, _ := g.claim("t", "c")
	g.resolved("t", "c", old)
	now = now.Add(claimPendingTTL + time.Second) // old lapses unconfirmed
	newTok, ok := g.claim("t", "c")
	if !ok {
		t.Fatal("lapsed claim must be replaceable")
	}
	g.abandon("t", "c", old) // stale token: must be a no-op
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("F2: a stale token's abandon cleared the newer claim")
	}
	g.resolved("t", "c", old) // stale: must not restart/alter the newer claim
	g.resolved("t", "c", newTok)
	g.opened("t", "c")
	g.opened("t", "c") // the late confirm of the lapsed claim
	g.closed("t", "c")
	if _, ok := g.claim("t", "c"); ok {
		t.Fatal("F2: closing one connection freed the city under another live one")
	}
	g.closed("t", "c")
	if _, ok := g.claim("t", "c"); !ok {
		t.Fatal("city must be claimable after the last connection closed")
	}
}

// The REAL legacy wiring (legacyWSOptions, exactly what run() installs):
// one slot whatever city the client names; freed on close.
func TestBug1196_LegacyWiringSingleSlot(t *testing.T) {
	host := newTestHost(t, "")
	rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	srv := wsserver.New(rc.Transport(), buildinfo.Version, wsserver.DefaultHandshakeTimeout, legacyWSOptions(rc.Transport())...)
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http")
	a, code := dialHandshake(t, url, "alpha")
	if code != "" {
		t.Fatalf("first refused %q", code)
	}
	b, code := dialHandshake(t, url, "bravo")
	_ = b.Close()
	if code != ErrCitySessionBusy {
		t.Fatalf("legacy second conn (other city name) code=%q, want %s", code, ErrCitySessionBusy)
	}
	_ = a.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, code := dialHandshake(t, url, "alpha")
		_ = c.Close()
		if code == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy slot never freed (code %q)", code)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
