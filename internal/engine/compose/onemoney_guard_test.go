package compose

// FEAT-231 M1 — the "one money" doctrine, made mechanical and fail-closed.
//
// Doctrine (BUG-355 / BUG-324): simState mirrors FinanceAPI; it is not a
// second source of truth. The player's money lives in exactly one
// simulation field, st.treasury, and that field is written through
// exactly one function, setTreasury (chrome_publish.go), which also keeps
// the BUG-324 publish mirror (treasuryPub) in step. The runtime invariant
// test (chrome_publish_test.go) catches a bypassing writer whose value
// differs from the mirror AT REST, but by its own admission cannot catch a
// bypass that a later setTreasury call re-compensates within the same
// effect — "That residue is a STATIC property, and the right instrument
// for it is an astgate rule banning `.treasury =` outside setTreasury —
// proposed as a follow-up". This file is that static instrument.
//
// Two AST asserts over the compose package's OWN source (every non-test
// .go file in this directory — setTreasury lives in chrome_publish.go, not
// compose.go, so a compose.go-only scan would be vacuous):
//
//  1. Money-field allowlist ratchet: every int64/atomic.Int64 field of
//     simState whose NAME looks like money (moneyFieldPattern) must appear
//     in an explicit allowlist. A newly-added money-shaped field that
//     nobody listed fails the gate, forcing whoever adds it to justify it
//     against the one-money doctrine (does it need conservation ledgering?
//     is it a mirror that can drift? is it a second source of truth?).
//
//  2. Single writer of st.treasury: the ONLY function that may contain an
//     assignment to a `.treasury` selector is setTreasury. Any direct
//     `st.treasury = …` anywhere else fails closed — that is the exact
//     attack the BUG-324 round ran (one call site changed to assign
//     st.treasury directly, whole suite stayed green, bar rendered "money
//     9" for a £10 treasury).
//
// _test.go files are excluded on purpose (mirroring
// foundation/errs/source_scan_test.go): bug308_test.go legitimately does
// `st.treasury = …` directly to construct a fixture state, and that is not
// a product-code bypass.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// parseComposeProductFiles parses every NON-test .go file sitting directly
// in the compose package's own source directory (".", which `go test` sets
// to the package dir) and returns their ASTs, using WalkDir/ParseFile
// rather than the deprecated parser.ParseDir. It excludes _test.go (see the
// file doc: bug308_test.go legitimately writes st.treasury directly to
// build a fixture) and, for free, the stray compose.go.clean (not a .go
// name). It t.Fatals rather than returning empty, so a broken walk can
// never make the asserts pass vacuously (BUG-230).
func parseComposeProductFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading compose package dir: %v", err)
	}
	var files []*ast.File
	for _, de := range entries {
		if de.IsDir() {
			continue // sibling subpackages are not part of package compose
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		if f.Name.Name != "compose" {
			// Defensive: a stray same-dir file in another package would
			// otherwise pollute the scan. None exist today.
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("parsed zero non-test .go files from the compose package — the guard is broken (wrong CWD?), not clean")
	}
	return files
}

// moneyFieldPattern matches a struct field NAME (case-insensitive) that
// looks like it holds money. Deliberately broad: the point of assert #1 is
// to notice a money-shaped field appearing at all, so a stray "slushFund"
// or "reserveCash" trips it and has to be justified into the allowlist.
var moneyFieldPattern = regexp.MustCompile(`(?i)treasury|wealth|cash|balance|reserve|debt|fund|money`)

// moneyFieldAllowlist is the ratchet. Every money-shaped scalar field of
// simState (int64 or atomic.Int64) must be listed here. Adding a new one
// is a deliberate act: list it AND be able to say why a second money-ish
// number in simState does not violate the one-money doctrine.
//
// LIGHT JUDGMENT CALL (flag for lead/Aaron): this list is human-curated.
// It was derived by reading the simState struct — note it includes
// previousClosingMoney (compose.go), which the M1 brief's first-draft list
// omitted, and treasuryPub (atomic.Int64 mirror). Each entry:
//   - treasury            the one true money field (written via setTreasury)
//   - treasuryPub         BUG-324 publish-only mirror of treasury (atomic)
//   - citizenWealth       aggregate household wealth (mirrors AcctHouseholds)
//   - moneyOpening        conservation-ledger opening total (treasury+wealth)
//   - moneyDelta          conservation-ledger per-tick delta
//   - moneyFlows          cumulative gross money flow (AC-9)
//   - previousClosingMoney snapshot carry: last tick's closing total
var moneyFieldAllowlist = map[string]bool{
	"treasury":             true,
	"treasuryPub":          true,
	"citizenWealth":        true,
	"moneyOpening":         true,
	"moneyDelta":           true,
	"moneyFlows":           true,
	"previousClosingMoney": true,
}

// treasuryWriterAllowlist is the single-writer allowlist for assert #2.
// Exactly one function may assign st.treasury.
var treasuryWriterAllowlist = map[string]bool{
	"setTreasury": true,
}

// typeString renders the simple type expressions we care about
// (int64, atomic.Int64) to a canonical string. Anything more exotic
// renders to "" and is simply not treated as a money-scalar.
func typeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}
	return ""
}

