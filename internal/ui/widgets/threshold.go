package widgets

import "github.com/gdamore/tcell/v2"

// ThresholdState is the derived state of a value against a Thresholds
// configuration: fine, approaching a limit, or over it. Widgets that
// colour by threshold (Gauge, BigNum) share this one derivation rather
// than each reinventing "is this value bad yet."
type ThresholdState int

const (
	// StateOK is the default: value has not crossed Warning.
	StateOK ThresholdState = iota
	// StateWarning: value has crossed Warning but not Danger.
	StateWarning
	// StateDanger: value has crossed Danger.
	StateDanger
)

// Thresholds configures a warning/danger pair and the direction that
// counts as "worse." HigherIsBad true means crossing upward is bad
// (e.g. junction occupancy, queue length); false means crossing
// downward is bad (e.g. a cash reserve, a stock level).
type Thresholds struct {
	Warning, Danger float64
	HigherIsBad     bool
}

// State derives value's ThresholdState. Warning and Danger need not be
// ordered by the caller in any particular direction relative to each
// other beyond matching HigherIsBad's sense — State just compares value
// against each independently, so a Danger threshold "less extreme" than
// Warning simply never lets StateWarning show (Danger wins the
// comparison first). No panic on any input, including NaN/Inf (Go's
// comparison operators handle them without crashing; NaN compares false
// to everything and falls through to StateOK, which is the correct
// "can't tell, don't alarm" degenerate behaviour for AC-11-style bad
// input).
func (t Thresholds) State(value float64) ThresholdState {
	if t.HigherIsBad {
		if value >= t.Danger {
			return StateDanger
		}
		if value >= t.Warning {
			return StateWarning
		}
		return StateOK
	}
	if value <= t.Danger {
		return StateDanger
	}
	if value <= t.Warning {
		return StateWarning
	}
	return StateOK
}

// ThresholdStyle returns base recoloured for s: StateDanger ->
// TokenDanger, StateWarning -> TokenWarning, StateOK -> base unchanged
// (no colour opinion for the fine case — callers already chose a base
// style for the "normal" rendering).
func (p Palette) ThresholdStyle(s ThresholdState, base tcell.Style) tcell.Style {
	switch s {
	case StateDanger:
		return base.Foreground(p.Color(TokenDanger))
	case StateWarning:
		return base.Foreground(p.Color(TokenWarning))
	default:
		return base
	}
}
