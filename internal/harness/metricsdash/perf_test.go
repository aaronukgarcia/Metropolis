package metricsdash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestReadLastRecordForPreset_OversizedLineIsBoundedNotUnbounded is
// BUG-305's unbounded-read proof: readLastRecordForPreset used to scan
// via a bare bufio.Reader.ReadString('\n'), with no per-line ceiling —
// an oversized line (a temp file well past synth.MaxResultsLineBytes)
// could OOM the reader. This proves the call returns promptly with the
// oversized line reported as a CorruptLine, a genuine EARLIER record for
// the preset still recovered as `last`, and a genuine LATER record after
// the oversized line ALSO recovered -- exactly ASM-355's "skip like a
// torn line, keep scanning" recovery contract, mirrored from
// synth.LoadLatestBaseline.
func TestReadLastRecordForPreset_OversizedLineIsBoundedNotUnbounded(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "perf-results.ndjson")

	first := synth.PerfRecord{CommitHash: "aaa000", Preset: "1M", Result: measuredResult("1M", 100*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, first); err != nil {
		t.Fatalf("AppendResult(first): %v", err)
	}

	// An oversized line: well past synth.MaxResultsLineBytes (1 MiB), but
	// not so large this test itself becomes slow/expensive -- the point
	// is proving the ceiling is enforced and the read returns promptly,
	// not proving an actual OOM would occur without it.
	oversized := strings.Repeat("x", synth.MaxResultsLineBytes+1024)
	f, err := os.OpenFile(resultsPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to append oversized line: %v", err)
	}
	if _, err := f.WriteString(oversized + "\n"); err != nil {
		t.Fatalf("writing oversized line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	last := synth.PerfRecord{CommitHash: "ccc222", Preset: "1M", Result: measuredResult("1M", 105*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, last); err != nil {
		t.Fatalf("AppendResult(last): %v", err)
	}

	rec, corrupt, err := readLastRecordForPreset(resultsPath, "1M")
	if err != nil {
		t.Fatalf("readLastRecordForPreset: %v", err)
	}
	if rec == nil {
		t.Fatal("expected the genuine record written AFTER the oversized line to still be recovered, got nil")
	}
	if rec.CommitHash != "ccc222" {
		t.Errorf("CommitHash = %q, want %q (the last genuine record, not lost behind the oversized line)", rec.CommitHash, "ccc222")
	}
	foundOversizedWarning := false
	for _, c := range corrupt {
		if strings.Contains(c.Err.Error(), "exceeds maxResultsLineBytes") {
			foundOversizedWarning = true
		}
	}
	if !foundOversizedWarning {
		t.Errorf("expected a CorruptLine naming the oversized-line rejection (ASM-355), got %+v", corrupt)
	}
}

// TestReadLastRecordForPreset_UnmeasuredRecordIsSkipped proves the
// BUG-305 provenance-screening fix: a record with Measured=false written
// by a second, non-AppendResult writer (a hand edit, bypassing
// AppendResult's own BUG-055 rejection) must be SKIPPED by
// readLastRecordForPreset exactly as synth.LoadLatestBaseline would
// reject it as a baseline candidate -- not trusted verbatim as "the last
// measurement" the dashboard displays.
func TestReadLastRecordForPreset_UnmeasuredRecordIsSkipped(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "perf-results.ndjson")

	genuine := synth.PerfRecord{CommitHash: "aaa000", Preset: "1M", Result: measuredResult("1M", 100*time.Millisecond)}
	if err := synth.AppendResult(resultsPath, genuine); err != nil {
		t.Fatalf("AppendResult(genuine): %v", err)
	}

	// Hand-write a Measured=false record directly, bypassing
	// AppendResult's own BUG-055 rejection -- the "second writer" shape
	// BUG-073/BUG-085's provenance re-check exists for.
	tampered := genuine
	tampered.CommitHash = "bbb111"
	tampered.Result.Measured = false
	data, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshalling tampered record: %v", err)
	}
	f, err := os.OpenFile(resultsPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to append tampered line: %v", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("writing tampered line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	rec, corrupt, err := readLastRecordForPreset(resultsPath, "1M")
	if err != nil {
		t.Fatalf("readLastRecordForPreset: %v", err)
	}
	if rec == nil {
		t.Fatal("expected the genuine EARLIER record to still be recovered as `last` once the tampered one is screened out, got nil")
	}
	if rec.CommitHash != "aaa000" {
		t.Errorf("CommitHash = %q, want %q -- the tampered Measured=false record must never be reported as the last measurement", rec.CommitHash, "aaa000")
	}
	foundProvenanceWarning := false
	for _, c := range corrupt {
		if strings.Contains(c.Err.Error(), "BUG-073") {
			foundProvenanceWarning = true
		}
	}
	if !foundProvenanceWarning {
		t.Errorf("expected a CorruptLine naming the Measured=false rejection (BUG-073), got %+v", corrupt)
	}
}
