package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// expectGlyphs builds the expected row string from sparkGlyphs indices,
// so the test's "known mapping" is expressed as index arithmetic
// against the same package-level glyph table Sparkline itself uses,
// rather than hand-transcribed Unicode block characters (which are easy
// to get subtly wrong by eye). This is still an independent
// computation from Sparkline's own logic: the indices below are worked
// out by hand from the documented formula
// level = round((v-min)/(max-min)*7), not copied from Sparkline's code.
func expectGlyphs(levels []int) string {
	var s []rune
	for _, l := range levels {
		if l < 0 {
			s = append(s, ' ')
			continue
		}
		s = append(s, sparkGlyphs[l])
	}
	return string(s)
}

func TestSparkline_KnownSeriesMapping(t *testing.T) {
	// series[i] = i for i in 0..11: min=0, max=11.
	// level(v) = round(v/11*7); hand-computed below.
	series := make([]float64, 12)
	for i := range series {
		series[i] = float64(i)
	}
	buf := core.NewBuffer(12, 1)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 12, H: 1}, series, tcell.StyleDefault)

	want := expectGlyphs([]int{0, 1, 1, 2, 3, 3, 4, 4, 5, 6, 6, 7})
	assertGrid(t, buf, []string{want})
}

func TestSparkline_FlatSeriesRendersMidLevel(t *testing.T) {
	series := []float64{5, 5, 5, 5}
	buf := core.NewBuffer(12, 1)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 12, H: 1}, series, tcell.StyleDefault)

	// n=4, width=12: bucket = i*12/4 = i*3 -> buckets 0,3,6,9 populated,
	// all others blank; flat -> level 3 (sparklineFlatLevel) at each.
	levels := []int{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
	levels[0], levels[3], levels[6], levels[9] = sparklineFlatLevel, sparklineFlatLevel, sparklineFlatLevel, sparklineFlatLevel
	want := expectGlyphs(levels)
	assertGrid(t, buf, []string{want})
}

func TestSparkline_SinglePoint(t *testing.T) {
	buf := core.NewBuffer(12, 1)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 12, H: 1}, []float64{42}, tcell.StyleDefault)

	levels := make([]int, 12)
	for i := range levels {
		levels[i] = -1
	}
	levels[0] = sparklineFlatLevel
	want := expectGlyphs(levels)
	assertGrid(t, buf, []string{want})
}

func TestSparkline_NegativeValues(t *testing.T) {
	// width == len(series): each point owns exactly one bucket.
	series := []float64{-10, 0, 10}
	buf := core.NewBuffer(3, 1)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 3, H: 1}, series, tcell.StyleDefault)

	want := expectGlyphs([]int{0, 4, 7})
	assertGrid(t, buf, []string{want})
}

func TestSparkline_EmptySeriesRendersAllBlank(t *testing.T) {
	buf := core.NewBuffer(12, 1)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 12, H: 1}, nil, tcell.StyleDefault)
	assertGrid(t, buf, []string{"            "})
}

func TestSparkline_DegenerateRectDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(5, 5)
	Sparkline(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, []float64{1, 2, 3}, tcell.StyleDefault)
	Sparkline(nil, core.Rect{X: 0, Y: 0, W: 5, H: 1}, []float64{1}, tcell.StyleDefault)
}

func TestSparkline_Deterministic(t *testing.T) {
	series := []float64{1, 4, 2, 8, 5, 7, 3, 9, 6, 0, 10, 2}
	buf1 := core.NewBuffer(12, 1)
	buf2 := core.NewBuffer(12, 1)
	Sparkline(buf1, core.Rect{X: 0, Y: 0, W: 12, H: 1}, series, tcell.StyleDefault)
	Sparkline(buf2, core.Rect{X: 0, Y: 0, W: 12, H: 1}, series, tcell.StyleDefault)
	if g1, g2 := gridRunes(buf1), gridRunes(buf2); g1[0] != g2[0] {
		t.Fatalf("Sparkline is not deterministic: %q vs %q", g1[0], g2[0])
	}
}

func BenchmarkSparkline(b *testing.B) {
	series := []float64{1, 4, 2, 8, 5, 7, 3, 9, 6, 0, 10, 2, 3, 4, 5, 6, 7, 8, 9, 10, 1, 2, 3, 4}
	buf := core.NewBuffer(12, 1)
	rect := core.Rect{X: 0, Y: 0, W: 12, H: 1}
	style := tcell.StyleDefault
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sparkline(buf, rect, series, style)
	}
}
