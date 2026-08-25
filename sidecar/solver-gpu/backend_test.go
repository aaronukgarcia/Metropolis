package solvergpu

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// --- test doubles ---------------------------------------------------------

// failingTransport models a GPU worker that is unreachable: every
// SolveRemote returns a registry-sourced "sidecar unavailable" error, so the
// Backend exercises its mandatory local-fallback path (AC-5).
type failingTransport struct{}

func (failingTransport) SolveRemote(req solver.Request) (solver.Response, error) {
	return solver.Response{}, errs.New(errSidecarUnavailable, errs.NewCorrelationID(), map[string]any{
		"problem": req.Problem.String(),
	})
}

// recordingTransport captures the exact Request the transport was handed, so
// the contract-conformance test can assert the wire carries every field
// unmutilated (AC-3), then delegates to a backing transport.
type recordingTransport struct {
	backing Transport
	mu      sync.Mutex
	reqs    []solver.Request
}

func (r *recordingTransport) SolveRemote(req solver.Request) (solver.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.backing.SolveRemote(req)
}

func (r *recordingTransport) last() solver.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		return solver.Request{}
	}
	return r.reqs[len(r.reqs)-1]
}

// recordingHooks captures name-lookup and re-authentication calls (ICD §9's
// reconnect + re-auth + name-lookup), succeeding by default.
type recordingHooks struct {
	lookups []string
	auths   []string
}

func (h *recordingHooks) Lookup(name string) (string, error) {
	h.lookups = append(h.lookups, name)
	return name + ".addr", nil
}

func (h *recordingHooks) Authenticate(addr string) error {
	h.auths = append(h.auths, addr)
	return nil
}

// --- determinism (ICD §11.1; AC-6 crown invariant) ------------------------

// TestDeterminism_ByteIdenticalToCPU proves the load-bearing contract: for
// the same (Problem, Seed, Payload) the GPU backend's Response.Payload is
// byte-identical to CPUBackend's, over a table of seeds and payload shapes.
// It is the gate a real CUDA worker must keep passing (ICD §2/§7); today the
// worker delegates to the same transform, but the test still proves the
// offload path forwards Seed/Payload unmutated and adds nothing to the
// bytes.
func TestDeterminism_ByteIdenticalToCPU(t *testing.T) {
	gpu := NewBackend()
	cpu := solver.NewCPUBackend()

	payloads := [][]byte{
		nil,
		{},
		[]byte("x"),
		[]byte("the quick brown fox"),
		bytes.Repeat([]byte{0x5a}, 1024),
	}
	seeds := []uint64{0, 1, 0x9E3779B97F4A7C15, 0xDEADBEEFCAFEF00D}

	for _, seed := range seeds {
		for _, payload := range payloads {
			req := solver.Request{Problem: solver.EchoProblem, SchemaVersion: 1, Seed: seed, Deterministic: true, Payload: payload}

			gpuResp, err := gpu.Solve(req)
			if err != nil {
				t.Fatalf("gpu.Solve(seed=%x, payload=%d bytes): %v", seed, len(payload), err)
			}
			cpuResp, err := cpu.Solve(req)
			if err != nil {
				t.Fatalf("cpu.Solve(seed=%x, payload=%d bytes): %v", seed, len(payload), err)
			}

			if !bytes.Equal(gpuResp.Payload, cpuResp.Payload) {
				t.Fatalf("determinism broken: gpu payload != cpu payload (seed=%x, payload=%d bytes)", seed, len(payload))
			}
			if gpuResp.Backend != BackendName {
				t.Fatalf("gpu backend label = %q, want %q", gpuResp.Backend, BackendName)
			}
			if cpuResp.Backend != "cpu.v1" {
				t.Fatalf("cpu backend label = %q, want cpu.v1", cpuResp.Backend)
			}
		}
	}
}

