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

// shim_test.go — FEAT-1972079936 Phase 0 increment 2: the version WINDOW
// (AC-3), the in-window-connects half of AC-4, the one concrete compat
// shim proving the mechanism end-to-end, and its AC-6 determinism proof.

// --- renameJSONField / shimForOffset: unit level -----------------------

func TestRenameJSONField_RenamesAndPreservesOtherKeys(t *testing.T) {
	raw := json.RawMessage(`{"correlation_id":"abc","kind":"Pause","issuedAtTick":5}`)
	got, err := renameJSONField(raw, "correlation_id", "correlationId")
	if err != nil {
		t.Fatalf("renameJSONField: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, has := m["correlation_id"]; has {
		t.Fatalf("expected the old key to be gone, got %v", m)
	}
	if m["correlationId"] != "abc" {
		t.Fatalf("expected correlationId=abc, got %v", m["correlationId"])
	}
	if m["kind"] != "Pause" || m["issuedAtTick"] != float64(5) {
		t.Fatalf("expected untouched sibling keys to survive, got %v", m)
	}
}

func TestRenameJSONField_NoOpWhenFromAbsent(t *testing.T) {
	raw := json.RawMessage(`{"correlationId":"already-current"}`)
	got, err := renameJSONField(raw, "correlation_id", "correlationId")
	if err != nil {
		t.Fatalf("renameJSONField: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if m["correlationId"] != "already-current" {
		t.Fatalf("expected the already-current key untouched, got %v", m)
	}
}

func TestRenameJSONField_DoesNotOverwriteExistingTo(t *testing.T) {
	// A malformed payload carrying BOTH keys must not have the legacy
	// value silently clobber the current one -- see renameJSONField's own
	// doc comment for why this is deliberate (a well-formed single-family
	// payload never carries both).
	raw := json.RawMessage(`{"correlation_id":"legacy","correlationId":"current"}`)
	got, err := renameJSONField(raw, "correlation_id", "correlationId")
	if err != nil {
		t.Fatalf("renameJSONField: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if m["correlationId"] != "current" {
		t.Fatalf("expected the current key to win, got %v", m["correlationId"])
	}
}

func TestShimForOffset(t *testing.T) {
	if _, ok := shimForOffset(0); ok {
		t.Fatal("offset 0 (current major) must never have a shim")
	}
	if _, ok := shimForOffset(-1); ok {
		t.Fatal("a negative offset must never have a shim")
	}
	s, ok := shimForOffset(1)
	if !ok {
		t.Fatal("expected offset 1 to have a registered shim (this increment's concrete example)")
	}
	if s.adaptCommandIn == nil || s.adaptResultOut == nil {
		t.Fatal("expected the offset-1 shim to define both directions")
	}
	if _, ok := shimForOffset(2); ok {
		t.Fatal("offset 2 has no shim registered yet in this increment (only offset 1 is Phase 0's concrete example)")
	}
}

// --- AC-6 determinism: shim never changes the DECODED Command ---------

// TestShim_DecodesIdenticallyToUnshimmedCommand is AC-6's exact proof at
// the smallest possible scope: the SAME logical command, sent once in the
// current major's wire shape and once in the legacy (offset-1) shape,
// must decode to a byte-identical protocol.Command (protocolVersion
// included) once the offset-1 shim's adaptCommandIn AND the BUG-471
// protocolVersion-normalize step have rewritten the legacy shape — proving
// the shim adapts WIRE bytes only, never the decoded value the engine
// sees.
//
// BUG-471 fix (this test was REWRITTEN, not just extended): the ORIGINAL
// version of this test stamped BOTH the "current-shape" fixture AND the
// "legacy-shape" fixture with the SAME literal protocolVersion ("2.0"),
// which made it structurally incapable of catching BUG-471 -- a real
// offset-1 (legacy-major) client declares its OWN older version ("1.0"),
// not the current build's. A test that never constructs that divergence
// cannot distinguish "the shim leaves protocolVersion alone" (the bug)
// from "the shim normalizes it" (the fix): both pass when the inputs
// already agree. This version declares the legacy client's real, OLDER
// version and asserts the decoded result is normalized to CURRENT's
// canonical string despite that -- the case that actually discriminates
// the two implementations.
func TestShim_DecodesIdenticallyToUnshimmedCommand(t *testing.T) {
	current := protocol.Command{
		ProtocolVersion: protocol.CurrentWireVersion.String(),
		CorrelationID:   "det-corr-1",
		IssuedAtTick:    7,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	currentBytes, err := protocol.EncodeCommand(current)
	if err != nil {
		t.Fatalf("encode current-shape command: %v", err)
	}

	// The legacy (offset-1) client sends the SAME logical command, but
	// declares ITS OWN (older, offset-1) protocolVersion "1.0" -- NOT
	// current's -- and carries its correlation id under the old
	// snake_case key. A real offset-1 client's major is
	// CurrentWireVersion.Major-1; Phase 0 pins an older major's minor to 0
	// (negotiateVersion's own doc comment), so "1.0" is what an actual
	// offset-1 connection declares when current is "2.0".
	legacyVersion := (protocol.WireVersion{Major: protocol.CurrentWireVersion.Major - 1, Minor: 0}).String()
	legacyBytes := json.RawMessage(`{"protocolVersion":"` + legacyVersion + `","correlation_id":"det-corr-1","issuedAtTick":7,"kind":"Pause","payload":{}}`)

	shim, ok := shimForOffset(1)
	if !ok {
		t.Fatal("expected a registered offset-1 shim")
	}
	adapted, err := shim.adaptCommandIn(legacyBytes)
	if err != nil {
		t.Fatalf("adaptCommandIn: %v", err)
	}
	// BUG-471: adaptCommandIn alone only renames the correlation-id key --
	// the protocolVersion normalize step handleCommand applies (server.go)
	// is exercised here explicitly, since this test asserts the boundary
	// AC-6 requires (decoded Command byte-identical, version tag
	// included), not just the correlation-id half shim_test.go's other
	// cases already cover.
	adapted, err = normalizeProtocolVersionField(adapted, protocol.CurrentWireVersion.String())
	if err != nil {
		t.Fatalf("normalizeProtocolVersionField: %v", err)
	}

	gotFromCurrent, err := protocol.DecodeCommand(currentBytes)
	if err != nil {
		t.Fatalf("decode current-shape bytes: %v", err)
	}
	gotFromLegacy, err := protocol.DecodeCommand(adapted)
	if err != nil {
		t.Fatalf("decode shimmed legacy-shape bytes: %v", err)
	}

	if gotFromCurrent.ProtocolVersion != gotFromLegacy.ProtocolVersion ||
		gotFromCurrent.CorrelationID != gotFromLegacy.CorrelationID ||
		gotFromCurrent.IssuedAtTick != gotFromLegacy.IssuedAtTick ||
		gotFromCurrent.Kind != gotFromLegacy.Kind {
		t.Fatalf("decoded Command differs after shimming: from-current=%+v from-legacy(shimmed)=%+v", gotFromCurrent, gotFromLegacy)
	}
	if gotFromLegacy.ProtocolVersion != protocol.CurrentWireVersion.String() {
		t.Fatalf("expected the shimmed legacy Command's ProtocolVersion normalized to canonical %q, got %q", protocol.CurrentWireVersion.String(), gotFromLegacy.ProtocolVersion)
	}
	if _, ok := gotFromCurrent.Payload.(protocol.PausePayload); !ok {
		t.Fatalf("expected protocol.PausePayload, got %T", gotFromCurrent.Payload)
	}
	if _, ok := gotFromLegacy.Payload.(protocol.PausePayload); !ok {
		t.Fatalf("expected protocol.PausePayload, got %T", gotFromLegacy.Payload)
	}

	// MUTATION-PROVE (correlation id): without the shim, decoding the
	// legacy bytes directly would produce a DIFFERENT CorrelationID
	// (empty, since "correlationId" is absent) -- proving this test
	// actually exercises the rename shim, not a vacuously-true comparison.
	gotUnshimmed, err := protocol.DecodeCommand(legacyBytes)
	if err != nil {
		t.Fatalf("decode raw legacy bytes (unshimmed): %v", err)
	}
	if gotUnshimmed.CorrelationID == gotFromCurrent.CorrelationID {
		t.Fatal("test invalid: decoding the UNSHIMMED legacy bytes must NOT already match (else the shim proves nothing)")
	}

	// MUTATION-PROVE (BUG-471, the case the original test could not
	// catch): without the protocolVersion-normalize step, the shim's own
	// output (adaptCommandIn's result, BEFORE normalizeProtocolVersionField)
	// still carries the legacy client's OWN "1.0"-style version, which
	// DIFFERS from current's canonical string -- proving this test would
	// fail if the normalize step were skipped, i.e. it actually pins the
	// fix rather than passing vacuously.
	renamedOnly, err := shim.adaptCommandIn(legacyBytes)
	if err != nil {
		t.Fatalf("adaptCommandIn (re-run for the mutation check): %v", err)
	}
	gotRenamedOnly, err := protocol.DecodeCommand(renamedOnly)
	if err != nil {
		t.Fatalf("decode rename-only (not yet version-normalized) bytes: %v", err)
	}
	if gotRenamedOnly.ProtocolVersion == protocol.CurrentWireVersion.String() {
		t.Fatal("test invalid: the rename-only (pre-normalize) decode must NOT already be canonical (else BUG-471 could never have been observed)")
	}
}

// --- BUG-636: synchronizing tests that mutate protocol.CurrentWireVersion
// against an httptest server ------------------------------------------
//
// Every test below temporarily overrides the package-global
// protocol.CurrentWireVersion for the duration of an httptest.Server's
// life, then restores it via a deferred write. That restore races the
// SERVER-SIDE per-connection goroutine's reads of the same global
// (negotiateVersion/handshake's window check, and pump's own shim-offset
// computation) unless the test can PROVE every such goroutine has already
// returned before the restore runs.
//
// httptest.Server.Close() does NOT provide that proof here: Upgrade()
// hijacks the TCP connection before handshake() ever runs (server.go),
// which takes the connection out of net/http's own in-flight-request
// tracking -- Close() can return while a hijacked connection's ServeHTTP
// call (successful handshake+pump, OR an early handshake refusal) is
// still executing. The client having already read a response over the
// socket does not help either: the race detector does not treat plain
// socket I/O as a happens-before edge, only real synchronization
// primitives (channels, WaitGroup, mutex, atomic, `go`) count.
//
// wrapServeHTTPWithWaitGroup + closeAndWaitForServer give the detector a
// real edge: every ServeHTTP call increments the WaitGroup on entry and
// decrements it on return (whichever path returns -- refused handshake or
// a fully pumped-and-torn-down connection), and the test waits on that
// WaitGroup, AFTER closing every client conn, before its own defers
// restore protocol.CurrentWireVersion / close the server. That wait is a
// genuine happens-before edge, so the race detector can see that every
// global read this connection's handling could ever perform strictly
// precedes the restore write.
func wrapServeHTTPWithWaitGroup(h http.Handler, wg *sync.WaitGroup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		h.ServeHTTP(w, r)
	})
}

func closeAndWaitForServer(t *testing.T, wg *sync.WaitGroup, conns ...*websocket.Conn) {
	t.Helper()
	for _, c := range conns {
		_ = c.Close()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BUG-636: timed out waiting for server-side ServeHTTP goroutines to return")
	}
}

// --- AC-3/AC-4 end-to-end: window negotiation + in-window shim --------

// TestWindowNegotiation_CurrentAndOneBack_NegotiateOwnMajor_TwoBack_Refused
// is AC-3's exact mutation, using MAJORS per Aaron's ruling (window = 3
// MAJOR versions: current + 2 back): configure the server with
// WithVersionWindowDepth(1) (a 2-major window: current + 1 back). Connect
// three clients declaring ceilings at current, current-1, and current-2
// majors. (a) and (b) must connect, each negotiating ITS OWN major (not
// silently current's); (c) must be refused. This is the case that
// discriminates "window boundary enforced at N" from both "anything not
// exactly current refused" (inc1's old rule) and "everything accepted
// regardless of window" (the doc's own false-pass guard).
func TestWindowNegotiation_CurrentAndOneBack_NegotiateOwnMajor_TwoBack_Refused(t *testing.T) {
	originalVersion := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = originalVersion }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 3, Minor: 0}

	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithVersionWindowDepth(1))
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	// (a) current major.
	connCurrent := dial(t, wsURL)
	defer func() { _ = connCurrent.Close() }()
	_, resCurrent := sendHandshakeFull(t, connCurrent, handshakeParams{ClientVersion: "v1.2.3"})
	if resCurrent.NegotiatedVersion != (protocol.WireVersion{Major: 3, Minor: 0}) {
		t.Fatalf("(a) NegotiatedVersion = %+v, want current 3.0", resCurrent.NegotiatedVersion)
	}

	// (b) current-1 major -- in-window, must connect and negotiate ITS
	// OWN major (2.0), not current's (3.0).
	connOneBack := dial(t, wsURL)
	defer func() { _ = connOneBack.Close() }()
	oneBackMax := protocol.WireVersion{Major: 2, Minor: 0}
	_, resOneBack := sendHandshakeFull(t, connOneBack, handshakeParams{
		ClientVersion:    "v1.2.3",
		ClientMinVersion: &oneBackMax,
		ClientMaxVersion: &oneBackMax,
	})
	if resOneBack.NegotiatedVersion != oneBackMax {
		t.Fatalf("(b) NegotiatedVersion = %+v, want the client's own ceiling %+v (in-window, not silently current)", resOneBack.NegotiatedVersion, oneBackMax)
	}

	// (c) current-2 major -- below the N=1 window floor (floor = 3-1 = 2)
	// -- must be refused.
	connTwoBack := dial(t, wsURL)
	defer func() { _ = connTwoBack.Close() }()
	twoBackMax := protocol.WireVersion{Major: 1, Minor: 0}
	params, _ := json.Marshal(handshakeParams{ClientVersion: "v1.2.3", ClientMinVersion: &twoBackMax, ClientMaxVersion: &twoBackMax})
	id := int64(1)
	req := rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}
	if err := connTwoBack.WriteJSON(req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var resp rpcMessage
	if err := connTwoBack.ReadJSON(&resp); err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("(c) expected a below-window-floor refusal, got acceptance %+v", resp.Result)
	}
	// Specific code check (not just "some refusal happened somewhere",
	// per AC-4's false-pass guard). Inc3 retires inc2's temporary reuse of
	// ErrHandshakeVersionMismatch for this case -- the below-floor refusal
	// now carries its OWN dedicated code, ErrHandshakeBelowWindowFloor
	// (MET-P020), distinct from the build-string mismatch code (MET-P010).
	if resp.Error.Code != protocol.ErrHandshakeBelowWindowFloor {
		t.Fatalf("(c) expected code %s, got %s", protocol.ErrHandshakeBelowWindowFloor, resp.Error.Code)
	}
	if resp.Error.Code == protocol.ErrHandshakeVersionMismatch {
		t.Fatal("below-floor refusal must NOT reuse ErrHandshakeVersionMismatch (that code's registered meaning is the separate build-string check)")
	}
	if resp.Error.Data["windowFloorMajor"] != float64(2) {
		t.Fatalf("(c) expected windowFloorMajor=2 in error context, got %+v", resp.Error.Data)
	}

	// BUG-636: wait for every ServeHTTP call (a, b, and c's refusal) to
	// return before the deferred restore of protocol.CurrentWireVersion
	// runs -- see the doc comment on closeAndWaitForServer.
	closeAndWaitForServer(t, &srvWG, connCurrent, connOneBack, connTwoBack)
}

