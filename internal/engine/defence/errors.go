package defence

// Registry error codes for engine.defence (MOD-067). Range: G3800-G3899,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table). The E layer (E000-E999) is fully exhausted, and the G layer's
// blocks through G3700-G3799 (engine.coastal) were claimed by earlier engine
// modules by the time this module landed, so engine.defence opens
// G3800-G3899 under BUG-234's three-to-four-digit code-format widening.
// Checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G38" internal/ cmd/` before claiming, per BUG-008's lesson
// that the table alone is not always current — no prior MET-G38xx code
// existed in either place. Every code below IS registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7);
// the internal/foundation/errs source-scan test guards against drift.
const (
	// ErrDefenceDataInvalid: data/defence.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// non-positive grant/facility figure, an out-of-domain win-probability
	// anchor, a mandate with fewer than two choices, or a facility type
	// referenced by a mandate/choice but absent from the facility table).
	// Load-time (GR#15/GR#17). Never a silent default substitution.
	ErrDefenceDataInvalid = "MET-G3800"

	// ErrCopiedValue: a DefenceAPI method was called on a struct-copied
	// value, not the one New/Load constructed (SEC-020 family). A copied
	// *DefenceAPI would alias its mutex/state across two values, so the copy
	// guard rejects the call.
	ErrCopiedValue = "MET-G3801"

	// ErrUndeclaredPot: a grant bid named a pot id that is not one of the
	// data/defence.json competitive pots (AC-11). Rejected loudly — never a
	// silently-accepted no-op a caller could mistake for a bid.
	ErrUndeclaredPot = "MET-G3802"

	// ErrIneligibleSite: a facility-siting command named a cell outside the
	// world grid bounds (or a build submission the ownership gate rejected)
	// (AC-11). Rejected before any facility is recorded — never a
	// silently-accepted no-op.
	ErrIneligibleSite = "MET-G3803"

	// ErrMandateAlreadyResponded: a mandate-response command was issued a
	// second time for a mandate already accepted or refused (AC-12). The
	// first response is never silently overwritten.
	ErrMandateAlreadyResponded = "MET-G3804"

	// ErrUnknownMandate: a mandate-response command named a mandate id that
	// is not one of data/defence.json's mandate events (AC-11). Rejected
	// rather than fabricating a response for a mandate that was never issued.
	ErrUnknownMandate = "MET-G3805"

	// ErrInvalidChoice: a mandate-response command named a choice that is not
	// one of the mandate's compliant choices (AC-5). The player's choice must
	// be within compliance — an out-of-set choice is rejected, never
	// auto-mapped to a default.
	ErrInvalidChoice = "MET-G3806"

	// ErrGrantRefused: a grant bid was rejected because a mandate refusal is
	// in effect (AC-6). This is the refusal-specific cost of the libertarian
	// path — distinct from ErrUndeclaredPot/ErrInvalidInput's ordinary
	// rejection, so a caller can tell "refused because you refused a
	// mandate" from "refused for a bad bid".
	ErrGrantRefused = "MET-G3807"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (engine.build / engine.finance / engine.citizens) was invoked before
	// that dependency was wired (GR#17). Fails closed rather than fabricating
	// a build/settlement/ledger figure.
	ErrDependencyMissing = "MET-G3808"

	// ErrInvalidInput: a numeric input (month, population, match funding,
	// wage-bill factor, planning quality) was negative, non-finite, or
	// otherwise out of domain (GR#16). Rejected, never silently clamped.
	ErrInvalidInput = "MET-G3809"

	// ErrNoFacility: a facility query/payroll/procurement accessor referenced
	// a FacilityID that was never built (AC-9/AC-11). Never a zero-value
	// figure returned as if a real facility stood there.
	ErrNoFacility = "MET-G3810"
)
