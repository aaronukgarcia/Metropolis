package converge

// Registry error codes for harness.converge. Range: H500-H509, claimed
// via `node tools/plan/add-error.js claim-range harness.converge --size
// 10 --layer H` and declared in data/errors.json's "ranges.reserved"
// table (GR#7 — every code below is registered there with real
// severity/module/message/remedy fields; the foundation/errs source-scan
// test guards this never drifting out of sync).
const (
	// codeFixtureLoadFailed: an I/O failure (missing file, permission
	// denied, ...) opening or reading a parity fixture file — distinct
	// from codeFixtureDecodeFailed, which covers bytes that WERE read
	// successfully but failed to parse.
	codeFixtureLoadFailed = "MET-H500"

	// codeFixtureDecodeFailed: a parity fixture's JSON was malformed or
	// missing a required field (domain/samples/tick/values).
	codeFixtureDecodeFailed = "MET-H501"

	// codeUnknownTolerance: Compare was asked to check a field the
	// contract has no Tolerance entry for. An unconstrained field is
	// never silently treated as passing — Compare fails closed instead.
	codeUnknownTolerance = "MET-H502"

	// codeJournalOpFailed: a Domain adapter could not apply one
	// JournalEntry (unknown op name, malformed args, or the underlying
	// engine call returned an error). Never silently skipped. Used by
	// every in-package Domain adapter (finance_domain.go's
	// applyFinanceJournalOp today) — the adapters live in THIS package
	// (harness.converge), not in the engine module they wrap, so the
	// code is declared and used here rather than duplicated per adapter
	// file (see finance_domain.go's layering note for why the adapter
	// itself lives here and not in internal/engine/finance).
	codeJournalOpFailed = "MET-H503"
)
