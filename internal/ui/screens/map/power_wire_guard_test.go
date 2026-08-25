package mapscreen

import (
	"encoding/json"
	"testing"
)

// powerGuardPatchJSON marshals a full "f1.viewport" patch whose cells are a
// single valid entry and whose powerLines are the given raw entries — the
// minimal legal patch shape around the field under test.
func powerGuardPatchJSON(t *testing.T, lines []map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"full":          true,
		"origin":        map[string]int{"x": 0, "y": 0},
		"extent":        map[string]int{"width": 8, "height": 8},
		"cells":         []map[string]any{{"x": 0, "y": 0, "terrain": "grass"}},
		"powerLines":    lines,
	})
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return raw
}

// TestDecodeWirePatch_RejectsOversizedPowerLineCount pins the SEC-039-class
// count gate: a patch carrying more than maxPowerLines spans is rejected at
// decode, never applied.
func TestDecodeWirePatch_RejectsOversizedPowerLineCount(t *testing.T) {
	lines := make([]map[string]any, 0, maxPowerLines+1)
	for i := 0; i <= maxPowerLines; i++ {
		lines = append(lines, map[string]any{
			"id": i + 1, "class": "localPole",
			"fromX": i % 4, "fromY": 0, "toX": (i % 4) + 1, "toY": 0,
			"capacityMW": 0.5,
		})
	}
	_, err := decodeWirePatch(powerGuardPatchJSON(t, lines))
	if err == nil {
		t.Fatalf("patch with %d powerLines decoded clean, want rejection at maxPowerLines=%d", len(lines), maxPowerLines)
	}
}

// TestDecodeWirePatch_RejectsOutOfBoundsPowerLineCoord pins the per-line
// coordinate gate: every endpoint coordinate must land inside the grid
// domain every legitimate snapshot extent is itself bounded by
// ([0, maxGridSide)); anything else is rejected at decode rather than
// handed to the renderers.
func TestDecodeWirePatch_RejectsOutOfBoundsPowerLineCoord(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(l map[string]any)
	}{
		{"negative fromX", func(l map[string]any) { l["fromX"] = -1 }},
		{"negative fromY", func(l map[string]any) { l["fromY"] = -1 }},
		{"negative toX", func(l map[string]any) { l["toX"] = -7 }},
		{"negative toY", func(l map[string]any) { l["toY"] = -7 }},
		{"fromX at side ceiling", func(l map[string]any) { l["fromX"] = maxGridSide }},
		{"toY at side ceiling", func(l map[string]any) { l["toY"] = maxGridSide }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := map[string]any{
				"id": 1, "class": "localPole",
				"fromX": 0, "fromY": 0, "toX": 3, "toY": 0, "capacityMW": 0.5,
			}
			tc.mutate(line)
			_, err := decodeWirePatch(powerGuardPatchJSON(t, []map[string]any{line}))
			if err == nil {
				t.Fatalf("powerLine with out-of-bounds coordinate decoded clean, want rejection")
			}
		})
	}
}

// TestDecodeWirePatch_RejectsDegeneratePowerLine pins the degenerate-span
// gate: from==to carries zero drawable cells (the engine's own PlaceLine
// rejects the same shape), so accepting it would only hand the renderers
// an allocation with no payload.
func TestDecodeWirePatch_RejectsDegeneratePowerLine(t *testing.T) {
	line := map[string]any{
		"id": 1, "class": "localPole",
		"fromX": 3, "fromY": 3, "toX": 3, "toY": 3, "capacityMW": 0.5,
	}
	_, err := decodeWirePatch(powerGuardPatchJSON(t, []map[string]any{line}))
	if err == nil {
		t.Fatalf("degenerate powerLine decoded clean, want rejection")
	}
}

// refSpanCellsWindow walks the legacy materialising walker and filters its
// output to the window — the exactness reference for walkPowerSpan's
// streaming/clamped result on spans small enough for the reference itself
// to be safe to build.
func refSpanCellsWindow(from [2]int, to [2]int, win gridWindow) [][2]int {
	var out [][2]int
	for _, c := range powerSpanCells(from[0], from[1], to[0], to[1]) {
		if c[0] >= win.x0 && c[0] <= win.x1 && c[1] >= win.y0 && c[1] <= win.y1 {
			out = append(out, c)
		}
	}
	return out
}

func sameCells(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWalkPowerSpan_ClampsToWindow pins the render-side defense in depth:
// a hostile span with absurd endpoints walked against a small viewport
// window (a) produces exactly the cells the unclipped line covers inside
// that window (identical pixels, GR#21 determinism) and (b) visits a
// number of cells bounded by the window itself — never proportional to
// the endpoint magnitudes, so no huge allocation or iteration is possible
// regardless of what reached the renderer.
func TestWalkPowerSpan_ClampsToWindow(t *testing.T) {
	win := gridWindow{x0: 100, y0: 100, x1: 107, y1: 107}
	cases := []struct {
		name string
		from [2]int
		to   [2]int
	}{
		{"horizontal through window", [2]int{-1_000_000_000, 103}, [2]int{1_000_000_000, 103}},
		{"vertical through window", [2]int{105, -1_000_000_000}, [2]int{105, 1_000_000_000}},
		{"shallow diagonal through window", [2]int{-1_000_000, 90}, [2]int{1_000_000, 120}},
		{"steep diagonal through window", [2]int{95, -1_000_000}, [2]int{115, 1_000_000}},
		{"entirely outside", [2]int{-50, -50}, [2]int{-10, -10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			visited := 0
			var got [][2]int
			walkPowerSpan(tc.from[0], tc.from[1], tc.to[0], tc.to[1], win, func(x, y int) {
				visited++
				got = append(got, [2]int{x, y})
				if visited > win.x1-win.x0+win.y1-win.y0+2 {
					t.Fatalf("walk visited more than the window bound (%d cells so far)", visited)
				}
				if x < win.x0 || x > win.x1 || y < win.y0 || y > win.y1 {
					t.Fatalf("visit outside window: (%d,%d)", x, y)
				}
			})
			want := refSpanCellsWindow(tc.from, tc.to, win)
			if !sameCells(got, want) {
				t.Fatalf("clamped walk = %v, want reference-filtered %v", got, want)
			}
		})
	}
}
