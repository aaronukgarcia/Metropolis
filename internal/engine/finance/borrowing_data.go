package finance

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is FEAT-057's GR#15 data-file contract: InstrumentTable is the
// validated, ordered view of data/borrowing_instruments.json, and
// LoadInstrumentTable is this package's self-contained loader (os.ReadFile
// + encoding/json + buildInstrumentTable — the engine.freight/engine.mining
// pattern, matching engine.firms' config.go). GR#20: a module's own data
// file is loaded without importing an unregistered edge; engine.finance
// predates the foundation.data convention and has no registered
// foundation.data outbound edge (ASM-769/ES-2), so this loader follows the
// firms precedent rather than adding one.
//
// Every tunable number this taxonomy consumes — the IMF/government rate
// ranges, the IMF availability threshold, the revenue-share percentage
// bound, and the PFI deferred-capex fraction / unitary-charge multiplier /
// minimum term — comes from the data file, never a Go literal (GR#15).
// Each entry carries a non-empty disclosure field naming it a placeholder
// pending Aaron's balance pass (the balance-number regime, AC-3). Loading
// is all-or-nothing: any failure returns ErrInvalidBorrowingInstrument and
// no table.

// FileBorrowingInstruments is data/borrowing_instruments.json's filename,
// relative to the resolved data directory (see the composition root's data
// directory resolution).
const FileBorrowingInstruments = "borrowing_instruments.json"

// rawBorrowingData is data/borrowing_instruments.json's JSON wire shape,
// decoded only to be validated and folded into an InstrumentTable.
type rawBorrowingData struct {
	Version      int                  `json:"version"`
	Sources      map[string]rawSource `json:"sources"`
	RevenueShare rawRevenueShare      `json:"revenueShare"`
	PFI          rawPFI               `json:"pfi"`
}

type rawSource struct {
	Name         string          `json:"name"`
	Disclosure   string          `json:"disclosure"`
	Availability rawAvailability `json:"availability"`
	Secured      rawRateRange    `json:"secured"`
	Unsecured    rawRateRange    `json:"unsecured"`
}

type rawAvailability struct {
	Mode           string `json:"mode"`
	MaxCreditScore int64  `json:"maxCreditScore"`
	Disclosure     string `json:"disclosure"`
}

type rawRateRange struct {
	MinBp      int64  `json:"minBp"`
	MaxBp      int64  `json:"maxBp"`
	Disclosure string `json:"disclosure"`
}

type rawRevenueShare struct {
	MaxSharePermille int64  `json:"maxSharePermille"`
	Disclosure       string `json:"disclosure"`
}

type rawPFI struct {
	UpfrontCapexFractionPermille  int64  `json:"upfrontCapexFractionPermille"`
	UnitaryChargeMonthlyBp        int64  `json:"unitaryChargeMonthlyBp"`
	MinimumTermMonths             int64  `json:"minimumTermMonths"`
	LockInMode                    string `json:"lockInMode"`
	EarlyTerminationPenaltyMonths int64  `json:"earlyTerminationPenaltyMonths"`
	Disclosure                    string `json:"disclosure"`
}

// LoadInstrumentTable reads, decodes and validates
// data/borrowing_instruments.json from path, returning the table or
// ErrInvalidBorrowingInstrument. Every failure is a registry-sourced
// *errs.E — never a panic, never a silently-defaulted entry (GR#17).
func LoadInstrumentTable(path, correlationID string) (InstrumentTable, error) {
	var zero InstrumentTable
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrInvalidBorrowingInstrument, correlationID, err, map[string]any{
			"path": path, "cause": err.Error(),
		})
	}
	var raw rawBorrowingData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrInvalidBorrowingInstrument, correlationID, err, map[string]any{
			"path": path, "cause": err.Error(),
		})
	}
	return buildInstrumentTable(raw, path, correlationID)
}

