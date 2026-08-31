package main

import (
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

// routing_test.go — FEAT-1972079936 Phase 2 inc2, AC-5/AC-4 at the metroserve
// seam: a CityHost-backed wsserver (the exact wiring runHosted performs, but
// built directly here so the test needs no signal handling or real listen
// address) routes two WS connections naming two cities to two distinct real
// engines. This exercises the same resolver closure shape runHosted installs,
// without a full HTTP boot of run().

// hostResolver mirrors runHosted's closure exactly: build the persist.CityKey
// from the two strings wsserver hands it and route via host.GetOrCreate.
func hostResolver(h *CityHost) wsserver.TransportResolver {
	return func(tenant, city string) (protocol.Transport, error) {
		rc, err := h.GetOrCreate(h.rootCtx, persist.CityKey{TenantID: tenant, CityID: city})
		if err != nil {
			return nil, err
		}
		return rc.Transport(), nil
	}
}

// hsAndSetSpeed dials, handshakes naming cityID, sends SetSpeed(2), and returns
// the engine's CommandResult — proving the connection reached a real engine.
func hsAndSetSpeed(t *testing.T, url, cityID string) protocol.CommandResult {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Handshake naming the city. clientVersion must equal the server's
	// engineVersion (buildinfo.Version) for the build-string gate to pass.
	hsParams, _ := json.Marshal(map[string]any{
		"clientVersion": buildinfo.Version,
		"tenantId":      persistTenantID,
		"cityId":        cityID,
	})
	id := int64(1)
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "handshake", "params": json.RawMessage(hsParams)}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var hsResp struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&hsResp); err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if hsResp.Error != nil {
		t.Fatalf("handshake for city %q refused: %s", cityID, hsResp.Error.Code)
	}

	corr := protocol.CorrelationID(t.Name() + "-" + cityID)
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corr,
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 2},
	}
	cmdBytes, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	cid := int64(2)
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": cid, "method": "command", "params": json.RawMessage(cmdBytes)}); err != nil {
		t.Fatalf("write command: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read result for city %q: %v", cityID, err)
		}
		if msg.Error != nil {
			t.Fatalf("command on city %q errored: %s", cityID, msg.Error.Code)
		}
		if msg.Method != "result" {
			continue // skip the ack (has no method) and any other notification
		}
		var res protocol.CommandResult
		if err := json.Unmarshal(msg.Params, &res); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if res.CorrelationID != corr {
			continue
		}
		return res
	}
}

// TestHostBackedServer_RoutesTwoCities is AC-5/AC-4 at the metroserve seam: a
// CityHost-backed wsserver routes connections naming two different cities to
// two distinct real engines, each accepting its own command; and two
// connections naming the SAME city share ONE engine (one runningCity).
func TestHostBackedServer_RoutesTwoCities(t *testing.T) {
	host := newTestHost(t, "") // no-persist, deterministic, tick-free

	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout,
		wsserver.WithTransportResolver(hostResolver(host)))
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	// Two different cities → two distinct engines, each accepting SetSpeed(2).
	resA := hsAndSetSpeed(t, url, "alpha")
	if !resA.Accepted {
		t.Fatalf("city alpha rejected SetSpeed(2): %+v", resA.Error)
	}
	resB := hsAndSetSpeed(t, url, "bravo")
	if !resB.Accepted {
		t.Fatalf("city bravo rejected SetSpeed(2): %+v", resB.Error)
	}

	// The two cities are genuinely distinct running engines (distinct transports).
	rcA, err := host.GetOrCreate(host.rootCtx, persist.CityKey{TenantID: persistTenantID, CityID: "alpha"})
	if err != nil {
		t.Fatalf("GetOrCreate alpha: %v", err)
	}
	rcB, err := host.GetOrCreate(host.rootCtx, persist.CityKey{TenantID: persistTenantID, CityID: "bravo"})
	if err != nil {
		t.Fatalf("GetOrCreate bravo: %v", err)
	}
	if rcA == rcB || rcA.Transport() == rcB.Transport() {
		t.Fatal("two different cities must be distinct running engines")
	}

	// Same city twice → one engine shared (AC-4's same-city half).
	rcA2, err := host.GetOrCreate(host.rootCtx, persist.CityKey{TenantID: persistTenantID, CityID: "alpha"})
	if err != nil {
		t.Fatalf("GetOrCreate alpha again: %v", err)
	}
	if rcA2 != rcA {
		t.Fatal("same-city connections must share one running engine")
	}
}

// TestHostBacked_ResolverErrorRefused proves the metroserve-shaped resolver
// propagates a build/rehydrate failure as a clean handshake refusal (no
// fallback). A resolver that always fails stands in for a corrupt-journal city
// (the real fatal case), keeping the test independent of on-disk corruption.
func TestHostBacked_ResolverErrorRefused(t *testing.T) {
	failing := func(tenant, city string) (protocol.Transport, error) {
		return nil, errFailResolve
	}
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout,
		wsserver.WithTransportResolver(failing))
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	hsParams, _ := json.Marshal(map[string]any{"clientVersion": buildinfo.Version, "cityId": "anything"})
	id := int64(1)
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "handshake", "params": json.RawMessage(hsParams)}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var hsResp struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&hsResp); err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if hsResp.Error == nil {
		t.Fatal("expected a refusal, got acceptance")
	}
	if hsResp.Error.Code != protocol.ErrHandshakeCityUnavailable {
		t.Fatalf("expected %s, got %s", protocol.ErrHandshakeCityUnavailable, hsResp.Error.Code)
	}
}

var errFailResolve = &resolveErr{"simulated city build failure"}

type resolveErr struct{ s string }

func (e *resolveErr) Error() string { return e.s }
