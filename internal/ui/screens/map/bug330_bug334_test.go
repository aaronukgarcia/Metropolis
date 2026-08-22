package mapscreen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// unknownTerrainPatchRaw builds a full "f1.viewport" v1 patch whose
// n x n extent is entirely filled with the given terrain string (used to
// feed an unrecognised surface into the render path, BUG-334).
func unknownTerrainPatchRaw(t *testing.T, terrain string, n int) json.RawMessage {
	t.Helper()
	cells := make([]wireCell, 0, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			cells = append(cells, wireCell{X: x, Y: y, Terrain: terrain})
		}
	}
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: n, Height: n},
		Cells:         cells,
	})
	if err != nil {
		t.Fatalf("marshal unknown-terrain patch: %v", err)
	}
	return raw
}

// unknownTerrainPatchDistinctRaw builds an n x n "f1.viewport" full
// patch whose cells each carry a DISTINCT unrecognised terrain string
// ("unknown-<i>") — used to feed more novel surface classes than
// maxUnknownTerrainSeen in one render, for the round-D5 cap assertion.
func unknownTerrainPatchDistinctRaw(t *testing.T, n int) json.RawMessage {
	t.Helper()
	cells := make([]wireCell, 0, n*n)
	for i := 0; i < n*n; i++ {
		cells = append(cells, wireCell{X: i % n, Y: i / n, Terrain: fmt.Sprintf("unknown-%d", i)})
	}
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: n, Height: n},
		Cells:         cells,
	})
	if err != nil {
		t.Fatalf("marshal distinct-unknown-terrain patch: %v", err)
	}
	return raw
}

// TestUnknownTerrainSeen_CappedAtMax is the BUG-317 round-D5 assertion:
// a screen fed MORE distinct unrecognised surface strings than
// maxUnknownTerrainSeen records at most that many in its dedupe map, so
// a hostile stream of novel strings cannot grow unknownTerrainSeen
// without bound. The map is capped; past the cap, further novel surfaces
// draw '?' but are neither cached nor logged (no unbounded map growth,
// and no per-cell log storm for an uncached surface).
func TestUnknownTerrainSeen_CappedAtMax(t *testing.T) {
	var logBuf bytes.Buffer
	if err := errs.SetSink(errs.NewLogger(&logBuf)); err != nil {
		t.Fatalf("SetSink: %v", err)
	}
	defer func() {
		if err := errs.SetSink(nil); err != nil {
			t.Errorf("restore SetSink(nil): %v", err)
		}
	}()

	const n = 12 // 144 distinct surfaces > 64 = maxUnknownTerrainSeen
	m := NewMapScreen("corr-unknown-capped", widgets.DefaultPalette)
	m.ApplyPatch(unknownTerrainPatchDistinctRaw(t, n))

	rect := core.Rect{X: 0, Y: 0, W: n, H: n}
	m.Render(core.NewBuffer(n, n), rect)

	if got := len(m.unknownTerrainSeen); got > maxUnknownTerrainSeen {
		t.Fatalf("unknownTerrainSeen held %d distinct surfaces after 144 fed, want at most %d (cap D5)", got, maxUnknownTerrainSeen)
	}
}

// TestTerrainGlyph_UnknownSurface_DrawsQuestion pins the BUG-334 glyph
// contract: a known surface keeps its distinct glyph, while ANY
// unrecognised surface string (including the empty string) draws the
// visible '?' glyph, never silent blankGlyph.
func TestTerrainGlyph_UnknownSurface_DrawsQuestion(t *testing.T) {
	known := map[string]rune{
		"shore":      glyphShore,
		"shelf":      glyphShelf,
		"motorway":   glyphMotorway,
		"escarpment": glyphEscarpment,
	}
	for terrain, want := range known {
		if got := terrainGlyph(terrain); got != want {
			t.Errorf("terrainGlyph(%q) = %q, want %q", terrain, got, want)
		}
	}

	for _, terrain := range []string{"unknown", "marsh", "SAND", ""} {
		if got := terrainGlyph(terrain); got != glyphUnknown {
			t.Errorf("terrainGlyph(%q) = %q, want %q (unrecognised surface must draw '?', not blank)", terrain, got, glyphUnknown)
		}
	}
}

