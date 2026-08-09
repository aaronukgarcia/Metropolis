package core

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SEC-018 (Destructive-2, round 3, P1): SEC-016 guarded the two e.mu
// sites its own PoC exercised (RegisterPhaseHook, seal()). The hang
// mechanism SEC-016 described — a copy's mu bytes captured mid-lock on
// the original, then blocking forever on that copy's own Lock() —
// applies identically to every OTHER e.mu.Lock() site in this package,
// not just those two. Tester-1 reproduced it live against Clock(): 3,000
// copies racing a lock hammer, then Clock() on each — only 1,786
// returned, the rest wedged permanently on the same
// runtime_SemacquireMutex signature.
//
// ENUMERATION METHOD (so the next one is findable, per Bill's ask): grep
// for the literal call `e.mu.Lock()` across every non-test .go file in
// this package, then map each match back to its enclosing `func (e
// *Engine) ...` by source position:
//
//	awk '/^func \(e \*Engine\)/{fn=$0} /e\.mu\.Lock\(\)/{if ($0 !~ /\/\//) print FILENAME":"FNR": "fn}' \
//	    internal/engine/core/engine.go internal/engine/core/commands.go internal/engine/core/persist.go
//
// This intentionally excludes subscribe.go's `s.mu.Lock()` — that guards
// SubscriptionServer, a DIFFERENT struct with its own identity, not
// Engine, and is out of scope for an Engine-struct-copy attack (a
// SubscriptionServer copy would be a separate finding against a separate
// type, not a sibling of this one). It found exactly eight sites:
//
//  1. engine.go   Clock()               — was UNGUARDED, now guarded
//  2. engine.go   RegisterPhaseHook()   — guarded since SEC-016
//  3. engine.go   seal()                — guarded since SEC-016
//  4. engine.go   advanceOneDailyTick() — deliberately left unguarded;
//     unexported, single call site (AdvanceTicks' loop), which only
//     ever reaches it after seal() has already rejected a copy, and
//     self never changes for the Engine's lifetime — see that
//     function's doc comment and the dispatch report's ASM for the
//     reasoning (adding a redundant check here would cost one atomic
//     load PER TICK for zero additional safety)
//  5. commands.go handleSetSpeed()      — was UNGUARDED, now guarded
//     (directly, and transitively via HandleCommand's entry check)
//  6. commands.go handlePause()         — was UNGUARDED, now guarded
//     (directly, and transitively via HandleCommand's entry check)
//  7. commands.go handleResume()        — was UNGUARDED, now guarded
//     (directly, and transitively via HandleCommand's entry check)
//  8. persist.go  snapshotStateLocked() — was UNGUARDED; guarded via its
//     only caller, Snapshot(), which now checks before calling it
//
// TestSEC018_Deterministic_AllGuardedSites_RejectedNotHung proves all
// eight sites are now safe using a DETERMINISTIC construction of SEC-016/
// SEC-018's attack state, per Bill's second ask this round: the
// concurrent-hammer construction (sec016_poc_test.go) can only catch mu
// mid-lock by timing luck across thousands of iterations, and is itself
// a data race by construction, so it is excluded from -race builds —
// which means, as Tester-1 found, NOTHING under `go test ./... -race`
// would have caught a regression back to check-after-lock ordering.
// This test builds the identical attack state WITHOUT timing luck and
// WITHOUT racing anything:
//
//	e.mu.Lock()        // single goroutine, no race with anything
//	e2 := e2Copy(e)     // copy while WE hold the lock
//	e.mu.Unlock()
//	// e2's mu now byte-identically represents "locked, owned by nobody
//	// who will ever unlock THIS copy's address" — SEC-016's exact
//	// attack state, built deterministically.
//
// This runs clean under `go vet ./...` and `go test ./... -race` against
// the fix, and (verified the same way SEC-003/SEC-014/SEC-016's PoCs
// were: run against the code exactly as it stood before this round's
// guards existed) hangs within the bounded per-call timeout against the
// unguarded shape — see this file's git history / the dispatch report
// for that before/after run.
func TestSEC018_Deterministic_AllGuardedSites_RejectedNotHung(t *testing.T) {
	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("seed RegisterPhaseHook: %v", err)
	}

	// Deterministic construction of SEC-016's exact attack state:
	// single-goroutine copy while e's own mu is held, no timing luck, no
	// data race (only one goroutine ever touches e or e2's memory during
	// the copy itself).
	e.mu.Lock()
	e2 := e2Copy(e)
	e.mu.Unlock()

	pauseCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	resumeCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindResume, Payload: protocol.ResumePayload{}}
	setSpeedCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}}

	cases := []struct {
		name string
		call func() error
	}{
		{"Clock", func() error { _, err := e2.Clock(); return err }},
		{"RegisterPhaseHook", func() error { return e2.RegisterPhaseHook(PhaseConsumptionShortfall, noopHook{}) }},
		{"AdvanceTicks(seal)", func() error { return e2.AdvanceTicks("sec018-det-advance", 1) }},
		{"Snapshot(snapshotStateLocked)", func() error {
			var buf bytes.Buffer
			_, err := e2.Snapshot(&buf, "sec018-det-snapshot")
			return err
		}},
		{"HandleCommand(Pause->handlePause)", func() error {
			return commandResultErr(e2.HandleCommand(pauseCmd))
		}},
		{"HandleCommand(Resume->handleResume)", func() error {
			return commandResultErr(e2.HandleCommand(resumeCmd))
		}},
		{"HandleCommand(SetSpeed->handleSetSpeed)", func() error {
			return commandResultErr(e2.HandleCommand(setSpeedCmd))
		}},
	}

	for _, c := range cases {
		done := make(chan error, 1)
		go func() { done <- c.call() }()
		select {
		case err := <-done:
			if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
				t.Errorf("%s on deterministically mid-lock-copied Engine: err = %v, want ErrEngineCopied", c.name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("SEC-018 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", c.name)
		}
	}

	// The ORIGINAL Engine must be completely unaffected — its mu was
	// only ever held and released normally by us, once, to take the
	// copy.
	if err := e.AdvanceTicks("sec018-original-still-works", 1); err != nil {
		t.Fatalf("original Engine AdvanceTicks after copy attack: %v", err)
	}
}

// commandResultErr extracts the registry-sourced error a rejected
// protocol.CommandResult carries, or nil if the result was Accepted.
// toErrorRef (commands.go) only ever loses the original *errs.E's Code
// in the (unreachable-here) fallback branch, so reconstructing a
// minimal *errs.E from the ErrorRef's Code is sufficient for this test's
// errors.Is(err, &errs.E{Code: ErrEngineCopied}) comparison (Is compares
// Code only — see errs.E.Is's doc comment).
func commandResultErr(res protocol.CommandResult) error {
	if res.Accepted || res.Error == nil {
		return nil
	}
	return &errs.E{Code: res.Error.Code}
}
