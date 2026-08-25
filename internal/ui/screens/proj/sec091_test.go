package proj

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestThresholdLineCap_ViewportDerived pins the data-driven cap (GR#15):
// the maximum number of threshold lines a chart can draw is its dot height
// (4 dot-rows per cell row), derived from the render viewport — never a
// hardcoded literal. A 7-cell-tall chart (RenderCurve's 8-row rect minus the
// label row) holds 28 dot-rows, so at most 28 threshold lines are visually
// distinct. Degenerate (non-positive) heights cap to zero.
func TestThresholdLineCap_ViewportDerived(t *testing.T) {
	if got := thresholdLineCap(7); got != 28 {
		t.Errorf("thresholdLineCap(7) = %d, want 28 (7 cell rows x 4 dot-rows)", got)
	}
	if got := thresholdLineCap(0); got != 0 {
		t.Errorf("thresholdLineCap(0) = %d, want 0", got)
	}
	if got := thresholdLineCap(-1); got != 0 {
		t.Errorf("thresholdLineCap(-1) = %d, want 0", got)
	}
}

// TestRenderCurve_ThresholdLoopBoundedByViewport is SEC-091's regression: a
// hostile "f7.projections" patch can carry ~80k thresholds and still fit the
// 1 MiB wire cap, and the pre-fix threshold loop drew a full dotsW-wide
// horizontal line per threshold every render tick. The fix draws each
// distinct dot-row at most once and stops once the chart's dot height is
// covered, so the drawn output (and the line-rasterization work behind it)
// is clamped to the viewport. The test proves it deterministically: a curve
// with 100,000 thresholds renders byte-identically to the same curve whose
// thresholds are reduced to the distinct dot-rows, so a huge threshold set
// draws no more than a chart-height-sized one.
func TestRenderCurve_ThresholdLoopBoundedByViewport(t *testing.T) {
	history := []float64{0, 100}
	projection := []float64{0, 100}

	huge := Curve{
		Key: "water.demand", Status: StatusAvailable,
		History: history, Projection: projection,
	}
	const n = 100000
	for i := 0; i < n; i++ {
		huge.Thresholds = append(huge.Thresholds, Threshold{Value: float64(i) * 100.0 / (n - 1)})
	}

	rect := core.Rect{X: 0, Y: 0, W: 80, H: 8}
	chart := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	dotsH := thresholdLineCap(chart.H)
	min, max := combinedRange(history, projection)

	// The exact set the bounded loop should draw: the first threshold per
	// distinct dot-row, in wire order.
	bounded := Curve{
		Key: huge.Key, Status: huge.Status,
		History: history, Projection: projection,
	}
	seen := make(map[int]bool)
	for _, th := range huge.Thresholds {
		row := dotRow(th.Value, min, max, dotsH)
		if seen[row] {
			continue
		}
		seen[row] = true
		bounded.Thresholds = append(bounded.Thresholds, th)
	}
	if len(bounded.Thresholds) >= len(huge.Thresholds) {
		t.Fatalf("test setup: expected dedupe to reduce %d thresholds, got %d", len(huge.Thresholds), len(bounded.Thresholds))
	}
	if len(bounded.Thresholds) > dotsH {
		t.Fatalf("test setup: deduped thresholds %d exceed the chart's dot height %d", len(bounded.Thresholds), dotsH)
	}

	bufHuge := core.NewBuffer(rect.W, rect.H)
	RenderCurve(bufHuge, rect, huge, widgets.DefaultPalette)
	bufBounded := core.NewBuffer(rect.W, rect.H)
	RenderCurve(bufBounded, rect, bounded, widgets.DefaultPalette)

	for y := 0; y < rect.H; y++ {
		for x := 0; x < rect.W; x++ {
			a, b := bufHuge.Get(x, y), bufBounded.Get(x, y)
			if a != b {
				t.Fatalf("cell (%d,%d): %d thresholds rendered %+v but %d thresholds rendered %+v — the threshold loop must draw no more than the viewport's dot rows (SEC-091)", x, y, len(huge.Thresholds), a, len(bounded.Thresholds), b)
			}
		}
	}
}

// TestCurveRenderCap_ViewportDerived pins the second half of SEC-091's cap:
// the total number of curves rendered per tick is derived from the render
// viewport (GR#15), never a hardcoded literal. One curve occupies
// curveBandRows rows (a label row plus a chart row), so an 8-row viewport
// holds 4 curves, a 9-row viewport still 4 (the 9th row is a partial band
// and can't fit another curve), and degenerate (non-positive) heights cap
// to zero.
func TestCurveRenderCap_ViewportDerived(t *testing.T) {
	if got := curveRenderCap(8); got != 4 {
		t.Errorf("curveRenderCap(8) = %d, want 4 (8 rows / 2 rows per curve)", got)
	}
	if got := curveRenderCap(9); got != 4 {
		t.Errorf("curveRenderCap(9) = %d, want 4 (9 rows still holds only 4 full 2-row bands)", got)
	}
	if got := curveRenderCap(0); got != 0 {
		t.Errorf("curveRenderCap(0) = %d, want 0", got)
	}
	if got := curveRenderCap(-1); got != 0 {
		t.Errorf("curveRenderCap(-1) = %d, want 0", got)
	}
}

// TestRenderCurves_TotalCountBoundedByViewport is SEC-091's second-half
// regression: a hostile "f7.projections" patch can carry ~10k curves and
// still fit the 1 MiB wire cap, and the pre-fix per-curve loop allocated
// 2+ Braille canvases per curve with no total-curve-count bound (12.2 ms /
// 11.6 MB per tick at 10k). RenderCurves must clamp the count to the
// viewport's capacity — it returns curveRenderCap(rect.H), not len(curves),
// and the drawn output must be byte-identical to rendering just the first
// cap curves (so the dropped curves allocate nothing and paint nothing).
// Every curve carries a distinct key/label/value so any rendered curve
// beyond the cap would change the output and fail the byte-identical check.
func TestRenderCurves_TotalCountBoundedByViewport(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 8}
	cap := curveRenderCap(rect.H)
	if cap != 4 {
		t.Fatalf("test setup: curveRenderCap(%d) = %d, want 4", rect.H, cap)
	}

	const n = 10000
	curves := make([]Curve, n)
	for i := range curves {
		curves[i] = Curve{
			Key:        fmt.Sprintf("curve.%d", i),
			Label:      fmt.Sprintf("curve %d", i),
			Status:     StatusAvailable,
			History:    []float64{float64(i), float64(i) + 100},
			Projection: []float64{float64(i), float64(i) + 100},
		}
	}

	bufHuge := core.NewBuffer(rect.W, rect.H)
	if got := RenderCurves(bufHuge, rect, curves, widgets.DefaultPalette); got != cap {
		t.Fatalf("RenderCurves drew %d of %d curves, want %d (the total-curve-count bound, SEC-091)", got, len(curves), cap)
	}

	// The truncated slice is exactly what RenderCurves must have drawn, so a
	// second render of just the first cap curves is byte-identical.
	bufBounded := core.NewBuffer(rect.W, rect.H)
	if got := RenderCurves(bufBounded, rect, curves[:cap], widgets.DefaultPalette); got != cap {
		t.Fatalf("RenderCurves drew %d of %d curves, want %d", got, len(curves[:cap]), cap)
	}

	for y := 0; y < rect.H; y++ {
		for x := 0; x < rect.W; x++ {
			a, b := bufHuge.Get(x, y), bufBounded.Get(x, y)
			if a != b {
				t.Fatalf("cell (%d,%d): %d curves rendered %+v but %d curves rendered %+v — RenderCurves must clamp to the viewport capacity (SEC-091)", x, y, len(curves), a, cap, b)
			}
		}
	}
}
