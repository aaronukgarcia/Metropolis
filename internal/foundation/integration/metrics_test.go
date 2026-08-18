package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// (a) Counters record and Snapshot reports them correctly, per tier, per
// integration.
func TestRegistry_RecordAndSnapshot(t *testing.T) {
	reg := NewRegistry()

	depth := Depth{T0: 2, T1: 5, T1OnDisk: 1, T2: true}
	state := StateConnected

	m := reg.Register("freight",
		WithDepthFunc(func() Depth { return depth }),
		WithStateFunc(func() ConnState { return state }),
	)

	m.RecordDelivered(10)
	m.RecordDelivered(25)
	m.RecordError(fmt.Errorf("scripted failure"))

	snap := reg.Snapshot()
	if len(snap.Integrations) != 1 {
		t.Fatalf("Integrations len = %d, want 1", len(snap.Integrations))
	}
	got := snap.Integrations[0]

	if got.Name != "freight" {
		t.Fatalf("Name = %q, want freight", got.Name)
	}
	if got.State != "up" {
		t.Fatalf("State = %q, want up", got.State)
	}
	if got.Delivered != 35 {
		t.Fatalf("Delivered = %d, want 35", got.Delivered)
	}
	if got.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", got.Errors)
	}
	// peak throughput is the largest single RecordDelivered batch (25),
	// not the running total (35).
	if got.PeakThroughput != 25 {
		t.Fatalf("PeakThroughput = %d, want 25", got.PeakThroughput)
	}
	// peak depth is T0+T1 from the wired DepthFunc (2+5=7).
	if got.PeakDepth != 7 {
		t.Fatalf("PeakDepth = %d, want 7", got.PeakDepth)
	}
	if got.LastError != "scripted failure" {
		t.Fatalf("LastError = %q, want %q", got.LastError, "scripted failure")
	}

	wantTiers := []TierDepth{
		{Tier: "T0", Depth: 2},
		{Tier: "T1", Depth: 5, OnDisk: 1},
		{Tier: "T2", Depth: 1},
	}
	if !reflect.DeepEqual(got.QueueDepth, wantTiers) {
		t.Fatalf("QueueDepth = %+v, want %+v", got.QueueDepth, wantTiers)
	}

	// A degraded/down state maps correctly too.
	state = StateRetrying
	if s := reg.Snapshot().Integrations[0].State; s != "degraded" {
		t.Fatalf("State after Retrying = %q, want degraded", s)
	}
	state = StateReconnecting
	if s := reg.Snapshot().Integrations[0].State; s != "down" {
		t.Fatalf("State after Reconnecting = %q, want down", s)
	}

	// A RecordDelivered(0) or negative n is a documented no-op.
	before := reg.Snapshot().Integrations[0].Delivered
	m.RecordDelivered(0)
	m.RecordDelivered(-5)
	if after := reg.Snapshot().Integrations[0].Delivered; after != before {
		t.Fatalf("Delivered changed on a non-positive RecordDelivered: before=%d after=%d", before, after)
	}

	// An unwired integration defaults to StatusUp with no queue depth —
	// the "local, always-connected degenerate case" default.
	bare := reg.Register("bare-integration")
	bareSnap := reg.Snapshot()
	var bareGot *IntegrationSnapshot
	for i := range bareSnap.Integrations {
		if bareSnap.Integrations[i].Name == "bare-integration" {
			bareGot = &bareSnap.Integrations[i]
		}
	}
	if bareGot == nil {
		t.Fatal("bare-integration missing from snapshot")
	}
	if bareGot.State != "up" {
		t.Fatalf("unwired integration State = %q, want up", bareGot.State)
	}
	if len(bareGot.QueueDepth) != 0 {
		t.Fatalf("unwired integration QueueDepth = %+v, want empty", bareGot.QueueDepth)
	}
	_ = bare

	// Register is idempotent: registering the same name twice returns
	// the SAME entry (counters preserved), not a fresh one.
	again := reg.Register("freight")
	if again != m {
		t.Fatal("Register on an existing name returned a different *IntegrationMetrics")
	}
	if reg.Snapshot().Integrations[1].Delivered != 35 { // sorted: bare-integration, freight
		t.Fatalf("re-Register lost recorded counters")
	}
}

