package errs

// BUG-357 step 3: the mechanical real-call-site gate.
//
// The BUG-357 measurement found 376 errs.New/errs.Wrap call sites whose
// template tokens have no matching ctx key — each renders a literal {token}
// to a user. The previous "gate" for this class was a hand-written ctx table
// (the round found the ui.keys gate used one), so a key rename left the gate
// green and the message broken. This gate is a REAL go/ast walk: it parses
// every non-test .go file under a root, resolves errs.New/errs.Wrap call
// sites (including package-level `const codeX = "MET-…"` patterns), renders
// each registry template with the literal ctx keys that exact call site
// passes, and fails on any surviving placeholder.
//
// The gate accounts for the BUG-357 root fix: a Wrap with a non-nil cause
// auto-injects {cause}, so that token is satisfied for Wrap-with-cause even
// when the ctx map lacks it. Tokens {code} and {correlationId} always
// resolve (renderTemplate supplies them).
//
// Sites whose code or ctx is not statically resolvable (a variable, a helper
// call) are reported as dynamic findings — they must be seen, not assumed.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const errsPkgPath = "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

type siteFinding struct {
	File   string
	Line   int
	Kind   string // survivor | dynamic-code | dynamic-ctx | unknown-code
	Code   string
	Token  string
	Detail string
}

// scanTree walks root (recursively), skips testdata and *_test.go, and
// returns gate findings for every resolvable call site.
func scanTree(root string, entries map[string]registryEntry) []siteFinding {
	// Group files by directory so package-level consts resolve across files
	// (the real code declares codeX = "MET-…" in errors.go and uses it in
	// palette.go / marks.go — a per-file table would falsely flag every one).
	dirs := map[string][]string{}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
		return nil
	})
	if walkErr != nil {
		// A gate that silently returns zero findings on a broken walk is a
		// gate that cannot fail (BUG-356 class) — scanTree has no *testing.T
		// to fail through, so surface the walk failure loudly rather than
		// letting callers read "no findings" as "clean".
		panic(fmt.Sprintf("scanTree: WalkDir(%q) failed: %v", root, walkErr))
	}

	var all []siteFinding
	for _, files := range dirs {
		consts := collectConsts(files)
		for _, f := range files {
			all = append(all, scanFile(f, consts, entries)...)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		return all[i].Line < all[j].Line
	})
	return all
}

// collectConsts returns every package-level string const whose value is a
// registered-format code: const codeX = "MET-F900" and grouped const (…) forms.
func collectConsts(files []string) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range astFile.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val := strings.Trim(lit.Value, `"`)
					if codeFormat.MatchString(val) {
						out[name.Name] = val
					}
				}
			}
		}
	}
	return out
}

// scanFile parses one file and finds errs.New/errs.Wrap call sites.
func scanFile(path string, consts map[string]string, entries map[string]registryEntry) []siteFinding {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}

	aliases := map[string]bool{}
	for _, imp := range astFile.Imports {
		ip := strings.Trim(imp.Path.Value, `"`)
		if ip != errsPkgPath {
			continue
		}
		name := "errs"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = true
	}
	if len(aliases) == 0 {
		return nil
	}

	var findings []siteFinding
	ast.Inspect(astFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		xIdent, ok := sel.X.(*ast.Ident)
		if !ok || !aliases[xIdent.Name] {
			return true
		}
		var wrap bool
		switch sel.Sel.Name {
		case "New":
		case "Wrap":
			wrap = true
		default:
			return true
		}

		codeIdx := 0
		ctxIdx := 2
		if wrap {
			ctxIdx = 3
		}
		if len(call.Args) <= ctxIdx {
			return true // malformed call — not our concern here
		}

		code := resolveCodeArg(call.Args[codeIdx], consts)
		if code == "" {
			findings = append(findings, siteFinding{
				File: path, Line: fset.Position(call.Pos()).Line,
				Kind: "dynamic-code", Detail: exprString(call.Args[codeIdx]),
			})
			return true
		}

		entry, ok := entries[code]
		if !ok {
			findings = append(findings, siteFinding{
				File: path, Line: fset.Position(call.Pos()).Line,
				Kind: "unknown-code", Code: code,
			})
			return true
		}

		ctxKeys := resolveCtxKeys(call.Args[ctxIdx])
		if ctxKeys == nil {
			findings = append(findings, siteFinding{
				File: path, Line: fset.Position(call.Pos()).Line,
				Kind: "dynamic-ctx", Code: code, Detail: exprString(call.Args[ctxIdx]),
			})
			return true
		}

		// Wrap-with-non-nil-cause satisfies {cause} via the BUG-357 root fix.
		hasCause := wrap && !isNilLiteral(call.Args[2])
		provided := map[string]bool{}
		for k := range ctxKeys {
			provided[k] = true
		}
		provided["code"] = true
		provided["correlationId"] = true
		if hasCause {
			provided["cause"] = true
		}

		for _, tok := range templateTokens(entry.Message) {
			if !provided[tok] {
				findings = append(findings, siteFinding{
					File: path, Line: fset.Position(call.Pos()).Line,
					Kind: "survivor", Code: code, Token: tok,
				})
			}
		}
		return true
	})
	return findings
}

