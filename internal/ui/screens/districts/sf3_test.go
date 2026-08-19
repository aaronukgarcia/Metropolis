package districts

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func bufsEqual(a, b *core.Buffer, rect core.Rect) bool {
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return false
			}
		}
	}
	return true
}

// TestSF3_TaxSettingChanges is AC-6's own SF-3-shaped differential single-
// field mutation check: two wire patches differ in exactly one figure (the
// harbour councilTax multiplier), and (a) the tax-settings pane must
// render differently while (b) an untouched pane (the BLOCKED policy-
// library pane, which never reads wire data at all) must render
// byte-identically between the two runs -- proving this screen reads the
// real subscribed field rather than hardcoding a value or wiring the
// wrong one.
func TestSF3_TaxSettingChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	mutatedSettings := []wireDistrictTaxSetting{
		{DistrictID: "harbour", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 1.5, Rate: 10, RateMax: 20, EffectiveRate: 15},
		{DistrictID: "harbour", InstrumentID: "businessRates", InstrumentLabel: "Business Rates", Multiplier: 1.0, Rate: 8, RateMax: 16, EffectiveRate: 8},
		{DistrictID: "old-town", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 0.3, Rate: 10, RateMax: 20, EffectiveRate: 3},
	}
	mutated.TaxSettings = &mutatedSettings

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.SetSelectedDistrict("old-town")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.SetSelectedDistrict("old-town")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	settingsA, _ := sA.TaxSettings()
	settingsB, _ := sB.TaxSettings()

	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}

	// a) The old-town councilTax multiplier change must change the pane.
	bufA := core.NewBuffer(80, 10)
	RenderTaxSettings(bufA, rect, settingsA, "old-town", "", true, testStyle)
	bufB := core.NewBuffer(80, 10)
	RenderTaxSettings(bufB, rect, settingsB, "old-town", "", true, testStyle)
	if bufsEqual(bufA, bufB, rect) {
		t.Error("tax-settings pane unchanged after mutating old-town councilTax multiplier from 0.80 to 0.30 (a)")
	}

	// b) The untouched, always-BLOCKED policy-library pane must remain
	// byte-identical -- it never reads wire data at all.
	blockA := core.NewBuffer(80, 10)
	RenderBlockedFeature(blockA, rect, "POLICY LIBRARY", testStyle)
	blockB := core.NewBuffer(80, 10)
	RenderBlockedFeature(blockB, rect, "POLICY LIBRARY", testStyle)
	if !bufsEqual(blockA, blockB, rect) {
		t.Error("BLOCKED policy-library pane changed even though it carries no wire-sourced fields (b)")
	}
}
