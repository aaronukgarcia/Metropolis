package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

// BUG-1209: unloading a city with a live client must close that client's
// socket and run the connection's onClose (host.Release and the session-gate
// release). Observable: the client's read fails, and a NEW connection to the
// same city is accepted (before the fix the dead connection's gate claim was
// never released, so the city stayed refused).
func TestBug1209_UnloadWithLiveClientClosesConnAndReleasesGate(t *testing.T) {
	host := newTestHost(t, "")
	srv := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, hostedWSOptions(host)...)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	first, code := dialHandshake(t, url, "unload")
	if code != "" {
		t.Fatalf("first connection refused: %s", code)
	}
	defer func() { _ = first.Close() }()

	if err := host.Shutdown(persist.CityKey{TenantID: persistTenantID, CityID: "unload"}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	_ = first.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		if _, _, err := first.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatalf("socket stayed open after the city unloaded: %v", err)
			}
			break
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		second, code := dialHandshake(t, url, "unload")
		if code == "" {
			_ = second.Close()
			return
		}
		_ = second.Close()
		if time.Now().After(deadline) {
			t.Fatalf("city still refused (%s) after unload: connection's onClose never ran", code)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
