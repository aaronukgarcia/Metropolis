package mapscreen

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-334: terrainGlyph's default branch used to render any UNRECOGNISED
// surface string (a future 6th terrain, or a corrupt byte whose
// Surface.String() came through as "unknown") as blankGlyph with NO log —
// an empty screen indistinguishable from "nothing here", the silent-blank
// family (sibling of BUG-330). It must now draw a VISIBLE marker AND log
// the unrecognised surface once through the MET-U100 path, DEDUPED so a
// 40,000-cell grid does not emit 40,000 identical warn lines.
//
// RED proof (BUG-230 — no vacuous guard tests): scratch-revert render.go's
// terrainGlyph default from `return glyphUnknown` back to `return
// blankGlyph` and TestBUG334_TerrainGlyph_UnrecognisedReturnsVisibleMarker_NotBlank
// goes RED (the marker is blank again). Confirmed before landing.

// countingWriter is a thread-safe io.Writer that accumulates everything the
// errs sink Logger writes, so a test can count how many NDJSON lines carried
// a given error code.
type countingWriter struct {
	mu   sync.Mutex
	data []byte
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, p...)
	return len(p), nil
}

// count returns how many written NDJSON lines carried "code":"<code>".
func (c *countingWriter) count(code string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Count(string(c.data), `"code":"`+code+`"`)
}

func TestBUG334_TerrainGlyph_UnrecognisedReturnsVisibleMarker_NotBlank(t *testing.T) {
	// A future 6th surface, a corrupt "unknown", a case-mismatched real
	// name, an empty string, and a trailing-space near-miss all fall through
	// to the same VISIBLE marker — never blankGlyph.
	for _, terrain := range []string{"unknown", "marsh", "SAND", "", "Water", "grass "} {
		got := terrainGlyph(terrain)
		if got == blankGlyph {
			t.Errorf("terrainGlyph(%q) = blankGlyph (%q) — an unrecognised surface must never render as an empty cell (BUG-334)", terrain, blankGlyph)
		}
		if got != glyphUnknown {
			t.Errorf("terrainGlyph(%q) = %q, want the visible unknown marker %q", terrain, got, glyphUnknown)
		}
	}
}

func TestBUG334_TerrainGlyph_KnownSurfacesUnchanged(t *testing.T) {
	// No-regression: every recognised surface (both vocabularies) still maps
	// to its own distinct glyph, none of them the unknown marker.
	known := map[string]rune{
		"shore":      glyphShore,
		"shelf":      glyphShelf,
		"motorway":   glyphMotorway,
		"escarpment": glyphEscarpment,
		"grass":      glyphGrass,
		"woodland":   glyphWoodland,
		"water":      glyphWater,
		"shingle":    glyphShingle,
		"rock":       glyphRock,
	}
	for terrain, want := range known {
		got := terrainGlyph(terrain)
		if got != want {
			t.Errorf("terrainGlyph(%q) = %q, want %q (BUG-334 must not regress known surfaces)", terrain, got, want)
		}
		if got == glyphUnknown {
			t.Errorf("known surface %q rendered as the unknown marker %q", terrain, glyphUnknown)
		}
		if !terrainRecognised(terrain) {
			t.Errorf("terrainRecognised(%q) = false, want true", terrain)
		}
	}
	for _, unknown := range []string{"marsh", "unknown", ""} {
		if terrainRecognised(unknown) {
			t.Errorf("terrainRecognised(%q) = true, want false", unknown)
		}
	}
}

func TestBUG334_UnrecognisedTerrain_LogsMETU100_AtMostOncePerString(t *testing.T) {
	cw := &countingWriter{}
	logger := errs.NewLogger(cw)
	if err := errs.SetSink(logger); err != nil {
		t.Fatalf("SetSink: %v", err)
	}
	defer func() { _ = errs.SetSink(nil) }()

	m := NewMapScreen("corr-bug334", widgets.DefaultPalette)

	// A full 8x8 grid where EVERY one of the 64 cells carries the same
	// unrecognised surface "marsh" — the "grid of an unknown surface" the
	// bug describes, scaled down.
	const w, h = 8, 8
	cells := make([]wireCell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cells = append(cells, wireCell{X: x, Y: y, Terrain: "marsh"})
		}
	}
	m.applyFullLocked(wirePatch{Full: true, Extent: wireExtent{Width: w, Height: h}, Cells: cells})

	buf := core.NewBuffer(48, 14)
	// Render repeatedly: the dedup must hold across frames as well as across
	// the 64 cells of a single frame.
	for i := 0; i < 5; i++ {
		m.Render(buf, core.Rect{X: 0, Y: 0, W: 48, H: 14})
	}

	if n := cw.count("MET-U100"); n != 1 {
		t.Fatalf("64-cell all-\"marsh\" grid rendered 5x logged MET-U100 %d times, want exactly 1 — BUG-334 dedup must not log per-cell (a 40,000-cell grid would emit 40,000 lines)", n)
	}
	// Once-guard state proof, independent of the log sink.
	if !m.seenUnrecognisedTerrain["marsh"] {
		t.Fatalf("seenUnrecognisedTerrain does not record \"marsh\" — the dedup set is not tracking the sighting")
	}
	// The marker must actually be ON SCREEN (visible, not blank).
	if text := bufferText(buf); !strings.ContainsRune(text, glyphUnknown) {
		t.Fatalf("unknown surface did not draw the visible marker %q on screen\n%s", glyphUnknown, text)
	}

	// A DIFFERENT unrecognised surface logs its OWN single line — dedup is
	// per-distinct-string, not a global once.
	m.applySparseLocked(wirePatch{Cells: []wireCell{{X: 0, Y: 0, Terrain: "swamp"}}})
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 48, H: 14})
	if n := cw.count("MET-U100"); n != 2 {
		t.Fatalf("after a second distinct unknown surface, MET-U100 count = %d, want 2 (one per distinct string)", n)
	}

	// And a re-render of the now-familiar surfaces adds nothing.
	m.applySparseLocked(wirePatch{Cells: []wireCell{{X: 1, Y: 1, Terrain: "marsh"}}})
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 48, H: 14})
	if n := cw.count("MET-U100"); n != 2 {
		t.Fatalf("re-seeing already-logged surfaces logged again, MET-U100 count = %d, want still 2", n)
	}
}
