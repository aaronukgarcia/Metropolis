package synth

// Registry error codes for harness.synth (MOD-016). Range: H300-H399,
// declared in data/errors.json's "ranges.reserved" table under the
// existing "H" (harness) layer's fourth sub-range — H000-H099 belongs to
// harness.replay, H100-H199 to ui.harness, H200-H299 to harness.headless
// (MOD-015; pre-registered ahead of that package's own build, per its
// own entries in data/errors.json). Checked against BOTH that table AND
// a live source scan (`grep -rn "MET-H3" internal/ cmd/`) before
// claiming H300-H399, per BUG-008's lesson that the table alone is not
// always current — no existing MET-H3xx code was found either place.
// Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test
// (TestSourceCodesAreRegisteredAndInRange) guards against this ever
// drifting out of sync, and against another module's range accidentally
// overlapping this one.
const (
	// codeCitizenCountOutOfRange: Params.CitizenCount fell outside
	// MinSyntheticCitizens..MaxSyntheticCitizens (limits.go) — rejected
	// BEFORE any generation allocation begins (AC-1b(b)/(c)), never
	// clamped to the nearest legal value.
	codeCitizenCountOutOfRange = "MET-H300"

	// codeSprawlOutOfRange: Params.Sprawl fell outside
	// MinSprawl..MaxSprawl (AC-7b).
	codeSprawlOutOfRange = "MET-H301"

	// codeInvalidNetworkShape: Params.NetworkShape was not one of the
	// closed enum {NetworkGrid, NetworkRadial, NetworkOrganic} (AC-7b) —
	// never accepted as an arbitrary free string.
	codeInvalidNetworkShape = "MET-H302"

	// codeGenerationIOFailed: Generate's underlying
	// serialize.NDJSONSerializer write to the caller's io.Writer failed
	// (disk full, closed pipe, ...) AFTER ValidateParams already passed —
	// an I/O failure on a legal request, distinct from a domain-
	// validation rejection.
	codeGenerationIOFailed = "MET-H303"

	// codeInvalidMonths: RunPerf was called with months <= 0.
	codeInvalidMonths = "MET-H304"

	// codePerfRunFailed: the engine.core command path (AdvanceTicks via
	// protocol.Command, the same seam engine.detgate's RunGate uses)
	// rejected a command, or the transport/command loop failed, during a
	// RunPerf measurement run.
	codePerfRunFailed = "MET-H305"

	// codeBaselineCorrupt: an existing perf-results file could not be
	// parsed as the expected NDJSON PerfRecord schema (AC-5/AC-8) —
	// distinct from "no results file exists yet", which is not an error
	// (see LoadLatestBaseline's doc comment).
	codeBaselineCorrupt = "MET-H306"

	// codeRegressionDetected: the perf CI gate (cmd/perfci) found a
	// >RegressionThreshold monthly-tick-time regression against the
	// stored baseline (AC-6, AC-10; M0-ENG §6 point 5).
	codeRegressionDetected = "MET-H307"

	// codeUnmeasuredResult: AppendResult was asked to persist a
	// PerfResult whose Measured flag is false — i.e. a value that was
	// never actually produced by RunPerf (BUG-055: a hand-built
	// PerfResult{} zero-value literal is byte-for-byte indistinguishable
	// in the ndjson from a legitimate "RunPerf really measured 0 hooks"
	// record unless provenance is checked explicitly at the write
	// boundary).
	codeUnmeasuredResult = "MET-H308"

	// codeGateCouldNotEvaluate: cmd/perfci's regression comparison was
	// SKIPPED — ScaleMismatch or BelowNoiseFloor — rather than genuinely
	// passing or failing (BUG-071). Distinct from codeRegressionDetected
	// (MET-H307, a real failure) and from a plain pass (no error at all):
	// this is the registry-sourced signal for the gate's third outcome,
	// "could not evaluate", so it can never be silently indistinguishable
	// from "satisfied" in either the exit code or the human-readable
	// message a human/CI log reads.
	codeGateCouldNotEvaluate = "MET-H309"

	// codeImplausibleResult: a syntactically-valid, Measured=true
	// PerfRecord carries a value a genuine RunPerf call can never
	// structurally produce (negative CitizenCount/Months/PerMonthTick —
	// see PerfResult.ImplausibleReason, perf.go). BUG-085: closes the
	// gap left by only checking the Measured flag, which is a
	// self-reported bool with no structural backing.
	codeImplausibleResult = "MET-H310"

	// codeUnjustifiedAcceptedRegression: a PerfRecord sets
	// AcceptedRegression=true (BUG-083's deliberate, visible baseline
	// override, now gated on a git-committed AcceptedRegistry entry —
	// BUG-095, accepted.go) with no AcceptedReason. An override with no
	// recorded justification is
	// exactly as untrustworthy as an unmeasured record with no
	// provenance — refused at both the write boundary (AppendResult)
	// and the read boundary (LoadLatestBaseline), the same two-boundary
	// enforcement shape as codeUnmeasuredResult/BUG-073.
	codeUnjustifiedAcceptedRegression = "MET-H311"
)
