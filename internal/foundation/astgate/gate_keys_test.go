package astgate

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- BUG-119 round 7: complete-identity keying (Bill's ruling, 2026-08-12) --
//
// Rounds 1-6 each patched exactly the collision a Destructive reattack
// found and were each REJECTed by the NEXT reattack finding a different
// collision the previous round's hand-enumerated key scheme did not
// anticipate (see violationKey's doc comment in gate.go for the full
// round-by-round history). Bill's ruling formalises the lesson those six
// rounds taught: a hand-assembled, hand-enumerated identity key cannot be
// complete by construction, no matter how many cases get added to it —
// only a key built from the node's genuinely COMPLETE identity (file,
// enclosing declaration chain, kind, and the full type expression AS
// PRINTED via go/printer, not a hand-picked subset of fields) can be.
//
// This file replaces "hunt for the next collision by hand" (one regression
// test per Destructive-found collision, six rounds running) with an
// exhaustive, enumerated matrix of declaration shapes, crossed pairwise,
// asserting NO two distinct declarations anywhere in the matrix ever
// produce the same violationKey — plus the mandatory self-check proof
// (Part 2 of Bill's ruling): Run itself must hard-fail, not silently
// merge, if two distinct AST nodes ever do collide.

// keyMatrixFixture is the enumerated decl-shape matrix. It deliberately
// covers every shape category Bill's ruling names:
//
//   - plain type param                  (Consume01)
//   - pointer type param                (Consume02, and the receiver
//     path via Guarded's own method)
//   - slice type param                  (Consume03)
//   - variadic type param               (Consume04)
//   - generic instantiation, one type param, as a receiver (Set[T])
//   - generic instantiation, two type params, as a receiver (Pair[K, V])
//   - method vs. free function          (TypeA/TypeB/TypeC methods vs.
//     the free Consume01/02/03/04 functions, all sharing candidate-type
//     parameters)
//   - value receiver vs. pointer receiver (TypeD, value; TypeA, pointer)
//   - a "nested" declaration shape: Go has no true nested package-level
//     type declaration, so the nearest AST-permitted analogue — a
//     grouped `type ( ... )` block declaring GroupedGuarded alongside an
//     unrelated grouped member — stands in, per the task's own "if Go's
//     AST permits" allowance.
//   - a SECOND, independently-shaped candidate type (GenericGuarded[T],
//     itself generic) with its own receiver-method and parameter-taking
//     reachable functions, so the matrix is not exclusively built around
//     one candidate type's identity.
const keyMatrixFixture = `package fixture

import "sync"

// Guarded is the primary candidate type: a sync.Mutex value field plus a
// slice field (AC-1).
type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// Method is the receiver-method path on the candidate type itself —
// TypeName/receiver/matched-type are all "Guarded" here.
func (g *Guarded) Method() {}

// Consume01/02/03/04 are free functions, each taking Guarded via a
// DIFFERENT parameter shape (plain, pointer, slice, variadic) — same
// receiver-less "enclosing declaration" component, different matched
// type expression.
func Consume01(g Guarded)    { _ = g }
func Consume02(g *Guarded)   { _ = g }
func Consume03(g []Guarded)  { _ = g }
func Consume04(g ...Guarded) { _ = g }

// TypeA/TypeB/TypeC/TypeD are unrelated (non-candidate) receiver types,
// each declaring its own "Consume" method taking Guarded via a different
// parameter shape — method vs. free function, and value vs. pointer
// receiver, crossed with parameter shape.
type TypeA struct{}

func (a *TypeA) Consume(g *Guarded) { _ = g }

type TypeB struct{}

func (b *TypeB) Consume(g []Guarded) { _ = g }

type TypeC struct{}

func (c *TypeC) Consume(g ...Guarded) { _ = g }

type TypeD struct{}

// Consume on a VALUE receiver (not pointer) — a distinct receiver shape
// from TypeA/TypeB/TypeC's pointer receivers.
func (d TypeD) Consume(g Guarded) { _ = g }

// Set is a single-type-parameter generic container — its own Consume
// method exercises a generic-instantiation RECEIVER shape
// (*ast.IndexExpr).
type Set[T any] struct {
	values []T
}

func (s *Set[T]) Consume(g *Guarded) { _ = g }

// Pair is a two-type-parameter generic container — its own Consume
// method exercises a generic-instantiation RECEIVER shape with more than
// one type parameter (*ast.IndexListExpr).
type Pair[K comparable, V any] struct {
	values map[K]V
}

func (p *Pair[K, V]) Consume(g *Guarded) { _ = g }

// GroupedGuarded stands in for "a nested/inner type" (Go's AST has no
// true nested package-level type declaration): declared inside a grouped
// type(...) block alongside an unrelated member, per the enumeration
// task's "if Go's AST permits" allowance.
type (
	GroupedGuarded struct {
		mu    sync.Mutex
		items []int
	}
	groupedUnrelated struct {
		n int
	}
)

func (g *GroupedGuarded) checkNotCopied() bool { return true }

func (g *GroupedGuarded) Method() { _ = groupedUnrelated{} }

// GenericGuarded is a SECOND, independently-shaped candidate type: a
// generic struct that itself matches AC-1's mutex+aliasable shape, so the
// matrix is not built exclusively around one candidate identity.
type GenericGuarded[T any] struct {
	mu     sync.Mutex
	values []T
}

func (g *GenericGuarded[T]) checkNotCopied() bool { return true }

// Method is GenericGuarded's own receiver-method path — same FuncName
// ("Method") as Guarded.Method and GroupedGuarded.Method above, but a
// completely different (generic) receiver type expression.
func (g *GenericGuarded[T]) Method() {}

// ConsumeGeneric takes GenericGuarded[T] by parameter from a free
// function, exercising a generic type used as a PARAMETER (rather than a
// receiver) shape.
func ConsumeGeneric(g *GenericGuarded[int]) { _ = g }
`

// TestViolationKey_EnumeratedShapeMatrix_AllKeysUnique is BUG-119 round
// 7's required generative/enumerated test (Bill's ruling): rather than one
// regression test per Destructive-found collision, this builds the whole
// keyMatrixFixture shape matrix in one pass and asserts NO two distinct
// ReachableFuncs — covering every shape category the ruling names, method
// and free-function, value and pointer receiver, plain/pointer/slice/
// variadic parameter, single- and multi-type-param generic instantiation,
// and a grouped-block "nested" declaration — ever produce the same
// violationKey. It checks EVERY ReachableFunc (not just unguarded ones),
// since violationKey's collision-freedom must hold regardless of guard
// status.
func TestViolationKey_EnumeratedShapeMatrix_AllKeysUnique(t *testing.T) {
	root := writeFixturePkg(t, "keymatrix", map[string]string{"fixture.go": keyMatrixFixture})
	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/keymatrix"]
	candidates := findCandidateTypes("internal/keymatrix", files)

	// Sanity: both candidate types (Guarded and its grouped/generic
	// siblings) must actually have been recognised, or this test would
	// pass vacuously with an empty/near-empty matrix.
	names := map[string]bool{}
	for _, c := range candidates {
		names[c.Name] = true
	}
	for _, want := range []string{"Guarded", "GroupedGuarded", "GenericGuarded"} {
		if !names[want] {
			t.Fatalf("fixture setup broken: expected %q to be recognised as a candidate type, candidates: %+v", want, candidates)
		}
	}

	reachable := findReachableFuncs(candidates, files)
	if len(reachable) < 12 {
		t.Fatalf("fixture setup broken: expected a substantial matrix of reachable functions (>=12), got %d: this test's coverage claim "+
			"depends on the fixture actually exercising many shapes, not on the key-uniqueness check alone", len(reachable))
	}

	seen := make(map[string]*ReachableFunc, len(reachable))
	for _, rf := range reachable {
		key := violationKey(rf)
		if prior, dup := seen[key]; dup {
			t.Fatalf("BUG-119 ROUND 7 REGRESSION: two DISTINCT declarations in the enumerated shape matrix collided on the same "+
				"violationKey %q -- first: %s:%d (%s %q, receiver expr %q, matched expr %q); second: %s:%d (%s %q, receiver expr %q, "+
				"matched expr %q). The complete-identity key is not actually complete.",
				key,
				prior.File, prior.Line, prior.Kind, prior.FuncName, prior.ReceiverExprPrinted, prior.MatchedExprPrinted,
				rf.File, rf.Line, rf.Kind, rf.FuncName, rf.ReceiverExprPrinted, rf.MatchedExprPrinted,
			)
		}
		seen[key] = rf
	}

	t.Logf("BUG-119 round 7 matrix: %d distinct reachable functions, %d distinct keys (all unique)", len(reachable), len(seen))
}

// --- BUG-119 round 7 Part 2: the self-check must hard-fail on a real ---
// --- key collision, not silently merge it                            ---

// duplicateDeclarationFixture deliberately reproduces the one shape the
// new complete-identity key CANNOT distinguish by construction: two
// bit-for-bit IDENTICAL function declarations (same file, same name, same
// receiver-less "enclosing declaration", same parameter name, same
// parameter type expression). Real, compiling Go forbids two package-level
// declarations of the same name from coexisting -- but astgate's scan is
// syntax-only (go/parser, no go/types -- see doc.go's "no type-checking"
// blind spot), so a file shaped like this parses without error even
// though `go build` would reject it. This is therefore a genuine
// same-key-from-two-distinct-AST-nodes case astgate's syntactic scope can
// actually encounter, used here purely to PROVE the self-check fires --
// not a claim that this shape occurs in real, compiling source.
const duplicateDeclarationFixture = `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func Consume(g *Guarded) {
	_ = g
}

func Consume(g *Guarded) {
	_ = g
}
`

// TestRun_SelfCheck_HardFailsOnGenuineKeyCollision is the required proof
// (per the dispatch brief) that the self-check mechanism itself actually
// works, not merely that "the key is now more complete" implies no future
// collision can slip through silently. It deliberately constructs two
// colliding declarations (duplicateDeclarationFixture) and confirms Run
// returns a hard MET-F703 error naming both locations, rather than
// silently keeping only one Finding.
func TestRun_SelfCheck_HardFailsOnGenuineKeyCollision(t *testing.T) {
	root := writeFixturePkg(t, "selfcheckfix", map[string]string{"fixture.go": duplicateDeclarationFixture})

	res, err := Run(root, "internal")
	if err == nil {
		t.Fatalf("BUG-119 ROUND 7 REGRESSION (self-check): Run returned no error for a fixture with two DISTINCT declarations "+
			"producing an identical violationKey -- the self-check did not fire; got Result with %d findings: %v", len(res.Findings), res.Findings)
	}

	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected a registry-sourced *errs.E (GR#7) for the self-check failure, got %T: %v", err, err)
	}
	if e.Code != "MET-F703" {
		t.Errorf("expected error code MET-F703 (the self-check's registered code), got %q", e.Code)
	}
	if e.CorrelationID == "" || e.CorrelationID == "MISSING-CORRELATION-ID" {
		t.Errorf("expected a real, minted correlation ID on the self-check error, got %q", e.CorrelationID)
	}

	key, _ := e.Ctx["key"].(string)
	if key == "" {
		t.Error("expected ctx[\"key\"] to name the colliding violationKey")
	}
	firstLoc, _ := e.Ctx["firstLocation"].(string)
	secondLoc, _ := e.Ctx["secondLocation"].(string)
	if firstLoc == "" || secondLoc == "" {
		t.Errorf("expected ctx to name both colliding locations, got firstLocation=%q secondLocation=%q", firstLoc, secondLoc)
	}
	if firstLoc == secondLoc {
		t.Errorf("expected the two colliding locations to be DIFFERENT (they are two distinct declarations, at different line "+
			"numbers) -- got the same location %q twice, which would mean the self-check is comparing a node against itself, "+
			"not against a genuinely different node", firstLoc)
	}

	t.Logf("self-check fired as designed: %s", e.Display())
}

// TestRun_SelfCheck_DoesNotFireOnTheEnumeratedMatrix is the mirror-image
// proof: Run, driven end-to-end (not just findReachableFuncs +
// violationKey directly, as
// TestViolationKey_EnumeratedShapeMatrix_AllKeysUnique above does) against
// the SAME enumerated shape matrix, must complete with no error -- proving
// the self-check's hard-fail path is reached only by a genuine collision,
// not by any ordinary, non-colliding scan.
func TestRun_SelfCheck_DoesNotFireOnTheEnumeratedMatrix(t *testing.T) {
	root := writeFixturePkg(t, "keymatrixrunfix", map[string]string{"fixture.go": keyMatrixFixture})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run unexpectedly failed on the enumerated shape matrix (which contains no genuine key collision): %v", err)
	}
	if len(res.Reachable) < 12 {
		t.Fatalf("fixture setup broken: expected a substantial matrix of reachable functions (>=12), got %d", len(res.Reachable))
	}
}

