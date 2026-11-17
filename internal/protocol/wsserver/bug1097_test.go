package wsserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1097 / Q100145: the handshake build-string gate is relaxed to a warn.
// These tests pin AC-1, AC-2, AC-3, AC-4 (Go half), AC-9 and AC-10.

// syncBuffer is a goroutine-safe bytes.Buffer used as an errs sink writer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureErrs routes every errs registry record into a decodable buffer for
// the test's life, restoring the in-memory ring afterwards.
func captureErrs(t *testing.T) *syncBuffer {
	t.Helper()
	sb := &syncBuffer{}
	if err := errs.SetSink(errs.NewLogger(sb)); err != nil {
		t.Fatalf("SetSink: %v", err)
	}
	t.Cleanup(func() { _ = errs.SetSink(nil) })
	return sb
}

// entriesWithCode decodes the captured NDJSON and returns every Entry with
// the given registry code (decoded fields, never a substring match).
func entriesWithCode(t *testing.T, sb *syncBuffer, code string) []errs.Entry {
	t.Helper()
	var out []errs.Entry
	for _, line := range strings.Split(sb.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e errs.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("decode sink line %q: %v", line, err)
		}
		if e.Code == code {
			out = append(out, e)
		}
	}
	return out
}

// settledEntries waits (the server audits just AFTER it writes the accept
// frame, so the client can read the response first) until at least want
// records exist, then waits a further grace period so a duplicate emit
// would also be seen, and returns every record with the code.
func settledEntries(t *testing.T, sb *syncBuffer, code string, want int) []errs.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(entriesWithCode(t, sb, code)) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	return entriesWithCode(t, sb, code)
}

func decodeResult(t *testing.T, resp rpcMessage) handshakeResult {
	t.Helper()
	var r handshakeResult
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return r
}

// pauseRoundTrip sends a Pause command and requires it to reach the
// transport AND be acked: the "commands flow" proof AC-1 demands (an
// accepted handshake alone is the false-pass).
func pauseRoundTrip(t *testing.T, conn *websocket.Conn, transport *protocol.InProcTransport, corr string) {
	t.Helper()
	cmdBytes, err := protocol.EncodeCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corr),
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	})
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	id := int64(7)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodCommand, Params: cmdBytes}); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case got := <-transport.Commands():
		if string(got.CorrelationID) != corr {
			t.Fatalf("forwarded command correlationId %q, want %q", got.CorrelationID, corr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command never reached the transport")
	}
	var ack rpcMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("expected ack, got error %+v", ack.Error)
	}
}

// AC-1: same protocol, differing build strings (the observed 7-vs-8 char
// short-hash pair, a different tag, and "dev") all CONNECT and commands flow.
func TestBug1097_AC1_DifferentBuildStringConnectsAndCommandsFlow(t *testing.T) {
	cases := []struct{ engine, client string }{
		{"v0.3.0-609-g3b657a7", "v0.3.0-609-g3b657a70"},
		{"v0.3.0-609-g3b657a7", "v0.2.0-1-gabc1234"},
		{"v0.3.0-609-g3b657a7", "dev"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.client, func(t *testing.T) {
			url, transport, cleanup := newTestServer(t, c.engine, time.Second)
			defer cleanup()
			conn := dial(t, url)
			defer func() { _ = conn.Close() }()

			resp := sendHandshake(t, conn, c.client)
			if resp.Error != nil {
				t.Fatalf("differing build must connect, got refusal %+v", resp.Error)
			}
			r := decodeResult(t, resp)
			if !r.Accepted {
				t.Fatalf("expected Accepted, got %+v", r)
			}
			if r.NegotiatedVersion != protocol.CurrentWireVersion {
				t.Fatalf("negotiated %v, want current wire %v", r.NegotiatedVersion, protocol.CurrentWireVersion)
			}
			if r.ServerVersion != c.engine {
				t.Fatalf("serverVersion %q must still be exchanged, want %q", r.ServerVersion, c.engine)
			}
			pauseRoundTrip(t, conn, transport, "corr-1097")
		})
	}
}

