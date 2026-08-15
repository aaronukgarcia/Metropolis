package diagrams

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packageGoFiles returns every non-test .go file in this package directory.
func packageGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(".", name))
	}
	return out
}

// TestNoEngineOrProtocolImport is the mechanical backing for AC-1: no
// non-test file may import the engine or protocol package — this package
// takes topology as a caller-supplied argument and fetches nothing.
func TestNoEngineOrProtocolImport(t *testing.T) {
	for _, path := range packageGoFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		s := string(b)
		for _, banned := range []string{
			`"github.com/aaronukgarcia/Metropolis/internal/engine"`,
			`"github.com/aaronukgarcia/Metropolis/internal/protocol"`,
		} {
			if strings.Contains(s, banned) {
				t.Errorf("%s imports %s — GR#20/AC-1 forbids it", path, banned)
			}
		}
	}
}

// TestNoWallClockInNonTestFiles is the mechanical backing for AC-9: layout
// is topology-driven only, never wall-clock-driven.
func TestNoWallClockInNonTestFiles(t *testing.T) {
	for _, path := range packageGoFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		if strings.Contains(string(b), "time.Now") {
			t.Errorf("%s calls time.Now — diagrams must be pure functions of their input topology (GR#21/AC-9)", path)
		}
	}
}

// TestPackageDocCitesModuleKeyAndSpec is the mechanical backing for AC-11:
// the package doc states the module key and cites the spec sections.
func TestPackageDocCitesModuleKeyAndSpec(t *testing.T) {
	b, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatalf("ReadFile doc.go: %v", err)
	}
	s := string(b)
	for _, want := range []string{"ui.diagrams", "UI-SPEC §2", "§33", "§50", "§54", "never calls back", "SourceID"} {
		if !strings.Contains(s, want) {
			t.Errorf("doc.go missing expected mention of %q", want)
		}
	}
}
