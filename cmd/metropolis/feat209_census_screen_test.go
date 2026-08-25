package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-209's end-to-end proof: F6 — the Demographics/Census screen — is
// reachable (registered + F6-switchable) and renders REAL, non-empty
// content, not "unavailable" panes.
//
// Deliberately an end-to-end assertion through the same bootCore the
// shipped binary uses, for the same reason bug323_viewport_view_test.go is:
// engine.census could hold real data and ui.screen.census could draw it,
// each unit-tested green, while the registered "f6.census" view joining
// them was missing — only a test that boots the real composition and looks
// at the rendered BUFFER can fail when that join is removed. It asserts on
// GLYPHS, never on "the subscription succeeded": "0-17" (the first age-band
// row label) is drawn only when the census screen actually holds data; the
// no-data branch draws the literal "unavailable" instead.

// bootForCensusScreenTest boots a real composition and returns the wiring.
func bootForCensusScreenTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("feat209-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	if w.censusScreen == nil {
		t.Fatal("bootCore did not construct w.censusScreen (FEAT-209's F6 screen did not take)")
	}
	return w
}

// TestFEAT209_CensusSubscription_IsAcceptedAndDelivers proves the engine
// side directly: bootCore primes "f6.census" through primeScreenSubscription
// (which FAILS THE BOOT if the Subscribe is rejected or no Delta arrives),
// so a successful bootCore already proves acceptance + delivery — but only
// if the priming call is present, which this pins by observing the screen's
// own state: HaveData must be true and the age-band spline must hold the
// real 64-citizen seed, not an empty roster.
func TestFEAT209_CensusSubscription_IsAcceptedAndDelivers(t *testing.T) {
	w := bootForCensusScreenTest(t)

	if !w.censusScreen.HaveData() {
		t.Fatal("censusScreen.HaveData() = false after boot — the subscription was not primed/delivered; a registered-but-empty view is not a fix (FEAT-209)")
	}
	bands, have := w.censusScreen.AgeBandSeries()
	if !have {
		t.Fatal("censusScreen.AgeBandSeries() reported have=false after boot — the published patch carried no ageBands the screen recognised")
	}
	var pop int64
	for _, v := range bands {
		pop += v
	}
	if pop != 64 {
		t.Fatalf("AgeBandSeries() sums to %d, want 64 (seed population) — the published view is empty/placeholder, not real citizens", pop)
	}
}

// TestFEAT209_CensusScreen_RendersNonEmptyGlyphs is the rendered-glyph
// proof: switch to F6 and draw it into a buffer, then assert the flattened
// text carries the census screen's own data-bearing glyphs (the age-band
// row label "0-17", present only when data flowed) rather than the
// "unavailable" placeholder or a field of spaces.
func TestFEAT209_CensusScreen_RendersNonEmptyGlyphs(t *testing.T) {
	w := bootForCensusScreenTest(t)

	// F6 -> census, through the real chrome-global routing path.
	routeKeyInput(w, keyMsg(tcell.KeyF6, 0))
	if got := w.screens.ActiveID(); got != screenIDCensus {
		t.Fatalf("ActiveID() after F6 = %q, want %q — F6 is not wired as a chrome global", got, screenIDCensus)
	}

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)
	t.Logf("F6 at %dx%d through a real bootCore:\n%s", width, height, text)

	counts := countGlyphs(text)
	blanks := counts[' ']
	if blanks == width*height {
		t.Fatalf("F6 rendered completely blank at %dx%d — a registered screen whose view publishes nothing renders as an empty field", width, height)
	}

	// The age-band row label "0-17" is drawn ONLY when the screen holds real
	// age-band data (the no-data branch draws "unavailable" instead). Its
	// presence is therefore the rendered-glyph equivalent of "non-empty".
	if !strings.Contains(text, "POPULATION PYRAMID") {
		t.Fatalf("F6's rendered output lacks the age-pyramid header — the screen drew nothing recognisable:\n%s", text)
	}
	if !strings.Contains(text, "0-17") {
		t.Fatalf("F6's rendered output lacks the age-band row label \"0-17\", which appears only when the screen has data (no data renders \"unavailable\") — the view is empty:\n%s", text)
	}
	if strings.Contains(text, "unavailable") {
		t.Fatalf("F6's rendered output contains an \"unavailable\" pane, which means at least one surface received no data:\n%s", text)
	}
}

// TestFEAT209_CensusScreen_KPITilesRender proves the eight-KPI tile row is
// drawn with real values, not a header-only skeleton — the KPI keys are the
// second F6 headline surface, and their labels only print on the data path.
func TestFEAT209_CensusScreen_KPITilesRender(t *testing.T) {
	w := bootForCensusScreenTest(t)
	routeKeyInput(w, keyMsg(tcell.KeyF6, 0))

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)

	if !strings.Contains(text, "CITY KPIs") {
		t.Fatalf("F6's rendered output lacks the \"CITY KPIs\" header — the KPI tile row did not draw:\n%s", text)
	}
	// "Homeless" is one of the eight KPI keys; its label prints on the data
	// path (all eight tiles always publish).
	if !strings.Contains(text, "Homeless") {
		t.Fatalf("F6's rendered KPI row lacks the \"Homeless\" tile label — the KPI tiles are not rendering:\n%s", text)
	}
}
