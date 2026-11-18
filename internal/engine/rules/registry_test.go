package rules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const rulesImportPath = "github.com/aaronukgarcia/Metropolis/internal/engine/rules"

var itemRE = regexp.MustCompile(`^(BUG|FEAT|MOD|INT)-\d+$`)

// currentReaders are the only places (outside this package) that may read
// rules.Current directly: the composition, save/restore, replay and the
// command-line programs. Every module reads it through rules.Active.
var currentReaders = []string{
	"internal/engine/compose/",
	"internal/engine/save/",
	"internal/harness/replay/",
	"cmd/",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// versionConstants parses rules.go and returns every Version-typed
// constant (other than Current) with its integer value.
func versionConstants(t *testing.T, path string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	vals := map[string]int{}
	idents := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				switch v := vs.Values[i].(type) {
				case *ast.BasicLit:
					if n, err2 := strconv.Atoi(v.Value); err2 == nil {
						vals[vs.Names[i].Name] = n
					}
				case *ast.Ident:
					idents[n.Name] = v.Name
				}
			}
		}
	}
	for n, target := range idents {
		if v, ok := vals[target]; ok {
			vals[n] = v
		}
	}
	return vals
}

// validate returns every problem with a registry. It takes the entries,
// the declared constants, the Current value and the repo root so tests can
// feed it deliberately broken registries.
func validate(entries []Rule, consts map[string]int, current Version, root string, used map[string]bool) []string {
	var p []string
	if len(entries) == 0 {
		return []string{"registry is empty"}
	}
	prevDate := ""
	declared := map[string]bool{}
	for i, e := range entries {
		want := Version(i + 1)
		if e.Number != want {
			p = append(p, "entry "+strconv.Itoa(i)+": number "+strconv.Itoa(int(e.Number))+", want "+strconv.Itoa(int(want))+" (contiguous from 1, ascending, no duplicates)")
		}
		if e.Const == "" {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": empty Const")
		} else {
			declared[e.Const] = true
			if v, ok := consts[e.Const]; !ok {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": constant "+e.Const+" is not declared in rules.go")
			} else if Version(v) != e.Number {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": constant "+e.Const+" = "+strconv.Itoa(v)+", not the entry number")
			}
		}
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": date "+strconv.Quote(e.Date)+" is not an ISO date")
		} else {
			if prevDate != "" && e.Date < prevDate {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": date "+e.Date+" goes backwards from "+prevDate)
			}
			prevDate = e.Date
		}
		if !itemRE.MatchString(e.Item) {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": item "+strconv.Quote(e.Item)+" is not a BOW code")
		}
		if strings.TrimSpace(e.Old) == "" || strings.TrimSpace(e.New) == "" {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": Old and New must both be non-empty one-liners")
		}
		if strings.ContainsAny(e.Old, "\r\n") || strings.ContainsAny(e.New, "\r\n") {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": Old and New must be one line each")
		}
		if strings.TrimSpace(e.Fixture) == "" {
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": empty Fixture path")
		}
		switch e.Status {
		case StatusPendingWiring:
			if !itemRE.MatchString(e.PendingItem) {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": pending-wiring needs a PendingItem BOW code")
			}
			if used[e.Const] {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": a call site uses "+e.Const+" but the entry still says pending-wiring; mark it wired")
			}
		case StatusWired:
			if e.PendingItem != "" {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": a wired entry must not carry a PendingItem")
			}
			if !used[e.Const] {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": wired, but no non-test call site uses "+e.Const)
			}
			dir := filepath.Join(root, filepath.FromSlash(e.Fixture))
			ents, err := os.ReadDir(dir)
			if err != nil || len(ents) == 0 {
				p = append(p, "entry "+strconv.Itoa(int(e.Number))+": wired, but fixture directory "+e.Fixture+" is missing or empty")
			}
		default:
			p = append(p, "entry "+strconv.Itoa(int(e.Number))+": unknown status "+strconv.Quote(string(e.Status)))
		}
	}
	for name := range consts {
		if name != "Current" && !declared[name] {
			p = append(p, "constant "+name+" has no registry entry")
		}
	}
	if last := entries[len(entries)-1].Number; current != last {
		p = append(p, "Current is "+strconv.Itoa(int(current))+", the last entry is "+strconv.Itoa(int(last)))
	}
	sort.Strings(p)
	return p
}

