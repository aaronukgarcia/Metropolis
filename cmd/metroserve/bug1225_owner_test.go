package main

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

// bug1225Holds samples the engine's subscription count every 10ms for d and
// fails the moment it differs from want.
func bug1225Holds(t *testing.T, rc *runningCity, want int, d time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if got := rc.engine.SubscriptionCount(); got != want {
			t.Fatalf("%s: engine holds %d subscriptions, want %d", why, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func bug1225HostedServer(t *testing.T) (*CityHost, string, func()) {
	t.Helper()
	host := newTestHost(t, "")
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	return host, "ws" + strings.TrimPrefix(httpSrv.URL, "http"), httpSrv.Close
}

func bug1225City(t *testing.T, host *CityHost, city string) *runningCity {
	t.Helper()
	rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: city})
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	return rc
}

func bug1225InProc(t *testing.T, rc *runningCity, id string) {
	t.Helper()
	err := rc.transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(id),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Flood 300 Subscribes then drop abruptly, 30 times. Subscribes still queued
// when the connection dropped are applied after the detach; none may survive,
// and the next connection must start from zero.
func TestBug1225_FloodThenDropLeavesNothing(t *testing.T) {
	host, url, closeSrv := bug1225HostedServer(t)
	defer closeSrv()
	const city = "bug1225-flood"
	var rc *runningCity
	for cycle := 0; cycle < 30; cycle++ {
		conn, code := bug1225DialRetry(t, url, city)
		if code != "" {
			t.Fatalf("cycle %d refused %s", cycle, code)
		}
		if rc == nil {
			rc = bug1225City(t, host, city)
		}
		// The gate frees the slot only after the detach: whatever the previous
		// connection left queued is dropped, never applied.
		bug1225Holds(t, rc, 0, 150*time.Millisecond, "cycle "+strconv.Itoa(cycle)+" start")
		for i := 0; i < 300; i++ {
			bug1225Subscribe(t, conn, int64(10+i), "f"+strconv.Itoa(i))
		}
		_ = conn.Close()
	}
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("final dial refused %s", code)
	}
	_ = conn.Close()
	bug1225WaitSubs(t, rc, 0, "after final drop")
	bug1225Holds(t, rc, 0, 400*time.Millisecond, "after final settle")
}

// 200 rapid reconnects: the live connection always keeps its own
// subscription, an earlier connection's cleanup never removes it, and nothing
// ownerless remains at the end.
func TestBug1225_RapidReconnect200(t *testing.T) {
	host, url, closeSrv := bug1225HostedServer(t)
	defer closeSrv()
	const city = "bug1225-rapid"
	var rc *runningCity
	for cycle := 0; cycle < 200; cycle++ {
		conn, code := bug1225DialRetry(t, url, city)
		if code != "" {
			t.Fatalf("cycle %d refused %s", cycle, code)
		}
		if rc == nil {
			rc = bug1225City(t, host, city)
		}
		bug1225Subscribe(t, conn, 10, "r")
		if cycle%2 == 0 {
			bug1225WaitSubs(t, rc, 1, "cycle "+strconv.Itoa(cycle)+": live connection's own subscription")
			bug1225Holds(t, rc, 1, 20*time.Millisecond, "cycle "+strconv.Itoa(cycle)+": live subscription must stay")
		}
		_ = conn.Close()
	}
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("final dial refused %s", code)
	}
	_ = conn.Close()
	bug1225WaitSubs(t, rc, 0, "after 200 rapid cycles")
	bug1225Holds(t, rc, 0, 400*time.Millisecond, "ownerless subscriptions after settle")
}

// A subscriber with no connection owner (in-process) survives 10 socket drops.
func TestBug1225_InProcessSubscriberSurvivesSocketDrops(t *testing.T) {
	host, url, closeSrv := bug1225HostedServer(t)
	defer closeSrv()
	const city = "bug1225-inproc"
	rc := bug1225City(t, host, city)
	bug1225InProc(t, rc, "bug1225-inproc")
	bug1225WaitSubs(t, rc, 1, "in-process subscribe")
	for cycle := 0; cycle < 10; cycle++ {
		conn, code := bug1225DialRetry(t, url, city)
		if code != "" {
			t.Fatalf("cycle %d refused %s", cycle, code)
		}
		bug1225Subscribe(t, conn, 10, "w")
		bug1225WaitSubs(t, rc, 2, "in-process plus the socket's")
		_ = conn.Close()
		bug1225WaitSubs(t, rc, 1, "after drop only the in-process subscriber remains")
	}
	bug1225Holds(t, rc, 1, 300*time.Millisecond, "in-process subscriber survives")
}

