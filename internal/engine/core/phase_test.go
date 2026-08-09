package core

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

func TestNumShards_Is256(t *testing.T) {
	if got := NumShards(); got != 256 {
		t.Fatalf("NumShards() = %d, want 256 (AC-5)", got)
	}
}

func TestPoolSizeForCPUs_Floor(t *testing.T) {
	cases := []struct {
		cpus int
		want int
	}{
		{1, 1}, // floor prevents zero/negative
		{2, 1}, // floor prevents zero/negative
		{3, 1},
		{4, 2},
		{16, 14},
	}
	for _, c := range cases {
		if got := poolSizeForCPUs(c.cpus); got != c.want {
			t.Errorf("poolSizeForCPUs(%d) = %d, want %d", c.cpus, got, c.want)
		}
	}
}

// recordingHook is a PhaseHook that appends its own name to a shared,
// mutex-guarded slice (never a map) on every RunShard call, and
// optionally emits Effects for the out-of-order barrier test.
type recordingHook struct {
	mu      *sync.Mutex
	entered *[]string
	name    string

	emit func(shard int) []Effect

	applyMu *sync.Mutex
	applied *[]Effect
}

func newRecordingHook(name string, mu *sync.Mutex, entered *[]string) *recordingHook {
	return &recordingHook{mu: mu, entered: entered, name: name}
}

func (h *recordingHook) RunShard(shard int) ([]Effect, error) {
	h.mu.Lock()
	*h.entered = append(*h.entered, h.name)
	h.mu.Unlock()
	if h.emit != nil {
		return h.emit(shard), nil
	}
	return nil, nil
}

func (h *recordingHook) ApplyEffect(eff Effect) {
	if h.applyMu == nil {
		return
	}
	h.applyMu.Lock()
	*h.applied = append(*h.applied, eff)
	h.applyMu.Unlock()
}

// TestPhaseOrder_FixedAndObservable registers a recording hook against
// every phase in the fixed pipeline (daily + monthly) and asserts the
// PhaseObserver sees them in exactly the documented order (AC-3).
func TestPhaseOrder_FixedAndObservable(t *testing.T) {
	var mu sync.Mutex
	var observed []PhaseKind

	e := NewEngine(
		WithPoolSize(2),
		WithPhaseObserver(func(kind PhaseKind, tick, month int64) {
			mu.Lock()
			observed = append(observed, kind)
			mu.Unlock()
		}),
	)

	// Register a real+stub pair via the module registry (Name/Version/
	// Health) alongside a PhaseHook, mirroring "a recording stub module
	// registered real+stub" from the dispatch brief.
	stubMod := fakeModule{name: "test.recorder", version: "0.0.1"}
	if err := e.Registry().Register("test.recorder", nil, stubMod); err != nil {
		t.Fatalf("Registry().Register: %v", err)
	}

	var entered []string
	var enteredMu sync.Mutex
	for _, phase := range append(append([]PhaseKind{}, DailyPhaseOrder...), MonthlyPhaseOrder...) {
		hook := newRecordingHook(string(phase), &enteredMu, &entered)
		if err := e.RegisterPhaseHook(phase, hook); err != nil {
			t.Fatalf("RegisterPhaseHook(%s): %v", phase, err)
		}
	}

	// Advance exactly 30 ticks: one month boundary, so both the daily
	// phase (30 times) and the monthly pipeline (once) run.
	if err := e.AdvanceTicks("corr-1", 30); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	wantTail := append(append([]PhaseKind{}, DailyPhaseOrder...), MonthlyPhaseOrder...)
	mu.Lock()
	defer mu.Unlock()
	if len(observed) < len(wantTail) {
		t.Fatalf("observed %v, too short for want tail %v", observed, wantTail)
	}
	gotTail := observed[len(observed)-len(wantTail):]
	for i, w := range wantTail {
		if gotTail[i] != w {
			t.Fatalf("phase order mismatch at index %d: got %v, want tail %v (full observed: %v)", i, gotTail, wantTail, observed)
		}
	}
}

