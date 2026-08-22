package census

// AC-1 (GR#20; code.json outbound contract): F6 reaches engine.census
// exclusively through an int.protocol view subscription -- no direct
// import of internal/engine/census types in this package's non-test
// source. Mechanical check: `go list -deps
// ./internal/ui/screens/census/...` shows no import of internal/engine/...
// (test files exempt per README.md's _test.go depguard carve-out,
// fixtures only). This test encodes the same check TestNoEngineImport
// establishes for ui.screen.services/ui.screen.finance.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNoEngineImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte(`"github.com/aaronukgarcia/Metropolis/internal/engine`)
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
			t.Errorf("%s imports internal/engine -- this package must consume engine.census exclusively via int.protocol view subscriptions (AC-1/GR#20)", name)
		}
	}
}
