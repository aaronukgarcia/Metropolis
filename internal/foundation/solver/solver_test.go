package solver

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fixedSolver is a test double that always returns a fixed response (or
// error) for Solve, and supports whatever ProblemKind it is told to.
type fixedSolver struct {
	problem ProblemKind
	resp    Response
	err     error
}

func (f *fixedSolver) Supports(p ProblemKind) bool { return p == f.problem }
func (f *fixedSolver) Solve(Request) (Response, error) {
	if f.err != nil {
		return Response{}, f.err
	}
	return f.resp, nil
}

// mustRegister is a test helper that fails the test immediately if
// Register returns an error (SEC-020 wave 2 made Register fallible — a
// struct-copied Registry rejects Register with ErrRegistryCopied — so
// every test call site must check the return rather than discard it,
// both to satisfy errcheck and so a regressed copy-guard fails loudly
// here instead of silently no-op'ing a registration the rest of the test
// assumes happened).
func mustRegister(t *testing.T, r *Registry, name string, s Solver, priority int) {
	t.Helper()
	if err := r.Register(name, s, priority); err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
}

func TestGetPrefersHigherPriorityBackend(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "cpu.v1", NewCPUBackend(), 0)
	mustRegister(t, r, "gpu.fake", &fixedSolver{
		problem: EchoProblem,
		resp:    Response{Payload: []byte("gpu-answer"), Backend: "gpu.fake"},
	}, 100)

	s, err := r.Get(EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := s.Solve(Request{Problem: EchoProblem})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if resp.Backend != "gpu.fake" {
		t.Fatalf("expected higher-priority backend gpu.fake to win, got %q", resp.Backend)
	}
}

func TestGetFallsBackToCPUOnHigherPriorityFailure(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "cpu.v1", NewCPUBackend(), 0)
	mustRegister(t, r, "gpu.flaky", &fixedSolver{
		problem: EchoProblem,
		err:     errors.New("cuda: device lost"),
	}, 100)

	var mu sync.Mutex
	var events []FailoverEvent
	if err := r.SetFailoverHook(func(e FailoverEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}); err != nil {
		t.Fatalf("SetFailoverHook: %v", err)
	}

	s, err := r.Get(EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	req := Request{Problem: EchoProblem, Seed: 42, Payload: []byte("hello")}
	resp, err := s.Solve(req)
	if err != nil {
		t.Fatalf("Solve should have transparently fallen back to cpu.v1, got error: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("expected fallback to cpu.v1, got backend %q", resp.Backend)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 observable failover event, got %d: %+v", len(events), events)
	}
	if events[0].Backend != "gpu.flaky" {
		t.Fatalf("expected failover event to name gpu.flaky, got %q", events[0].Backend)
	}
	if events[0].Problem != EchoProblem {
		t.Fatalf("expected failover event for EchoProblem, got %v", events[0].Problem)
	}
}

func TestGetNoFallbackFailsLoudly(t *testing.T) {
	r := NewRegistry()
	// Nothing registered at all.
	if _, err := r.Get(TrafficAssignment); !errors.Is(err, ErrNoFallback) {
		t.Fatalf("expected ErrNoFallback, got %v", err)
	}

	// A backend registered for a *different* problem kind still leaves
	// TrafficAssignment with no fallback.
	mustRegister(t, r, "cpu.echo-only", &fixedSolver{problem: EchoProblem}, 0)
	if _, err := r.Get(TrafficAssignment); !errors.Is(err, ErrNoFallback) {
		t.Fatalf("expected ErrNoFallback for a problem kind with no registered backend, got %v", err)
	}
}

func TestSolveAllBackendsFailReturnsJoinedError(t *testing.T) {
	r := NewRegistry()
	errA := errors.New("backend A exploded")
	errB := errors.New("backend B exploded")
	mustRegister(t, r, "a", &fixedSolver{problem: EchoProblem, err: errA}, 10)
	mustRegister(t, r, "b", &fixedSolver{problem: EchoProblem, err: errB}, 5)

	s, err := r.Get(EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, err = s.Solve(Request{Problem: EchoProblem})
	if err == nil {
		t.Fatal("expected total failure when every backend errors")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("expected joined error to wrap both underlying errors, got: %v", err)
	}
}

func TestEchoRoundTripDeterministic(t *testing.T) {
	cpu := NewCPUBackend()
	req := Request{
		Problem:       EchoProblem,
		SchemaVersion: 1,
		Seed:          12345,
		Deterministic: true,
		Payload:       []byte("the quick brown fox jumps over the lazy dog"),
	}

	resp1, err := cpu.Solve(req)
	if err != nil {
		t.Fatalf("Solve #1: %v", err)
	}
	resp2, err := cpu.Solve(req)
	if err != nil {
		t.Fatalf("Solve #2: %v", err)
	}

	if !bytes.Equal(resp1.Payload, resp2.Payload) {
		t.Fatalf("same request produced different payloads:\n  1: %x\n  2: %x", resp1.Payload, resp2.Payload)
	}
	if bytes.Equal(resp1.Payload, req.Payload) {
		t.Fatal("echo transform should not be a no-op (payload unchanged)")
	}
	if len(resp1.Payload) != len(req.Payload) {
		t.Fatalf("payload length changed: got %d want %d", len(resp1.Payload), len(req.Payload))
	}

	// Different seed must (overwhelmingly likely) produce a different
	// result — proves Seed actually participates in the transform.
	req2 := req
	req2.Seed = req.Seed + 1
	resp3, err := cpu.Solve(req2)
	if err != nil {
		t.Fatalf("Solve #3: %v", err)
	}
	if bytes.Equal(resp1.Payload, resp3.Payload) {
		t.Fatal("different seeds produced identical payloads")
	}
}

func TestEchoUnknownProblemNotImplemented(t *testing.T) {
	cpu := NewCPUBackend()
	_, err := cpu.Solve(Request{Problem: TrafficAssignment})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestCPUBackendSupportsAllProblemKinds(t *testing.T) {
	cpu := NewCPUBackend()
	for _, p := range []ProblemKind{EchoProblem, TrafficAssignment, ColdPassBatch, DeepProjection, LifeWriting} {
		if !cpu.Supports(p) {
			t.Fatalf("CPUBackend must support every ProblemKind (mandatory local fallback), missing %v", p)
		}
	}
}

func TestDefaultRegistryRegisterAndGet(t *testing.T) {
	// Exercise the package-level convenience functions against Default,
	// using a ProblemKind unlikely to collide with other tests in this
	// package (Default is process-global and shared across tests run in
	// the same binary).
	if err := Register("cpu.default-test", NewCPUBackend(), 0); err != nil {
		t.Fatalf("Register against Default: %v", err)
	}
	s, err := Get(ColdPassBatch)
	if err != nil {
		t.Fatalf("Get(ColdPassBatch) against Default: %v", err)
	}
	if s == nil {
		t.Fatal("Get returned nil Solver with nil error")
	}
}

func TestEstimateODBytes(t *testing.T) {
	cases := []struct {
		zones int
		want  int64
	}{
		{0, 0},
		{-5, 0},
		{1, 8},
		{1000, 1000 * 1000 * 8},
		{ReferenceODZoneCount, ReferenceODZoneCount * ReferenceODZoneCount * 8},
	}
	for _, c := range cases {
		if got := EstimateODBytes(c.zones); got != c.want {
			t.Errorf("EstimateODBytes(%d) = %d, want %d", c.zones, got, c.want)
		}
	}
}

func TestEstimateRoadGraphBytes(t *testing.T) {
	if got := EstimateRoadGraphBytes(0, 64); got != 0 {
		t.Errorf("EstimateRoadGraphBytes(0, 64) = %d, want 0", got)
	}
	if got := EstimateRoadGraphBytes(1000, 0); got != 1000*defaultRoadEdgeBytes {
		t.Errorf("EstimateRoadGraphBytes(1000, 0) = %d, want %d (default edge size)", got, 1000*defaultRoadEdgeBytes)
	}
	if got := EstimateRoadGraphBytes(1000, 128); got != 1000*128 {
		t.Errorf("EstimateRoadGraphBytes(1000, 128) = %d, want %d", got, 1000*128)
	}
}

func TestFitsGPUEnvelope(t *testing.T) {
	if !FitsGPUEnvelope(1024) {
		t.Error("1KB should fit the 4GB GPU envelope")
	}
	if FitsGPUEnvelope(GPUVRAMEnvelopeBytes) {
		t.Error("exactly the envelope size should not count as fitting (strict less-than)")
	}
	if FitsGPUEnvelope(GPUVRAMEnvelopeBytes + 1) {
		t.Error("over the envelope size should not fit")
	}
}

func TestExceedsLocalCPUCeiling(t *testing.T) {
	if ExceedsLocalCPUCeiling(19_999_999) {
		t.Error("19,999,999 citizens should not exceed the A9 low ceiling")
	}
	if !ExceedsLocalCPUCeiling(LocalCitizenCeilingLow) {
		t.Error("exactly the low ceiling should count as exceeding it")
	}
	if ExceedsLocalCPUCeilingHigh(LocalCitizenCeilingLow) {
		t.Error("the low ceiling should not exceed the high ceiling")
	}
	if !ExceedsLocalCPUCeilingHigh(LocalCitizenCeilingHigh) {
		t.Error("exactly the high ceiling should count as exceeding it")
	}
}

func TestProblemKindString(t *testing.T) {
	known := map[ProblemKind]string{
		EchoProblem:       "EchoProblem",
		TrafficAssignment: "TrafficAssignment",
		ColdPassBatch:     "ColdPassBatch",
		DeepProjection:    "DeepProjection",
		LifeWriting:       "LifeWriting",
	}
	for k, want := range known {
		if got := k.String(); got != want {
			t.Errorf("ProblemKind(%d).String() = %q, want %q", k, got, want)
		}
	}
	if got := ProblemKind(999).String(); got == "" {
		t.Error("unknown ProblemKind should still render a non-empty placeholder string")
	}
}

// TestRegistryConcurrentUse registers and resolves concurrently to be
// exercised under `go test -race`; it does not assert on ordering, only
// that concurrent Register/Get/Solve/SetFailoverHook calls are race-free
// and every Get/Solve either succeeds or returns a well-formed error.
func TestRegistryConcurrentUse(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "cpu.v1", NewCPUBackend(), 0)

	var wg sync.WaitGroup
	const goroutines = 16
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			// t.Errorf (never t.Fatalf) from a non-test goroutine: Fatalf
			// calls runtime.Goexit, which is only safe on the goroutine
			// running the test function itself.
			if err := r.Register("extra", &fixedSolver{problem: EchoProblem}, i); err != nil {
				t.Errorf("Register: %v", err)
			}
		}(i)
		go func() {
			defer wg.Done()
			s, err := r.Get(EchoProblem)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			if _, err := s.Solve(Request{Problem: EchoProblem, Payload: []byte("x")}); err != nil {
				t.Errorf("Solve: %v", err)
			}
		}()
	}
	wg.Wait()
}

