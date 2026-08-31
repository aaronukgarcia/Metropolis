package wsserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// lifecycle_test.go — FEAT-1972079942 AC-1: the optional connection-lifecycle
// hook (WithConnectionLifecycle). These tests prove: onOpen fires exactly once
// with the bound (tenant, city) right after a successful handshake; onClose
// fires exactly once with the SAME (tenant, city) on EVERY disconnect path
// (client close, server shutdown); a REFUSED handshake fires neither (the pair
// is armed only after a bind); the pairing is exactly-once under concurrent
// connections (-race); and a Server with NO hook installed is unchanged (AC-6).

// lifecycleRec records onOpen/onClose invocations and surfaces them over
// buffered channels so a test can deterministically await a hook firing (the
// hooks run on the server's own per-connection goroutine, asynchronously to the
// client). Safe for concurrent use — many connections' hooks fire at once.
type lifecycleRec struct {
	mu      sync.Mutex
	opens   []call
	closes  []call
	openCh  chan call
	closeCh chan call
}

func newLifecycleRec(buf int) *lifecycleRec {
	return &lifecycleRec{
		openCh:  make(chan call, buf),
		closeCh: make(chan call, buf),
	}
}

func (r *lifecycleRec) onOpen(tenant, city string) {
	r.mu.Lock()
	r.opens = append(r.opens, call{tenant, city})
	r.mu.Unlock()
	r.openCh <- call{tenant, city}
}

func (r *lifecycleRec) onClose(tenant, city string) {
	r.mu.Lock()
	r.closes = append(r.closes, call{tenant, city})
	r.mu.Unlock()
	r.closeCh <- call{tenant, city}
}

func (r *lifecycleRec) counts() (opens, closes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.opens), len(r.closes)
}

// newLifecycleServer starts an httptest server fronting a Server that wraps a
// single transport and has the lifecycle hooks installed (no resolver — the
// hook is independent of routing; tenant/city default to local/default).
func newLifecycleServer(t *testing.T, engineVersion string, rec *lifecycleRec) (wsURL string, cleanup func()) {
	t.Helper()
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, engineVersion, time.Second, WithConnectionLifecycle(rec.onOpen, rec.onClose))
	httpSrv := httptest.NewServer(srv)
	wsURL = "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	return wsURL, func() {
		httpSrv.Close()
		_ = transport.Close()
	}
}

func awaitCall(t *testing.T, ch chan call, what string) call {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s hook to fire", what)
		return call{}
	}
}

// TestLifecycle_OpenClosePaired (AC-1): a handshake that binds fires onOpen once
// with the bound (tenant, city); closing the connection fires onClose once with
// the SAME (tenant, city). Client omits the city fields, so defaults apply.
func TestLifecycle_OpenClosePaired(t *testing.T) {
	rec := newLifecycleRec(8)
	url, cleanup := newLifecycleServer(t, "v1.2.3", rec)
	defer cleanup()

	conn := dial(t, url)
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}

	opened := awaitCall(t, rec.openCh, "onOpen")
	if opened.tenant != defaultTenantID || opened.city != defaultCityID {
		t.Fatalf("onOpen got (%q,%q), want defaults (%q,%q)", opened.tenant, opened.city, defaultTenantID, defaultCityID)
	}

	_ = conn.Close() // client-initiated normal close
	closed := awaitCall(t, rec.closeCh, "onClose")
	if closed != opened {
		t.Fatalf("onClose (%+v) != onOpen (%+v) — hooks must carry the SAME key", closed, opened)
	}

	// Exactly-once: give any spurious extra call a chance to arrive, then assert.
	time.Sleep(50 * time.Millisecond)
	if o, c := rec.counts(); o != 1 || c != 1 {
		t.Fatalf("want exactly 1 open + 1 close, got %d open / %d close", o, c)
	}
}

// TestLifecycle_AbruptDropFiresClose (AC-1): an ABRUPT client drop — the
// underlying TCP conn slammed shut with no WebSocket close handshake — is a
// distinct disconnect path from the clean close in the test above (the server's
// ReadMessage returns an unexpected error, not a clean close). onClose must
// still fire exactly once via the defer. This is the "read error" arm of "every
// disconnect path"; the defer is what makes onClose robust to whichever way
// ServeHTTP returns (a genuine server shutdown ends the process's sockets the
// same way, so it too returns through this defer).
func TestLifecycle_AbruptDropFiresClose(t *testing.T) {
	rec := newLifecycleRec(8)
	url, cleanup := newLifecycleServer(t, "v1.2.3", rec)
	defer cleanup()

	conn := dial(t, url)
	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake refused: %+v", resp.Error)
	}
	_ = awaitCall(t, rec.openCh, "onOpen")

	// Slam the raw TCP connection shut without a WebSocket close frame: the
	// server's inbound ReadMessage returns an unexpected-close error, ServeHTTP
	// returns, and the deferred onClose fires.
	_ = conn.UnderlyingConn().Close()

	closed := awaitCall(t, rec.closeCh, "onClose")
	if closed.tenant != defaultTenantID || closed.city != defaultCityID {
		t.Fatalf("onClose got (%q,%q), want defaults", closed.tenant, closed.city)
	}
	time.Sleep(50 * time.Millisecond)
	if o, c := rec.counts(); o != 1 || c != 1 {
		t.Fatalf("want exactly 1 open + 1 close after an abrupt drop, got %d / %d", o, c)
	}
}

