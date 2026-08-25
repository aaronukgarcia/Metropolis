package cloud

import (
	"bytes"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// loopbackTransport simulates a cloud worker running the same pure, seeded
// solver function as local: it delegates to the local backend, which is
// exactly what "offload is a faster local, not a different local" means.
type loopbackTransport struct {
	local solver.Solver
}

func (t loopbackTransport) Solve(req solver.Request) (solver.Response, error) {
	return t.local.Solve(req)
}

// failingTransport simulates a cloud tier that is down: every call fails
// with ErrCloudUnavailable.
type failingTransport struct{}

func (failingTransport) Solve(solver.Request) (solver.Response, error) {
	return solver.Response{}, ErrCloudUnavailable
}

// recordingTransport captures every request so a test can assert the
// request arrived on the transport unmutated (contract-conformance).
type recordingTransport struct {
	local solver.Solver

	mu  sync.Mutex
	got []solver.Request
}

func (t *recordingTransport) Solve(req solver.Request) (solver.Response, error) {
	t.mu.Lock()
	cp := req
	cp.Payload = append([]byte(nil), req.Payload...)
	t.got = append(t.got, cp)
	t.mu.Unlock()
	return t.local.Solve(req)
}

func (t *recordingTransport) last() solver.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.got[len(t.got)-1]
}

// recordingHooks records re-auth / name-lookup invocations so a test can
// prove Reconnect runs them.
type recordingHooks struct {
	integration.ReconnectHooks

	mu           sync.Mutex
	authCalls    int
	lookupCalls  int
	lookupTarget string
}

func (h *recordingHooks) Authenticate(correlationID string) error {
	h.mu.Lock()
	h.authCalls++
	h.mu.Unlock()
	return nil
}

func (h *recordingHooks) Lookup(correlationID, name string) (string, error) {
	h.mu.Lock()
	h.lookupCalls++
	h.lookupTarget = name
	h.mu.Unlock()
	return name, nil
}

// echoRequest returns a deterministic EchoProblem request whose payload is
// the given byte pattern, so CPUBackend produces real, checkable output.
func echoRequest(seed uint64, payload []byte) solver.Request {
	return solver.Request{
		Problem:       solver.EchoProblem,
		SchemaVersion: 1,
		Seed:          seed,
		Deterministic: true,
		Payload:       payload,
	}
}

func testPayload(seed int) []byte {
	return []byte{byte(seed), byte(seed >> 8), 0xAB, 0xCD, 0xEF, 0x01, 0x02, 0x03}
}

// TestAzureSolverOffloadByteIdenticalToLocal is ICD §11's determinism
// equivalence test: an offloaded solve and a local solve produce
// byte-identical Payload for the same (Problem, Seed, Payload).
func TestAzureSolverOffloadByteIdenticalToLocal(t *testing.T) {
	local := solver.NewCPUBackend()
	azure := NewAzureSolver(Config{Enabled: true}, loopbackTransport{local: local}, local)

	for seed := uint64(0); seed < 16; seed++ {
		payload := testPayload(int(seed))
		req := echoRequest(seed, payload)

		localResp, err := local.Solve(req)
		if err != nil {
			t.Fatalf("local solve seed=%d: %v", seed, err)
		}
		cloudResp, err := azure.Solve(req)
		if err != nil {
			t.Fatalf("cloud solve seed=%d: %v", seed, err)
		}

		if !bytes.Equal(localResp.Payload, cloudResp.Payload) {
			t.Fatalf("seed=%d: cloud payload %x != local payload %x (GR#21)", seed, cloudResp.Payload, localResp.Payload)
		}
		if cloudResp.Backend != "azure.solver" {
			t.Errorf("seed=%d: offloaded Backend = %q, want %q", seed, cloudResp.Backend, "azure.solver")
		}
	}
}

