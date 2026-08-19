package tourism

// Registry error codes for engine.tourism (MOD-057). Range: G4400-G4499,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table).
//
// The E layer (E000-E999) is fully exhausted by eleven earlier engine
// modules, and the G layer's blocks through G4300-G4399 were claimed before
// this module landed (engine.citizens G000-G099 … engine.policies
// G4000-G4099, engine.prison G4100-G4199/G4300-G4399, engine.fuel
// G4200-G4299), so engine.tourism opens G4400-G4499 under BUG-234's
// 2026-08-14 code-format widening (three digits → three-or-four). Checked
// against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G44" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current — no prior MET-G44xx code
// existed either place. Every code below IS registered in data/errors.json
// with real severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against drift.
const (
	// ErrTourismDataInvalid: data/tourism.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// missing/invalid access-tier reach multiplier, a negative accommodation
	// bed count, a missing/negative portfolio term weight, a non-positive
	// visitor rate, or a negative reputation scale). Load-time (AC-16) —
	// never a silent default-to-zero or default-to-unlimited capacity.
	ErrTourismDataInvalid = "MET-G4400"

	// ErrUnknownAttraction: a portfolio query named an attraction ID that was
	// never registered via AddAttraction (AC-15) — never a fabricated
	// zero-value portfolio contribution.
	ErrUnknownAttraction = "MET-G4401"

	// ErrUnknownAccommodation: an accommodation query named a facility ID
	// that was never registered (AC-15) — never a fabricated zero-bed record.
	ErrUnknownAccommodation = "MET-G4402"

	// ErrInvalidAttraction: AddAttraction was called with a zero ID, a term
	// outside the five active terms, or a non-finite/negative score.
	ErrInvalidAttraction = "MET-G4403"

	// ErrInvalidAccommodation: AddAccommodation/SetAccommodationCapacity was
	// called with a zero ID, an invalid kind, or a negative bed count.
	ErrInvalidAccommodation = "MET-G4404"

	// ErrDependencyMissing: a draw-score/venue/news operation was invoked
	// before the attract/leisure/season/news dependency was wired. Fails
	// loudly rather than fabricating a figure (GR#1/GR#17).
	ErrDependencyMissing = "MET-G4405"

	// ErrCopiedValue: a TourismAPI method was called on a struct-copied value
	// (SEC-020 family).
	ErrCopiedValue = "MET-G4406"

	// ErrInvalidInput: a numeric/enum input (month index, access tier, bed
	// count, reputation scale) was outside its documented domain.
	ErrInvalidInput = "MET-G4407"
)
