package wsserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// newTestServer starts an httptest server fronting a fresh Server wrapping
// a fresh InProcTransport, and returns the ws:// URL plus a cleanup func.
func newTestServer(t *testing.T, engineVersion string, handshakeTimeout time.Duration) (wsURL string, transport *protocol.InProcTransport, cleanup func()) {
	t.Helper()
	transport = protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, engineVersion, handshakeTimeout)
	httpSrv := httptest.NewServer(srv)
	wsURL = "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	return wsURL, transport, func() {
		httpSrv.Close()
		_ = transport.Close()
	}
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

// sendHandshake writes a well-formed handshake request with the given
// clientVersion and returns the decoded response.
func sendHandshake(t *testing.T, conn *websocket.Conn, clientVersion string) rpcMessage {
	t.Helper()
	params, _ := json.Marshal(handshakeParams{ClientVersion: clientVersion})
	id := int64(1)
	req := rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	return resp
}

// TestHandshake_MatchAccepts is AC-required: a client whose ClientVersion
// equals the server's engineVersion is accepted, and the accepted
// response carries the server's version.
func TestHandshake_MatchAccepts(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	resp := sendHandshake(t, conn, "v1.2.3")
	if resp.Error != nil {
		t.Fatalf("expected acceptance, got error %+v", resp.Error)
	}
	var result handshakeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Accepted {
		t.Fatalf("expected Accepted=true, got %+v", result)
	}
	if result.ServerVersion != "v1.2.3" {
		t.Fatalf("expected ServerVersion v1.2.3, got %q", result.ServerVersion)
	}
}

// TestHandshake_MismatchRefusesAndCloses is the core AC: a version
// mismatch must REFUSE (typed error, MET-P010) and close the connection
// -- never degrade or serve the session anyway. This is the test the
// mutation-proof below targets: an "accept anyway" mutation of handshake()
// must turn this test RED.
func TestHandshake_MismatchRefusesAndCloses(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	resp := sendHandshake(t, conn, "v9.9.9")
	if resp.Error == nil {
		t.Fatalf("expected a refusal error, got acceptance %+v", resp.Result)
	}
	if resp.Error.Code != protocol.ErrHandshakeVersionMismatch {
		t.Fatalf("expected code %s, got %s", protocol.ErrHandshakeVersionMismatch, resp.Error.Code)
	}
	if resp.Error.Data["clientVersion"] != "v9.9.9" {
		t.Fatalf("expected clientVersion in error data, got %+v", resp.Error.Data)
	}
	if resp.Error.Data["serverVersion"] != "v1.2.3" {
		t.Fatalf("expected serverVersion in error data, got %+v", resp.Error.Data)
	}

	// The connection must actually be closed by the server -- attempting
	// to send a command afterward must fail (read returns an error), not
	// silently succeed as if the session were live.
	cmdParams, _ := json.Marshal(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "test-corr",
		Kind:            "advanceTicks",
		Payload:         nil,
	})
	id := int64(2)
	_ = conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdParams})
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected the connection to be closed after a refused handshake, but a message was read")
	}
}

// TestHandshake_DirtySuffixIgnored_Accepts is BAR-4's core AC: two builds
// of the SAME commit where one carries git describe's volatile "-dirty"
// suffix and the other doesn't must be accepted, not false-refused.
func TestHandshake_DirtySuffixIgnored_Accepts(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v0.3.0-153-gABCD", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	resp := sendHandshake(t, conn, "v0.3.0-153-gABCD-dirty")
	if resp.Error != nil {
		t.Fatalf("expected acceptance despite the -dirty suffix, got error %+v", resp.Error)
	}
	var result handshakeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Accepted {
		t.Fatalf("expected Accepted=true, got %+v", result)
	}
}

