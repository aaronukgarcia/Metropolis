package mapscreen

// Package-internal (white-box) tests for paintOverlay (overlay_data.go),
// the two-data-layers-per-cell paint mechanism AC-4 describes. Every one
// of AC-3's ten named overlays is BLOCKED in production today
// (overlayLiveValue, overlayBlockedReason — no live per-cell data source
// exists anywhere on this tree, confirmed by direct investigation of
// int.protocol/compose/engine.traffic/engine.services at FEAT-031's
// dispatch: nothing feeds any engine module's per-cell output into a
// protocol Delta). Consequently no honest test can assert two REAL,
// currently-wired overlays render different backgrounds — there are
// none. What CAN be proven honestly, and is proven here, is that the
// glue code a real overlay will reuse verbatim (paintOverlay) correctly
// implements AC-4's contract given ANY per-cell value source: foreground
// glyphs are never touched, and a per-cell value change changes only
// that cell's background. These tests exercise paintOverlay directly
// with synthetic overlayValueFunc providers built in this file, NOT
// through MapScreen's public API and NOT claiming any Overlay constant
// is live — see overlay.go/overlay_data.go for the honest, still-BLOCKED
// production state.

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// twoStopRamp is a minimal, deterministic HeatRamp for these tests: value
// 0 maps to black, value 1 maps to white — nothing else needed to prove
// "the background changed" vs "the background did not change".
var twoStopRamp = widgets.HeatRamp{tcell.ColorBlack, tcell.ColorWhite}

// snapshotBufferCells is a small local helper (distinct from
// sec020_test.go's package-external snapshotBuffer, which lives in
// mapscreen_test and is therefore unreachable from this white-box file)
// capturing every cell in a w x h buffer for before/after comparison.
func snapshotBufferCells(buf *core.Buffer, w, h int) []core.Cell {
	out := make([]core.Cell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, buf.Get(x, y))
		}
	}
	return out
}

// TestPaintOverlay_NeverTouchesForegroundGlyph is AC-4's foreground-
// invariance half: paint a buffer with known glyphs (simulating what
// drawViewport already put there), run paintOverlay with a provider that
// reports a value for every cell, and assert every Rune is byte-identical
// before and after — only Style (background) may change.
func TestPaintOverlay_NeverTouchesForegroundGlyph(t *testing.T) {
	const w, h = 3, 2
	buf := core.NewBuffer(w, h)
	glyphs := [][]rune{{'~', '.', '='}, {'^', '#', '+'}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf.Set(x, y, glyphs[y][x], tcell.StyleDefault)
		}
	}
	before := snapshotBufferCells(buf, w, h)

	snap := renderSnapshot{width: w, height: h, offsetX: 0, offsetY: 0}
	get := func(ov Overlay, x, y int) (float64, bool) {
		return float64(x+y) / float64(w+h), true // every cell has a value
	}
	paintOverlay(buf, core.Rect{X: 0, Y: 0, W: w, H: h}, snap, OverlayTraffic, get, 0, 1, twoStopRamp)

	after := snapshotBufferCells(buf, w, h)
	for i := range before {
		if before[i].Rune != after[i].Rune {
			t.Fatalf("cell %d: Rune changed from %q to %q — paintOverlay must never touch the foreground glyph (AC-4)", i, before[i].Rune, after[i].Rune)
		}
	}
	// Sanity: background actually did change somewhere, or this test
	// would pass vacuously even if paintOverlay painted nothing at all.
	changed := false
	for i := range before {
		if before[i].Style != after[i].Style {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("no cell's background changed at all — paintOverlay did not paint anything, this test would pass even if the mechanism were broken")
	}
}

