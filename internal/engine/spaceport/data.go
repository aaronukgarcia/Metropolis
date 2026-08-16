package spaceport

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §MP and resources-design-brief.md §8 leave unquantified lives
// here, never as a Go literal. AC-15 requires every numeric value to carry
// a unit and a disclosure naming it a placeholder.
const fileName = "spaceport.json"

// Quant is one data-sourced balance value plus its unit and a disclosure
// naming it a placeholder (AC-15: every numeric value carries a unit and a
// disclosure, never a bare magic number). Value is int64 — the spaceport's
// magnitudes are integer (no float monetary/prestige field, GR#16/AC-12).
type Quant struct {
	Value      int64  `json:"value"`
	Unit       string `json:"unit"`
	Disclosure string `json:"disclosure"`
}

// SpaceportData is the JSON shape of data/spaceport.json. The top-level
// $comment/meta blocks and the per-Quant disclosure fields are
// documentation for Aaron's balance pass; only Value, Unit, Disclosure and
// CatalogueAnchor are decoded. The map-like Quant fields are only ever
// ACCESSED by name (never ranged), so JSON object-key order is irrelevant
// to determinism (GR#21).
type SpaceportData struct {
	Version         int    `json:"version"`
	CatalogueAnchor string `json:"catalogueAnchor"`
	BuildMonths     Quant  `json:"buildMonths"`
	LaunchCadence   Quant  `json:"launchCadenceMonths"`
	ExportPerLaunch Quant  `json:"exportValuePerLaunch"`
	Prestige        Quant  `json:"prestigePerLaunch"`
	FdiDraw         Quant  `json:"fdiDrawAmount"`
	TourismDraw     Quant  `json:"tourismDrawAmount"`
	ExclusionRadius Quant  `json:"exclusionRadiusCells"`
	ExclusionFactor Quant  `json:"exclusionLandFactorPerMille"`
	ExpertThreshold Quant  `json:"expertThreshold"`
}

// Validate satisfies the foundation/data.Validator contract so the generic
// data.Load runs schema validation immediately after JSON decoding. It
// checks field presence and the AC-15 disclosure obligations (every numeric
// value states a unit and carries a non-empty disclosure); the per-field
// numeric domains are enforced by Config.validate after conversion (the
// same foundation.data "per-field vs semantic" split education uses).
func (d *SpaceportData) Validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if d.CatalogueAnchor == "" {
		return &data.FieldError{Field: "catalogueAnchor", Rule: "required, must be non-empty"}
	}
	// Fixed slice, not a map range, so the first violation is reported
	// deterministically regardless of decode order (GR#21).
	qs := []struct {
		field string
		q     Quant
	}{
		{"buildMonths", d.BuildMonths},
		{"launchCadenceMonths", d.LaunchCadence},
		{"exportValuePerLaunch", d.ExportPerLaunch},
		{"prestigePerLaunch", d.Prestige},
		{"fdiDrawAmount", d.FdiDraw},
		{"tourismDrawAmount", d.TourismDraw},
		{"exclusionRadiusCells", d.ExclusionRadius},
		{"exclusionLandFactorPerMille", d.ExclusionFactor},
		{"expertThreshold", d.ExpertThreshold},
	}
	for _, x := range qs {
		if x.q.Unit == "" {
			return &data.FieldError{Field: x.field + ".unit", Rule: "required — every numeric value states its unit (AC-15)"}
		}
		if x.q.Disclosure == "" {
			return &data.FieldError{Field: x.field + ".disclosure", Rule: "required — every numeric value carries a disclosure (AC-15)"}
		}
	}
	return nil
}

// config converts the decoded JSON shape into the runtime Config, taking
// the resolved anchor's blightClass from data/buildings.json.
func (d *SpaceportData) config(blightClass string) Config {
	return Config{
		CatalogueAnchor:         d.CatalogueAnchor,
		BlightClass:             blightClass,
		BuildMonths:             d.BuildMonths.Value,
		LaunchCadenceMonths:     d.LaunchCadence.Value,
		ExportPerLaunch:         d.ExportPerLaunch.Value,
		PrestigePerLaunch:       d.Prestige.Value,
		FdiDrawAmount:           d.FdiDraw.Value,
		TourismDrawAmount:       d.TourismDraw.Value,
		ExclusionRadius:         d.ExclusionRadius.Value,
		ExclusionFactorPerMille: d.ExclusionFactor.Value,
		ExpertThreshold:         d.ExpertThreshold.Value,
	}
}

// Load reads and schema-validates data/spaceport.json from dir, resolves
// the catalogue anchor against data/buildings.json (AC-1), and returns a
// ready *SpaceportAPI. correlationID is attached to every error this call
// (and the returned API's methods) construct (GR#1). Every failure is a
// registry-sourced *errs.E — never a silent default, never a panic. The
// seams are wired later via the Set* setters.
func Load(dir, correlationID string) (*SpaceportAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	path := filepath.Join(dir, fileName)
	raw, err := data.Load[SpaceportData, *SpaceportData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrSpaceportDataInvalid, correlationID, err, map[string]any{
			"dir": dir, "cause": err.Error(),
		})
	}

	buildings, err := data.LoadBuildings(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrSpaceportDataInvalid, correlationID, err, map[string]any{
			"dir": dir, "cause": err.Error(),
		})
	}
	blightClass, err := resolveAnchor(buildings, raw.CatalogueAnchor, correlationID)
	if err != nil {
		return nil, err
	}

	cfg := raw.config(blightClass)
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	return New(cfg, 0, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot
// wiring, tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*SpaceportAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// resolveAnchor finds the single data/buildings.json entry whose ID equals
// anchor, returning its blightClass. Exactly one match is required — zero
// matches (dangling anchor) or more than one (a silent second launch-site
// entry, GR#3) is a load-time ErrCatalogueAnchorUnresolved, never a silent
// default (AC-1). The loop is index-order over Entries (a JSON array, so
// order is stable) — no map iteration.
func resolveAnchor(b data.Buildings, anchor string, correlationID string) (string, error) {
	var match *data.BuildingEntry
	for i := range b.Entries {
		if b.Entries[i].ID == anchor {
			if match != nil {
				return "", errs.New(ErrCatalogueAnchorUnresolved, correlationID, map[string]any{
					"anchor": anchor, "rule": "exactly one entry must match the catalogue anchor",
				})
			}
			entry := b.Entries[i]
			match = &entry
		}
	}
	if match == nil {
		return "", errs.New(ErrCatalogueAnchorUnresolved, correlationID, map[string]any{
			"anchor": anchor, "rule": "no data/buildings.json entry matches the catalogue anchor",
		})
	}
	return match.BlightClass, nil
}
