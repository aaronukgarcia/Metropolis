package uitest

// Registry error codes for ui.harness (MOD-014). Range: H100-H199,
// declared in data/errors.json's "ranges.reserved" table under the
// existing "H" (harness) layer's second sub-range — H000-H099 already
// belongs to harness.replay (MOD-013), so this package claims the next
// free H-layer sub-range rather than colliding with it. Checked against
// BOTH data/errors.json's ranges.reserved table AND a live source scan
// (`grep -rn "MET-H1" internal/ cmd/`) before claiming H100-H199, per
// BUG-008's lesson that the table alone is not always current — no
// existing MET-H1xx code was found either place. Every code below IS
// registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); internal/foundation/errs's source-scan test
// (TestSourceCodesAreRegisteredAndInRange) guards against this ever
// drifting out of sync, and against another module's range accidentally
// overlapping this one.
const (
	// codeMalformedScriptToken: a scripted key-sequence DSL token failed
	// to parse (AC-2b) — either it does not name a documented <Name>
	// special, or it is not a single rune and not a <Name> form. The same
	// code covers AC-7's "decodes to no registered action" case: this
	// package's only "registered actions" are the tokens ParseScript's
	// grammar documents (doc.go), so a token outside that grammar is
	// simultaneously "malformed" and "unmapped" — one rejection, not two
	// codes for the same underlying condition. Logged as an assumption
	// (see this item's dispatch report): a reasonable BA could have
	// wanted AC-7 to carry a distinct code from AC-2b's.
	codeMalformedScriptToken = "MET-H100"

	// codeFixtureExhausted: an attached harness.replay fixture's Delta
	// stream closed before the scripted sequence's caller-stated expected
	// delta count was reached (AC-3b) — reported as a distinct, named
	// condition rather than silently treating "no more deltas" as "the
	// scenario completed."
	codeFixtureExhausted = "MET-H101"

	// codeMissingGolden: AssertSnapshot was called without -update and no
	// golden file exists at the resolved snapshot path (AC-8) — distinct
	// from a content mismatch, which is a plain t.Fatalf comparison
	// failure, not a registry error (a mismatch is the normal, expected
	// output of a comparison that found a difference, not an exceptional
	// condition).
	codeMissingGolden = "MET-H102"

	// codeHostileSnapshotName: a snapshot name (t.Name(), AC-5b) contains
	// a '/'-segment that fails serialize.ValidateShardName, or the
	// resolved path would fall outside testdata/snapshots/ — rejected
	// outright, never silently sanitised into wherever it would otherwise
	// resolve.
	codeHostileSnapshotName = "MET-H103"

	// codeHarnessCopied: a *Harness method was called on a struct copy of
	// the value NewHarness constructed (SEC-020-class, AC-13b-style
	// guard, mirrors harness.replay's Recorder/EnginePlayer).
	codeHarnessCopied = "MET-H104"

	// codeFixturePlaybackReadOnly: SendCommand was called against the
	// read-only Transport adapter AttachFixture builds around a
	// harness.replay.UIPlayer (AC-3) — fixture playback has nothing to
	// send a command to.
	codeFixturePlaybackReadOnly = "MET-H105"
)
