package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

// TestRun_NoBaselineExitsZeroAndRecordsBaseline is AC-8 exercised at the
// CLI boundary: a first run against a fresh results file must exit 0
// and report "no prior baseline", not fail. -citizens overrides the
// preset's real 1M citizen count so this test stays fast (see -citizens'
// own flag doc comment in main.go) — the real 1M/10M scale is exercised
// by a human/CI running perfci for real, not by go test.
func TestRun_NoBaselineExitsZeroAndRecordsBaseline(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stdout, stderr bytes.Buffer

	code := run([]string{"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() = %d, want 0 (no baseline should never fail the build); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("no prior baseline")) {
		t.Fatalf("stdout = %q, want it to mention \"no prior baseline\" (AC-8)", stdout.String())
	}
	if _, err := os.Stat(results); err != nil {
		t.Fatalf("results file was not created: %v", err)
	}
}

// TestRun_SecondRunComparesAgainstFirst proves the baseline round-trips
// through a real results file across two separate `run` invocations
// (AC-6): the second run finds the first run's recorded measurement and
// reports a delta, not another "no prior baseline".
func TestRun_SecondRunComparesAgainstFirst(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var out1, err1, out2, err2 bytes.Buffer

	if code := run([]string{"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results}, &out1, &err1); code != 0 {
		t.Fatalf("first run() = %d, stderr=%s", code, err1.String())
	}
	code := run([]string{"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results}, &out2, &err2)
	// A second, near-identical run should not itself be expected to
	// regress (same citizen count, same month count) — but this is
	// wall-clock timing on a real machine, so this test asserts only
	// that the CLI completed and produced a comparison message, not a
	// specific pass/fail verdict (asserting a specific verdict on real
	// timing would be exactly the BUG-031 mistake this item's brief
	// warned against).
	if bytes.Contains(out2.Bytes(), []byte("no prior baseline")) {
		t.Fatalf("second run should have found the first run's baseline, got stdout=%q", out2.String())
	}
	// BUG-071: a real comparison at matching scale has three legitimate
	// outcomes now, not two — pass (0), regression (1), or could-not-
	// evaluate (exitCouldNotEvaluate) if this near-identical, fast local
	// run happens to measure below MinMeasurableDuration. Never 2
	// (usage/IO error) — that would mean the CLI itself malfunctioned,
	// not a real comparison outcome.
	if code != 0 && code != 1 && code != exitCouldNotEvaluate {
		t.Fatalf("run() = %d, want 0 (pass), 1 (regression), or %d (could-not-evaluate) — never 2 (usage/IO error): stderr=%s", code, exitCouldNotEvaluate, err2.String())
	}
}

// TestRun_ScaleMismatchIsDistinctFromPassAndDoesNotBecomeBaseline is
// BUG-071's core regression test — the Destructive-5 finding this item
// exists to close, reproduced end to end through the real CLI rather
// than only through CompareToBaseline in isolation. Two live-verified
// claims, both asserted here:
//
//  1. A skipped (could-not-evaluate) comparison must exit a code
//     DISTINCT from both a genuine pass (0) and a genuine regression
//     failure (1) — pre-fix, ScaleMismatch left cmp.Regressed false, so
//     run() returned 0, byte-for-byte indistinguishable from a real
//     pass at the exit-code boundary CI actually reads.
//  2. The unevaluated run's measurement must NOT be appended to the
//     results file — pre-fix, AppendResult ran unconditionally
//     regardless of cmp.CouldNotEvaluate(), so the mismatched-scale
//     (or below-noise-floor) run was laundered into the next baseline.
//
// This test is RED against the pre-fix run(): it would see code == 0
// (ScaleMismatch never sets Regressed) AND before != after (AppendResult
// ran unconditionally after computing cmp) — proven by construction,
// not by re-deriving the old code path.
func TestRun_ScaleMismatchIsDistinctFromPassAndDoesNotBecomeBaseline(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var out1, err1, out2, err2 bytes.Buffer

	if code := run([]string{"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results}, &out1, &err1); code != 0 {
		t.Fatalf("first run() = %d, stderr=%s", code, err1.String())
	}
	before, readErr := os.ReadFile(results)
	if readErr != nil {
		t.Fatalf("reading results file after first run: %v", readErr)
	}

	// Second run at a DIFFERENT citizen count than the first — the exact
	// mismatched-scale shape Destructive-5 live-verified: baseline and
	// current carry different CitizenCount, so CompareToBaseline reports
	// ScaleMismatch and skips the percentage check entirely.
	code := run([]string{"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results}, &out2, &err2)

	if code == 0 {
		t.Fatalf("run() = 0 on a ScaleMismatch (could-not-evaluate) outcome — BUG-071: a skipped comparison must never be exit-code-identical to a genuine pass. stdout=%s stderr=%s", out2.String(), err2.String())
	}
	if code == 1 {
		t.Fatalf("run() = 1 (regression) on a ScaleMismatch outcome — a skipped comparison is not a regression verdict either, it needs its own distinct code. stdout=%s", out2.String())
	}
	if code != exitCouldNotEvaluate {
		t.Fatalf("run() = %d, want the distinct could-not-evaluate code %d for a ScaleMismatch outcome", code, exitCouldNotEvaluate)
	}
	if !bytes.Contains(err2.Bytes(), []byte("COULD NOT EVALUATE")) {
		t.Fatalf("stderr = %q, want a loud, unambiguous could-not-evaluate annotation (BUG-071)", err2.String())
	}

	after, readErr := os.ReadFile(results)
	if readErr != nil {
		t.Fatalf("reading results file after second run: %v", readErr)
	}
	if string(before) != string(after) {
		t.Fatalf("results file changed after a could-not-evaluate run — BUG-071: an unevaluated (ScaleMismatch) measurement must never be laundered into the new baseline.\nbefore=%s\nafter=%s", before, after)
	}
}

// TestRun_UnknownPresetExitsNonZero proves the CLI rejects an
// unrecognised -preset rather than silently defaulting.
func TestRun_UnknownPresetExitsNonZero(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stdout, stderr bytes.Buffer

	code := run([]string{"-preset", "bogus", "-results", results}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run() with an unknown -preset should not exit 0")
	}
	if stderr.Len() == 0 {
		t.Fatal("expected an error message on stderr for an unknown -preset")
	}
}

// TestRun_AcceptRegressionCLIFlagNoLongerExists is BUG-095's usage-
// boundary regression test: this command USED TO accept a regression via
// a bare -accept-regression/-accept-reason CLI flag pair, which
// Destructive-9 live-verified is exactly as forgeable as the results-file
// field it set (see cmd/perfci's package doc comment for the full
// rationale). That flag is gone — passing it must now be an ordinary
// "unknown flag" usage error (exit 2), not silently accepted or
// reinterpreted as anything else.
func TestRun_AcceptRegressionCLIFlagNoLongerExists(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stdout, stderr bytes.Buffer

	code := run([]string{"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results, "-accept-regression"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() with the removed -accept-regression flag = %d, want 2 (usage error) — BUG-095: this flag must not exist any more", code)
	}
	if _, statErr := os.Stat(results); !os.IsNotExist(statErr) {
		t.Fatalf("run() rejected for a usage error but still touched the results file %q (stat err: %v)", results, statErr)
	}
}

// TestRun_AcceptedRegistryRescuesCouldNotEvaluate is BUG-095's end-to-end
// regression test for the real accept path, and folds in BUG-094's
// endorsed extension in the same test: the accept mechanism must also be
// able to rescue a permanently could-not-evaluate baseline, not only an
// ordinary regression.
//
// Deliberately uses a ScaleMismatch (not a wall-clock-timed regression) to
// force a deterministic CouldNotEvaluate outcome — asserting a specific
// pass/fail verdict from real wall-clock timing between two `run` calls
// would repeat BUG-031's mistake (see TestRun_SecondRunComparesAgainstFirst's
// own comment for the same reasoning applied elsewhere in this file).
//
//  1. WITHOUT a matching registry entry: a scale-mismatched run against a
//     seeded baseline exits exitCouldNotEvaluate (3), same as
//     TestRun_ScaleMismatchIsDistinctFromPassAndDoesNotBecomeBaseline —
//     the ordinary, unrescued case.
//  2. WITH a registry entry naming this exact commit: the SAME
//     scale-mismatched run instead exits 0, prints an ACCEPTING banner,
//     and persists a record with AcceptedRegression=true — the git-
//     committed evidence rescued a state that, pre-BUG-094-fold-in, had
//     no CLI-reachable override at all.
func TestRun_AcceptedRegistryRescuesCouldNotEvaluate(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")

	seed := synth.PerfRecord{
		CommitHash: "seed",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, Measured: true},
	}
	if err := synth.AppendResult(results, seed); err != nil {
		t.Fatalf("seeding baseline: %v", err)
	}

	// (1) No registry file at all -- unrescued.
	var out1, err1 bytes.Buffer
	code := run([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "human-reviewed-commit",
		"-accepted-regressions", filepath.Join(t.TempDir(), "no-such-registry.json"),
	}, &out1, &err1)
	if code != exitCouldNotEvaluate {
		t.Fatalf("run() without a registry entry = %d, want %d (could-not-evaluate); stderr=%s", code, exitCouldNotEvaluate, err1.String())
	}

	// (2) A registry naming this EXACT commit -- rescued.
	registryPath := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	registryContent := `[{"preset": "1M", "commitHash": "human-reviewed-commit", "reason": "citizen-count override for a smoke test, reviewed"}]`
	if err := os.WriteFile(registryPath, []byte(registryContent), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}

	var out2, err2 bytes.Buffer
	code = run([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "human-reviewed-commit",
		"-accepted-regressions", registryPath,
	}, &out2, &err2)
	if code != 0 {
		t.Fatalf("run() with a matching registry entry = %d, want 0 (rescued); stdout=%s stderr=%s", code, out2.String(), err2.String())
	}
	if !bytes.Contains(out2.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want an ACCEPTING banner", out2.String())
	}
	data, err := os.ReadFile(results)
	if err != nil {
		t.Fatalf("reading results file: %v", err)
	}
	if !bytes.Contains(data, []byte(`"acceptedRegression":true`)) || !bytes.Contains(data, []byte("human-reviewed-commit")) {
		t.Fatalf("results file = %s, want a persisted record for commit %q with acceptedRegression=true", data, "human-reviewed-commit")
	}
}

// TestRun_AcceptedRegistryEntryForADifferentCommitDoesNotRescue proves the
// registry match is exact on commit hash — an acceptance entry for some
// OTHER commit must never rescue the current run, which would defeat the
// entire "names the exact commit" premise of BUG-095's fix.
func TestRun_AcceptedRegistryEntryForADifferentCommitDoesNotRescue(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")

	seed := synth.PerfRecord{
		CommitHash: "seed",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, Measured: true},
	}
	if err := synth.AppendResult(results, seed); err != nil {
		t.Fatalf("seeding baseline: %v", err)
	}

	registryPath := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	registryContent := `[{"preset": "1M", "commitHash": "some-other-commit", "reason": "accepted for a different commit entirely"}]`
	if err := os.WriteFile(registryPath, []byte(registryContent), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "the-actual-current-commit",
		"-accepted-regressions", registryPath,
	}, &stdout, &stderr)
	if code != exitCouldNotEvaluate {
		t.Fatalf("run() with a registry entry for a DIFFERENT commit = %d, want %d (still unrescued) — BUG-095: acceptance must match the exact commit under test", code, exitCouldNotEvaluate)
	}
	if bytes.Contains(stdout.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want no ACCEPTING banner for a mismatched commit", stdout.String())
	}
}

// TestRun_MalformedAcceptedRegistryIsAUsageError proves a corrupted
// accepted-regressions file fails the run outright (exit 2) rather than
// being silently treated as "nothing accepted" — see
// synth.LoadAcceptedRegistry's doc comment for why this fails closed.
func TestRun_MalformedAcceptedRegistryIsAUsageError(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	registryPath := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	if err := os.WriteFile(registryPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("writing malformed registry fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results,
		"-accepted-regressions", registryPath,
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() with a malformed accepted-regressions registry = %d, want 2 (usage/IO error); stderr=%s", code, stderr.String())
	}
}
