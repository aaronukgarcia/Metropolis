package mapscreen

import (
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// overlayValueFunc returns the raw metric value for overlay ov at
// absolute grid coordinate (x, y), and whether one is currently
// available. Production Render always calls paintOverlay with
// overlayLiveValue (below); this indirection exists so the two-data-
// layers-per-cell paint mechanism (paintOverlay, AC-4) can be exercised
// in isolation, with a synthetic provider, by this package's own
// white-box tests (overlay_paint_internal_test.go) — proving the glue
// code that a real overlay will reuse verbatim once one lands, without
// this package fabricating any engine data today.
type overlayValueFunc func(ov Overlay, x, y int) (value float64, have bool)

// overlayLiveValue is the ONLY overlayValueFunc production code ever
// calls (render.go's Render). It reports have=false for every one of
// AC-3's ten named overlays — see overlayBlockedReason below for why,
// overlay by overlay. This is not a placeholder bug to silently fix
// later: it is FEAT-031's honestly-documented current state, checked
// mechanically by tripwire_test.go for the three overlays with a
// candidate engine module (traffic, per-service coverage, parking) and
// left as documented-not-fabricated for the remaining seven, which have
// no engine module or code.json node to probe at all (mirroring
// ui.screen.services' SVC-3 reasoning — see tripwire_test.go's header).
func overlayLiveValue(ov Overlay, x, y int) (float64, bool) {
	return 0, false
}

// overlayBlockedReason names, for each of AC-3's ten overlays, why it has
// no live per-cell data source today. Every reason cites either (a) a
// code.json outbound-edge gap against an engine module that already
// exists on this tree (traffic, services, parking — GR#25 forbids
// building against the unregistered edge even though the module itself
// is real and tick-wired), or (b) the total absence of any engine module
// or code.json node for that metric at all (the other seven — same
// "no stable detection point" posture ui.screen.services' SVC-3 note
// documents rather than fabricates a tripwire for). Read alongside
// tripwire_test.go, which makes the (a) cases mechanical.
func overlayBlockedReason(ov Overlay) string {
	switch ov {
	case OverlayTraffic:
		return "internal/engine/traffic exists and is wired into the daily tick (compose.Wire), but ui.screen.map has no registered engine.traffic outbound edge in code.json (GR#25), and no Subscribe/Delta path carries its output to any view today — see tripwire_test.go"
	case OverlayServiceCoverage:
		return "internal/engine/services.ServicesAPI.CoverageSummary() exists and is wired into the attract-terms pipeline (compose.Wire), but ui.screen.map has no registered engine.services outbound edge in code.json (GR#25), and no Subscribe/Delta path carries it to any view today — see tripwire_test.go"
	case OverlayParkingOccupancy:
		return "internal/engine/parking exists on this tree, but ui.screen.map has no registered engine.parking outbound edge in code.json (GR#25), and no Subscribe/Delta path exists — see tripwire_test.go"
	case OverlayOwnership:
		return "no engine module or code.json node exists yet for a per-cell ownership metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayLandValue:
		return "no engine module or code.json node exists yet for a per-cell land-value metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayZoning:
		return "no engine module or code.json node exists yet for a per-cell zoning metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayUtilities:
		return "no engine module or code.json node exists yet for a per-cell utilities metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayPollution:
		return "no engine module or code.json node exists yet for a per-cell pollution metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayDecay:
		return "no engine module or code.json node exists yet for a per-cell decay metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	case OverlayVitality:
		return "no engine module or code.json node exists yet for a per-cell vitality metric — no stable detection point to tripwire; revisit once a candidate module/edge lands"
	default:
		return "unrecognised overlay"
	}
}

// paintOverlay paints ov's background heatmap over rect (AC-4's
// background-only layer), sourcing each visible cell's value from get.
// It NEVER touches a cell's Rune (foreground glyph) — every write goes
// through widgets.Heatmap on a 1x1 sub-rect per cell, so widgets.Heatmap's
// own AC-4/AC-5 "never touches Rune" contract (heatmap.go) applies
// per-cell, not just in aggregate. A cell whose get reports have=false is
// left completely untouched (buf keeps whatever drawViewport already
// painted for it — the terrain/road/building background and glyph) —
// this is how a BLOCKED overlay (overlayLiveValue, always have=false)
// renders identically to no overlay at all: honestly, not by a special
// case, but because paintOverlay's per-cell skip already does the right
// thing whether zero cells or all cells report have=false.
//
// min/max normalise get's raw values into ramp's [0,1] domain (same
// contract as widgets.Heatmap itself — pass 0,1 if values are already
// normalised).
func paintOverlay(buf *core.Buffer, rect core.Rect, snap renderSnapshot, ov Overlay, get overlayValueFunc, min, max float64, ramp widgets.HeatRamp) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || get == nil {
		return
	}
	for row := 0; row < rect.H; row++ {
		for col := 0; col < rect.W; col++ {
			gx, gy := snap.offsetX+col, snap.offsetY+row
			v, have := get(ov, gx, gy)
			if !have {
				continue
			}
			cellRect := core.Rect{X: rect.X + col, Y: rect.Y + row, W: 1, H: 1}
			widgets.Heatmap(buf, cellRect, []float64{v}, 1, min, max, ramp)
		}
	}
}
