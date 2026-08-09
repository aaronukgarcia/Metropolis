package core

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

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
	// race.
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
	// At least one call must have been sealed-rejected: AdvanceTicks
	// seals on its very first call, and 2000 RegisterPhaseHook calls
	// racing against a tight AdvanceTicks loop overwhelmingly land after
	// that first seal.
	if sawSealed == 0 {
		t.Fatal("no RegisterPhaseHook call observed ErrEngineSealed — the seal-on-first-AdvanceTicks invariant did not engage during this run")
	}
	t.Logf("RegisterPhaseHook outcomes: %d succeeded before seal, %d rejected (sealed)", sawSuccess, sawSealed)
}

type noopHook struct{}

func (noopHook) RunShard(shard int) ([]Effect, error) { return nil, nil }
func (noopHook) ApplyEffect(Effect)                   {}