// TestHandshake_DifferentCommit_StillRefuses proves BAR-4 does not
// over-loosen the compare: two builds of genuinely DIFFERENT commits
// (different commit count AND different short sha) must still be
// refused, dirty suffix or not.
func TestHandshake_DifferentCommit_StillRefuses(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v0.3.0-153-gABCD", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	resp := sendHandshake(t, conn, "v0.3.0-154-gEFGH")
	if resp.Error == nil {
		t.Fatalf("expected a refusal for a genuinely different commit, got acceptance %+v", resp.Result)
	}
	if resp.Error.Code != protocol.ErrHandshakeVersionMismatch {
		t.Fatalf("expected %s, got %+v", protocol.ErrHandshakeVersionMismatch, resp.Error)
	}
}

// TestNormalizeVersion is a direct unit test on the pure helper (BAR-4).
func TestNormalizeVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.3.0-153-gABCD-dirty", "v0.3.0-153-gABCD"},
		{"v0.3.0-153-gABCD", "v0.3.0-153-gABCD"},
		{"v0.3.0-154-gEFGH", "v0.3.0-154-gEFGH"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeVersion(c.in); got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if normalizeVersion("v0.3.0-153-gABCD-dirty") == normalizeVersion("v0.3.0-154-gEFGH") {
		t.Fatalf("normalizeVersion must not collapse two genuinely different commits")
	}
}

// TestHandshake_MalformedFirstFrame_Refuses covers a first frame that
// isn't a handshake at all (e.g. a client that jumps straight to
// "command") -- distinct code (MET-P011) from a version mismatch.
func TestHandshake_MalformedFirstFrame_Refuses(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	id := int64(1)
	_ = conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: json.RawMessage(`{}`)})

	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrHandshakeInvalid {
		t.Fatalf("expected %s refusal, got %+v", protocol.ErrHandshakeInvalid, resp.Error)
	}
}

// TestHandshake_MissingClientVersion_Refuses covers a handshake frame
// that parses as JSON-RPC but omits clientVersion entirely.
func TestHandshake_MissingClientVersion_Refuses(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	id := int64(1)
	_ = conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: json.RawMessage(`{}`)})

	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrHandshakeInvalid {
		t.Fatalf("expected %s refusal, got %+v", protocol.ErrHandshakeInvalid, resp.Error)
	}
}

// TestHandshake_Timeout_Refuses covers a connection that never sends a
// handshake frame at all within the configured deadline.
func TestHandshake_Timeout_Refuses(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", 50*time.Millisecond)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	// Deliberately silent -- no handshake frame sent.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected the server to close the idle connection after its handshake timeout")
	}
}

// TestCommandRoundTrip_AfterAcceptedHandshake proves the v1 command
// forwarding path: once a handshake is accepted, a "command" request is
// decoded and forwarded to the wrapped Transport (observable via the
// Transport's own Commands() channel), and an ack is written back.
func TestCommandRoundTrip_AfterAcceptedHandshake(t *testing.T) {
	url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake failed: %+v", resp.Error)
	}

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "corr-1",
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	cmdBytes, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	id := int64(7)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
		t.Fatalf("write command: %v", err)
	}

	select {
	case got := <-transport.Commands():
		if got.CorrelationID != "corr-1" {
			t.Fatalf("expected forwarded command correlationId corr-1, got %q", got.CorrelationID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command to reach the transport")
	}

	var ack rpcMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("expected ack, got error %+v", ack.Error)
	}
}

// TestDeltaRelay_AfterAcceptedHandshake proves the outbound leg: a Delta
// pushed engine-side onto the wrapped Transport is relayed as a "delta"
// notification over the socket.
func TestDeltaRelay_AfterAcceptedHandshake(t *testing.T) {
	url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
		t.Fatalf("handshake failed: %+v", resp.Error)
	}

	if !transport.SendDelta(protocol.Delta{SubscriptionID: "sub-1", Tick: 5, Seq: 1, Patch: json.RawMessage(`{"x":1}`)}) {
		t.Fatal("SendDelta reported not sent")
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg rpcMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read delta notification: %v", err)
	}
	if msg.Method != methodDelta {
		t.Fatalf("expected method %q, got %q", methodDelta, msg.Method)
	}
	var d protocol.Delta
	if err := json.Unmarshal(msg.Params, &d); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if d.SubscriptionID != "sub-1" || d.Seq != 1 {
		t.Fatalf("unexpected delta: %+v", d)
	}
}