// TestAzureSolverFallbackByteIdenticalToLocal is ICD §11's resilience /
// fallback test: cloud down mid-sweep degrades to local, still returning a
// byte-identical Payload (only the diagnostic Backend differs).
func TestAzureSolverFallbackByteIdenticalToLocal(t *testing.T) {
	local := solver.NewCPUBackend()
	azure := NewAzureSolver(Config{Enabled: true}, failingTransport{}, local)

	req := echoRequest(42, testPayload(42))
	localResp, err := local.Solve(req)
	if err != nil {
		t.Fatalf("local solve: %v", err)
	}

	cloudResp, err := azure.Solve(req)
	if err != nil {
		t.Fatalf("cloud solve must fall back, not error: %v", err)
	}
	if !bytes.Equal(localResp.Payload, cloudResp.Payload) {
		t.Fatalf("fallback payload %x != local payload %x", cloudResp.Payload, localResp.Payload)
	}
	if cloudResp.Backend != localResp.Backend {
		t.Errorf("fallback Backend = %q, want local's %q", cloudResp.Backend, localResp.Backend)
	}
	if got := azure.Fallbacks(); got != 1 {
		t.Errorf("Fallbacks() = %d, want 1", got)
	}
	if got := azure.State(); got != integration.StateRetrying {
		t.Errorf("State() = %v, want StateRetrying after a failed offload", got)
	}
}

// TestAzureSolverContractConformance is ICD §11's contract-conformance
// test: the request arrives on the transport field-for-field unmutated and
// the response Payload is passed through byte-for-byte.
func TestAzureSolverContractConformance(t *testing.T) {
	local := solver.NewCPUBackend()
	rec := &recordingTransport{local: local}
	azure := NewAzureSolver(Config{Enabled: true}, rec, local)

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
	req := solver.Request{
		Problem:       solver.EchoProblem,
		SchemaVersion: 7,
		Seed:          0xFEEDFACE,
		Deterministic: true,
		Payload:       payload,
	}

	_, err := azure.Solve(req)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	got := rec.last()
	if got.Problem != req.Problem {
		t.Errorf("transport Problem = %v, want %v", got.Problem, req.Problem)
	}
	if got.SchemaVersion != req.SchemaVersion {
		t.Errorf("transport SchemaVersion = %d, want %d", got.SchemaVersion, req.SchemaVersion)
	}
	if got.Seed != req.Seed {
		t.Errorf("transport Seed = %d, want %d", got.Seed, req.Seed)
	}
	if got.Deterministic != req.Deterministic {
		t.Errorf("transport Deterministic = %v, want %v", got.Deterministic, req.Deterministic)
	}
	if !bytes.Equal(got.Payload, req.Payload) {
		t.Errorf("transport Payload = %x, want %x", got.Payload, req.Payload)
	}
}

// TestAzureSolverDisabledIsLocalPassThrough is the GR#20 stub test: a
// disabled tier never touches the transport and answers locally.
func TestAzureSolverDisabledIsLocalPassThrough(t *testing.T) {
	local := solver.NewCPUBackend()
	// A transport that fails the test if it is ever called.
	rec := &recordingTransport{local: local}
	azure := NewAzureSolver(Config{Enabled: false}, rec, local)

	req := echoRequest(9, testPayload(9))
	localResp, err := local.Solve(req)
	if err != nil {
		t.Fatalf("local solve: %v", err)
	}
	cloudResp, err := azure.Solve(req)
	if err != nil {
		t.Fatalf("disabled solve: %v", err)
	}
	if !bytes.Equal(localResp.Payload, cloudResp.Payload) {
		t.Fatalf("disabled payload %x != local %x", cloudResp.Payload, localResp.Payload)
	}
	rec.mu.Lock()
	calls := len(rec.got)
	rec.mu.Unlock()
	if calls != 0 {
		t.Errorf("disabled tier called transport %d times, want 0", calls)
	}
	if got := azure.Fallbacks(); got != 0 {
		t.Errorf("disabled tier Fallbacks() = %d, want 0 (cloud absent is baseline, not a fallback)", got)
	}
}

