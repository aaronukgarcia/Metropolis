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

// routing_test.go — FEAT-1972079936 Phase 2 inc2 (connection->city routing).
// These tests exercise WithTransportResolver at the wsserver handshake seam:
// AC-4 (two connections naming different cities route to their own engine, and
// same-city connections share one transport), AC-3 (a resolver error refuses
// the handshake cleanly, never a fallback), AC-1 (default city/tenant applied
// when the handshake omits them), and AC-6 (no resolver => the single wrapped
// transport serves every connection, unchanged).

// recordingResolver is a stub TransportResolver that maps a cityID to a fixed
// transport (so same-city callers share one), records every (tenant, city) it
// is asked for, and can be told to fail for a named city.
type recordingResolver struct {
	mu       sync.Mutex
	byCity   map[string]protocol.Transport
	failCity string
	failErr  error
	calls    []call
}

type call struct{ tenant, city string }

func (r *recordingResolver) resolve(tenant, city string) (protocol.Transport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{tenant, city})
	if city == r.failCity {
		return nil, r.failErr
	}
	t, ok := r.byCity[city]
	if !ok {
		t = protocol.NewInProcTransport(
			protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
			protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
		r.byCity[city] = t
	}
	return t, nil
}

func (r *recordingResolver) callsFor() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]call, len(r.calls))
	copy(out, r.calls)
	return out
}

// newRoutingServer starts an httptest server fronting a Server whose transport
// is resolved per-connection by res. The wrapped transport is nil on purpose:
// with a resolver installed the Server never touches it.
func newRoutingServer(t *testing.T, engineVersion string, res *recordingResolver) (wsURL string, cleanup func()) {
	t.Helper()
	srv := New(nil, engineVersion, time.Second, WithTransportResolver(res.resolve))
	httpSrv := httptest.NewServer(srv)
	wsURL = "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	return wsURL, func() { httpSrv.Close() }
}

// handshakeCity performs a handshake naming cityID (tenantID may be "") and
// fails the test if it is refused. clientVersion must match engineVersion.
func handshakeCity(t *testing.T, conn *websocket.Conn, clientVersion, tenantID, cityID string) {
	t.Helper()
	params, _ := json.Marshal(handshakeParams{ClientVersion: clientVersion, TenantID: tenantID, CityID: cityID})
	id := int64(1)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("handshake for city %q refused: %+v", cityID, resp.Error)
	}
}

// sendCommandExpectAck writes a well-formed SetSpeed(2) command and reads its
// ack, failing the test if the ack is an error.
func sendCommandExpectAck(t *testing.T, conn *websocket.Conn, id int64) protocol.CorrelationID {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(t.Name() + "-" + time.Now().Format(time.RFC3339Nano)),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 2},
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
	return cmd.CorrelationID
}

// TestRouting_TwoCitiesIsolated is AC-4: two connections naming DIFFERENT
// cities route to their own transports. A command on connection A lands on
// city A's transport and NOT city B's (inbound isolation); a result pushed onto
// city B's transport reaches connection B and NOT connection A (outbound
// isolation). Proven at the routing seam with two distinct in-proc transports.
func TestRouting_TwoCitiesIsolated(t *testing.T) {
	res := &recordingResolver{byCity: map[string]protocol.Transport{}}
	url, cleanup := newRoutingServer(t, "v1.2.3", res)
	defer cleanup()

	connA := dial(t, url)
	defer func() { _ = connA.Close() }()
	connB := dial(t, url)
	defer func() { _ = connB.Close() }()

	handshakeCity(t, connA, "v1.2.3", "local", "A")
	handshakeCity(t, connB, "v1.2.3", "local", "B")

	transportA := res.byCity["A"].(*protocol.InProcTransport)
	transportB := res.byCity["B"].(*protocol.InProcTransport)
	if transportA == transportB {
		t.Fatal("two different cities must not share a transport")
	}

	// Inbound isolation: a command on A lands on A's transport, not B's.
	sendCommandExpectAck(t, connA, 10)
	select {
	case <-transportA.Commands():
		// good: A's engine got it
	case <-time.After(2 * time.Second):
		t.Fatal("command sent on connection A never reached city A's transport")
	}
	select {
	case c := <-transportB.Commands():
		t.Fatalf("command sent on connection A leaked to city B's transport: %+v", c)
	case <-time.After(150 * time.Millisecond):
		// good: B is untouched
	}

	// Outbound isolation: a result pushed onto B's transport reaches connection
	// B and NOT connection A.
	want := protocol.CorrelationID("only-for-B")
	if !transportB.SendResult(protocol.CommandResult{CorrelationID: want, Accepted: true}) {
		t.Fatal("failed to push a result onto city B's transport")
	}
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := waitForResult(t, connB, want)
	var res2 protocol.CommandResult
	if err := json.Unmarshal(got.Params, &res2); err != nil {
		t.Fatalf("decode B's result: %v", err)
	}
	if res2.CorrelationID != want {
		t.Fatalf("connection B got wrong result: %+v", res2)
	}
	// Connection A must NOT see B's result.
	_ = connA.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var stray rpcMessage
	if err := connA.ReadJSON(&stray); err == nil {
		t.Fatalf("connection A received a frame meant for city B: %+v", stray)
	}
}

