// Command astinfo is a small, read-only helper for tools/plan/codejson-audit.js
// (FEAT-062). It reports, per requested directory, the exported top-level Go
// declarations (funcs, types, vars, consts — the identifiers a code.json
// `inbound.name`/contract type string is meant to name) and the set of
// import paths used by that directory's non-test .go files.
//
// This is deliberately a real go/ast parse, not a text grep: a grep would
// false-positive on a package that merely mentions a word in a comment or a
// string literal, and would miss dot-imports/aliased imports (see AC-3's
// "what a lazy implementation looks like" note in
// docs/planning/acceptance/plan.pipeline.md).
//
// Read-only: this program never writes to any file it inspects.
//
// Usage: go run ./tools/plan/astinfo <dir1> [<dir2> ...]
// Output: JSON object keyed by the input dir string (echoed verbatim so the
// caller can correlate), each value a PkgInfo.
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Symbol is one exported top-level declaration.
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // func | type | var | const
	File string `json:"file"`
	Line int    `json:"line"`
}

// PkgInfo is the astinfo report for a single directory.
type PkgInfo struct {
	Dir      string   `json:"dir"`
	Error    string   `json:"error,omitempty"`
	Exported []Symbol `json:"exported"`
	Imports  []string `json:"imports"`
}

func main() {
	dirs := os.Args[1:]
	out := make(map[string]PkgInfo, len(dirs))
	for _, d := range dirs {
		out[d] = analyzeDir(d)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		os.Exit(1)
	}
}

// analyzeDir parses every non-test .go file directly inside dir (no
// recursion — a code.json module path names one package directory; child
// subpackages are separate directories with their own entries or are
// covered as documented children, per AC-4).
func analyzeDir(dir string) PkgInfo {
	info := PkgInfo{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		info.Error = err.Error()
		return info
	}

	fset := token.NewFileSet()
	importSet := map[string]bool{}
	var symbols []Symbol
	sawGoFile := false
	var parseErrs []string

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.IsDir() {
			continue
		}
		sawGoFile = true
		full := filepath.Join(dir, name)
		f, perr := parser.ParseFile(fset, full, nil, parser.ParseComments)
		if perr != nil {
			parseErrs = append(parseErrs, perr.Error())
			continue
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			importSet[p] = true
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					continue // methods are not top-level contract identifiers
				}
				if d.Name.IsExported() {
					pos := fset.Position(d.Pos())
					symbols = append(symbols, Symbol{Name: d.Name.Name, Kind: "func", File: filepath.Base(pos.Filename), Line: pos.Line})
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							pos := fset.Position(s.Pos())
							symbols = append(symbols, Symbol{Name: s.Name.Name, Kind: "type", File: filepath.Base(pos.Filename), Line: pos.Line})
						}
					case *ast.ValueSpec:
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						for _, n := range s.Names {
							if n.IsExported() {
								pos := fset.Position(n.Pos())
								symbols = append(symbols, Symbol{Name: n.Name, Kind: kind, File: filepath.Base(pos.Filename), Line: pos.Line})
							}
						}
					}
				}
			}
		}
	}

	if len(parseErrs) > 0 {
		info.Error = strings.Join(parseErrs, "; ")
	} else if !sawGoFile {
		info.Error = "no non-test .go files found"
	}

	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Name != symbols[j].Name {
			return symbols[i].Name < symbols[j].Name
		}
		return symbols[i].File < symbols[j].File
	})
	imports := make([]string, 0, len(importSet))
	for k := range importSet {
		imports = append(imports, k)
	}
	sort.Strings(imports)

	info.Exported = symbols
	info.Imports = imports
	return info
}
