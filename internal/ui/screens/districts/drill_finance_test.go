package districts

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	financescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// TestFinanceJumpTarget_NamesARealRegisteredView is AC-7's whole-view
// "not a fabricated non-view" check, mirroring ui.screen.services'
// TestCoverageJumpTarget_NamesARealRegisteredView exactly (the drift-test
// shape: districts holds the literal financeJumpView, this test imports
// the real source and asserts agreement -- if F2's view is ever renamed,
// this fails and forces reconciliation). Importing ui.screen.finance from
// a _test.go file is the sanctioned exception AC-1/SF-1 already carves out
// for tests (AC-1 forbids internal/engine imports in non-test source; a
// same-layer cross-screen import in a test file is not an AC-1 concern at
// all).
func TestFinanceJumpTarget_NamesARealRegisteredView(t *testing.T) {
	target := FinanceJumpTarget("harbour")

	if target.ViewName != financescreen.ViewSubscriptionName {
		t.Fatalf("FinanceJumpTarget ViewName = %q, want the registered F2 view %q (a fabricated non-view is a dead end)",
			target.ViewName, financescreen.ViewSubscriptionName)
	}
	if target.EntityID != "district.harbour.tax-revenue" {
		t.Errorf("FinanceJumpTarget EntityID = %q, want %q", target.EntityID, "district.harbour.tax-revenue")
	}
	if _, err := dash.NewDrillTarget(target.ViewName, string(target.EntityID)); err != nil {
		t.Errorf("FinanceJumpTarget(%q) is not a valid dash.DrillTarget: %v", "harbour", err)
	}
}

// TestDrillTargets_IncludesOneFinanceJumpPerDistrict (AC-7/SF-5): the
// registered drill sources include exactly one whole-view finance jump per
// distinct district represented in the tax settings, plus one per-row tax
// target -- no duplicate finance jumps for a district with multiple
// instrument rows.
func TestDrillTargets_IncludesOneFinanceJumpPerDistrict(t *testing.T) {
	settings := []DistrictTaxSetting{
		{DistrictID: "harbour", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 1.5, Rate: 10, RateMax: 20, EffectiveRate: 15},
		{DistrictID: "harbour", InstrumentID: "businessRates", InstrumentLabel: "Business Rates", Multiplier: 1.0, Rate: 8, RateMax: 16, EffectiveRate: 8},
		{DistrictID: "old-town", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 0.8, Rate: 10, RateMax: 20, EffectiveRate: 8},
	}

	targets := DrillTargets(settings)

	financeJumps := 0
	taxRows := 0
	for _, tg := range targets {
		switch tg.ViewName {
		case financescreen.ViewSubscriptionName:
			financeJumps++
		case ViewSubscriptionName:
			taxRows++
		default:
			t.Errorf("unexpected drill target view %q", tg.ViewName)
		}
	}
	if financeJumps != 2 {
		t.Errorf("finance jump count = %d, want 2 (one per distinct district)", financeJumps)
	}
	if taxRows != 3 {
		t.Errorf("tax-row drill target count = %d, want 3 (one per tax-setting row)", taxRows)
	}
}