// isMoneyScalarType reports whether a field type is one of the scalar
// money-carrying types the doctrine cares about: a raw int64, or an
// atomic.Int64 (the mirror). Both hold a money figure; a new field of
// either kind whose name looks like money must be justified.
func isMoneyScalarType(expr ast.Expr) bool {
	switch typeString(expr) {
	case "int64", "atomic.Int64":
		return true
	}
	return false
}

// collectMoneyFields returns the names of every simState field whose type
// is a money scalar (int64/atomic.Int64) AND whose name matches
// moneyFieldPattern. Pure over the given files so a fixture can drive it.
func collectMoneyFields(files []*ast.File, structName string) []string {
	var names []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if !isMoneyScalarType(field.Type) {
					continue
				}
				for _, nm := range field.Names {
					if moneyFieldPattern.MatchString(nm.Name) {
						names = append(names, nm.Name)
					}
				}
			}
			return false
		})
	}
	sort.Strings(names)
	return names
}

// treasuryWrite records one assignment to a `.treasury` selector: the name
// of the function that encloses it (or "" for a package-level/anonymous
// site) and a location string for the failure message.
type treasuryWrite struct {
	Func string
	Pos  string
}

// assignsTreasurySelector reports whether an expression is a selector
// ending in `.treasury` — i.e. an lvalue naming a treasury field. Base is
// left unrestricted on purpose (fail-closed): ANY `x.treasury = …` in
// product code must be inside setTreasury, whatever x is.
func assignsTreasurySelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "treasury"
}

// collectTreasuryWriters walks every function in the given files and
// records each assignment (including op-assign and ++/--) whose LHS is a
// `.treasury` selector, tagged with the enclosing function's name. Pure
// over the given files so a fixture can drive it.
func collectTreasuryWriters(fset *token.FileSet, files []*ast.File) []treasuryWrite {
	var writes []treasuryWrite
	record := func(funcName string, node ast.Node, lhs []ast.Expr) {
		for _, e := range lhs {
			if assignsTreasurySelector(e) {
				writes = append(writes, treasuryWrite{Func: funcName, Pos: posString(fset, node.Pos())})
			}
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			name := fn.Name.Name
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				switch s := m.(type) {
				case *ast.AssignStmt:
					record(name, s, s.Lhs)
				case *ast.IncDecStmt:
					record(name, s, []ast.Expr{s.X})
				}
				return true
			})
			return true
		})
	}
	return writes
}

func posString(fset *token.FileSet, p token.Pos) string {
	if fset == nil {
		return "?"
	}
	return fset.Position(p).String()
}

// --- Live asserts over the real compose package ---------------------------