// buildInstrumentTable validates a decoded rawBorrowingData and folds it
// into an ordered InstrumentTable. Kept separate (like firms' buildConfig)
// so tests can drive it with synthetic raw structs.
func buildInstrumentTable(raw rawBorrowingData, path, correlationID string) (InstrumentTable, error) {
	fail := func(field, rule string) (InstrumentTable, error) {
		return InstrumentTable{}, errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"path": path, "field": field, "rule": rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Sources: both imf and government required, in a fixed order (AC-1).
	table := InstrumentTable{Sources: make(map[LoanSource]SourceSpec, len(raw.Sources))}
	for _, key := range []LoanSource{LoanSourceGovernment, LoanSourceIMF} {
		rs, ok := raw.Sources[string(key)]
		if !ok {
			return fail("sources."+string(key), "required source missing")
		}
		if rs.Name == "" {
			return fail("sources."+string(key)+".name", "required, must be non-empty")
		}
		if rs.Disclosure == "" {
			return fail("sources."+string(key)+".disclosure", "required, must be non-empty (placeholder disclosure)")
		}
		if rs.Availability.Disclosure == "" {
			return fail("sources."+string(key)+".availability.disclosure", "required, must be non-empty (placeholder disclosure)")
		}
		switch AvailabilityMode(rs.Availability.Mode) {
		case AvailabilityAlways:
		case AvailabilityBelowCreditScore:
			if rs.Availability.MaxCreditScore < 0 || rs.Availability.MaxCreditScore > int64(creditScoreMax) {
				return fail("sources."+string(key)+".availability.maxCreditScore", "must be in [0, 1000]")
			}
		default:
			return fail("sources."+string(key)+".availability.mode", "must be 'always' or 'belowCreditScore'")
		}
		sec, err := buildRateRange(rs.Secured, "sources."+string(key)+".secured", correlationID)
		if err != nil {
			return InstrumentTable{}, err
		}
		unsec, err := buildRateRange(rs.Unsecured, "sources."+string(key)+".unsecured", correlationID)
		if err != nil {
			return InstrumentTable{}, err
		}
		table.Sources[key] = SourceSpec{
			Name:       rs.Name,
			Disclosure: rs.Disclosure,
			Availability: AvailabilitySpec{
				Mode:           AvailabilityMode(rs.Availability.Mode),
				MaxCreditScore: CreditScore(rs.Availability.MaxCreditScore),
				Disclosure:     rs.Availability.Disclosure,
			},
			Secured:   sec,
			Unsecured: unsec,
		}
	}

	// Revenue-share bound.
	if raw.RevenueShare.MaxSharePermille < 0 || raw.RevenueShare.MaxSharePermille > permilleScale {
		return fail("revenueShare.maxSharePermille", "must be in [0, 1000] permille")
	}
	if raw.RevenueShare.Disclosure == "" {
		return fail("revenueShare.disclosure", "required, must be non-empty (placeholder disclosure)")
	}
	table.RevenueShareMaxPermille = raw.RevenueShare.MaxSharePermille

	// PFI spec.
	if raw.PFI.Disclosure == "" {
		return fail("pfi.disclosure", "required, must be non-empty (placeholder disclosure)")
	}
	if raw.PFI.MinimumTermMonths <= 0 {
		return fail("pfi.minimumTermMonths", "must be positive")
	}
	if raw.PFI.UpfrontCapexFractionPermille < 0 || raw.PFI.UpfrontCapexFractionPermille > permilleScale {
		return fail("pfi.upfrontCapexFractionPermille", "must be in [0, 1000] permille")
	}
	if raw.PFI.UnitaryChargeMonthlyBp < 0 {
		return fail("pfi.unitaryChargeMonthlyBp", "must be >= 0")
	}
	switch raw.PFI.LockInMode {
	case LockInEarlyTerminationPenaltyMonths, LockInNotModelled:
	default:
		return fail("pfi.lockInMode", "must be 'earlyTerminationPenaltyMonths' or 'notModelled'")
	}
	if raw.PFI.EarlyTerminationPenaltyMonths < 0 {
		return fail("pfi.earlyTerminationPenaltyMonths", "must be >= 0")
	}
	table.PFI = PFISpec{
		UpfrontCapexFractionPermille:  raw.PFI.UpfrontCapexFractionPermille,
		UnitaryChargeMonthlyBp:        raw.PFI.UnitaryChargeMonthlyBp,
		MinimumTermMonths:             raw.PFI.MinimumTermMonths,
		LockInMode:                    raw.PFI.LockInMode,
		EarlyTerminationPenaltyMonths: raw.PFI.EarlyTerminationPenaltyMonths,
		Disclosure:                    raw.PFI.Disclosure,
	}

	return table, nil
}

// buildRateRange validates and folds one raw rate range.
func buildRateRange(r rawRateRange, field, correlationID string) (RateRange, error) {
	if r.MinBp < 0 {
		return RateRange{}, errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"field": field + ".minBp", "rule": "must be >= 0",
		})
	}
	if r.MaxBp < r.MinBp {
		return RateRange{}, errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"field": field + ".maxBp", "rule": "must be >= minBp",
		})
	}
	if r.Disclosure == "" {
		return RateRange{}, errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"field": field + ".disclosure", "rule": "required, must be non-empty (placeholder disclosure)",
		})
	}
	return RateRange{MinBp: BasisPoints(r.MinBp), MaxBp: BasisPoints(r.MaxBp), Disclosure: r.Disclosure}, nil
}
