package compose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// attractTermsFile is FEAT-167's one new balance-data file
// (data/attract_terms.json, GR#15 — see
// docs/planning/icd/engine.attract-terms.md §3 "New data file"): every
// scale/curve constant the Safety/LeisureFit/Environment term wiring
// introduces lives here, never as a Go literal. It is NOT one of
// foundation/data's registered §24 files (that aggregate is owned
// elsewhere, foundation/data/load.go's Config/LoadAll) — compose loads and
// validates it directly, the same way engine.crime/engine.leisure/
// engine.refuse each own a package-private data file outside the §24 set.
const attractTermsFile = "attract_terms.json"

// attractTermsData is the decoded, validated shape of
// data/attract_terms.json.
type attractTermsData struct {
	// Version and Weights are read by attract.ParseWeights; they are named here
	// only so that the strict decode below knows them (unknown keys are refused).
	Version         json.RawMessage             `json:"version"`
	Weights         json.RawMessage             `json:"weights"`
	Built           attractTermsBuilt           `json:"built"`
	World           attractTermsWorld           `json:"world"`
	Environment     attractTermsEnvironment     `json:"environment"`
	Leisure         attractTermsLeisure         `json:"leisure"`
	JobAvailability attractTermsJobAvailability `json:"jobAvailability"`
	ServiceCoverage attractTermsServiceCoverage `json:"serviceCoverage"`
}

// attractTermsBuilt holds the built flags of the two migration modifiers
// (BUG-1243, Aaron 2026-11-18: hold safety and environment at neutral until
// police and refuse collection exist). A pointer so a flag missing from an
// older data file is distinguishable from a genuine false: missing means
// built, the behaviour before BUG-1243, so older data files and test data
// directories behave as they always did.
type attractTermsBuilt struct {
	Modifiers struct {
		Safety      *bool `json:"safety"`
		Environment *bool `json:"environment"`
	} `json:"modifiers"`
	// WhenUnbuilt says what an unbuilt modifier contributes (BUG-1243 option C).
	// Missing or "neutral" means exactly 0 points (the ruling as made, option A).
	WhenUnbuilt struct {
		Safety      string `json:"safety"`
		Environment string `json:"environment"`
		Comment     string `json:"comment"`
	} `json:"whenUnbuilt"`
	Comment string `json:"comment"`
}

// Modes of attractTermsBuilt.WhenUnbuilt.
const (
	// whenUnbuiltNeutral: an unbuilt modifier adds and subtracts nothing.
	whenUnbuiltNeutral = "neutral"
	// whenUnbuiltFloor: an unbuilt modifier counts when its reading is above
	// the outside-world baseline and counts as the baseline when below, so it
	// helps a young city and never hurts it (contribution weight x max(0, reading - baseline)).
	whenUnbuiltFloor = "floorAtBaseline"
)

// attractTermsWorld holds the optional outside-world baseline override
// (BUG-1243 option B). A missing key keeps the code default baselineOneAWorld
// (40), the behaviour before this key existed; a present key must be a number
// (a string or null is refused, never read as "use the default").
//
// world.aWorld is re-read from the data file on every load of a city and is
// NOT stored in a save: a save made under one value and loaded under another
// continues under the new value, deterministically.
type attractTermsWorld struct {
	AWorld  optionalNumber `json:"aWorld"`
	Comment string         `json:"comment"`
}

// optionalNumber is a JSON number that is either absent or a real number.
type optionalNumber struct {
	Set bool
	V   float64
}

// UnmarshalJSON accepts only a JSON number; null and strings are refused.
func (o *optionalNumber) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil || string(b) == "null" {
		return fmt.Errorf("not a number: %s", string(b))
	}
	o.Set, o.V = true, f
	return nil
}

// worldBaseline returns the outside-world baseline: the data value when
// present, else the code default.
func (w attractTermsWorld) worldBaseline() float64 {
	if !w.AWorld.Set {
		return baselineOneAWorld
	}
	return w.AWorld.V
}

