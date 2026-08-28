package viewgate

// FEAT-231 increment V1 — the "one view registry" static gate.
//
// This file is the mechanical enforcement of the doctrine described in
// doc.go: every view-name string literal used AS a view name anywhere
// under internal/ or cmd/ must be a member of the registered set that
// compose.viewRegistrationOrder publishes, OR an explicitly-accepted,
// pending-wiring name in accepted-views.json.
//
// Structure mirrors internal/foundation/errs/source_scan_test.go: the
// scan (scanViewNames), the registry extraction (extractRegistry), and
// the pure check (verifyViewNames) are separate so the check can be
// driven with fixture data in the negative controls, independent of the
// live tree. The one live-tree gate is TestAllViewNamesRegistered.
//
// How the registered set is recovered from SOURCE (never by importing
// compose — see doc.go): extractRegistry parses every non-test .go file
// in internal/engine/compose, collects the string-const values assigned
// to the identifiers listed as `name:` entries of the viewRegistrationOrder
// slice literal, and treats exactly those strings as registered. Nothing
// here hardcodes the view names (GR#15) — deleting an entry from
// compose's viewRegistrationOrder removes it from this gate's registry in
// lockstep.
//
// What is scanned AS a view name (structural, never a raw literal-pattern
// sweep — tile IDs like "f1.population" match the same fN.<seg> shape but
// are NOT view names, so a text sweep would false-positive on them):
//
//   - a composite-literal field `ViewName: "..."` (DrillTarget,
//     protocol.SubscribePayload, dash.TableRow.Drill, ...);
//   - the first argument of a `NewDrillTarget("...", ...)` call;
//   - the string-const value of any identifier whose name contains
//     "ViewSubscriptionName" (every screen's own view const, and
//     compose's backing consts, follow that convention).
//
// Every collected literal is validated against the int.protocol view-name
// grammar (protocol.ValidateViewName — the SSOT, reused rather than
// re-implemented) and then must be registered-or-accepted.
//
// Known blind spots (documented rather than silently unhandled — a
// scanner with unadvertised false negatives is worse than no scanner):
//
//   - _test.go files are excluded outright (fixtures deliberately use
//     unregistered names like "f9.not-a-view", "f2.ledger", "f3.market"
//     to exercise rejection paths; scanning them would demand those be
//     registered, defeating their purpose).
//   - A view name assembled dynamically (e.g. demo's
//     `"household.typology." + t.Typology`) is a BinaryExpr, not a
//     BasicLit, in the ViewName position, so its literal prefix is not
//     collected. This is deliberate: entity-scoped view names
//     legitimately carry a runtime ID segment, so dynamic construction of
//     a view name is NOT bannable the way a dynamically-built MET- code
//     is (contrast source_scan_test.go's BUG-038). The gate checks only
//     the fully-literal names it can resolve.
//   - A screen view const NOT named "*ViewSubscriptionName" (e.g.
//     ui.screen.chrome's ViewChrome = "chrome.topbar", which happens to
//     be registered) is not collected via the const rule; if such a name
//     is also used in a ViewName field / NewDrillTarget call it is still
//     caught there. A brand-new, differently-named, unregistered const
//     used nowhere else would evade — acceptable residual gap for V1.
//   - FEAT-042 coupling: once protocol.TargetRef lands as the sanctioned
//     view-name carrier, its literals become another scan site and its
//     type-registered names another registry source. TODO(FEAT-042): add
//     TargetRef literals to scanViewNames and, if it introduces its own
//     registered set, union it into extractRegistry — do not block on it.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// viewNameLit is one view-name string literal found in scanned source,
// with enough location info to build an actionable failure message.
type viewNameLit struct {
	Name string
	File string // relative to repo root, forward-slash separated
	Line int
}

// findRepoRoot ascends from this test file's own directory until it finds
// go.mod, returning that directory. Using runtime.Caller keeps the gate
// independent of the working directory `go test` is invoked from.
func findRepoRoot(t testing.TB) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate this test file to resolve repo root")
	}
	dir := filepath.Dir(self)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod ascending from %s", filepath.Dir(self))
		}
		dir = parent
	}
}

