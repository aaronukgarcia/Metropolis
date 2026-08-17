package wellbeing

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// WellbeingAPI is code.json's "engine.wellbeing" inbound contract
// (GUID da2c5c2a-495b-43b5-b496-2b641a5ec16a, "driver-decomposed scores;
// every driver drill-through"): the two 0-100 per-citizen tracks (physical,
// mental) with causal, independently-queryable drivers, the headline
// Wellbeing = f(physical, mental, satisfaction) composite, and the four
// documented downstream-effect modifiers.
//
// The zero value is not usable; construct via [New]. A *WellbeingAPI is safe
// for concurrent use (AC-17): the config is immutable after New and the
// source fields are guarded by mu, with checkNotCopied rejecting a method
// call on a struct-copied value (SEC-020-class, mirroring engine.attract).
type WellbeingAPI struct {
	correlationID string
	cfg           WellbeingFile
	seed          uint64

	// Dependencies wired via SetSeason/SetShopping/SetTraffic/SetHealthcare/
	// SetNeighbourhood/SetPollution and read under mu. season is a required
	// dependency (AC-10); the rest degrade to a neutral delta + low
	// confidence when missing (AC-14).
	season        SeasonSource
	shopping      ShoppingSource
	traffic       TrafficSource
	healthcare    HealthcareSource
	neighbourhood NeighbourhoodSource
	pollution     PollutionSource

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer, mirroring
	// engine.attract's AttractAPI.self). Stored exactly once, in New, before
	// the value is returned to any caller.
	self atomic.Pointer[WellbeingAPI]
}

// New constructs a WellbeingAPI from a schema-validated WellbeingFile and a
// world seed (used for the deterministic per-citizen/month baseline offset,
// AC-15/AC-18). correlationID is attached to every error this call (and the
// returned API's methods) construct (GR#1). An invalid config is rejected
// with a registry-sourced error — never a silently-defaulted weight.
func New(cfg WellbeingFile, seed uint64, correlationID string) (*WellbeingAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{"cause": err.Error()})
	}
	w := &WellbeingAPI{correlationID: correlationID, cfg: cloneConfig(cfg), seed: seed}
	w.self.Store(w)
	return w, nil
}

// cloneConfig deep-copies the mutable parts of a WellbeingFile so the stored
// config can never alias caller-owned memory (SEC-157). A WellbeingFile is
// stored by value in New, which copies the struct but not the []AgeCurvePoint
// backing array, so w.cfg.Physical.AgeCurve would otherwise alias the caller's
// slice and let a post-New mutation of the caller's config silently change the
// running API's age-curve arithmetic — breaking the documented invariant "the
// config is immutable after New" (AC-17, GR#3) and GR#21 determinism. The only
// reference-typed field in the config is Physical.AgeCurve; every other field
// is a value (float64/int), so one slice clone is a full deep copy. Mirrors
// engine.attract's cloneTermInputs.
func cloneConfig(cfg WellbeingFile) WellbeingFile {
	cfg.Physical.AgeCurve = append([]AgeCurvePoint(nil), cfg.Physical.AgeCurve...)
	return cfg
}