// --- BUG-012: Request.Payload size bound ---------------------------------

// TestEchoOversizedPayloadRejected proves an over-limit payload is rejected
// with the registry-sourced ErrRequestPayloadTooLarge and produces no
// response payload. If MaxRequestPayloadBytes were removed (or the check
// dropped), Solve would succeed and return a payload — errors.Is(nil, …)
// is false, so this test fails loudly rather than passing.
func TestEchoOversizedPayloadRejected(t *testing.T) {
	cpu := NewCPUBackend()
	req := Request{
		Problem: EchoProblem,
		Payload: make([]byte, MaxRequestPayloadBytes+1),
	}

	resp, err := cpu.Solve(req)
	if !errors.Is(err, &errs.E{Code: ErrRequestPayloadTooLarge}) {
		t.Fatalf("Solve(oversized payload) err = %v, want ErrRequestPayloadTooLarge", err)
	}
	if resp.Payload != nil {
		t.Fatalf("Solve(oversized payload) returned a %d-byte payload, want nil (rejected, never allocated)", len(resp.Payload))
	}
}

// TestEchoOversizedPayloadRejectedWithoutAllocating proves the rejection
// happens on len(req.Payload) BEFORE solveEcho's make([]byte, len(Payload))
// — i.e. the input is bounded, not the output (weakness pattern #6).
// Measured via runtime.MemStats' cumulative TotalAlloc delta, the same
// technique internal/ui/screens/map/sec009_test.go uses for the identical
// "size reaches an allocation" class (SEC-009). If the make ever ran, it
// would add MaxRequestPayloadBytes (1 MiB) to the delta; the registry-error
// construction is a handful of small allocations, orders of magnitude less.
// The 64 KiB bound is generous headroom for errs.New's per-call overhead
// while being utterly incompatible with the 1 MiB payload buffer having
// been allocated.
func TestEchoOversizedPayloadRejectedWithoutAllocating(t *testing.T) {
	// Warm the error registry (sync.Once) so the first-load JSON parse —
	// which allocates on the order of the whole data/errors.json — does not
	// pollute the TotalAlloc delta measured below.
	_ = errs.New(ErrRequestPayloadTooLarge, "warmup", nil)

	cpu := NewCPUBackend()
	req := Request{
		Problem: EchoProblem,
		Payload: make([]byte, MaxRequestPayloadBytes+1),
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	resp, err := cpu.Solve(req)
	runtime.ReadMemStats(&after)

	if !errors.Is(err, &errs.E{Code: ErrRequestPayloadTooLarge}) {
		t.Fatalf("Solve(oversized payload) err = %v, want ErrRequestPayloadTooLarge", err)
	}
	if resp.Payload != nil {
		t.Fatalf("Solve(oversized payload) returned a %d-byte payload, want nil", len(resp.Payload))
	}

	const maxPlausibleRejectionOverheadBytes = 64 * 1024
	if delta := after.TotalAlloc - before.TotalAlloc; delta > maxPlausibleRejectionOverheadBytes {
		t.Fatalf("Solve(oversized payload) allocated %d bytes on the rejection path, want < %d — the payload-sized buffer was allocated before the bound check", delta, maxPlausibleRejectionOverheadBytes)
	}
}

// TestOversizedPayloadRejectedThroughRegistryPath proves the bound is
// enforced at the shared dispatch entry point (chainSolver.Solve), not only
// inside CPUBackend (AC-2). A higher-priority non-CPU backend is registered
// so that, were the chain-level check missing, the oversized request would
// reach gpu.fake and succeed — this test would then fail with err = nil.
func TestOversizedPayloadRejectedThroughRegistryPath(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "cpu.v1", NewCPUBackend(), 0)
	mustRegister(t, r, "gpu.fake", &fixedSolver{
		problem: EchoProblem,
		resp:    Response{Payload: []byte("gpu-answer"), Backend: "gpu.fake"},
	}, 100)

	s, err := r.Get(EchoProblem)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	req := Request{Problem: EchoProblem, Payload: make([]byte, MaxRequestPayloadBytes+1)}
	_, err = s.Solve(req)
	if !errors.Is(err, &errs.E{Code: ErrRequestPayloadTooLarge}) {
		t.Fatalf("chainSolver.Solve(oversized payload) err = %v, want ErrRequestPayloadTooLarge (the bound must hold at the shared entry point, before any backend is dispatched)", err)
	}
}