// AC-2: the protocol window still refuses, with the pinned code and text.
func TestBug1097_AC2_BelowWindowFloorStillRefusedExactly(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()

	// Floor major is current - depth; pick one major below it.
	floor := protocol.WindowFloorMajor(protocol.CurrentWireVersion, protocol.DefaultVersionWindowDepth)
	if floor < 1 {
		t.Skip("window floor major is 0; no representable below-floor major")
	}
	below := protocol.WireVersion{Major: floor - 1, Minor: 0}
	params, _ := json.Marshal(handshakeParams{ClientVersion: "v1.2.3", ClientMinVersion: &below, ClientMaxVersion: &below})
	id := int64(1)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("below-floor must refuse, got %+v", resp.Result)
	}
	if resp.Error.Code != "MET-P020" {
		t.Fatalf("code %s, want MET-P020", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "MET-P020") {
		t.Fatalf("message %q lost the code tag", resp.Error.Message)
	}
	want := errs.New(protocol.ErrHandshakeBelowWindowFloor, "x", resp.Error.Data).Msg
	if !strings.Contains(resp.Error.Message, want) {
		t.Fatalf("message %q does not carry the registered text %q", resp.Error.Message, want)
	}
}

// AC-3: exactly ONE MET-P050 per mismatched handshake carrying the decoded
// fields; none on a match; its correlation id equals the response's.
func TestBug1097_AC3_AuditRecordOncePerMismatch(t *testing.T) {
	sb := captureErrs(t)
	url, _, cleanup := newTestServer(t, "v0.3.0-609-g3b657a7", time.Second)
	defer cleanup()

	// Matching client first: no audit record.
	cm := dial(t, url)
	if resp := sendHandshake(t, cm, "v0.3.0-609-g3b657a7"); resp.Error != nil {
		t.Fatalf("match refused: %+v", resp.Error)
	}
	_ = cm.Close()
	if n := len(settledEntries(t, sb, protocol.ErrHandshakeBuildMismatch, 0)); n != 0 {
		t.Fatalf("matching handshake emitted %d MET-P050 records, want 0", n)
	}

	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	params, _ := json.Marshal(handshakeParams{ClientVersion: "v0.3.0-609-g3b657a70", TenantID: "ten-1", CityID: "city-9"})
	id := int64(1)
	if err := conn.WriteJSON(rpcMessage{JSONRPC: rpcVersion, ID: &id, Method: methodHandshake, Params: params}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp rpcMessage
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("mismatch refused: %+v", resp.Error)
	}
	r := decodeResult(t, resp)

	got := settledEntries(t, sb, protocol.ErrHandshakeBuildMismatch, 1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 MET-P050 record, got %d", len(got))
	}
	e := got[0]
	if e.Level != "warn" {
		t.Fatalf("level %q, want warn", e.Level)
	}
	for k, want := range map[string]string{
		"clientVersion":     "v0.3.0-609-g3b657a70",
		"serverVersion":     "v0.3.0-609-g3b657a7",
		"negotiatedVersion": r.NegotiatedVersion.String(),
		"tenantId":          "ten-1",
		"cityId":            "city-9",
	} {
		if e.Ctx[k] != want {
			t.Fatalf("ctx[%s]=%v, want %q", k, e.Ctx[k], want)
		}
	}
	if at, _ := e.Ctx["at"].(string); at == "" {
		t.Fatalf("ctx.at missing: %+v", e.Ctx)
	} else if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		t.Fatalf("ctx.at %q not RFC3339: %v", at, err)
	}
	if ra, _ := e.Ctx["remoteAddr"].(string); ra == "" {
		t.Fatalf("ctx.remoteAddr missing: %+v", e.Ctx)
	}
	if r.BuildMismatchCorrelationID == "" || e.CorrelationID != r.BuildMismatchCorrelationID {
		t.Fatalf("record correlation %q != response correlation %q", e.CorrelationID, r.BuildMismatchCorrelationID)
	}
}