// TestPaintOverlay_SwitchingOverlays_BackgroundDiffersGlyphsIdentical is
// the crown-jewel AC-4 proof: render with overlay A's synthetic values,
// then overlay B's DIFFERENT synthetic values, into two separate buffers
// seeded identically. Foreground glyphs must be byte-identical between
// the two renders (unchanged cells); backgrounds must differ for at
// least one cell (the values genuinely differ) — proving switching
// overlays changes ONLY the background, never the foreground, and that a
// real background difference is actually detectable when the underlying
// values differ (not just "nothing painted, so nothing differs").
func TestPaintOverlay_SwitchingOverlays_BackgroundDiffersGlyphsIdentical(t *testing.T) {
	const w, h = 2, 2
	seed := func() *core.Buffer {
		buf := core.NewBuffer(w, h)
		buf.Set(0, 0, '~', tcell.StyleDefault)
		buf.Set(1, 0, '.', tcell.StyleDefault)
		buf.Set(0, 1, '=', tcell.StyleDefault)
		buf.Set(1, 1, '^', tcell.StyleDefault)
		return buf
	}
	snap := renderSnapshot{width: w, height: h}
	rect := core.Rect{X: 0, Y: 0, W: w, H: h}

	bufA := seed()
	getA := func(ov Overlay, x, y int) (float64, bool) { return 0, true } // every cell: low (black)
	paintOverlay(bufA, rect, snap, OverlayTraffic, getA, 0, 1, twoStopRamp)

	bufB := seed()
	getB := func(ov Overlay, x, y int) (float64, bool) { return 1, true } // every cell: high (white)
	paintOverlay(bufB, rect, snap, OverlayServiceCoverage, getB, 0, 1, twoStopRamp)

	cellsA := snapshotBufferCells(bufA, w, h)
	cellsB := snapshotBufferCells(bufB, w, h)

	anyBackgroundDiffered := false
	for i := range cellsA {
		if cellsA[i].Rune != cellsB[i].Rune {
			t.Fatalf("cell %d: foreground Rune differs between overlay A (%q) and overlay B (%q) renders — AC-4 forbids overlay switching from touching the foreground glyph", i, cellsA[i].Rune, cellsB[i].Rune)
		}
		if cellsA[i].Style != cellsB[i].Style {
			anyBackgroundDiffered = true
		}
	}
	if !anyBackgroundDiffered {
		t.Fatalf("no cell's background differed between overlay A and overlay B, even though their synthetic values were 0 vs 1 for every cell — the two-data-layer paint mechanism is not actually applying overlay-specific colour")
	}
}

// TestPaintOverlay_PerCellDifferential_OnlyMutatedCellChanges mutates a
// SINGLE cell's value between two paintOverlay calls on otherwise
// identical providers and asserts every OTHER cell's background is
// unaffected — the differential property the dispatch brief calls out
// explicitly ("change one cell's metric, only that cell's background
// changes").
func TestPaintOverlay_PerCellDifferential_OnlyMutatedCellChanges(t *testing.T) {
	const w, h = 3, 3
	seed := func() *core.Buffer {
		buf := core.NewBuffer(w, h)
		buf.Fill('.', tcell.StyleDefault)
		return buf
	}
	snap := renderSnapshot{width: w, height: h}
	rect := core.Rect{X: 0, Y: 0, W: w, H: h}

	baseline := func(ov Overlay, x, y int) (float64, bool) { return 0, true }
	bufBefore := seed()
	paintOverlay(bufBefore, rect, snap, OverlayDecay, baseline, 0, 1, twoStopRamp)

	// mutated: identical to baseline except grid cell (1,1) reports 1
	// instead of 0.
	mutated := func(ov Overlay, x, y int) (float64, bool) {
		if x == 1 && y == 1 {
			return 1, true
		}
		return 0, true
	}
	bufAfter := seed()
	paintOverlay(bufAfter, rect, snap, OverlayDecay, mutated, 0, 1, twoStopRamp)

	before := snapshotBufferCells(bufBefore, w, h)
	after := snapshotBufferCells(bufAfter, w, h)

	mutatedIdx := 1*w + 1 // (x=1, y=1) in row-major order
	for i := range before {
		if before[i].Rune != after[i].Rune {
			t.Fatalf("cell %d: Rune changed — paintOverlay must never touch the foreground glyph", i)
		}
		if i == mutatedIdx {
			if before[i].Style == after[i].Style {
				t.Fatalf("mutated cell (1,1) background did not change even though its metric value changed from 0 to 1")
			}
			continue
		}
		if before[i].Style != after[i].Style {
			t.Fatalf("cell %d background changed even though only cell (1,1)'s metric value was mutated — the differential must be isolated to the mutated cell", i)
		}
	}
}

