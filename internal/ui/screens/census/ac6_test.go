package census

// AC-6 (KPI drill-in -- every tile Enter-selectable to its source, per
// UI-SPEC §4 and engine.census.md AC-20).

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderKPISourceInto(src KPISource, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 6)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 6}
	RenderKPISource(buf, rect, src, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// TestKPIDrillSource_HomelessEntities resolves homeless's source against
// a fixture and asserts the drilled view shows exactly the fixture's
// entity-ID set (never a generic/unrelated pane).
func TestKPIDrillSource_HomelessEntities(t *testing.T) {
	s := newScreenWithData(t, "sub-drill-homeless")
	src, ok := s.KPISource(KPIKeyHomeless)
	if !ok {
		t.Fatal("KPISource(homeless) ok=false, want true")
	}
	want := []uint64{11, 22, 33}
	if !reflect.DeepEqual(src.EntityIDs, want) {
		t.Errorf("KPISource(homeless).EntityIDs = %v, want %v (fixture-exact, not recomputed)", src.EntityIDs, want)
	}
}

// TestKPIDrillSource_GDPLineValue resolves gdp's source and asserts the
// drilled view shows the fixture's LineValue, not a recomputed figure.
// False-pass risk this rejects: a drill target that always opens the same
// static pane regardless of which KPI was selected would pass a smoke
// test while failing UI-SPEC §4's "goes to its source" requirement.
func TestKPIDrillSource_GDPLineValue(t *testing.T) {
	s := newScreenWithData(t, "sub-drill-gdp")
	src, ok := s.KPISource(KPIKeyGDP)
	if !ok {
		t.Fatal("KPISource(gdp) ok=false, want true")
	}
	if src.LineValue != 125000000 {
		t.Errorf("KPISource(gdp).LineValue = %d, want 125000000 (fixture-exact, not recomputed)", src.LineValue)
	}
	if len(src.EntityIDs) != 0 {
		t.Errorf("KPISource(gdp).EntityIDs = %v, want empty (gdp is an aggregate KPI, not population-derived)", src.EntityIDs)
	}

	// The homeless and gdp panes must render distinguishably -- a static
	// "details" pane that never traces to the selected KPI would fail
	// this.
	homelessSrc, _ := s.KPISource(KPIKeyHomeless)
	hBuf, hRect := renderKPISourceInto(homelessSrc, true)
	gBuf, _ := renderKPISourceInto(src, true)
	if bufsEqual(hBuf, gBuf, hRect) {
		t.Error("homeless and gdp source panes rendered identically -- drill target is not tracing to the selected KPI's own fixture data")
	}
}

// TestKPIDrillSource_UnknownKeyNotOK proves an unrequested/unsent KPI key
// reports ok=false rather than a fabricated zero source.
func TestKPIDrillSource_UnknownKeyNotOK(t *testing.T) {
	s := newScreenWithData(t, "sub-drill-unknown")
	if _, ok := s.KPISource("not-a-real-kpi"); ok {
		t.Error("KPISource for an unsent key returned ok=true, want false")
	}
}
