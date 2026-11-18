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

	// codeActionListDecodeFailed: FEAT-1972079936 inc2's P2 bridge
	// (finance_ab_actions.go) could not read or parse the canonical
	// action-list JSON (converge-finance-actions.json) shared with the
	// TS-side emitter (webconsole/test/converge-fixture-emit.mjs).
	codeActionListDecodeFailed = "MET-H504"

	// codeActionOpFailed: the P2 bridge could not apply one action-list
	// entry against the composed engine (unrecognised op, malformed
	// args, or the composed engine's HandleCommand rejected the
	// translated protocol.Command). Never silently skipped — a
	// deliberately-uncovered op (place_utility_ts_only) is recorded as a
	// SkippedOp instead, not routed through this code.
	codeActionOpFailed = "MET-H505"

	// codeBuildStampMissing: CompareStamped was given an empty reference or
	// candidate build string (BUG-1097 AC-8) -- a validation error, never a
	// silently unstamped report.
	codeBuildStampMissing = "MET-H506"

	// codeEmptyReference: Compare was given a reference trajectory with no
	// samples (BUG-1140) -- zero ticks evaluated is a failure, never a pass.
	codeEmptyReference = "MET-H507"

	// codeNoFieldsReported: the reference had samples but none reported a
	// field (BUG-1140), so there was nothing to constrain or compare.
	codeNoFieldsReported = "MET-H508"

	// codeZeroEvaluated: reference had ticks and fields but no comparison
	// was made (BUG-1140): no tick overlap, an empty candidate, or a
	// distribution window longer than the series.
	codeZeroEvaluated = "MET-H509"

	// BUG-1140 r2 (range H510-H519, claimed for harness.converge).

	// codeDuplicateTick: a trajectory carried two samples for one tick, so
	// Compare would otherwise have silently kept only the last.
	codeDuplicateTick = "MET-H510"

	// codeFieldMissingFromReference: a field the contract declares (or
	// another reference sample reports) is absent from a reference sample.
	codeFieldMissingFromReference = "MET-H511"

	// codeFieldNeverEvaluated: a declared or reference-reported field had
	// zero comparisons made (no shared ticks, candidate never reports it,
	// or a distribution window that never filled).
	codeFieldNeverEvaluated = "MET-H512"
)

// BUG-1140 (Run side), same range H510-H519.
const (
	// codeEmptyJournal: a Domain was asked to Run a journal with no entries.
	codeEmptyJournal = "MET-H513"

	// codeNoSample: the journal has no sample op, so its trajectory is empty.
	codeNoSample = "MET-H514"

	// codeSetupMissing: an advance/sample ran before the setup op it needs.
	codeSetupMissing = "MET-H515"

	// codeUnknownArg: an op's args carried a key the adapter does not define.
	codeUnknownArg = "MET-H516"

	// codeMissingArg: a required op argument was absent (never defaulted).
	codeMissingArg = "MET-H517"

	// codeSampleTickOrder: a sample tick was negative or not strictly after
	// the previous sample's tick.
	codeSampleTickOrder = "MET-H518"

	// codeAdvanceTooLarge: an advance op asked for more days than
	// maxAdvanceDays, so the run would loop for minutes.
	codeAdvanceTooLarge = "MET-H519"
)
