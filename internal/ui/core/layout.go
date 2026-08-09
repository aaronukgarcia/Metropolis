package core

// MinCols and MinRows are the minimum supported terminal size
// (UI-SPEC §1: "Minimum 120x30").
const (
	MinCols = 120
	MinRows = 30
)

// Rect is an axis-aligned pane rectangle in terminal cell coordinates.
type Rect struct {
	X, Y, W, H int
}

// BelowMinimum reports whether (w, h) is smaller than the game's
// absolute minimum terminal size (MinCols x MinRows).
func BelowMinimum(w, h int) bool {
	return w < MinCols || h < MinRows
}

// PaneSpec is a pane's identity and minimum content size, the input to
// ReflowPane. Actual pane layout (where each pane sits) is an F-screen
// concern (out of scope here per ui.core's acceptance criteria); this
// package only decides, given a pane's allotted Rect, whether it must
// collapse.
type PaneSpec struct {
	Name string
	MinW int
	MinH int
}

// PaneState is the reflow decision for one pane at one terminal size.
type PaneState struct {
	Spec PaneSpec
	Rect Rect
	// Stub is true when Rect is smaller than Spec's minimum in either
	// dimension: the pane must render its collapsed tab-stub content
	// (e.g. just its name/mnemonic) instead of its normal widgets, per
	// UI-SPEC §1: "below minimum a pane collapses to a tab stub."
	Stub bool
}

// ReflowPane decides whether spec, given the Rect it has been allotted,
// must collapse to a stub. It never mutates rect or spec and never
// panics on a degenerate (zero or negative) Rect — a pane squeezed to
// nothing is exactly the case this function exists to make safe.
func ReflowPane(rect Rect, spec PaneSpec) PaneState {
	stub := rect.W < spec.MinW || rect.H < spec.MinH
	return PaneState{Spec: spec, Rect: rect, Stub: stub}
}

// ReflowPanes applies ReflowPane to every (name -> Rect) entry in rects,
// matched against specs by PaneSpec.Name. A rect with no matching spec
// is skipped (not an error — a caller building up a partial layout
// during construction is a normal state, not a bug this function should
// flag).
func ReflowPanes(rects map[string]Rect, specs []PaneSpec) map[string]PaneState {
	byName := make(map[string]PaneSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	out := make(map[string]PaneState, len(rects))
	for name, r := range rects {
		spec, ok := byName[name]
		if !ok {
			continue
		}
		out[name] = ReflowPane(r, spec)
	}
	return out
}