// safetyFloored reports whether the unbuilt safety modifier is floored at the baseline.
func (b attractTermsBuilt) safetyFloored() bool { return b.WhenUnbuilt.Safety == whenUnbuiltFloor }

// environmentFloored reports whether the unbuilt environment modifier is floored at the baseline.
func (b attractTermsBuilt) environmentFloored() bool {
	return b.WhenUnbuilt.Environment == whenUnbuiltFloor
}

// modifierTerm turns one modifier reading into the pushed lever term: a built
// modifier passes its reading; an unbuilt one is neutral, or (floorAtBaseline)
// is pushed as built with max(reading, baseline), which contributes
// weight x max(0, reading - baseline) and is never negative.
func modifierTerm(reading float64, built, floored bool, aWorld float64) attract.LeverTerm {
	if built {
		return attract.LeverTerm{Value: reading, Built: true}
	}
	if floored {
		return attract.LeverTerm{Value: math.Max(reading, aWorld), Built: true}
	}
	return attract.LeverTerm{Value: reading, Built: false}
}

// safetyBuilt reports whether the safety modifier counts toward migration.
func (b attractTermsBuilt) safetyBuilt() bool {
	return b.Modifiers.Safety == nil || *b.Modifiers.Safety
}

// environmentBuilt reports whether the environment modifier counts toward migration.
func (b attractTermsBuilt) environmentBuilt() bool {
	return b.Modifiers.Environment == nil || *b.Modifiers.Environment
}

// attractTermsEnvironment holds the Environment term's half-saturation
// curve parameter (environmentTerm in compose.go).
type attractTermsEnvironment struct {
	// PollutionHalfSaturationKg is the total uncollected+disposal-backlog
	// waste (kg, summed across engine.refuse's three streams) at which the
	// Environment term reads 50/100 — the same half-saturation curve shape
	// engine.crime's own safety.halfSaturationActiveCrime (data/crime.json)
	// uses. Must be finite and > 0 (a zero or negative half-saturation
	// point makes the curve degenerate/divide-by-zero-adjacent).
	PollutionHalfSaturationKg float64 `json:"pollutionHalfSaturationKg"`
	Comment                   string  `json:"comment"`
}

// attractTermsLeisure holds the compose->leisure venue-registration
// bridge's one scale constant (registerLeisureVenues in compose.go).
type attractTermsLeisure struct {
	// FloorAtBaseline (BUG-1245, default false = today's behaviour): when true
	// the parks lever's value is never below the outside-world baseline
	// (max(reading, baseline)), so it adds nothing before the first venue and
	// a venue can only raise it, never lower it.
	FloorAtBaseline bool `json:"floorAtBaseline"`
	// BridgeVenueCapacityUnits is the weekly patronage capacity (person-
	// hours, engine.leisure's Venue.Capacity unit) the bridge assigns to
	// every completed engine.build ZoneEntertainment order. Must be > 0
	// (engine.leisure.OpenVenue itself rejects a non-positive capacity).
	BridgeVenueCapacityUnits int64  `json:"bridgeVenueCapacityUnits"`
	Comment                  string `json:"comment"`
}

// attractTermsJobAvailability holds the JobAvailability term's half-
// saturation curve parameter (jobAvailabilityTerm in servicesfirms_wire.go,
// FEAT-167 completion via docs/planning/icd/engine.firms-labourmarket.md).
type attractTermsJobAvailability struct {
	// VacancyRateHalfSaturationPerMille is the engine.firms
	// LabourMarket().VacancyRatePerMille value at which the JobAvailability
	// term reads 50/100 — the same half-saturation curve shape Environment's
	// pollutionHalfSaturationKg uses, applied here because
	// VacancyRatePerMille carries no upper bound (the labour-market ICD's
	// own doc: "there is NO upper clamp"). Must be finite and > 0.
	VacancyRateHalfSaturationPerMille float64 `json:"vacancyRateHalfSaturationPerMille"`
	Comment                           string  `json:"comment"`
}