// --- BUG-119 round 9: printExpr must be independent of the original ---
// --- source's line-break/whitespace/redundant-paren layout            ---
//
// Round 8 (Destructive reattack, attacker "Halyard", REJECT) found that
// round 7's printExpr -- despite deriving violationKey from "the complete
// type expression as printed" -- was NOT actually layout-independent:
// go/printer.Fprint replays the ORIGINAL source's own line-break placement
// for a multi-type-param generic instantiation (*ast.IndexListExpr, e.g.
// Map[K, V]) whenever the source happened to wrap it across lines. A
// purely cosmetic reflow of an already-flagged declaration's own generic
// type -- semantically identical, gofmt-stable either way -- therefore
// changed printExpr's output text, which changed violationKey, which
// flipped a previously-accepted finding into a false NEW-violation: round
// 1's original bug (cosmetic edit -> false new-violation), reappearing via
// the very mechanism (go/printer) the round 7 fix was built on.
//
// The tests below prove round 9's fix (canonicalizeTypeExpr's redundant-
// paren stripping + canonicalizeWhitespace's whitespace collapse, both
// inside printExpr -- see its doc comment in gate.go) directly against
// Halyard's own reproduction, then extend coverage to the broader
// formatting-invariance CLASS the round 8 bug belongs to (rounds 1-7's
// matrix tested SHAPE uniqueness; this matrix tests LAYOUT invariance for
// declarations that are the SAME shape).

