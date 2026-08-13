package save

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoWallClockInNonTestFiles is AC-15: no non-test .go file in this
// package may call time.Now/time.Since/time.Ticker/time.Sleep. The one
// documented, allowed pattern for a display-only wall-clock string
// (e.g. a human-readable "saved at" label) is not used anywhere in this
// build — this package's Meta/SaveSummary carry no such field — so the
// check below is a flat, unexceptioned grep, matching this AC's own
// "an unexplained match is a FAIL" wording.
func TestNoWallClockInNonTestFiles(t *testing.T) {
	pattern := regexp.MustCompile(`\btime\.(Now|Since|Ticker|Sleep)\b`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if loc := pattern.FindIndex(data); loc != nil {
			t.Fatalf("%s contains a wall-clock call (%q) — AC-15 forbids time.Now/time.Since/time.Ticker/time.Sleep in this package's non-test files outside a documented, never-determinism-sensitive display-only exception (none exist in this build)", name, data[loc[0]:loc[1]])
		}
	}
}
