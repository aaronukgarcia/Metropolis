package wellbeing

// Registry error codes for engine.wellbeing (MOD-034). Range: G2200-G2299,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) is fully exhausted, and every G-layer block
// through G2100-G2199 was claimed by the twenty earlier engine modules by
// the time this module landed (engine.citizens … engine.refuse, engine.rail,
// feat.containerport at G2000-G2099, and engine.leisure at G2100-G2199), so
// engine.wellbeing opens G2200-G2299 as the next free four-digit block, per
// BUG-234's 2026-08-14 three-to-four-digit code-format widening. Checked
// against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G22" internal/ cmd/` before claiming, per BUG-008's lesson
// — no prior MET-G22xx code existed in either place. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards
// against this ever drifting out of sync.
const (
	// ErrDataInvalid: data/wellbeing.json could not be loaded or failed this
	// package's schema validation (missing file, malformed JSON, a
	// non-finite/non-positive weight, an out-of-order age curve). Load-time
	// (AC-19/GR#15).
	ErrDataInvalid = "MET-G2200"

	// ErrInvalidInput: an attribution query was constructed with an
	// out-of-domain driver input (a negative commute time, a personality
	// axis outside 0-100, a negative rent burden, or a fraction input
	// outside [0,1]). Rejected with this typed error, never silently
	// clamped and folded into the wellbeing total (AC-13).
	ErrInvalidInput = "MET-G2201"

	// ErrNonFiniteInput: an attribution query was handed a NaN or ±Inf
	// float where only a finite simulation-state value is meaningful.
	// Rejected at the boundary rather than propagated into the conserved
	// sum (SEC-093 / AC-14).
	ErrNonFiniteInput = "MET-G2202"

	// ErrDependencyMissing: AttributeCitizen was called before a required
	// dependency (engine.season) was wired. The seasonal health wave is a
	// mandatory physical component this module must source from
	// engine.season, so its absence is a wiring error, not a degradable
	// per-driver gap (AC-10).
	ErrDependencyMissing = "MET-G2203"

	// ErrCopiedValue: a WellbeingAPI method was called on a struct-copied
	// value (SEC-020 family). A copied *WellbeingAPI would alias its
	// mutex/source fields across two values, so the copy guard rejects the
	// call rather than let a torn read pass silently.
	ErrCopiedValue = "MET-G2204"

	// ErrUnknownCitizen: reserved for the citizens-lookup convenience path
	// (a future AttributeCitizen variant that resolves a citizen id through a
	// wired CitizensAPI itself). The current AttributeCitizen entry point
	// takes the citizen record directly, so this code is not yet raised.
	ErrUnknownCitizen = "MET-G2205"
)