// TestCommandFailures_DistinctCodesAndCorrelationIDs is BAR-2's regression
// (round-r1 REJECT): the three post-handshake command-failure sites
// (decode/validate/send) used to all hand-build an rpcError carrying the
// handshake's own MET-P011 (ErrHandshakeInvalid) code, conflating three
// distinct failure classes under one code and never minting a fresh
// correlation ID for any of them. Each must now surface its OWN registry
// code via errs.New + a fresh correlation ID, exactly like the handshake
// error sites in the same file.
func TestCommandFailures_DistinctCodesAndCorrelationIDs(t *testing.T) {
	t.Run("decode failure", func(t *testing.T) {
		url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
		defer cleanup()
		conn := dial(t, url)
		defer func() { _ = conn.Close() }()
		if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
			t.Fatalf("handshake failed: %+v", resp.Error)
		}
		// The outer JSON-RPC envelope must itself be well-formed (an
		// envelope that fails to parse at all is silently dropped by
		// pump's inbound loop, per its own doc comment -- a different,
		// out-of-scope failure mode). What must fail is DecodeCommand's
		// json.Unmarshal into wireCommand specifically: "params" here is
		// a JSON ARRAY, which is valid JSON but cannot unmarshal into
		// wireCommand's struct shape.
		id := int64(2)
		if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: json.RawMessage(`[1,2,3]`)}); err != nil {
			t.Fatalf("write malformed command: %v", err)
		}
		var resp rpcMessage
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read response: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != protocol.ErrCommandDecodeFailed {
			t.Fatalf("expected %s, got %+v", protocol.ErrCommandDecodeFailed, resp.Error)
		}
	})

	t.Run("validation failure carries the command's correlationId", func(t *testing.T) {
		url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
		defer cleanup()
		conn := dial(t, url)
		defer func() { _ = conn.Close() }()
		if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
			t.Fatalf("handshake failed: %+v", resp.Error)
		}
		cmd := protocol.Command{
			ProtocolVersion: "0.0-wrong-version",
			CorrelationID:   "corr-bad",
			Kind:            protocol.KindPause,
			Payload:         protocol.PausePayload{},
		}
		cmdBytes, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		id := int64(2)
		if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
			t.Fatalf("write command: %v", err)
		}
		var resp rpcMessage
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read response: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != protocol.ErrCommandValidationFailed {
			t.Fatalf("expected %s, got %+v", protocol.ErrCommandValidationFailed, resp.Error)
		}
		if resp.Error.Data["correlationId"] != "corr-bad" {
			t.Fatalf("expected the command's own correlationId in error data, got %+v", resp.Error.Data)
		}
	})

	t.Run("send failure (transport closed)", func(t *testing.T) {
		url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
		defer cleanup()
		conn := dial(t, url)
		defer func() { _ = conn.Close() }()
		if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
			t.Fatalf("handshake failed: %+v", resp.Error)
		}
		_ = transport.Close() // force SendCommand to fail with ErrTransportClosed

		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   "corr-send",
			Kind:            protocol.KindPause,
			Payload:         protocol.PausePayload{},
		}
		cmdBytes, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		id := int64(2)
		if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
			t.Fatalf("write command: %v", err)
		}
		var resp rpcMessage
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read response: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != protocol.ErrCommandSendFailed {
			t.Fatalf("expected %s, got %+v", protocol.ErrCommandSendFailed, resp.Error)
		}
	})

	// MUTATION-PROOF target: the three command-failure codes must be
	// DISTINCT from each other and from ErrHandshakeInvalid -- reverting
	// any one of the three call sites back to a hardcoded "MET-P011"
	// (the round-r1 REJECT's actual bug) collapses this set and turns
	// this assertion RED.
	codes := map[string]bool{
		protocol.ErrCommandDecodeFailed:     true,
		protocol.ErrCommandValidationFailed: true,
		protocol.ErrCommandSendFailed:       true,
	}
	if len(codes) != 3 {
		t.Fatalf("expected 3 distinct command-failure codes, got %d: %+v", len(codes), codes)
	}
	if codes[protocol.ErrHandshakeInvalid] {
		t.Fatalf("a command-failure code collided with ErrHandshakeInvalid (MET-P011) -- the exact round-r1 bug")
	}
}

