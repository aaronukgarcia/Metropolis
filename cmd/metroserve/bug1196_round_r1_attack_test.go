package main

// ROUND r1 BUG-1196 attacker tests (hostile review; not production code).

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

func rAttackServe(t *testing.T, opts ...wsserver.Option) string {
	t.Helper()
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, opts...)
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http")
}

func rActive(h *CityHost, city string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.cities[persist.CityKey{TenantID: persistTenantID, CityID: city}]; ok {
		return e.active
	}
	return -1
}

// A1: tight concurrent burst on one city -> exactly one admitted; refusals
// never change the host refcount.
func TestRoundR1_ConcurrentBurstExactlyOne(t *testing.T) {
	host := newTestHost(t, "")
	url := rAttackServe(t, hostedWSOptions(host)...)
	const n = 16
	var wg sync.WaitGroup
	var accepted, refused atomic.Int32
	conns := make([]*websocket.Conn, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			c, code := dialHandshake(t, url, "alpha")
			conns[i] = c
			switch code {
			case "":
				accepted.Add(1)
			case ErrCitySessionBusy:
				refused.Add(1)
			default:
				t.Errorf("unexpected code %q", code)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 || refused.Load() != n-1 {
		t.Fatalf("accepted=%d refused=%d, want 1/%d", accepted.Load(), refused.Load(), n-1)
	}
	deadline := time.Now().Add(5 * time.Second)
	for rActive(host, "alpha") != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a := rActive(host, "alpha"); a != 1 {
		t.Fatalf("refcount after burst = %d, want 1 (refusals must not Acquire)", a)
	}
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
}

// A2: slow-but-alive first handshake (resolver blocked past claimPendingTTL,
// e.g. a cold rehydrate or a GetOrCreate waiting on an eviction teardown)
// races the TTL. Uses the gate's clock seam, composed exactly as
// hostedWSOptions composes it.
func TestRoundR1_TTLLapseAdmitsSecondWhileFirstSlow(t *testing.T) {
	host := newTestHost(t, "")
	gate := newSessionGate()
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	gate.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	var calls atomic.Int32
	resolve := func(tenant, city string) (protocol.Transport, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release // first handshake is slow, but alive
		}
		rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: tenant, CityID: city})
		if err != nil {
			return nil, err
		}
		return rc.Transport(), nil
	}
	url := rAttackServe(t,
		wsserver.WithTransportResolver(gate.gatedResolver(resolve, identityKey)),
		gate.gatedLifecycle(identityKey,
			func(tn, c string) { host.Acquire(tn, c) },
			func(tn, c string) { host.Release(tn, c) }))

	type res struct {
		c    *websocket.Conn
		code string
	}
	firstCh := make(chan res, 1)
	go func() { c, code := dialHandshake(t, url, "alpha"); firstCh <- res{c, code} }()
	<-entered
	mu.Lock()
	now = now.Add(claimPendingTTL + time.Second)
	mu.Unlock()
	second, code2 := dialHandshake(t, url, "alpha")
	close(release)
	first := <-firstCh
	defer func() { _ = first.c.Close() }()
	defer func() { _ = second.Close() }()
	t.Logf("first=%q second=%q", first.code, code2)
	if first.code == "" && code2 == "" {
		t.Errorf("FINDING: TWO live connections admitted to one city (TTL lapsed while the first handshake was merely slow)")
	}
	// Ownerless release: close the first; while the second is still live, a third must be refused.
	_ = first.c.Close()
	time.Sleep(300 * time.Millisecond)
	third, code3 := dialHandshake(t, url, "alpha")
	defer func() { _ = third.Close() }()
	if code2 == "" && code3 == "" {
		t.Errorf("FINDING: closing one connection released the city while another is still live; third admitted")
	}
}

// A3: confirmed connection drops abruptly (no close frame) -> city claimable again.
func TestRoundR1_AbruptDropReleases(t *testing.T) {
	host := newTestHost(t, "")
	url := rAttackServe(t, hostedWSOptions(host)...)
	first, code := dialHandshake(t, url, "alpha")
	if code != "" {
		t.Fatalf("first refused %q", code)
	}
	_ = first.UnderlyingConn().Close() // TCP close, no WS close frame
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, code := dialHandshake(t, url, "alpha")
		_ = c.Close()
		if code == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("city still busy 5s after abrupt drop (code %q)", code)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A4: legacy wiring (copied from run(): single slot, fixed transport) refuses a
// second connection even to a DIFFERENT city name, and frees on close.
func TestRoundR1_LegacySingleSlot(t *testing.T) {
	host := newTestHost(t, "")
	rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	transport := rc.Transport()
	legacyGate := newSessionGate()
	srv := wsserver.New(transport, buildinfo.Version, wsserver.DefaultHandshakeTimeout,
		wsserver.WithTransportResolver(legacyGate.gatedResolver(
			func(string, string) (protocol.Transport, error) { return transport, nil }, singleEngineKey)),
		legacyGate.gatedLifecycle(singleEngineKey, nil, nil))
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
