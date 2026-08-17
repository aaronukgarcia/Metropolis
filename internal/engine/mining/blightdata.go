package mining

import (
	"encoding/json"
	"math"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GR#15 data-file contract for the blight-model half of
// engine.mining (MOD-046): BlightConfig is the validated, ordered view of
// data/mining.json, and LoadBlightConfig is this package's loader. Every
// tunable number the general blight model and extraction siting consume —
// the noise dBA-falloff curve shape and mitigation reductions, the per-class
// noise contour radius / visual profile, the viewshed occlusion scale, eye
// height and seen-distance falloff, the tree-belt grow-in delay, the
// spoil-tip blight profile, and the extraction capacity — comes from here,
// never from a Go numeric literal (GR#15). The two balance gaps the
// acceptance doc already logged (ASM-316: noise falloff / occlusion scale;
// ASM-317: extraction capacity) are therefore a table edit, never a code
// change. Loading is all-or-nothing: any missing/malformed/schema failure
// returns ErrBlightDataInvalid and no BlightConfig (GR#7 — there is no
// partial config and no silent default substitution).
//
// The loader is self-contained (os.ReadFile + encoding/json + this file's
// validation), mirroring params.go / minetype.go, so this package consumes
// no unregistered module edge (GR#20) beyond internal/foundation/errs and
// internal/foundation/num's reject-form coercion where a data value crosses
// a numeric boundary.

// NoiseConfig is the noise (dBA-falloff) half of the blight model (AC-5).
type NoiseConfig struct {
	MinDistanceM       float64 // metres — floor so the source penalty never diverges at d=0
	FalloffExponent    float64 // power-law falloff exponent (ASM-316 curve shape)
	EnclosureReduction float64 // [0,1] — enclosure buildings cut the heard component
	NightBanReduction  float64 // [0,1] — night-working ban cuts the heard component
}

// ViewshedConfig is the elevation-based line-of-sight half (AC-4).
type ViewshedConfig struct {
	EyeHeightM      float64 // metres — the home-cell viewer height above ground
	OcclusionScaleM float64 // metres of clearance deficit that fully occludes (continuous occlusion)
	SeenFalloffM    float64 // metres — seen distance falloff scale (monotonic, not a radius)
}

// ClassProfileEntry is one blight class's data-sourced profile: the noise
// contour radius and the visual (viewshed) height + magnitude. The class is
// the lookup KEY, never a field of the entry (same distinct-facility rule as
// MineTypeParams).
type ClassProfileEntry struct {
	NoiseRadiusM  int64
	VisualHeightM float64
	Magnitude     float64 // base heard/seen penalty in (0,1]
}

// TreeBeltConfig is the tree-belt mitigation's grow-in delay (AC-7).
type TreeBeltConfig struct {
	GrowInYears int64 // full occlusion only after this many simulated years
}

// SpoilTipConfig is the deep-coal spoil tip's own blight profile (AC-3):
// the spoil tip is registered against BlightAPI as a minor blighting object.
type SpoilTipConfig struct {
	HeightM      float64
	NoiseRadiusM int64
	Class        BlightClass
}

// ExtractionConfig is the extraction-ladder capacity placeholder (ASM-317).
type ExtractionConfig struct {
	CapacityDays float64 // a site's capacity = outputRate * capacityDays (tonnes)
}

// BlightConfig is the fully-validated blight-model configuration.
type BlightConfig struct {
	Version      int
	Noise        NoiseConfig
	Viewshed     ViewshedConfig
	ClassProfile map[BlightClass]ClassProfileEntry
	TreeBelt     TreeBeltConfig
	SpoilTip     SpoilTipConfig
	Extraction   ExtractionConfig
}

// classProfile returns the data-sourced profile for class, ok=false when the
// class is not one of the four recognised ordinals.
func (c BlightConfig) classProfile(class BlightClass) (ClassProfileEntry, bool) {
	e, ok := c.ClassProfile[class]
	return e, ok
}

// validateBlightConfig re-validates a resolved BlightConfig, so a caller who
// builds the config by hand (bypassing LoadBlightConfig) is held to exactly
// the domain the loader enforces. This is the same fail-closed constructor
// guard the deposit shuffle gained for DepositParams (SEC-208): a +Inf/NaN
// occlusion scale, seen falloff, min distance, falloff exponent, grow-in
// delay, capacity days, or per-class profile value would otherwise leak
// +Inf/NaN straight into the viewshed/noise arithmetic rather than being
// rejected at the boundary (GR#16).
func validateBlightConfig(c BlightConfig, correlationID string) error {
	fail := func(field, rule string) error {
		return errs.New(ErrBlightDataInvalid, correlationID, map[string]any{"field": field, "rule": rule})
	}
	if !finitePositive(c.Noise.MinDistanceM) {
		return fail("noise.minDistanceM", "must be a finite, positive metres value")
	}
	if !finitePositive(c.Noise.FalloffExponent) {
		return fail("noise.falloffExponent", "must be a finite, positive exponent")
	}
	if !inUnitInterval(c.Noise.EnclosureReduction) {
		return fail("noise.enclosureReduction", "must be in [0,1]")
	}
	if !inUnitInterval(c.Noise.NightBanReduction) {
		return fail("noise.nightBanReduction", "must be in [0,1]")
	}
	if !finiteNonNegative(c.Viewshed.EyeHeightM) {
		return fail("viewshed.eyeHeightM", "must be a finite, non-negative metres value")
	}
	if !finitePositive(c.Viewshed.OcclusionScaleM) {
		return fail("viewshed.occlusionScaleM", "must be a finite, positive metres value")
	}
	if !finitePositive(c.Viewshed.SeenFalloffM) {
		return fail("viewshed.seenFalloffM", "must be a finite, positive metres value")
	}
	if c.TreeBelt.GrowInYears <= 0 {
		return fail("treeBelt.growInYears", "must be a positive integer")
	}
	if !finitePositive(c.SpoilTip.HeightM) {
		return fail("spoilTip.heightM", "must be a finite, positive metres value")
	}
	if c.SpoilTip.NoiseRadiusM <= 0 {
		return fail("spoilTip.noiseRadiusM", "must be a positive metres value")
	}
	if !validClass(c.SpoilTip.Class) {
		return fail("spoilTip.class", "unknown blight class")
	}
	// capacityDays is bounded to maxDataMagnitude (SEC-219): it is one factor
	// of the site-capacity product outputRate × capacityDays, so an unbounded
	// finite value overflows the product to +Inf and makes the site
	// inexhaustible (s.extracted >= s.capacity is never true).
	if !validFloat(c.Extraction.CapacityDays, 0, maxDataMagnitude) || c.Extraction.CapacityDays <= 0 {
		return fail("extraction.capacityDays", "must be a finite, positive days value at most 1e12")
	}
	for class := BlightLow; class <= BlightSevere; class++ {
		prof, ok := c.ClassProfile[class]
		if !ok {
			return fail("classProfile."+class.String(), "missing required class profile")
		}
		if prof.NoiseRadiusM <= 0 {
			return fail("classProfile."+class.String()+".noiseRadiusM", "must be a positive metres value")
		}
		if !finitePositive(prof.VisualHeightM) {
			return fail("classProfile."+class.String()+".visualHeightM", "must be a finite, positive metres value")
		}
		if prof.Magnitude <= 0 || prof.Magnitude > 1 || math.IsNaN(prof.Magnitude) || math.IsInf(prof.Magnitude, 0) {
			return fail("classProfile."+class.String()+".magnitude", "must be in (0,1]")
		}
	}
	return nil
}

// rawBlightData is the JSON wire shape of data/mining.json, decoded only to
// be validated and folded into the ordered BlightConfig above.
type rawBlightData struct {
	Version      int                        `json:"version"`
	Noise        rawNoise                   `json:"noise"`
	Viewshed     rawViewshed                `json:"viewshed"`
	ClassProfile map[string]rawClassProfile `json:"classProfile"`
	TreeBelt     rawTreeBelt                `json:"treeBelt"`
	SpoilTip     rawSpoilTip                `json:"spoilTip"`
	Extraction   rawExtraction              `json:"extraction"`
}

type rawNoise struct {
	MinDistanceM       float64 `json:"minDistanceM"`
	FalloffExponent    float64 `json:"falloffExponent"`
	EnclosureReduction float64 `json:"enclosureReduction"`
	NightBanReduction  float64 `json:"nightBanReduction"`
}

type rawViewshed struct {
	EyeHeightM      float64 `json:"eyeHeightM"`
	OcclusionScaleM float64 `json:"occlusionScaleM"`
	SeenFalloffM    float64 `json:"seenFalloffM"`
}

type rawClassProfile struct {
	NoiseRadiusM  int64   `json:"noiseRadiusM"`
	VisualHeightM float64 `json:"visualHeightM"`
	Magnitude     float64 `json:"magnitude"`
}

type rawTreeBelt struct {
	GrowInYears int64 `json:"growInYears"`
}

type rawSpoilTip struct {
	HeightM      float64 `json:"heightM"`
	NoiseRadiusM int64   `json:"noiseRadiusM"`
	Class        string  `json:"class"`
}

type rawExtraction struct {
	CapacityDays float64 `json:"capacityDays"`
}

// LoadBlightConfig reads, decodes and validates data/mining.json from path,
// returning the ordered BlightConfig or ErrBlightDataInvalid (GR#7). Every
// failure is a registry-sourced *errs.E — never a panic, never a silent
// default, never a partially-populated result.
func LoadBlightConfig(path, correlationID string) (BlightConfig, error) {
	var zero BlightConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrBlightDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawBlightData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrBlightDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	return buildBlightConfig(raw, path, correlationID)
}

// finitePositive reports whether f is a finite, strictly-positive float.
func finitePositive(f float64) bool {
	return f > 0 && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// finiteNonNegative reports whether f is finite and >= 0.
func finiteNonNegative(f float64) bool {
	return f >= 0 && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// inUnitInterval reports whether f is finite and in [0,1].
func inUnitInterval(f float64) bool {
	return f >= 0 && f <= 1 && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// buildBlightConfig folds the decoded raw data into an ordered, validated
// BlightConfig. The class-profile map is resolved to the BlightClass enum as
// its key (never a string key), so lookups are typed and canonical.
func buildBlightConfig(raw rawBlightData, path, correlationID string) (BlightConfig, error) {
	fail := func(field, rule string) (BlightConfig, error) {
		return BlightConfig{}, errs.New(ErrBlightDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if !finitePositive(raw.Noise.MinDistanceM) {
		return fail("noise.minDistanceM", "must be a finite, positive metres value")
	}
	if !finitePositive(raw.Noise.FalloffExponent) {
		return fail("noise.falloffExponent", "must be a finite, positive exponent (ASM-316 curve shape)")
	}
	if !inUnitInterval(raw.Noise.EnclosureReduction) {
		return fail("noise.enclosureReduction", "must be in [0,1]")
	}
	if !inUnitInterval(raw.Noise.NightBanReduction) {
		return fail("noise.nightBanReduction", "must be in [0,1]")
	}
	if !finiteNonNegative(raw.Viewshed.EyeHeightM) {
		return fail("viewshed.eyeHeightM", "must be a finite, non-negative metres value")
	}
	if !finitePositive(raw.Viewshed.OcclusionScaleM) {
		return fail("viewshed.occlusionScaleM", "must be a finite, positive metres value")
	}
	if !finitePositive(raw.Viewshed.SeenFalloffM) {
		return fail("viewshed.seenFalloffM", "must be a finite, positive metres value")
	}
	if raw.TreeBelt.GrowInYears <= 0 {
		return fail("treeBelt.growInYears", "must be a positive integer (the §32 5-year grow-in)")
	}
	if !finitePositive(raw.SpoilTip.HeightM) {
		return fail("spoilTip.heightM", "must be a finite, positive metres value")
	}
	if raw.SpoilTip.NoiseRadiusM <= 0 {
		return fail("spoilTip.noiseRadiusM", "must be a positive metres value")
	}
	spoilClass, ok := blightClassByName(raw.SpoilTip.Class)
	if !ok {
		return fail("spoilTip.class", "unknown blight class (want low/moderate/high/severe)")
	}
	// Bound to maxDataMagnitude (SEC-219) — one factor of the capacity product.
	if !validFloat(raw.Extraction.CapacityDays, 0, maxDataMagnitude) || raw.Extraction.CapacityDays <= 0 {
		return fail("extraction.capacityDays", "must be a finite, positive days value at most 1e12")
	}

	// The four class-profile entries are required, each in the canonical enum
	// order's own String names — resolved to the enum key, never left as
	// strings (GR#3/GR#16). The required set is derived from the enum, not a
	// second hand-maintained list.
	profiles := make(map[BlightClass]ClassProfileEntry, 4)
	for class := BlightLow; class <= BlightSevere; class++ {
		name := class.String()
		rp, ok := raw.ClassProfile[name]
		if !ok {
			return fail("classProfile."+name, "missing required class profile (low/moderate/high/severe all required)")
		}
		if rp.NoiseRadiusM <= 0 {
			return fail("classProfile."+name+".noiseRadiusM", "must be a positive metres value")
		}
		if !finitePositive(rp.VisualHeightM) {
			return fail("classProfile."+name+".visualHeightM", "must be a finite, positive metres value")
		}
		if rp.Magnitude <= 0 || rp.Magnitude > 1 || math.IsNaN(rp.Magnitude) || math.IsInf(rp.Magnitude, 0) {
			return fail("classProfile."+name+".magnitude", "must be in (0,1]")
		}
		profiles[class] = ClassProfileEntry(rp)
	}

	return BlightConfig{
		Version: raw.Version,
		Noise: NoiseConfig{
			MinDistanceM:       raw.Noise.MinDistanceM,
			FalloffExponent:    raw.Noise.FalloffExponent,
			EnclosureReduction: raw.Noise.EnclosureReduction,
			NightBanReduction:  raw.Noise.NightBanReduction,
		},
		Viewshed: ViewshedConfig{
			EyeHeightM:      raw.Viewshed.EyeHeightM,
			OcclusionScaleM: raw.Viewshed.OcclusionScaleM,
			SeenFalloffM:    raw.Viewshed.SeenFalloffM,
		},
		ClassProfile: profiles,
		TreeBelt:     TreeBeltConfig{GrowInYears: raw.TreeBelt.GrowInYears},
		SpoilTip: SpoilTipConfig{
			HeightM:      raw.SpoilTip.HeightM,
			NoiseRadiusM: raw.SpoilTip.NoiseRadiusM,
			Class:        spoilClass,
		},
		Extraction: ExtractionConfig{CapacityDays: raw.Extraction.CapacityDays},
	}, nil
}