// TestRegression_PumpConcurrentClose_NoRace is a targeted regression test
// for a data race -race caught during increment-1 development: pump's
// inbound (ServeHTTP's own goroutine) and outbound (the delta/result/event
// relay goroutine) sides both called a closeDone() closure guarding a bare
// `var closeOnce bool` -- two goroutines racing that flag's read/write
// around close(done). Fixed with sync.Once (server.go's pump doc comment).
//
// BAR-3 (round-r1 follow-up): the original shape of this test ran its
// attempts SEQUENTIALLY, one httptest server/connection at a time. That
// leaves the actual race window too narrow to trip reliably -- empirically
// confirmed against a scratch revert of server.go's sync.Once back to a
// bare bool: 500 SEQUENTIAL iterations of the send-delta-vs-close-conn
// race produced ZERO -race failures, because within a single connection
// the inbound and outbound goroutines' respective error paths rarely land
// close enough in time to actually overlap on the same memory. Running
// MANY attempts CONCURRENTLY -- many httptest servers and pump loops all
// competing for the same OS scheduler at once -- reproduces the original
// failure reliably: confirmed, reverting server.go's sync.Once to a bare
// `var closeOnce bool` guard and running `go test -race -run
// TestRegression_PumpConcurrentClose_NoRace` against THIS concurrent
// shape fails on every one of 3 consecutive runs (WARNING: DATA RACE on
// server.go's pump.func1, exactly the closeDone guard). Restored to
// sync.Once, the same 3 runs are green.
func TestRegression_PumpConcurrentClose_NoRace(t *testing.T) {
	const attempts = 50
	var outer sync.WaitGroup
	outer.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer outer.Done()
			url, transport, cleanup := newTestServer(t, "v1.2.3", time.Second)
			defer cleanup()
			conn := dial(t, url)
			defer func() { _ = conn.Close() }()
			if resp := sendHandshake(t, conn, "v1.2.3"); resp.Error != nil {
				t.Errorf("handshake failed: %+v", resp.Error)
				return
			}
			// A shared `start` barrier holds both goroutines at the gate
			// until BOTH are scheduled and blocked on the receive, then
			// close(start) releases them in the same instant -- racing
			// the outbound path (a delta arriving, relayed, then the
			// connection closing under it) against the inbound path
			// (closing the client conn directly, making ServeHTTP's own
			// ReadMessage loop return an error) as close together as
			// possible.
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				transport.SendDelta(protocol.Delta{SubscriptionID: "sub-1", Tick: 1, Seq: 1, Patch: json.RawMessage(`{}`)})
			}()
			go func() {
				defer wg.Done()
				<-start
				_ = conn.Close()
			}()
			close(start)
			wg.Wait()
		}()
	}
	outer.Wait()
}

// unusedHTTPImportGuard keeps net/http imported for the http.Handler type
// assertion below (documentation-as-test: *Server must satisfy
// http.Handler, which ServeHTTP's signature already establishes at
// compile time -- this line makes that explicit and named so a future
// refactor that quietly breaks the interface fails to compile here with
// a readable name instead of a cryptic call-site error elsewhere).
var _ http.Handler = (*Server)(nil)
