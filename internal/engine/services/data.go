package services

import (
	"fmt"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileServices is data/services.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir). This package owns its own
// balance surface the same way engine.market owns data/market.json and
// engine.logistics owns data/logistics.json; the loader below routes
// through foundation/data's generic Load[T] (the shared duplicate-key /
// malformed-JSON / version-field machinery) rather than a hand-rolled
// decoder, matching the MOD-020 ruling-1 precedent.
const fileServices = "services.json"

// ServicesFile is data/services.json's top-level schema (§26 + §54): the
// shared staffing pools, the Public Service Pie benchmark ratios, and the
// single v1 civil-service wage placeholder.
type ServicesFile struct {
	Version                         int            `json:"version"`
	WagePerStaffPerMonthMicropounds int64          `json:"wagePerStaffPerMonthMicropounds"`
	StaffingPools                   []StaffingPool `json:"staffingPools"`
	Pie                             PieFile        `json:"pie"`
}

// StaffingPool is one data/services.json staffingPools entry (§26): a named
// pool plus the ServiceKind ids that draw from it. The members list IS the
// data-driven "which services share this pool" configuration AC-4 requires
// — nothing about the pool→service membership is hardcoded in Go.
type StaffingPool struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Members []string `json:"members"`
	SpecRef string   `json:"specRef"`
}

// PieFile is data/services.json's pie object (§54): the benchmark ratio
// table plus the population half-point that shapes the scale-dependent
// consequence curve (AC-6).
type PieFile struct {
	SpecRef                     string         `json:"specRef"`
	SeverityHalfPointPopulation float64        `json:"severityHalfPointPopulation"`
	Benchmarks                  []PieBenchmark `json:"benchmarks"`
}

// PieBenchmark is one §54 benchmark staffing ratio. A benchmark is either
// per-1,000-population (PerThousand) or per-pupil (PerPupil) — §54 gives
// police per 1,000 and teachers per pupil, and no category uses both
// denominators. Placeholder marks the seven categories §54 names without
// a number (GR#15: those ratios are directional placeholders pending the
// M2 Batch, never invented-as-spec-fact).
type PieBenchmark struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	PerThousand float64 `json:"perThousand"`
	PerPupil    float64 `json:"perPupil"`
	Placeholder bool    `json:"placeholder"`
	SpecRef     string  `json:"specRef"`
}

// LoadServices reads and validates data/services.json from dir via
// foundation/data's generic Load[T] (which supplies the duplicate-key /
// malformed-JSON / missing-version handling for free) and returns the
// decoded, schema-validated file. Every failure is a registry-sourced
// *errs.E — never a silent default substitution, never a panic.
func LoadServices(dir, correlationID string) (ServicesFile, error) {
	f, err := data.Load[ServicesFile, *ServicesFile](filepath.Join(dir, fileServices), correlationID)
	if err != nil {
		// foundation/data.Load* already returns a registry-sourced *errs.E
		// (MET-F6xx) for a missing file, malformed JSON, or a generic
		// per-record schema violation. Re-wrap it under this package's own
		// ErrServiceDataInvalid so every Load-time failure this package's
		// callers see carries one consistent engine.services code.
		return ServicesFile{}, errs.Wrap(ErrServiceDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return f, nil
}

// Validate implements data.Validator. It enforces the module-specific
// schema rules foundation/data's generic loader has no notion of (the
// shared loader only checks version presence and JSON structure):
//
//   - a positive wage placeholder;
//   - every staffing pool carrying a non-empty id and at least one member;
//   - the severity half-point being strictly positive;
//   - every Pie benchmark carrying a non-empty id/label and at least one
//     non-negative denominator, with no benchmark using both denominators.
//
// Field-level failures are returned as *data.FieldError so the generic
// Load reports the offending field name in its registry-sourced message.
func (s *ServicesFile) Validate() error {
	if s.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if s.WagePerStaffPerMonthMicropounds <= 0 {
		return &data.FieldError{Field: "wagePerStaffPerMonthMicropounds", Rule: "must be positive"}
	}

	poolIDs := make(map[string]bool, len(s.StaffingPools))
	for i, p := range s.StaffingPools {
		prefix := fmt.Sprintf("staffingPools[%d]", i)
		if p.ID == "" {
			return &data.FieldError{Field: prefix + ".id", Rule: "required, must be non-empty"}
		}
		if poolIDs[p.ID] {
			return &data.FieldError{Field: prefix + ".id", Rule: fmt.Sprintf("duplicate pool id %q", p.ID)}
		}
		poolIDs[p.ID] = true
		if len(p.Members) == 0 {
			return &data.FieldError{Field: prefix + ".members", Rule: "must have at least one member service kind"}
		}
		for j, m := range p.Members {
			if m == "" {
				return &data.FieldError{Field: fmt.Sprintf("%s.members[%d]", prefix, j), Rule: "member must be non-empty"}
			}
		}
	}

	if s.Pie.SeverityHalfPointPopulation <= 0 {
		return &data.FieldError{Field: "pie.severityHalfPointPopulation", Rule: "must be positive"}
	}

	benchIDs := make(map[string]bool, len(s.Pie.Benchmarks))
	for i, b := range s.Pie.Benchmarks {
		prefix := fmt.Sprintf("pie.benchmarks[%d]", i)
		if b.ID == "" {
			return &data.FieldError{Field: prefix + ".id", Rule: "required, must be non-empty"}
		}
		if benchIDs[b.ID] {
			return &data.FieldError{Field: prefix + ".id", Rule: fmt.Sprintf("duplicate benchmark id %q", b.ID)}
		}
		benchIDs[b.ID] = true
		if b.Label == "" {
			return &data.FieldError{Field: prefix + ".label", Rule: "required, must be non-empty"}
		}
		if b.PerThousand < 0 || b.PerPupil < 0 {
			return &data.FieldError{Field: prefix, Rule: "perThousand/perPupil must be >= 0"}
		}
		if b.PerThousand != 0 && b.PerPupil != 0 {
			return &data.FieldError{Field: prefix, Rule: "must not use both perThousand and perPupil denominators"}
		}
		if b.PerThousand == 0 && b.PerPupil == 0 {
			return &data.FieldError{Field: prefix, Rule: "must have a non-zero perThousand or perPupil ratio"}
		}
	}

	return nil
}