// extractConsumeParamType parses src (expected to declare exactly one
// package-level function named "Consume" with exactly one parameter) and
// returns that parameter's type expression plus the fset it was parsed
// into -- a minimal, direct extraction (bypassing findReachableFuncs/
// Run's full pipeline) so these tests exercise printExpr as directly as
// Halyard's own reproduction did.
func extractConsumeParamType(t *testing.T, src string) (*token.FileSet, ast.Expr) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Consume" {
			continue
		}
		if fd.Type.Params == nil || len(fd.Type.Params.List) == 0 {
			t.Fatalf("fixture setup broken: Consume has no parameters")
		}
		return fset, fd.Type.Params.List[0].Type
	}
	t.Fatalf("fixture setup broken: no Consume function found in source")
	return nil, nil
}

// TestPrintExpr_FormattingInvariant_HalyardReproduction is BUG-119 round
// 9's required direct proof: Halyard's EXACT scenario, reproduced and
// fixed. singleLine and reflowed declare the textually-identical parameter
// type *Mapp[string, int] -- one on a single line, one with the generic
// type argument list deliberately reflowed across two lines (matching the
// round 8 finding's own example) -- and both must now produce the
// IDENTICAL printExpr output, and therefore the identical violationKey
// component.
//
// A companion check (rawPrinterFprintDiffersOnHalyardFixture, invoked
// below) independently confirms via bare printer.Fprint (no
// canonicalisation) that the two fixtures DO produce different raw text --
// proving this test would have caught round 8's actual bug, not just that
// the fixed function happens to return equal strings for an unrelated
// reason.
func TestPrintExpr_FormattingInvariant_HalyardReproduction(t *testing.T) {
	const singleLine = `package fixture

type Mapp[K comparable, V any] struct{}

func Consume(v *Mapp[string, int]) { _ = v }
`
	const reflowed = `package fixture

type Mapp[K comparable, V any] struct{}

func Consume(v *Mapp[string,
	int]) {
	_ = v
}
`
	if !rawPrinterFprintDiffersOnHalyardFixture(t, singleLine, reflowed) {
		t.Fatalf("test setup broken: bare go/printer.Fprint (no canonicalisation) produced IDENTICAL text for the single-line and " +
			"reflowed fixtures -- this test would not actually have caught round 8's bug; the fixture no longer reproduces the " +
			"original finding and needs to be revisited")
	}

	fsetA, exprA := extractConsumeParamType(t, singleLine)
	fsetB, exprB := extractConsumeParamType(t, reflowed)

	gotA := printExpr(fsetA, exprA)
	gotB := printExpr(fsetB, exprB)

	if gotA != gotB {
		t.Fatalf("BUG-119 ROUND 8 REGRESSION (Halyard): a purely cosmetic line-break reflow of the SAME generic parameter type "+
			"produced a DIFFERENT printExpr output -- single-line form: %q, reflowed form: %q. printExpr must be independent of "+
			"the original source's line-break placement.", gotA, gotB)
	}
	t.Logf("printExpr is now layout-invariant for Halyard's exact reproduction: both forms print as %q", gotA)
}