// TestOneMoney_SimStateMoneyFieldsMatchAllowlist is assert #1: the set of
// money-shaped scalar fields on simState must equal moneyFieldAllowlist. A
// new unlisted money field fails; a listed field that disappears also fails
// (keeping the allowlist honest, not a stale superset).
func TestOneMoney_SimStateMoneyFieldsMatchAllowlist(t *testing.T) {
	fset := token.NewFileSet()
	files := parseComposeProductFiles(t, fset)
	found := collectMoneyFields(files, "simState")
	if len(found) == 0 {
		t.Fatal("found zero money-shaped fields on simState — the scanner is broken (simState not found, or type matching wrong), not that the struct lost its money; treasury/citizenWealth are always there")
	}

	foundSet := map[string]bool{}
	for _, n := range found {
		foundSet[n] = true
	}

	var unlisted, missing []string
	for n := range foundSet {
		if !moneyFieldAllowlist[n] {
			unlisted = append(unlisted, n)
		}
	}
	for n := range moneyFieldAllowlist {
		if !foundSet[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(missing)

	if len(unlisted) > 0 {
		t.Errorf("simState has money-shaped field(s) %v not in moneyFieldAllowlist (FEAT-231 one-money ratchet). "+
			"A new money-ish number in simState is a design decision: either it is a MIRROR of FinanceAPI/treasury "+
			"(document why it cannot drift, like treasuryPub) or it is a SECOND SOURCE OF TRUTH (which the doctrine bans). "+
			"Add it to the allowlist WITH a justification, or route it through the ledger instead.", unlisted)
	}
	if len(missing) > 0 {
		t.Errorf("moneyFieldAllowlist lists field(s) %v that no longer exist on simState — remove them so the allowlist stays an exact ratchet, not a stale superset", missing)
	}
}

// TestOneMoney_TreasuryHasSingleWriter is assert #2: the only function that
// assigns a `.treasury` selector anywhere in the compose package's product
// code is setTreasury. This is the static complement to the BUG-324 runtime
// mirror test, closing the "re-compensated within the same effect" residue
// that test documented it could not catch.
func TestOneMoney_TreasuryHasSingleWriter(t *testing.T) {
	fset := token.NewFileSet()
	pkgFiles := parseComposeProductFiles(t, fset)
	writes := collectTreasuryWriters(fset, pkgFiles)

	if len(writes) == 0 {
		t.Fatal("found zero assignments to st.treasury in the whole compose package — the scanner is broken (setTreasury's `st.treasury = v` must be found), not that treasury is never written; a scan that finds nothing must not pass green (BUG-230)")
	}

	var offenders []string
	for _, w := range writes {
		if !treasuryWriterAllowlist[w.Func] {
			offenders = append(offenders, w.Func+" ("+w.Pos+")")
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("st.treasury is assigned outside setTreasury by: %v (FEAT-231 one-money single-writer ban). "+
			"Every treasury change MUST go through setTreasury(v) so the BUG-324 publish mirror (treasuryPub) can never drift — "+
			"this is the exact bypass the BUG-324 round exploited (a direct assignment left the suite green while the bar showed a stale figure). "+
			"Compute the new value and call st.setTreasury(...) instead of assigning st.treasury directly.", offenders)
	}
}

// --- Negative controls: prove BOTH asserts actually fire, via fixture
// source strings rather than a deliberate violation left in the live tree
// (BUG-230 non-vacuity). ---

func parseFixture(t *testing.T, fset *token.FileSet, src string) []*ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	return []*ast.File{f}
}

// TestCollectMoneyFields_RED proves assert #1's detector fires: a struct
// carrying a stray money-shaped field (slushFund) surfaces it, and a
// non-money int64 field (peopleDelta) is NOT surfaced.
func TestCollectMoneyFields_RED(t *testing.T) {
	fset := token.NewFileSet()
	src := `package compose
import "sync/atomic"
type simState struct {
	treasury      int64
	citizenWealth int64
	slushFund     int64
	treasuryPub   atomic.Int64
	peopleDelta   int64
	cid           string
}
`
	got := collectMoneyFields(parseFixture(t, fset, src), "simState")
	set := map[string]bool{}
	for _, n := range got {
		set[n] = true
	}
	if !set["slushFund"] {
		t.Errorf("stray money field slushFund not detected; got %v", got)
	}
	if !set["treasuryPub"] {
		t.Errorf("atomic.Int64 money field treasuryPub not detected; got %v", got)
	}
	if set["peopleDelta"] {
		t.Errorf("peopleDelta is not money-shaped and must not be collected; got %v", got)
	}
	if set["cid"] {
		t.Errorf("cid is a string, not a money scalar, and must not be collected; got %v", got)
	}
}

// TestCollectMoneyFields_CleanMatchesAllowlist proves assert #1 passes on a
// struct whose money fields are exactly the allowlist — the non-RED half,
// so the test is not merely "everything fails".
func TestCollectMoneyFields_CleanMatchesAllowlist(t *testing.T) {
	fset := token.NewFileSet()
	src := `package compose
import "sync/atomic"
type simState struct {
	treasury             int64
	treasuryPub          atomic.Int64
	citizenWealth        int64
	moneyOpening         int64
	moneyDelta           int64
	moneyFlows           int64
	previousClosingMoney int64
	peopleDelta          int64
}
`
	got := collectMoneyFields(parseFixture(t, fset, src), "simState")
	for _, n := range got {
		if !moneyFieldAllowlist[n] {
			t.Errorf("clean fixture produced unlisted field %q (got %v)", n, got)
		}
	}
	if len(got) != len(moneyFieldAllowlist) {
		t.Errorf("clean fixture money fields %v do not match allowlist size %d", got, len(moneyFieldAllowlist))
	}
}

// TestCollectTreasuryWriters_RED proves assert #2's detector fires: a
// direct `st.treasury = …` in a function other than setTreasury is
// recorded and attributed to that function, while the legitimate write
// inside setTreasury is attributed to setTreasury.
func TestCollectTreasuryWriters_RED(t *testing.T) {
	fset := token.NewFileSet()
	src := `package compose
type simState struct{ treasury int64 }
func (st *simState) setTreasury(v int64) { st.treasury = v }
func (st *simState) sneakyBypass()       { st.treasury = 9 }
func (st *simState) readOnly() int64     { return st.treasury }
func (st *simState) opAssign()           { st.treasury += 1 }
`
	writes := collectTreasuryWriters(fset, parseFixture(t, fset, src))

	byFunc := map[string]int{}
	for _, w := range writes {
		byFunc[w.Func]++
	}
	if byFunc["setTreasury"] != 1 {
		t.Errorf("expected exactly 1 treasury write in setTreasury, got %d (all: %v)", byFunc["setTreasury"], byFunc)
	}
	if byFunc["sneakyBypass"] != 1 {
		t.Errorf("expected the sneaky `st.treasury = 9` bypass to be detected in sneakyBypass, got %d (all: %v)", byFunc["sneakyBypass"], byFunc)
	}
	if byFunc["opAssign"] != 1 {
		t.Errorf("expected the `st.treasury += 1` op-assign to be detected in opAssign, got %d (all: %v)", byFunc["opAssign"], byFunc)
	}
	if byFunc["readOnly"] != 0 {
		t.Errorf("readOnly only READS st.treasury and must not be recorded as a writer, got %d", byFunc["readOnly"])
	}

	// And the allowlist filter must flag exactly the non-setTreasury writers.
	var offenders []string
	for _, w := range writes {
		if !treasuryWriterAllowlist[w.Func] {
			offenders = append(offenders, w.Func)
		}
	}
	sort.Strings(offenders)
	want := []string{"opAssign", "sneakyBypass"}
	if strings.Join(offenders, ",") != strings.Join(want, ",") {
		t.Errorf("expected offenders %v, got %v", want, offenders)
	}
}

// TestCollectTreasuryWriters_CleanIsSingleWriter proves assert #2 passes
// when setTreasury is the sole writer — the non-RED half.
func TestCollectTreasuryWriters_CleanIsSingleWriter(t *testing.T) {
	fset := token.NewFileSet()
	src := `package compose
type simState struct{ treasury int64; treasuryPub int64 }
func (st *simState) setTreasury(v int64) { st.treasury = v; st.treasuryPub = v }
func (st *simState) spend()              { st.setTreasury(st.treasury - 1) }
`
	writes := collectTreasuryWriters(fset, parseFixture(t, fset, src))
	for _, w := range writes {
		if !treasuryWriterAllowlist[w.Func] {
			t.Errorf("clean fixture reported an offending treasury writer %q at %s", w.Func, w.Pos)
		}
	}
	if len(writes) != 1 {
		t.Errorf("expected exactly 1 treasury write (in setTreasury), got %d: %+v", len(writes), writes)
	}
}