// TestPaintOverlay_HaveFalse_LeavesCellUntouched proves the BLOCKED-
// overlay production path (overlayLiveValue, which always reports
// have=false) leaves every cell exactly as drawViewport already painted
// it — this is how switching between two BLOCKED overlays in production
// today correctly produces byte-identical renders (both foreground AND
// background), the honest current state, without paintOverlay needing
// any special-cased "is this overlay blocked" branch.
func TestPaintOverlay_HaveFalse_LeavesCellUntouched(t *testing.T) {
	const w, h = 2, 2
	buf := core.NewBuffer(w, h)
	buf.Set(0, 0, '~', tcell.StyleDefault.Background(tcell.ColorBlue))
	buf.Set(1, 0, '.', tcell.StyleDefault.Background(tcell.ColorGreen))
	buf.Set(0, 1, '=', tcell.StyleDefault.Background(tcell.ColorYellow))
	buf.Set(1, 1, '^', tcell.StyleDefault.Background(tcell.ColorPurple))
	before := snapshotBufferCells(buf, w, h)

	snap := renderSnapshot{width: w, height: h}
	paintOverlay(buf, core.Rect{X: 0, Y: 0, W: w, H: h}, snap, OverlayOwnership, overlayLiveValue, 0, 1, twoStopRamp)

	after := snapshotBufferCells(buf, w, h)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("cell %d changed (%+v -> %+v) after paintOverlay with the production overlayLiveValue provider (always have=false) — a BLOCKED overlay must leave every cell untouched", i, before[i], after[i])
		}
	}
}

// TestOverlayLiveValue_EveryOverlay_ReportsHaveFalse is the mechanical
// proof that overlayLiveValue's BLOCKED posture covers all ten AC-3
// overlays today, not just the ones exercised above. Each overlay must
// also carry a non-empty overlayBlockedReason — GR#7's "no silent
// failure" posture applied to a design-time gap, not just a runtime
// error: a BLOCKED overlay with no documented reason would be exactly
// the kind of undocumented gap this item's dispatch was scoped to avoid.
func TestOverlayLiveValue_EveryOverlay_ReportsHaveFalse(t *testing.T) {
	for _, ov := range overlayOrder {
		if _, have := overlayLiveValue(ov, 0, 0); have {
			t.Fatalf("overlayLiveValue(%v, ...) reports have=true — this overlay is no longer correctly BLOCKED; revisit overlayBlockedReason and this package's doc.go Overlay cycle section, and wire the real data source", ov)
		}
		if reason := overlayBlockedReason(ov); reason == "" || reason == "unrecognised overlay" {
			t.Fatalf("overlayBlockedReason(%v) = %q, want a real, non-empty documented reason", ov, reason)
		}
	}
}

// TestOverlayOrder_MatchesOverlayCount_NoDuplicates cross-checks
// overlayOrder (overlay.go) against overlayCount and overlayIndexOf: the
// cycle's own consistency invariant — exactly overlayCount entries, no
// duplicates, every entry findable by overlayIndexOf at its own position.
func TestOverlayOrder_MatchesOverlayCount_NoDuplicates(t *testing.T) {
	if len(overlayOrder) != int(overlayCount) {
		t.Fatalf("len(overlayOrder) = %d, want overlayCount = %d", len(overlayOrder), int(overlayCount))
	}
	seen := make(map[Overlay]bool, len(overlayOrder))
	for i, ov := range overlayOrder {
		if seen[ov] {
			t.Fatalf("overlayOrder[%d] = %v is a duplicate", i, ov)
		}
		seen[ov] = true
		if idx := overlayIndexOf(ov); idx != i {
			t.Fatalf("overlayIndexOf(%v) = %d, want %d (its position in overlayOrder)", ov, idx, i)
		}
	}
	if idx := overlayIndexOf(Overlay(-1)); idx != -1 {
		t.Fatalf("overlayIndexOf(-1) = %d, want -1 for an Overlay not in overlayOrder", idx)
	}
}