// rawPrinterFprintDiffersOnHalyardFixture is the "would this test actually
// have failed pre-fix" proof: it calls bare go/printer.Fprint directly
// (bypassing printExpr's canonicalisation entirely) on both fixtures'
// parameter type expressions and reports whether the raw output differs --
// which it must, to confirm the fixture genuinely exercises round 8's
// source-layout-replication mechanism rather than some other difference.
func rawPrinterFprintDiffersOnHalyardFixture(t *testing.T, singleLine, reflowed string) bool {
	t.Helper()
	fsetA, exprA := extractConsumeParamType(t, singleLine)
	fsetB, exprB := extractConsumeParamType(t, reflowed)
	var bufA, bufB bytes.Buffer
	if err := printer.Fprint(&bufA, fsetA, exprA); err != nil {
		t.Fatalf("printer.Fprint: %v", err)
	}
	if err := printer.Fprint(&bufB, fsetB, exprB); err != nil {
		t.Fatalf("printer.Fprint: %v", err)
	}
	return bufA.String() != bufB.String()
}

// TestPrintExpr_FormattingInvariant_Matrix extends round 7-9's coverage
// (which tested SHAPE uniqueness -- different declarations must produce
// different keys) with the mirror-image, round 8-motivated property:
// declarations that are the SAME logical shape, written with DIFFERENT
// source layout, must produce the SAME printExpr output -- this is what
// stops a purely cosmetic edit from ever flipping an accepted finding into
// a false new violation, the whole reason BUG-119 exists.
func TestPrintExpr_FormattingInvariant_Matrix(t *testing.T) {
	cases := []struct {
		name    string
		variant string
	}{
		{
			name: "multi-type-param generic reflowed across lines (Halyard's own shape)",
			variant: `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v *Pair[
	string,
	int,
]) {
	_ = v
}
`,
		},
		{
			name: "extra internal blank lines within the type argument list",
			variant: `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v *Pair[string,

	int]) {
	_ = v
}
`,
		},
		{
			name: "tabs instead of spaces around the comma",
			variant: `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v	*Pair[string,	int]) {
	_ = v
}
`,
		},
		{
			name: "redundant parens around the whole parameter type",
			variant: `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v (*Pair[string, int])) {
	_ = v
}
`,
		},
		{
			name: "doubly-redundant parens around the whole parameter type",
			variant: `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v ((*Pair[string, int]))) {
	_ = v
}
`,
		},
	}

	const baseline = `package fixture

type Pair[K comparable, V any] struct{}

func Consume(v *Pair[string, int]) { _ = v }
`
	fsetBase, exprBase := extractConsumeParamType(t, baseline)
	want := printExpr(fsetBase, exprBase)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset, expr := extractConsumeParamType(t, c.variant)
			got := printExpr(fset, expr)
			if got != want {
				t.Fatalf("BUG-119 ROUND 9 REGRESSION: layout variant (%s) produced a DIFFERENT printExpr output than the tightly-"+
					"packed baseline -- baseline: %q, variant: %q. A purely cosmetic layout difference must never change this "+
					"output, or it changes violationKey and produces a false new-violation.", c.name, want, got)
			}
		})
	}
}

