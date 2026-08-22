package router

// GR#21 determinism: no code path in this package's non-test source ever
// reads the wall clock directly -- every staleness/pruning decision is
// driven by protocol.Tick values carried on routed messages (see
// router.go's advanceTickLocked/pruneStaleLocked and doc.go's "no
// wall-clock" note).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestNoWallClockUsage mechanically encodes the ICD §7 grep check ("no
// wall-clock time is read anywhere in this seam") as a real test,
// mirroring ui.screen.build/proj/trade/ticker's own identical
// TestNoWallClockUsage convention.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte("time.Now(")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if bytes.Contains(b, needle) {
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (GR#21/ICD §7)", name)
		}
	}
}
