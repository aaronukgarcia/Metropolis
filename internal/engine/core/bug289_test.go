package core

import (
	"errors"
	"sync"
	"testing"
)

// BUG-289 regression tests. advanceOneDailyTick used to commit the clock
// (clock.advanceOneDay + tickCounter bump) BEFORE the monthly phase
// pipeline ran, so a monthly-hook failure left the engine past the month
// boundary (Tick==30, Month()==1) with most of the month's phases never
// executed — and no API to re-run them. These tests pin the fixed
// behaviour: a failed month leaves every engine-owned observation point
// (Clock copy, TicksCompleted) exactly as it was at month-start, and a
// retried AdvanceTicks re-runs day 30 plus the whole monthly pipeline to
// completion.
//
// Scope note: these assertions cover the state engine.core itself owns
// and can restore. Hook/module-owned state mutated by an EARLIER phase
// of the failing month is the owning module's concern (engine.core has
// no visibility into it under GR#20's protocol-only split).

// errBug289Sentinel is returned by the test hooks' RunShard so the
// ErrPhaseHookFailed-wrapped error surfaced through AdvanceTicks can be
// identified via errors.Is.
var errBug289Sentinel = errors.New("bug289 synthetic monthly failure")

// failingEverywhereHook is a PhaseHook whose RunShard fails on every
// shard with the bug289 sentinel.
type failingEverywhereHook struct{}

func (failingEverywhereHook) RunShard(int) ([]Effect, error) {
	return nil, errBug289Sentinel
}
func (failingEverywhereHook) ApplyEffect(Effect) {}

// TestBUG289_MonthlyFailure_RestoresMonthStartState drives exactly 30
// ticks (one month boundary) with production failing and asserts the
// engine is left precisely at month-start: Tick 29, Month 0, DayInMonth
// 29, TicksCompleted 29 — not the committed-but-unsettled Tick 30 /
// Month 1 the bug produced.
func TestBUG289_MonthlyFailure_RestoresMonthStartState(t *testing.T) {
	e := NewEngine(WithPoolSize(2))

	if err := e.RegisterPhaseHook(PhaseProduction, failingEverywhereHook{}); err != nil {
		t.Fatalf("RegisterPhaseHook(production): %v", err)
	}

	err := e.AdvanceTicks("corr-bug289", 30)
	if err == nil {
		t.Fatal("AdvanceTicks: want error from failing monthly production hook, got nil")
	}
	if !errors.Is(err, errBug289Sentinel) {
		t.Fatalf("AdvanceTicks error = %v, want it to wrap the bug289 sentinel", err)
	}

	c := clockOrFatal(t, e)
	if got := c.Tick(); got != 29 {
		t.Errorf("Tick() = %d after failed month, want 29 (month-start state)", got)
	}
	if got := c.Month(); got != 0 {
		t.Errorf("Month() = %d after failed month, want 0", got)
	}
	if got := c.DayInMonth(); got != 29 {
		t.Errorf("DayInMonth() = %d after failed month, want 29", got)
	}
	if got := e.TicksCompleted(); got != 29 {
		t.Errorf("TicksCompleted() = %d after failed month, want 29", got)
	}
}

// TestBUG289_MonthlyFailure_LaterPhasesNeverRan proves the abort still
// holds (AC-10) alongside the rollback: finance, the last monthly phase,
// must never have executed for the failed month.
func TestBUG289_MonthlyFailure_LaterPhasesNeverRan(t *testing.T) {
	e := NewEngine(WithPoolSize(2))

	if err := e.RegisterPhaseHook(PhaseProduction, failingEverywhereHook{}); err != nil {
		t.Fatalf("RegisterPhaseHook(production): %v", err)
	}

	var laterRan bool
	var laterMu sync.Mutex
	later := laterHook{ran: &laterRan, mu: &laterMu}
	if err := e.RegisterPhaseHook(PhaseFinance, later); err != nil {
		t.Fatalf("RegisterPhaseHook(finance): %v", err)
	}

	if err := e.AdvanceTicks("corr-bug289-abort", 30); err == nil {
		t.Fatal("AdvanceTicks: want error from failing monthly production hook, got nil")
	}

	laterMu.Lock()
	defer laterMu.Unlock()
	if laterRan {
		t.Fatal("finance phase hook ran despite an earlier monthly phase erroring — AC-10 violated")
	}
}

// failFirstMonthThenSucceedHook fails RunShard on shard 0 exactly once
// (the first monthly attempt) and succeeds thereafter, modelling a
// transient downstream failure — the shape the BOW item's retry ruling
// cares about.
type failFirstMonthThenSucceedHook struct {
	mu     sync.Mutex
	failed bool
}

func (h *failFirstMonthThenSucceedHook) RunShard(shard int) ([]Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.failed {
		h.failed = true
		return nil, errBug289Sentinel
	}
	return nil, nil
}
func (*failFirstMonthThenSucceedHook) ApplyEffect(Effect) {}

// countingFinanceHook counts RunShard invocations (mutex-guarded), so a
// test can prove finance ran (or never ran) for a given attempt.
type countingFinanceHook struct {
	mu   sync.Mutex
	runs int
}

func (h *countingFinanceHook) RunShard(shard int) ([]Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	h.mu.Lock()
	h.runs++
	h.mu.Unlock()
	return nil, nil
}
func (*countingFinanceHook) ApplyEffect(Effect) {}

func (h *countingFinanceHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs
}

// TestBUG289_FailedMonth_RetryCompletesSettlement proves the rollback
// buys real retry semantics: after the failed attempt left the engine at
// month-start, one more AdvanceTicks re-runs day 30 AND the entire
// monthly pipeline (finance runs exactly once for the settled month),
// landing on Tick 30 / Month 1 / TicksCompleted 30.
func TestBUG289_FailedMonth_RetryCompletesSettlement(t *testing.T) {
	e := NewEngine(WithPoolSize(2))

	flaky := &failFirstMonthThenSucceedHook{}
	if err := e.RegisterPhaseHook(PhaseProduction, flaky); err != nil {
		t.Fatalf("RegisterPhaseHook(production): %v", err)
	}

	finance := &countingFinanceHook{}
	if err := e.RegisterPhaseHook(PhaseFinance, finance); err != nil {
		t.Fatalf("RegisterPhaseHook(finance): %v", err)
	}

	if err := e.AdvanceTicks("corr-bug289-fail", 30); err == nil {
		t.Fatal("AdvanceTicks (first attempt): want error, got nil")
	}
	if got := finance.count(); got != 0 {
		t.Fatalf("finance ran %d times during the failed month, want 0", got)
	}

	if err := e.AdvanceTicks("corr-bug289-retry", 1); err != nil {
		t.Fatalf("AdvanceTicks (retry): %v", err)
	}

	if got := finance.count(); got != 1 {
		t.Errorf("finance ran %d times across the retried month, want exactly 1", got)
	}

	c := clockOrFatal(t, e)
	if got := c.Tick(); got != 30 {
		t.Errorf("Tick() = %d after retried month, want 30", got)
	}
	if got := c.Month(); got != 1 {
		t.Errorf("Month() = %d after retried month, want 1", got)
	}
	if got := e.TicksCompleted(); got != 30 {
		t.Errorf("TicksCompleted() = %d after retried month, want 30", got)
	}
}
