package capexport

// Registry error codes for engine.capexport (MOD-049). Range: G3400-G3499,
// claimed here per the Sprint-1 convention (per-module error subranges are
// claimed at build time by the owning module, not pre-allocated in a master
// table).
//
// The E layer (E000-E999) is fully exhausted by eleven earlier engine
// modules, and the G layer's blocks through G3300-G3399 were all claimed by
// earlier engine modules/features (engine.citizens … engine.worklife,
// engine.spaceport, engine.fiscal, engine.maintenance, engine.comms), so
// engine.capexport opens G3400-G3499 under BUG-234's 2026-08-14 code-format
// widening (three digits → three-or-four). Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G34" internal/ cmd/` before
// claiming, per BUG-008's lesson that the table alone is not always current —
// no prior MET-G34xx code existed in either place. Every code below IS
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the internal/foundation/errs source-scan test guards against
// drift.
const (
	// ErrCapexportDataInvalid: data/capexport.json could not be loaded or
	// failed this package's schema validation (missing file, malformed JSON,
	// a non-positive rate, an empty id/label/unit, a non-finite projection
	// growth rate, or a duplicate line id). Load-time (AC-5, GR#15).
	ErrCapexportDataInvalid = "MET-G3400"

	// ErrUnknownServiceLine: a query or command named an ExportableService id
	// that is not one of the catalogue's registered lines. Never a zero-value
	// surplus book silently returned for a line that does not exist (AC-5).
	ErrUnknownServiceLine = "MET-G3401"

	// ErrInsufficientSurplus: IssueContract was asked to sell more than the
	// line's current exportable slack (capacity − internal demand − already
	// committed). Rejected at issue time (AC-8) — never a contract that
	// silently oversells and only fails later at the crossing.
	ErrInsufficientSurplus = "MET-G3402"

	// ErrInvalidContract: an operation (PayCancellationPenalty, AccrueRevenue)
	// addressed a contract id that was never issued, is already cancelled, has
	// ended (no remaining term), or has no un-accrued months left to post.
	// Rejected (AC-9) — never a silent no-op and never a zero-valued contract.
	ErrInvalidContract = "MET-G3403"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (engine.services / engine.finance / engine.projections) was invoked
	// before that dependency was wired (GR#17). Fails loudly rather than
	// fabricating a surplus figure, a ledger posting, or a projection.
	ErrDependencyMissing = "MET-G3404"

	// ErrCopiedValue: a CapExportAPI method was called on a struct-copied
	// value (SEC-020 family). A copied *CapExportAPI would alias its
	// mutex/maps across two values, so the copy guard rejects the call.
	ErrCopiedValue = "MET-G3405"

	// ErrInvalidContractInput: IssueContract/AccrueRevenue/SetMonth was handed
	// an out-of-domain input — a non-positive quantity/term/rate, a negative
	// rate, a non-monotonic month, or a non-finite quantity (GR#16). Rejected,
	// never silently clamped.
	ErrInvalidContractInput = "MET-G3406"

	// ErrNoCrossing: CutInternalService was invoked for a line whose internal
	// demand has not (yet) crossed its committed capacity — there is no
	// shortfall to cut, so the choice is not offered (AC-3). Never a cut that
	// silently reduces citizens' coverage by zero.
	ErrNoCrossing = "MET-G3407"

	// ErrNoBackingService: a surplus/coverage query addressed an exportable
	// line that has no engine.services instance bound via BindServiceLine
	// (GR#20 — capacity/demand is always sourced through ServicesAPI, never
	// invented). A line in the catalogue but not yet bound has no surplus
	// book to report.
	ErrNoBackingService = "MET-G3408"
)
