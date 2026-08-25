package mapscreen_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// FEAT-199 tests: the zoning fields ride the existing f1.viewport wire
// (additive, omitempty) and render through the SAME two-layer mechanism
// as terrain — background colour from the injected data-driven palette,
// foreground glyph from the cell's data layer. The JSON here is
// hand-written rather than marshalled from internal/engine/stub because
// stub's fixture types predate the zoning fields; the wire shape is the
// contract, and these literals are exactly what compose's
// viewport_publish.go emits for a zoned cell.

// bgOf extracts a style's background colour via tcell's Decompose — the
// public getter, since Background() is a setter.
func bgOf(s tcell.Style) tcell.Color {
	_, bg, _ := s.Decompose()
	return bg
}

const feat199FullPatch = `{
  "schemaVersion": 1,
  "full": true,
  "origin": {"x": 0, "y": 0},
  "extent": {"width": 2, "height": 2},
  "cells": [
    {"x": 0, "y": 0, "terrain": "grass", "zone": "residential", "zoneDensity": 3, "zoneColourKey": "res3"},
    {"x": 1, "y": 0, "terrain": "water", "zone": "mining", "zoneDensity": 5, "zoneColourKey": "mine5"},
    {"x": 0, "y": 1, "terrain": "grass", "zone": "residential", "zoneDensity": 1, "zoneColourKey": "unknown-key"},
    {"x": 1, "y": 1, "terrain": "rock"}
  ]
}`

func feat199Setup(t *testing.T, palette map[string]tcell.Color) (*mapscreen.MapScreen, *core.Buffer) {
	t.Helper()
	m := mapscreen.NewMapScreen("feat199-corr", widgets.DefaultPalette)
	if palette != nil {
		m.SetZonePalette(palette)
	}
	m.ApplyPatch([]byte(feat199FullPatch))
	buf := core.NewBuffer(4, 4)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 4, H: 4})
	return m, buf
}

// TestZoneCellRendersDataDrivenBackgroundAndDensityGlyph: a zoned cell's
// BACKGROUND is its palette colour (data-driven via SetZonePalette) and
// its GLYPH is the density digit — density readable without colour vision.
func TestZoneCellRendersDataDrivenBackgroundAndDensityGlyph(t *testing.T) {
	res3 := tcell.PaletteColor(120)
	_, buf := feat199Setup(t, map[string]tcell.Color{"res3": res3})

	cell := buf.Get(0, 0)
	if got := bgOf(cell.Style); got != res3 {
		t.Errorf("zoned cell background = %v, want the injected res3 palette colour %v", got, res3)
	}
	if cell.Rune != '3' {
		t.Errorf("zoned cell glyph = %q, want '3' (density digit)", cell.Rune)
	}
}

// TestZoneCellUnknownKeyFallsBackToTerrain: a wire key the injected
// palette lacks degrades to the terrain colour (never a panic, never a
// blank), while the density digit still renders — the missing entry costs
// a tint, not the data.
func TestZoneCellUnknownKeyFallsBackToTerrain(t *testing.T) {
	res3 := tcell.PaletteColor(120)
	_, buf := feat199Setup(t, map[string]tcell.Color{"res3": res3})

	cell := buf.Get(1, 0) // mining/5, no palette entry injected
	want := widgets.DefaultPalette.Color(widgets.TokenWater)
	if got := bgOf(cell.Style); got != want {
		t.Errorf("unknown-key zoned cell background = %v, want terrain fallback %v", got, want)
	}
	if cell.Rune != '5' {
		t.Errorf("glyph = %q, want '5' even with an unresolved colour key", cell.Rune)
	}
}

// TestUnzonedCellUnchangedByZoningPath: a cell with no zone fields renders
// byte-identically to the pre-FEAT-199 path — same terrain token colour,
// same terrain glyph. The zoning layer must be invisible when absent.
func TestUnzonedCellUnchangedByZoningPath(t *testing.T) {
	_, buf := feat199Setup(t, map[string]tcell.Color{})

	cell := buf.Get(1, 1) // rock, no zone fields
	want := widgets.DefaultPalette.Color(widgets.TokenDecay)
	if got := bgOf(cell.Style); got != want {
		t.Errorf("unzoned cell background = %v, want terrain token %v", got, want)
	}
	if cell.Rune != '^' {
		t.Errorf("unzoned cell glyph = %q, want rock's '^'", cell.Rune)
	}
}

// TestNoPaletteInjectedStillRenders: before SetZonePalette runs, zoned
// cells keep their terrain colours but still show their digits — the
// screen never requires the injection to function.
func TestNoPaletteInjectedStillRenders(t *testing.T) {
	m := mapscreen.NewMapScreen("feat199-corr", widgets.DefaultPalette)
	m.ApplyPatch([]byte(feat199FullPatch))
	buf := core.NewBuffer(4, 4)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 4, H: 4})

	cell := buf.Get(0, 0)
	want := widgets.DefaultPalette.Color(widgets.TokenMoney)
	if got := bgOf(cell.Style); got != want {
		t.Errorf("pre-injection background = %v, want terrain token %v", got, want)
	}
	if cell.Rune != '3' {
		t.Errorf("pre-injection glyph = %q, want '3'", cell.Rune)
	}
}

// TestInspectSurfacesZoningState: Inspect carries the zoning fields
// verbatim off the applied snapshot — the "enter to inspect" seam shows
// what the engine actually holds.
func TestInspectSurfacesZoningState(t *testing.T) {
	m, _ := feat199Setup(t, nil)

	r := m.Inspect(0, 0)
	if !r.Found || r.Zone != "residential" || r.ZoneDensity != 3 || r.ZoneColourKey != "res3" {
		t.Errorf("Inspect(0,0) = %+v, want Found with residential/3/res3", r)
	}
	r = m.Inspect(1, 1)
	if !r.Found || r.Zone != "" || r.ZoneDensity != 0 || r.ZoneColourKey != "" {
		t.Errorf("Inspect(1,1) = %+v, want Found with zeroed zoning state", r)
	}
}

// TestSetZonePaletteReplacesWholesale: a second SetZonePalette call
// REPLACES the palette (the old key stops resolving) — replacement, not
// accumulation, so a live re-theme can never strand stale entries.
func TestSetZonePaletteReplacesWholesale(t *testing.T) {
	m, _ := feat199Setup(t, map[string]tcell.Color{"res3": tcell.PaletteColor(120)})
	m.SetZonePalette(map[string]tcell.Color{"mine5": tcell.PaletteColor(52)})

	buf := core.NewBuffer(4, 4)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 4, H: 4})

	if got := bgOf(buf.Get(0, 0).Style); got == tcell.PaletteColor(120) {
		t.Error("old res3 colour still resolving after wholesale replace")
	}
	if got := bgOf(buf.Get(1, 0).Style); got != tcell.PaletteColor(52) {
		t.Errorf("mine5 background after replace = %v, want PaletteColor(52)", got)
	}
}