// extractRegistry recovers the registered view-name set from compose
// SOURCE: it parses every non-test .go file under composeDir, builds a
// map of string-const identifier -> value, finds the viewRegistrationOrder
// slice literal, and returns the set of const values referenced by its
// `name:` entries. A `name:` entry given as a direct string literal
// (rather than an identifier) is honoured too. Returns an error, never a
// silent empty set, if viewRegistrationOrder cannot be found or resolves
// to nothing — an empty registry would make the whole gate vacuous.
func extractRegistry(t testing.TB, composeDir string) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()

	stringConsts := map[string]string{}
	var nameExprs []ast.Expr
	foundOrder := false

	entries, err := os.ReadDir(composeDir)
	if err != nil {
		t.Fatalf("reading compose dir %s: %v", composeDir, err)
	}
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".go") || strings.HasSuffix(de.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(composeDir, de.Name())
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				// Collect any string-const identifier -> value.
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, uerr := strconv.Unquote(lit.Value); uerr == nil {
						stringConsts[name.Name] = v
					}
				}
				// Collect the viewRegistrationOrder slice's `name:` exprs.
				if name.Name == "viewRegistrationOrder" {
					foundOrder = true
					cl, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, elt := range cl.Elts {
						ecl, ok := elt.(*ast.CompositeLit)
						if !ok {
							continue
						}
						for _, f := range ecl.Elts {
							kv, ok := f.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							if kid, ok := kv.Key.(*ast.Ident); ok && kid.Name == "name" {
								nameExprs = append(nameExprs, kv.Value)
							}
						}
					}
				}
			}
			return true
		})
	}

	if !foundOrder {
		t.Fatalf("extractRegistry: no viewRegistrationOrder var found under %s — the gate cannot recover the registered view set from source (did compose move it?)", composeDir)
	}

	registry := map[string]struct{}{}
	for _, e := range nameExprs {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				if s, uerr := strconv.Unquote(v.Value); uerr == nil {
					registry[s] = struct{}{}
				}
			}
		case *ast.Ident:
			if s, ok := stringConsts[v.Name]; ok {
				registry[s] = struct{}{}
			} else {
				t.Fatalf("extractRegistry: viewRegistrationOrder references name %q which is not a resolvable string const in compose source", v.Name)
			}
		}
	}
	if len(registry) == 0 {
		t.Fatal("extractRegistry: viewRegistrationOrder resolved to zero registered view names — the scanner is broken, not that compose registered nothing")
	}
	return registry
}

