package fuel

// Registry-sourced error codes (GR#7) for engine.fuel (MOD-062), claimed in
// data/errors.json's ranges.reserved table under G4200-G4299 — the next free
// four-digit G-layer block after engine.tourism's G4100-G4199 (the E layer is
// fully exhausted and G000-G4199 were all claimed by earlier engine modules,
// so engine.fuel opens G4200-G4299 under BUG-234's three-to-four-digit
// widening). Checked against this table AND
// `grep -rn "MET-G42" internal/ cmd/` before claiming, per BUG-008's lesson —
// no prior MET-G42xx code existed either place. Every code below IS registered
// in data/errors.json with real severity/module/message/remedy fields; the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrFuelDataInvalid: data/fuel.json could not be loaded or failed this
	// package's schema validation (a defined era missing an EV-share figure,
	// an EV-share outside [0,1], a negative charging-load weight, an empty
	// charging profile, a non-positive duty rate, a missing tax-instrument
	// key, a non-positive forecourt target, a negative tanker throughput).
	// Load-time (AC-8) — never a silent default-to-zero that would mask a
	// data-authoring bug as "no charging demand".
	ErrFuelDataInvalid = "MET-G4200"

	// ErrCopiedValue: a FuelAPI method was called on a struct-copied value
	// (SEC-020 family, mirroring engine.tax/engine.roads).
	ErrCopiedValue = "MET-G4201"

	// ErrFuelShortage: a fuel-supply shortfall was applied (reduced tanker
	// throughput below liquid-fuel demand) with no strategic reserve and no
	// alternative supply path to cover it, stranding the fuel-dependent
	// logistics deliveries. Raised as the distinct, recorded event of an
	// unmitigated shortage (AC-7) — never a silent stall.
	ErrFuelShortage = "MET-G4202"

	// ErrUnknownEra: a fleet-composition or duty query named an era key not
	// present in data/fuel.json's "eras" list. Never a zero-value fleet
	// composition silently treated as valid.
	ErrUnknownEra = "MET-G4203"

	// ErrInvalidHour: a charging-load query used an hour index outside
	// [0, 24). The hour-of-day charging profile has exactly 24 buckets.
	ErrInvalidHour = "MET-G4204"

	// ErrLogisticsNotWired: a fuel-dependent logistics operation ran before
	// SetLogistics wired engine.logistics (GR#17). Fails closed rather than
	// fabricating a delivery answer.
	ErrLogisticsNotWired = "MET-G4205"

	// ErrTaxNotWired: a fuel-duty posting operation ran before SetTax wired
	// engine.tax (GR#17). Fails closed rather than dropping the revenue flow.
	ErrTaxNotWired = "MET-G4206"

	// ErrUnknownCommodity: a fuel-gated replenishment delivery named a
	// commodity outside the closed set of fuel-transported replenishment
	// commodities this package gates (food staples, food fresh, construction
	// materials, consumer goods). Never a silently-ungated delivery.
	ErrUnknownCommodity = "MET-G4207"

	// ErrInvalidInput: a setter or query input was outside its documented
	// domain — a negative/non-finite tanker throughput or strategic-reserve
	// level, a negative forecourt count or population. Rejected, never
	// clamped to a silently-plausible value (weakness pattern #4).
	ErrInvalidInput = "MET-G4208"
)
