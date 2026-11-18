package flyout

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Criterion 59: the package imports no engine or cmd package; it is pure
// (no clock, no randomness, no goroutines, no I/O, no map range) so a frame
// is a function of the state and the area (GR#21).
func TestPurity_ImportsAndSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, "/internal/engine") || strings.Contains(p, "/cmd/") ||
				p == "time" || p == "math/rand" || p == "os" || p == "net" || p == "sync" || p == "net/http" {
				t.Errorf("%s imports %s", f, p)
			}
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"time.Now(", "go func", "range seen", "rand."} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s contains %q", f, bad)
			}
		}
	}
}