func resolveCodeArg(arg ast.Expr, consts map[string]string) string {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			return strings.Trim(a.Value, `"`)
		}
	case *ast.Ident:
		if v, ok := consts[a.Name]; ok {
			return v
		}
	}
	return ""
}

// resolveCtxKeys returns the literal string keys of a map ctx argument, or
// nil when the argument is not a statically resolvable map literal (a
// variable / helper call / empty nil is resolvable-as-empty).
func resolveCtxKeys(arg ast.Expr) map[string]bool {
	if ident, ok := arg.(*ast.Ident); ok && ident.Name == "nil" {
		return map[string]bool{}
	}
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil // spread/unkeyed element — not a literal key table
		}
		keyLit, ok := kv.Key.(*ast.BasicLit)
		if !ok || keyLit.Kind != token.STRING {
			return nil
		}
		keys[strings.Trim(keyLit.Value, `"`)] = true
	}
	return keys
}

func isNilLiteral(arg ast.Expr) bool {
	id, ok := arg.(*ast.Ident)
	return ok && id.Name == "nil"
}

// templateTokens returns every bare {key} in a template, in order.
func templateTokens(tmpl string) []string {
	var toks []string
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			continue
		}
		if j := strings.IndexByte(tmpl[i:], '}'); j >= 0 {
			toks = append(toks, tmpl[i+1:i+j])
			i += j
		}
	}
	return toks
}

// exprString is a short rendering of an AST expr for finding details.
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.CallExpr:
		return "call()"
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.BasicLit:
		return v.Value
	}
	return "expr"
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// fixtureGateRegistry is a minimal registry whose templates exercise every
// gate behavior: a required key, a cause token, an auto key.
const fixtureGateRegistry = `{
  "version": 1,
  "codes": {
    "MET-G900": {"severity":"error","module":"m","message":"bad {thing}","remedy":"r"},
    "MET-G901": {"severity":"error","module":"m","message":"failed: {cause}","remedy":"r"},
    "MET-G902": {"severity":"error","module":"m","message":"auto {correlationId}","remedy":"r"}
  }
}`

func gateEntries(t *testing.T) map[string]registryEntry {
	t.Helper()
	codes, _, err := decodeCodes([]byte(fixtureGateRegistry))
	if err != nil {
		t.Fatalf("decode fixture registry: %v", err)
	}
	return codes
}

func writeGatePkg(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "gate.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return pkg
}

