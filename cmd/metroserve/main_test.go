package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureRun redirects run's stdout/stderr *os.File params to temp files so
// output can be asserted without depending on the process's real stdio.
func captureRun(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	outFile, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("temp stdout: %v", err)
	}
	defer func() { _ = outFile.Close() }()
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = errFile.Close() }()

	code = run(args, outFile, errFile)

	if _, err := outFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek stdout: %v", err)
	}
	if _, err := errFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek stderr: %v", err)
	}
	var outBuf, errBuf bytes.Buffer
	if _, err := io.Copy(&outBuf, outFile); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if _, err := io.Copy(&errBuf, errFile); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return outBuf.String(), errBuf.String(), code
}

// TestRun_Version_PrintsBuildIdentityAndExitsCleanly mirrors
// cmd/metropolis's own TestRun_Version_PrintsBuildIdentity pattern: this
// binary's --version must print buildinfo.String() and exit 0 without
// ever constructing an engine or opening a network listener.
func TestRun_Version_PrintsBuildIdentityAndExitsCleanly(t *testing.T) {
	stdout, _, code := captureRun(t, []string{"-version"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "metropolis ") {
		t.Fatalf("stdout %q does not contain the expected build identity line", stdout)
	}
}

// TestRun_UnknownFlag_ReturnsUsageError proves a bad flag is reported and
// exits non-zero rather than falling through to booting the engine.
func TestRun_UnknownFlag_ReturnsUsageError(t *testing.T) {
	_, _, code := captureRun(t, []string{"-not-a-real-flag"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (flag parse error)", code)
	}
}