// scanResult is what the source scan found.
type scanResult struct {
	problems []string
	used     map[string]bool // constants referenced by non-test files outside rules
}

// scanTree reads every .go file under root/internal and root/cmd (except
// this package) and checks Has call sites and Current reads.
func scanTree(root string, names map[string]bool) (scanResult, error) {
	res := scanResult{used: map[string]bool{}}
	for _, top := range []string{"internal", "cmd"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "node_modules", "temp", ".claude":
					return filepath.SkipDir
				}
				if rel == "internal/engine/rules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			fset := token.NewFileSet()
			head, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			alias := ""
			for _, imp := range head.Imports {
				if strings.Trim(imp.Path.Value, "\"`") == rulesImportPath {
					alias = "rules"
					if imp.Name != nil {
						alias = imp.Name.Name
					}
				}
			}
			if alias == "" {
				return nil
			}
			fset = token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			isTest := strings.HasSuffix(path, "_test.go")
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Has" || !isIdent(sel.X, alias) {
						return true
					}
					pos := fset.Position(x.Pos())
					where := rel + ":" + strconv.Itoa(pos.Line)
					if len(x.Args) != 2 {
						res.problems = append(res.problems, where+": rules.Has needs exactly two arguments")
						return true
					}
					s2, ok := x.Args[1].(*ast.SelectorExpr)
					if !ok || !isIdent(s2.X, alias) || !names[s2.Sel.Name] {
						res.problems = append(res.problems, where+": rules.Has second argument must be a named rule constant from the registry, never a literal or expression")
					}
				case *ast.SelectorExpr:
					if !isIdent(x.X, alias) || isTest {
						return true
					}
					if names[x.Sel.Name] {
						res.used[x.Sel.Name] = true
					}
					if x.Sel.Name == "Current" {
						allowed := false
						for _, pre := range currentReaders {
							if strings.HasPrefix(rel, pre) {
								allowed = true
							}
						}
						if !allowed {
							pos := fset.Position(x.Pos())
							res.problems = append(res.problems, rel+":"+strconv.Itoa(pos.Line)+": rules.Current read outside the composition, save/restore, replay and cmd code; use rules.Active")
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return res, err
		}
	}
	sort.Strings(res.problems)
	return res, nil
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func constNames(entries []Rule) map[string]bool {
	m := map[string]bool{}
	for _, e := range entries {
		m[e.Const] = true
	}
	return m
}

// TestRegistry_LiveTree is C1 to C4 over the real registry and the real
// source tree.
func TestRegistry_LiveTree(t *testing.T) {
	root := repoRoot(t)
	consts := versionConstants(t, "rules.go")
	scan, err := scanTree(root, constNames(registry))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, s := range scan.problems {
		t.Errorf("source scan: %s", s)
	}
	for _, s := range validate(registry, consts, Current, root, scan.used) {
		t.Errorf("registry: %s", s)
	}
}

func goodEntry() Rule {
	return Rule{Number: 1, Const: "A", Date: "2026-11-18", Item: "BUG-1", Old: "old", New: "new", Fixture: "x/v1", Status: StatusPendingWiring, PendingItem: "FEAT-1"}
}

