package fiscal

// Registry error codes for engine.fiscal (MOD-066). Range: G3100-G3199,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table).
//
// The E layer (E000-E999) is fully exhausted by eleven earlier engine
// modules, and the G layer's blocks through G3000-G3099 were all claimed
// before this module landed (engine.spaceport is G3000-G3099; the wave's
// earlier claimants run G2200 engine.wellbeing … G2900 engine.worklife), so
// engine.fiscal opens G3100-G3199 under BUG-234's 2026-08-14 code-format
// widening (three digits → three-or-four). Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G31" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always current —
// no prior MET-G31xx code existed in either place. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards against
// drift.
const (
	// ErrFiscalDataInvalid: data/fiscal.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// non-positive funding target or subsidy figure, an out-of-domain curve
	// anchor, a reversed zero/full anchor pair). Load-time (GR#15/GR#17).
	ErrFiscalDataInvalid = "MET-G3100"

	// ErrUnknownCategory: a Sankey node/flow category query named a category
	// that is not one of the fixed whole-economy categories (AC-10). Never a
	// zero-value node silently returned as if it were a real, built producer.
	ErrUnknownCategory = "MET-G3101"

	// ErrCopiedValue: a FiscalAPI method was called on a struct-copied value
	// (SEC-020 family). A copied *FiscalAPI would alias its mutex/state across
	// two values, so the copy guard rejects the call.
	ErrCopiedValue = "MET-G3102"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (engine.finance / engine.tax) was invoked before that dependency was
	// wired (GR#17). Fails loudly rather than fabricating a figure.
	ErrDependencyMissing = "MET-G3103"

	// ErrInvalidFundingLevel: SetPlanningFunding was called with a level
	// outside [0,1] (negative, above 100%) or a non-finite value (AC-5).
	// Rejected, never silently clamped.
	ErrInvalidFundingLevel = "MET-G3104"

	// ErrInvalidInput: a numeric input (childcare places, benefit amount,
	// civil-service wage bill) was negative or otherwise out of domain
	// (GR#16 — money is never negative).
	ErrInvalidInput = "MET-G3105"

	// ErrFiscalOverflow: a monetary intermediate (gross wage or extra income
	// × a rate) overflowed int64, so the honest clawback/net figure cannot be
	// computed. Surfaced rather than silently returning a net that reads
	// ≈gross at a high rate (SEC-094 class).
	ErrFiscalOverflow = "MET-G3106"
)
