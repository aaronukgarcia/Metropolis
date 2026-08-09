package core

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSEC003_RegisterPhaseHook_RejectedAfterSeal_Deterministic is the
// deterministic, single-goroutine proof that AdvanceTicks seals
// RegisterPhaseHook (SEC-003's fix): drive one AdvanceTicks call to
// completion first, so the engine is DEFINITELY sealed (no scheduling
// involved — seal() has already returned by the time AdvanceTicks
// itself returns), then assert a subsequent RegisterPhaseHook call
// observes ErrEngineSealed every single time.
//
// This replaces relying on the concurrent hammer below
// (TestSEC003_ConcurrentRegisterDuringAdvanceTicks) to ever land a
// RegisterPhaseHook call after the seal — CI proved that assumption
// false: the hammer's ordering depends on goroutine scheduling, and on
// at least one CI run every RegisterPhaseHook call completed before
// AdvanceTicks' single seal() call, so the invariant held perfectly but
// the test had no way to observe it ("no RegisterPhaseHook call
// observed ErrEngineSealed"). Per the "make it deterministic, don't
// make it more likely" principle (same class as BUG-005/BUG-006): this
// test removes scheduling from the picture entirely rather than trying
// to improve the odds with retries, sleeps, or a larger iteration count.
func TestSEC003_RegisterPhaseHook_RejectedAfterSeal_Deterministic(t *testing.T) {
	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("seed RegisterPhaseHook (pre-seal): %v", err)
	}

	// Drive exactly one AdvanceTicks call to completion. seal() runs
	// synchronously at the top of AdvanceTicks and has unconditionally
	// returned — sealed is true — before this call itself returns, so
	// there is no window, no race, no "might not have landed yet": the
	// engine IS sealed by the time control returns here.
	if err := e.AdvanceTicks("sec003-det-advance", 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	if err := e.RegisterPhaseHook(PhaseProduction, noopHook{}); !errors.Is(err, &errs.E{Code: ErrEngineSealed}) {
		t.Fatalf("RegisterPhaseHook after AdvanceTicks: err = %v, want ErrEngineSealed", err)
	}
}

// TestSEC003_ConcurrentRegisterDuringAdvanceTicks reproduces SEC-003's
// attack/misuse path against the FIXED engine and asserts it is no
// longer a race: one goroutine hammers AdvanceTicks (which used to read
// e.hooks[kind] via runPhase with no lock at all) while another
// concurrently hammers RegisterPhaseHook (which mutates e.hooks[kind]
// under e.mu). This exact test, run byte-for-byte against a
// `git archive HEAD` extraction of commit f81e5d7 (internal/engine/core
// has been tracked since that commit — confirmed via
// `git show HEAD:internal/engine/core/phase.go`, byte-identical to the
// extracted file; an earlier draft of this comment incorrectly claimed
// the package was untracked and used a hand-reconstructed pre-fix copy
// instead — that claim was wrong and has been corrected here, see
// ASM-028/029's follow-up note), produced:
//
//	WARNING: DATA RACE
//	Read at 0x... by goroutine 9: ... Engine.runPhase() phase.go:202
//	Previous write at 0x... by goroutine 8: ... Engine.RegisterPhaseHook() engine.go:247
//
// i.e. a genuine, race-detector-confirmed concurrent map read+write
// between exactly these two call sites, at the exact line numbers of
// the real committed code (not a reconstruction) — the same class of
// bug that, without -race instrumenting the access, the Go runtime
// treats as a fatal, unrecoverable process crash (no panic/recover).
// Against the fixed engine below, the same two goroutines hammering the
// same two methods produce no race and no crash.
//
// This test asserts ONLY the property it can actually guarantee
// regardless of scheduling: every RegisterPhaseHook result is either a
// clean success or ErrEngineSealed, never a race and never a third
// outcome. It deliberately does NOT assert that at least one call
// observed ErrEngineSealed — that depends on scheduling (see CI's
// failure above) and is proven deterministically instead by
// TestSEC003_RegisterPhaseHook_RejectedAfterSeal_Deterministic.
func TestSEC003_ConcurrentRegisterDuringAdvanceTicks(t *testing.T) {
	e := NewEngine(WithPoolSize(4))

	// Seed one hook so AdvanceTicks has phase work to iterate — this is
	// what makes goroutine A's every AdvanceTicks call actually reach
	// runPhase's e.hooks[kind] read, not skip it on an empty map.
	if err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("seed RegisterPhaseHook: %v", err)
	}

	stop := make(chan struct{})
	var wgA sync.WaitGroup

	// Goroutine A: hammer AdvanceTicks. Its very first call seals the
	// engine (Engine.seal, engine.go) before running a single phase —
	// every runPhase read after that point is provably safe because no
	// writer can succeed against a sealed engine (see the Engine.sealed
	// field's doc comment).
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = e.AdvanceTicks("poc-advance", 1)
			}
		}
	}()

	// Goroutine B (this goroutine): hammer RegisterPhaseHook against a
	// monthly phase concurrently with A's AdvanceTicks calls — the exact
	// "looks like an ordinary call sequence" misuse path SEC-003
	// describes. Every result must be either a clean success (raced
	// ahead of A's first seal) or ErrEngineSealed (rejected once
	// sealed) — never anything else, and (enforced by -race) never a
	// race. Whether ANY call actually lands after the seal is a
	// scheduling detail this test does not depend on (see the
	// deterministic sibling test above for that guarantee).
	var sawSuccess, sawSealed, sawOther int
	for i := 0; i < 2000; i++ {
		err := e.RegisterPhaseHook(PhaseProduction, noopHook{})
		switch {
		case err == nil:
			sawSuccess++
		case errors.Is(err, &errs.E{Code: ErrEngineSealed}):
			sawSealed++
		default:
			sawOther++
			t.Errorf("RegisterPhaseHook call %d: unexpected error %v (want nil or ErrEngineSealed)", i, err)
		}
	}

	close(stop)
	wgA.Wait()

	if sawOther != 0 {
		t.Fatalf("saw %d RegisterPhaseHook results that were neither success nor ErrEngineSealed", sawOther)
	}
	t.Logf("RegisterPhaseHook outcomes: %d succeeded before seal, %d rejected (sealed) — both are valid outcomes; this test does not require any particular split", sawSuccess, sawSealed)
}

type noopHook struct{}

func (noopHook) RunShard(shard int) ([]Effect, error) { return nil, nil }
func (noopHook) ApplyEffect(Effect)                   {}
