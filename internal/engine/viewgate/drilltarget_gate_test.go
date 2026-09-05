package viewgate

// FEAT-231 increment V2 — the "one DrillTarget type" static gate.
//
// Doctrine (FEAT-042 SSOT, moved to internal/protocol by the architect
// ruling of 2026-09-05): the drill-through carrier — a (ViewName,
// EntityID) pair naming "the thing Enter navigates to" — must exist as
// exactly ONE type in the whole engine/UI tree: protocol.TargetRef,
// declared in internal/protocol (entity.go). ui.dash.DrillTarget is a
// type ALIAS for protocol.TargetRef (`type DrillTarget =
// protocol.TargetRef`, drill.go) — the same type under a second name,
// not a second carrier, so it does not and must not itself appear as a
// struct declaration for this scanner to find (an alias has no fields of
// its own). A second, locally-declared (ViewName, EntityID) struct
// anywhere else silently fragments that SSOT: two carriers drift apart,
// and a tile built around the rogue one is not the tile the drill/audit
// machinery understands. This gate fails `go test` the moment such a
// second carrier appears, before it can ship.
//
// History: V2 originally sanctioned dash.DrillTarget itself as the one
// true carrier (FEAT-042 landed protocol.TargetRef alongside it as a
// second, wire-shaped carrier, which is what first tripped this gate).
// The architect ruling moved the SSOT DOWN to the protocol layer instead
// of leaving two structs to reconcile, and repointed dash.DrillTarget at
// it via alias so every existing dash consumer keeps compiling
// unchanged.
//
// Like V1 this is a pure SOURCE scanner (walkGoSourceFiles, shared with the
// one-view-registry gate): it never imports dash or engine at runtime, so
// it adds no import edge and cannot collide with a parallel lane's package
// tests. It mirrors V1's shape — a scan (scanDrillCarriers), a pure check
// (verifyDrillCarriers) driven by fixtures in the negative controls, and
// one live-tree gate (TestNoSecondDrillTargetType).
//
// What is flagged as a "(ViewName, EntityID) carrier" (structural, over
// struct type declarations only — a local var or a func param of the same
// shape is not a *type* and cannot fragment the type SSOT):
//
//   - RULE A: a struct that carries BOTH a view-name-shaped field AND an
//     entity-id-shaped field. A field is view-name-shaped if it is named
//     "ViewName" or its type's final name is "ViewName" (covers the plain
//     `ViewName string` of dash.DrillTarget today and a future typed
//     `dash.ViewName`/`protocol.ViewName` field); entity-id-shaped is the
//     same test against "EntityID".
//   - RULE B: a struct whose TYPE NAME ends in "Target" (matching the
//     "*DrillTarget"/"*Target" family) that carries a view-name-shaped
//     field — even without an EntityID field. This catches a renamed clone
//     (`type NavTarget struct { ViewName string; Row int }`) that Rule A's
//     both-fields test would miss.
//
// The ONE sanctioned declaration: a type named "TargetRef" declared under
// internal/protocol/. Anything else that matches Rule A or Rule B fails
// closed — including a hypothetical struct (re-)declaration named
// "DrillTarget", since the sanctioned dash name is now an alias, not a
// struct, and does not itself match the scanner's shape at all. Note
// deliberate NON-matches that keep the gate from false-positiving on
// legitimate neighbours:
//
//   - protocol.SubscribePayload (ViewName + Params) — a view name but no
//     entity id and not a *Target name: it is the wire command, not a
//     drill carrier.
//   - engine/news Event/Story (EntityID + …) — an entity id but no view
//     name: engine-side source references, not drill carriers.
//   - unlocks.ForceTarget (Tier + NodeID) — a *Target name but no view-name
//     field: an unlock selector, not a drill carrier.

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// drillCarrier is one struct type declaration that looks like a
// (ViewName, EntityID) drill-through carrier, with enough location info to
// build an actionable failure message.
type drillCarrier struct {
	TypeName     string
	File         string // repo-relative, forward-slash separated
	Line         int
	HasViewName  bool
	HasEntityID  bool
	NameIsTarget bool // type name ends in "Target"
}