// TestBarrier_DeterministicOrderRegardlessOfSubmission runs a hook that
// emits messages with intentionally scrambled (shard, sequence) pairs
// from different shards/goroutines and asserts ApplyEffect always sees
// them in canonical ascending (shard, sequence) order, regardless of
// which worker finished first (AC-6).
func TestBarrier_DeterministicOrderRegardlessOfSubmission(t *testing.T) {
	// Emit two effects from a handful of shards, with sequence numbers
	// that do NOT correlate with shard-processing order (shards near
	// the end of the range emit "early" sequence numbers and vice
	// versa), so a submission-order-based application would visibly
	// differ from the canonical (shard, sequence) order.
	emitShards := []int{3, 1, 255, 0, 42, 128}

	for trial := 0; trial < 5; trial++ {
		var appliedMu sync.Mutex
		var applied []Effect

		hook := &recordingHook{
			mu:      &sync.Mutex{},
			entered: &[]string{},
			name:    "barrier-test",
			applyMu: &sync.Mutex{},
			applied: &applied,
		}
		hook.emit = func(shard int) []Effect {
			for _, want := range emitShards {
				if shard == want {
					return []Effect{
						{Sequence: 1, Payload: fmt.Sprintf("%d:1", shard)},
						{Sequence: 0, Payload: fmt.Sprintf("%d:0", shard)},
					}
				}
			}
			return nil
		}

		e := NewEngine(WithPoolSize(8))
		if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
			t.Fatalf("RegisterPhaseHook: %v", err)
		}
		if err := e.AdvanceTicks("corr-barrier", 1); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}

		appliedMu.Lock()
		got := append([]Effect{}, applied...)
		appliedMu.Unlock()

		sortedShards := append([]int{}, emitShards...)
		sort.Ints(sortedShards)

		if len(got) != len(sortedShards)*2 {
			t.Fatalf("trial %d: got %d applied effects, want %d", trial, len(got), len(sortedShards)*2)
		}
		idx := 0
		for _, shard := range sortedShards {
			for _, seq := range []int{0, 1} {
				want := fmt.Sprintf("%d:%d", shard, seq)
				if got[idx].Payload != want {
					t.Fatalf("trial %d: applied[%d] = %v, want payload %q (canonical shard,seq order)", trial, idx, got[idx], want)
				}
				idx++
			}
		}
	}
}

// TestPhaseHookError_AbortsRemainingPhases asserts that a phase hook
// error stops the tick's remaining phases (AC-10): a hook registered
// against an earlier phase fails, and a hook registered against a
// later phase in the same tick must never run.
func TestPhaseHookError_AbortsRemainingPhases(t *testing.T) {
	e := NewEngine(WithPoolSize(2))

	failingHook := failingPhaseHook{}
	if err := e.RegisterPhaseHook(PhaseProduction, failingHook); err != nil {
		t.Fatalf("RegisterPhaseHook(production): %v", err)
	}

	var laterRan bool
	var laterMu sync.Mutex
	later := laterHook{ran: &laterRan, mu: &laterMu}
	if err := e.RegisterPhaseHook(PhaseFinance, later); err != nil {
		t.Fatalf("RegisterPhaseHook(finance): %v", err)
	}

	// Drive exactly 30 ticks so the monthly pipeline (production ->
	// ... -> finance) runs once.
	err := e.AdvanceTicks("corr-abort", 30)
	if err == nil {
		t.Fatal("AdvanceTicks: want error from failing phase hook, got nil")
	}

	laterMu.Lock()
	ran := laterRan
	laterMu.Unlock()
	if ran {
		t.Fatal("finance phase hook ran despite an earlier phase (production) erroring — AC-10 violated")
	}
}

type failingPhaseHook struct{}

func (failingPhaseHook) RunShard(shard int) ([]Effect, error) {
	if shard == 0 {
		return nil, fmt.Errorf("synthetic failure on shard 0")
	}
	return nil, nil
}
func (failingPhaseHook) ApplyEffect(Effect) {}

type laterHook struct {
	ran *bool
	mu  *sync.Mutex
}

func (h laterHook) RunShard(shard int) ([]Effect, error) {
	h.mu.Lock()
	*h.ran = true
	h.mu.Unlock()
	return nil, nil
}
func (laterHook) ApplyEffect(Effect) {}

// fakeModule implements registry.Module minimally for tests.
type fakeModule struct {
	name    string
	version string
}

func (m fakeModule) Name() string            { return m.name }
func (m fakeModule) Version() string         { return m.version }
func (m fakeModule) Health() registry.Health { return registry.HealthOK }
