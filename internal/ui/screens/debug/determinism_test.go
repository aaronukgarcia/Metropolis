package debug_test

import (
	"testing"

	engcore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	debug "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
)

// TestPhaseOrder_MatchesEngineCore asserts the debug snapshot's phase
// series stays aligned with the real
// internal/engine/core.MonthlyPhaseOrder(). Since BUG-382 the phase
// NAMES are compile-time shared via internal/protocol (phase.go derives
// from protocol.MonthlyPhaseOrderNames — no literal copy any more), so
// a rename now fails the build outright; what this test still uniquely
// guards is the WIRING: that Collect keys each series by exactly the
// engine's order and that every phase is Available against a registry
// keyed by the real names.
//
// This test file is exempt from the ui-must-not-import-engine depguard
// rule (test files only, see .golangci.yml) — the sanctioned way to
// check against the engine itself.
//
// Reads via MonthlyPhaseOrder() (a function call) rather than a bare
// var, per SEC-005's fix (2026-08-09): the real slice is no longer an
// exported, directly-mutable package variable — see
// internal/engine/core/phase.go's dailyPhaseOrder/monthlyPhaseOrder doc
// comments. This test only ever reads the returned copy.
func TestPhaseOrder_MatchesEngineCore(t *testing.T) {
	wantOrder := engcore.MonthlyPhaseOrder()

	reg := registry.NewRegistry()
	for _, kind := range wantOrder {
		mod := fakeModule{name: string(kind), version: "0.0.0", health: registry.HealthOK}
		if err := reg.Register(string(kind), nil, mod); err != nil {
			t.Fatalf("register %s: %v", kind, err)
		}
		if err := reg.RecordTickCost(string(kind), 1); err != nil {
			t.Fatalf("RecordTickCost %s: %v", kind, err)
		}
	}

	s := debug.NewScreen(reg, "corr-1", debug.WithDebugFlag(func() bool { return true }))
	snap := s.Collect()

	if len(snap.PhaseSeries) != len(wantOrder) {
		t.Fatalf("len(PhaseSeries) = %d, want %d (len(engine/core.MonthlyPhaseOrder()))", len(snap.PhaseSeries), len(wantOrder))
	}
	for i, kind := range wantOrder {
		if snap.PhaseSeries[i].Phase != string(kind) {
			t.Errorf("PhaseSeries[%d].Phase = %q, want %q (engine/core.MonthlyPhaseOrder()[%d])", i, snap.PhaseSeries[i].Phase, string(kind), i)
		}
		if !snap.PhaseSeries[i].Available {
			t.Errorf("PhaseSeries[%d] (%q) not Available — the local mirror key does not match a registry entry keyed by the real phase name", i, kind)
		}
	}
}