// (b) Two snapshots of identical state are byte-identical JSON — proves
// Snapshot's ordering (by name, never map iteration) and struct field
// order are both deterministic.
func TestRegistry_SnapshotJSON_Deterministic(t *testing.T) {
	reg := NewRegistry()

	names := []string{"zeta", "alpha", "mid", "beta"}
	for _, n := range names {
		m := reg.Register(n,
			WithDepthFunc(func() Depth { return Depth{T0: 1, T1: 2, T1OnDisk: 0, T2: false} }),
			WithStateFunc(func() ConnState { return StateConnected }),
		)
		m.RecordDelivered(3)
	}
	reg.ObservePhase("finance", 42, 3)

	encode := func() []byte {
		buf, err := json.Marshal(reg.Snapshot())
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return buf
	}

	first := encode()
	second := encode()
	if !bytes.Equal(first, second) {
		t.Fatalf("snapshots of identical state differ:\n1: %s\n2: %s", first, second)
	}

	// Sanity: the integrations really did come out sorted by name.
	var decoded Snapshot
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	gotOrder := make([]string, len(decoded.Integrations))
	for i, in := range decoded.Integrations {
		gotOrder[i] = in.Name
	}
	wantOrder := []string{"alpha", "beta", "mid", "zeta"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("integration order = %v, want %v", gotOrder, wantOrder)
	}
	if decoded.LastPhase == nil || decoded.LastPhase.Kind != "finance" || decoded.LastPhase.Tick != 42 || decoded.LastPhase.Month != 3 {
		t.Fatalf("LastPhase = %+v, want {finance 42 3}", decoded.LastPhase)
	}
}

// (c) The gate: ServeMetrics refuses to start when not enabled — no Gate
// at all, and a Gate that denies.
func TestServeMetrics_RefusesWithoutEnable(t *testing.T) {
	reg := NewRegistry()

	// No gate configured at all.
	if srv, err := ServeMetrics("127.0.0.1:0", nil, reg); err == nil {
		if srv != nil {
			_ = srv.Close()
		}
		t.Fatal("expected an error with no Gate configured, got nil")
	} else if !containsCode(err, ErrMetricsServerNotEnabled) {
		t.Fatalf("expected code %s, got: %v", ErrMetricsServerNotEnabled, err)
	}

	// A Gate that explicitly denies.
	denyGate := func(correlationID string) error {
		return fmt.Errorf("debug mode is off")
	}
	if srv, err := ServeMetrics("127.0.0.1:0", denyGate, reg); err == nil {
		if srv != nil {
			_ = srv.Close()
		}
		t.Fatal("expected an error from a denying Gate, got nil")
	}

	// A nil Registry is also refused, even with an approving gate.
	allowGate := func(correlationID string) error { return nil }
	if srv, err := ServeMetrics("127.0.0.1:0", allowGate, nil); err == nil {
		if srv != nil {
			_ = srv.Close()
		}
		t.Fatal("expected an error with a nil Registry, got nil")
	} else if !containsCode(err, ErrMetricsServerNotEnabled) {
		t.Fatalf("expected code %s, got: %v", ErrMetricsServerNotEnabled, err)
	}

	// A non-localhost address is refused even with an approving gate.
	if srv, err := ServeMetrics("0.0.0.0:0", allowGate, reg); err == nil {
		if srv != nil {
			_ = srv.Close()
		}
		t.Fatal("expected an error for a non-localhost address, got nil")
	} else if !containsCode(err, ErrMetricsAddrNotLocal) {
		t.Fatalf("expected code %s, got: %v", ErrMetricsAddrNotLocal, err)
	}
}

