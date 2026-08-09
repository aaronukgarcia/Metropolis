package debug_test

import (
	"testing"

	engcore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	debug "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
)

// TestPhaseOrder_MatchesEngineCore guards against phase.go's local
// monthlyPhaseOrder (a literal copy, required by GR#20 — internal/ui
// non-test code may not import internal/engine) drifting from the real
// internal/engine/core.MonthlyPhaseOrder(). This test file is exempt
// from the ui-must-not-import-engine depguard rule (test files only,
// see .golangci.yml) — the sanctioned way to check the mirror stays in
// sync.
//
// Reads via MonthlyPhaseOrder() (a function call) rather than a bare
// var, per SEC-005's fix (2026-08-09): the real slice is no longer an
// exported, directly-mutable package variable — see
// internal/engine/core/phase.go's dailyPhaseOrder/monthlyPhaseOrder doc
// comments. This test only ever reads the returned copy, so the change
// is call-syntax only (added parens); it still validates the exact same
// invariant against the exact same authoritative order.
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
