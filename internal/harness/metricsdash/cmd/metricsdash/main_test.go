package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
)

// TestRun_LogNoteWritesRecordWithoutRepoOrSprintFlags is AC-9: the
// "easy" log entry point must be reachable via a single command with no
// pause/inspect/live-session step -- confirmed here by invoking it with
// nothing but -log/-inbox and asserting a real record lands on disk.
func TestRun_LogNoteWritesRecordWithoutRepoOrSprintFlags(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := tempOutputFiles(t)

	code := run([]string{"-log", "the sprint board looks stale", "-inbox", dir}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() exit code = %d, want 0", code)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 logged record, got %d", len(entries))
	}
}

func TestRun_EmptyLogNoteFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := tempOutputFiles(t)

	code := run([]string{"-log", "   ", "-inbox", dir}, stdout, stderr)
	if code == 0 {
		t.Fatal("expected a non-zero exit code for an empty note, got 0")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no record written for a rejected empty note, found %d", len(entries))
	}

	stderrContent := readAll(t, stderr)
	if !strings.Contains(stderrContent, "failed to log note") {
		t.Errorf("stderr = %q, expected an explicit failure message (GR#1/AC-10)", stderrContent)
	}
}

// TestRun_KindFlagSelectsRecordKind is the CLI-level regression test for
// BUG-133: the -kind flag must genuinely select which BOW item type the
// resulting FeedbackRecord asks claude-devfeedback-import.js to create,
// not silently collapse to "bug" for anything other than the default.
// LogNote and claude-devfeedback-import.js already have their own kind
// regression coverage (BUG-126) -- this test closes the one gap those
// didn't cover: that run()'s flag.String("kind", ...) value actually
// reaches metricsdash.LogNote unmolested from the CLI entry point, for
// every valid kind, not just the untested default.
func TestRun_KindFlagSelectsRecordKind(t *testing.T) {
	cases := []struct {
		kindFlag string
		want     string
	}{
		{"bug", "bug"},
		{"finding", "finding"},
		{"assumption", "assumption"},
		{"", "bug"}, // no -kind flag at all -- documented default (AC-7)
	}
	for _, tc := range cases {
		t.Run("kind="+tc.kindFlag, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr := tempOutputFiles(t)

			args := []string{"-log", "a note for the -kind regression test", "-inbox", dir}
			if tc.kindFlag != "" {
				args = append(args, "-kind", tc.kindFlag)
			}

			code := run(args, stdout, stderr)
			if code != 0 {
				t.Fatalf("run() exit code = %d, want 0 (stderr: %s)", code, readAll(t, stderr))
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("ReadDir: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected exactly 1 logged record, got %d", len(entries))
			}

			raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var rec debug.FeedbackRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Fatalf("record is not valid debug.FeedbackRecord JSON: %v", err)
			}
			if rec.Kind != tc.want {
				t.Errorf("-kind %q produced record.Kind = %q, want %q -- the -kind flag did not select the BOW item type", tc.kindFlag, rec.Kind, tc.want)
			}
		})
	}
}

func tempOutputFiles(t *testing.T) (stdout, stderr *os.File) {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "stdout.txt"))
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr.txt"))
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	t.Cleanup(func() {
		_ = out.Close()
		_ = errF.Close()
	})
	return out, errF
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}