// scanViewNames walks every non-test .go file under repoRoot/<dir> for
// each dir in dirs and collects view-name string literals used in the
// three view-name positions documented at the top of this file.
func scanViewNames(t testing.TB, repoRoot string, dirs ...string) []viewNameLit {
	t.Helper()
	fset := token.NewFileSet()
	var out []viewNameLit

	record := func(rel string, lit *ast.BasicLit) {
		if lit.Kind != token.STRING {
			return
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		pos := fset.Position(lit.Pos())
		out = append(out, viewNameLit{Name: s, File: rel, Line: pos.Line})
	}

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
			rel = filepath.ToSlash(rel)

			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.KeyValueExpr:
					// `ViewName: "..."` in any composite literal.
					if kid, ok := node.Key.(*ast.Ident); ok && kid.Name == "ViewName" {
						if lit, ok := node.Value.(*ast.BasicLit); ok {
							record(rel, lit)
						}
					}
				case *ast.ValueSpec:
					// const/var whose name follows the *ViewSubscriptionName
					// convention, assigned a string literal.
					for i, name := range node.Names {
						if i >= len(node.Values) {
							continue
						}
						if !strings.Contains(strings.ToLower(name.Name), "viewsubscriptionname") {
							continue
						}
						if lit, ok := node.Values[i].(*ast.BasicLit); ok {
							record(rel, lit)
						}
					}
				case *ast.CallExpr:
					// NewDrillTarget("...", ...) first arg.
					if calleeName(node.Fun) == "NewDrillTarget" && len(node.Args) >= 1 {
						if lit, ok := node.Args[0].(*ast.BasicLit); ok {
							record(rel, lit)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// calleeName returns the bare function name of a call target, resolving
// both a plain identifier (`NewDrillTarget`) and a package selector
// (`dash.NewDrillTarget`).
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// acceptedView is one entry of the accepted-views.json ratchet.
type acceptedView struct {
	View   string `json:"view"`
	Reason string `json:"reason"`
}

// loadAllowlist reads accepted-views.json (the ratchet of known
// unregistered, pending-wiring view names) into a view -> reason map.
func loadAllowlist(t testing.TB, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading allowlist %s: %v", path, err)
	}
	var list []acceptedView
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decoding allowlist %s: %v", path, err)
	}
	out := make(map[string]string, len(list))
	for _, a := range list {
		if strings.TrimSpace(a.View) == "" || strings.TrimSpace(a.Reason) == "" {
			t.Fatalf("allowlist %s: every entry must have a non-empty view and reason (got %+v)", path, a)
		}
		out[a.View] = a.Reason
	}
	return out
}

// verifyViewNames is the pure check at the heart of the gate: given every
// view-name literal found, the registered set, and the accepted-views
// allowlist, it returns one human-readable violation per problem. An
// empty result means every view name in the tree is either registered or
// explicitly accepted. Kept free of I/O so the negative controls can
// drive it with fixture data.
func verifyViewNames(found []viewNameLit, registry map[string]struct{}, allowlist map[string]string) []string {
	var violations []string
	for _, v := range found {
		if err := protocol.ValidateViewName(v.Name); err != nil {
			violations = append(violations, format(v,
				"%q is not a grammar-valid int.protocol view name (%v) — a ViewName must be lowercase dot-separated segments",
				v.Name, err))
			continue
		}
		if _, ok := registry[v.Name]; ok {
			continue
		}
		if _, ok := allowlist[v.Name]; ok {
			continue
		}
		violations = append(violations, format(v,
			"%q is used as a view name but is NOT a member of compose.viewRegistrationOrder (the one view registry) "+
				"and is not accepted in accepted-views.json — a Subscribe to it is rejected at runtime and the screen renders blank "+
				"(FEAT-231 one-view-registry doctrine). Register it in compose's viewRegistrationOrder, or, if it is known-unwired "+
				"work-in-progress, add it to accepted-views.json with a TODO/BOW reference",
			v.Name))
	}
	return violations
}

func format(v viewNameLit, msg string, args ...any) string {
	return fmt.Sprintf("%s:%d: %s", v.File, v.Line, fmt.Sprintf(msg, args...))
}

// --- the live-tree gate ---

// TestAllViewNamesRegistered is the FEAT-231 V1 gate: every view-name
// literal used under internal/ or cmd/ must be registered in compose's
// viewRegistrationOrder or accepted in accepted-views.json. Both the
// registry and the allowlist are read from source/data at test time —
// nothing here is a hardcoded list of view names (GR#15).
func TestAllViewNamesRegistered(t *testing.T) {
	repoRoot := findRepoRoot(t)

	registry := extractRegistry(t, filepath.Join(repoRoot, "internal", "engine", "compose"))
	allowlist := loadAllowlist(t, filepath.Join(repoRoot, "internal", "engine", "viewgate", "accepted-views.json"))

	found := scanViewNames(t, repoRoot, "internal", "cmd")
	if len(found) == 0 {
		t.Fatal("scanViewNames found zero view-name literals under internal/ or cmd/ — the scanner is almost certainly broken (repoRoot resolution, walk paths, or the position rules), not that the tree stopped using views")
	}
	// Non-vacuous sanity: at least one registered name must actually be
	// observed in the scanned tree, or the gate is trivially green.
	sawRegistered := false
	for _, v := range found {
		if _, ok := registry[v.Name]; ok {
			sawRegistered = true
			break
		}
	}
	if !sawRegistered {
		t.Fatal("scan found view-name literals but none of them are registered — extractRegistry and scanViewNames are out of sync; the gate would be vacuous")
	}

	violations := verifyViewNames(found, registry, allowlist)
	if len(violations) > 0 {
		t.Errorf("%d unregistered view-name literal(s) found (FEAT-231 one-view-registry doctrine):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// --- negative controls: prove the gate actually fails, driven by fixture
// data / a fixture source tree rather than a deliberate violation left in
// the live tree (BUG-230: a gate that cannot be shown to fail is worthless). ---

func fixtureRegistry() map[string]struct{} {
	return map[string]struct{}{"f1.viewport": {}, "f2.finance": {}}
}

// TestVerifyViewNames_CatchesUnregistered proves an unregistered,
// un-accepted screen-scoped name fails, and that adding it to the
// allowlist (the ratchet) makes it pass — i.e. the allowlist is
// load-bearing, not decorative.
func TestVerifyViewNames_CatchesUnregistered(t *testing.T) {
	found := []viewNameLit{{Name: "f9.bogus", File: "internal/ui/screens/bogus/wire.go", Line: 12}}

	if v := verifyViewNames(found, fixtureRegistry(), map[string]string{}); len(v) != 1 {
		t.Fatalf("expected exactly 1 violation for an unregistered view name, got %d: %v", len(v), v)
	} else {
		for _, want := range []string{"f9.bogus", "internal/ui/screens/bogus/wire.go:12", "one view registry"} {
			if !strings.Contains(v[0], want) {
				t.Errorf("violation %q missing expected substring %q", v[0], want)
			}
		}
	}

	// Same name, now accepted -> no violation (proves the ratchet works).
	allow := map[string]string{"f9.bogus": "known WIP, BOW-XXXX"}
	if v := verifyViewNames(found, fixtureRegistry(), allow); len(v) != 0 {
		t.Fatalf("expected no violation once the name is accepted, got: %v", v)
	}
}

// TestVerifyViewNames_CatchesMalformed proves a non-grammar view name is
// rejected even before the registry check.
func TestVerifyViewNames_CatchesMalformed(t *testing.T) {
	found := []viewNameLit{{Name: "NOT VALID", File: "internal/x/y.go", Line: 3}}
	v := verifyViewNames(found, fixtureRegistry(), map[string]string{})
	if len(v) != 1 {
		t.Fatalf("expected exactly 1 violation for a malformed name, got %d: %v", len(v), v)
	}
	if !strings.Contains(v[0], "grammar-valid") {
		t.Errorf("violation %q missing expected malformed-name wording", v[0])
	}
}

// TestVerifyViewNames_RegisteredAndAcceptedPass proves clean input
// produces no violations, via both routes (registered + accepted).
func TestVerifyViewNames_RegisteredAndAcceptedPass(t *testing.T) {
	found := []viewNameLit{
		{Name: "f1.viewport", File: "a.go", Line: 1},
		{Name: "f2.finance", File: "b.go", Line: 2},
		{Name: "f3.build", File: "c.go", Line: 3},
	}
	allow := map[string]string{"f3.build": "screen built, not yet in viewRegistrationOrder"}
	if v := verifyViewNames(found, fixtureRegistry(), allow); len(v) != 0 {
		t.Fatalf("expected no violations for registered+accepted input, got: %v", v)
	}
}

// TestScanViewNames_FixtureTree proves the scanner (a) collects literals
// from all three view-name positions, (b) ignores tile-ID literals of the
// same fN.<seg> shape that are NOT in a view-name position, and (c)
// ignores _test.go files — and, run through verifyViewNames with an empty
// allowlist, that a bogus view name in the fixture makes the gate RED
// while its absence leaves it GREEN (the "fixture present -> RED, remove
// -> GREEN" proof, using a tree the scanner actually reads).
func TestScanViewNames_FixtureTree(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "fixture")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Non-test source: one view-name const, one ViewName field literal,
	// one NewDrillTarget call — plus a tile-ID literal in a NON-view-name
	// position ("f1.population" as a tile id) that must NOT be collected.
	src := `package fixture

const ViewSubscriptionName = "f1.viewport"

type DrillTarget struct{ ViewName, EntityID string }

func NewDrillTarget(v, e string) (DrillTarget, error) { return DrillTarget{v, e}, nil }
func NewBignumTile(id string, d DrillTarget) {}

func build() {
	_ = DrillTarget{ViewName: "f2.finance"}
	_, _ = NewDrillTarget("f4.services", "line-1")
	// "f1.population" here is a TILE ID, not a view name.
	NewBignumTile("f1.population", DrillTarget{ViewName: "f1.viewport"})
	// This comment mentions "f9.commentonly" but never uses it.
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture.go: %v", err)
	}
	// _test.go source with an unregistered name must be ignored entirely.
	testSrc := `package fixture

func x() { _ = DrillTarget{ViewName: "f9.testonly"} }
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write fixture_test.go: %v", err)
	}

	found := scanViewNames(t, dir, "internal")
	names := map[string]bool{}
	for _, v := range found {
		names[v.Name] = true
	}
	for _, want := range []string{"f1.viewport", "f2.finance", "f4.services"} {
		if !names[want] {
			t.Errorf("expected scanner to collect view name %q from its position", want)
		}
	}
	if names["f1.population"] {
		t.Error("f1.population is a tile ID (not in a view-name position) and must NOT be collected")
	}
	if names["f9.commentonly"] {
		t.Error("f9.commentonly appears only in a comment and must NOT be collected")
	}
	if names["f9.testonly"] {
		t.Error("f9.testonly lives in a _test.go file and must NOT be collected")
	}

	reg := map[string]struct{}{"f1.viewport": {}, "f2.finance": {}, "f4.services": {}}

	// GREEN: with only registered names, no violation.
	if v := verifyViewNames(found, reg, map[string]string{}); len(v) != 0 {
		t.Fatalf("expected GREEN on the clean fixture, got: %v", v)
	}

	// RED: drop a bogus unregistered view name into the fixture tree and
	// rescan — the gate must now fail.
	bogus := `package fixture

func y() { _ = DrillTarget{ViewName: "f9.bogus"} }
`
	if err := os.WriteFile(filepath.Join(pkgDir, "bogus.go"), []byte(bogus), 0o644); err != nil {
		t.Fatalf("write bogus.go: %v", err)
	}
	found2 := scanViewNames(t, dir, "internal")
	v2 := verifyViewNames(found2, reg, map[string]string{})
	if len(v2) != 1 || !strings.Contains(v2[0], "f9.bogus") {
		t.Fatalf("expected exactly 1 violation naming f9.bogus after adding the fixture, got: %v", v2)
	}

	// GREEN again once the fixture file is removed.
	if err := os.Remove(filepath.Join(pkgDir, "bogus.go")); err != nil {
		t.Fatalf("remove bogus.go: %v", err)
	}
	if v3 := verifyViewNames(scanViewNames(t, dir, "internal"), reg, map[string]string{}); len(v3) != 0 {
		t.Fatalf("expected GREEN after removing the bogus fixture, got: %v", v3)
	}
}

// TestExtractRegistry_LiveComposeIsNonEmpty proves the source-based
// registry recovery actually resolves the live compose registration order
// to a non-trivial set (guards against a silent parse regression turning
// the whole gate vacuous).
func TestExtractRegistry_LiveComposeIsNonEmpty(t *testing.T) {
	repoRoot := findRepoRoot(t)
	reg := extractRegistry(t, filepath.Join(repoRoot, "internal", "engine", "compose"))
	// f1.viewport is the F1 default-screen view; if it is missing the
	// recovery is broken (BUG-323 registered it and boot depends on it).
	if _, ok := reg["f1.viewport"]; !ok {
		t.Errorf("extractRegistry did not recover f1.viewport from live compose source; got %v", keys(reg))
	}
	if len(reg) < 2 {
		t.Errorf("extractRegistry recovered only %d name(s) from live compose — expected the full viewRegistrationOrder set", len(reg))
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
