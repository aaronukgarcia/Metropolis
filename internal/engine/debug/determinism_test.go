package debug

import (
	"bytes"
	"sync"
	"testing"

	core "github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// TestTogglingDebugDoesNotAffectEngineState is AC-13: enabling/
// disabling debug and invoking cheats does not retroactively change
// already-committed simulation history. This package never touches any
// engine.core state at all (no coupling exists), so this test proves
// that structural fact directly against a real Engine: advance ticks,
// snapshot, toggle debug + invoke a cheat via a wholly separate State,
// snapshot again, and assert the two snapshots are byte-identical.
func TestTogglingDebugDoesNotAffectEngineState(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(20260809), core.WithPoolSize(2))
	if err := e.AdvanceTicks("corr-adv", 33); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	var before bytes.Buffer
	if _, err := e.Snapshot(&before, "corr-snap-before"); err != nil {
		t.Fatalf("Snapshot (before): %v", err)
	}
	tickBefore := e.Clock().Tick()

	// Toggle debug on/off and invoke a cheat on a State entirely
	// unconnected to e — this is the point being proven: nothing here
	// can reach into engine.core's committed history because this
	// package holds no reference to it at all.
	s := NewState(WithHeader(newTestHeader()))
	if err := s.Enable(SourcePalette, "corr-dbg-1"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := s.InvokeCheat("corr-cheat", CheatFreeMoney, nil, func() error { return nil }); err != nil {
		t.Fatalf("InvokeCheat: %v", err)
	}
	s.Disable()

	var after bytes.Buffer
	if _, err := e.Snapshot(&after, "corr-snap-after"); err != nil {
		t.Fatalf("Snapshot (after): %v", err)
	}
	tickAfter := e.Clock().Tick()

	if tickBefore != tickAfter {
		t.Fatalf("engine tick changed by toggling debug: before=%d after=%d", tickBefore, tickAfter)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatalf("engine snapshot changed by toggling debug/invoking a cheat, want byte-identical")
	}
}

// TestConcurrentUse exercises State under concurrent access (Enable/
// IsOn/InvokeCheat/gates from many goroutines) — go test -race is the
// actual assertion here.
func TestConcurrentUse(t *testing.T) {
	s := NewState(WithHeader(newTestHeader()), WithEntityLookup(func(ref string) (any, error) {
		return map[string]string{"ref": ref}, nil
	}), WithFidelityDial(&fakeFidelityDial{radius: 2}))

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 5)

	for i := 0; i < n; i++ {
		go func() { defer wg.Done(); _ = s.Enable(SourceFlag, "corr-c1") }()
		go func() { defer wg.Done(); _ = s.IsOn() }()
		go func() {
			defer wg.Done()
			_ = s.InvokeCheat("corr-c2", CheatInstantBuild, nil, func() error { return nil })
		}()
		go func() { defer wg.Done(); _, _ = s.InspectEntity("corr-c3", "citizen:1") }()
		go func() { defer wg.Done(); _, _ = s.FidelityDial("corr-c4") }()
	}
	wg.Wait()

	if !s.IsOn() {
		t.Fatalf("IsOn() = false after concurrent Enable calls, want true")
	}
}
