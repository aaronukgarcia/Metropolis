package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
