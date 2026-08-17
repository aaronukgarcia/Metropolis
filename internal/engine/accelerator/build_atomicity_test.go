package accelerator

import (
	"errors"
	"testing"
)

// This file closes the Destructive REJECT on MOD-077 (AC-13 partial state on
// the FDI-failure path): a build-time side effect must never be posted before
// a later step that can fail, because that leaves a phantom effect for a
// facility that was never built. The class fix is that the two external
// writes in Build — the FDI anchor draw and the day-one decommission
// liability — are ordered so the decommission accrual is the final external
// write (the engine.spaceport commit-adjacent pattern), and the FDI draw is
// compensated back if that final write fails. Both failure orders are tested.

// sentinel errors the failing seams return, so a test can assert the build
// surfaced the exact failure it wired (not a silently swallowed one).
var (
	errFdiDown          = errors.New("fdi seam unavailable")
	errDecommissionDown = errors.New("decommission seam unavailable")
)

// TestFdiFailureNoPartialState is the REJECT regression: with a clean
// decommission seam and a FAILING FDI seam, Build must return an error and
// leave no partial state — critically, the day-one decommission liability
// must NOT have been accrued for a facility that was never built. This test
// FAILS against the pre-fix ordering (decommission accrued before FDI).
func TestFdiFailureNoPartialState(t *testing.T) {
	a, u := loadTestAPI(t)
	d := wireAll(t, a, u, a.ExpertGateThreshold())
	d.fdi.err = errFdiDown // the FDI draw fails; decommission stays clean

	err := a.Build(BuildCommand{Key: CatalogueKey})
	if err == nil {
		t.Fatal("expected Build to fail when the FDI seam fails")
	}
	if !errors.Is(err, errFdiDown) {
		t.Errorf("Build error = %v, want the FDI seam failure to surface", err)
	}

	// AC-13: no partial state — no facility record, no draw, no effect.
	if a.IsBuilt() {
		t.Error("IsBuilt = true after an FDI-failure rejection")
	}
	if a.IsOnline() {
		t.Error("IsOnline = true after an FDI-failure rejection")
	}
	if a.Prestige() != 0 {
		t.Errorf("prestige = %d after an FDI-failure rejection, want 0", a.Prestige())
	}
	if d.fdi.prospects != 0 {
		t.Errorf("FDI prospects = %d after an FDI-failure rejection, want 0", d.fdi.prospects)
	}
	if d.decomm.accrued != "" {
		t.Errorf("decommission accrued for %q after an FDI-failure rejection, want none (phantom liability)", d.decomm.accrued)
	}
	if d.wellbeing.posts != 0 {
		t.Errorf("wellbeing posts = %d after an FDI-failure rejection, want 0", d.wellbeing.posts)
	}
}

// TestDecommissionFailureCompensatesFdiNoPartialState is the class-fix
// mirror: with the FDI seam succeeding and the decommission seam FAILING,
// Build must compensate the already-posted FDI draw so the rejection still
// leaves no partial state. It fails against a reorder that moves the FDI draw
// first WITHOUT compensating it (prospects would remain nonzero).
func TestDecommissionFailureCompensatesFdiNoPartialState(t *testing.T) {
	a, u := loadTestAPI(t)
	d := wireAll(t, a, u, a.ExpertGateThreshold())
	d.decomm.err = errDecommissionDown // the decommission accrual fails; FDI stays clean

	err := a.Build(BuildCommand{Key: CatalogueKey})
	if err == nil {
		t.Fatal("expected Build to fail when the decommission seam fails")
	}
	if !errors.Is(err, errDecommissionDown) {
		t.Errorf("Build error = %v, want the decommission seam failure to surface", err)
	}

	if a.IsBuilt() {
		t.Error("IsBuilt = true after a decommission-failure rejection")
	}
	if a.IsOnline() {
		t.Error("IsOnline = true after a decommission-failure rejection")
	}
	if a.Prestige() != 0 {
		t.Errorf("prestige = %d after a decommission-failure rejection, want 0", a.Prestige())
	}
	if d.fdi.prospects != 0 {
		t.Errorf("FDI prospects = %d after a decommission-failure rejection, want 0 (draw not compensated)", d.fdi.prospects)
	}
	if d.decomm.accrued != "" {
		t.Errorf("decommission accrued for %q after a decommission-failure rejection, want none", d.decomm.accrued)
	}
	if d.wellbeing.posts != 0 {
		t.Errorf("wellbeing posts = %d after a decommission-failure rejection, want 0", d.wellbeing.posts)
	}
}