// --- BUG-119 round 9 (Halyard, lower severity): dirs dedup -------------

// TestRun_DedupsOverlappingDirs proves the fix for the self-check's stale
// reasoning Halyard also flagged: Run's key-collision self-check assumed
// "a repeated key can only mean two different declarations", which does
// not hold if the SAME directory is scanned twice because a caller passed
// overlapping/duplicate dirs. Run(root, "internal/pkg", "internal/pkg")
// must behave identically to Run(root, "internal/pkg") -- no MET-F703
// self-inflicted collision, and no doubled Reachable/Findings counts.
func TestRun_DedupsOverlappingDirs(t *testing.T) {
	root := writeFixturePkg(t, "dedupfix", map[string]string{"fixture.go": keyMatrixFixture})

	once, err := Run(root, "internal/dedupfix")
	if err != nil {
		t.Fatalf("Run (single dir) unexpectedly failed: %v", err)
	}

	twice, err := Run(root, "internal/dedupfix", "internal/dedupfix")
	if err != nil {
		t.Fatalf("BUG-119 ROUND 9 REGRESSION (Halyard, dirs dedup): Run hard-failed with duplicate dirs passed for the SAME "+
			"directory, tripping the self-check on the gate's own double-scan rather than a genuine identity-scheme gap: %v", err)
	}

	if len(twice.Reachable) != len(once.Reachable) {
		t.Errorf("expected duplicate dirs to be deduped (same Reachable count as scanning once), got once=%d twice=%d",
			len(once.Reachable), len(twice.Reachable))
	}
	if len(twice.Findings) != len(once.Findings) {
		t.Errorf("expected duplicate dirs to be deduped (same Findings count as scanning once), got once=%d twice=%d",
			len(once.Findings), len(twice.Findings))
	}
}

