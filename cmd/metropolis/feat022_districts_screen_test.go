package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/engine/policies"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-022's end-to-end proof: F8 — the Districts & Policies screen — is
// reachable (registered + F8-switchable) and renders REAL, non-empty
// content on the live data path, not "unavailable" panes and not a field of
// spaces.
//
// Deliberately an end-to-end assertion through the same bootCore the
// shipped binary uses, for the same reason feat209_census_screen_test.go
// and bug323_viewport_view_test.go are: engine.policies/engine.tax could
// hold a real district and ui.screen.districts could draw its tax panel,
// each unit-tested green, while the registered "f8.districts" view joining
// them was missing — only a test that boots the real composition and looks
// at the rendered BUFFER can fail when that join is removed.
//
// Honest scope note: baseline one seeds NO districts (engine.policies is
// constructed empty), so a fresh boot's F8 renders its real headers and
// "select a district to edit its tax settings" — the one LIVE glyph on the
// data path is the instrument label in a tax-setting row, which only exists
// once a district is created AND selected. TestFEAT022_..._RendersInstrumentLabelOnDataPath
// therefore creates a district + a per-district multiplier through the
// composed modules' own public APIs and advances the sim so the view
// republishes, then asserts the instrument label glyph — never fabricating
// a roster the engine would not produce.

// bootForDistrictsScreenTest boots a real composition and returns the wiring.
func bootForDistrictsScreenTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("feat022-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	if w.districtsScreen == nil {
		t.Fatal("bootCore did not construct w.districtsScreen (FEAT-022's F8 screen did not take)")
	}
	if w.composition == nil {
		t.Fatal("bootCore did not retain the *compose.Composition — the F8 data-path test needs it to reach the composed policies/tax modules")
	}
	return w
}

// seedDistrictThroughComposition creates one named district and sets a
// per-district multiplier on one instrument through the composed modules'
// own public APIs — the real data path, never a fabricated patch.
func seedDistrictThroughComposition(t *testing.T, w *skeletonWiring, name, instrument string, multiplier float64) string {
	t.Helper()
	did, err := w.composition.Policies().CreateDistrict(name, []policies.CellRef{{
		Tile:  world.TileCoord{X: 15, Y: 15},
		Local: world.CellLocal{Row: 0, Col: 0},
	}})
	if err != nil {
		t.Fatalf("CreateDistrict(%q): %v", name, err)
	}
	if err := w.composition.Tax().SetDistrictMultiplier(tax.DistrictID(did), instrument, multiplier); err != nil {
		t.Fatalf("SetDistrictMultiplier(%q, %q, %v): %v", did, instrument, multiplier, err)
	}
	return string(did)
}

// advanceAndWaitForDistrictsDelta sends one AdvanceTicks command (which wakes
// the subscription pump so the "f8.districts" view republishes) and polls the
// screen until its TaxSettings accessor reports the seeded rows — the router
// delivers the republished delta to the screen asynchronously after
// sendAndAwaitResult returns its CommandResult.
func advanceAndWaitForDistrictsDelta(t *testing.T, w *skeletonWiring, wantRows int) {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("feat022-advance"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}
	res := sendAndAwaitResult(t, w, cmd)
	if !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		settings, have := w.districtsScreen.TaxSettings()
		if have && len(settings) >= wantRows {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	settings, have := w.districtsScreen.TaxSettings()
	t.Fatalf("districtsScreen.TaxSettings() did not report %d rows after AdvanceTicks (have=%v, len=%d) — the republished \"f8.districts\" delta never reached the screen", wantRows, have, len(settings))
}

// TestFEAT022_DistrictsScreen_IsReachableAndRendersNonBlank proves F8 is
// registered + switchable through the real chrome-global key route and that
// its (empty-at-seed) render is real content, not a blank field: the
// always-on tax-panel header and the "select a district…" data-path prompt.
func TestFEAT022_DistrictsScreen_IsReachableAndRendersNonBlank(t *testing.T) {
	w := bootForDistrictsScreenTest(t)

	// F8 -> districts, through the real chrome-global routing path.
	routeKeyInput(w, keyMsg(tcell.KeyF8, 0))
	if got := w.screens.ActiveID(); got != screenIDDistricts {
		t.Fatalf("ActiveID() after F8 = %q, want %q — F8 is not wired as a chrome global", got, screenIDDistricts)
	}

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)
	t.Logf("F8 at %dx%d through a real bootCore (empty at seed):\n%s", width, height, text)

	counts := countGlyphs(text)
	if counts[' '] == width*height {
		t.Fatalf("F8 rendered completely blank at %dx%d — a registered screen whose view publishes nothing renders as an empty field", width, height)
	}
	if !strings.Contains(text, "PER-DISTRICT TAX SETTINGS") {
		t.Fatalf("F8's rendered output lacks the tax-panel header — the screen drew nothing recognisable:\n%s", text)
	}
	// The data-path (have=true) empty-roster prompt: with no district yet,
	// the panel renders this rather than the "unavailable" no-data branch.
	if !strings.Contains(text, "select a district to edit its tax settings") {
		t.Fatalf("F8's rendered output lacks \"select a district to edit its tax settings\" (the data-path empty-roster prompt) — the view may be absent or the screen took the no-data branch:\n%s", text)
	}
}

// TestFEAT022_DistrictsScreen_RendersInstrumentLabelOnDataPath is the
// rendered-glyph CONTENT proof: create a district + a per-district
// multiplier through the composed modules, advance the sim so the view
// republishes, select the district, and assert the flattened glyph text
// carries the instrument's data-loaded label — drawn ONLY on the data path
// (RenderTaxSettings renders a "%s x%.2f -> %.2f%%" row per instrument when
// a district is selected and has settings; the no-data branch draws
// "unavailable" and the empty-roster branch draws "select a district…").
func TestFEAT022_DistrictsScreen_RendersInstrumentLabelOnDataPath(t *testing.T) {
	w := bootForDistrictsScreenTest(t)

	did := seedDistrictThroughComposition(t, w, "Folkestone", "council-tax", 1.5)
	advanceAndWaitForDistrictsDelta(t, w, 6)

	// F8 -> districts, then select the seeded district (local UI state — no
	// keybinding exists for district selection in this increment, so it is
	// set through the screen's own public API).
	routeKeyInput(w, keyMsg(tcell.KeyF8, 0))
	if got := w.screens.ActiveID(); got != screenIDDistricts {
		t.Fatalf("ActiveID() after F8 = %q, want %q", got, screenIDDistricts)
	}
	w.districtsScreen.SetSelectedDistrict(did)

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)
	t.Logf("F8 at %dx%d after seeding district %q and selecting it:\n%s", width, height, did, text)

	// "Council tax" is the data-loaded instrument name (data/tax_instruments.json);
	// it prints only in a rendered tax-setting row, which itself exists only
	// on the data path (district selected + settings present).
	if !strings.Contains(text, "Council tax") {
		t.Fatalf("F8's rendered output lacks the instrument label \"Council tax\", which appears only in a rendered tax-setting row (the no-data branch draws \"unavailable\", the empty-roster branch draws \"select a district…\") — the view/join is not feeding the render layer:\n%s", text)
	}
	// The engine-computed effective rate (council-tax reference rate 100 ×
	// 1.5 = 150%) is the row's own figure — its glyph proves the join's math,
	// not just a label echo.
	if !strings.Contains(text, "150.00%") {
		t.Fatalf("F8's rendered output lacks \"150.00%%\" (the council-tax effective rate 100 × 1.5) — the join's EffectiveRate is not being rendered:\n%s", text)
	}
}
