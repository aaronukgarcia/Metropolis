package solver

import (
	"bytes"
	"errors"
	"sync"
	"testing"
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

func TestGetPrefersHigherPriorityBackend(t *testing.T) {
	r := NewRegistry()
	r.Register("cpu.v1", NewCPUBackend(), 0)
	r.Register("gpu.fake", &fixedSolver{
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
	r.Register("cpu.v1", NewCPUBackend(), 0)
	r.Register("gpu.flaky", &fixedSolver{
		problem: EchoProblem,
		err:     errors.New("cuda: device lost"),
	}, 100)

	var mu sync.Mutex
	var events []FailoverEvent
	r.SetFailoverHook(func(e FailoverEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

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
	r.Register("cpu.echo-only", &fixedSolver{problem: EchoProblem}, 0)
	if _, err := r.Get(TrafficAssignment); !errors.Is(err, ErrNoFallback) {
		t.Fatalf("expected ErrNoFallback for a problem kind with no registered backend, got %v", err)
	}
}

func TestSolveAllBackendsFailReturnsJoinedError(t *testing.T) {
	r := NewRegistry()
	errA := errors.New("backend A exploded")
	errB := errors.New("backend B exploded")
	r.Register("a", &fixedSolver{problem: EchoProblem, err: errA}, 10)
	r.Register("b", &fixedSolver{problem: EchoProblem, err: errB}, 5)

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
	Register("cpu.default-test", NewCPUBackend(), 0)
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
	r.Register("cpu.v1", NewCPUBackend(), 0)

	var wg sync.WaitGroup
	const goroutines = 16
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			r.Register("extra", &fixedSolver{problem: EchoProblem}, i)
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
