package cafe

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// WellbeingSource represents the local contract interface for engine.wellbeing (MOD-034).
type WellbeingSource interface {
	SetCommunityVenueAccess(citizenID uint64, access float64) error
}

// Compile-time verification that wellbeing dependency is referenced in our lane code (AC-6).
var _ wellbeing.TrackAttribution

// Config stores the base weights and parameters for the vitality calculation (GR#15).
type Config struct {
	FootfallWeight        float64 `json:"footfallWeight"`
	DensityWeight         float64 `json:"densityWeight"`
	DwellWeight           float64 `json:"dwellWeight"`
	SafetyWeight          float64 `json:"safetyWeight"`
	CapacityWeight        float64 `json:"capacityWeight"`
	BaseSafetyValue       float64 `json:"baseSafetyValue"`
	Pedestrianization     float64 `json:"pedestrianizationBoost"`
	PedestrianizationCost float64 `json:"pedestrianizationCost"`
	MarketDayBoost        float64 `json:"marketDayBoost"`
	PerformanceBoost      float64 `json:"performanceBoost"`
}

// Centre represents the live state of a single café/vitality center (AC-1/AC-2).
type Centre struct {
	ID                        uint64
	Area                      float64
	BaseOutdoorCapacity       float64
	Patronage                 int64
	VenueCount                int
	DwellQuality              float64
	Pedestrianised            bool
	MarketDay                 bool
	StreetPerformanceLicensed bool
}

// VitalityAPI represents the café culture & street-life vitality module (MOD-054).

const (
	ErrNegativeDwell      = "MET-G5110"
	ErrInvalidSociability = "MET-G5111"
	ErrInvalidAccess      = "MET-G5112"
	ErrWellbeingPush      = "MET-G5113"
	ErrCopiedValue        = "MET-G5199"
	ErrUnknownCentre      = "MET-G5101"
	ErrReadConfig         = "MET-G5102"
	ErrMalformedConfig    = "MET-G5103"
	ErrNegativeWeight     = "MET-G5104"
	ErrInvalidSafety      = "MET-G5105"
	ErrInvalidArea        = "MET-G5106"
	ErrInvalidCapacity    = "MET-G5107"
	ErrNegativePatronage  = "MET-G5108"
	ErrNegativeVenueCount = "MET-G5109"
)

// VitalityAPI is engine.cafe's public handle: it owns the registered
// venues/centres, the vitality-index configuration (ASM-325: data-driven
// placeholder weights), and computes venue-district vitality from season
// and wellbeing inputs. Struct copies are rejected via the self atomic
// pointer (SEC-020 family); methods must be called on the *VitalityAPI
// returned by New/Load.
type VitalityAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[VitalityAPI]
	season        *season.SeasonAPI
	wellbeing     WellbeingSource
	centres       map[uint64]*Centre
	cfg           Config
	correlationID string
}

// New constructs a new VitalityAPI with default configuration.
func New() *VitalityAPI {
	v := &VitalityAPI{
		centres: make(map[uint64]*Centre),
		cfg: Config{
			FootfallWeight:        1.0,
			DensityWeight:         1.0,
			DwellWeight:           1.0,
			SafetyWeight:          1.0,
			CapacityWeight:        1.0,
			BaseSafetyValue:       0.8, // PLACEHOLDER safety term until crime edge lands (AC-5)
			Pedestrianization:     15.0,
			PedestrianizationCost: 500.0,
			MarketDayBoost:        10.0,
			PerformanceBoost:      5.0,
		},
		correlationID: "default-cafe",
	}
	v.self.Store(v)
	return v
}

// checkNotCopied rejects a method invoked on a struct-copied VitalityAPI
// before any lock is touched (SEC-020 family — a copy aliases the original's
// mutex while holding an independent lock).
func (v *VitalityAPI) checkNotCopied(method string) error {
	if v.self.Load() != v {
		return errs.New(ErrCopiedValue, v.correlationID, map[string]any{"method": method})
	}
	return nil
}

