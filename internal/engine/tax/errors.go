package tax

// Registry error codes for engine.tax (MOD-052). Range: G1300-G1399,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed by eleven earlier engine modules, and the G layer's
// G000-G1299 was claimed by engine.citizens, engine.projections,
// engine.finance, engine.consumption, engine.logistics, engine.build,
// engine.households, engine.attract, feat.compositionroot, engine.unlocks,
// engine.freight, engine.spiral and engine.services before this module
// landed, so engine.tax is the next G-layer claimant. Checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G13" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G13xx code existed either place. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrTaxDataInvalid: data/tax_instruments.json could not be loaded or
	// failed schema validation (missing file, malformed JSON, schema
	// violation, unknown instrument ID). Load-time.
	ErrTaxDataInvalid = "MET-G1300"

	// ErrRateOutOfRange: SetRate was given a finite rate outside the
	// instrument's data-loaded rateRange (AC-11). Rejected, never clamped
	// or silently accepted; the current rate is left unchanged.
	ErrRateOutOfRange = "MET-G1301"

	// ErrUnknownInstrument: a query or mutation referenced an instrument ID
	// that is not one of the six loaded from data/tax_instruments.json
	// (AC-12). Never a zero-value instrument silently treated as valid.
	ErrUnknownInstrument = "MET-G1302"

	// ErrNonFiniteRate: SetRate was given NaN or ±Inf. A non-finite rate
	// would otherwise collapse inside the elasticity/revenue arithmetic.
	ErrNonFiniteRate = "MET-G1303"

	// ErrNegativeBase: SetBase was given a negative base. Money is never
	// negative (M0-ENG §1.2, GR#16).
	ErrNegativeBase = "MET-G1304"

	// ErrInvalidEVShare: SetEVShare was given a non-finite value or one
	// outside [0,1]. The EV-share is a base-erosion fraction.
	ErrInvalidEVShare = "MET-G1305"

	// ErrInvalidDistrictMultiplier: SetDistrictMultiplier was given an empty
	// district ID or a negative/non-finite multiplier.
	ErrInvalidDistrictMultiplier = "MET-G1306"

	// ErrUnknownZoneClass: BusinessRateRevenue received a zone class outside
	// §34's closed 8-way enum.
	ErrUnknownZoneClass = "MET-G1307"

	// ErrFinanceNotWired: a finance-dependent operation ran before
	// SetFinance (GR#17).
	ErrFinanceNotWired = "MET-G1308"

	// ErrCopiedValue: a TaxAPI method was called on a struct-copied value
	// (SEC-020-class).
	ErrCopiedValue = "MET-G1309"
)