// --- BUG-119 round 9, Bill's round-9 guardrail requirement #1 ----------
//
// Bill's ruling (BOW comment, round-9 guardrail): the formatting-invariance
// matrix "must include a full 'gofmt the entire fixture tree and re-run'
// assertion, not only hand-crafted reflows" -- and requirement #2, "the
// round-6 self-check ... must remain active and be exercised by the
// matrix". TestRun_GofmtRoundTrip_FormattingInvariant below satisfies both:
// it runs the FULL gate (Run, which includes the MET-F703 self-check) over
// a deliberately messily-formatted fixture, then over the SAME source after
// a real go/format.Source pass (the same transform `gofmt -w` performs),
// and asserts the two runs produce the identical set of violation keys --
// not a hand-picked string reflow, but whatever gofmt itself actually does.

// mangledLayoutFixture is a deliberately messily-formatted (but
// syntactically valid) rendition of a small decl matrix: irregular blank
// lines, inconsistent spacing around identifiers, and Halyard's own
// reflowed multi-type-param generic instantiation both as a receiver
// (Pair[K,\nV]) and as a matched parameter type (Pair[\n\tstring,\n\tint,\n]).
// go/format.Source normalises the SURROUNDING whitespace but -- per round
// 8's own finding -- does NOT collapse the reflowed generic argument lists
// back to one line, so this fixture continues to exercise the exact
// layout-replication mechanism round 9 fixes even after a real gofmt pass.
const mangledLayoutFixture = `package fixture


import   "sync"


type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g   *Guarded)    checkNotCopied() bool { return true }


func (g *Guarded) Method()    {}


type Pair[K comparable, V any] struct {
	mu     sync.Mutex
	values map[K]V
}

func (p *Pair[K,
	V]) checkNotCopied() bool { return true }



func Consume(v *Pair[
	string,
	int,
]) {
	_ = v
}
`

