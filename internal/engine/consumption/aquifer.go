package consumption

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// AquiferYield models §17's aquifer sustainable-yield ceiling (AC-8): a
// borehole source may abstract water up to a sustainable ceiling, and
// abstraction SUSTAINED above that ceiling degrades the aquifer's future
// yield, rather than the aquifer supplying unlimited water forever.
//
// A *AquiferYield is stateful (Abstract mutates current yield) and is
// intended to be owned by exactly one borehole [Source] on the water
// [Network]; that network's [Network.Solve] draws through Abstract in
// source-insertion order, so degradation is deterministic (GR#21).
type AquiferYield struct {
	sustainable   float64
	current       float64
	correlationID string
}

// NewAquiferYield constructs an aquifer whose sustainable yield (and
// initial current yield) is sustainable — the §17 ceiling, expressed in
// the water network's unit (litres per daily tick, matching the §17.1
// litres-per-day convention). A negative or non-finite yield is rejected
// with ErrInvalidAquiferYield (GR#1/GR#16): an aquifer must never yield a
// negative draw.
func NewAquiferYield(sustainable float64, correlationID string) (*AquiferYield, error) {
	if !num.IsFinite(sustainable) || sustainable < 0 {
		return nil, errs.New(ErrInvalidAquiferYield, correlationID, map[string]any{
			"value": sustainable,
		})
	}
	return &AquiferYield{
		sustainable:   sustainable,
		current:       sustainable,
		correlationID: correlationID,
	}, nil
}

// Sustainable returns the §17 sustainable-yield ceiling (never changes).
func (a *AquiferYield) Sustainable() float64 { return a.sustainable }

// Current returns the aquifer's current yield — the ceiling, or a degraded
// figure below it after sustained over-abstraction.
func (a *AquiferYield) Current() float64 { return a.current }

// Abstract draws up to requested litres for one tick, bounded by the
// current yield (an aquifer never supplies more than its current yield),
// and returns the amount actually supplied. A negative or non-finite
// request is rejected with ErrInvalidAbstraction (GR#1/GR#16) — the
// mutation counterpart of NewAquiferYield's constructor validation. If
// requested exceeds the SUSTAINABLE ceiling, the aquifer's current yield
// is degraded by overAbstractionDecay — a multiplicative factor < 1
// applied once per over-abstraction tick, so SUSTAINED over-abstraction
// compounds the degradation month over month (AC-8's "continued, not
// one-shot" requirement: abstracting once above ceiling degrades once,
// abstracting above ceiling every month keeps degrading). Abstraction at
// or below the ceiling leaves yield unchanged; recovery toward the ceiling
// is out of scope at v1 (a candidate M2 Batch tuning behaviour, not a
// Sprint-4 requirement).
func (a *AquiferYield) Abstract(requested float64) (float64, error) {
	if !num.IsFinite(requested) || requested < 0 {
		return 0, errs.New(ErrInvalidAbstraction, a.correlationID, map[string]any{
			"value": requested,
		})
	}
	drawn := math.Min(requested, a.current)
	if requested > a.sustainable {
		a.current *= overAbstractionDecay
	}
	return drawn, nil
}

// overAbstractionDecay is the §17 aquifer-degradation placeholder: the
// per-over-abstraction-tick multiplicative factor by which future yield
// shrinks. §17 states only that over-abstraction "degrades future yield"
// with no rate, so this is a plausible v1 default pending M2 Batch tuning
// (the same convention as engine.season/engine.market's unstated-number
// placeholders) — a directional figure (< 1 so degradation compounds),
// never a spec-transcribed one.
const overAbstractionDecay = 0.9
