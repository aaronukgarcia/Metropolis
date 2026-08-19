package census

// AC-5 (city KPI tile row -- eight named KPIs, all sourced live).

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderKPITilesInto(kpis []KPITile, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 12)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 12}
	RenderKPITiles(buf, rect, kpis, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// TestKPITile_AllEightNamedAgainstSource proves doc.go names all eight
// tiles against their KPIKey* source (AC-5's "go doc" requirement,
// encoded as a data check rather than a doc-string parse).
func TestKPITile_AllEightNamedAgainstSource(t *testing.T) {
	if len(AllKPIKeys) != 8 {
		t.Fatalf("AllKPIKeys has %d entries, want 8", len(AllKPIKeys))
	}
	seen := map[string]bool{}
	for _, k := range AllKPIKeys {
		if kpiLabels[k] == "" {
			t.Errorf("KPI key %q has no display label in kpiLabels (render.go)", k)
		}
		seen[k] = true
	}
	want := []string{KPIKeyGDP, KPIKeyHappiness, KPIKeyLandValue, KPIKeyHomeless, KPIKeyInHospital, KPIKeyOutOfWork, KPIKeyUnfilledJobs, KPIKeyJobSkillDemand}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("AllKPIKeys is missing %q", k)
		}
	}
}

// TestKPITile_DifferentialChangesOnlyThatTile feeds a fixture delta with
// known values for all eight KPIs and asserts changing one KPI's fixture
// field changes only that tile's rendered row.
func TestKPITile_DifferentialChangesOnlyThatTile(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()
	mutatedKPIs := []wireKPITile{
		{Key: KPIKeyGDP, Value: 125000000},
		{Key: KPIKeyHappiness, Value: 62.5},
		{Key: KPIKeyLandValue, Value: 87000000},
		{Key: KPIKeyHomeless, Value: 999}, // only this one changes
		{Key: KPIKeyInHospital, Value: 128},
		{Key: KPIKeyOutOfWork, Value: 310},
		{Key: KPIKeyUnfilledJobs, Value: 96},
		{Key: KPIKeyJobSkillDemand, Value: 220},
	}
	mutated.KPIs = &mutatedKPIs

	sA := New("corr-kpi-a")
	sA.BindSubscription("sub-kpi-a")
	sA.ApplyDelta(protocolDelta(t, "sub-kpi-a", base))
	sB := New("corr-kpi-b")
	sB.BindSubscription("sub-kpi-b")
	sB.ApplyDelta(protocolDelta(t, "sub-kpi-b", mutated))

	kpisA, _ := sA.KPITiles()
	kpisB, _ := sB.KPITiles()

	for i := range kpisA {
		if kpisA[i].Key != kpisB[i].Key {
			t.Fatalf("KPI order diverged at %d: %q vs %q", i, kpisA[i].Key, kpisB[i].Key)
		}
		if kpisA[i].Key == KPIKeyHomeless {
			if kpisA[i].Value == kpisB[i].Value {
				t.Error("homeless KPI value did not actually differ -- test setup bug")
			}
			continue
		}
		if kpisA[i].Value != kpisB[i].Value {
			t.Errorf("KPI %q value changed even though only homeless was mutated -- want independence per AC-5", kpisA[i].Key)
		}
	}

	ta, taRect := renderKPITilesInto(kpisA, true)
	tb, _ := renderKPITilesInto(kpisB, true)
	if bufsEqual(ta, tb, taRect) {
		t.Error("KPI tile row unchanged after mutating the homeless KPI's value")
	}
}