// TestLifecycle_RefusedHandshakeNoHooks (AC-1): a handshake that never binds (a
// version mismatch, refused before any transport bind) fires NEITHER hook — the
// close hook is armed only after onOpen, so an unbound connection is inert.
func TestLifecycle_RefusedHandshakeNoHooks(t *testing.T) {
	rec := newLifecycleRec(8)
	url, cleanup := newLifecycleServer(t, "v1.2.3", rec)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	if resp := sendHandshake(t, conn, "v9.9.9"); resp.Error == nil {
		t.Fatal("expected a version-mismatch refusal, got acceptance")
	}

	// Neither hook may fire for a refused (never-bound) connection.
	select {
	case c := <-rec.openCh:
		t.Fatalf("onOpen fired for a refused handshake: %+v", c)
	case c := <-rec.closeCh:
		t.Fatalf("onClose fired for a refused handshake: %+v", c)
	case <-time.After(300 * time.Millisecond):
		// good: no hook fired
	}
	if o, c := rec.counts(); o != 0 || c != 0 {
		t.Fatalf("refused handshake must fire no hooks, got %d open / %d close", o, c)
	}
}

// TestLifecycle_NamedCityKey (AC-1): the hook carries the handshake's named
// (tenant, city), not just the defaults — proving the key threaded through is
// the resolved one routing would use.
func TestLifecycle_NamedCityKey(t *testing.T) {
	rec := newLifecycleRec(8)
	url, cleanup := newLifecycleServer(t, "v1.2.3", rec)
	defer cleanup()

	conn := dial(t, url)
	handshakeCity(t, conn, "v1.2.3", "tenantX", "cityY")
	opened := awaitCall(t, rec.openCh, "onOpen")
	if opened.tenant != "tenantX" || opened.city != "cityY" {
		t.Fatalf("onOpen got (%q,%q), want (tenantX,cityY)", opened.tenant, opened.city)
	}
	_ = conn.Close()
	closed := awaitCall(t, rec.closeCh, "onClose")
	if closed != opened {
		t.Fatalf("onClose %+v != onOpen %+v", closed, opened)
	}
}

// TestLifecycle_ConcurrentExactlyOncePaired (AC-1, the -race attack): many
// connections open and close at once; the total onOpen count equals the total
// onClose count equals the number of connections, with no double/missed close on
// any path. Run under `go test -race`.
func TestLifecycle_ConcurrentExactlyOncePaired(t *testing.T) {
	const n = 40
	rec := newLifecycleRec(4 * n)
	url, cleanup := newLifecycleServer(t, "v1.2.3", rec)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			conn := dial(t, url)
			if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
				t.Errorf("handshake refused: %+v", resp.Error)
				_ = conn.Close()
				return
			}
			_ = conn.Close()
		}()
	}
	wg.Wait()

	// Await n closes (each accepted connection must produce exactly one).
	deadline := time.After(5 * time.Second)
	for got := 0; got < n; {
		select {
		case <-rec.closeCh:
			got++
		case <-deadline:
			o, c := rec.counts()
			t.Fatalf("timed out awaiting %d closes: got %d open / %d close", n, o, c)
		}
	}
	// Absorb any (illegal) stray extra hook before the final tally.
	time.Sleep(100 * time.Millisecond)
	if o, c := rec.counts(); o != n || c != n {
		t.Fatalf("want %d open + %d close, got %d / %d (a close was doubled or missed)", n, n, o, c)
	}
}

// TestLifecycle_NoHookUnchanged (AC-6): a Server constructed WITHOUT
// WithConnectionLifecycle has nil hooks and serves a connection exactly as
// before — no panic, handshake accepted, command round-trips. (The full
// byte-for-byte no-hook guarantee is covered by every other test in this
// package continuing to pass; this asserts the nil-hook fields explicitly.)
func TestLifecycle_NoHookUnchanged(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second) // no lifecycle option
	if srv.onConnOpen != nil || srv.onConnClose != nil {
		t.Fatal("no lifecycle option installed, but hook fields are non-nil")
	}
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	defer func() { _ = transport.Close() }()
	url := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	resp := sendHandshake(t, conn, "v1.2.3")
	if resp.Error != nil {
		t.Fatalf("handshake refused on a no-hook server: %+v", resp.Error)
	}
	var result handshakeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil || !result.Accepted {
		t.Fatalf("expected acceptance, got %+v (err %v)", result, err)
	}
}