// (d) Metrics collection does not perturb an Execute/Drain determinism
// test — byte-identical WITH metrics attached vs WITHOUT.
func TestMetrics_DoesNotPerturbDeterminism(t *testing.T) {
	runExecute := func(reg *Registry) (uint64, []record) {
		in := newSumIntegration(777, false)
		merged, err := Execute[uint64, uint64]("corr-metrics-det", NewLocalPool(4), in)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if reg != nil {
			// Exactly the shape a composition root's wrapper would call:
			// record delivered AFTER Execute already returned, never
			// injected into Execute/Dispatch/Combine/ApplyMessage itself.
			m := reg.Register("sum-integration")
			m.RecordDelivered(int64(len(in.Applied())))
		}
		return merged, in.Applied()
	}

	sumWithout, appliedWithout := runExecute(nil)
	sumWith, appliedWith := runExecute(NewRegistry())

	if sumWithout != sumWith {
		t.Fatalf("Execute merged sum differs: without=%d with=%d", sumWithout, sumWith)
	}
	if !reflect.DeepEqual(appliedWithout, appliedWith) {
		t.Fatalf("Execute applied-message sequence differs:\nwithout: %+v\nwith:    %+v", appliedWithout, appliedWith)
	}

	// Same proof for the queue layer's Drain: attach a Registry that
	// records delivered/queue-depth right after Drain returns, and
	// confirm the delivered command sequence is unaffected.
	runDrain := func(reg *Registry) []string {
		inner := newMockTransport()
		q := NewQueuedTransport(inner, Config{DiskRoot: t.TempDir(), T1MemCap: 8})
		for i := 0; i < 20; i++ {
			if err := q.SendCommand(buyCmd(fmt.Sprintf("corr-%d", i), i)); err != nil {
				t.Fatalf("SendCommand: %v", err)
			}
		}
		var m *IntegrationMetrics
		if reg != nil {
			m = reg.Register("queue-under-test", WithDepthFunc(q.Depth))
		}
		stats, err := q.Drain(0)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if m != nil {
			m.RecordDelivered(int64(stats.Total()))
			_ = m.sampleDepth() // exercises the depth sampler, same as a poll would
		}
		out := make([]string, 0, len(inner.Received()))
		for _, c := range inner.Received() {
			out = append(out, string(c.CorrelationID))
		}
		return out
	}

	drainWithout := runDrain(nil)
	drainWith := runDrain(NewRegistry())
	if !reflect.DeepEqual(drainWithout, drainWith) {
		t.Fatalf("Drain delivered-command order differs:\nwithout: %v\nwith:    %v", drainWithout, drainWith)
	}

	// det.Message sanity — pulled in only to keep the det import used if
	// sumIntegration's dependency ever changes shape; the real
	// determinism proof is the equality checks above.
	var _ = det.Message[uint64]{}
}

// (e) The endpoint serves /metrics.json and / when enabled.
func TestNewMetricsHandler_ServesEndpoints(t *testing.T) {
	reg := NewRegistry()
	m := reg.Register("harbor",
		WithDepthFunc(func() Depth { return Depth{T0: 1} }),
		WithStateFunc(func() ConnState { return StateConnected }),
	)
	m.RecordDelivered(4)

	ts := httptest.NewServer(NewMetricsHandler(reg))
	defer ts.Close()

	// GET /metrics.json
	resp, err := http.Get(ts.URL + "/metrics.json")
	if err != nil {
		t.Fatalf("GET /metrics.json: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics.json status = %d, want 200", resp.StatusCode)
	}
	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode /metrics.json body: %v", err)
	}
	if len(snap.Integrations) != 1 || snap.Integrations[0].Name != "harbor" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.Integrations[0].Delivered != 4 {
		t.Fatalf("Delivered = %d, want 4", snap.Integrations[0].Delivered)
	}

	// GET /
	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp2.StatusCode)
	}
	ct := resp2.Header.Get("Content-Type")
	if ct == "" {
		t.Fatal("GET / missing Content-Type")
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp2.Body); err != nil {
		t.Fatalf("read / body: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("GET / returned an empty body")
	}
	if !bytes.Contains(buf.Bytes(), []byte("metrics.json")) {
		t.Fatal("dashboard page does not reference /metrics.json — likely serving the wrong content")
	}

	// A nil Registry reports 503 rather than panicking.
	ts2 := httptest.NewServer(NewMetricsHandler(nil))
	defer ts2.Close()
	resp3, err := http.Get(ts2.URL + "/metrics.json")
	if err != nil {
		t.Fatalf("GET /metrics.json (nil registry): %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil-registry status = %d, want 503", resp3.StatusCode)
	}
}

