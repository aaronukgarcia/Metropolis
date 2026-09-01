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

	// MET-H503 ("a Domain adapter could not apply one JournalEntry —
	// unknown op name, malformed args, or the underlying engine call
	// returned an error; never silently skipped") is reserved here but
	// declared and USED as its own Go const in each Domain adapter's own
	// package instead of here — only the adapter (e.g.
	// internal/engine/finance's codeFinanceConvergeJournalOpFailed)
	// knows its op vocabulary and engine-call context well enough to
	// build a useful error map, and this package has no journal-
	// executing code of its own to raise it from (Domain.Run is the
	// adapter's method, not this package's). Still registered in
	// data/errors.json under harness.converge (GR#7) since it is this
	// module's error surface, just not this .go file's constant.
)
