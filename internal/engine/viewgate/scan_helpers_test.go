package viewgate

// Shared source-scan plumbing for the viewgate static gates (FEAT-231).
//
// Both gates in this package are pure source scanners over the same file
// set — every non-test .go file under internal/ and cmd/. V1
// (viewgate_test.go, the one-view-registry gate) and V2
// (drilltarget_gate_test.go, the one-DrillTarget-type gate) share this
// walker so the "which files does the gate see" rule lives in exactly one
// place: change the exclusion policy here and both gates move in lockstep.
//
// findRepoRoot and calleeName also live in viewgate_test.go and are shared
// across the package's test files the same way.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// walkGoSourceFiles parses every non-test .go file under repoRoot/<dir> for
// each dir in dirs and invokes fn with the parsed file and its repo-root
// relative, forward-slash separated path. Positions taken from nodes in
// file resolve against the caller-supplied fset (so a caller that needs
// line numbers passes the same fset it later queries). _test.go files are
// excluded deliberately — the gates' own negative-control fixtures are
// _test.go and use intentionally-illegal shapes; scanning them would make
// the fixtures fail the live gate.
func walkGoSourceFiles(t testing.TB, fset *token.FileSet, repoRoot string, dirs []string, fn func(file *ast.File, rel string)) {
	t.Helper()
	for _, d := range dirs {
		root := filepath.Join(repoRoot, d)
		err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("parsing %s: %v", path, perr)
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				rel = path
			}
			fn(file, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}
}