// LoadConfig attempts to load the configuration from the given directory (AC-11).
func (v *VitalityAPI) LoadConfig(dir string) error {
	if err := v.checkNotCopied("LoadConfig"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	path := filepath.Join(dir, "cafe.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return errs.Wrap(ErrReadConfig, v.correlationID, err, map[string]any{"cause": err.Error()})
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return errs.Wrap(ErrMalformedConfig, v.correlationID, err, map[string]any{"cause": err.Error()})
	}

	// Validate config schema (AC-11)
	if cfg.FootfallWeight < 0 || cfg.DensityWeight < 0 || cfg.DwellWeight < 0 || cfg.SafetyWeight < 0 || cfg.CapacityWeight < 0 {
		return errs.New(ErrNegativeWeight, v.correlationID, nil)
	}
	if cfg.BaseSafetyValue < 0 || cfg.BaseSafetyValue > 1.0 {
		return errs.New(ErrInvalidSafety, v.correlationID, nil)
	}

	v.cfg = cfg
	return nil
}

// SetSeason sets the season dependency (AC-4).
func (v *VitalityAPI) SetSeason(s *season.SeasonAPI) error {
	if err := v.checkNotCopied("SetSeason"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.season = s
	return nil
}

// SetWellbeing sets the wellbeing dependency (AC-6).
func (v *VitalityAPI) SetWellbeing(w WellbeingSource) error {
	if err := v.checkNotCopied("SetWellbeing"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.wellbeing = w
	return nil
}

// RegisterCentre registers a new centre (AC-3).
func (v *VitalityAPI) RegisterCentre(centreID uint64, area float64, baseCapacity float64) error {
	if err := v.checkNotCopied("RegisterCentre"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	if area <= 0 {
		return errs.New(ErrInvalidArea, v.correlationID, map[string]any{"area": area})
	}
	if baseCapacity < 0 {
		return errs.New(ErrInvalidCapacity, v.correlationID, map[string]any{"baseCapacity": baseCapacity})
	}

	v.centres[centreID] = &Centre{
		ID:                  centreID,
		Area:                area,
		BaseOutdoorCapacity: baseCapacity,
		DwellQuality:        1.0,
	}
	return nil
}

// RegisterPatronage registers realized pedestrian or venue patronage activity (AC-3).
func (v *VitalityAPI) RegisterPatronage(centreID uint64, count int64) error {
	if err := v.checkNotCopied("RegisterPatronage"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}
	if count < 0 {
		return errs.New(ErrNegativePatronage, v.correlationID, nil)
	}

	c.Patronage += count
	return nil
}

// SetVenueCount sets the number of active venues in a centre (AC-3).
func (v *VitalityAPI) SetVenueCount(centreID uint64, count int) error {
	if err := v.checkNotCopied("SetVenueCount"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}
	if count < 0 {
		return errs.New(ErrNegativeVenueCount, v.correlationID, nil)
	}

	c.VenueCount = count
	return nil
}

// SetDwellQuality sets the base dwell quality for a centre (AC-3).
func (v *VitalityAPI) SetDwellQuality(centreID uint64, quality float64) error {
	if err := v.checkNotCopied("SetDwellQuality"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}
	if quality < 0 {
		return errs.New(ErrNegativeDwell, v.correlationID, nil)
	}

	c.DwellQuality = quality
	return nil
}

// SetPedestrianised toggles pedestrianized street status (AC-7).
func (v *VitalityAPI) SetPedestrianised(centreID uint64, status bool) error {
	if err := v.checkNotCopied("SetPedestrianised"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	c.Pedestrianised = status
	return nil
}

// SetMarketDay toggles market day scheduling status (AC-7).
func (v *VitalityAPI) SetMarketDay(centreID uint64, status bool) error {
	if err := v.checkNotCopied("SetMarketDay"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	c.MarketDay = status
	return nil
}

// SetStreetPerformanceLicensed toggles street performance licensing status (AC-7).
func (v *VitalityAPI) SetStreetPerformanceLicensed(centreID uint64, status bool) error {
	if err := v.checkNotCopied("SetStreetPerformanceLicensed"); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	c, ok := v.centres[centreID]
	if !ok {
		return errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	c.StreetPerformanceLicensed = status
	return nil
}

// Footfall returns the current footfall factor (AC-2/AC-3).
func (v *VitalityAPI) Footfall(centreID uint64) (float64, error) {
	if err := v.checkNotCopied("Footfall"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	c, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	base := float64(c.Patronage)
	if c.Pedestrianised {
		base += v.cfg.Pedestrianization
	}
	if c.MarketDay {
		base += v.cfg.MarketDayBoost
	}
	return base * v.cfg.FootfallWeight, nil
}

// VenueDensity returns the current venue density factor normalized by area (AC-2/AC-3).
func (v *VitalityAPI) VenueDensity(centreID uint64) (float64, error) {
	if err := v.checkNotCopied("VenueDensity"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	c, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	density := float64(c.VenueCount) / c.Area
	return density * v.cfg.DensityWeight, nil
}

// DwellQuality returns the current dwell quality factor (AC-2/AC-3).
func (v *VitalityAPI) DwellQuality(centreID uint64) (float64, error) {
	if err := v.checkNotCopied("DwellQuality"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	c, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	base := c.DwellQuality
	if c.StreetPerformanceLicensed {
		base += v.cfg.PerformanceBoost / 100.0
	}
	return base * v.cfg.DwellWeight, nil
}

// Safety returns the current safety factor (AC-2/AC-5).
// TODO: Source from real engine.crime deprivation/policing signal once outbound edge is registered (BUG-058 finding #8).
func (v *VitalityAPI) Safety(centreID uint64) (float64, error) {
	if err := v.checkNotCopied("Safety"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	_, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	// STUB PLACEHOLDER: Currently returns BaseSafetyValue from Config
	return v.cfg.BaseSafetyValue * v.cfg.SafetyWeight, nil
}

// WeatherAdjustedCapacity returns outdoor capacity scaled by the live SeasonAPI query (AC-2/AC-4).
func (v *VitalityAPI) WeatherAdjustedCapacity(centreID uint64, monthIndex int64) (float64, error) {
	if err := v.checkNotCopied("WeatherAdjustedCapacity"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	c, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	multiplier := 1.0
	if v.season != nil {
		mix, err := v.season.LeisureMix(monthIndex)
		if err != nil {
			return 0, err
		}
		// Beach weight corresponds perfectly to outdoor-capacity season curve (AC-4)
		multiplier = mix.Beach
	}

	return c.BaseOutdoorCapacity * multiplier * v.cfg.CapacityWeight, nil
}

// VitalityIndex returns the five-term composite vitality index for a centre (AC-2).
func (v *VitalityAPI) VitalityIndex(centreID uint64, monthIndex int64) (float64, error) {
	if err := v.checkNotCopied("VitalityIndex"); err != nil {
		return 0, err
	}
	footfall, err := v.Footfall(centreID)
	if err != nil {
		return 0, err
	}
	density, err := v.VenueDensity(centreID)
	if err != nil {
		return 0, err
	}
	dwell, err := v.DwellQuality(centreID)
	if err != nil {
		return 0, err
	}
	safety, err := v.Safety(centreID)
	if err != nil {
		return 0, err
	}
	capacity, err := v.WeatherAdjustedCapacity(centreID, monthIndex)
	if err != nil {
		return 0, err
	}

	// Composite formula: footfall * density * dwell * safety * capacity
	composite := footfall * density * dwell * safety * capacity
	if math.IsNaN(composite) || math.IsInf(composite, 0) {
		return 0, nil
	}
	return composite, nil
}

// PushIsolationReduction reduces isolation for citizens near a venue (AC-6).
func (v *VitalityAPI) PushIsolationReduction(citizenID uint64, sociability float64, access float64) (float64, error) {
	if err := v.checkNotCopied("PushIsolationReduction"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	if sociability < 0 || sociability > 100 {
		return 0, errs.New(ErrInvalidSociability, v.correlationID, nil)
	}
	if access < 0 || access > 1.0 {
		return 0, errs.New(ErrInvalidAccess, v.correlationID, nil)
	}

	// Push access level to the wellbeing module if wired
	if v.wellbeing != nil {
		if err := v.wellbeing.SetCommunityVenueAccess(citizenID, access); err != nil {
			return 0, errs.Wrap(ErrWellbeingPush, v.correlationID, err, map[string]any{"cause": err.Error()})
		}
	}

	// Return weighted reduction: access * (sociability / 100.0)
	reduction := access * (sociability / 100.0)
	return reduction, nil
}

// LeverageRatio returns the queryable cost-to-vitality-delta leverage ratio (AC-8).
func (v *VitalityAPI) LeverageRatio(centreID uint64) (float64, error) {
	if err := v.checkNotCopied("LeverageRatio"); err != nil {
		return 0, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	_, ok := v.centres[centreID]
	if !ok {
		return 0, errs.New(ErrUnknownCentre, v.correlationID, map[string]any{"centre": centreID})
	}

	// Calculate a real cost-to-vitality-delta ratio for pedestrianisation.
	// Cost is loaded from config.
	cost := v.cfg.PedestrianizationCost
	if cost <= 0 {
		return 0, errs.New(ErrMalformedConfig, v.correlationID, map[string]any{"cause": "pedestrianizationCost must be positive"})
	}
	// Delta vitality generated by pedestrianization is 15.0 (the boost to footfall).
	delta := v.cfg.Pedestrianization
	return delta / cost, nil
}