// checkNotCopied rejects a method call on a struct-copied *WellbeingAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (w *WellbeingAPI) checkNotCopied(method string) error {
	if w.self.Load() != w {
		return errs.New(ErrCopiedValue, w.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetSeason wires the required engine.season dependency (the §9/§18
// HealthWaveModifier, AC-10).
func (w *WellbeingAPI) SetSeason(s SeasonSource) error {
	if err := w.checkNotCopied("SetSeason"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.season = s
	return nil
}

// SetShopping wires the engine.shopping seam (Diet's fresh-food share).
func (w *WellbeingAPI) SetShopping(s ShoppingSource) error {
	if err := w.checkNotCopied("SetShopping"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shopping = s
	return nil
}

// SetTraffic wires the engine.traffic seam (commute time + active travel).
func (w *WellbeingAPI) SetTraffic(s TrafficSource) error {
	if err := w.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.traffic = s
	return nil
}

// SetHealthcare wires the engine.services seam (healthcare access).
func (w *WellbeingAPI) SetHealthcare(s HealthcareSource) error {
	if err := w.checkNotCopied("SetHealthcare"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.healthcare = s
	return nil
}

// SetNeighbourhood wires the engine.world neighbourhood seam (green space
// within 400m + noise).
func (w *WellbeingAPI) SetNeighbourhood(s NeighbourhoodSource) error {
	if err := w.checkNotCopied("SetNeighbourhood"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.neighbourhood = s
	return nil
}

// SetPollution wires the engine.world pollution seam (home-cell pollution
// overlay, AC-12b).
func (w *WellbeingAPI) SetPollution(s PollutionSource) error {
	if err := w.checkNotCopied("SetPollution"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pollution = s
	return nil
}

// Attribute is the pure, deterministic attribution engine (AC-15): a
// function of (worldSeed, citizenID, month, driver inputs). It needs no
// wired dependencies — every driver input is passed explicitly. It is the
// reconstruct-on-inspect core (AC-18): callers recompute it on demand for a
// HOT, WARM or COLD citizen and always get a byte-identical result for
// identical inputs, with no durable per-citizen attribution storage.
func (w *WellbeingAPI) Attribute(citizenID uint64, month int64, in DriverInputs) (TrackAttribution, error) {
	if err := w.checkNotCopied("Attribute"); err != nil {
		return TrackAttribution{}, err
	}
	return attribute(w.cfg, w.seed, citizenID, month, in, w.correlationID, "direct")
}

// AttributeCitizen gathers the driver inputs from the wired sources (season,
// shopping, traffic, healthcare, neighbourhood, world pollution) plus the
// passed citizen record and pushed context, then runs the pure attribution
// engine. It is the composition-root entry point: the caller fetches the
// citizen record through engine.citizens' CitizensAPI and supplies the
// household/finance context it derives, while this method reaches
// engine.season and engine.world (via the seams) for the seasonal and
// neighbourhood inputs.
//
// A missing upstream record for one driver (ok=false, or an unwired
// optional source) degrades that driver to a neutral delta + a low
// confidence flag (AC-14), never a NaN or divide-by-zero in Total. A nil
// season is a hard wiring error (ErrDependencyMissing, AC-10).
func (w *WellbeingAPI) AttributeCitizen(cit citizens.Citizen, month int64, ctx ContextInputs) (TrackAttribution, error) {
	if err := w.checkNotCopied("AttributeCitizen"); err != nil {
		return TrackAttribution{}, err
	}

	// Snapshot the source pointers under the lock, then call them outside it
	// (each source holds its own lock) — mirroring engine.attract's
	// snapshotTerms lock discipline.
	w.mu.RLock()
	seasonSrc := w.season
	shoppingSrc := w.shopping
	trafficSrc := w.traffic
	healthcareSrc := w.healthcare
	neighbourhoodSrc := w.neighbourhood
	pollutionSrc := w.pollution
	w.mu.RUnlock()

	if seasonSrc == nil {
		return TrackAttribution{}, errs.New(ErrDependencyMissing, w.correlationID, map[string]any{
			"dependency": "season",
			"operation":  "AttributeCitizen",
		})
	}
	wave, err := seasonSrc.HealthWaveModifier(month)
	if err != nil {
		return TrackAttribution{}, err
	}

	corr := w.correlationID
	// sourceValue turns a source lookup into (value, confidence): ok=false
	// (or a nil source) degrades to value 0, confidence 0 (AC-14).
	sourceValue := func(fn func() (float64, bool, error)) (float64, float64, error) {
		v, ok, err := fn()
		if err != nil {
			return 0, 0, err
		}
		if !ok {
			return 0, 0, nil
		}
		return v, 1.0, nil
	}

	fresh, dietConf, err := sourceValue(func() (float64, bool, error) {
		if shoppingSrc == nil {
			return 0, false, nil
		}
		return shoppingSrc.FreshFoodShare(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	commute, commuteConf, err := sourceValue(func() (float64, bool, error) {
		if trafficSrc == nil {
			return 0, false, nil
		}
		return trafficSrc.CommuteMinutes(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	activeTravel, activeConf, err := sourceValue(func() (float64, bool, error) {
		if trafficSrc == nil {
			return 0, false, nil
		}
		return trafficSrc.ActiveTravelShare(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	healthAccess, healthConf, err := sourceValue(func() (float64, bool, error) {
		if healthcareSrc == nil {
			return 0, false, nil
		}
		return healthcareSrc.HealthcareAccess(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	green, greenConf, err := sourceValue(func() (float64, bool, error) {
		if neighbourhoodSrc == nil {
			return 0, false, nil
		}
		return neighbourhoodSrc.GreenSpace400m(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	noise, noiseConf, err := sourceValue(func() (float64, bool, error) {
		if neighbourhoodSrc == nil {
			return 0, false, nil
		}
		return neighbourhoodSrc.Noise(cit.ID, corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	pollution, pollutionConf, err := sourceValue(func() (float64, bool, error) {
		if pollutionSrc == nil {
			return 0, false, nil
		}
		return pollutionSrc.Pollution(uint32(cit.Home), corr)
	})
	if err != nil {
		return TrackAttribution{}, err
	}

	sat, err := satisfactionScore(cit, corr)
	if err != nil {
		return TrackAttribution{}, err
	}

	// Re-validate the third personality axis the gather path folds into the
	// sport-participation product (SEC-158). A caller-supplied citizens.Citizen
	// has not passed citizens.ValidateCitizen, and unlike the ambition and
	// sociability axes — re-validated 0-100 in validateDriverInputs via their
	// DriverInputs fields — physicality is folded into SportParticipation
	// before any range check, so an out-of-domain physicality (e.g. 200) with a
	// 0.5 venue access would silently produce an in-domain SportParticipation of
	// 1.0 and grant a larger-than-valid benefit. Reject, never fold (same class
	// as the SEC-139 non-wrap sibling).
	physicality := float64(cit.Personality[citizens.AxisPhysicality])
	if physicality < 0 || physicality > 100 {
		return TrackAttribution{}, invalid(corr, "physicality", physicality)
	}

	in := DriverInputs{
		AgeMonths:            cit.Age(),
		HealthcareAccess:     healthAccess,
		FreshFoodShare:       fresh,
		ActiveTravelShare:    activeTravel,
		PollutionExposure:    pollution,
		SportParticipation:   (physicality / 100.0) * clamp01(ctx.SportVenueAccess),
		SeasonalHealthWave:   wave,
		CommuteMinutes:       commute,
		JobAmbition:          float64(cit.Personality[citizens.AxisAmbition]),
		EmploymentState:      cit.Employment.State,
		Sector:               cit.Employment.Sector,
		GreenSpace400m:       green,
		LeisureFit:           ctx.LeisureFit,
		PersonsPerRoom:       ctx.PersonsPerRoom,
		Sociability:          float64(cit.Personality[citizens.AxisSociability]),
		CommunityVenueAccess: ctx.CommunityVenueAccess,
		NoiseExposure:        noise,
		RentBurden:           (citizens.Household{}).RentBurdenRatio(ctx.MonthlyRentMicroPounds, ctx.MonthlyIncomeMicroPounds),
		UnemploymentMonths:   ctx.UnemploymentMonths,
		Satisfaction:         sat,
	}

	attr, err := attribute(w.cfg, w.seed, cit.ID, month, in, corr, "")
	if err != nil {
		return TrackAttribution{}, err
	}

	// Stamp the real per-driver source + confidence (the pure engine marks
	// "direct"/1.0; the gather path corrects each to its actual upstream).
	attr.Physical.HealthcareAccess.Source = "engine.services"
	attr.Physical.HealthcareAccess.Confidence = healthConf
	attr.Physical.Diet.Source = "engine.shopping"
	attr.Physical.Diet.Confidence = dietConf
	attr.Physical.ActiveTravel.Source = "engine.traffic"
	attr.Physical.ActiveTravel.Confidence = activeConf
	attr.Physical.PollutionExposure.Source = "engine.world"
	attr.Physical.PollutionExposure.Confidence = pollutionConf
	attr.Physical.AgeCurve.Source = "engine.citizens"
	attr.Physical.SportParticipation.Source = "engine.citizens"
	attr.Mental.CommuteTime.Source = "engine.traffic"
	attr.Mental.CommuteTime.Confidence = commuteConf
	attr.Mental.JobAmbitionMismatch.Source = "engine.citizens"
	attr.Mental.GreenSpace400m.Source = "engine.world"
	attr.Mental.GreenSpace400m.Confidence = greenConf
	attr.Mental.LeisureFit.Source = "engine.leisure"
	attr.Mental.Crowding.Source = "engine.citizens"
	attr.Mental.Isolation.Source = "engine.citizens"
	attr.Mental.Noise.Source = "engine.world"
	attr.Mental.Noise.Confidence = noiseConf
	attr.Mental.FinancialStress.Source = "engine.citizens"
	attr.Mental.UnemploymentDuration.Source = "engine.citizens"
	return attr, nil
}

// satisfactionScore is the 0-100 satisfaction input to the headline
// composite: the mean of engine.citizens' five satisfaction components
// (housing/services/environment/leisure-fit/commute, §5.1/AC-8).
//
// The gather path receives a caller-supplied citizens.Citizen that has not
// passed citizens.ValidateCitizen, so each component is re-validated against
// its 0-100 contract here (SEC-139): an out-of-domain component is rejected
// with a registry-sourced error rather than silently folded into a mean that
// happens to land in-domain. The sum is accumulated in int64 — five int32
// components each up to MaxInt32 overflow an int32 running sum, and a
// narrower accumulator would wrap before the mean is computed (SEC-139
// class: an integer accumulator narrower than the values it sums).
func satisfactionScore(cit citizens.Citizen, correlationID string) (float64, error) {
	var sum int64
	for i := 0; i < citizens.NumSatisfactionComponents; i++ {
		v := cit.Satisfaction[i]
		if v < 0 || v > 100 {
			return 0, errs.New(ErrInvalidInput, correlationID, map[string]any{
				"field": "satisfaction",
				"index": i,
				"value": v,
			})
		}
		sum += int64(v)
	}
	return float64(sum) / float64(citizens.NumSatisfactionComponents), nil
}

// Wellbeing is the §18 headline composite f(physical, mental, satisfaction):
// a data-weighted combination of the two tracks plus the satisfaction score
// (AC-8). Each input is independently movable — changing one with the others
// held fixed changes the result.
func (w *WellbeingAPI) Wellbeing(physical, mental, satisfaction float64) float64 {
	if err := w.checkNotCopied("Wellbeing"); err != nil {
		return 0
	}
	return wellbeingScore(w.cfg, physical, mental, satisfaction)
}

// MortalityModifier is the §18 mortality-hazard multiplier as a function of
// the two tracks: 1.0 at perfect health, rising as the tracks worsen (AC-9).
func (w *WellbeingAPI) MortalityModifier(physical, mental float64) float64 {
	if err := w.checkNotCopied("MortalityModifier"); err != nil {
		return 0
	}
	return mortalityModifier(w.cfg, physical, mental)
}

// ProductivityModifier is the §18 productivity multiplier: 1.0 at perfect
// health, falling as the tracks worsen (AC-9).
func (w *WellbeingAPI) ProductivityModifier(physical, mental float64) float64 {
	if err := w.checkNotCopied("ProductivityModifier"); err != nil {
		return 0
	}
	return productivityModifier(w.cfg, physical, mental)
}

// SatisfactionModifier is the §18 satisfaction multiplier: 1.0 at perfect
// health, falling as the tracks worsen (AC-9).
func (w *WellbeingAPI) SatisfactionModifier(physical, mental float64) float64 {
	if err := w.checkNotCopied("SatisfactionModifier"); err != nil {
		return 0
	}
	return satisfactionModifier(w.cfg, physical, mental)
}

// EmigrationModifier is the §18 emigration-probability multiplier: 1.0 at
// perfect health, rising as the tracks worsen (AC-9).
func (w *WellbeingAPI) EmigrationModifier(physical, mental float64) float64 {
	if err := w.checkNotCopied("EmigrationModifier"); err != nil {
		return 0
	}
	return emigrationModifier(w.cfg, physical, mental)
}
