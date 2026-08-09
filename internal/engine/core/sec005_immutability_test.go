package core

import (
	"sync"
	"testing"
)

// TestPhaseOrder_ReturnedCopyMutationDoesNotAffectEngine proves SEC-005's
// fix: DailyPhaseOrder()/MonthlyPhaseOrder() return a defensive copy, so
// a caller mutating what it got back (reversing it, in place, with a
// plain assignment — no unsafe, no reflection, exactly the Destructive
// agent's original PoC) cannot touch the Engine's actual execution
// order. Obtains the order via the only API a caller now has, mutates
// it, then drives a real Engine through one month and asserts the
// observed phase sequence is still the documented, un-reversed one.
func TestPhaseOrder_ReturnedCopyMutationDoesNotAffectEngine(t *testing.T) {
	got := MonthlyPhaseOrder()

	// Reverse the caller's copy in place — the exact attack from
	// SEC-005: "core.MonthlyPhaseOrder[0] = core.PhaseFinance" or a full
	// reversal, no unsafe/reflection required.
	for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
		got[i], got[j] = got[j], got[i]
	}
	// Also try truncating and appending a bogus phase, for good measure.
	// The mutated local slice is logged below (not just discarded) so
	// this assignment isn't a dead store (staticcheck SA4006) — the
	// logged value is itself evidence the mutation attempt was genuinely
	// made against the CALLER's copy, distinct from whatever
	// MonthlyPhaseOrder() returns on a later, fresh call.
	got = append(got[:2], PhaseKind("bogus-injected-phase"))
	t.Logf("caller's local copy after reverse+truncate+append (must not affect the Engine below): %v", got)

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

	for _, phase := range MonthlyPhaseOrder() {
		if err := e.RegisterPhaseHook(phase, noopHook{}); err != nil {
			t.Fatalf("RegisterPhaseHook(%s): %v", phase, err)
		}
	}

	// Drive exactly one month (30 daily ticks) so the monthly pipeline
	// runs exactly once.
	if err := e.AdvanceTicks("corr-sec005", 30); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	wantMonthly := MonthlyPhaseOrder()
	mu.Lock()
	gotTail := append([]PhaseKind{}, observed[len(observed)-len(wantMonthly):]...)
	mu.Unlock()

	for i, w := range wantMonthly {
		if gotTail[i] != w {
			t.Fatalf("engine's actual monthly execution order at index %d = %v, want %v (full observed tail: %v) — a caller's local mutation of DailyPhaseOrder()/MonthlyPhaseOrder()'s returned copy corrupted the Engine's real pipeline order", i, gotTail[i], w, gotTail)
		}
	}
	if wantMonthly[0] != PhaseProduction || wantMonthly[len(wantMonthly)-1] != PhaseFinance {
		t.Fatalf("MonthlyPhaseOrder() itself returned a corrupted order on a FRESH call: %v — the mutation of an earlier returned copy leaked into the package's real backing array", wantMonthly)
	}
}

// TestPhaseOrder_FunctionsReturnFreshCopyEachCall proves two separate
// calls to DailyPhaseOrder()/MonthlyPhaseOrder() never alias the same
// backing array — mutating one call's result must never be observable
// through a second call, which is the property that makes the "return a
// copy" fix actually work rather than just moving the same bug behind a
// function call.
func TestPhaseOrder_FunctionsReturnFreshCopyEachCall(t *testing.T) {
	first := MonthlyPhaseOrder()
	first[0] = PhaseFinance // corrupt the caller's own copy

	second := MonthlyPhaseOrder()
	if second[0] != PhaseProduction {
		t.Fatalf("MonthlyPhaseOrder() second call = %v, want first element %q (unaffected by mutating an earlier call's returned slice) — got %q", second, PhaseProduction, second[0])
	}

	firstDaily := DailyPhaseOrder()
	if len(firstDaily) > 0 {
		firstDaily[0] = PhaseKind("corrupted")
	}
	secondDaily := DailyPhaseOrder()
	if len(secondDaily) > 0 && secondDaily[0] != PhaseDailyTick {
		t.Fatalf("DailyPhaseOrder() second call = %v, want first element %q — got %q", secondDaily, PhaseDailyTick, secondDaily[0])
	}
}

// TestValidPhase_UnaffectedByReturnedCopyMutation proves validPhase
// (RegisterPhaseHook's gate) reads the package's real, unexported
// backing arrays — not anything reachable through the exported
// copy-returning functions — so mutating a caller's copy can never
// smuggle a bogus PhaseKind past RegisterPhaseHook's validation, nor
// remove a legitimate one from it (the second half of SEC-005's finding:
// "a caller can additionally inject a bogus PhaseKind ... or remove a
// legitimate one").
func TestValidPhase_UnaffectedByReturnedCopyMutation(t *testing.T) {
	order := MonthlyPhaseOrder()
	order[0] = PhaseKind("bogus-injected-phase") // "remove" PhaseProduction from the caller's view
	order = append(order, PhaseKind("another-bogus-phase"))
	_ = order // the mutation above must not reach validPhase's source

	e := NewEngine()

	// The legitimate phase this mutation "erased" from the caller's copy
	// must still validate.
	if err := e.RegisterPhaseHook(PhaseProduction, noopHook{}); err != nil {
		t.Fatalf("RegisterPhaseHook(PhaseProduction): want success (still a valid phase), got %v", err)
	}
	// The bogus phase injected into the caller's copy must still be
	// rejected — it was never actually added to the real order.
	if err := e.RegisterPhaseHook(PhaseKind("bogus-injected-phase"), noopHook{}); err == nil {
		t.Fatal("RegisterPhaseHook(bogus-injected-phase): want error, got nil — a caller's mutated copy leaked into validPhase's real backing data")
	}
}