// TestAzureSolverReconnectRunsReauthAndLookup is ICD §11's reconnect +
// re-auth test.
func TestAzureSolverReconnectRunsReauthAndLookup(t *testing.T) {
	local := solver.NewCPUBackend()
	hooks := &recordingHooks{}
	azure := NewAzureSolver(Config{Enabled: true, Hooks: hooks}, failingTransport{}, local)

	// Force the connection out of Connected.
	if _, err := azure.Solve(echoRequest(1, testPayload(1))); err != nil {
		t.Fatalf("solve should fall back, not error: %v", err)
	}

	if _, err := azure.Reconnect("azure.solver"); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if got := azure.State(); got != integration.StateConnected {
		t.Errorf("State() after Reconnect = %v, want StateConnected", got)
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.authCalls != 1 {
		t.Errorf("Authenticate called %d times, want 1", hooks.authCalls)
	}
	if hooks.lookupCalls != 1 {
		t.Errorf("Lookup called %d times, want 1", hooks.lookupCalls)
	}
	if hooks.lookupTarget != "azure.solver" {
		t.Errorf("Lookup target = %q, want %q", hooks.lookupTarget, "azure.solver")
	}
}

// TestAzureSolverRegisterIntoRegistry proves the backend plugs int.solver's
// registry (same seams as local) and resolves byte-identically through it.
func TestAzureSolverRegisterIntoRegistry(t *testing.T) {
	local := solver.NewCPUBackend()
	reg := solver.NewRegistry()
	if err := reg.Register("cpu.v1", local, 0); err != nil {
		t.Fatalf("register cpu: %v", err)
	}
	azure := NewAzureSolver(Config{Enabled: true}, loopbackTransport{local: local}, local)
	if err := azure.Register(reg, DefaultSolverPriority); err != nil {
		t.Fatalf("register azure: %v", err)
	}

	chain, err := reg.Get(solver.EchoProblem)
	if err != nil {
		t.Fatalf("Get(EchoProblem): %v", err)
	}
	req := echoRequest(7, testPayload(7))
	direct, err := local.Solve(req)
	if err != nil {
		t.Fatalf("direct local solve: %v", err)
	}
	viaChain, err := chain.Solve(req)
	if err != nil {
		t.Fatalf("chain solve: %v", err)
	}
	if !bytes.Equal(direct.Payload, viaChain.Payload) {
		t.Fatalf("registry-resolved payload %x != direct %x", viaChain.Payload, direct.Payload)
	}
	// The cloud backend is highest priority, so the chain answers from it.
	if viaChain.Backend != "azure.solver" {
		t.Errorf("chain Backend = %q, want %q (cloud priority > cpu)", viaChain.Backend, "azure.solver")
	}
}

// TestAzureSolverSupportsDelegatesToLocal confirms the "same seams as
// local" Supports surface.
func TestAzureSolverSupportsDelegatesToLocal(t *testing.T) {
	local := solver.NewCPUBackend()
	azure := NewAzureSolver(Config{Enabled: true}, failingTransport{}, local)

	for _, p := range []solver.ProblemKind{
		solver.EchoProblem, solver.TrafficAssignment, solver.ColdPassBatch,
		solver.DeepProjection, solver.LifeWriting,
	} {
		if got, want := azure.Supports(p), local.Supports(p); got != want {
			t.Errorf("Supports(%v) = %v, want %v", p, got, want)
		}
	}
}

// TestAzureSolverConfigDefaults ensures the zero Config yields the
// conventional backend name.
func TestAzureSolverConfigDefaults(t *testing.T) {
	azure := NewAzureSolver(Config{}, failingTransport{}, solver.NewCPUBackend())
	if azure.name != "azure.solver" {
		t.Errorf("default backend name = %q, want %q", azure.name, "azure.solver")
	}
}

// TestConfigShouldOffloadCitizenShards proves the A9 threshold is consumed
// from int.solver (never re-hardcoded) and that disabled cloud never
// offloads citizen shards.
func TestConfigShouldOffloadCitizenShards(t *testing.T) {
	// Below the low A9 bound: no offload.
	if (Config{Enabled: true}).ShouldOffloadCitizenShards(19_999_999) {
		t.Error("citizens just below the A9 low bound must not offload")
	}
	// At the low bound: offload (solver.ExceedsLocalCPUCeiling, consumed).
	if !(Config{Enabled: true}).ShouldOffloadCitizenShards(20_000_000) {
		t.Error("citizens at the A9 low bound must offload via solver.ExceedsLocalCPUCeiling")
	}
	// Disabled cloud: never offload, even far above the bound.
	if (Config{Enabled: false}).ShouldOffloadCitizenShards(100_000_000) {
		t.Error("disabled cloud must never offload citizen shards")
	}
	// Explicit threshold override wins.
	cfg := Config{Enabled: true, CitizenShardThreshold: 42}
	if !cfg.ShouldOffloadCitizenShards(42) {
		t.Error("explicit CitizenShardThreshold=42 must offload at 42")
	}
	if cfg.ShouldOffloadCitizenShards(41) {
		t.Error("explicit CitizenShardThreshold=42 must not offload at 41")
	}
}