// TestServeMetrics_StartsWhenEnabled proves the fail-closed gate has a
// real open side too: an approving Gate lets ServeMetrics actually bind
// and serve, end to end (not just NewMetricsHandler directly).
func TestServeMetrics_StartsWhenEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ferry", WithStateFunc(func() ConnState { return StateConnected }))

	allowGate := func(correlationID string) error { return nil }

	srv, err := ServeMetrics("127.0.0.1:0", allowGate, reg)
	if err != nil {
		t.Fatalf("ServeMetrics: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/metrics.json")
	if err != nil {
		t.Fatalf("GET /metrics.json against real listener: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// registryByteCopy and metricsByteCopy perform SEC-020's attack — a plain
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer,
// mirroring internal/foundation/errs/copyguard_test.go's loggerByteCopy
// exactly (see its doc comment for why this is the sanctioned TEST-ONLY
// mechanism): a literal `c := *reg` is legal, unsafe-free Go, but it is
// also something `go vet`'s copylocks check statically flags, and this
// package's VERIFY step requires a clean `go vet ./...`. The byte-level
// copy produces IDENTICAL runtime semantics to a literal struct copy
// (mu's bytes copied as-is, aliasable fields copied unchanged, self's
// pointer bytes copied unchanged — still pointing at the ORIGINAL) without
// a statically-flaggable copy expression.
func registryByteCopy(r *Registry) *Registry {
	c := new(Registry)
	*(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(r))
	return c
}

func metricsByteCopy(m *IntegrationMetrics) *IntegrationMetrics {
	c := new(IntegrationMetrics)
	*(*[unsafe.Sizeof(IntegrationMetrics{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(IntegrationMetrics{})]byte)(unsafe.Pointer(m))
	return c
}

// Copy-guard: every exported method fails closed on a struct-copied
// Registry/IntegrationMetrics, mirroring queue_test.go/resilience_test.go's
// own copy-guard coverage for tierQueue/QueuedTransport/Connection.
func TestMetrics_CopyGuard(t *testing.T) {
	reg := NewRegistry()
	m := reg.Register("copy-guard-target")

	regCopy := registryByteCopy(reg)
	if err := regCopy.checkNotCopied("test"); err == nil || !containsCode(err, ErrMetricsRegistryCopied) {
		t.Fatalf("Registry copy: expected %s, got %v", ErrMetricsRegistryCopied, err)
	}
	if got := regCopy.Snapshot(); len(got.Integrations) != 0 {
		t.Fatalf("copied Registry.Snapshot() returned entries: %+v", got)
	}

	mCopy := metricsByteCopy(m)
	if err := mCopy.checkNotCopied("test"); err == nil || !containsCode(err, ErrMetricsEntryCopied) {
		t.Fatalf("IntegrationMetrics copy: expected %s, got %v", ErrMetricsEntryCopied, err)
	}
	snap := mCopy.Snapshot()
	if snap.State != StatusDown.String() {
		t.Fatalf("copied IntegrationMetrics.Snapshot().State = %q, want %q (fail-closed)", snap.State, StatusDown.String())
	}
}
