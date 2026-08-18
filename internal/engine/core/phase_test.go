package core

import (
	"fmt"
	"sort"
	"strings"
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
	for _, phase := range append(append([]PhaseKind{}, DailyPhaseOrder()...), MonthlyPhaseOrder()...) {
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

	wantTail := append(append([]PhaseKind{}, DailyPhaseOrder()...), MonthlyPhaseOrder()...)
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

// --- BUG-269 SingleShardHook fast-path tests ---

// countingHook is a PhaseHook whose RunShard call count and per-shard
// argument are observable (mutex-guarded, never a map), so tests can
// prove HOW MANY TIMES and for WHICH shards RunShard was invoked — the
// direct way to distinguish "fast path took one call for shard 0" from
// "pooled path called all 256 shards" without depending on timing.
// shard0Effects (if set) is returned verbatim for shard 0 and mirrors
// runPhaseForHookFast's expected input; every other shard returns
// (nil, nil). singleShard, when true, additionally makes countingHook
// implement SingleShardHook via *singleShardCountingHook below.
type countingHook struct {
	mu            *sync.Mutex
	calls         *int
	calledShards  *[]int
	shard0Effects []Effect

	applyMu *sync.Mutex
	applied *[]Effect
}

func (h *countingHook) RunShard(shard int) ([]Effect, error) {
	h.mu.Lock()
	*h.calls++
	*h.calledShards = append(*h.calledShards, shard)
	h.mu.Unlock()
	if shard != 0 {
		return nil, nil
	}
	return h.shard0Effects, nil
}

func (h *countingHook) ApplyEffect(eff Effect) {
	h.applyMu.Lock()
	*h.applied = append(*h.applied, eff)
	h.applyMu.Unlock()
}

// singleShardCountingHook embeds countingHook and additionally
// implements SingleShardHook, always returning true — the explicit
// opt-in BUG-269 introduces. Kept as a distinct type (rather than a
// bool field checked from SingleShard) so the pooled-path comparison
// hook (plain *countingHook) provably does NOT satisfy SingleShardHook
// at the type-assertion runPhaseForHook performs — the same guarantee
// production hooks rely on.
type singleShardCountingHook struct {
	*countingHook
}

func (singleShardCountingHook) SingleShard() bool { return true }

// TestSingleShardHook_FastPathCallsShardZeroOnly proves the fast path
// (STEP 3's "distinguishable from the pooled path" requirement): a hook
// that opts into SingleShardHook has RunShard invoked exactly once, for
// shard 0, never for 1..255 — the whole point of BUG-269's change.
func TestSingleShardHook_FastPathCallsShardZeroOnly(t *testing.T) {
	var mu, applyMu sync.Mutex
	calls := 0
	var calledShards []int
	var applied []Effect

	hook := singleShardCountingHook{&countingHook{
		mu: &mu, calls: &calls, calledShards: &calledShards,
		shard0Effects: []Effect{{Sequence: 0, Payload: "fast"}},
		applyMu:       &applyMu, applied: &applied,
	}}

	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
		t.Fatalf("RegisterPhaseHook: %v", err)
	}
	if err := e.AdvanceTicks("corr-fast", 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("RunShard called %d times, want exactly 1 (fast path)", calls)
	}
	if len(calledShards) != 1 || calledShards[0] != 0 {
		t.Fatalf("RunShard called for shards %v, want [0]", calledShards)
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if len(applied) != 1 || applied[0].Payload != "fast" {
		t.Fatalf("applied = %v, want one effect with payload %q", applied, "fast")
	}
}

// TestNonOptedHook_StillUsesPooledPath proves (b): a hook that does NOT
// implement SingleShardHook is completely unaffected by BUG-269 — it
// still gets the full det.RunPhase treatment, RunShard called once per
// shard for all 256 shards.
func TestNonOptedHook_StillUsesPooledPath(t *testing.T) {
	var mu, applyMu sync.Mutex
	calls := 0
	var calledShards []int
	var applied []Effect

	hook := &countingHook{
		mu: &mu, calls: &calls, calledShards: &calledShards,
		shard0Effects: []Effect{{Sequence: 0, Payload: "pooled"}},
		applyMu:       &applyMu, applied: &applied,
	}

	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
		t.Fatalf("RegisterPhaseHook: %v", err)
	}
	if err := e.AdvanceTicks("corr-pooled", 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 256 {
		t.Fatalf("RunShard called %d times, want exactly 256 (pooled path, one hook not opted in)", calls)
	}
	sortedShards := append([]int{}, calledShards...)
	sort.Ints(sortedShards)
	for i, s := range sortedShards {
		if s != i {
			t.Fatalf("pooled path did not call every shard exactly once: calledShards[%d] = %d after sort, want %d", i, s, i)
		}
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if len(applied) != 1 || applied[0].Payload != "pooled" {
		t.Fatalf("applied = %v, want one effect with payload %q", applied, "pooled")
	}
}

// TestSingleShardHook_FastPathMatchesPooledPath is the (a) determinism
// equivalence test: the SAME shard-0 RunShard/ApplyEffect behaviour,
// driven once through the fast path (hook opts in) and once forced
// through the pooled path (an equivalent hook that does NOT opt in),
// must produce byte-identical applied Effects. This is the direct proof
// that runPhaseForHookFast's (Shard,Sequence)-degenerates-to-Sequence
// reasoning (see its doc comment) holds in practice, not just on paper.
func TestSingleShardHook_FastPathMatchesPooledPath(t *testing.T) {
	// Emit several effects from shard 0 with sequence numbers
	// deliberately out of emission order, so a bug that applied them in
	// slice order (rather than sorted Sequence order) would be visible.
	shard0 := []Effect{
		{Sequence: 3, Payload: "s0:3"},
		{Sequence: 1, Payload: "s0:1"},
		{Sequence: 0, Payload: "s0:0"},
		{Sequence: 2, Payload: "s0:2"},
	}

	var fastMu, fastApplyMu sync.Mutex
	fastCalls := 0
	var fastCalledShards []int
	var fastApplied []Effect
	fastHook := singleShardCountingHook{&countingHook{
		mu: &fastMu, calls: &fastCalls, calledShards: &fastCalledShards,
		shard0Effects: shard0, applyMu: &fastApplyMu, applied: &fastApplied,
	}}
	fastEngine := NewEngine(WithPoolSize(6))
	if err := fastEngine.RegisterPhaseHook(PhaseDailyTick, fastHook); err != nil {
		t.Fatalf("RegisterPhaseHook (fast): %v", err)
	}
	if err := fastEngine.AdvanceTicks("corr-equiv-fast", 1); err != nil {
		t.Fatalf("AdvanceTicks (fast): %v", err)
	}

	var pooledMu, pooledApplyMu sync.Mutex
	pooledCalls := 0
	var pooledCalledShards []int
	var pooledApplied []Effect
	pooledHook := &countingHook{
		mu: &pooledMu, calls: &pooledCalls, calledShards: &pooledCalledShards,
		shard0Effects: shard0, applyMu: &pooledApplyMu, applied: &pooledApplied,
	}
	pooledEngine := NewEngine(WithPoolSize(6))
	if err := pooledEngine.RegisterPhaseHook(PhaseDailyTick, pooledHook); err != nil {
		t.Fatalf("RegisterPhaseHook (pooled): %v", err)
	}
	if err := pooledEngine.AdvanceTicks("corr-equiv-pooled", 1); err != nil {
		t.Fatalf("AdvanceTicks (pooled): %v", err)
	}

	fastApplyMu.Lock()
	gotFast := append([]Effect{}, fastApplied...)
	fastApplyMu.Unlock()
	pooledApplyMu.Lock()
	gotPooled := append([]Effect{}, pooledApplied...)
	pooledApplyMu.Unlock()

	if len(gotFast) != len(shard0) || len(gotPooled) != len(shard0) {
		t.Fatalf("got %d fast / %d pooled applied effects, want %d each", len(gotFast), len(gotPooled), len(shard0))
	}
	for i := range gotFast {
		if gotFast[i] != gotPooled[i] {
			t.Fatalf("applied[%d]: fast=%v pooled=%v — fast path diverged from pooled path", i, gotFast[i], gotPooled[i])
		}
	}
	// Both must be in ascending Sequence order (0,1,2,3), proving the
	// fast path's inline sort matches det.ApplyBarrier's canonical order.
	for i, eff := range gotFast {
		if eff.Sequence != i {
			t.Fatalf("gotFast[%d].Sequence = %d, want %d (ascending canonical order)", i, eff.Sequence, i)
		}
	}
}

// TestSingleShardHookAssert_CatchesBrokenPromise is (d): a hook that
// opts into SingleShardHook (SingleShard() == true) but, in violation
// of its own promise, does real work on a shard other than 0 must be
// caught by WithSingleShardAssert's dev-mode safety net — proving the
// promise is guarded, not merely assumed.
func TestSingleShardHookAssert_CatchesBrokenPromise(t *testing.T) {
	hook := lyingSingleShardHook{}

	e := NewEngine(WithPoolSize(4), WithSingleShardAssert(true))
	if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
		t.Fatalf("RegisterPhaseHook: %v", err)
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("AdvanceTicks did not panic despite a SingleShardHook lying about shard 1 — safety net did not catch the broken promise")
		}
		msg := fmt.Sprintf("%v", r)
		if !containsAll(msg, "BUG-269", "shard 1") {
			t.Fatalf("panic message %q does not clearly identify the broken SingleShardHook promise", msg)
		}
	}()
	_ = e.AdvanceTicks("corr-lying", 1)
	t.Fatal("unreachable: AdvanceTicks should have panicked")
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// lyingSingleShardHook opts into SingleShardHook but actually emits an
// Effect on shard 1 too — the broken-promise case
// TestSingleShardHookAssert_CatchesBrokenPromise exercises.
type lyingSingleShardHook struct{}

func (lyingSingleShardHook) RunShard(shard int) ([]Effect, error) {
	if shard == 1 {
		return []Effect{{Sequence: 0, Payload: "shard1-should-not-exist"}}, nil
	}
	return nil, nil
}
func (lyingSingleShardHook) ApplyEffect(Effect) {}
func (lyingSingleShardHook) SingleShard() bool  { return true }

// fakeModule implements registry.Module minimally for tests.
type fakeModule struct {
	name    string
	version string
}

func (m fakeModule) Name() string            { return m.name }
func (m fakeModule) Version() string         { return m.version }
func (m fakeModule) Health() registry.Health { return registry.HealthOK }
