package invariant

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// brokenPeopleProvider always reports an untracked people loss for the
// given tick — the deliberately-broken fixture the hook-level tests
// drive through, so both the dev-mode hard-fail path and the
// release-mode logging path are proven to actually fire, not merely
// asserted by comment.
func brokenPeopleProvider(tick int64) Snapshot {
	s := NewSnapshot(tick)
	s.Readings[StockPeople] = StockReading{Registered: true, Opening: 10, Closing: 9, TrackedDelta: 0}
	return s
}

func newTestHook(t *testing.T, opts ...HookOption) (*Hook, *core.Engine) {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(NewPeopleInvariant()); err != nil {
		t.Fatal(err)
	}
	e := core.NewEngine()
	h := &Hook{engine: e, registry: reg, provider: brokenPeopleProvider}
	for _, opt := range opts {
		opt(h)
	}
	return h, e
}

// TestHook_DevModeHardFail is AC-8: in dev mode, a Detected Violation
// triggers the hard-fail path. Uses WithPanicFunc's test-only override
// (per AC-8's check note) rather than a real, process-killing panic.
func TestHook_DevModeHardFail(t *testing.T) {
	var captured string
	h, _ := newTestHook(t, WithDevMode(true), WithPanicFunc(func(msg string) { captured = msg }))

	effects, err := h.RunShard(0)
	if err != nil {
		t.Fatalf("RunShard(0): %v", err)
	}
	for _, eff := range effects {
		h.ApplyEffect(eff)
	}

	if captured == "" {
		t.Fatal("dev-mode hard-fail path did not fire for a Detected Violation")
	}
	if !strings.Contains(captured, "people") {
		t.Errorf("hard-fail diagnostic %q does not name the invariant", captured)
	}
	if !strings.Contains(captured, "tick=0") {
		t.Errorf("hard-fail diagnostic %q does not name the tick", captured)
	}
}

// TestHook_DevModeNoFailWhenBalanced proves the alarm does not cry wolf
// (weakness pattern #1): a balanced snapshot must never trigger the
// hard-fail path, even in dev mode.
func TestHook_DevModeNoFailWhenBalanced(t *testing.T) {
	balanced := func(tick int64) Snapshot {
		s := NewSnapshot(tick)
		s.Readings[StockPeople] = StockReading{Registered: true, Opening: 10, Closing: 10, TrackedDelta: 0}
		return s
	}
	fired := false
	reg := NewRegistry()
	if err := reg.Register(NewPeopleInvariant()); err != nil {
		t.Fatal(err)
	}
	e := core.NewEngine()
	h := &Hook{engine: e, registry: reg, provider: balanced, devMode: true, panicFn: func(string) { fired = true }}

	effects, err := h.RunShard(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, eff := range effects {
		h.ApplyEffect(eff)
	}
	if fired {
		t.Fatal("hard-fail path fired for a legitimately balanced tick — an alarm that cries wolf on correct behaviour gets ignored (SEC-026's class)")
	}
}

// TestHook_ReleaseModeLogs is AC-9: outside dev mode, a Detected
// Violation produces a registry-sourced logged error and never panics.
func TestHook_ReleaseModeLogs(t *testing.T) {
	var mu sync.Mutex
	var logged []*errs.E
	h, _ := newTestHook(t, WithDevMode(false), WithLogSink(func(e *errs.E) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, e)
	}))

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("release-mode path panicked: %v", r)
			}
		}()
		effects, err := h.RunShard(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, eff := range effects {
			h.ApplyEffect(eff)
		}
	}()

	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 {
		t.Fatalf("len(logged) = %d, want 1", len(logged))
	}
	if logged[0].Code != ErrConservationViolation {
		t.Errorf("logged error code = %q, want %q", logged[0].Code, ErrConservationViolation)
	}
}

// TestHook_RunShard_OnlyShardZeroWorks proves the documented shape:
// every shard other than 0 is a cheap no-op, and never itself detects
// or reports a violation.
func TestHook_RunShard_OnlyShardZeroWorks(t *testing.T) {
	h, _ := newTestHook(t)
	for _, shard := range []int{1, 2, 255} {
		effects, err := h.RunShard(shard)
		if err != nil {
			t.Fatalf("RunShard(%d): %v", shard, err)
		}
		if effects != nil {
			t.Fatalf("RunShard(%d) returned %v, want nil", shard, effects)
		}
	}
}

// TestHook_ConcurrentShards_NoRace is AC-16: RunShard must be safe to
// call concurrently for many shards, as det.RunPhase does across the
// POOL-SIM worker pool.
func TestHook_ConcurrentShards_NoRace(t *testing.T) {
	h, _ := newTestHook(t, WithDevMode(false), WithLogSink(func(*errs.E) {}))

	var wg sync.WaitGroup
	for shard := 0; shard < 256; shard++ {
		shard := shard
		wg.Add(1)
		go func() {
			defer wg.Done()
			effects, err := h.RunShard(shard)
			if err != nil {
				t.Errorf("RunShard(%d): %v", shard, err)
				return
			}
			for _, eff := range effects {
				h.ApplyEffect(eff)
			}
		}()
	}
	wg.Wait()
}
