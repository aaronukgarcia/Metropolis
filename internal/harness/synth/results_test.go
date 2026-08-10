package synth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile is a tiny test-local helper writing raw content to path —
// used only by the corrupt-baseline-file test below, which needs
// control over exact bytes rather than going through AppendResult.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// TestAppendResult_ResultSchema is AC-5's check: results are persisted
// in a form a CI graphing step can consume (JSON keyed by commit hash
// and scale preset), not only printed to stdout — and reading it back
// via LoadLatestBaseline recovers exactly what was written.
func TestAppendResult_ResultSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	rec := PerfRecord{
		CommitHash: "deadbeef",
		Preset:     "1M",
		Result:     PerfResult{Preset: "1M", CitizenCount: OneMillionCitizens, Months: 12, PerMonthTick: 42 * time.Millisecond},
	}
	if err := AppendResult(path, rec); err != nil {
		t.Fatalf("AppendResult: %v", err)
	}

	got, err := LoadLatestBaseline(path, "1M")
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil after a matching record was written")
	}
	if got.PerMonthTick != rec.Result.PerMonthTick {
		t.Fatalf("PerMonthTick = %v, want %v", got.PerMonthTick, rec.Result.PerMonthTick)
	}
}

// TestAppendResult_Appends proves multiple commits accumulate rather
// than overwrite (AC-5's "keyed by commit hash", meaningless if a second
// write erased the first), and that LoadLatestBaseline returns the MOST
// RECENT matching record.
func TestAppendResult_Appends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	older := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{PerMonthTick: 100 * time.Millisecond}}
	newer := PerfRecord{CommitHash: "commit2", Preset: "1M", Result: PerfResult{PerMonthTick: 110 * time.Millisecond}}
	if err := AppendResult(path, older); err != nil {
		t.Fatalf("AppendResult(older): %v", err)
	}
	if err := AppendResult(path, newer); err != nil {
		t.Fatalf("AppendResult(newer): %v", err)
	}

	got, err := LoadLatestBaseline(path, "1M")
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if got == nil || got.PerMonthTick != newer.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the most recently appended record (%v)", got, newer.Result.PerMonthTick)
	}
}

// TestLoadLatestBaseline_MissingFileIsNotAnError is AC-8's core claim
// applied to the storage layer: a fresh CI cache (no results file at
// all) must not fail the build.
func TestLoadLatestBaseline_MissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.ndjson")

	got, err := LoadLatestBaseline(path, "1M")
	if err != nil {
		t.Fatalf("LoadLatestBaseline on a missing file should not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("LoadLatestBaseline on a missing file should return nil, got %+v", got)
	}
}

// TestLoadLatestBaseline_NoMatchingPresetIsNotAnError: a results file
// exists but has never recorded THIS preset — also not an error (a
// fresh scale preset has no prior baseline either, AC-8).
func TestLoadLatestBaseline_NoMatchingPresetIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	if err := AppendResult(path, PerfRecord{CommitHash: "c1", Preset: "1M", Result: PerfResult{}}); err != nil {
		t.Fatalf("AppendResult: %v", err)
	}

	got, err := LoadLatestBaseline(path, "10M")
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if got != nil {
		t.Fatalf("LoadLatestBaseline for an unrecorded preset should return nil, got %+v", got)
	}
}

// TestLoadLatestBaseline_CorruptFileIsAnError proves a file that DOES
// exist but fails to parse is reported as codeBaselineCorrupt — distinct
// from the "no baseline yet" cases above, which are never errors.
func TestLoadLatestBaseline_CorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	if err := writeFile(path, "{not valid json\n"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	_, err := LoadLatestBaseline(path, "1M")
	wantCode(t, err, codeBaselineCorrupt)
}
