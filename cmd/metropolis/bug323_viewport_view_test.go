package main

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// BUG-323's end-to-end proof: F1 — the DEFAULT screen at boot — renders
// REAL terrain, not an empty field of spaces.
//
// This is deliberately an end-to-end assertion through the same bootCore
// the shipped binary uses, at the same 100x24 the finding was
// reproduced at, because the bug this file guards was not in any single
// component: engine.world held real terrain, ui.screen.map could draw
// it, and both were unit-tested green — what was missing was the
// registered "f1.viewport" view joining them (compose's
// viewRegistrationOrder). Only a test that boots the real composition
// and looks at the rendered BUFFER can fail when that join is removed
// again; a test on either side's internals cannot.
//
// It asserts on GLYPHS, never on internal screen state: a subscription
// that is accepted, a patch that decodes, and a grid that is populated
// are all necessary and none of them is what the player sees. The
// screen is what the player sees.

// bug323BootAndRenderMap boots a real composition, draws the initially
// active screen (map, per FEAT-211's registration order) into a fresh
// width x height buffer, and returns the flattened text.
func bug323BootAndRenderMap(t *testing.T, width, height int) string {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("bug323-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)

	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("precondition: initially active screen = %q, want %q — this test only proves anything if the map is what boot renders", got, screenIDMap)
	}

	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	return bufferText(back)
}

// countGlyphs tallies every rune in text except the row separators.
func countGlyphs(text string) map[rune]int {
	counts := make(map[rune]int)
	for _, r := range text {
		if r == '\n' {
			continue
		}
		counts[r]++
	}
	return counts
}

// TestBUG323_DefaultScreenAtBoot_RendersRealTerrainNotBlank is the
// regression test for the finding itself: at 100x24, through a real
// bootCore, the map's viewport area must be covered in terrain glyphs.
//
// Before the fix this failed on the very first assertion — the entire
// buffer was spaces except a single staleness dot.
func TestBUG323_DefaultScreenAtBoot_RendersRealTerrainNotBlank(t *testing.T) {
	const width, height = 100, 24
	text := bug323BootAndRenderMap(t, width, height)

	// The screenshot is logged unconditionally (go test -v) so the
	// rendered output is evidence a human can read, not just a pass/fail
	// bit — this bug was invisible for as long as it was because nobody
	// had ever LOOKED at this screen.
	t.Logf("F1 at %dx%d through a real bootCore:\n%s", width, height, text)

	counts := countGlyphs(text)
	blanks := counts[' ']
	total := width * height

	if blanks == total || blanks == total-1 {
		t.Fatalf("F1 rendered blank at %dx%d: %d of %d cells are spaces — this is exactly the BUG-323 finding (an unregistered \"f1.viewport\" view leaves every cell Known=false, which renders as blankGlyph)", width, height, blanks, total)
	}

	// The viewport is every row but the last (render.go's splitRect
	// reserves one row for the minimap strip). Require the overwhelming
	// majority of it to be non-blank: a handful of blank cells would be
	// a legitimate terrain feature, but a mostly-blank viewport is the
	// bug.
	const minNonBlankFraction = 0.9
	nonBlank := total - blanks
	if float64(nonBlank) < minNonBlankFraction*float64(total) {
		t.Fatalf("F1's rendered viewport is mostly blank: %d of %d cells carry a glyph (want at least %.0f%%) — a view that registers but publishes nothing renders identically to no view at all", nonBlank, total, minNonBlankFraction*100)
	}

	// And the glyphs must be TERRAIN glyphs the map's own vocabulary
	// produces, not incidental text: at least one of engine.world's five
	// real surface runes must be present. This is what catches a view
	// that publishes a terrain string the renderer does not recognise
	// (which falls through to blankGlyph and looks exactly like no view
	// at all).
	terrainRunes := []rune{'.', '%', '~', ':', '^'}
	found := 0
	for _, r := range terrainRunes {
		found += counts[r]
	}
	if found == 0 {
		t.Fatalf("F1's rendered output contains none of the map's terrain glyphs %q — the view published something ui.screen.map cannot draw; rendered:\n%s", string(terrainRunes), text)
	}
}

