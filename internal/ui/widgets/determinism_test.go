package widgets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAC12_NoWallClockInNonTestFiles is the real gate behind AC-12's
// documented contract (doc.go: "grep -rn "time.Now" ... returns no
// matches"), run as an actual test rather than left as a manual step.
func TestAC12_NoWallClockInNonTestFiles(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		if strings.Contains(string(b), "time.Now") {
			t.Errorf("%s calls time.Now — widgets must be pure functions of their input state (GR#21/AC-12)", name)
		}
	}
}

// TestAC14_PackageDocCitesModuleKeyAndSpec is a light smoke check that
// doc.go's package comment (read directly from source, since Go does
// not expose package-doc text as a runtime string) mentions the module
// key and the UI-SPEC section this package implements, per AC-14.
func TestAC14_PackageDocCitesModuleKeyAndSpec(t *testing.T) {
	b, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatalf("ReadFile doc.go: %v", err)
	}
	s := string(b)
	for _, want := range []string{"ui.widgets", "UI-SPEC §2", "colourblind"} {
		if !strings.Contains(s, want) {
			t.Errorf("doc.go missing expected mention of %q", want)
		}
	}
}