// TestDeterminism_StableAcrossRuns re-runs the same solve N times and
// asserts byte-stability — the "no run-to-run variance" half of AC-7's
// guarantee, which matters the day a real CUDA worker lands with its own
// reduction order.
func TestDeterminism_StableAcrossRuns(t *testing.T) {
	gpu := NewBackend()
	req := solver.Request{Problem: solver.EchoProblem, SchemaVersion: 1, Seed: 42, Deterministic: true, Payload: []byte("stable")}

	first, err := gpu.Solve(req)
	if err != nil {
		t.Fatalf("first solve: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := gpu.Solve(req)
		if err != nil {
			t.Fatalf("solve %d: %v", i, err)
		}
		if !bytes.Equal(got.Payload, first.Payload) {
			t.Fatalf("solve %d payload diverged from run 1", i)
		}
	}
}

// --- mandatory local fallback (ICD §11.2; AC-5) ---------------------------

// TestFallback_GPUUnavailable_FallsThroughToLocal proves a down GPU still
// yields a correct answer: the local CPU result, byte-identical, only
// distinguishable by Response.Backend, with a non-fatal warning and the
// fallback activation counter incremented (ICD §10's critical signal).
func TestFallback_GPUUnavailable_FallsThroughToLocal(t *testing.T) {
	gpu := NewBackend(WithTransport(failingTransport{}))
	cpu := solver.NewCPUBackend()
	req := solver.Request{Problem: solver.EchoProblem, SchemaVersion: 1, Seed: 7, Deterministic: true, Payload: []byte("fallback")}

	resp, err := gpu.Solve(req)
	if err != nil {
		t.Fatalf("Solve with GPU down must still succeed via local fallback, got %v", err)
	}

	want, err := cpu.Solve(req)
	if err != nil {
		t.Fatalf("cpu.Solve: %v", err)
	}
	if !bytes.Equal(resp.Payload, want.Payload) {
		t.Fatal("fallback payload != cpu payload — the fallback must be byte-identical")
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("fallback Backend = %q, want cpu.v1 (distinguishable only by Backend)", resp.Backend)
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("fallback response should carry a non-fatal warning")
	}
	m := gpu.Metrics()
	if m.Fallbacks != 1 || m.Solves != 0 || m.TransportErrors != 1 {
		t.Fatalf("metrics = %+v, want 1 fallback, 0 solves, 1 transport error", m)
	}
	if got := gpu.Status(); got != StatusDegraded {
		t.Fatalf("Status = %s, want degraded (transport present but last solve fell back)", got)
	}
}

// TestFallback_NoTransportAtAll reports StatusDown when no transport is ever
// connected, while Solve still falls back correctly.
func TestFallback_NoTransportAtAll(t *testing.T) {
	// A dial that always fails leaves the sidecar with no transport.
	gpu := NewBackend(WithDial(func(string) (Transport, error) {
		return nil, errs.New(errSidecarUnavailable, errs.NewCorrelationID(), nil)
	}))

	if got := gpu.Status(); got != StatusDown {
		t.Fatalf("Status = %s, want down", got)
	}
	resp, err := gpu.Solve(solver.Request{Problem: solver.EchoProblem, Seed: 1, Payload: []byte("z")})
	if err != nil {
		t.Fatalf("Solve with no transport must fall back locally, got %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("Backend = %q, want cpu.v1", resp.Backend)
	}
}

// --- reconnect + re-auth + name lookup (ICD §11.2/§9) ---------------------

// TestReconnect_ReauthAndLookup_OnRecovery drives a down GPU back to up via
// Reconnect, asserting name-lookup and re-authentication both ran and the
// re-established transport is what answered the next solve.
func TestReconnect_ReauthAndLookup_OnRecovery(t *testing.T) {
	hooks := &recordingHooks{}
	gpu := NewBackend(
		WithTransport(failingTransport{}),
		WithHooks(hooks),
		WithDial(func(addr string) (Transport, error) {
			return newLocalTransport(BackendName), nil
		}),
	)

	req := solver.Request{Problem: solver.EchoProblem, Seed: 3, Payload: []byte("recover")}
	if _, err := gpu.Solve(req); err != nil {
		t.Fatalf("pre-reconnect solve: %v", err)
	}
	if got := gpu.Status(); got != StatusDegraded {
		t.Fatalf("pre-reconnect Status = %s, want degraded", got)
	}

	if err := gpu.Reconnect(BackendName); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if len(hooks.lookups) != 1 || hooks.lookups[0] != BackendName {
		t.Fatalf("Lookup calls = %v, want exactly one for %q", hooks.lookups, BackendName)
	}
	if len(hooks.auths) != 1 || hooks.auths[0] != BackendName+".addr" {
		t.Fatalf("Authenticate calls = %v, want exactly one for the looked-up addr", hooks.auths)
	}

	resp, err := gpu.Solve(req)
	if err != nil {
		t.Fatalf("post-reconnect solve: %v", err)
	}
	if resp.Backend != BackendName {
		t.Fatalf("post-reconnect Backend = %q, want %q (transport re-established)", resp.Backend, BackendName)
	}
	if got := gpu.Status(); got != StatusUp {
		t.Fatalf("post-reconnect Status = %s, want up", got)
	}
}

// TestReconnect_LookupFailure_LeavesSidecarDown proves a failed reconnect
// surfaces the registry error and does not half-install a transport.
func TestReconnect_LookupFailure_LeavesSidecarDown(t *testing.T) {
	gpu := NewBackend(
		WithTransport(failingTransport{}),
		WithHooks(hooksThatFailLookup{}),
	)

	err := gpu.Reconnect(BackendName)
	if !errors.Is(err, &errs.E{Code: errSidecarUnavailable}) {
		t.Fatalf("Reconnect error = %v, want registry code %s", err, errSidecarUnavailable)
	}
	// The transport is unchanged: still the failing one, so Solve falls back.
	resp, err := gpu.Solve(solver.Request{Problem: solver.EchoProblem, Seed: 9, Payload: []byte("k")})
	if err != nil {
		t.Fatalf("Solve after failed reconnect: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("Backend = %q, want cpu.v1 (failed reconnect must not half-install)", resp.Backend)
	}
}

type hooksThatFailLookup struct{}

func (hooksThatFailLookup) Lookup(string) (string, error) {
	return "", errs.New(errSidecarUnavailable, errs.NewCorrelationID(), nil)
}
func (hooksThatFailLookup) Authenticate(string) error { return nil }

// --- contract conformance (ICD §11.3; AC-1/AC-3/AC-4/AC-12) ---------------

// TestContract_TransportRoundTrip_PreservesFields feeds a Request through the
// transport and asserts every field arrives unmutilated — the Go-side,
// no-.proto shape of AC-3's field-for-field wire conformance.
func TestContract_TransportRoundTrip_PreservesFields(t *testing.T) {
	rec := &recordingTransport{backing: newLocalTransport(BackendName)}
	gpu := NewBackend(WithTransport(rec))

	req := solver.Request{
		Problem:       solver.EchoProblem,
		SchemaVersion: 3,
		Seed:          0x123456789ABCDEF0,
		Deterministic: true,
		Payload:       []byte("round-trip"),
	}
	if _, err := gpu.Solve(req); err != nil {
		t.Fatalf("Solve: %v", err)
	}

	got := rec.last()
	if got.Problem != req.Problem || got.SchemaVersion != req.SchemaVersion || got.Seed != req.Seed ||
		got.Deterministic != req.Deterministic || !bytes.Equal(got.Payload, req.Payload) {
		t.Fatalf("transport received mutated request: %+v (want %+v)", got, req)
	}
}

// TestContract_OversizedPayload_Rejected asserts the shared payload bound is
// enforced at the sidecar's own entry point with the registry-sourced
// MET-F401 (GR#7).
func TestContract_OversizedPayload_Rejected(t *testing.T) {
	gpu := NewBackend()
	big := bytes.Repeat([]byte{0xff}, solver.MaxRequestPayloadBytes+1)

	_, err := gpu.Solve(solver.Request{Problem: solver.EchoProblem, Payload: big})
	if !errors.Is(err, &errs.E{Code: solver.ErrRequestPayloadTooLarge}) {
		t.Fatalf("oversized payload error = %v, want registry code %s", err, solver.ErrRequestPayloadTooLarge)
	}
}

// TestSupports_DeclaresOffloadSubset pins the declared set: TrafficAssignment
// and ColdPassBatch (AC-4) plus EchoProblem for the determinism proof, and
// false for the TODO-SPEC kinds.
func TestSupports_DeclaresOffloadSubset(t *testing.T) {
	gpu := NewBackend()
	stub := Stub{}

	wantTrue := []solver.ProblemKind{solver.TrafficAssignment, solver.ColdPassBatch, solver.EchoProblem}
	wantFalse := []solver.ProblemKind{solver.DeepProjection, solver.LifeWriting}

	for _, p := range wantTrue {
		if !gpu.Supports(p) {
			t.Errorf("Backend.Supports(%s) = false, want true", p)
		}
		if !stub.Supports(p) {
			t.Errorf("Stub.Supports(%s) = false, want true", p)
		}
	}
	for _, p := range wantFalse {
		if gpu.Supports(p) {
			t.Errorf("Backend.Supports(%s) = true, want false", p)
		}
		if stub.Supports(p) {
			t.Errorf("Stub.Supports(%s) = true, want false", p)
		}
	}
}

// TestContract_NonEchoProblem_MatchesCPUBahaviour asserts the sidecar and CPU
// agree (both ErrNotImplemented) on a real kind whose algorithm no backend
// has built yet — behaviour byte-identical even when no payload is produced.
func TestContract_NonEchoProblem_MatchesCPUBehaviour(t *testing.T) {
	gpu := NewBackend()
	cpu := solver.NewCPUBackend()
	req := solver.Request{Problem: solver.TrafficAssignment, Seed: 5, Payload: []byte("od")}

	_, gpuErr := gpu.Solve(req)
	_, cpuErr := cpu.Solve(req)
	if gpuErr == nil || cpuErr == nil {
		t.Fatalf("both backends should fail for an unimplemented kind; gpu=%v cpu=%v", gpuErr, cpuErr)
	}
	if !errors.Is(gpuErr, solver.ErrNotImplemented) || !errors.Is(cpuErr, solver.ErrNotImplemented) {
		t.Fatalf("both should be ErrNotImplemented; gpu=%v cpu=%v", gpuErr, cpuErr)
	}
}

// TestContract_GarbagePayload_DoesNotCorruptSubsequentSolve proves the
// sidecar is stateless (AC-12): a malformed/garbage request cannot corrupt a
// later request.
func TestContract_GarbagePayload_DoesNotCorruptSubsequentSolve(t *testing.T) {
	gpu := NewBackend()
	cpu := solver.NewCPUBackend()

	// Garbage payload on a kind the sidecar declares (TrafficAssignment):
	// delegated to the local solver, which does not decode it — no crash, no
	// shared-state write.
	_, _ = gpu.Solve(solver.Request{Problem: solver.TrafficAssignment, Seed: 1, Payload: []byte{0x00, 0xff, 0x00}})

	// A subsequent valid solve must still answer correctly.
	req := solver.Request{Problem: solver.EchoProblem, Seed: 11, Payload: []byte("after-garbage")}
	resp, err := gpu.Solve(req)
	if err != nil {
		t.Fatalf("Solve after garbage request: %v", err)
	}
	want, _ := cpu.Solve(req)
	if !bytes.Equal(resp.Payload, want.Payload) {
		t.Fatal("garbage request corrupted the sidecar's subsequent answer")
	}
}

// --- registry integration (AC-1/AC-2/AC-5) --------------------------------

// TestRegistry_StubFallsThroughToCPU registers the Stub at PriorityGPU above
// CPU and asserts the chain falls through to CPU for a supported kind (the
// AC-2 headless-fallthrough proof).
func TestRegistry_StubFallsThroughToCPU(t *testing.T) {
	r := solver.NewRegistry()
	if err := r.Register("cpu.v1", solver.NewCPUBackend(), 0); err != nil {
		t.Fatalf("register cpu: %v", err)
	}
	if err := r.Register(BackendName, Stub{}, PriorityGPU); err != nil {
		t.Fatalf("register gpu stub: %v", err)
	}

	chain, err := r.Get(solver.EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := chain.Solve(solver.Request{Problem: solver.EchoProblem, Seed: 2, Payload: []byte("stub")})
	if err != nil {
		t.Fatalf("chain.Solve: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("chain resolved Backend = %q, want cpu.v1 (stub must fall through)", resp.Backend)
	}
}

// TestRegistry_BackendFallsThroughToCPU registers a real Backend with a
// failing transport and asserts the chain still answers via CPU — the
// end-to-end "GPU down, CPU answers" path through the real registry.
func TestRegistry_BackendFallsThroughToCPU(t *testing.T) {
	r := solver.NewRegistry()
	if err := r.Register("cpu.v1", solver.NewCPUBackend(), 0); err != nil {
		t.Fatalf("register cpu: %v", err)
	}
	if err := r.Register(BackendName, NewBackend(WithTransport(failingTransport{})), PriorityGPU); err != nil {
		t.Fatalf("register gpu backend: %v", err)
	}

	chain, err := r.Get(solver.EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := chain.Solve(solver.Request{Problem: solver.EchoProblem, Seed: 4, Payload: []byte("reg")})
	if err != nil {
		t.Fatalf("chain.Solve: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("chain resolved Backend = %q, want cpu.v1", resp.Backend)
	}
}

// TestRegister_InstallsAtPriorityGPU proves the exported registration helper
// wires a backend at PriorityGPU into a real Registry.
func TestRegister_InstallsAtPriorityGPU(t *testing.T) {
	r := solver.NewRegistry()
	if err := r.Register("cpu.v1", solver.NewCPUBackend(), 0); err != nil {
		t.Fatalf("register cpu: %v", err)
	}
	if err := Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}

	chain, err := r.Get(solver.EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := chain.Solve(solver.Request{Problem: solver.EchoProblem, Seed: 6, Payload: []byte("prio")})
	if err != nil {
		t.Fatalf("chain.Solve: %v", err)
	}
	if resp.Backend != BackendName {
		t.Fatalf("chain resolved Backend = %q, want %q (GPU wins at higher priority)", resp.Backend, BackendName)
	}
}

// --- monitoring + sizing (ICD §10; AC-9/GR#15) ----------------------------

// TestFitsEnvelope_ConsumesSolverSizing asserts the VRAM envelope check uses
// the spec's GPUVRAMEnvelopeBytes as its default and honours an injected
// runtime budget — proving the 4 GB figure is consumed, never re-hardcoded.
func TestFitsEnvelope_ConsumesSolverSizing(t *testing.T) {
	if !NewBackend().FitsEnvelope(solver.GPUVRAMEnvelopeBytes - 1) {
		t.Fatal("a job just under the spec envelope must fit by default")
	}
	if NewBackend().FitsEnvelope(solver.GPUVRAMEnvelopeBytes) {
		t.Fatal("a job at the spec envelope boundary must NOT fit (strict <)")
	}

	small := NewBackend(WithVRAMBudget(100))
	if small.FitsEnvelope(200) {
		t.Fatal("a 200-byte job must not fit a 100-byte runtime budget")
	}
	if !small.FitsEnvelope(50) {
		t.Fatal("a 50-byte job must fit a 100-byte runtime budget")
	}
}

// --- concurrency (GR#21, exercised under -race) ---------------------------

// TestBackend_ConcurrentSolveAndReconnect hammers Solve while Reconnect
// swaps the transport, asserting no race and byte-identical results
// regardless of which transport answered (GR#21: ordering never affects the
// answer). Run under `go test -race`.
func TestBackend_ConcurrentSolveAndReconnect(t *testing.T) {
	gpu := NewBackend(
		WithTransport(failingTransport{}),
		WithDial(func(addr string) (Transport, error) { return newLocalTransport(BackendName), nil }),
	)

	req := solver.Request{Problem: solver.EchoProblem, Seed: 99, Deterministic: true, Payload: []byte("concurrent")}
	want, err := solver.NewCPUBackend().Solve(req)
	if err != nil {
		t.Fatalf("cpu.Solve: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				resp, err := gpu.Solve(req)
				if err != nil {
					t.Errorf("Solve: %v", err)
					return
				}
				if !bytes.Equal(resp.Payload, want.Payload) {
					t.Errorf("payload diverged under concurrency")
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := gpu.Reconnect(BackendName); err != nil {
			t.Fatalf("Reconnect: %v", err)
		}
	}
	wg.Wait()
}

// --- AC-2 stub ------------------------------------------------------------

// TestStub_ReturnsTypedUnavailable asserts the Stub fails with the typed
// registry error and never a nil error, nil response pair.
func TestStub_ReturnsTypedUnavailable(t *testing.T) {
	var s Stub
	resp, err := s.Solve(solver.Request{Problem: solver.EchoProblem, Payload: []byte("x")})
	if err == nil {
		t.Fatal("Stub.Solve must error")
	}
	if !errors.Is(err, &errs.E{Code: errSidecarUnavailable}) {
		t.Fatalf("Stub error = %v, want registry code %s", err, errSidecarUnavailable)
	}
	if resp.Payload != nil {
		t.Fatalf("Stub response payload = %v, want nil", resp.Payload)
	}
}

var _ Transport = (*localTransport)(nil)
