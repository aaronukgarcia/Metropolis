package compose

import (
	"encoding/json"
	"os"
	"path/filepath"

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
	Environment attractTermsEnvironment `json:"environment"`
	Leisure     attractTermsLeisure     `json:"leisure"`
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
	// BridgeVenueCapacityUnits is the weekly patronage capacity (person-
	// hours, engine.leisure's Venue.Capacity unit) the bridge assigns to
	// every completed engine.build ZoneEntertainment order. Must be > 0
	// (engine.leisure.OpenVenue itself rejects a non-positive capacity).
	BridgeVenueCapacityUnits int64  `json:"bridgeVenueCapacityUnits"`
	Comment                  string `json:"comment"`
}

// loadAttractTermsData resolves the data/ directory (foundation/data's
// documented resolution order, data.ResolveDataDir) and reads+validates
// attract_terms.json. Every failure is a registry-sourced *errs.E under
// this package's own ErrModuleFailed (GR#7) — never a silent default
// substitution — mirroring engine.refuse/engine.leisure's own "fail loudly
// on a bad data file" discipline for a module-owned file outside the §24
// set.
func loadAttractTermsData(correlationID string) (attractTermsData, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return attractTermsData{}, err
	}
	path := filepath.Join(dir, attractTermsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return attractTermsData{}, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{
			"module": "attract_terms_data", "path": path,
		})
	}
	var d attractTermsData
	if err := json.Unmarshal(b, &d); err != nil {
		return attractTermsData{}, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{
			"module": "attract_terms_data", "path": path, "cause": "malformed JSON",
		})
	}
	if !num.IsFinite(d.Environment.PollutionHalfSaturationKg) || d.Environment.PollutionHalfSaturationKg <= 0 {
		return attractTermsData{}, errs.New(ErrModuleFailed, correlationID, map[string]any{
			"module": "attract_terms_data",
			"field":  "environment.pollutionHalfSaturationKg",
			"value":  d.Environment.PollutionHalfSaturationKg,
		})
	}
	if d.Leisure.BridgeVenueCapacityUnits <= 0 {
		return attractTermsData{}, errs.New(ErrModuleFailed, correlationID, map[string]any{
			"module": "attract_terms_data",
			"field":  "leisure.bridgeVenueCapacityUnits",
			"value":  d.Leisure.BridgeVenueCapacityUnits,
		})
	}
	return d, nil
}