// TestValidate_CanFail proves each registry check can fail (C1 to C4).
func TestValidate_CanFail(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "has", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "has", "v1", "journal.dat"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	two := func() []Rule {
		a, b := goodEntry(), goodEntry()
		b.Number, b.Const = 2, "B"
		return []Rule{a, b}
	}
	cases := []struct {
		name    string
		mut     func() ([]Rule, map[string]int, Version, map[string]bool)
		wantSub string
	}{
		{"baseline passes", func() ([]Rule, map[string]int, Version, map[string]bool) {
			return []Rule{goodEntry()}, map[string]int{"A": 1}, 1, nil
		}, ""},
		{"gap in numbers", func() ([]Rule, map[string]int, Version, map[string]bool) {
			a, b := goodEntry(), goodEntry()
			b.Number, b.Const = 3, "B"
			return []Rule{a, b}, map[string]int{"A": 1, "B": 3}, 3, nil
		}, "contiguous"},
		{"duplicate number", func() ([]Rule, map[string]int, Version, map[string]bool) {
			a, b := goodEntry(), goodEntry()
			b.Const = "B"
			return []Rule{a, b}, map[string]int{"A": 1, "B": 1}, 1, nil
		}, "contiguous"},
		{"current not last", func() ([]Rule, map[string]int, Version, map[string]bool) {
			return []Rule{goodEntry()}, map[string]int{"A": 1}, 2, nil
		}, "Current is 2"},
		{"date backwards", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := two()
			e[1].Date = "2026-11-17"
			return e, map[string]int{"A": 1, "B": 2}, 2, nil
		}, "goes backwards"},
		{"bad date", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Date = "18/11/2026"
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "not an ISO date"},
		{"bad item", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Item = "1224"
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "not a BOW code"},
		{"empty old", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Old = " "
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "non-empty"},
		{"empty new", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.New = ""
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "non-empty"},
		{"multi-line old", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Old = "a\nb"
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "one line"},
		{"empty fixture path", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Fixture = ""
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "empty Fixture"},
		{"pending without item", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.PendingItem = ""
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "PendingItem"},
		{"pending but used", func() ([]Rule, map[string]int, Version, map[string]bool) {
			return []Rule{goodEntry()}, map[string]int{"A": 1}, 1, map[string]bool{"A": true}
		}, "mark it wired"},
		{"wired but unused", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Status, e.PendingItem, e.Fixture = StatusWired, "", "has/v1"
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "no non-test call site"},
		{"wired with missing fixture", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Status, e.PendingItem, e.Fixture = StatusWired, "", "has/missing"
			return []Rule{e}, map[string]int{"A": 1}, 1, map[string]bool{"A": true}
		}, "missing or empty"},
		{"wired ok", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Status, e.PendingItem, e.Fixture = StatusWired, "", "has/v1"
			return []Rule{e}, map[string]int{"A": 1}, 1, map[string]bool{"A": true}
		}, ""},
		{"constant value differs", func() ([]Rule, map[string]int, Version, map[string]bool) {
			return []Rule{goodEntry()}, map[string]int{"A": 7}, 1, nil
		}, "not the entry number"},
		{"constant without entry", func() ([]Rule, map[string]int, Version, map[string]bool) {
			return []Rule{goodEntry()}, map[string]int{"A": 1, "Z": 2}, 1, nil
		}, "no registry entry"},
		{"unknown status", func() ([]Rule, map[string]int, Version, map[string]bool) {
			e := goodEntry()
			e.Status = "done"
			return []Rule{e}, map[string]int{"A": 1}, 1, nil
		}, "unknown status"},
	}
	for _, c := range cases {
		entries, consts, cur, used := c.mut()
		got := strings.Join(validate(entries, consts, cur, root, used), "\n")
		if c.wantSub == "" && got != "" {
			t.Errorf("%s: unexpected problems:\n%s", c.name, got)
		}
		if c.wantSub != "" && !strings.Contains(got, c.wantSub) {
			t.Errorf("%s: want a problem containing %q, got:\n%s", c.name, c.wantSub, got)
		}
	}
}

// TestScanTree_CanFail proves the source scan catches a literal at a Has
// call site, a Current read from a module, and records real uses (C4).
func TestScanTree_CanFail(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hdr := "package p\n\nimport \"" + rulesImportPath + "\"\n\n"
	write("internal/engine/build/good.go", hdr+"func f(a rules.Version) bool { return rules.Has(a, rules.Alpha) }\n")
	write("internal/engine/build/lit.go", hdr+"func g(a rules.Version) bool { return rules.Has(a, 1) }\n")
	write("internal/engine/build/cast.go", hdr+"func h(a rules.Version) bool { return rules.Has(a, rules.Version(1)) }\n")
	write("internal/engine/build/cur.go", hdr+"func i() rules.Version { return rules.Current }\n")
	write("internal/engine/compose/cur.go", hdr+"func j() rules.Version { return rules.Current }\n")
	write("internal/engine/build/lit_test.go", hdr+"func k(a rules.Version) bool { return rules.Has(a, 2) }\n")
	write("internal/engine/other/unrelated.go", "package q\n\ntype T struct{}\n\nfunc (T) Has(a, b int) bool { return a > b }\n\nfunc u(t T) bool { return t.Has(1, 2) }\n")
	res, err := scanTree(root, map[string]bool{"Alpha": true})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(res.problems, "\n")
	for _, want := range []string{"build/lit.go:5", "build/cast.go:5", "build/cur.go:5", "build/lit_test.go:5"} {
		if !strings.Contains(got, want) {
			t.Errorf("scan missed %s; problems:\n%s", want, got)
		}
	}
	for _, bad := range []string{"build/good.go", "compose/cur.go", "unrelated.go"} {
		if strings.Contains(got, bad) {
			t.Errorf("scan flagged %s wrongly; problems:\n%s", bad, got)
		}
	}
	if !res.used["Alpha"] {
		t.Error("scan did not record Alpha as used by build/good.go")
	}
}

