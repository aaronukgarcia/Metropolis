package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
	if code != 0 && code != 1 {
		t.Fatalf("run() = %d, want 0 (pass) or 1 (regression) — never 2 (usage/IO error): stderr=%s", code, err2.String())
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
