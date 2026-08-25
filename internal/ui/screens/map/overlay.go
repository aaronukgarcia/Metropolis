package mapscreen

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// Overlay identifies one F1 map-metric heatmap layer (AC-3, this item's
// BOW FEAT-031). The overlay cycle ("o" forward, "O" reverse — key
// BINDING remains ui.keys' later job per doc.go's "Scope" section, this
// package exposes CycleOverlay as the plain Go API a future binding
// drives, mirroring Pan/MoveCursor's existing precedent) steps through
// overlayOrder below, wrapping at both ends.
//
// §13-F1 / the FEAT-031 BOW item's overlay list: "ownership, land value,
// zoning, utilities, traffic, pollution, decay, per-service coverage,
// parking occupancy, vitality" — ten overlays, exactly overlayOrder's
// length (AC-3's "at minimum" is satisfied at exactly ten; nothing here
// stops a later item from appending an eleventh).
type Overlay int

const (
	OverlayOwnership Overlay = iota
	OverlayLandValue
	OverlayZoning
	OverlayUtilities
	OverlayTraffic
	OverlayPollution
	OverlayDecay
	OverlayServiceCoverage
	OverlayParkingOccupancy
	OverlayVitality

	// OverlayPower is FEAT-1972079851's eleventh entry: the placed-pylon
	// layer. Unlike the ten heatmaps above it (all blocked on per-cell
	// data sources today, overlayBlockedReason), this one has a REAL data
	// source — "f1.viewport"'s powerLines field — and paints placed spans
	// in class-distinct colours when active (render.go's drawPowerLines).
	// Appended at the END of the cycle so every existing ordinal stays
	// stable; overlay.go's own header note explicitly allows an eleventh.
	OverlayPower

	overlayCount
)

// overlayOrder is the fixed cycle order AC-3 / FEAT-031 list, in the
// exact sequence quoted above. o/O walk forward/backward through this
// slice; CycleOverlay wraps at both ends (index -1 becomes
// overlayCount-1, index overlayCount becomes 0).
var overlayOrder = [...]Overlay{
	OverlayOwnership,
	OverlayLandValue,
	OverlayZoning,
	OverlayUtilities,
	OverlayTraffic,
	OverlayPollution,
	OverlayDecay,
	OverlayServiceCoverage,
	OverlayParkingOccupancy,
	OverlayVitality,
	OverlayPower,
}

// String names ov for status-bar / log display. An out-of-range Overlay
// (should never happen — the only producers are overlayOrder's own
// entries and CycleOverlay's wrapping arithmetic) reports "unknown"
// rather than panicking or indexing out of bounds, mirroring
// widgets.Palette.Color's out-of-range discipline.
func (ov Overlay) String() string {
	switch ov {
	case OverlayOwnership:
		return "ownership"
	case OverlayLandValue:
		return "land value"
	case OverlayZoning:
		return "zoning"
	case OverlayUtilities:
		return "utilities"
	case OverlayTraffic:
		return "traffic"
	case OverlayPollution:
		return "pollution"
	case OverlayDecay:
		return "decay"
	case OverlayServiceCoverage:
		return "per-service coverage"
	case OverlayParkingOccupancy:
		return "parking occupancy"
	case OverlayVitality:
		return "vitality"
	case OverlayPower:
		return "power"
	default:
		return "unknown"
	}
}

// overlayIndexOf returns ov's position in overlayOrder, or -1 if ov is
// not one of the ten entries (defensive — every producer in this package
// only ever hands CycleOverlay/ActiveOverlay a value drawn from
// overlayOrder itself, but this keeps the lookup total rather than
// assuming the slice and the Overlay constant space stay in lockstep by
// convention alone).
func overlayIndexOf(ov Overlay) int {
	for i, o := range overlayOrder {
		if o == ov {
			return i
		}
	}
	return -1
}

// ActiveOverlay returns the overlay currently selected by the cycle
// (AC-3). Fails closed to overlayOrder[0] (OverlayOwnership) on a
// struct-copied receiver — the same starting overlay a freshly
// constructed MapScreen reports — mirroring Offset()/CursorPos()'s
// fail-closed posture.
func (m *MapScreen) ActiveOverlay() Overlay {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ActiveOverlay"}); err != nil {
		return overlayOrder[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ActiveOverlay"}); err != nil {
		return overlayOrder[0]
	}
	return overlayOrder[m.overlayIdx]
}

// CycleOverlay steps the active overlay forward (forward=true, "o") or
// backward (forward=false, "O") through overlayOrder, wrapping at both
// ends, and returns the newly active Overlay (AC-3: "cycling through all
// overlays returns to the starting overlay after N steps ... in each
// direction" — N calls in one direction always lands back on the overlay
// active before the first call, since overlayIdx's arithmetic below is a
// pure mod-overlayCount walk). Fails closed to overlayOrder[0] on a
// struct-copied receiver, same posture as every other guarded setter
// (Pan, MoveCursor, ...): the write is silently dropped rather than
// applied to the copy's aliased-at-construction-time state.
func (m *MapScreen) CycleOverlay(forward bool) Overlay {
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CycleOverlay"}); err != nil {
		return overlayOrder[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CycleOverlay"}); err != nil {
		return overlayOrder[0]
	}
	n := len(overlayOrder)
	if forward {
		m.overlayIdx = (m.overlayIdx + 1) % n
	} else {
		m.overlayIdx = (m.overlayIdx - 1 + n) % n
	}
	return overlayOrder[m.overlayIdx]
}