// TestWindowNegotiation_FloorItself_Accepted is AC-4's boundary-inclusive
// proof: a client declaring EXACTLY the floor major must be accepted, not
// off-by-one excluded.
func TestWindowNegotiation_FloorItself_Accepted(t *testing.T) {
	originalVersion := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = originalVersion }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 3, Minor: 0}

	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithVersionWindowDepth(1))
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, wsURL)
	defer func() { _ = conn.Close() }()
	floor := protocol.WireVersion{Major: 2, Minor: 0} // 3 - 1 = floor
	_, result := sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3", ClientMinVersion: &floor, ClientMaxVersion: &floor})
	if result.NegotiatedVersion != floor {
		t.Fatalf("expected the floor version itself to be accepted and negotiated as-is, got %+v", result.NegotiatedVersion)
	}

	// BUG-636: wait for the ServeHTTP call to return before the deferred
	// restore of protocol.CurrentWireVersion runs -- see the doc comment
	// on closeAndWaitForServer.
	closeAndWaitForServer(t, &srvWG, conn)
}

// TestInWindowOlderClient_RealCommandRoundTrips_ViaShim proves the other
// half of increment 2's promise: not just that an in-window older client
// CONNECTS, but that a REAL command sent on that connection round-trips
// correctly through the offset-1 shim -- the ack for a well-formed
// shimmed command must succeed exactly like an unshimmed current-major
// command would.
func TestInWindowOlderClient_RealCommandRoundTrips_ViaShim(t *testing.T) {
	originalVersion := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = originalVersion }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 2, Minor: 0}

	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithVersionWindowDepth(1))
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, wsURL)
	defer func() { _ = conn.Close() }()
	oneBack := protocol.WireVersion{Major: 1, Minor: 0} // offset 1 from current (2.0)
	_, result := sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3", ClientMinVersion: &oneBack, ClientMaxVersion: &oneBack})
	if result.NegotiatedVersion != oneBack {
		t.Fatalf("expected negotiation onto the client's own major 1.0, got %+v", result.NegotiatedVersion)
	}

	// Send a real command using the LEGACY (offset-1) wire shape: the
	// envelope's correlation id under "correlation_id" -- the ONE
	// deliberately-introduced difference this increment's shim exists to
	// bridge (shim.go's doc comment).
	legacyCmdParams := json.RawMessage(`{"protocolVersion":"1.0","correlation_id":"legacy-corr","issuedAtTick":0,"kind":"Pause","payload":{}}`)
	cmdID := int64(2)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &cmdID, Method: methodCommand, Params: legacyCmdParams}); err != nil {
		t.Fatalf("write legacy-shape command: %v", err)
	}

	var ack rpcMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("read command ack: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("expected the shimmed command to ack successfully, got error %+v", ack.Error)
	}

	// The transport must have received a fully-decoded Command with the
	// CURRENT major's field names resolved correctly -- i.e. the shim
	// actually ran, not merely "some ack came back."
	select {
	case got := <-transport.Commands():
		if got.CorrelationID != "legacy-corr" {
			t.Fatalf("expected CorrelationID 'legacy-corr' (shim-recovered from correlation_id), got %q", got.CorrelationID)
		}
		if got.Kind != protocol.KindPause {
			t.Fatalf("expected Kind Pause, got %q", got.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the shimmed command to reach the transport")
	}

	// BUG-636: wait for the ServeHTTP call (handshake + pump, including the
	// command just round-tripped above) to return before the deferred
	// restore of protocol.CurrentWireVersion runs -- see the doc comment
	// on closeAndWaitForServer. Without this, the server-side pump
	// goroutine's read of protocol.CurrentWireVersion (server.go's shim-
	// offset computation) races this test's own restore under -race,
	// intermittently, since httptest.Server.Close() does not wait for
	// hijacked websocket connections.
	closeAndWaitForServer(t, &srvWG, conn)
}

// TestInWindowOlderClient_MalformedLegacyShape_Refused proves the shim
// path still surfaces a decode failure as a typed error (not a silent
// drop or a panic) when the legacy-shaped payload is genuinely broken.
func TestInWindowOlderClient_MalformedLegacyShape_Refused(t *testing.T) {
	originalVersion := protocol.CurrentWireVersion
	defer func() { protocol.CurrentWireVersion = originalVersion }()
	protocol.CurrentWireVersion = protocol.WireVersion{Major: 2, Minor: 0}

	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	srv := New(transport, "v1.2.3", time.Second, WithVersionWindowDepth(1))
	var srvWG sync.WaitGroup
	httpSrv := httptest.NewServer(wrapServeHTTPWithWaitGroup(srv, &srvWG))
	defer func() {
		httpSrv.Close()
		_ = transport.Close()
	}()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	conn := dial(t, wsURL)
	defer func() { _ = conn.Close() }()
	oneBack := protocol.WireVersion{Major: 1, Minor: 0}
	sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3", ClientMinVersion: &oneBack, ClientMaxVersion: &oneBack})

	// A valid JSON value (so the outer rpcMessage itself still marshals
	// cleanly) that is NOT a JSON object -- renameJSONField's own
	// json.Unmarshal into a map[string]json.RawMessage fails on this,
	// exercising the shim's error path specifically (as opposed to a
	// non-JSON payload, which would fail at the client's own WriteJSON
	// step before ever reaching the server).
	notAnObject := json.RawMessage(`"not-an-object"`)
	cmdID := int64(2)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &cmdID, Method: methodCommand, Params: notAnObject}); err != nil {
		t.Fatalf("write malformed command: %v", err)
	}
	var ack rpcMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("read command ack: %v", err)
	}
	if ack.Error == nil {
		t.Fatal("expected a decode-failure error for a malformed legacy-shape payload")
	}
	if ack.Error.Code != protocol.ErrCommandDecodeFailed {
		t.Fatalf("expected code %s, got %s", protocol.ErrCommandDecodeFailed, ack.Error.Code)
	}

	// BUG-636: wait for the ServeHTTP call to return before the deferred
	// restore of protocol.CurrentWireVersion runs -- see the doc comment
	// on closeAndWaitForServer.
	closeAndWaitForServer(t, &srvWG, conn)
}
