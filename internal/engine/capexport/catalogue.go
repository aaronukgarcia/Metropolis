package capexport

import (
	"fmt"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileCapexport is data/capexport.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir). This package owns its own balance
// surface the same way engine.market owns data/market.json; the loader routes
// through foundation/data's generic Load[T] (duplicate-key / malformed-JSON /
// version-field machinery) rather than a hand-rolled decoder.
const fileCapexport = "capexport.json"

// ExportableService is the capexport-side identity of one §36 exportable
// service line. The underlying string is the stable key both the catalogue
// (data/capexport.json) and callers reference, so a line's Go identity and
// its data identity are the same value — there is no separate name-mapping
// table to drift out of sync (GR#3).
//
// The ten constants below are the ten §36 table rows this item can legally
// reach today (AC-5): refuse collection & disposal, incineration,
// toxic/hazardous waste processing, sewage & water treatment, hospital beds,
// university places, crematorium/cemetery, prison places, port transshipment,
// and fire/ambulance mutual aid. They are the STABLE KEYS, not the balance
// data — each line's unit and placeholder rate live in data/capexport.json,
// never here (GR#15). The surplus-power line (§36's eleventh row) is
// deliberately absent: it is out of scope pending BUG-058 (no registered
// engine.capexport → engine.consumption edge), see doc.go.
type ExportableService string

const (
	// ExportRefuse: refuse collection & disposal (§36, £/t).
	ExportRefuse ExportableService = "refuse"
	// ExportIncineration: incineration (§36, £/t + you keep the MWh).
	ExportIncineration ExportableService = "incineration"
	// ExportToxicWaste: toxic/hazardous waste processing (§36, best margin,
	// worst neighbour).
	ExportToxicWaste ExportableService = "toxic-waste"
	// ExportSewage: sewage & water treatment (§36, £/m³).
	ExportSewage ExportableService = "sewage"
	// ExportHospitalBeds: hospital beds (§36, £/bed-day).
	ExportHospitalBeds ExportableService = "hospital-beds"
	// ExportUniversity: university places (§36, £/student-yr).
	ExportUniversity ExportableService = "university-places"
	// ExportCrematorium: crematorium/cemetery (§36, £/service).
	ExportCrematorium ExportableService = "crematorium"
	// ExportPrisonPlaces: prison places (§36, £/prisoner-yr; ties §43).
	ExportPrisonPlaces ExportableService = "prison-places"
	// ExportPortTransship: port transshipment (§36, £/t).
	ExportPortTransship ExportableService = "port-transshipment"
	// ExportMutualAid: fire/ambulance mutual aid (§36, standby retainer).
	ExportMutualAid ExportableService = "mutual-aid"
)

// ExportableServices is the ordered set of the ten §36 lines this item
// reaches (AC-5). Ordered (a slice, not a map) so enumeration is
// deterministic (GR#21); the drift test proves it matches data/capexport.json
// exactly (weakness pattern #2 — a value duplicated across the Go enum and the
// data file must not silently diverge).
var ExportableServices = []ExportableService{
	ExportRefuse,
	ExportIncineration,
	ExportToxicWaste,
	ExportSewage,
	ExportHospitalBeds,
	ExportUniversity,
	ExportCrematorium,
	ExportPrisonPlaces,
	ExportPortTransship,
	ExportMutualAid,
}

// ExportableDef is one data/capexport.json catalogue row (AC-5): a line's
// display label, its §36 unit, and its placeholder per-unit monthly rate.
// Every field is data-sourced — no rate is a Go literal (GR#15).
type ExportableDef struct {
	// ID is the stable line key (matches an ExportableService constant).
	ID ExportableService `json:"id"`
	// Label is the display name (e.g. "Hospital beds").
	Label string `json:"label"`
	// Unit is the §36 unit string (e.g. £/bed-day) — display only.
	Unit string `json:"unit"`
	// RateMicropounds is the placeholder per-unit monthly rate, micro-pounds
	// (1 pound sterling = 1,000,000). ASM-309: a placeholder, not a spec-fixed figure.
	RateMicropounds int64 `json:"rateMicropounds"`
	// Placeholder marks the rate as a directional placeholder pending the M2
	// balance pass (mirrors the services module's PieBenchmark.Placeholder).
	Placeholder bool `json:"placeholder"`
	// SpecRef is the master-spec section this line cites.
	SpecRef string `json:"specRef"`
}

// Config is engine.capexport's runtime balance configuration (AC-5, GR#15):
// the ten §36 exportable-service catalogue rows plus the projection demand
// growth rate AC-2's crossing scenario uses. Every numeric magnitude §36
// leaves unquantified lives here, never as a Go literal.
type Config struct {
	Version int `json:"version"`
	// SpecRef is the master-spec section (§36) this file implements.
	SpecRef string `json:"specRef"`
	// ProjectionDemandGrowthPerMonth is the placeholder monthly growth rate
	// the internal-demand projection curve compounds by (ASM-309 — a
	// placeholder used to make AC-2's crossing reachable, not a spec-fixed
	// growth figure).
	ProjectionDemandGrowthPerMonth float64 `json:"projectionDemandGrowthPerMonth"`
	// Services is the ten-row catalogue (AC-5).
	Services []ExportableDef `json:"services"`
}

// Validate implements foundation/data.Validator so the generic data.Load runs
// schema validation immediately after JSON decoding. Every failure is a
// *data.FieldError naming the offending field and rule (AC-9: a malformed
// catalogue entry — missing/zero rate or an empty id/label/unit — is a
// registry-sourced load-time failure, never a zero-valued contract).
func (c *Config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if !num.IsFinite(c.ProjectionDemandGrowthPerMonth) || c.ProjectionDemandGrowthPerMonth < 0 {
		return &data.FieldError{Field: "projectionDemandGrowthPerMonth", Rule: "must be finite and >= 0"}
	}

	seen := make(map[ExportableService]bool, len(c.Services))
	for i, s := range c.Services {
		prefix := fmt.Sprintf("services[%d]", i)
		if s.ID == "" {
			return &data.FieldError{Field: prefix + ".id", Rule: "required, must be non-empty"}
		}
		if seen[s.ID] {
			return &data.FieldError{Field: prefix + ".id", Rule: fmt.Sprintf("duplicate service line id %q", s.ID)}
		}
		seen[s.ID] = true
		if s.Label == "" {
			return &data.FieldError{Field: prefix + ".label", Rule: "required, must be non-empty"}
		}
		if s.Unit == "" {
			return &data.FieldError{Field: prefix + ".unit", Rule: "required, must be non-empty"}
		}
		if s.RateMicropounds <= 0 {
			return &data.FieldError{Field: prefix + ".rateMicropounds", Rule: "must be positive"}
		}
	}
	return nil
}

// LoadCapexport reads and schema-validates data/capexport.json from dir via
// foundation/data's generic Load[T]. Every failure is a registry-sourced
// *errs.E — never a silent default substitution, never a panic.
func LoadCapexport(dir, correlationID string) (Config, error) {
	f, err := data.Load[Config, *Config](filepath.Join(dir, fileCapexport), correlationID)
	if err != nil {
		return Config{}, errs.Wrap(ErrCapexportDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return f, nil
}