// finalTypeName returns the trailing identifier of a field's type
// expression — "ViewName" for a bare `ViewName`, and the selector's Sel
// ("ViewName" for `dash.ViewName`/`protocol.ViewName`). Pointers and other
// wrappers unwrap to their element. Anything else yields "".
func finalTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return finalTypeName(e.X)
	}
	return ""
}

// fieldMatches reports whether a struct field is "<want>-shaped": it is
// named exactly want, or its type's final name is want. want is "ViewName"
// or "EntityID".
func fieldMatches(field *ast.Field, want string) bool {
	for _, n := range field.Names {
		if n.Name == want {
			return true
		}
	}
	// An embedded/anonymous field (no names) is matched by its type name;
	// a named field is also allowed to match by type (e.g.
	// `View dash.ViewName`).
	return finalTypeName(field.Type) == want
}

// scanDrillCarriers walks every non-test .go file under repoRoot/<dir> and
// collects every struct type declaration that matches Rule A or Rule B
// (see the file doc). It returns the raw matches; whether each is a
// violation is verifyDrillCarriers's job (so the negative controls can run
// the check on fixture data).
func scanDrillCarriers(t testing.TB, repoRoot string, dirs ...string) []drillCarrier {
	t.Helper()
	fset := token.NewFileSet()
	var out []drillCarrier

	walkGoSourceFiles(t, fset, repoRoot, dirs, func(file *ast.File, rel string) {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			var hasVN, hasEID bool
			for _, f := range st.Fields.List {
				if fieldMatches(f, "ViewName") {
					hasVN = true
				}
				if fieldMatches(f, "EntityID") {
					hasEID = true
				}
			}
			nameIsTarget := strings.HasSuffix(ts.Name.Name, "Target")

			ruleA := hasVN && hasEID
			ruleB := nameIsTarget && hasVN
			if !ruleA && !ruleB {
				return true
			}
			pos := fset.Position(ts.Pos())
			out = append(out, drillCarrier{
				TypeName:     ts.Name.Name,
				File:         rel,
				Line:         pos.Line,
				HasViewName:  hasVN,
				HasEntityID:  hasEID,
				NameIsTarget: nameIsTarget,
			})
			return true
		})
	})

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// sanctionedDrillCarrier reports whether a matched carrier is THE one
// blessed declaration: a type named "TargetRef" under internal/protocol/.
// This is the doctrine constant (the single sanctioned home), not derived
// data — analogous to V1 naming the single sanctioned registry.
//
// dash.DrillTarget is deliberately NOT named here: it is a type alias
// (`type DrillTarget = protocol.TargetRef`), which has no field list of
// its own, so scanDrillCarriers structurally cannot observe it as a
// carrier in the first place — there is nothing for this predicate to
// bless or reject at that name/location.
func sanctionedDrillCarrier(c drillCarrier) bool {
	return c.TypeName == "TargetRef" &&
		strings.HasPrefix(c.File, "internal/protocol/")
}

// verifyDrillCarriers is the pure check: given every matched carrier and
// the sanctioned-predicate, it returns one human-readable violation per
// unsanctioned carrier. Empty result == every (ViewName, EntityID) carrier
// in the tree is the one sanctioned dash.DrillTarget. Kept I/O-free so the
// negative controls drive it with fixture data.
func verifyDrillCarriers(found []drillCarrier, sanctioned func(drillCarrier) bool) []string {
	var violations []string
	for _, c := range found {
		if sanctioned(c) {
			continue
		}
		var why string
		switch {
		case c.HasViewName && c.HasEntityID:
			why = "carries BOTH a view-name-shaped and an entity-id-shaped field"
		case c.NameIsTarget && c.HasViewName:
			why = "is a *Target-named struct carrying a view-name-shaped field"
		default:
			why = "matched the drill-carrier shape"
		}
		violations = append(violations, fmt.Sprintf(
			"%s:%d: type %s %s — a second (ViewName, EntityID) drill carrier fragments the drill SSOT. "+
				"There must be exactly one such type, protocol.TargetRef in internal/protocol (FEAT-042/FEAT-231 "+
				"one-DrillTarget-type doctrine, home moved here by the 2026-09-05 architect ruling). Reuse "+
				"protocol.TargetRef (or dash.DrillTarget, its alias) instead of declaring a local carrier; "+
				"if this is a sanctioned new home, extend sanctionedDrillCarrier",
			c.File, c.Line, c.TypeName, why))
	}
	return violations
}

