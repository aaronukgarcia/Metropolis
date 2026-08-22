package invariant

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// hookAttackFixture builds a 4-invariant registry (people/money/goods/
// vehicles — every invariant this package currently ships) and a Hook
// wired to record every logged error and every hard-fail invocation,
// so an attack test can assert exactly how many of each fired.
func hookAttackFixture(t *testing.T, provider SnapshotProvider, devMode bool) (*Hook, *[]*errs.E, *[]string) {
	t.Helper()
	reg := NewRegistry()
	for _, inv := range []Invariant{NewPeopleInvariant(), NewMoneyInvariant(), NewGoodsInvariant(), NewVehicleInvariant()} {
		if err := reg.Register(inv); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var logged []*errs.E
	var panics []string
	e := core.NewEngine()
	h := &Hook{
		engine:   e,
		registry: reg,
		provider: provider,
		devMode:  devMode,
		logSink: func(er *errs.E) {
			mu.Lock()
			defer mu.Unlock()
			logged = append(logged, er)
		},
		panicFn: func(msg string) {
			mu.Lock()
			defer mu.Unlock()
			panics = append(panics, msg)
		},
	}
	return h, &logged, &panics
}

func runTick(t *testing.T, h *Hook) {
	t.Helper()
	effects, err := h.RunShard(0)
	if err != nil {
		t.Fatalf("RunShard(0): %v", err)
	}
	for _, eff := range effects {
		h.ApplyEffect(eff)
	}
}

// TestAttack_PartialRegistration_ExactlySkippedCount is the "3 of 5"
// (here 2-of-4, since this package ships 4 invariants) edge the brief
// calls out: only SOME registered invariants are starved. The fix must
// report exactly the starved ones — not zero (under-reporting hides
// the defect class again), not all four (over-reporting would be its
// own false alarm, weakness pattern #1's "cries wolf").
func TestAttack_PartialRegistration_ExactlySkippedCount(t *testing.T) {
	provider := func(tick int64) Snapshot {
		s := NewSnapshot(tick)
		// people and money balanced+registered; goods and vehicles never
		// reported this tick (AC-12 skip, but on TWO of four stocks at
		// once).
		s.Readings[StockPeople] = StockReading{Registered: true, Opening: 10, Closing: 10, TrackedDelta: 0}
		s.Readings[StockMoney] = StockReading{Registered: true, Opening: 500, Closing: 500, TrackedDelta: 0}
		return s
	}
	h, logged, panics := hookAttackFixture(t, provider, true)
	runTick(t, h)

	if len(*panics) != 2 {
		t.Fatalf("hard-fail count = %d, want 2 (goods+vehicles starved, people+money clean)", len(*panics))
	}
	if len(*logged) != 2 {
		t.Fatalf("logged count = %d, want 2", len(*logged))
	}
	for _, e := range *logged {
		if e.Code != ErrConservationViolation {
			t.Errorf("logged code = %q, want %q", e.Code, ErrConservationViolation)
		}
	}
}

// TestAttack_ZeroRegisteredInvariants_VacuousPassIsHonest documents (and
// pins) the vacuous-pass edge: a Registry with NOTHING registered yet
// (legitimate bootstrap state, distinct from AC-12's "registered but
// not reported this tick") produces AllRan=true and must not log or
// hard-fail — there is nothing starved because nothing was ever
// promised. If this ever starts firing, RunSuite's AllRan default
// changed semantics silently.
func TestAttack_ZeroRegisteredInvariants_VacuousPassIsHonest(t *testing.T) {
	reg := NewRegistry()
	e := core.NewEngine()
	var mu sync.Mutex
	var logged []*errs.E
	var panicked bool
	h := &Hook{
		engine:   e,
		registry: reg,
		provider: func(tick int64) Snapshot { return NewSnapshot(tick) },
		devMode:  true,
		logSink:  func(er *errs.E) { mu.Lock(); defer mu.Unlock(); logged = append(logged, er) },
		panicFn:  func(string) { mu.Lock(); defer mu.Unlock(); panicked = true },
	}
	runTick(t, h)
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 0 || panicked {
		t.Fatalf("zero-registered tick logged=%d panicked=%v, want 0/false (vacuous pass)", len(logged), panicked)
	}
}

// TestAttack_AllRanFlapping_NoStateLeak proves the Hook carries no
// stale per-tick state across ticks: a starved tick followed by a
// fully-run tick must produce exactly one hard-fail/log (from the
// starved tick), and a fully-run tick followed by a starved one must
// likewise fire exactly once — the second tick's verdict is never
// contaminated by the first's.
func TestAttack_AllRanFlapping_NoStateLeak(t *testing.T) {
	var tick int64
	starved := true
	provider := func(t int64) Snapshot {
		s := NewSnapshot(t)
		if !starved {
			s.Readings[StockPeople] = StockReading{Registered: true, Opening: 10, Closing: 10, TrackedDelta: 0}
			s.Readings[StockMoney] = StockReading{Registered: true, Opening: 500, Closing: 500, TrackedDelta: 0}
			s.Readings[StockGoods] = StockReading{Registered: true, Opening: 1, Closing: 1, TrackedDelta: 0}
			s.Readings[StockVehicles] = StockReading{Registered: true, Opening: 1, Closing: 1, TrackedDelta: 0}
		}
		// starved tick: leave every stock at its Registered:false zero value.
		return s
	}
	h, logged, panics := hookAttackFixture(t, provider, true)

	// Tick 0: starved.
	runTick(t, h)
	if len(*panics) != 4 || len(*logged) != 4 {
		t.Fatalf("tick0 (starved) panics=%d logged=%d, want 4/4", len(*panics), len(*logged))
	}

	// Tick 1: fully registered and balanced — must add ZERO new
	// panics/logs (proves no leaked "still starved" state).
	starved = false
	tick = 1
	_ = tick
	runTick(t, h)
	if len(*panics) != 4 || len(*logged) != 4 {
		t.Fatalf("tick1 (clean) panics=%d logged=%d, want unchanged 4/4 (state leaked across ticks)", len(*panics), len(*logged))
	}

	// Tick 2: starved again — must fire again, proving the clean tick
	// didn't latch a false "never starves again" state either.
	starved = true
	runTick(t, h)
	if len(*panics) != 8 || len(*logged) != 8 {
		t.Fatalf("tick2 (starved again) panics=%d logged=%d, want 8/8", len(*panics), len(*logged))
	}
}

// TestAttack_Starvation_ReleaseModeNeverHardFails is the dev-mode
// gating edge: with devMode explicitly false (the release default),
// panicFn must NEVER be invoked for a starved invariant, no matter how
// many stocks are missing — only the logged error path is loud.
func TestAttack_Starvation_ReleaseModeNeverHardFails(t *testing.T) {
	provider := func(tick int64) Snapshot { return NewSnapshot(tick) } // everything starved
	h, logged, panics := hookAttackFixture(t, provider, false)
	runTick(t, h)

	if len(*panics) != 0 {
		t.Fatalf("release mode hard-failed %d times on starvation, want 0 — devMode gating is broken", len(*panics))
	}
	if len(*logged) != 4 {
		t.Fatalf("release mode logged %d, want 4 (one per starved invariant)", len(*logged))
	}
}
