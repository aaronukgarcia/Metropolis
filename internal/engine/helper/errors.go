package helper

// Registry error codes for engine.helper (FEAT-063). Range: E700-E799,
// claimed here per docs/planning/acceptance/README.md's "per-module
// error subranges are claimed at build time" convention (mirrors
// engine.market's errors.go — E600-E699 was the last-claimed E-layer
// sub-range; E700-E799 was the next free one, checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-E7" internal/ cmd/` before claiming, per BUG-008's
// lesson that the table alone is not always current — no prior
// MET-E7xx code existed either place).
//
// Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync. The helperfixture package (this package's
// fixture-only proof package, AC-11) also constructs these same codes —
// that is expected: BUG-008's ownership rule is per NUMERIC RANGE, not
// per Go package, and helperfixture lives inside engine.helper's own
// module scope (it exists solely to prove this package's contract, per
// FEAT-063's fixture-only-proof scope).
const (
	// ErrEmptyTaxonomyID: Registry.Register was called with a Registrant
	// whose TaxonomyID() returned "" — AC-1(a) requires a stable,
	// non-empty, registry-unique identifier. Register-time.
	ErrEmptyTaxonomyID = "MET-E700"

	// ErrDuplicateTaxonomyID: Registry.Register was called with a
	// Registrant whose ActionTaxonomyID is already registered on this
	// *Registry. Register-time (AC-1a: "registry-unique").
	ErrDuplicateTaxonomyID = "MET-E701"

	// ErrRegistrationSealed: Registry.Register was called after the
	// registry has been sealed (AC-3) — either by an explicit Seal call
	// or implicitly by a prior Recommend/Preview read. The type enforces
	// this, not a doc comment (dev-team-process.md's "a comment saying
	// 'never X at runtime' is a code smell, not a control").
	ErrRegistrationSealed = "MET-E702"

	// ErrUnknownAction: Preview was called with an ActionTaxonomyID no
	// Registrant has registered with this *Registry. Query-time, never a
	// silent zero-value projection (AC-8, mirrors MarketAPI.lookup's
	// ErrUnknownCommodity precedent).
	ErrUnknownAction = "MET-E703"

	// ErrPreconditionEvalFailed: a Precondition's Evaluate genuinely
	// could not determine pass/fail against the given GameStateView
	// (AC-2) — distinct from a real "precondition not met" (false, nil).
	// Never returned as a silent false.
	ErrPreconditionEvalFailed = "MET-E704"

	// ErrMalformedStateView: a GameStateView passed to Recommend/Preview/
	// a Precondition/ProjectConsequence is missing a field the callee
	// required (AC-2). Distinct, more specific sentinel a
	// Precondition/Registrant MAY wrap ErrPreconditionEvalFailed with, or
	// raise directly when the missing-field diagnosis is precise.
	ErrMalformedStateView = "MET-E705"
)