// --- the live-tree gate ---

// TestNoSecondDrillTargetType is the FEAT-231 V2 gate: the only
// (ViewName, EntityID) carrier type under internal/ or cmd/ is
// protocol.TargetRef (ui.dash.DrillTarget is its alias, not a second
// carrier — see the file doc). Any other matching struct fails closed.
func TestNoSecondDrillTargetType(t *testing.T) {
	repoRoot := findRepoRoot(t)
	found := scanDrillCarriers(t, repoRoot, "internal", "cmd")

	// Non-vacuous: the scanner MUST observe the sanctioned protocol.TargetRef
	// in the live tree. If it does not, the scanner is broken (walk paths,
	// field detection, repoRoot) rather than the tree having no carrier —
	// and a broken scanner that sees nothing would pass trivially.
	sawSanctioned := false
	for _, c := range found {
		if sanctionedDrillCarrier(c) {
			sawSanctioned = true
			break
		}
	}
	if !sawSanctioned {
		t.Fatalf("scanDrillCarriers did not find protocol.TargetRef in the live tree — the scanner is broken, not that the carrier vanished; got %d carrier(s): %+v", len(found), found)
	}

	violations := verifyDrillCarriers(found, sanctionedDrillCarrier)
	if len(violations) > 0 {
		t.Errorf("%d rogue drill-carrier type(s) found (FEAT-231 one-DrillTarget-type doctrine):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// --- negative controls: prove the gate fails, driven by fixture data and a
// fixture source tree the scanner actually reads (BUG-230). ---

// TestVerifyDrillCarriers_CatchesRogue proves the pure check flags an
// unsanctioned carrier and passes the sanctioned protocol one — i.e. the
// sanctioned predicate is load-bearing.
func TestVerifyDrillCarriers_CatchesRogue(t *testing.T) {
	sanctioned := []drillCarrier{{TypeName: "TargetRef", File: "internal/protocol/entity.go", Line: 76, HasViewName: true, HasEntityID: true}}
	if v := verifyDrillCarriers(sanctioned, sanctionedDrillCarrier); len(v) != 0 {
		t.Fatalf("expected the sanctioned protocol.TargetRef to pass, got: %v", v)
	}

	rogue := []drillCarrier{{TypeName: "FakeDrill", File: "internal/engine/foo/bar.go", Line: 7, HasViewName: true, HasEntityID: true}}
	v := verifyDrillCarriers(rogue, sanctionedDrillCarrier)
	if len(v) != 1 {
		t.Fatalf("expected exactly 1 violation for a rogue carrier, got %d: %v", len(v), v)
	}
	for _, want := range []string{"FakeDrill", "internal/engine/foo/bar.go:7", "fragments the drill SSOT"} {
		if !strings.Contains(v[0], want) {
			t.Errorf("violation %q missing expected substring %q", v[0], want)
		}
	}

	// A TargetRef name OUTSIDE internal/protocol is NOT sanctioned (the
	// location, not just the name, is what blesses it) — and, symmetrically,
	// a struct literally named "DrillTarget" is now just an ordinary rogue
	// name (dash's own DrillTarget is an alias with no fields, so it can
	// never appear here as a scanned struct).
	elsewhere := []drillCarrier{{TypeName: "TargetRef", File: "internal/engine/foo/drill.go", Line: 3, HasViewName: true, HasEntityID: true}}
	if v := verifyDrillCarriers(elsewhere, sanctionedDrillCarrier); len(v) != 1 {
		t.Fatalf("expected a TargetRef outside internal/protocol to be flagged, got %d: %v", len(v), v)
	}
	reforkedDrillTarget := []drillCarrier{{TypeName: "DrillTarget", File: "internal/ui/dash/drill.go", Line: 19, HasViewName: true, HasEntityID: true}}
	if v := verifyDrillCarriers(reforkedDrillTarget, sanctionedDrillCarrier); len(v) != 1 {
		t.Fatalf("expected a struct literally named DrillTarget (a re-fork away from the alias) to be flagged, got %d: %v", len(v), v)
	}
}

// TestScanDrillCarriers_FixtureTree proves the scanner (a) flags a rogue
// (ViewName, EntityID) struct outside dash, (b) flags a *Target-named
// struct carrying only a view name (Rule B), (c) does NOT flag benign
// neighbours (view-name-only non-Target, entity-id-only, *Target with no
// view name), and (d) that adding the rogue fixture makes the gate RED
// while removing it returns GREEN — the "fixture present -> RED, remove ->
// GREEN" proof over a tree the scanner actually reads.
func TestScanDrillCarriers_FixtureTree(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "fixture")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Benign neighbours only — must produce ZERO matches.
	benign := `package fixture

// view name only, not a *Target name -> not a carrier (like SubscribePayload)
type SubscribePayload struct {
	ViewName string
	Params   map[string]string
}

// entity id only -> not a carrier (like news.Story)
type Story struct {
	EntityID string
	Text     string
}

// *Target name but no view-name field -> not a carrier (like ForceTarget)
type ForceTarget struct {
	Tier   int
	NodeID string
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "benign.go"), []byte(benign), 0o644); err != nil {
		t.Fatalf("write benign.go: %v", err)
	}
	// A _test.go carrier must be ignored entirely by the walker.
	testSrc := `package fixture

type TestOnlyDrill struct {
	ViewName string
	EntityID string
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "benign_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write benign_test.go: %v", err)
	}

	// GREEN: no matches at all on the benign tree.
	if found := scanDrillCarriers(t, dir, "internal"); len(found) != 0 {
		t.Fatalf("expected zero carriers on the benign fixture tree, got: %+v", found)
	}

	// RED (Rule A): a rogue (ViewName, EntityID) struct outside dash — this
	// is the doctrine's exact target shape (dash.ViewName/dash.EntityID
	// typed fields, parse-only so the unresolved import is fine).
	ruleA := `package fixture

import "example/dash"

type FakeDrill struct {
	ViewName dash.ViewName
	EntityID dash.EntityID
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fakedrill.go"), []byte(ruleA), 0o644); err != nil {
		t.Fatalf("write fakedrill.go: %v", err)
	}
	found := scanDrillCarriers(t, dir, "internal")
	v := verifyDrillCarriers(found, sanctionedDrillCarrier)
	if len(v) != 1 || !strings.Contains(v[0], "FakeDrill") {
		t.Fatalf("expected exactly 1 violation naming FakeDrill after adding the Rule-A fixture, got: %v", v)
	}

	// RED (Rule B): a *Target-named struct carrying only a view name.
	ruleB := `package fixture

type NavTarget struct {
	ViewName string
	Row      int
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "navtarget.go"), []byte(ruleB), 0o644); err != nil {
		t.Fatalf("write navtarget.go: %v", err)
	}
	v2 := verifyDrillCarriers(scanDrillCarriers(t, dir, "internal"), sanctionedDrillCarrier)
	if len(v2) != 2 {
		t.Fatalf("expected 2 violations (FakeDrill + NavTarget) after adding the Rule-B fixture, got %d: %v", len(v2), v2)
	}
	sawNav := false
	for _, s := range v2 {
		if strings.Contains(s, "NavTarget") && strings.Contains(s, "*Target-named") {
			sawNav = true
		}
	}
	if !sawNav {
		t.Errorf("expected a Rule-B violation naming NavTarget as a *Target-named struct, got: %v", v2)
	}

	// GREEN again once both rogue fixtures are removed.
	if err := os.Remove(filepath.Join(pkgDir, "fakedrill.go")); err != nil {
		t.Fatalf("remove fakedrill.go: %v", err)
	}
	if err := os.Remove(filepath.Join(pkgDir, "navtarget.go")); err != nil {
		t.Fatalf("remove navtarget.go: %v", err)
	}
	if v3 := verifyDrillCarriers(scanDrillCarriers(t, dir, "internal"), sanctionedDrillCarrier); len(v3) != 0 {
		t.Fatalf("expected GREEN after removing both rogue fixtures, got: %v", v3)
	}
}