func TestHasAndActive(t *testing.T) {
	if Has(0, RefuseOccupiedCell) {
		t.Error("version 0 must not have the version 1 rule")
	}
	if !Has(1, RefuseOccupiedCell) || !Has(2, RefuseOccupiedCell) {
		t.Error("versions 1 and later must have the version 1 rule")
	}
	if Active(nil) != Current {
		t.Error("Active(nil) must be Current")
	}
	if Active(func() Version { return 0 }) != 0 {
		t.Error("Active must return the getter's value")
	}
}

func TestClassifyAndIsKnown(t *testing.T) {
	old := OldestSupported
	defer func() { OldestSupported = old }()
	OldestSupported = 0
	cases := []struct {
		v     Version
		known bool
		want  Support
	}{
		{-1, false, SupportInvalid},
		{0, true, SupportOK},
		{Current, true, SupportOK},
		{Current + 1, false, SupportTooNew},
	}
	for _, c := range cases {
		if IsKnown(c.v) != c.known {
			t.Errorf("IsKnown(%d) = %v", c.v, !c.known)
		}
		if got := Classify(c.v); got != c.want {
			t.Errorf("Classify(%d) = %d, want %d", c.v, got, c.want)
		}
	}
	OldestSupported = 1
	if Classify(0) != SupportTooOld {
		t.Error("with OldestSupported = 1, version 0 must be too old")
	}
	if Classify(1) != SupportOK {
		t.Error("with OldestSupported = 1, version 1 must be ok")
	}
}

func TestEntryBetweenDescribe(t *testing.T) {
	if _, ok := Entry(0); ok {
		t.Error("Entry(0) must not exist")
	}
	e, ok := Entry(RefuseOccupiedCell)
	if !ok || e.Item != "BUG-1224" {
		t.Fatalf("Entry(1) = %+v, %v", e, ok)
	}
	if got := Between(0, Current); len(got) != int(Current) {
		t.Errorf("Between(0, Current) has %d entries, want %d", len(got), Current)
	}
	if got := Between(Current, Current); len(got) != 0 {
		t.Errorf("Between(Current, Current) has %d entries, want none", len(got))
	}
	d := Describe(0, Current)
	for _, want := range []string{"rule 1", "2026-11-18", "BUG-1224", e.Old, e.New} {
		if !strings.Contains(d, want) {
			t.Errorf("Describe missing %q: %s", want, d)
		}
	}
	if Describe(Current, Current) != "" {
		t.Error("Describe over nothing crossed must be empty")
	}
}

// TestRegistry_ReturnsCopy: mutating the returned slice must not change the registry.
func TestRegistry_ReturnsCopy(t *testing.T) {
	r := Registry()
	r[0].Old = "tampered"
	if registry[0].Old == "tampered" {
		t.Fatal("Registry() aliases the live registry")
	}
}

// TestBoundaryIsDocumented: the ruling that only command behaviour is
// versioned must stay written in the package documentation and the
// registry's own description (Aaron 2026-11-18).
func TestBoundaryIsDocumented(t *testing.T) {
	for _, f := range []string{"doc.go", "registry.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.Contains(s, "daily simulation formula") && !strings.Contains(s, "daily simulation formulas") {
			t.Errorf("%s no longer states that daily simulation formulas are not versioned", f)
		}
	}
}
