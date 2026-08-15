package proj

// SF-1 (GR#20, structural): this package renders exclusively from the
// int.protocol Delta stream — no direct import of internal/engine in any
// non-test source file. .golangci.yml's depguard ui-must-not-import-engine
// rule already blocks this at lint time; the test below makes the same
// guarantee mechanically verifiable in `go test`, mirroring
// determinism_test.go's file-scan approach (so a CI job that runs only
// `go test` still catches an accidental engine import).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestNoEngineImport scans this package's non-test .go files and fails if
// any imports internal/engine — the SF-1 structural check. _test.go files
// are exempt per the depguard config's own comment (and this package's
// tests import none anyway).
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
			t.Errorf("%s imports internal/engine — this package must consume the engine only via internal/protocol (SF-1/GR#20)", name)
		}
	}
}
