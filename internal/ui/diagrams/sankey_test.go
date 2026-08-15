package diagrams

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// sankeyBandWidths returns each flow's rendered band width, looked up from
// the hit-test output by ID.
func sankeyBandWidths(t *testing.T, hits []Hit, ids []SourceID) []int {
	t.Helper()
	byID := make(map[SourceID]int, len(hits))
	for _, h := range hits {
		byID[h.ID] = h.Rect.W
	}
	out := make([]int, len(ids))
	for i, id := range ids {
		w, ok := byID[id]
		if !ok {
			t.Fatalf("flow %q missing from hit-test output", id)
		}
		out[i] = w
	}
	return out
}

func TestRenderSankey_BandWidthsProportional(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{
			{ID: "s1", Name: "a", Amount: 100},
			{ID: "s2", Name: "b", Amount: 200},
			{ID: "s3", Name: "c", Amount: 300},
		},
		Sinks: []SankeyFlow{
			{ID: "k1", Name: "d", Amount: 50},
			{ID: "k2", Name: "e", Amount: 100},
			{ID: "k3", Name: "f", Amount: 150},
			{ID: "k4", Name: "g", Amount: 200},
		},
	}
	buf := core.NewBuffer(40, 12)
	res, err := RenderSankey(buf, topo, Options{Palette: widgets.DefaultPalette})
	if err != nil {
		t.Fatalf("RenderSankey: %v", err)
	}

	// labelW = max(width("budget")=6, one-char names) = 6, so with a 40-wide
	// buffer bandMax = 40 - 6 - 1 = 33.
	const bandMax = 33

	check := func(name string, flows []SankeyFlow, ids []SourceID) {
		t.Helper()
		widths := sankeyBandWidths(t, res.Hits, ids)
		total := 0.0
		for _, f := range flows {
			total += f.Amount
		}
		prev := -1
		for i, f := range flows {
			w := widths[i]
			// Exact match to the documented rounding: round(amount/total*bandMax).
			want := int(math.Round(f.Amount / total * float64(bandMax)))
			if w != want {
				t.Errorf("%s %q: width %d, want %d (round(%.2f/%v*%d))", name, f.ID, w, want, f.Amount, total, bandMax)
			}
			// Proportionality within the half-cell rounding tolerance.
			ratio := f.Amount / total
			if math.Abs(float64(w)/float64(bandMax)-ratio) > 0.5/float64(bandMax)+1e-9 {
				t.Errorf("%s %q: width %d not within half a cell of ratio %.3f", name, f.ID, w, ratio)
			}
			// Monotonic: non-decreasing width with non-decreasing amount.
			if w < prev {
				t.Errorf("%s %q: width %d < previous %d for a non-decreasing amount", name, f.ID, w, prev)
			}
			prev = w
		}
	}
	check("source", topo.Sources, []SourceID{"s1", "s2", "s3"})
	check("sink", topo.Sinks, []SourceID{"k1", "k2", "k3", "k4"})

	// Every band is a hit carrying its ID; the budget node carries no ID.
	if want := len(topo.Sources) + len(topo.Sinks); len(res.Hits) != want {
		t.Fatalf("got %d hits, want %d (one per flow, budget carries no ID)", len(res.Hits), want)
	}
}

func TestRenderSankey_UnbalancedTotalsNotAnError(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 50}, {ID: "k2", Name: "schools", Amount: 50}},
	}
	buf := core.NewBuffer(40, 8)
	res, err := RenderSankey(buf, topo, Options{})
	if err != nil {
		t.Fatalf("unbalanced source/sink totals must not error (AC-7): %v", err)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("got %d hits, want 3 (1 source + 2 sinks)", len(res.Hits))
	}
}

func TestRenderSankey_EmptyNotAnError(t *testing.T) {
	buf := core.NewBuffer(20, 5)
	res, err := RenderSankey(buf, SankeyTopology{}, Options{})
	if err != nil {
		t.Fatalf("empty sankey should not error: %v", err)
	}
	if len(res.Hits) != 0 || res.Region != (core.Rect{}) {
		t.Errorf("empty sankey should render empty, got %+v", res)
	}
}
