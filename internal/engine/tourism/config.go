package tourism

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Config is the runtime configuration of a TourismAPI. Every numeric
// magnitude arrives from data/tourism.json via [Load] (GR#15) — never a Go
// literal in this package's source. Construct directly only in tests.
type Config struct {
	// AccessTierReach maps each access tier to its §44 reach multiplier.
	// Indexed by AccessTier (domestic=0, continental=1, global=2).
	AccessTierReach [numAccessTiers]float64

	// ReputationScale converts engine.attract's signed reputation momentum
	// into a draw multiplier via 1 + reputation/ReputationScale (clamped
	// non-negative). See the reputationMultiplier doc comment.
	ReputationScale float64

	// ReputationLagMonths is the number of months a reputation shock takes
	// to reach the draw score (AC-10 / ASM-319's lag window).
	ReputationLagMonths int

	// StayingVisitorStayMonths is the number of months a staying visitor
	// occupies a bed (the accommodation-nights denominator).
	StayingVisitorStayMonths int

	// StayingVisitorRate is visitors admitted per unit of draw score per
	// month (the draw→staying-visitor conversion).
	StayingVisitorRate float64

	// DayTripRate is day-trippers per unit of draw score per month.
	DayTripRate float64

	// PortfolioWeights is the per-term weight the composite portfolio score
	// uses (indexed by TermKind). Default 1.0 each → a plain sum.
	PortfolioWeights [numPortfolioTerms]float64

	// Accommodation is the default bed stock per kind (seeded at Load).
	// Zero is a legitimate "nothing built yet" value; the load-time guard
	// rejects only negative/invalid values (AC-16).
	Accommodation [numAccommodationKinds]int64

	// Load is the per-visitor load-signal coefficients (AC-11).
	Load LoadConfig

	// Spend is the per-visitor spend/hour figures (§44 "tourists spend into
	// shops, cafés, venues, transport").
	Spend SpendConfig
}

// SpendConfig holds the visitor-spend figures: a day-tripper's hours and
// spend (small, hours-long) versus a staying visitor's per-night spend
// (larger — the accommodation-bound stream). Placeholder v1 magnitudes.
type SpendConfig struct {
	DayTripHours               float64
	DayTripMicroPounds         int64
	StayingPerNightMicroPounds int64
}

// LoadConfig holds the volume→load coefficients for AC-11's transport,
// waste and policing signals. Day-trippers carry a large transport load
// (rail/coach/car) and a small spend-side load; staying visitors carry the
// larger waste/policing load (§44).
type LoadConfig struct {
	DayTripperTransport float64
	DayTripperWaste     float64
	DayTripperPolicing  float64
	StayingTransport    float64
	StayingWaste        float64
	StayingPolicing     float64
}

// validate rejects a non-finite or out-of-domain config field with a
// registry-sourced error and no partial state (FEAT-086: validate every
// numeric input at every entry point).
func (c Config) validate(correlationID string) error {
	for tier := AccessDomestic; int(tier) < numAccessTiers; tier++ {
		v := c.AccessTierReach[tier]
		if !num.IsFinite(v) || v <= 0 {
			return errs.New(ErrInvalidInput, correlationID, map[string]any{
				"field":  "accessTierReach." + tier.String(),
				"value":  v,
				"reason": "must be finite and positive",
			})
		}
	}
	if !num.IsFinite(c.ReputationScale) || c.ReputationScale <= 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "reputationScale",
			"value":  c.ReputationScale,
			"reason": "must be finite and positive",
		})
	}
	if c.ReputationLagMonths < 1 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "reputationLagMonths",
			"value":  c.ReputationLagMonths,
			"reason": "must be a positive integer",
		})
	}
	if c.StayingVisitorStayMonths < 1 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "stayingVisitorStayMonths",
			"value":  c.StayingVisitorStayMonths,
			"reason": "must be a positive integer",
		})
	}
	if !num.IsFinite(c.StayingVisitorRate) || c.StayingVisitorRate <= 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "stayingVisitorRate",
			"value":  c.StayingVisitorRate,
			"reason": "must be finite and positive",
		})
	}
	if !num.IsFinite(c.DayTripRate) || c.DayTripRate <= 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "dayTripRate",
			"value":  c.DayTripRate,
			"reason": "must be finite and positive",
		})
	}
	for term := TermKind(0); int(term) < numPortfolioTerms; term++ {
		w := c.PortfolioWeights[term]
		if !num.IsFinite(w) || w < 0 {
			return errs.New(ErrInvalidInput, correlationID, map[string]any{
				"field":  "portfolioWeights." + term.String(),
				"value":  w,
				"reason": "must be finite and non-negative",
			})
		}
	}
	for kind := AccommodationKind(0); int(kind) < numAccommodationKinds; kind++ {
		if c.Accommodation[kind] < 0 {
			return errs.New(ErrInvalidInput, correlationID, map[string]any{
				"field":  "accommodation." + kind.String(),
				"value":  c.Accommodation[kind],
				"reason": "must be a non-negative integer",
			})
		}
	}
	if err := c.Load.validate(correlationID); err != nil {
		return err
	}
	if !num.IsFinite(c.Spend.DayTripHours) || c.Spend.DayTripHours < 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "spend.dayTripHours",
			"value":  c.Spend.DayTripHours,
			"reason": "must be finite and non-negative",
		})
	}
	if c.Spend.DayTripMicroPounds < 0 || c.Spend.StayingPerNightMicroPounds < 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"field":  "spend",
			"value":  c.Spend,
			"reason": "spend figures must be non-negative",
		})
	}
	return nil
}

func (l LoadConfig) validate(correlationID string) error {
	fields := []struct {
		name string
		v    float64
	}{
		{"load.dayTripperTransport", l.DayTripperTransport},
		{"load.dayTripperWaste", l.DayTripperWaste},
		{"load.dayTripperPolicing", l.DayTripperPolicing},
		{"load.stayingTransport", l.StayingTransport},
		{"load.stayingWaste", l.StayingWaste},
		{"load.stayingPolicing", l.StayingPolicing},
	}
	for _, f := range fields {
		if !num.IsFinite(f.v) || f.v < 0 {
			return errs.New(ErrInvalidInput, correlationID, map[string]any{
				"field":  f.name,
				"value":  f.v,
				"reason": "must be finite and non-negative",
			})
		}
	}
	return nil
}