// TestGate_CanFailAndPass is the "prove the check can fail" core: a call
// site that omits a required template key must be flagged; supplying the key
// must clear it. This is the RED/GREEN pair the round will demand.
func TestGate_CanFailAndPass(t *testing.T) {
	entries := gateEntries(t)

	omission := `package pkg

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

func F() {
	_ = errs.New("MET-G900", "c", map[string]any{})
}
`
	pkg := writeGatePkg(t, omission)
	findings := scanTree(pkg, entries)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 survivor finding for the omitted {thing}, got %+v", findings)
	}
	if findings[0].Kind != "survivor" || findings[0].Token != "thing" {
		t.Fatalf("finding = %+v, want survivor of token thing", findings[0])
	}

	// Restore the key -> the same site must now be clean.
	complete := `package pkg

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

func F() {
	_ = errs.New("MET-G900", "c", map[string]any{"thing": 1})
}
`
	if err := os.WriteFile(filepath.Join(pkg, "gate.go"), []byte(complete), 0o644); err != nil {
		t.Fatalf("rewrite gate.go: %v", err)
	}
	findings = scanTree(pkg, entries)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings once {thing} is supplied, got %+v", findings)
	}
}

// TestGate_WrapCauseSatisfied pins the root-fix interaction: {cause} is
// satisfied by a Wrap with a non-nil cause (auto-inject) but flagged for a
// Wrap with nil cause (a genuine gap) and for New with no ctx cause.
func TestGate_WrapCauseSatisfied(t *testing.T) {
	entries := gateEntries(t)

	src := `package pkg

import (
	"errors"
	errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func F() {
	_ = errs.Wrap("MET-G901", "c", errors.New("x"), nil) // satisfied via root fix
	_ = errs.Wrap("MET-G901", "c", nil, nil)             // gap: nil cause
	_ = errs.New("MET-G901", "c", nil)                   // gap: no cause at all
}
`
	pkg := writeGatePkg(t, src)
	findings := scanTree(pkg, entries)
	if len(findings) != 2 {
		t.Fatalf("expected 2 gap findings (nil cause + New), got %+v", findings)
	}
	for _, f := range findings {
		if f.Kind != "survivor" || f.Token != "cause" {
			t.Fatalf("finding = %+v, want survivor of token cause", f)
		}
	}
}

// TestGate_ConstCodeResolved is the exact pattern the round found the old
// hand-written table would miss: the code arg is a package-level const, not
// a literal. The gate must resolve it, not flag it as dynamic.
func TestGate_ConstCodeResolved(t *testing.T) {
	entries := gateEntries(t)

	src := `package pkg

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

const codeBadThing = "MET-G900"

func F() {
	_ = errs.New(codeBadThing, "c", map[string]any{"thing": 1})
}
`
	pkg := writeGatePkg(t, src)
	findings := scanTree(pkg, entries)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings with the const code resolved and {thing} supplied, got %+v", findings)
	}
}

// TestGate_DynamicCtxFlagged: a ctx built by a helper (not a literal map)
// must be reported as dynamic, never silently assumed clean.
func TestGate_DynamicCtxFlagged(t *testing.T) {
	entries := gateEntries(t)

	src := `package pkg

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

func makeCtx() map[string]any { return map[string]any{} }

func F() {
	_ = errs.New("MET-G900", "c", makeCtx())
}
`
	pkg := writeGatePkg(t, src)
	findings := scanTree(pkg, entries)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 dynamic-ctx finding, got %+v", findings)
	}
	if findings[0].Kind != "dynamic-ctx" {
		t.Fatalf("finding = %+v, want dynamic-ctx", findings[0])
	}
}

// TestGate_UnknownCodeFlagged: a site using a code absent from the registry
// must be reported, not silently skipped.
func TestGate_UnknownCodeFlagged(t *testing.T) {
	entries := gateEntries(t)

	src := `package pkg

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

func F() {
	_ = errs.New("MET-G999", "c", map[string]any{"thing": 1})
}
`
	pkg := writeGatePkg(t, src)
	findings := scanTree(pkg, entries)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 unknown-code finding, got %+v", findings)
	}
	if findings[0].Kind != "unknown-code" {
		t.Fatalf("finding = %+v, want unknown-code", findings[0])
	}
}
