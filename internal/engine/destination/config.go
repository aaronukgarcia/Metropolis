package destination

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileDestination is data/destination.json's filename, relative to the
// resolved data directory (see data.ResolveDataDir).
const fileDestination = "destination.json"

// config is data/destination.json decoded and validated (GR#15). Unexported
// — the only way another package reaches a destination balance figure is
// through DestAPI's exported surface (GR#20).
type config struct {
	Version    int                        `json:"version"`
	SpecRef    string                     `json:"specRef"`
	Archetypes map[string]archetypeConfig `json:"archetypes"`
}

// archetypeConfig is one data/destination.json "archetypes" entry (AC-2).
type archetypeConfig struct {
	Name              string  `json:"name"`
	Jobs              int64   `json:"jobs"`
	MinFootprintHa    float64 `json:"minFootprintHa"`
	YearRoundStaying  bool    `json:"yearRoundStaying"`
	MinShopFloorspace int64   `json:"minShopFloorspace"`
	ParkingSpaces     int64   `json:"parkingSpaces"`
	BaseDrawFactor    float64 `json:"baseDrawFactor"`
	BDIHalfSaturation float64 `json:"bdiHalfSaturation"`
	BDIMaxBoost       float64 `json:"bdiMaxBoost"`
	BlightClass       string  `json:"blightClass"`
	NoiseRadiusM      int64   `json:"noiseRadiusM"`
	VisualHeightM     float64 `json:"visualHeightM"`
	VisualMagnitude   float64 `json:"visualMagnitude"`
	ScreenWallHeightM float64 `json:"screenWallHeightM"`
}

// Validate implements foundation/data.Validator for config. It runs
// schema-level checks foundation.data's generic loader cannot: that both
// §48 archetypes are declared and that every per-archetype figure is
// structural — a positive job count, a positive minimum footprint, a
// non-negative floorspace/parking count, and finite draw/blight
// coefficients. It deliberately does NOT re-state the spec's named
// magnitudes (those live in the data file, GR#15): a malformed or
// under-minimum file is rejected at load time (AC-11), never silently
// defaulted.
func (c *config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	for _, kind := range archetypeKeys {
		key := kind.String()
		ar, ok := c.Archetypes[key]
		if !ok {
			return &data.FieldError{Field: "archetypes", Rule: "missing archetype " + key}
		}
		if err := validateArchetype(key, ar); err != nil {
			return err
		}
	}
	return nil
}

// validateArchetype enforces the structural rules one archetype entry must
// satisfy. The spec's named magnitudes (job counts, footprint, floorspace,
// "colossal" parking) themselves are data values; here only shape is
// checked (AC-11/GR#15).
func validateArchetype(key string, ar archetypeConfig) error {
	bad := func(field, rule string) error {
		return &data.FieldError{Field: "archetypes." + key + "." + field, Rule: rule}
	}
	if ar.Jobs <= 0 {
		return bad("jobs", "must be positive (a missing job count is a load-time error, never a silent default)")
	}
	if !num.IsFinite(ar.MinFootprintHa) || ar.MinFootprintHa <= 0 {
		return bad("minFootprintHa", "must be finite and positive")
	}
	if ar.MinShopFloorspace < 0 {
		return bad("minShopFloorspace", "must be >= 0")
	}
	if ar.ParkingSpaces < 0 {
		return bad("parkingSpaces", "must be >= 0")
	}
	if !num.IsFinite(ar.BaseDrawFactor) || ar.BaseDrawFactor <= 0 {
		return bad("baseDrawFactor", "must be finite and positive")
	}
	if !num.IsFinite(ar.BDIHalfSaturation) || ar.BDIHalfSaturation <= 0 {
		return bad("bdiHalfSaturation", "must be finite and positive")
	}
	if !num.IsFinite(ar.BDIMaxBoost) || ar.BDIMaxBoost < 0 || ar.BDIMaxBoost >= 1 {
		return bad("bdiMaxBoost", "must be in [0,1)")
	}
	if ar.BlightClass == "" {
		return bad("blightClass", "required, must be non-empty")
	}
	if ar.NoiseRadiusM < 0 {
		return bad("noiseRadiusM", "must be >= 0")
	}
	if !num.IsFinite(ar.VisualHeightM) || ar.VisualHeightM < 0 {
		return bad("visualHeightM", "must be finite and >= 0")
	}
	if !num.IsFinite(ar.VisualMagnitude) || ar.VisualMagnitude < 0 || ar.VisualMagnitude > 1 {
		return bad("visualMagnitude", "must be in [0,1]")
	}
	if !num.IsFinite(ar.ScreenWallHeightM) || ar.ScreenWallHeightM < 0 {
		return bad("screenWallHeightM", "must be finite and >= 0")
	}
	return nil
}

// loadConfig reads and validates data/destination.json from dir via
// foundation/data's generic Load[T]. Every failure is a registry-sourced
// *errs.E wrapped under this package's own ErrMalformedConfig — never a
// silent default substitution, never a panic (GR#7, GR#15).
func loadConfig(dir, correlationID string) (config, error) {
	cfg, err := data.Load[config, *config](filepath.Join(dir, fileDestination), correlationID)
	if err != nil {
		return config{}, errs.Wrap(ErrMalformedConfig, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return cfg, nil
}