// TestRender_UnknownTerrain_LogsOnceAndDrawsQuestion is the BUG-334
// integration assertion: a full grid of one unrecognised surface renders
// '?' glyphs (visible unknown-terrain, not blank) and logs the unknown
// surface through MET-U102 (ErrUnknownTerrainSurface) exactly ONCE — not
// once per cell, and not again on a second render of the same screen.
func TestRender_UnknownTerrain_LogsOnceAndDrawsQuestion(t *testing.T) {
	var logBuf bytes.Buffer
	if err := errs.SetSink(errs.NewLogger(&logBuf)); err != nil {
		t.Fatalf("SetSink: %v", err)
	}
	defer func() {
		if err := errs.SetSink(nil); err != nil {
			t.Errorf("restore SetSink(nil): %v", err)
		}
	}()

	m := NewMapScreen("corr-unknown-terrain", widgets.DefaultPalette)
	m.ApplyPatch(unknownTerrainPatchRaw(t, "marsh", 4))

	rect := core.Rect{X: 0, Y: 0, W: 4, H: 4}
	buf := core.NewBuffer(4, 4)
	m.Render(buf, rect)
	if got := buf.Get(0, 0).Rune; got != glyphUnknown {
		t.Fatalf("unknown-terrain cell (0,0) rendered rune %q, want %q ('?')", got, glyphUnknown)
	}

	// Second render of the same screen must not log the surface again.
	m.Render(core.NewBuffer(4, 4), rect)

	logs := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	matches := 0
	for _, line := range logs {
		if strings.Contains(line, ErrUnknownTerrainSurface) && strings.Contains(line, "marsh") {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("unknown surface %q logged %d times, want exactly 1 (dedupe per surface string, never per cell):\n%s", "marsh", matches, logBuf.String())
	}

	// Round D1: the unknown-surface path must log MET-U102, never the
	// MET-U100 "malformed f1.viewport patch" code — the patch WAS applied
	// and rendered; only the surface class is unrecognised.
	for _, line := range logs {
		if strings.Contains(line, "MET-U100") {
			t.Fatalf("unknown-terrain path logged MET-U100 (malformed-patch code), want MET-U102 only:\n%s", logBuf.String())
		}
	}
}

// TestRender_NoSnapshot_RendersNoTerrainData pins the BUG-330 map half: a
// screen with no applied snapshot (haveSnapshot == false) draws a centred
// "NO TERRAIN DATA" line rather than a blank grid indistinguishable from
// a broken screen.
func TestRender_NoSnapshot_RendersNoTerrainData(t *testing.T) {
	m := NewMapScreen("corr-no-snapshot", widgets.DefaultPalette)
	rect := core.Rect{X: 0, Y: 0, W: 30, H: 9}
	buf := core.NewBuffer(30, 9)
	m.Render(buf, rect)

	if !bufferContainsText(buf, rect, "NO TERRAIN DATA") {
		t.Fatalf("empty screen did not render the centred \"NO TERRAIN DATA\" message:\n%s", renderRows(buf, rect))
	}
}

// bufferContainsText reports whether any row of rect contains sub, after
// trimming trailing blanks (mirrors ui.screen.services' rowContains).
func bufferContainsText(buf *core.Buffer, rect core.Rect, sub string) bool {
	for _, row := range renderRows(buf, rect) {
		if strings.Contains(row, sub) {
			return true
		}
	}
	return false
}

// renderRows returns each row of rect as a trimmed string (blank runes as
// spaces), for test diagnostics.
func renderRows(buf *core.Buffer, rect core.Rect) []string {
	var rows []string
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		var sb strings.Builder
		for x := rect.X; x < rect.X+rect.W; x++ {
			c := buf.Get(x, y)
			if c.Rune == 0 || c.Rune == ' ' {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Rune)
			}
		}
		rows = append(rows, strings.TrimRight(sb.String(), " "))
	}
	return rows
}