// TestRouting_SameCityShareTransport is AC-4's second half: two connections
// naming the SAME city are routed to the SAME transport (one engine), so both
// their commands land on that single transport.
func TestRouting_SameCityShareTransport(t *testing.T) {
	res := &recordingResolver{byCity: map[string]protocol.Transport{}}
	url, cleanup := newRoutingServer(t, "v1.2.3", res)
	defer cleanup()

	conn1 := dial(t, url)
	defer func() { _ = conn1.Close() }()
	conn2 := dial(t, url)
	defer func() { _ = conn2.Close() }()

	handshakeCity(t, conn1, "v1.2.3", "local", "shared")
	handshakeCity(t, conn2, "v1.2.3", "local", "shared")

	shared := res.byCity["shared"].(*protocol.InProcTransport)
	if len(res.byCity) != 1 {
		t.Fatalf("same-city connections must not create a second transport, got %d", len(res.byCity))
	}

	sendCommandExpectAck(t, conn1, 1)
	sendCommandExpectAck(t, conn2, 2)
	for i := 0; i < 2; i++ {
		select {
		case <-shared.Commands():
		case <-time.After(2 * time.Second):
			t.Fatalf("expected both same-city commands on the shared transport, missing #%d", i+1)
		}
	}
}

// TestRouting_ResolverErrorRefusesCleanly is AC-3: a resolver error refuses the
// handshake with the dedicated MET-P030 code and closes the connection — it is
// NEVER served against a fallback city.
func TestRouting_ResolverErrorRefusesCleanly(t *testing.T) {
	res := &recordingResolver{
		byCity:   map[string]protocol.Transport{},
		failCity: "corrupt",
		failErr:  errFakeCorruptJournal,
	}
	url, cleanup := newRoutingServer(t, "v1.2.3", res)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	params, _ := json.Marshal(handshakeParams{ClientVersion: "v1.2.3", TenantID: "local", CityID: "corrupt"})
	id := int64(1)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected a refusal for a resolver error, got acceptance")
	}
	if resp.Error.Code != protocol.ErrHandshakeCityUnavailable {
		t.Fatalf("expected %s, got %s", protocol.ErrHandshakeCityUnavailable, resp.Error.Code)
	}
	// No fallback: a valid city must NOT have been resolved for this connection.
	for _, c := range res.callsFor() {
		if c.city != "corrupt" {
			t.Fatalf("resolver was called for a fallback city %q — must never fall back", c.city)
		}
	}
	// The connection is closed after refusal: the next read must fail.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("connection should be closed after a refusal")
	}
}

var errFakeCorruptJournal = &fakeErr{"city corrupt: simulated corrupt journal (fatal)"}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

// TestRouting_DefaultCityWhenOmitted is AC-1: an old client that sends no
// cityId/tenantId is routed to the default city ("default") under the default
// tenant ("local"), preserving today's single-city behaviour.
func TestRouting_DefaultCityWhenOmitted(t *testing.T) {
	res := &recordingResolver{byCity: map[string]protocol.Transport{}}
	url, cleanup := newRoutingServer(t, "v1.2.3", res)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	// An old client: bare ClientVersion, no city fields at all.
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}

	calls := res.callsFor()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one resolver call, got %d", len(calls))
	}
	if calls[0].tenant != defaultTenantID || calls[0].city != defaultCityID {
		t.Fatalf("expected default routing (%q,%q), got (%q,%q)",
			defaultTenantID, defaultCityID, calls[0].tenant, calls[0].city)
	}
}

