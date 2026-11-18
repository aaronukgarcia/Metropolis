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

func bug1225Send(t *testing.T, conn *websocket.Conn, id int64, cmd protocol.Command) {
	t.Helper()
	b, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "command", "params": json.RawMessage(b)}); err != nil {
		t.Fatalf("write command: %v", err)
	}
}

func bug1225Subscribe(t *testing.T, conn *websocket.Conn, id int64, tag string) {
	t.Helper()
	bug1225Send(t, conn, id, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(t.Name() + "-sub-" + tag),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
	})
}

func bug1225WaitSubs(t *testing.T, rc *runningCity, want int, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := rc.engine.SubscriptionCount()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: engine holds %d subscriptions, want %d", why, got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// bug1225DeltaSubIDs reads frames until the socket is quiet and returns the
// delta count per subscription id seen.
func bug1225DeltaSubIDs(conn *websocket.Conn, quiet time.Duration) map[string]int {
	ids := map[string]int{}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(quiet))
		var msg struct {
			Method string `json:"method"`
			Params struct {
				SubscriptionID string `json:"subscriptionId"`
			} `json:"params"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return ids
		}
		if msg.Method == "delta" {
			ids[msg.Params.SubscriptionID]++
		}
	}
}

// bug1225DialRetry redials while the previous connection's teardown (and so
// its session-gate release) is still in flight.
func bug1225DialRetry(t *testing.T, url, city string) (*websocket.Conn, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, code := dialHandshake(t, url, city)
		if code == "" || time.Now().After(deadline) {
			return conn, code
		}
		_ = conn.Close()
		time.Sleep(30 * time.Millisecond)
	}
}

// BUG-1225: when a connection detaches (abrupt drop or close frame) the
// subscriptions it created must go with it; a reconnect must see only its own.
func TestBug1225_DetachRemovesSubscriptions(t *testing.T) {
	for _, mode := range []string{"abrupt", "closeFrame"} {
		t.Run(mode, func(t *testing.T) {
			host := newTestHost(t, "")
			srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
			httpSrv := httptest.NewServer(srv)
			defer httpSrv.Close()
			url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
			const city = "bug1225"

			var rc *runningCity
			for cycle := 0; cycle < 3; cycle++ {
				conn, code := bug1225DialRetry(t, url, city)
				if code != "" {
					t.Fatalf("cycle %d refused: %s", cycle, code)
				}
				if rc == nil {
					got, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: city})
					if err != nil {
						t.Fatalf("GetOrCreate: %v", err)
					}
					rc = got
				}
				bug1225Subscribe(t, conn, 10, mode)
				bug1225WaitSubs(t, rc, 1, mode+" after subscribe")
				if err := rc.engine.AdvanceTicks("bug1225-adv", 1); err != nil {
					t.Fatalf("AdvanceTicks: %v", err)
				}
				ids := bug1225DeltaSubIDs(conn, 400*time.Millisecond)
				if len(ids) != 1 {
					t.Fatalf("%s cycle %d: connection received deltas for %d subscription ids %v, want exactly 1", mode, cycle, len(ids), ids)
				}
				if mode == "closeFrame" {
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				}
				_ = conn.Close()
				bug1225WaitSubs(t, rc, 0, mode+" after detach")
			}
		})
	}
}

// Fifty connect-subscribe-drop cycles: the engine never holds more than the
// live connection's one subscription, and a tick publishes exactly one delta.
func TestBug1225_FiftyCyclesStayFlat(t *testing.T) {
	host := newTestHost(t, "")
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	const city = "bug1225-50"

	var rc *runningCity
	for cycle := 0; cycle < 50; cycle++ {
		conn, code := bug1225DialRetry(t, url, city)
		if code != "" {
			t.Fatalf("cycle %d refused: %s", cycle, code)
		}
		if rc == nil {
			got, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: city})
			if err != nil {
				t.Fatalf("GetOrCreate: %v", err)
			}
			rc = got
		}
		bug1225Subscribe(t, conn, 10, "flat")
		bug1225WaitSubs(t, rc, 1, "after subscribe")
		if err := rc.engine.AdvanceTicks("bug1225-flat", 1); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
		ids := bug1225DeltaSubIDs(conn, 150*time.Millisecond)
		if len(ids) != 1 {
			t.Fatalf("cycle %d: deltas for %d subscription ids %v, want 1", cycle, len(ids), ids)
		}
		_ = conn.Close()
		bug1225WaitSubs(t, rc, 0, "after detach")
	}
}

// A client's explicit Unsubscribe still works, and a second connection while
// the first is live is still refused without disturbing the first's subs.
func TestBug1225_ExplicitUnsubscribeAndSingleConnection(t *testing.T) {
	host := newTestHost(t, "")
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	const city = "bug1225-explicit"

	conn, code := bug1225DialRetry(t, url, city)
	if code != "" {
		t.Fatalf("refused: %s", code)
	}
	defer func() { _ = conn.Close() }()
	rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: city})
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	bug1225Subscribe(t, conn, 10, "a")
	bug1225WaitSubs(t, rc, 1, "after subscribe")

	second, code2 := dialHandshake(t, url, city)
	_ = second.Close()
	if code2 != "MET-P043" {
		t.Fatalf("second live connection: code %q, want MET-P043", code2)
	}
	time.Sleep(200 * time.Millisecond)
	if got := rc.engine.SubscriptionCount(); got != 1 {
		t.Fatalf("refused second connection disturbed the first: %d subscriptions, want 1", got)
	}

	if err := rc.engine.AdvanceTicks("bug1225-explicit", 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	ids := bug1225DeltaSubIDs(conn, 300*time.Millisecond)
	if len(ids) != 1 {
		t.Fatalf("want one subscription id, got %v", ids)
	}
	var subID string
	for k := range ids {
		subID = k
	}
	bug1225Send(t, conn, 11, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(t.Name() + "-unsub"),
		Kind:            protocol.KindUnsubscribe,
		Payload:         protocol.UnsubscribePayload{SubscriptionID: protocol.SubscriptionID(subID)},
	})
	bug1225WaitSubs(t, rc, 0, "after explicit Unsubscribe")
}
