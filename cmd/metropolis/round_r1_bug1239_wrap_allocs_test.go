package main

// Round r1 (BUG-1239), hostile reviewer: the viewTicks wrap runs on every
// delta of every bound view in the shipped game. It must add no allocation
// per delta, record the tick only after the wrapped apply has run, and a
// result for the boot Resume must fire Ready whether the engine accepted or
// refused it (a refusal is never going to be applied either).

import (
	"testing"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestRoundR1_BUG1239_WrapAddsNoAllocationPerDelta(t *testing.T) {
	v := newViewTicks()
	var applied int64
	inner := func(d protocol.Delta) { applied = int64(d.Tick) }
	wrapped := v.wrap("r1.view", inner)
	d := protocol.Delta{Tick: 7}
	baseline := testing.AllocsPerRun(1000, func() { inner(d) })
	got := testing.AllocsPerRun(1000, func() { wrapped(d) })
	if got != baseline {
		t.Fatalf("wrap allocates per delta: %v allocations against %v for the bare apply", got, baseline)
	}
	if applied != 7 || v.settledTick() != 7 {
		t.Fatalf("applied %d settled %d, want 7 and 7", applied, v.settledTick())
	}
}

func TestRoundR1_BUG1239_TickRecordedOnlyAfterApply(t *testing.T) {
	v := newViewTicks()
	var seenDuringApply int64 = -1
	wrapped := v.wrap("r1.view", func(protocol.Delta) { seenDuringApply = v.settledTick() })
	wrapped(protocol.Delta{Tick: 42})
	if seenDuringApply != 0 {
		t.Fatalf("settledTick read %d inside the apply: the tick was recorded before the view showed it", seenDuringApply)
	}
	if v.settledTick() != 42 {
		t.Fatalf("settledTick %d after apply, want 42", v.settledTick())
	}
}

func TestRoundR1_BUG1239_RefusedResumeResultStillFiresReady(t *testing.T) {
	d := newTickDriver(func() (enginecore.Clock, error) { return enginecore.Clock{}, nil },
		func(protocol.Command) error { return nil }, 1000000)
	d.ApplyResult(protocol.CommandResult{CorrelationID: d.ResumeCorrelationID(), Accepted: false})
	if !isClosed(d.Ready()) {
		t.Fatal("a refused boot Resume left Ready open: a waiter would hang for the full deadline")
	}
}
