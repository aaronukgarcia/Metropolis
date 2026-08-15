package projections

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
)

// SeasonalCurveProvider is the concrete shape code.json's "engine.
// projections calls engine.season" outbound edge takes: a curve
// provider that composes a base trend with a real season.SeasonAPI-
// sourced monthly multiplier (US-2/AC-3), so a registrant with a
// seasonally-driven curve does not have to hand-roll its own
// month-to-calendar-month conversion. This package deliberately does
// NOT reimplement any of engine.season's own eight curves (Out of
// scope: "any individual system's actual curve math ... is out of
// scope") — Multiplier is supplied by the caller and simply reads
// whichever of SeasonAPI's own query methods its curve actually needs.
type SeasonalCurveProvider struct {
	// Base is the underlying (non-seasonal) trend at monthIndex.
	Base func(monthIndex int64) (float64, error)

	// Multiplier reads api at monthIndex and returns the seasonal
	// factor Base's value is scaled by — e.g.
	// func(api *season.SeasonAPI, m int64) (float64, error) {
	//     return api.PowerDemandMultiplier(m)
	// }
	Multiplier func(api *season.SeasonAPI, monthIndex int64) (float64, error)

	// Season is the SeasonAPI Multiplier queries against.
	Season *season.SeasonAPI
}

// Value implements CurveProvider: Base(monthIndex) * Multiplier(Season,
// monthIndex).
func (p SeasonalCurveProvider) Value(monthIndex int64) (float64, error) {
	base, err := p.Base(monthIndex)
	if err != nil {
		return 0, err
	}
	multiplier, err := p.Multiplier(p.Season, monthIndex)
	if err != nil {
		return 0, err
	}
	return base * multiplier, nil
}
