package spaceport

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Config is engine.spaceport's runtime configuration. Every numeric field
// is data-sourced from data/spaceport.json (GR#15) and is a PLACEHOLDER
// pending Aaron's balance pass — rebalancing is a data edit, never a code
// change. No field is a float: export/prestige/draw/radius/threshold are
// int64, so the accumulations route through foundation/num's saturating
// arithmetic (GR#16, AC-12).
type Config struct {
	// CatalogueAnchor is the data/buildings.json entry id the spaceport
	// resolves to (AC-1 shape (a): the spaceport IS space_launch_complex,
	// enriched in place — never a second launch-site entry).
	CatalogueAnchor string

	// BlightClass is the anchor entry's blightClass, read from
	// data/buildings.json and reflected by the exclusion contour (AC-5).
	BlightClass string

	// BuildMonths is the multi-year build duration (§MP "multi-year
	// builds") in months. The build advances one month per Tick; it can
	// never be completed by a single command (AC-4).
	BuildMonths int64

	// LaunchCadenceMonths is the months between launches once operational.
	LaunchCadenceMonths int64

	// ExportPerLaunch is the export value (whole £) credited per fired
	// launch (AC-4).
	ExportPerLaunch int64

	// PrestigePerLaunch is the prestige points credited per fired launch
	// (AC-4/AC-7).
	PrestigePerLaunch int64

	// FdiDrawAmount / TourismDrawAmount are the demand injected into
	// engine.fdi / engine.tourism when the spaceport is built (AC-6).
	FdiDrawAmount     int64
	TourismDrawAmount int64

	// ExclusionRadius is the launch-exclusion/noise contour radius in
	// cells, centred on the site (AC-5).
	ExclusionRadius int64

	// ExclusionFactorPerMille is the land-value factor (per-mille, 1000 =
	// no blight) a cell inside the exclusion radius experiences (AC-5).
	ExclusionFactorPerMille int64

	// ExpertThreshold is the expert gate threshold, in research points,
	// on engine.education's accumulated research output (AC-2/AC-3).
	ExpertThreshold int64
}

// validate rejects an out-of-contract Config with a registry-sourced error
// (GR#7/GR#16) — never a silently-defaulted placeholder. Every numeric
// field is checked for domain; New and Load both funnel through here.
func (c Config) validate(correlationID string) error {
	if c.CatalogueAnchor == "" {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "catalogueAnchor", "rule": "required, must be non-empty",
			"cause": fmt.Sprintf("field %q %s", "catalogueAnchor", "required, must be non-empty"),
		})
	}
	if c.BlightClass == "" {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "blightClass", "rule": "required, must be non-empty",
			"cause": fmt.Sprintf("field %q %s", "blightClass", "required, must be non-empty"),
		})
	}
	if c.BuildMonths < 1 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "buildMonths", "value": c.BuildMonths, "rule": "must be >= 1",
			"cause": fmt.Sprintf("field %q %s (got %v)", "buildMonths", "must be >= 1", c.BuildMonths),
		})
	}
	if c.LaunchCadenceMonths < 1 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "launchCadenceMonths", "value": c.LaunchCadenceMonths, "rule": "must be >= 1",
			"cause": fmt.Sprintf("field %q %s (got %v)", "launchCadenceMonths", "must be >= 1", c.LaunchCadenceMonths),
		})
	}
	if c.ExportPerLaunch < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "exportValuePerLaunch", "value": c.ExportPerLaunch, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "exportValuePerLaunch", "must be >= 0", c.ExportPerLaunch),
		})
	}
	if c.PrestigePerLaunch < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "prestigePerLaunch", "value": c.PrestigePerLaunch, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "prestigePerLaunch", "must be >= 0", c.PrestigePerLaunch),
		})
	}
	if c.FdiDrawAmount < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "fdiDrawAmount", "value": c.FdiDrawAmount, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "fdiDrawAmount", "must be >= 0", c.FdiDrawAmount),
		})
	}
	if c.TourismDrawAmount < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "tourismDrawAmount", "value": c.TourismDrawAmount, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "tourismDrawAmount", "must be >= 0", c.TourismDrawAmount),
		})
	}
	if c.ExclusionRadius < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "exclusionRadiusCells", "value": c.ExclusionRadius, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "exclusionRadiusCells", "must be >= 0", c.ExclusionRadius),
		})
	}
	if c.ExclusionFactorPerMille < 0 || c.ExclusionFactorPerMille > 1000 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "exclusionLandFactorPerMille", "value": c.ExclusionFactorPerMille,
			"rule":  "must be in [0, 1000] (per-mille factor)",
			"cause": fmt.Sprintf("field %q %s (got %v)", "exclusionLandFactorPerMille", "must be in [0, 1000] (per-mille factor)", c.ExclusionFactorPerMille),
		})
	}
	if c.ExpertThreshold < 0 {
		return errs.New(ErrSpaceportDataInvalid, correlationID, map[string]any{
			"field": "expertThreshold", "value": c.ExpertThreshold, "rule": "must be >= 0",
			"cause": fmt.Sprintf("field %q %s (got %v)", "expertThreshold", "must be >= 0", c.ExpertThreshold),
		})
	}
	return nil
}