// attractTermsServiceCoverage holds the ServiceCoverage term's scale
// parameter (serviceCoverageTerm in servicesfirms_wire.go, FEAT-167
// completion via docs/planning/icd/engine.services-coverage.md).
type attractTermsServiceCoverage struct {
	// CoverageRatioScalePercent scales engine.services'
	// CoverageSummary().CoverageRatio (already clamped [0,1], or exactly 1.0
	// when city-wide TotalDemand is zero — coverage.go's coverageRatio) onto
	// attract's [0,100] term scale: term = 100 * CoverageRatio *
	// (CoverageRatioScalePercent/100). 100 is the neutral (no-op) scale; a
	// future balance pass may tune it without a code change. Must be > 0.
	CoverageRatioScalePercent float64 `json:"coverageRatioScalePercent"`
	Comment                   string  `json:"comment"`
}

// loadAttractTermsData resolves the data/ directory (foundation/data's
// documented resolution order, data.ResolveDataDir) and reads+validates
// attract_terms.json. Every failure is a registry-sourced *errs.E under
// this package's own ErrModuleFailed (GR#7) — never a silent default
// substitution — mirroring engine.refuse/engine.leisure's own "fail loudly
// on a bad data file" discipline for a module-owned file outside the §24
// set.
func loadAttractTermsData(correlationID string) (attractTermsData, error) {
	b, _, err := readAttractTermsFile(correlationID)
	if err != nil {
		return attractTermsData{}, err
	}
	return parseAttractTermsData(b, correlationID)
}

// readAttractTermsFile reads attract_terms.json once; Wire passes the bytes to
// both the weights parser and the terms parser (BUG-1231 r2 tidy: the file was
// read twice per Wire).
func readAttractTermsFile(correlationID string) ([]byte, string, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, attractTermsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, path, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{
			"module": "attract_terms_data", "path": path,
		})
	}
	return b, path, nil
}

// parseAttractTermsData validates the bytes of attract_terms.json.
func parseAttractTermsData(b []byte, correlationID string) (attractTermsData, error) {
	var d attractTermsData
	// Unknown keys are refused: a key spelled almost right (floorAtBaselin,
	// aworld) would otherwise be ignored and the game would silently keep
	// today's behaviour, defeating the one-line data edit it was meant to be.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return attractTermsData{}, moduleFailedWrapped(correlationID, "attract_terms_data", err)
	}
	if !num.IsFinite(d.Environment.PollutionHalfSaturationKg) || d.Environment.PollutionHalfSaturationKg <= 0 {
		return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", "environment.pollutionHalfSaturationKg", d.Environment.PollutionHalfSaturationKg)
	}
	if d.Leisure.BridgeVenueCapacityUnits <= 0 {
		return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", "leisure.bridgeVenueCapacityUnits", d.Leisure.BridgeVenueCapacityUnits)
	}
	if !num.IsFinite(d.JobAvailability.VacancyRateHalfSaturationPerMille) || d.JobAvailability.VacancyRateHalfSaturationPerMille <= 0 {
		return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", "jobAvailability.vacancyRateHalfSaturationPerMille", d.JobAvailability.VacancyRateHalfSaturationPerMille)
	}
	if !num.IsFinite(d.ServiceCoverage.CoverageRatioScalePercent) || d.ServiceCoverage.CoverageRatioScalePercent <= 0 {
		return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", "serviceCoverage.coverageRatioScalePercent", d.ServiceCoverage.CoverageRatioScalePercent)
	}
	for _, m := range []struct{ f, v string }{{"built.whenUnbuilt.safety", d.Built.WhenUnbuilt.Safety}, {"built.whenUnbuilt.environment", d.Built.WhenUnbuilt.Environment}} {
		if m.v != "" && m.v != whenUnbuiltNeutral && m.v != whenUnbuiltFloor {
			return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", m.f, m.v)
		}
	}
	if d.World.AWorld.Set && (!num.IsFinite(d.World.AWorld.V) || d.World.AWorld.V < 0 || d.World.AWorld.V > 100) {
		return attractTermsData{}, moduleDataInvalid(correlationID, "attract_terms_data", "world.aWorld", d.World.AWorld.V)
	}
	return d, nil
}
