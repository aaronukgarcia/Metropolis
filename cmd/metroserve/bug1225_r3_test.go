package main

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

func bug1225R3Send(t *testing.T, tr protocol.Transport, id string) {
	t.Helper()
	if err := tr.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(id),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
	}); err != nil {
		t.Fatal(err)
	}
}

func bug1225End(t *testing.T, tr protocol.Transport) {
	t.Helper()
	cs, ok := tr.(wsserver.ConnectionScoped)
	if !ok {
		t.Fatalf("bound transport %T is not connection-scoped", tr)
	}
	cs.ConnectionEnded()
}

// The attacker's stale-close scenario (r2): bind A, bind B on one city, close
// A. The close carries A's own epoch, so B keeps its subscriptions and its
// deltas, in BOTH close orders. Where the gate allows (one connection per
// city) the real-path variants are the flood and rapid-reconnect tests.
func TestBug1225_StaleCloseOrderIndependence(t *testing.T) {
	for _, closeAFirst := range []bool{true, false} {
		host := newTestHost(t, "")
		ctx := context.Background()
		city := "r3order-" + strconv.FormatBool(closeAFirst)
		ta, err := host.BindConnection(ctx, persistTenantID, city)
		if err != nil {
			t.Fatal(err)
		}
		tb, err := host.BindConnection(ctx, persistTenantID, city)
		if err != nil {
			t.Fatal(err)
		}
		rc := bug1225City(t, host, city)
		for i := 0; i < 3; i++ {
			bug1225R3Send(t, ta, "a"+strconv.Itoa(i))
			bug1225R3Send(t, tb, "b"+strconv.Itoa(i))
		}
		bug1225WaitSubs(t, rc, 6, "both live")
		first, second := ta, tb
		if !closeAFirst {
			first, second = tb, ta
		}
		bug1225End(t, first)
		bug1225End(t, first) // idempotent
		bug1225WaitSubs(t, rc, 3, "survivor keeps all of its own")
		if err := rc.engine.AdvanceTicks("r3-adv", 1); err != nil {
			t.Fatal(err)
		}
		bug1225Holds(t, rc, 3, 100*time.Millisecond, "survivor stable")
		bug1225R3Send(t, second, "more")
		bug1225WaitSubs(t, rc, 4, "survivor can still subscribe")
		bug1225End(t, second)
		bug1225WaitSubs(t, rc, 0, "last close leaves zero")
		if n := rc.engine.LiveSubscriptionOwners(); n != 0 {
			t.Fatalf("live-owner set not empty: %d", n)
		}
	}
}

// "Detach after the gate release" is harmless by construction: the close
// carries the connection's own epoch, so a new bind that lands between the
// gate release and the old connection's detach is untouched.
func TestBug1225_DetachAfterGateReleaseIsHarmless(t *testing.T) {
	host := newTestHost(t, "")
	ctx := context.Background()
	const city = "r3late"
	rc := bug1225City(t, host, city)
	ta, _ := host.BindConnection(ctx, persistTenantID, city)
	bug1225R3Send(t, ta, "a1")
	bug1225WaitSubs(t, rc, 1, "A subscribed")
	// seam: A's slot is already free; B binds, subscribes, THEN A's detach runs.
	tb, _ := host.BindConnection(ctx, persistTenantID, city)
	bug1225R3Send(t, tb, "b1")
	bug1225WaitSubs(t, rc, 2, "A and B")
	bug1225End(t, ta)
	bug1225WaitSubs(t, rc, 1, "only A's removed")
	bug1225Holds(t, rc, 1, 100*time.Millisecond, "B survives A's late detach")
}

// 1000 handshakes that never bind (refused by the gate) plus 100 full
// connect/drop cycles leave the live-owner set empty.
func TestBug1225_FailedHandshakesLeaveNoLiveOwner(t *testing.T) {
	host, url, closeSrv := bug1225HostedServer(t)
	defer closeSrv()
	const city = "r3failed"
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("refused %s", code)
	}
	rc := bug1225City(t, host, city)
	for i := 0; i < 1000; i++ {
		s, c := dialHandshake(t, url, city)
		_ = s.Close()
		if c != "MET-P043" {
			t.Fatalf("attempt %d: %q", i, c)
		}
	}
	if n := rc.engine.LiveSubscriptionOwners(); n != 1 {
		t.Fatalf("with one live connection the set holds %d, want 1", n)
	}
	_ = conn.Close()
	for i := 0; i < 100; i++ {
		c, code := bug1225DialRetry(t, url, city)
		if code != "" {
			t.Fatalf("cycle %d refused %s", i, code)
		}
		_ = c.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for rc.engine.LiveSubscriptionOwners() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("live-owner set not empty: %d", rc.engine.LiveSubscriptionOwners())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Replay question (r2 reviewer): a Subscribe is accepted off the wire and
// journaled; Owner is not. Connect, subscribe, advance, drop, unload the city,
// load it again from its journal: how many subscriptions does the restored
// engine hold with no connection attached? Must be zero.
func TestBug1225_RestoredCityHoldsNoSubscriptions(t *testing.T) {
	dir := t.TempDir()
	h := newEvictHost(t, dir, 150*time.Millisecond, 10*time.Millisecond)
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(h)...)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	const city = "r3replay"
	key := persist.CityKey{TenantID: persistTenantID, CityID: city}
	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("refused %s", code)
	}
	rc := bug1225City(t, h, city)
	for i := 0; i < 3; i++ {
		bug1225Subscribe(t, conn, int64(10+i), "p"+strconv.Itoa(i))
	}
	bug1225WaitSubs(t, rc, 3, "subscribed")
	submitAll(t, rc.Engine(), []protocol.Command{advance("r3-replay-adv", 5)})
	pre := rc.Composition().StateDigest()
	_ = conn.Close()
	bug1225WaitSubs(t, rc, 0, "after drop")
	if !waitForNoCity(h, key, 5*time.Second) {
		t.Fatal("city not evicted")
	}
	rc2, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if rc2 == rc {
		t.Fatal("city was not rebuilt")
	}
	t.Logf("restored engine holds %d subscriptions with no connection attached", rc2.engine.SubscriptionCount())
	bug1225Holds(t, rc2, 0, 300*time.Millisecond, "restored city must hold no ownerless subscription")
	if got := rc2.Composition().StateDigest(); got != pre {
		t.Fatalf("restore digest changed: %x != %x", got, pre)
	}
	// The digest is the same as a control that never saw a Subscribe at all:
	// skipping them on replay changes no hashed state.
	if want := standaloneDigest(t, key, []protocol.Command{advance("r3-replay-adv", 5)}); pre != want || rc2.Composition().StateDigest() != want {
		t.Fatalf("digest depends on Subscribe: pre %x restored %x control-without-subscribe %x", pre, rc2.Composition().StateDigest(), want)
	}
}