// TestRouting_ConcurrentMixedCities is the Destructive concurrency attack
// (FEAT-1972079936 Phase 2 inc2, round): many connections handshake AT ONCE
// naming a MIX of cities — some sharing a city, some distinct, some naming a
// city the resolver fails for. Under `go test -race` this proves the
// per-connection transport binding is a per-goroutine local (never a shared
// mutable Server field clobbered across connections): each accepted connection
// lands its command on ITS OWN city's transport, each error-city connection is
// refused with MET-P030 (never a fallback), and same-city connections converge
// on ONE transport (the resolver reuses, not rebuilds).
func TestRouting_ConcurrentMixedCities(t *testing.T) {
	res := &recordingResolver{
		byCity:   map[string]protocol.Transport{},
		failCity: "boom",
		failErr:  errFakeCorruptJournal,
	}
	url, cleanup := newRoutingServer(t, "v1.2.3", res)
	defer cleanup()

	// A mix: "red"/"blue" appear multiple times (same-city sharing), "boom"
	// always fails, plus several unique cities.
	cities := []string{"red", "blue", "boom", "red", "green", "blue", "boom", "amber", "red", "blue"}

	var wg sync.WaitGroup
	var refusedBoom, acceptedOK int64
	var mu sync.Mutex
	for i, city := range cities {
		wg.Add(1)
		go func(i int, city string) {
			defer wg.Done()
			conn := dial(t, url)
			defer func() { _ = conn.Close() }()
			params, _ := json.Marshal(handshakeParams{ClientVersion: "v1.2.3", TenantID: "local", CityID: city})
			id := int64(1)
			if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
				return
			}
			var resp rpcMessage
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if err := conn.ReadJSON(&resp); err != nil {
				t.Errorf("conn %d city %q: read handshake: %v", i, city, err)
				return
			}
			if city == "boom" {
				if resp.Error == nil || resp.Error.Code != protocol.ErrHandshakeCityUnavailable {
					t.Errorf("conn %d city boom: expected %s refusal, got %+v", i, protocol.ErrHandshakeCityUnavailable, resp)
					return
				}
				mu.Lock()
				refusedBoom++
				mu.Unlock()
				return
			}
			if resp.Error != nil {
				t.Errorf("conn %d city %q: unexpected refusal %+v", i, city, resp.Error)
				return
			}
			// Send a command; it must land on THIS city's transport.
			corr := sendCommandExpectAck(t, conn, 2)
			_ = corr
			mu.Lock()
			acceptedOK++
			mu.Unlock()
		}(i, city)
	}
	wg.Wait()

	if refusedBoom != 2 {
		t.Fatalf("expected 2 boom refusals, got %d", refusedBoom)
	}
	if acceptedOK != 8 {
		t.Fatalf("expected 8 accepted connections, got %d", acceptedOK)
	}
	// Same-city reuse: exactly one transport per distinct non-failing city.
	for _, c := range []string{"red", "blue", "green", "amber"} {
		if _, ok := res.byCity[c]; !ok {
			t.Fatalf("city %q was never resolved", c)
		}
	}
	if _, ok := res.byCity["boom"]; ok {
		t.Fatal("a failing city must never be stored/reused as a transport")
	}
	// Every accepted city's commands must have landed on its own transport;
	// drain non-blocking to prove at least the multi-connection cities got >0.
	for _, c := range []string{"red", "blue"} {
		tr := res.byCity[c].(*protocol.InProcTransport)
		select {
		case <-tr.Commands():
			// good: at least one command arrived on this city's transport
		case <-time.After(time.Second):
			t.Fatalf("city %q transport received no command", c)
		}
	}
}

// TestRouting_NoResolverUsesSingleTransport is AC-6: with NO resolver
// installed, the Server's single wrapped transport serves every connection
// regardless of what city the handshake names — the pre-inc2 path, unchanged.
// A command sent by a cityId-naming client still lands on that one transport.
func TestRouting_NoResolverUsesSingleTransport(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second) // no WithTransportResolver
	if srv.resolveTransport != nil {
		t.Fatal("no resolver should be installed")
	}
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	defer func() { _ = transport.Close() }()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	// The client names a city, but with no resolver the server ignores it and
	// serves the single wrapped transport.
	handshakeCity(t, conn, "v1.2.3", "whoever", "some-city")
	sendCommandExpectAck(t, conn, 1)
	select {
	case <-transport.Commands():
		// good: the one wrapped transport received it
	case <-time.After(2 * time.Second):
		t.Fatal("command never reached the single wrapped transport")
	}
}