// TestRun_GofmtRoundTrip_FormattingInvariant is Bill's round-9 guardrail
// requirement #1 + #2, satisfied together: run the full gate (Run, self-
// check included) over mangledLayoutFixture as written, then again over
// the SAME source after a genuine go/format.Source pass, and assert the
// two runs' violation-key SETS are identical. A `gofmt -w` pass across the
// tree is one of the most ordinary, purely-cosmetic operations a real
// developer performs -- it must never flip an already-accepted finding
// into a false new violation, and the self-check (exercised by both Run
// calls) must not false-fire on either pass.
func TestRun_GofmtRoundTrip_FormattingInvariant(t *testing.T) {
	// Both roots use the SAME pkgDirName ("gofmtroundtrip") -- writeFixturePkg
	// gives each call its own fresh t.TempDir() root, but violationKey's
	// File component is REPO-ROOT-RELATIVE, so using the same subpath under
	// "internal/" for both is what makes the two runs' keys directly
	// comparable (a different subpath name would itself change the File
	// component and make every key differ trivially, which would test
	// nothing about gofmt).
	rootRaw := writeFixturePkg(t, "gofmtroundtrip", map[string]string{"fixture.go": mangledLayoutFixture})
	resRaw, err := Run(rootRaw, "internal/gofmtroundtrip")
	if err != nil {
		t.Fatalf("Run (raw, pre-gofmt fixture) unexpectedly failed -- possible self-check false-fire: %v", err)
	}

	formatted, err := format.Source([]byte(mangledLayoutFixture))
	if err != nil {
		t.Fatalf("go/format.Source rejected the mangled fixture -- fixture is not valid Go: %v", err)
	}
	if string(formatted) == mangledLayoutFixture {
		t.Fatalf("test setup broken: gofmt made no change to the mangled fixture -- this test is not actually exercising a real " +
			"formatting difference, it needs a messier fixture")
	}
	t.Logf("gofmt'd fixture (for the record):\n%s", formatted)

	rootFmt := writeFixturePkg(t, "gofmtroundtrip", map[string]string{"fixture.go": string(formatted)})
	resFmt, err := Run(rootFmt, "internal/gofmtroundtrip")
	if err != nil {
		t.Fatalf("Run (gofmt'd fixture) unexpectedly failed -- possible self-check false-fire: %v", err)
	}

	keysRaw := make(map[string]bool, len(resRaw.Findings))
	for _, f := range resRaw.Findings {
		keysRaw[f.Key] = true
	}
	keysFmt := make(map[string]bool, len(resFmt.Findings))
	for _, f := range resFmt.Findings {
		keysFmt[f.Key] = true
	}

	if len(keysRaw) == 0 {
		t.Fatalf("fixture setup broken: the raw fixture produced zero findings -- this test proves nothing without at least one " +
			"violation to compare across the gofmt boundary")
	}
	if len(keysRaw) != len(keysFmt) {
		t.Fatalf("BUG-119 ROUND 9 REGRESSION (Bill's gofmt-round-trip guardrail): the raw fixture produced %d distinct violation "+
			"keys, the gofmt'd SAME fixture produced %d -- a purely cosmetic gofmt pass must never change the key set. raw=%v gofmt'd=%v",
			len(keysRaw), len(keysFmt), keysRaw, keysFmt)
	}
	for k := range keysRaw {
		if !keysFmt[k] {
			t.Errorf("BUG-119 ROUND 9 REGRESSION: violation key %q present before gofmt, MISSING after gofmt -- gofmt flipped an "+
				"already-accepted finding into what would look like a new violation", k)
		}
	}
	t.Logf("gofmt round-trip is key-invariant: %d distinct keys, identical before and after go/format.Source", len(keysRaw))
}

// TestDedupDirs_PreservesOrderAndRemovesDuplicates unit-tests dedupDirs in
// isolation.
func TestDedupDirs_PreservesOrderAndRemovesDuplicates(t *testing.T) {
	got := dedupDirs([]string{"pkg", "other", "pkg", "pkg", "third", "other"})
	want := []string{"pkg", "other", "third"}
	if len(got) != len(want) {
		t.Fatalf("dedupDirs(%v) = %v, want %v", []string{"pkg", "other", "pkg", "pkg", "third", "other"}, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupDirs order mismatch at index %d: got %v, want %v", i, got, want)
		}
	}
}
