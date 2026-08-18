package metricsdash

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

func measuredResult(preset string, perMonth time.Duration) synth.PerfResult {
	// BUG-272: the perf gate now keys on deterministic allocation counts,
	// not noisy wall-clock. Scale the fixture's allocations with perMonth so
	// these tests' existing duration-ratio intentions still hold under the
	// alloc-based gate (100ms baseline -> ~105ms = +5% = passed; ->200ms =
	// +100% = regressed) and stay well above MinMeasurableAllocs (1000).
	allocCount := uint64(perMonth.Milliseconds()) * 10_000 // 100ms -> 1,000,000
	return synth.PerfResult{
		Preset:         preset,
		CitizenCount:   1_000_000,
		Seed:           1,
		Months:         12,
		TotalTicks:     360,
		TickTime:       perMonth * 12,
		PerMonthTick:   perMonth,
		AllocBytes:     allocCount * 64,
		AllocCount:     allocCount,
		PhaseHookCount: synth.PhaseHookCountInHeadlessPath(),
		Measured:       true,
	}
}

func TestRunPerf_MissingFilesIsNoDataYetNotAnError(t *testing.T) {
	dir := t.TempDir()
	report := RunPerf(filepath.Join(dir, "perf-results.ndjson"), filepath.Join(dir, "perf-accepted-regressions.json"))

	if !report.ResultsFileMissing || !report.AcceptedRegistryMissing {
		t.Fatalf("expected both files to be reported missing, got %+v", report)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("a missing file is not an error/warning condition (AC-6), got warnings: %v", report.Warnings)
	}
	if len(report.Presets) != len(KnownPerfPresets) {
		t.Fatalf("expected one status per known preset, got %d", len(report.Presets))
	}
	for _, p := range report.Presets {
		if p.HasHistory || p.Verdict != "no-history" {
			t.Errorf("preset %s: expected HasHistory=false Verdict=no-history on a fresh checkout, got %+v", p.Preset, p)
		}
	}
}

// TestRunPerf_AcceptedRegressionShowsAcceptedNotStillRegressed is AC-5's
// core claim: a regressed run followed by a matching accepted-registry
// entry must read as "accepted", not "still regressed" — proving the
// dashboard reads the registry the same way perfci itself does.
func TestRunPerf_AcceptedRegressionShowsAcceptedNotStillRegressed(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "perf-results.ndjson")
	acceptedPath := filepath.Join(dir, "perf-accepted-regressions.json")

	baseline := synth.PerfRecord{CommitHash: "aaa000", Preset: "1M", Result: measuredResult("1M", 100*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, baseline); err != nil {
		t.Fatalf("AppendResult(baseline): %v", err)
	}
	// 100% over baseline -- well past RegressionThreshold (10%).
	regressed := synth.PerfRecord{CommitHash: "bbb111", Preset: "1M", Result: measuredResult("1M", 200*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, regressed); err != nil {
		t.Fatalf("AppendResult(regressed): %v", err)
	}

	registryJSON := `[{"preset":"1M","commitHash":"bbb111","reason":"deliberate slowdown, accepted by Bill"}]`
	if err := writeFile(t, acceptedPath, registryJSON); err != nil {
		t.Fatalf("writing accepted-regressions fixture: %v", err)
	}

	report := RunPerf(resultsPath, acceptedPath)
	var got *PerfPresetStatus
	for i := range report.Presets {
		if report.Presets[i].Preset == "1M" {
			got = &report.Presets[i]
		}
	}
	if got == nil {
		t.Fatalf("no status for preset 1M: %+v", report.Presets)
	}
	if got.Verdict != "accepted" {
		t.Errorf("Verdict = %q, want %q (post-acceptance state, not still-regressed)", got.Verdict, "accepted")
	}
	if !got.Accepted || got.AcceptedReason == "" {
		t.Errorf("expected Accepted=true with a non-empty reason, got %+v", got)
	}
}

// TestRunPerf_UnregressedRunReadsAsPassed is the negative control for
// the acceptance test above: without a registry entry, an unregressed
// run must read as "passed", proving the "accepted" test above is
// actually sensitive to the registry, not just always reporting
// "accepted".
func TestRunPerf_UnregressedRunReadsAsPassed(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "perf-results.ndjson")

	baseline := synth.PerfRecord{CommitHash: "aaa000", Preset: "10M", Result: measuredResult("10M", 100*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, baseline); err != nil {
		t.Fatalf("AppendResult(baseline): %v", err)
	}
	passed := synth.PerfRecord{CommitHash: "ccc222", Preset: "10M", Result: measuredResult("10M", 105*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, passed); err != nil {
		t.Fatalf("AppendResult(passed): %v", err)
	}

	report := RunPerf(resultsPath, filepath.Join(dir, "perf-accepted-regressions.json"))
	var got *PerfPresetStatus
	for i := range report.Presets {
		if report.Presets[i].Preset == "10M" {
			got = &report.Presets[i]
		}
	}
	if got == nil {
		t.Fatalf("no status for preset 10M: %+v", report.Presets)
	}
	if got.Verdict != "passed" {
		t.Errorf("Verdict = %q, want %q", got.Verdict, "passed")
	}
}

// TestRunPerf_CorruptResultsFileIsWarningNotCrash is AC-11: a torn/
// corrupt results file must surface as a visible warning, not a crash,
// and must not be reported as a clean state.
func TestRunPerf_CorruptResultsFileIsWarningNotCrash(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "perf-results.ndjson")
	if err := writeFile(t, resultsPath, "{not valid json at all\n"); err != nil {
		t.Fatalf("writing corrupt fixture: %v", err)
	}

	report := RunPerf(resultsPath, filepath.Join(dir, "perf-accepted-regressions.json"))
	if len(report.Warnings) == 0 {
		t.Fatal("expected at least one warning for the corrupt results file, got none")
	}
	for _, p := range report.Presets {
		if p.Verdict == "passed" || p.Verdict == "accepted" {
			t.Errorf("preset %s: a corrupt source must never read as a clean %q state, got %+v", p.Preset, p.Verdict, p)
		}
	}
}

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}
