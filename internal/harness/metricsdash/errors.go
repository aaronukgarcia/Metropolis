package metricsdash

// Registry error codes for feat.metricsdash (module key pending
// confirmation, ASM-451/Escalation A/C — docs/planning/acceptance/
// feat.metricsdash.md). Range: H400-H499, declared in data/errors.json's
// "ranges.reserved" table under the harness ("H") layer's fifth
// sub-range — H000-H099 belongs to harness.replay, H100-H199 to
// ui.harness, H200-H299 to harness.headless, H300-H399 to
// harness.synth. Checked against BOTH that table AND a live source
// scan (`grep -rn "MET-H4" internal/ cmd/`) before claiming H400-H499,
// per BUG-008's lesson that the table alone is not always current — no
// existing MET-H4xx code was found either place at the time this file
// was written.
const (
	// codeWeaknessSourceUnavailable: RunWeakness could not obtain the
	// weakness-histogram source data — either `node claude-bow.js
	// weakness` itself failed to execute/exited non-zero, or its stdout
	// could not be parsed into the expected shape (AC-1/AC-11: a broken
	// source must surface as a visible failure, never a silently empty
	// or fabricated report).
	codeWeaknessSourceUnavailable = "MET-H400"

	// codeLintSourceUnavailable: RunLint could not obtain `node
	// claude-bow.js lint`'s drift report (exec failure or unparseable
	// stdout).
	codeLintSourceUnavailable = "MET-H401"

	// codeGateStatusSourceUnavailable: RunGateStatus could not obtain
	// `node claude-bow.js gate-status <sprint>`'s verdict report (exec
	// failure or unparseable stdout).
	codeGateStatusSourceUnavailable = "MET-H402"

	// codePerfSourceCorrupt: the perf-CI results NDJSON file or the
	// accepted-regression registry JSON file existed but could not be
	// read/parsed cleanly (AC-11, mirroring perfci's own BUG-054
	// corrupt-line handling) — distinct from "the file does not exist
	// yet" (AC-6), which is not an error.
	codePerfSourceCorrupt = "MET-H403"

	// codeFeedbackWriteFailed: the easy-logging entry point (feedback.go,
	// LogNote) could not durably write the submitted note to the shared
	// FEAT-065 feedback inbox (AC-10) — disk error, unwritable
	// directory, marshal failure.
	codeFeedbackWriteFailed = "MET-H404"

	// codeFeedbackEmptyBody: a logging submission arrived with an empty
	// (whitespace-only) body — rejected before any file is written
	// rather than filing a useless blank BOW record (AC-10).
	codeFeedbackEmptyBody = "MET-H405"
)