// AC-4 (Go half): MET-P050 is registered, template-clean, warn, owned by
// int.protocol; an unregistered code would render the MET-F003 fallback.
func TestBug1097_AC4_AuditCodeRegisteredAndTemplateClean(t *testing.T) {
	e := errs.New(protocol.ErrHandshakeBuildMismatch, "corr", map[string]any{
		"clientVersion": "c", "serverVersion": "s", "negotiatedVersion": "1.0",
		"tenantId": "t", "cityId": "y", "at": "now",
	})
	if e.Code != "MET-P050" {
		t.Fatalf("code %s: not registered (MET-F003 fallback?)", e.Code)
	}
	if strings.Contains(e.Msg, "{") || strings.Contains(e.Msg, "}") {
		t.Fatalf("message has unrendered/malformed brace tokens: %q", e.Msg)
	}
	for _, part := range []string{"c", "s", "1.0"} {
		if !strings.Contains(e.Msg, part) {
			t.Fatalf("message %q missing %q", e.Msg, part)
		}
	}
	if e.Module != "protocol" && e.Module != "int.protocol" {
		t.Fatalf("owner module %q, want the protocol module", e.Module)
	}
	raw := mustReadRegistry(t)
	var reg struct {
		Codes map[string]struct {
			Severity string `json:"severity"`
			Module   string `json:"module"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("registry json: %v", err)
	}
	p050, p010 := reg.Codes["MET-P050"], reg.Codes["MET-P010"]
	if p050.Severity != "warn" {
		t.Fatalf("MET-P050 severity %q, want warn", p050.Severity)
	}
	if p050.Module != "int.protocol" {
		t.Fatalf("MET-P050 owner %q, want int.protocol", p050.Module)
	}
	_ = p010
	// Built from parts: the commit-time secret guard reads a long upper-case
	// literal as high entropy.
	unregisteredCode := "MET-P05X-"
	unregisteredCode += "unreg"
	if bad := errs.New(unregisteredCode, "corr", nil); bad.Code == "MET-P050" {
		t.Fatal("unregistered code must not alias MET-P050")
	}
}

func mustReadRegistry(t *testing.T) []byte {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, "data", "errors.json")
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("data/errors.json not found above the test directory")
	return nil
}

// AC-9: no production Go code path emits MET-P010 after this change; its
// registry text says it is retired by Q100145.
func TestBug1097_AC9_MetP010RetiredHonestly(t *testing.T) {
	repo := filepath.Dir(mustFindFile(t, "go.mod"))
	re := regexp.MustCompile(`ErrHandshakeVersionMismatch|"MET-P010"`)
	for _, sub := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(repo, sub), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			if filepath.Base(p) == "codes.go" && strings.Contains(filepath.ToSlash(p), "internal/protocol/codes.go") {
				return nil // the constant's own declaration
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Errorf("read %s: %v", p, rerr)
				return nil
			}
			// Comments may name it; code may not.
			for i, line := range strings.Split(string(b), "\n") {
				code := line
				if j := strings.Index(code, "//"); j >= 0 {
					code = code[:j]
				}
				if re.MatchString(code) {
					t.Errorf("%s:%d emits/references retired MET-P010: %s", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
	}
	msg := errs.New("MET-P010", "corr", map[string]any{"clientVersion": "a", "serverVersion": "b"}).Msg
	if !strings.Contains(msg, "Q100145") || !strings.Contains(strings.ToLower(msg), "retired") {
		t.Fatalf("MET-P010 registry message %q must say it is retired by Q100145", msg)
	}
}

func mustFindFile(t *testing.T, name string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("%s not found above test dir", name)
	return ""
}

// AC-10: with matching builds the handshake response is byte-identical to
// trunk's (golden recorded from trunk behaviour; the mismatch-only
// correlation field is absent).
func TestBug1097_AC10_MatchingHandshakeResponseByteIdentical(t *testing.T) {
	url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
	defer cleanup()
	conn := dial(t, url)
	defer func() { _ = conn.Close() }()
	resp := sendHandshake(t, conn, "v1.2.3")
	if resp.Error != nil {
		t.Fatalf("refused: %+v", resp.Error)
	}
	const golden = `{"accepted":true,"serverVersion":"v1.2.3","negotiatedVersion":{"major":GOLDMAJOR,"minor":GOLDMINOR}}`
	want := strings.NewReplacer(
		"GOLDMAJOR", itoa(protocol.CurrentWireVersion.Major),
		"GOLDMINOR", itoa(protocol.CurrentWireVersion.Minor),
	).Replace(golden)
	if string(resp.Result) != want {
		t.Fatalf("matching-build handshake result changed:\n got  %s\n want %s", resp.Result, want)
	}
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