// TestBUG323_RenderedTerrain_MatchesWhatEngineWorldActuallyHolds pins
// the CONTENT, not merely its non-blankness. BUG-329 made the
// synthesiser emit more than grass; the map must therefore show more
// than one of engine.world's five surface glyphs, and specifically must
// NOT be rendering the Sprint-1 stub fixture's motorway band ('=').
func TestBUG323_RenderedTerrain_MatchesWhatEngineWorldActuallyHolds(t *testing.T) {
	const width, height = 100, 24
	text := bug323BootAndRenderMap(t, width, height)
	counts := countGlyphs(text)

	terrainRunes := []rune{'.', '%', '~', ':', '^'}
	kinds := 0
	for _, r := range terrainRunes {
		if counts[r] > 0 {
			kinds++
		}
	}
	if kinds < 2 {
		t.Fatalf("expected at least 2 of engine.world's surface glyphs %q after BUG-329, got %d kinds; full render:\n%s", string(terrainRunes), kinds, text)
	}

	// Negative half: the stub fixture's own bands must not appear. '='
	// (motorway) is unique to the fixture vocabulary — '~' and '^' are
	// shared runes between the two vocabularies, so only '=' is a
	// sound discriminator.
	if counts['='] != 0 {
		t.Fatalf("rendered map contains %d motorway glyphs ('='), which only internal/engine/stub's Folkestone-64 fixture produces — the real composition must be publishing engine.world's terrain, not a fixture; full render:\n%s", counts['='], text)
	}
}

// TestBUG323_ViewportSubscription_IsAcceptedAndDelivers proves the
// engine side directly: bootCore itself now primes "f1.viewport" through
// primeScreenSubscription, which FAILS THE BOOT if the Subscribe is
// rejected or if no Delta arrives within feat208PrimeTimeout. So a
// successful bootCore is already proof the subscription was accepted and
// delivered — but only if that priming call is actually present, which
// is what this test pins by name.
//
// It then asserts the patch reached the SCREEN's own state (Inspect on a
// known cell), which is the link between "a delta arrived" and "the
// screen has data": the sibling failure mode BUG-323 warns about
// (f4.services registers fine and publishes an empty roster forever) is
// exactly a delta arriving with nothing in it.
func TestBUG323_ViewportSubscription_IsAcceptedAndDelivers(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("bug323-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore failed — with f1.viewport primed, this means the Subscribe was rejected or no Delta arrived: %v", err)
	}
	t.Cleanup(w.shutdown)

	// Cell (0,0) of the start tile: real, in-extent, always covered by a
	// full snapshot.
	got := w.mapScreen.Inspect(0, 0)
	if !got.Found {
		t.Fatal("mapScreen.Inspect(0,0).Found = false after boot — the subscription was accepted but the screen holds no cell data; a registered-but-empty view is not a fix")
	}
	if got.Terrain == "" {
		t.Fatalf("mapScreen.Inspect(0,0).Terrain is empty — the published cell carries no terrain string; got %+v", got)
	}
	// Elevation is published as real metres AOD. After BUG-329 some cells
	// sit below the synthetic sea level (water); (0,0) may be either
	// side. A missing elevation is an empty Terrain string, already
	// rejected above — do not require a positive height here.

	// The far corner of the start tile must be known too: the published
	// window is the whole 200x200 tile, matching compose's own
	// cellFromRef coordinate space.
	if far := w.mapScreen.Inspect(199, 199); !far.Found {
		t.Fatal("mapScreen.Inspect(199,199).Found = false — the published window does not cover the whole start tile, so the map's coordinates no longer line up with the Buy/Zone/Build command seam (compose's cellFromRef)")
	}
}

// TestBUG323_MinimapStrip_IsAlsoPopulated covers the one row the
// viewport assertion above deliberately excludes. A blank minimap under
// a populated viewport would mean drawMinimap's dominantTerrain sampling
// silently disagrees with what the viewport draws.
func TestBUG323_MinimapStrip_IsAlsoPopulated(t *testing.T) {
	const width, height = 100, 24
	text := bug323BootAndRenderMap(t, width, height)

	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(rows) != height {
		t.Fatalf("rendered %d rows, want %d", len(rows), height)
	}
	minimap := rows[height-1]
	if strings.TrimSpace(minimap) == "" {
		t.Fatalf("the minimap strip (last row) rendered blank while the viewport above it did not:\n%s", text)
	}
}