// Server-side unload with a live, subscribed client and then a stopped
// engine: no panic, no deadlock, detach stays safe and idempotent.
func TestBug1225_ServerUnloadAndStoppedEngine(t *testing.T) {
	host, url, closeSrv := bug1225HostedServer(t)
	defer closeSrv()
	const city = "bug1225-unload"
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("refused %s", code)
	}
	defer func() { _ = conn.Close() }()
	rc := bug1225City(t, host, city)
	for i := 0; i < 50; i++ {
		bug1225Subscribe(t, conn, int64(10+i), "u"+strconv.Itoa(i))
	}
	bug1225WaitSubs(t, rc, 50, "all applied")
	done := make(chan error, 1)
	go func() { done <- host.Shutdown(persist.CityKey{TenantID: persistTenantID, CityID: city}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("Shutdown deadlocked with a live subscribed client")
	}
	rc.engine.DetachSubscriptionOwner(1)
	rc.engine.DetachSubscriptionOwner(1 << 40)
	if got := rc.engine.SubscriptionCount(); got != 0 {
		t.Fatalf("subscriptions left after unload+detach: %d", got)
	}
}

// Client close frame, then idle eviction after the detach, then a reconnect
// onto the rebuilt city.
func TestBug1225_CloseFrameAndIdleEviction(t *testing.T) {
	host, err := newCityHost("", hostTickDisabled, 50*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	host.engineOpts = testEngineOpts()
	t.Cleanup(func() { _ = host.Close() })
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	const city = "bug1225-idle"
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("refused %s", code)
	}
	rc := bug1225City(t, host, city)
	bug1225Subscribe(t, conn, 10, "i")
	bug1225WaitSubs(t, rc, 1, "subscribed")
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = conn.Close()
	bug1225WaitSubs(t, rc, 0, "after close frame")
	key := persist.CityKey{TenantID: persistTenantID, CityID: city}
	if !waitForNoCity(host, key, 5*time.Second) {
		t.Fatal("city not evicted once idle after the detach")
	}
	conn2, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("reconnect after eviction refused %s", code)
	}
	defer func() { _ = conn2.Close() }()
	rc2 := bug1225City(t, host, city)
	bug1225Subscribe(t, conn2, 10, "i2")
	bug1225WaitSubs(t, rc2, 1, "subscribe on rebuilt city")
}

// The legacy single-engine path with a real connection: its subscriptions go
// when it drops and an in-process one stays.
func TestBug1225_LegacyPathDetach(t *testing.T) {
	host := newTestHost(t, "")
	rc := bug1225City(t, host, "legacy")
	srv := wsserver.New(rc.Transport(), buildinfo.Version, wsserver.DefaultHandshakeTimeout, legacyWSOptions(rc.Transport(), rc.engine)...)
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http")
	bug1225InProc(t, rc, "bug1225-legacy-inproc")
	bug1225WaitSubs(t, rc, 1, "in-process")
	for cycle := 0; cycle < 5; cycle++ {
		conn, code := bug1225DialRetry(t, url, "legacy")
		if code != "" {
			t.Fatalf("cycle %d refused %s", cycle, code)
		}
		for i := 0; i < 100; i++ {
			bug1225Subscribe(t, conn, int64(10+i), "l"+strconv.Itoa(i))
		}
		bug1225WaitSubs(t, rc, 101, "legacy live")
		_ = conn.Close()
		c2, code := bug1225DialRetry(t, url, "legacy")
		if code != "" {
			t.Fatalf("redial refused %s", code)
		}
		bug1225Holds(t, rc, 1, 150*time.Millisecond, "legacy: only the in-process subscriber after the drop")
		_ = c2.Close()
		bug1225DialRetryClosed(t, url)
	}
}

// bug1225DialRetryClosed waits until the legacy slot is free again.
func bug1225DialRetryClosed(t *testing.T, url string) {
	t.Helper()
	c, code := bug1225DialRetry(t, url, "legacy")
	if code != "" {
		t.Fatalf("slot never freed: %s", code)
	}
	_ = c.Close()
	time.Sleep(50 * time.Millisecond)
}
