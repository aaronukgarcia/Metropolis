package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
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
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	if err := synth.AppendResult(results, seed); err != nil {
		t.Fatalf("seeding baseline: %v", err)
	}

	// (1) No registry file at all -- unrescued.
	var out1, err1 bytes.Buffer
	code := runWith([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "human-reviewed-commit",
		"-accepted-regressions", filepath.Join(t.TempDir(), "no-such-registry.json"),
	}, &out1, &err1, synth.LoadAcceptedRegistry)
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
	code = runWith([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "human-reviewed-commit",
		"-accepted-regressions", registryPath,
	}, &out2, &err2, synth.LoadAcceptedRegistry)
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
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
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
	code := runWith([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "the-actual-current-commit",
		"-accepted-regressions", registryPath,
	}, &stdout, &stderr, synth.LoadAcceptedRegistry)
	if code != exitCouldNotEvaluate {
		t.Fatalf("run() with a registry entry for a DIFFERENT commit = %d, want %d (still unrescued) — BUG-095: acceptance must match the exact commit under test", code, exitCouldNotEvaluate)
	}
	if bytes.Contains(stdout.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want no ACCEPTING banner for a mismatched commit", stdout.String())
	}
}

// TestRun_AcceptedRegistryRescuesBelowNoiseFloorFirstRecordLock is
// BUG-094's rescue-path regression test, updated for ASM-374. The
// original version seeded an all-zero (CitizenCount=0, Months=0,
// PerMonthTick=0) first record — but ASM-374 closed that shape at the
// WRITE boundary: a zero-valued CitizenCount/Months is now implausible,
// so AppendResult rejects it outright (see the zero-value cases in the
// synth package's tests). The still-legitimate degenerate seed is a
// BELOW-NOISE-FLOOR one: CitizenCount and Months match the follow-up
// run's scale, but PerMonthTick=0 — a real, if degenerate, walking-
// skeleton measurement (TickTime==0 is documented in limits.go's
// MinMeasurableDuration re-derivation). Such a record is plausible, so
// LoadLatestBaseline's seeding branch (results.go, `case anchor == nil`)
// takes it as both baseline and anchor without consulting
// MinMeasurableDuration; every subsequent real measurement then compares
// against a below-floor (zero) baseline, which CompareToBaseline's
// noise-floor check reports as BelowNoiseFloor — CouldNotEvaluate() —
// regardless of how the real run measured. That is what makes this test
// deterministic rather than a real-wall-clock race, and it is the exact
// "permanently stuck baseline" lock BUG-094's fix must still reach.
func TestRun_AcceptedRegistryRescuesBelowNoiseFloorFirstRecordLock(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")

	// A plausible below-noise-floor first record: the seed's scale
	// (CitizenCount=50, Months=1) matches the follow-up run below, so the
	// comparison is skipped for BelowNoiseFloor, not ScaleMismatch.
	belowFloorSeed := synth.PerfRecord{
		CommitHash: "degenerate-seed-commit",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 0, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	if reason := belowFloorSeed.Result.ImplausibleReason(); reason != "" {
		t.Fatalf("precondition failed: a below-noise-floor (PerMonthTick=0) record should be plausible, got reason %q", reason)
	}
	if err := synth.AppendResult(results, belowFloorSeed); err != nil {
		t.Fatalf("seeding the below-noise-floor first record: %v", err)
	}

	// (1) An ordinary subsequent run, no registry entry -- still locked,
	// exiting the distinct could-not-evaluate code, never a silent pass
	// and never laundered into a new baseline.
	before, readErr := os.ReadFile(results)
	if readErr != nil {
		t.Fatalf("reading results file after seeding: %v", readErr)
	}
	var out1, err1 bytes.Buffer
	code := runWith([]string{
		"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results,
		"-commit", "real-follow-up-commit",
		"-accepted-regressions", filepath.Join(t.TempDir(), "no-such-registry.json"),
	}, &out1, &err1, synth.LoadAcceptedRegistry)
	if code != exitCouldNotEvaluate {
		t.Fatalf("run() against a below-noise-floor baseline with no registry entry = %d, want %d (locked, BUG-094); stdout=%s stderr=%s", code, exitCouldNotEvaluate, out1.String(), err1.String())
	}
	after, readErr := os.ReadFile(results)
	if readErr != nil {
		t.Fatalf("reading results file after locked run: %v", readErr)
	}
	if string(before) != string(after) {
		t.Fatalf("results file changed after a could-not-evaluate run against a below-noise-floor baseline — an unevaluated measurement must never become the new baseline.\nbefore=%s\nafter=%s", before, after)
	}

	// (2) The SAME below-noise-floor lock, but this run's exact commit is
	// named in the accepted-regressions registry — BUG-094's endorsed fix:
	// the human override must reach a permanently could-not-evaluate
	// baseline, not only an ordinary Regressed==true comparison.
	registryPath := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	registryContent := `[{"preset": "1M", "commitHash": "real-follow-up-commit", "reason": "BUG-094: rescuing a below-noise-floor degenerate seed baseline"}]`
	if err := os.WriteFile(registryPath, []byte(registryContent), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}
	var out2, err2 bytes.Buffer
	code = runWith([]string{
		"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results,
		"-commit", "real-follow-up-commit",
		"-accepted-regressions", registryPath,
	}, &out2, &err2, synth.LoadAcceptedRegistry)
	if code != 0 {
		t.Fatalf("run() with a matching registry entry against a below-noise-floor lock = %d, want 0 (rescued, BUG-094); stdout=%s stderr=%s", code, out2.String(), err2.String())
	}
	if !bytes.Contains(out2.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want an ACCEPTING banner", out2.String())
	}
	data, err := os.ReadFile(results)
	if err != nil {
		t.Fatalf("reading results file after rescue: %v", err)
	}
	if !bytes.Contains(data, []byte(`"acceptedRegression":true`)) || !bytes.Contains(data, []byte("real-follow-up-commit")) {
		t.Fatalf("results file = %s, want a persisted record for commit %q with acceptedRegression=true", data, "real-follow-up-commit")
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
	code := runWith([]string{
		"-preset", "1M", "-citizens", "50", "-months", "1", "-results", results,
		"-accepted-regressions", registryPath,
	}, &stdout, &stderr, synth.LoadAcceptedRegistry)
	if code != 2 {
		t.Fatalf("run() with a malformed accepted-regressions registry = %d, want 2 (usage/IO error); stderr=%s", code, stderr.String())
	}
}

// TestFinishGate_RegressedRunIsNeverRecorded is ASM-353's core regression
// test, driven through finishGate directly because a genuine Regressed
// verdict is unreachable through run() itself today: a real wall-clock
// RunPerf at walking-skeleton scale always measures below
// MinMeasurableDuration, so CompareToBaseline reports BelowNoiseFloor
// (could-not-evaluate), never Regressed. The pre-fix run() appended rec
// unconditionally and only then branched on cmp.Regressed for the exit
// code — so a regressed run still landed in the results file and, via
// ci.yml's `if: always()` cache save, still became a candidate for the
// next run's comparison point. This test is RED against that shape and
// GREEN against finishGate, which must exit 1 AND leave the results file
// untouched — a regressed run must never become the new baseline.
func TestFinishGate_RegressedRunIsNeverRecorded(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stderr bytes.Buffer

	rec := synth.PerfRecord{
		CommitHash: "regressed-commit",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 150 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	cmp := synth.BaselineComparison{HasBaseline: true, Regressed: true, Message: "REGRESSED (step): baseline=100ms current=150ms delta=50.0%"}

	code := finishGate(results, rec, cmp, "test-correlation", &stderr)
	if code != 1 {
		t.Fatalf("finishGate() = %d, want 1 (genuine regression); stderr=%s", code, stderr.String())
	}
	if _, statErr := os.Stat(results); !os.IsNotExist(statErr) {
		t.Fatalf("finishGate recorded a regressed run to %q — ASM-353: a regressed run must never become the new baseline (stat err: %v)", results, statErr)
	}
	if stderr.Len() == 0 {
		t.Fatal("finishGate returned 1 but printed no regression signal to stderr — a regressed gate must be loud, not silent (GR#1)")
	}
}

// TestFinishGate_PassingRunIsRecorded is the companion zero-false-positive
// check: the ASM-353 fix must not suppress the ordinary pass path, which
// still appends the measurement so the next run has a real baseline to
// compare against.
func TestFinishGate_PassingRunIsRecorded(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stderr bytes.Buffer

	rec := synth.PerfRecord{
		CommitHash: "pass-commit",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	cmp := synth.BaselineComparison{HasBaseline: true}

	code := finishGate(results, rec, cmp, "test-correlation", &stderr)
	if code != 0 {
		t.Fatalf("finishGate() = %d, want 0 (genuine pass); stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(results)
	if err != nil {
		t.Fatalf("reading recorded results: %v", err)
	}
	if !bytes.Contains(data, []byte("pass-commit")) {
		t.Fatalf("results file = %s, want the passing run recorded (ASM-353 must not suppress the pass path)", data)
	}
}

// TestFinishGate_WallClockOnlyGrossRegressionIsAPass is BUG-473's
// exit-code check at the gate boundary: a comparison whose ONLY signal is
// a wall-clock gross regression (WallClockGrossRegressed true, but
// Regressed false because wall-clock is advisory only) must exit 0 (a
// PASS) and be recorded as the new baseline — it must NEVER exit 1. The
// advisory signal is surfaced separately as a non-blocking ::warning:: in
// run(); it does not reach finishGate's failing path.
func TestFinishGate_WallClockOnlyGrossRegressionIsAPass(t *testing.T) {
	results := filepath.Join(t.TempDir(), "perf-results.ndjson")
	var stderr bytes.Buffer

	rec := synth.PerfRecord{
		CommitHash: "wallclock-only-commit",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 1156 * time.Microsecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	// The exact BUG-473 shape: wall-clock grossly regressed, allocations
	// flat — so CompareToBaseline sets WallClockGrossRegressed but leaves
	// Regressed false.
	cmp := synth.BaselineComparison{
		HasBaseline:             true,
		WallClockGrossRegressed: true,
		Regressed:               false,
		Message:                 "alloc bytes delta=0.0% count delta=0.0% (threshold 10%); ADVISORY WARNING: wall-clock GROSS regression baseline=491µs current=1.156ms delta=135.4% (gross threshold 100%) — advisory ONLY, does NOT fail the gate (BUG-473)",
	}

	code := finishGate(results, rec, cmp, "test-correlation", &stderr)
	if code != 0 {
		t.Fatalf("finishGate() = %d, want 0 — BUG-473: a wall-clock-only gross regression is advisory and must PASS the gate, never exit 1; stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(results)
	if err != nil {
		t.Fatalf("reading recorded results: %v", err)
	}
	if !bytes.Contains(data, []byte("wallclock-only-commit")) {
		t.Fatalf("results file = %s, want the passing (advisory-only) run recorded as the new baseline", data)
	}
}

// TestRun_AcceptPathUsesDefaultRegistryPathWhenFlagOmitted proves the
// accept path works through cmd/perfci's DEFAULT -accepted-regressions
// path (perf-accepted-regressions.json, resolved relative to the working
// directory) — which is exactly how .github/workflows/ci.yml's perf-smoke
// and perf-1m-probe jobs reach the accept-regression registry: both now
// pass the flag explicitly, but the path itself is a checked-out repo-
// root file. ASM-375 was raised because the accept-regression hatch was
// wired through only one CI job; this test pins the default-path wiring
// both jobs share, so a future regression in the flag default or the
// path cannot silently remove every CI job's escape hatch.
func TestRun_AcceptPathUsesDefaultRegistryPathWhenFlagOmitted(t *testing.T) {
	dir := t.TempDir()
	results := filepath.Join(dir, "perf-results.ndjson")

	seed := synth.PerfRecord{
		CommitHash: "seed",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	if err := synth.AppendResult(results, seed); err != nil {
		t.Fatalf("seeding baseline: %v", err)
	}

	// Write the registry at the DEFAULT path, then chdir so that path
	// resolves into dir — simulating a CI checkout where the repo root
	// holds perf-accepted-regressions.json and perfci is run from there
	// with no -accepted-regressions flag.
	registry := `[{"preset": "1M", "commitHash": "default-path-commit", "reason": "default registry path works for CI jobs that omit the flag (ASM-375)"}]`
	if err := os.WriteFile(filepath.Join(dir, "perf-accepted-regressions.json"), []byte(registry), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}
	// FEAT-082: run() now drives a real composition (compose.Wire ->
	// market.LoadDefault), which resolves the repo's data/ directory.
	// chdir-ing into the temp dir breaks the cwd-upward search, so pin the
	// data dir via the env var the resolver already honours BEFORE chdir.
	if dataDir, err := data.ResolveDataDir("perfci-accept-path-test"); err == nil {
		t.Setenv("METROPOLIS_DATA_DIR", dataDir)
	}
	t.Chdir(dir)

	var out, errb bytes.Buffer
	code := runWith([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "default-path-commit",
	}, &out, &errb, synth.LoadAcceptedRegistry)
	if code != 0 {
		t.Fatalf("run() relying on the default -accepted-regressions path = %d, want 0 (rescued); stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want an ACCEPTING banner via the default registry path (ASM-375)", out.String())
	}
}

// TestRun_AcceptanceNeverWritesTheAcceptedLedger is BUG-245's second
// guard: a local perfci run that ACCEPTS a regression via the ledger must
// still leave the ledger file byte-for-byte unchanged. The ledger is the
// reviewed, git-tracked evidence; a local run may only READ it. If perfci
// ever appended to (or rewrote) the ledger, a developer could self-vouch a
// regression on their own workstation with no PR review at all — the exact
// forge BUG-245 closes. This is a regression guard rather than a red-on-
// old-code test: run() already only reads the ledger (LoadAcceptedRegistry
// uses os.ReadFile, and *acceptedRegistryPath is passed nowhere else), but
// nothing pinned that read-only property against a future refactor until
// this test.
//
// Uses a -citizens 500 follow-up against a -citizens 50 seed to force a
// deterministic ScaleMismatch (CouldNotEvaluate) that the ledger entry then
// rescues — asserting a real wall-clock regression verdict would repeat
// BUG-031's mistake, so the "accepting" path here is reached without any
// timing race.
func TestRun_AcceptanceNeverWritesTheAcceptedLedger(t *testing.T) {
	dir := t.TempDir()
	results := filepath.Join(dir, "perf-results.ndjson")

	seed := synth.PerfRecord{
		CommitHash: "seed",
		Preset:     "1M",
		Result:     synth.PerfResult{CitizenCount: 50, Months: 1, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: synth.PhaseHookCountInHeadlessPath(), Measured: true},
	}
	if err := synth.AppendResult(results, seed); err != nil {
		t.Fatalf("seeding baseline: %v", err)
	}

	registryPath := filepath.Join(dir, "perf-accepted-regressions.json")
	registry := `[{"preset": "1M", "commitHash": "accepting-commit", "reason": "reviewed acceptance for the BUG-245 ledger-write guard"}]`
	if err := os.WriteFile(registryPath, []byte(registry), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}
	before, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("reading registry fixture before run: %v", err)
	}

	var out, errb bytes.Buffer
	code := runWith([]string{
		"-preset", "1M", "-citizens", "500", "-months", "1", "-results", results,
		"-commit", "accepting-commit",
		"-accepted-regressions", registryPath,
	}, &out, &errb, synth.LoadAcceptedRegistry)
	if code != 0 {
		t.Fatalf("run() accepting via the ledger = %d, want 0 (rescued); stdout=%s stderr=%s", code, out.String(), errb.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("ACCEPTING")) {
		t.Fatalf("stdout = %q, want an ACCEPTING banner (precondition for the ledger-write guard)", out.String())
	}

	after, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("reading registry fixture after run: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("BUG-245: a local perfci run modified the accepted-regressions ledger during an accepting run — the ledger must be read-only to the runner (only a reviewed PR change may add an entry)\nbefore=%s\nafter=%s", before, after)
	}
}
