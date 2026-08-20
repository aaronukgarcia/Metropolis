package astgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// writeFixturePkg writes files (relative path -> content) under
// t.TempDir()/internal/<pkgDirName>/ and returns the temp root, so
// Run(root, "internal") walks exactly this fixture tree — the same
// on-disk-fixture pattern TestScanSourceCodes_LiteralsOnlyNotComments
// (internal/foundation/errs/source_scan_test.go) uses.
func writeFixturePkg(t *testing.T, pkgDirName string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", pkgDirName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pkgDir, err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// --- AC-1: candidate-type identification -----------------------------

// TestFindCandidateTypes_MatchesBothConditions proves AC-1's exact
// shape: a struct with BOTH a sync.Mutex/RWMutex VALUE field and an
// aliasable reference field is a candidate; a struct matching only one
// of the two conditions is not.
func TestFindCandidateTypes_MatchesBothConditions(t *testing.T) {
	src := `package fixture

import "sync"

// Guarded matches both AC-1 conditions: a sync.Mutex value field and a
// slice field.
type Guarded struct {
	mu    sync.Mutex
	items []int
}

// MutexOnly has a mutex but no aliasable field — excluded.
type MutexOnly struct {
	mu    sync.Mutex
	count int
}

// AliasableOnly has a slice but no mutex — excluded.
type AliasableOnly struct {
	items []int
}

// PointerMutex has a *sync.Mutex (pointer, not value) — AC-1 excludes
// pointer-held mutexes explicitly (a different, non-SEC-020-shaped
// hazard, out of scope).
type PointerMutex struct {
	mu    *sync.Mutex
	items []int
}
`
	root := writeFixturePkg(t, "candfix", map[string]string{"fixture.go": src})

	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/candfix"]
	if len(files) != 1 {
		t.Fatalf("expected 1 file loaded, got %d", len(files))
	}
	candidates := findCandidateTypes("internal/candfix", files)

	names := map[string]bool{}
	for _, c := range candidates {
		names[c.Name] = true
	}
	if !names["Guarded"] {
		t.Error("expected Guarded (mutex value + slice) to be a candidate")
	}
	for _, excluded := range []string{"MutexOnly", "AliasableOnly", "PointerMutex"} {
		if names[excluded] {
			t.Errorf("%s must NOT be a candidate (matches only one AC-1 condition, or a pointer mutex)", excluded)
		}
	}
}

// --- AC-3: reachable-function enumeration, the core blind-spot fix ----

// argumentBlindSpotFixture is the AC-3/AC-9 fixture: a candidate type
// Guarded with one receiver method (Method) and one package-level
// function (Consume) that takes *Guarded as a PARAMETER, not a
// receiver — the exact SetSink(l *Logger) shape that escaped nine hand
// enumerations by four agents (BUG-024's dispatch comment).
const argumentBlindSpotFixtureUnguarded = `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// Method is a receiver method — the shape every prior hand-sweep DID
// check.
func (g *Guarded) Method() {
	if !g.checkNotCopied() {
		return
	}
}

// Consume is a package-level function taking *Guarded by parameter —
// the shape every prior hand-sweep MISSED. Deliberately has NO guard
// call, for AC-3/AC-9's regression test.
func Consume(g *Guarded) {
	_ = g
}
`

// TestFindReachableFuncs_CatchesParameterFunction is AC-3's own
// required unit test: on the enumeration function alone (not the full
// gate), Consume — a package-level function taking a candidate type by
// parameter, not by receiver — must appear in the reachable-function
// list. This is the direct regression test for the blind spot named in
// BUG-024's dispatch comment.
func TestFindReachableFuncs_CatchesParameterFunction(t *testing.T) {
	root := writeFixturePkg(t, "argfix", map[string]string{"fixture.go": argumentBlindSpotFixtureUnguarded})
	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/argfix"]
	candidates := findCandidateTypes("internal/argfix", files)
	if len(candidates) != 1 || candidates[0].Name != "Guarded" {
		t.Fatalf("expected exactly 1 candidate named Guarded, got %+v", candidates)
	}

	reachable := findReachableFuncs(candidates, files)

	var sawMethod, sawConsumeAsParam bool
	for _, rf := range reachable {
		if rf.FuncName == "Method" && rf.Kind == KindReceiverMethod {
			sawMethod = true
		}
		if rf.FuncName == "Consume" {
			if rf.Kind != KindParameterFunc {
				t.Errorf("Consume found but classified as %v, want KindParameterFunc", rf.Kind)
			}
			if rf.ValueName != "g" {
				t.Errorf("Consume's matched parameter name = %q, want %q", rf.ValueName, "g")
			}
			sawConsumeAsParam = true
		}
	}
	if !sawMethod {
		t.Error("expected receiver method Method to be found (sanity check on the enumeration itself)")
	}
	if !sawConsumeAsParam {
		t.Fatal("AC-3 REGRESSION: Consume — a package-level function taking *Guarded as a parameter — was NOT found by findReachableFuncs. " +
			"This is the exact blind spot (errs.SetSink) that escaped nine manual enumerations by four agents (BUG-024).")
	}
}

// --- AC-4: guard-call detection, receiver and parameter -----------------

// TestIsGuarded_DetectsGuardCallOnReceiverAndParameter proves AC-4 on
// both enumeration paths: a receiver method calling checkNotCopied on
// itself is guarded; a package-level function calling checkNotCopied on
// its matched parameter is guarded; a function that never calls it is
// not.
func TestIsGuarded_DetectsGuardCallOnReceiverAndParameter(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func (g *Guarded) GuardedMethod() {
	if !g.checkNotCopied() {
		return
	}
}

func (g *Guarded) UnguardedMethod() {}

func GuardedFunc(g *Guarded) {
	if !g.checkNotCopied() {
		return
	}
}

func UnguardedFunc(g *Guarded) {
	_ = g
}
`
	root := writeFixturePkg(t, "guardfix", map[string]string{"fixture.go": src})
	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/guardfix"]
	candidates := findCandidateTypes("internal/guardfix", files)
	reachable := findReachableFuncs(candidates, files)

	got := map[string]bool{}
	for _, rf := range reachable {
		got[rf.FuncName] = isGuarded(rf)
	}
	want := map[string]bool{
		"GuardedMethod":   true,
		"UnguardedMethod": false,
		"GuardedFunc":     true,
		"UnguardedFunc":   false,
	}
	for name, wantGuarded := range want {
		if got[name] != wantGuarded {
			t.Errorf("isGuarded(%s) = %v, want %v", name, got[name], wantGuarded)
		}
	}
}

// --- AC-8/AC-9/AC-10: the full gate, driven through Run -----------------

// TestRun_CatchesUnguardedType_NegativeControl is AC-8: a candidate type
// with ZERO guarded reachable functions, run through the REAL gate logic
// (Run, not a hand assertion of intent), must produce at least one
// violation naming that type.
func TestRun_CatchesUnguardedType_NegativeControl(t *testing.T) {
	src := `package fixture

import "sync"

type WhollyUnguarded struct {
	mu    sync.Mutex
	items []int
}

func (w *WhollyUnguarded) checkNotCopied() bool { return true }

func (w *WhollyUnguarded) Add(x int) {
	w.items = append(w.items, x)
}
`
	root := writeFixturePkg(t, "unguardedfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Violations) == 0 {
		t.Fatal("AC-8 REGRESSION: expected at least 1 violation for a wholly-unguarded candidate type, got 0 — the gate detects nothing")
	}
	found := false
	for _, v := range res.Violations {
		if strings.Contains(v, "WhollyUnguarded") && strings.Contains(v, "Add") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a violation naming WhollyUnguarded's Add method, got: %v", res.Violations)
	}
}

// TestRun_CatchesUnguardedPackageLevelFunction_ArgumentBlindSpot is
// AC-9: the direct regression test for the actual incident BUG-024 was
// filed over. A candidate type with a FULLY-GUARDED receiver-method
// surface but one UNGUARDED package-level function taking it by
// parameter (mirroring SetSink's exact shape) must be reported as a
// violation. A gate that only checks methods would pass this fixture
// clean — that is itself a FAIL of this criterion.
func TestRun_CatchesUnguardedPackageLevelFunction_ArgumentBlindSpot(t *testing.T) {
	root := writeFixturePkg(t, "argblindspot", map[string]string{"fixture.go": argumentBlindSpotFixtureUnguarded})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var methodViolation, consumeViolation bool
	for _, v := range res.Violations {
		if strings.Contains(v, "\"Method\"") {
			methodViolation = true
		}
		if strings.Contains(v, "\"Consume\"") {
			consumeViolation = true
		}
	}
	if methodViolation {
		t.Error("Method (a guarded receiver method) was reported as a violation — false positive, the receiver-method path is broken")
	}
	if !consumeViolation {
		t.Fatal("AC-9 REGRESSION (the actual BUG-024 incident): Consume — a package-level function taking *Guarded by " +
			"parameter, with no guard call — was NOT reported as a violation, even though every receiver method on Guarded " +
			"is fine. A gate that only checks methods would pass this fixture clean; that is exactly the failure this item exists to close.")
	}
}

// TestRun_CleanCase_NoViolations is AC-10: a candidate type where every
// reachable function — both receiver methods AND package-level
// functions — guards correctly must produce ZERO violations. This is
// what proves AC-8/AC-9's fixtures exercise real detection logic rather
// than an always-fails (or always-passes) gate.
func TestRun_CleanCase_NoViolations(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func (g *Guarded) Method() {
	if !g.checkNotCopied() {
		return
	}
}

func Consume(g *Guarded) {
	if !g.checkNotCopied() {
		return
	}
}
`
	root := writeFixturePkg(t, "cleanfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Violations) != 0 {
		t.Fatalf("expected 0 violations for a fully-guarded fixture (both receiver method and parameter function), got: %v", res.Violations)
	}
}

// --- AC-6/AC-7: flooding-risk advisory flag ------------------------------

// TestFindFloodRisks_FlagsSharedRing_NotDedicated is AC-6/AC-7's fixture:
// a guarded function whose rejection path pushes into a package-level,
// ring-shaped variable that is ALSO used by ordinary (non-rejection)
// code must be flagged; a guarded function whose rejection path writes
// to its own dedicated, unshared buffer must NOT be flagged.
func TestFindFloodRisks_FlagsSharedRing_NotDedicated(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

type ringBuffer struct{ n int }

func (r *ringBuffer) push(x int) { r.n++ }

// sharedRing is read/written by ordinary, non-rejection code (Recent)
// AND by a guarded function's rejection path (Risky) — the SEC-030/
// SEC-031(b) shape.
var sharedRing = &ringBuffer{}

// Recent is ordinary, non-rejection code that uses sharedRing.
func Recent() int {
	return sharedRing.n
}

// Risky's rejection path pushes into the SHARED ring — must be flagged.
func (g *Guarded) Risky(x int) {
	if !g.checkNotCopied() {
		sharedRing.push(x)
		return
	}
}

// dedicatedRing is used ONLY from Safe's rejection path — never
// referenced anywhere else in the package.
var dedicatedRing = &ringBuffer{}

// Safe's rejection path pushes into its OWN dedicated, unshared ring —
// must NOT be flagged.
func (g *Guarded) Safe(x int) {
	if !g.checkNotCopied() {
		dedicatedRing.push(x)
		return
	}
}
`
	root := writeFixturePkg(t, "floodfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Violations) != 0 {
		t.Fatalf("expected 0 hard violations (both Risky and Safe guard correctly), got: %v", res.Violations)
	}

	var flaggedRisky, flaggedSafe bool
	for _, fr := range res.FloodRisks {
		if fr.FuncName == "Risky" {
			flaggedRisky = true
		}
		if fr.FuncName == "Safe" {
			flaggedSafe = true
		}
	}
	if !flaggedRisky {
		t.Errorf("AC-6 REGRESSION: Risky's rejection path pushes into sharedRing (also used by Recent), and was NOT flagged. Findings: %v", res.FloodRisks)
	}
	if flaggedSafe {
		t.Errorf("AC-6 false positive: Safe's rejection path pushes into dedicatedRing, which is used ONLY there — must not be flagged. Findings: %v", res.FloodRisks)
	}
}

// --- Destructive-attack regression fixes (BUG-024 P0 findings #1-#4) ---
//
// The four tests below reproduce, verbatim in shape, the Destructive
// agent's own constructed fixtures that escaped the pre-fix gate. Each
// was confirmed to FAIL against the pre-fix code (temporarily reverting
// findReachableFuncs/isSyncMutexValue/baseTypeName locally, rerunning,
// observing the new test fail, then reapplying the fix) before being
// checked in as a permanent regression test.

// TestRun_CatchesForeignReceiverMethodParameter is the regression test
// for Destructive finding #1: a method whose OWN receiver type is not a
// candidate (Attacher, no mutex/aliasable fields) must still be checked
// for a candidate-typed PARAMETER. Pre-fix, findReachableFuncs saw
// fd.Recv != nil, checked the receiver against candidates, found no
// match, and unconditionally `continue`d past parameter scanning — so
// Attach's *Guarded parameter was never enumerated at all, and a
// trivial future refactor of a SetSink-shaped function into any
// receiver method would silently reopen the exact hole AC-9 exists to
// close forever.
func TestRun_CatchesForeignReceiverMethodParameter(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// Attacher's receiver type is NOT a candidate — it has no mutex or
// aliasable field of its own.
type Attacher struct{}

// Attach takes *Guarded by parameter, with no guard call. Pre-fix, this
// function never entered the reachable-function list at all because it
// has a receiver (Attacher), so the parameter scan never ran.
func (r *Attacher) Attach(g *Guarded) {
	g.items = append(g.items, 1)
}
`
	root := writeFixturePkg(t, "foreignrecvfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, v := range res.Violations {
		if strings.Contains(v, "Attach") && strings.Contains(v, "Guarded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("DESTRUCTIVE FINDING #1 REGRESSION: Attach — a method with a foreign (non-candidate) receiver, taking "+
			"*Guarded by parameter with no guard call — was NOT reported as a violation. Violations: %v", res.Violations)
	}
}

// TestRun_CatchesReceiverMethodsSecondCandidateParameter is the
// regression test for Destructive finding #2: a receiver method can
// simultaneously be checked-via-its-receiver AND take a SECOND,
// independent candidate-typed parameter that must be checked
// separately. Merge's receiver g is correctly guarded, but its second
// parameter other is read (other.items) with no guard call on it at
// all — pre-fix, the unconditional `continue` after receiver handling
// meant `other` was never scanned, so the gate reported zero
// violations even though a live *Guarded value was read unguarded.
func TestRun_CatchesReceiverMethodsSecondCandidateParameter(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// Merge guards its receiver g correctly, but reads a SECOND *Guarded
// value (other) via append without ever calling other.checkNotCopied().
func (g *Guarded) Merge(other *Guarded) {
	if !g.checkNotCopied() {
		return
	}
	g.items = append(g.items, other.items...)
}
`
	root := writeFixturePkg(t, "secondparamfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var mergeReceiverFalselyFlagged, mergeParamFlagged bool
	for _, v := range res.Violations {
		if strings.Contains(v, "\"Merge\"") && strings.Contains(v, "receiver") {
			mergeReceiverFalselyFlagged = true
		}
		if strings.Contains(v, "\"Merge\"") && strings.Contains(v, "\"other\"") {
			mergeParamFlagged = true
		}
	}
	if mergeReceiverFalselyFlagged {
		t.Error("Merge's receiver g (correctly guarded) was reported as a violation — false positive")
	}
	if !mergeParamFlagged {
		t.Fatalf("DESTRUCTIVE FINDING #2 REGRESSION: Merge's second parameter `other` — read via other.items with no guard "+
			"call on it — was NOT reported as a violation. Violations: %v", res.Violations)
	}
}

// TestFindCandidateTypes_ResolvesLocalMutexAlias is the regression test
// for Destructive finding #3: a same-package `type Mu = sync.Mutex`
// alias field must still be recognised as a mutex-value field. Pre-fix,
// isSyncMutexValue required the field's type expression to literally be
// *ast.SelectorExpr{X: sync, Sel: Mutex|RWMutex}; `mu Mu` is a bare
// *ast.Ident and never matched, so AliasGuarded (mutex + slice,
// textbook SEC-020 shape) was invisible to the gate entirely.
func TestFindCandidateTypes_ResolvesLocalMutexAlias(t *testing.T) {
	src := `package fixture

import "sync"

type Mu = sync.Mutex

type AliasGuarded struct {
	mu    Mu
	items []int
}
`
	root := writeFixturePkg(t, "aliasfix", map[string]string{"fixture.go": src})
	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/aliasfix"]
	candidates := findCandidateTypes("internal/aliasfix", files)

	found := false
	for _, c := range candidates {
		if c.Name == "AliasGuarded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DESTRUCTIVE FINDING #3 REGRESSION: AliasGuarded (a locally type-aliased `type Mu = sync.Mutex` field plus a "+
			"slice field) was NOT identified as a candidate type. Candidates: %+v", candidates)
	}
}

// TestRun_CatchesUnguardedTypeAliasedMutex is finding #3's full-gate
// proof: AliasGuarded, once recognised as a candidate (see the test
// above), must also flow through reachable-function enumeration and
// guard detection like any other candidate — an unguarded function
// taking it must be reported as a violation.
func TestRun_CatchesUnguardedTypeAliasedMutex(t *testing.T) {
	src := `package fixture

import "sync"

type Mu = sync.Mutex

type AliasGuarded struct {
	mu    Mu
	items []int
}

func (a *AliasGuarded) checkNotCopied() bool { return true }

func (a *AliasGuarded) Unguarded() {
	a.items = append(a.items, 1)
}
`
	root := writeFixturePkg(t, "aliasrunfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, v := range res.Violations {
		if strings.Contains(v, "AliasGuarded") && strings.Contains(v, "Unguarded") {
			found = true
		}
	}
	if !found {
		t.Fatalf("DESTRUCTIVE FINDING #3 REGRESSION: AliasGuarded's Unguarded method was NOT reported as a violation "+
			"through the full gate. Violations: %v", res.Violations)
	}
}

// TestFindReachableFuncs_CatchesSliceAndVariadicParameters is the
// regression test for Destructive finding #4: baseTypeName only
// recognised *ast.Ident and *ast.StarExpr{X: Ident}; []*T, []T
// (*ast.ArrayType) and ...T (*ast.Ellipsis) are different AST node
// kinds and were silently skipped, so ConsumeAll ([]*Guarded) and
// ConsumeVariadic (...Guarded) never entered the reachable-function
// list — variadic is especially real since each variadic argument IS a
// fresh value-copy of the candidate struct at the call site.
func TestFindReachableFuncs_CatchesSliceAndVariadicParameters(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func ConsumeAll(gs []*Guarded) {
	_ = gs
}

func ConsumeVariadic(gs ...Guarded) {
	_ = gs
}
`
	root := writeFixturePkg(t, "slicevariadicfix", map[string]string{"fixture.go": src})
	pkgs, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := pkgs["internal/slicevariadicfix"]
	candidates := findCandidateTypes("internal/slicevariadicfix", files)
	reachable := findReachableFuncs(candidates, files)

	var sawSlice, sawVariadic bool
	for _, rf := range reachable {
		if rf.FuncName == "ConsumeAll" && rf.Kind == KindParameterFunc {
			sawSlice = true
		}
		if rf.FuncName == "ConsumeVariadic" && rf.Kind == KindParameterFunc {
			sawVariadic = true
		}
	}
	if !sawSlice {
		t.Error("DESTRUCTIVE FINDING #4 REGRESSION: ConsumeAll(gs []*Guarded) was NOT found by findReachableFuncs — a slice-of-candidate parameter is invisible")
	}
	if !sawVariadic {
		t.Error("DESTRUCTIVE FINDING #4 REGRESSION: ConsumeVariadic(gs ...Guarded) was NOT found by findReachableFuncs — a variadic candidate parameter is invisible")
	}
}

// TestRun_CatchesUnguardedSliceAndVariadicParameters is finding #4's
// full-gate proof, mirroring TestRun_CatchesUnguardedPackageLevelFunction_ArgumentBlindSpot's
// shape but for slice/variadic parameters specifically.
func TestRun_CatchesUnguardedSliceAndVariadicParameters(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func ConsumeAll(gs []*Guarded) {
	_ = gs
}

func ConsumeVariadic(gs ...Guarded) {
	_ = gs
}
`
	root := writeFixturePkg(t, "slicevariadicrunfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sliceViolation, variadicViolation bool
	for _, v := range res.Violations {
		if strings.Contains(v, "ConsumeAll") {
			sliceViolation = true
		}
		if strings.Contains(v, "ConsumeVariadic") {
			variadicViolation = true
		}
	}
	if !sliceViolation {
		t.Errorf("expected a violation naming ConsumeAll (slice-of-candidate, unguarded), got: %v", res.Violations)
	}
	if !variadicViolation {
		t.Errorf("expected a violation naming ConsumeVariadic (variadic candidate, unguarded), got: %v", res.Violations)
	}
}

// --- SEC-049: field-access chain (WorldAPI-wraps-*World shape) --------

// worldAPIShapedFixture mirrors the real engine.world/worldapi.go shape
// SEC-049 was filed against: World is the candidate type (a by-value
// sync.RWMutex plus an aliasable map field); WorldAPI wraps *World as a
// struct field (not a receiver, not a parameter) and its own exported
// methods reach World's mutex directly via the field
// (a.w.mu.Lock()/a.w.checkNotCopied(...)). guarded controls whether
// TouchGuarded calls a.w.checkNotCopied(...) before touching a.w.mu, so
// the same fixture shape proves both the positive (unguarded → flagged)
// and negative (guarded → clean) cases from one source template.
func worldAPIShapedFixture(guarded bool) string {
	guardCall := ""
	if guarded {
		guardCall = "\tif !a.w.checkNotCopied() {\n\t\treturn\n\t}\n"
	}
	return `package fixture

import "sync"

type World struct {
	mu    sync.RWMutex
	tiles map[int]int
}

func (w *World) checkNotCopied() bool { return true }

type WorldAPI struct {
	w *World
}

func (a *WorldAPI) TouchGuarded() {
` + guardCall + `	a.w.mu.Lock()
	defer a.w.mu.Unlock()
}
`
}

// TestFindFieldReachableFuncs_CatchesFieldAccessChain is SEC-049's core
// regression test: WorldAPI.TouchGuarded reaches World's mutex via the
// field-then-method chain a.w — a shape neither the receiver-method path
// (WorldAPI is not itself a candidate type: it has no mutex field of its
// own) nor the parameter path (TouchGuarded takes no World-typed
// parameter — World arrives already embedded in WorldAPI's own struct
// layout) recognised before this fix. Confirms findFieldReachableFuncs
// directly: exactly one KindFieldAccess ReachableFunc for World, keyed on
// the printed chain "a.w".
func TestFindFieldReachableFuncs_CatchesFieldAccessChain(t *testing.T) {
	root := writeFixturePkg(t, "worldapifieldfix", map[string]string{"fixture.go": worldAPIShapedFixture(false)})
	byDir, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := byDir["internal/worldapifieldfix"]
	if len(files) == 0 {
		t.Fatal("fixture package not found by loadPackages")
	}
	candidates := findCandidateTypes("internal/worldapifieldfix", files)
	byName := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = true
	}
	if !byName["World"] {
		t.Fatalf("World was not detected as a candidate type at all — fixture setup is broken, not testing SEC-049: candidates=%v", candidates)
	}

	fieldReachable := findFieldReachableFuncs(byName, files)
	var found *ReachableFunc
	for _, rf := range fieldReachable {
		if rf.FuncName == "TouchGuarded" && rf.TypeName == "World" {
			found = rf
		}
	}
	if found == nil {
		t.Fatalf("SEC-049 REGRESSION: WorldAPI.TouchGuarded (reaches World's mutex via the field-then-method chain a.w) "+
			"was not enumerated as a ReachableFunc for candidate type World — findFieldReachableFuncs returned: %v", fieldReachable)
	}
	if found.Kind != KindFieldAccess {
		t.Errorf("expected Kind == KindFieldAccess, got %v", found.Kind)
	}
	if found.ValueName != "a.w" {
		t.Errorf("expected ValueName == %q (the printed receiver.field chain), got %q", "a.w", found.ValueName)
	}
}

// TestRun_CatchesUnguardedFieldAccessChain is SEC-049's live end-to-end
// regression test: Run must report WorldAPI.TouchGuarded as a violation
// when it never calls a.w.checkNotCopied() before touching a.w.mu — the
// exact live gap the BOW finding described (WorldAPI's 11 real exported
// methods, invisible to reachable-function enumeration). Before this
// fix, Run reported ZERO violations for this fixture — WorldAPI's method
// matched neither the receiver-method path (WorldAPI is not a candidate
// type) nor the parameter path (no World-typed parameter).
func TestRun_CatchesUnguardedFieldAccessChain(t *testing.T) {
	root := writeFixturePkg(t, "worldapifieldrunfix", map[string]string{"fixture.go": worldAPIShapedFixture(false)})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, v := range res.Violations {
		if strings.Contains(v, "TouchGuarded") && strings.Contains(v, "World") {
			found = true
		}
	}
	if !found {
		t.Fatalf("SEC-049 REGRESSION: expected a violation naming WorldAPI.TouchGuarded's unguarded reach into candidate type "+
			"World via the field-access chain a.w, got: %v", res.Violations)
	}
}

// TestRun_FieldAccessChain_CleanCaseNoFalsePositive is SEC-049's negative
// control: the identical fixture, but with a.w.checkNotCopied() called
// first, must produce ZERO violations naming TouchGuarded — proving the
// new field-access path actually inspects guard presence rather than
// unconditionally flagging every field-wrapped method.
func TestRun_FieldAccessChain_CleanCaseNoFalsePositive(t *testing.T) {
	root := writeFixturePkg(t, "worldapifieldcleanfix", map[string]string{"fixture.go": worldAPIShapedFixture(true)})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, v := range res.Violations {
		if strings.Contains(v, "TouchGuarded") {
			t.Errorf("false positive: TouchGuarded calls a.w.checkNotCopied() before touching a.w.mu but was still flagged: %s", v)
		}
	}
}

// holderSliceFieldFixture reproduces the SEC-049 round-2 Destructive
// attacker's exact fixture: a wrapping type (Holder) holding a candidate
// type via a SLICE field (ws []*World), not a direct pointer field. Both
// gate.go's collectFieldWraps doc comment and doc.go's "Known blind
// spots" claimed this shape was "deliberately NOT matched" — the
// attacker proved that claim false: baseTypeName's *ast.ArrayType case
// (added earlier for the slice-parameter fix) unwraps right through the
// field's slice type down to "World", so before the round-2 fix
// Holder.Bad was silently registered as a KindFieldAccess ReachableFunc
// for World via the uncompilable chain h.ws.checkNotCopied(...) — a
// finding that could never be marked guarded, a permanent false-positive
// generator. isContainerFieldType now rejects this shape explicitly.
func holderSliceFieldFixture() string {
	return `package fixture

import "sync"

type World struct {
	mu    sync.RWMutex
	tiles map[int]int
}

func (w *World) checkNotCopied() bool { return true }

type Holder struct {
	ws []*World
}

func (h *Holder) Bad() {
	for _, w := range h.ws {
		w.mu.Lock()
		defer w.mu.Unlock()
	}
}
`
}

// TestCollectFieldWraps_RejectsSliceOfCandidateField is SEC-049 round 2's
// core regression test (the Destructive reattack): a field typed as a
// slice of a candidate type (ws []*World) must NOT be recorded as a
// fieldWrap at all — collectFieldWraps must reject it via
// isContainerFieldType, matching what the doc comments have always
// claimed. Before the round-2 fix this assertion failed: the field WAS
// recorded (OwnerType Holder, FieldName ws, Candidate World).
func TestCollectFieldWraps_RejectsSliceOfCandidateField(t *testing.T) {
	root := writeFixturePkg(t, "holderslicefieldfix", map[string]string{"fixture.go": holderSliceFieldFixture()})
	byDir, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := byDir["internal/holderslicefieldfix"]
	if len(files) == 0 {
		t.Fatal("fixture package not found by loadPackages")
	}
	candidates := findCandidateTypes("internal/holderslicefieldfix", files)
	byName := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = true
	}
	if !byName["World"] {
		t.Fatalf("World was not detected as a candidate type at all — fixture setup is broken: candidates=%v", candidates)
	}

	wraps := collectFieldWraps(byName, files)
	for _, w := range wraps {
		if w.OwnerType == "Holder" && w.FieldName == "ws" {
			t.Fatalf("SEC-049 ROUND 2 REGRESSION: Holder.ws (a slice-of-candidate field, []*World) was recorded as a "+
				"fieldWrap (%+v) — collectFieldWraps must reject container-typed fields, matching the doc's disclosed scope", w)
		}
	}
}

// TestFindFieldReachableFuncs_NoFindingForSliceOfCandidateField is the
// end-to-end companion: Holder.Bad (which touches h.ws, a slice field)
// must produce NO ReachableFunc/KindFieldAccess entry for World at all —
// not even an unguardable one — since a container field can never
// satisfy isGuarded's literal single-chain checkNotCopied() call shape.
func TestFindFieldReachableFuncs_NoFindingForSliceOfCandidateField(t *testing.T) {
	root := writeFixturePkg(t, "holderslicefieldrunfix", map[string]string{"fixture.go": holderSliceFieldFixture()})
	byDir, err := loadPackages(root, "internal")
	if err != nil {
		t.Fatalf("loadPackages: %v", err)
	}
	files := byDir["internal/holderslicefieldrunfix"]
	if len(files) == 0 {
		t.Fatal("fixture package not found by loadPackages")
	}
	candidates := findCandidateTypes("internal/holderslicefieldrunfix", files)
	byName := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = true
	}

	fieldReachable := findFieldReachableFuncs(byName, files)
	for _, rf := range fieldReachable {
		if rf.FuncName == "Bad" && rf.TypeName == "World" {
			t.Fatalf("SEC-049 ROUND 2 REGRESSION: Holder.Bad was registered as a ReachableFunc (%+v) for a slice-field "+
				"access chain — this shape must never be enumerated, since no guard call can ever satisfy it", rf)
		}
	}

	// Also confirm the live end-to-end Run path stays clean for this
	// fixture: no violation naming Bad should ever appear, now or later,
	// since a container-of-candidate field is explicitly out of scope.
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, v := range res.Violations {
		if strings.Contains(v, "Bad") && strings.Contains(v, "World") {
			t.Errorf("unexpected violation naming Holder.Bad for the slice-field shape (should be out of scope entirely): %s", v)
		}
	}
}

// TestRun_CatchesCandidateCopiedInsideFuncLit is BUG-138's regression
// test: a package-level closure (*ast.FuncLit assigned to a var) that
// copies a candidate type by value and locks the copy must be caught,
// even though no ordinary *ast.FuncDecl in the package takes that
// candidate type by parameter — reproduces the Destructive attacker's
// exact fixture (Guarded copied+locked inside Copier, only reached via
// a wrapper FuncDecl, Trigger, that itself takes no candidate param).
func TestRun_CatchesCandidateCopiedInsideFuncLit(t *testing.T) {
	src := `package fixture

import "sync"

type Guarded struct {
	mu  sync.Mutex
	ref *int
}

func (g *Guarded) checkNotCopied() bool { return true }

var Copier = func(g Guarded) *int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ref
}

func Trigger() {
	Copier(Guarded{})
}
`
	root := writeFixturePkg(t, "funclitrunfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var found bool
	for _, v := range res.Violations {
		if strings.Contains(v, "Copier") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a violation naming Copier (candidate copied+locked inside a closure, unguarded), got: %v", res.Violations)
	}
}

// --- AC-13: run against the live repo tree, baseline-ratchet enforced ---

// acceptedFindingsFile is accepted-findings.json's path relative to the
// repo root (resolveRepoRoot) -- the committed ratchet allowlist BUG-024's
// ASM-204 ruling requires. Kept as a named constant, not inlined, so
// TestRun_LiveTree_ReportsFindings' filepath.Join call and this file's own
// doc comments stay obviously in sync with the actual on-disk name.
const acceptedFindingsFile = "internal/foundation/astgate/accepted-findings.json"

// TestRun_LiveTree_ReportsFindings runs the real gate against cmd/ and
// internal/ and enforces the BUG-024 baseline ratchet (ASM-204's lead
// ruling): a violation whose exact message text has a matching entry in
// the committed allowlist (acceptedFindingsFile) is TOLERATED (logged,
// does not fail the build); a violation with NO matching entry is a
// genuinely NEW finding and FAILS the test via t.Error.
//
// History: report-only via t.Logf (never failing the build) was accepted
// as the LANDING state only, on the condition that a baseline ratchet
// followed as a required BUG-024 deliverable -- "a gate that reports via
// t.Logf is not a gate" (ASM-204's ruling comment). This is that ratchet,
// built the same way this project's other baseline ratchet
// (internal/harness/synth's AcceptedRegistry, BUG-095) works: a
// git-committed file is the sole source of truth for what is accepted,
// loaded fresh on every run, checked against independently of anything
// the scan itself claims (GR#3 -- one ratchet convention, not two).
//
// As of this writing the live tree has 78 real findings, the large
// majority UNEXPORTED helper methods (typically *Locked-suffixed, e.g.
// rotateLocked, tileLocked, sortedEntriesLocked) that this codebase's own
// established, reviewed convention treats as safe BECAUSE they are only
// ever reached through an already-guarded exported entry point (see
// internal/foundation/errs/copyguard_test.go's enumeration note on
// rotateLocked for the precedent) — a pattern this gate's syntactic,
// no-call-graph analysis cannot currently distinguish from a genuine
// unguarded entry point (see doc.go's blind-spot list). All 78 are
// recorded in acceptedFindingsFile, generated from a live scan (GR#15 --
// not hand-typed), and triaging any of them down to zero is separate
// BOW-tracked work (ASM-120, ASM-121), not something this ratchet
// resolves by itself. What the ratchet DOES guarantee: any 79th finding
// introduced by future code is a hard, unavoidable test failure — the
// build can no longer silently accumulate MORE of these, only the 78
// already reviewed and accepted.
func TestRun_LiveTree_ReportsFindings(t *testing.T) {
	root, err := resolveRepoRoot()
	if err != nil {
		t.Fatalf("resolveRepoRoot: %v", err)
	}
	res, err := Run(root, "cmd", "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("astgate live-tree scan: %d candidate types, %d reachable functions, %d hard violations, %d advisory flood-risk flags",
		len(res.Candidates), len(res.Reachable), len(res.Violations), len(res.FloodRisks))

	acceptedPath := filepath.Join(root, filepath.FromSlash(acceptedFindingsFile))
	accepted, err := LoadAcceptedFindings(acceptedPath)
	if err != nil {
		t.Fatalf("LoadAcceptedFindings(%s): %v", acceptedPath, err)
	}

	enforceRatchet(t, acceptedFindingsFile, res.Findings, accepted)

	// Advisory-only findings (flood risks) are not part of the hard gate
	// AC-13 wires into the merge-blocking surface — they remain report-only,
	// exactly as floodMessage's own doc comment says ("advisory only").
	for _, f := range res.FloodMessages {
		t.Logf("FLOOD-RISK (advisory, not ratchet-enforced): %s", f)
	}

	// SEC-051's fix: every allowlist entry MUST correspond to a currently
	// live violation. This used to be advisory-only (t.Logf, "STALE entry",
	// never failing the build) -- SEC-051 demonstrated live that the
	// advisory posture let a fabricated finding be pre-approved in
	// accepted-findings.json before the violating code existed, and the
	// gate silently tolerated it (LoadAcceptedFindings never cross-checked
	// an entry against real scan output). enforceNoOrphanedEntries below
	// makes that check a hard failure — see its own doc comment for why a
	// "fabricated" and a "merely stale" entry are treated identically.
	enforceNoOrphanedEntries(t, acceptedFindingsFile, res.Findings, accepted)
}

// ratchetReporter is the minimal testing.TB subset enforceRatchet needs.
// *testing.T satisfies it directly (used by TestRun_LiveTree_ReportsFindings).
// It is factored out as an interface, rather than taking *testing.T
// concretely, so the TestRatchet_* regression tests below can substitute a
// recording stub (fakeRatchetReporter) instead of a real *testing.T:
// go's testing package marks EVERY ancestor T as failed the instant any
// t.Run subtest calls Errorf/Fatalf, with no way to suppress that
// propagation from inside the subtest — so proving "this deliberately-bad
// fixture makes enforceRatchet report a failure" with a genuine *testing.T
// would make that regression test (and the whole package run) itself
// report FAIL, which is not what a "prove the gate can fail" test should
// do to its own suite.
type ratchetReporter interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
}

// enforceRatchet applies the BUG-024 baseline-ratchet decision to
// findings, given an already-loaded AcceptedFindings registry: a finding
// whose LOCATION-STABLE key (Finding.Key, violationKey — BUG-119's fix;
// see gate.go's doc comment on why this is no longer the full message
// text) matches an allowlist entry is logged and tolerated; any other
// finding is a hard Errorf failure. Factored out of
// TestRun_LiveTree_ReportsFindings so the TestRatchet_* regression tests
// below can exercise the EXACT SAME enforcement code path against small
// constructed fixtures, proving the ratchet can genuinely fail (and
// genuinely pass) without depending on the live tree's finding count
// staying stable across unrelated future changes.
func enforceRatchet(t ratchetReporter, listPath string, findings []Finding, accepted AcceptedFindings) {
	t.Helper()
	newCount := 0
	for _, f := range findings {
		if reason, ok := accepted.Reason(f.Key); ok {
			t.Logf("ACCEPTED (baseline ratchet, %s): %s -- %s", listPath, f.Message, reason)
			continue
		}
		newCount++
		t.Errorf("NEW astgate violation, not present in the committed baseline-ratchet allowlist (%s): %s -- "+
			"if this is a genuinely new unguarded entry point, fix it; if it is deliberately accepted, add a "+
			"reviewed entry to %s naming the reason (BUG-024's ratchet, ASM-204)", listPath, f.Message, listPath)
	}
	if newCount > 0 {
		t.Errorf("%d NEW astgate violation(s) not covered by the baseline ratchet (see individual errors above)", newCount)
	}
}

// enforceNoOrphanedEntries implements SEC-051's fix: every entry in
// accepted MUST correspond to a CURRENTLY-live finding in findings (i.e.
// its key must equal some finding's Key). An entry that matches nothing
// is a hard Errorf failure, regardless of WHY it matches nothing:
//
//   - it could be FABRICATED — pre-approved for a violation that does not
//     exist yet, SEC-051's demonstrated attack (a speculative entry added
//     to accepted-findings.json before the code producing that exact
//     finding was written);
//   - or it could be genuinely STALE — the underlying finding was fixed
//     and the entry simply never got cleaned up.
//
// This package cannot mechanically tell those two cases apart without
// git-blame archaeology (an entry has no timestamp linking it to when the
// matching violation did or didn't exist), but it does not need to: both
// are "an allowlist entry not backed by anything real right now", and the
// old advisory-only posture (t.Logf, "STALE entry", never failing the
// build — see git history) is exactly what let SEC-051's fabricated entry
// through silently. Forcing removal on sight is also strictly correct
// hygiene even in the merely-stale case (GR#18 — audit for orphaned
// entries in the same commit as the change that stales them).
//
// Factored out (mirroring enforceRatchet) so the TestOrphanedEntry_*
// regression tests below can exercise this EXACT code path against small
// constructed fixtures.
func enforceNoOrphanedEntries(t ratchetReporter, listPath string, findings []Finding, accepted AcceptedFindings) {
	t.Helper()
	live := make(map[string]bool, len(findings))
	for _, f := range findings {
		live[f.Key] = true
	}
	orphaned := 0
	for key, reason := range accepted {
		if live[key] {
			continue
		}
		orphaned++
		t.Errorf("ORPHANED astgate allowlist entry in %s: no live violation currently matches key %q (recorded reason: %q) -- "+
			"either this was fabricated/pre-approved before the violating code existed (SEC-051), or the finding it once "+
			"matched was fixed and the entry was never removed; either way, remove it from %s", listPath, key, reason, listPath)
	}
	if orphaned > 0 {
		t.Errorf("%d orphaned astgate allowlist entr(y/ies) not backed by any live violation (see individual errors above, SEC-051)", orphaned)
	}
}

// fakeRatchetReporter is a recording-only ratchetReporter stub: it never
// fails a real test, it just remembers how many times Errorf was called,
// which is exactly the signal TestRatchet_* needs ("did enforceRatchet
// decide this was a failure?") without the real-testing.T propagation
// problem ratchetReporter's doc comment explains.
type fakeRatchetReporter struct {
	errorCalls int
	logCalls   int
}

func (f *fakeRatchetReporter) Helper() {}

func (f *fakeRatchetReporter) Logf(format string, args ...any) {
	f.logCalls++
}

func (f *fakeRatchetReporter) Errorf(format string, args ...any) {
	f.errorCalls++
}

// --- BUG-024 ratchet regression coverage (proof it can actually fail) ---
//
// This project's own verification standard (Vestige: "prove every
// regression test can fail") applies here as directly as anywhere: a
// ratchet that never fails is exactly the "reports via t.Logf, never fails
// the build" decoration ASM-204's ruling exists to close. The two tests
// below run enforceRatchet — the SAME function TestRun_LiveTree_ReportsFindings
// calls — against small constructed fixtures via a t.Run subtest, so the
// subtest's own pass/fail result (t.Run's bool return) is direct,
// mechanical proof of both directions: a violation absent from the
// allowlist fails, and one present in it does not.

// ratchetFixtureSrc is a minimal AC-8-shaped unguarded fixture: one
// candidate type with a guard method, and one package-level function
// taking it by parameter that never calls the guard — exactly one
// violation, deterministically.
const ratchetFixtureSrc = `package fixture

import "sync"

type RatchetFixtureType struct {
	mu    sync.Mutex
	items []int
}

func (r *RatchetFixtureType) checkNotCopied() bool { return true }

// RatchetFixtureUnguarded deliberately has NO guard call. Its type and
// function names exist only inside this test's own temp fixture tree, so
// its violation message cannot already be present in the real, committed
// accepted-findings.json by coincidence.
func RatchetFixtureUnguarded(r *RatchetFixtureType) {
	_ = r
}
`

// TestRatchet_NewViolationNotInAllowlist_FailsBuild is the required proof
// that the ratchet can genuinely fail: a real violation, produced by the
// real gate, checked against an EMPTY allowlist (so it cannot possibly
// match), must make enforceRatchet call Errorf at least once.
func TestRatchet_NewViolationNotInAllowlist_FailsBuild(t *testing.T) {
	root := writeFixturePkg(t, "ratchetnewfix", map[string]string{"fixture.go": ratchetFixtureSrc})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation, got %d: %v", len(res.Findings), res.Findings)
	}

	accepted := AcceptedFindings{} // deliberately empty -- nothing accepted

	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", res.Findings, accepted)

	if fake.errorCalls == 0 {
		t.Fatal("RATCHET REGRESSION: enforceRatchet did NOT report any error for a violation absent from the allowlist -- " +
			"a ratchet that cannot fail is decoration, not a gate, which is exactly what ASM-204's ruling forbids")
	}
}

// TestRatchet_AllowlistedViolation_DoesNotFailBuild is the mirror-image
// proof: the SAME violation, when its EXACT message text is present in
// the allowlist, must NOT report any error. Without this half, a ratchet
// that simply fails on every violation unconditionally would also "pass"
// the test above -- this is what proves enforceRatchet's allowlist check
// does real work, not that it always fails.
func TestRatchet_AllowlistedViolation_DoesNotFailBuild(t *testing.T) {
	root := writeFixturePkg(t, "ratchetacceptedfix", map[string]string{"fixture.go": ratchetFixtureSrc})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation, got %d: %v", len(res.Findings), res.Findings)
	}

	accepted := AcceptedFindings{
		res.Findings[0].Key: "test fixture: pre-accepted for TestRatchet_AllowlistedViolation_DoesNotFailBuild",
	}

	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", res.Findings, accepted)

	if fake.errorCalls != 0 {
		t.Fatalf("an allowlisted violation (exact message match) reported %d error(s) via enforceRatchet -- the tolerate path is broken", fake.errorCalls)
	}
	if fake.logCalls == 0 {
		t.Error("expected enforceRatchet to Logf the allowlisted violation as accepted (sanity check that the tolerate path actually ran, not that it was skipped)")
	}
}

// --- BUG-119 regression: the ratchet key must survive a cosmetic edit ---
//
// TestFindingKey_StableAcrossCosmeticEditAboveIt reproduces the Destructive
// agent's own attack VERBATIM in shape: add a purely cosmetic edit (a
// comment) ABOVE an already-flagged violation in the same file, and prove
// the violation's ratchet-matching key is unchanged (so an allowlist entry
// recorded before the edit still matches after it), even though the
// violation's line number — and therefore violationMessage's text, which
// embeds "file:line" — DOES change.
//
// Confirmed, via a disposable git worktree with violationKey temporarily
// reverted to `return violationMessage(rf)` (the pre-fix behavior — the
// ratchet key WAS the full message text, which embeds the line number),
// that this exact test FAILS pre-fix: the cosmetic edit shifts rf.Line,
// which changes violationMessage's text, which changes the old key, and
// the final enforceRatchet call below reports a spurious error — exactly
// BUG-119's demonstrated live incident (one comment line added above an
// import block flipped 12 accepted commands.go findings to failing).
// Against the fix (violationKey excludes rf.Line — see gate.go), the same
// edit leaves the key unchanged and this test passes.
func TestFindingKey_StableAcrossCosmeticEditAboveIt(t *testing.T) {
	before := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func Consume(g *Guarded) {
	_ = g
}
`
	root := writeFixturePkg(t, "cosmeticfix", map[string]string{"fixture.go": before})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation, got %d: %v", len(res.Findings), res.Findings)
	}
	beforeKey := res.Findings[0].Key
	beforeMsg := res.Findings[0].Message

	// A purely cosmetic edit: one comment block inserted above the import
	// line. Nothing about Consume's shape changes — only its line number
	// (and every other line number below the insertion point).
	after := `package fixture

// This comment is a cosmetic addition that shifts every subsequent line
// number in the file without changing anything semantic below it — the
// exact BUG-119 attack (one comment line added above an import block
// flipped 12 accepted commands.go findings to failing).

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func Consume(g *Guarded) {
	_ = g
}
`
	fixturePath := filepath.Join(root, "internal", "cosmeticfix", "fixture.go")
	if err := os.WriteFile(fixturePath, []byte(after), 0o644); err != nil {
		t.Fatalf("rewriting fixture with cosmetic edit: %v", err)
	}

	res2, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run (after cosmetic edit): %v", err)
	}
	if len(res2.Findings) != 1 {
		t.Fatalf("fixture setup broken after cosmetic edit: expected exactly 1 violation, got %d: %v", len(res2.Findings), res2.Findings)
	}
	afterKey := res2.Findings[0].Key
	afterMsg := res2.Findings[0].Message

	if beforeMsg == afterMsg {
		t.Fatalf("fixture setup broken: expected the cosmetic edit to shift the violation's line number (and therefore its "+
			"message text), but the message was unchanged: %q", beforeMsg)
	}
	if beforeKey != afterKey {
		t.Fatalf("BUG-119 REGRESSION: a purely cosmetic edit above the flagged line changed the finding's ratchet key "+
			"(%q -> %q) even though Consume's own shape never changed. Message before: %q; message after: %q. This is "+
			"the exact live incident: a cosmetic edit anywhere above a flagged line flips every accepted finding "+
			"downstream in the file into a false NEW-violation build failure.",
			beforeKey, afterKey, beforeMsg, afterMsg)
	}

	// The end-to-end proof: an allowlist entry recorded against the
	// finding BEFORE the cosmetic edit must still match AFTER it, via the
	// exact enforceRatchet code path TestRun_LiveTree_ReportsFindings uses.
	accepted := AcceptedFindings{beforeKey: "test fixture: accepted before the cosmetic edit"}
	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", res2.Findings, accepted)
	if fake.errorCalls != 0 {
		t.Fatalf("BUG-119 REGRESSION: an allowlist entry recorded before a cosmetic edit no longer matches the same "+
			"violation after the edit -- enforceRatchet reported %d error(s)", fake.errorCalls)
	}
}

// --- BUG-306 regression: an UNASSIGNED closure's ratchet key must also ---
// --- survive a cosmetic edit above it (line-keying reborn one level down) ---
//
// TestFindingKey_ClosureStableAcrossCosmeticEditAboveIt is
// TestFindingKey_StableAcrossCosmeticEditAboveIt's sibling for the second,
// independent place a location-dependent identity crept back in:
// funcLitName's fallback for a function literal with no direct var/field
// assignment used to be "<func literal at line %d>" — fed straight into
// violationKey via FuncName, so a cosmetic edit (a doc comment) inserted
// ABOVE an unassigned closure shifted its line, which changed FuncName,
// which changed violationKey, which flipped its already-accepted finding
// into a false NEW-violation build failure — the BUG-119 false-positive
// class, reborn via a second code path BUG-119's own fix never touched
// (violationKey excludes rf.Line directly, but funcLitName's synthetic
// name was still built FROM rf.Line one call earlier). BUG-306 (Bro audit
// M2, 2026-08-20) reported this as the direct cause of the repeated manual
// accepted-findings rekey (commands.go re-keyed twice in two days).
//
// The fix (gate.go's funcLitName/scanFuncLits/walkClosuresIn) replaces the
// line-based fallback with scopeName (the enclosing function's own name)
// plus ordinal (this closure's 1-based position among ITS OWN enclosing
// scope's closures, in AST traversal order) — neither component moves
// when a cosmetic edit shifts line numbers, only when a sibling closure is
// actually added/removed ahead of it in the same enclosing scope.
func TestFindingKey_ClosureStableAcrossCosmeticEditAboveIt(t *testing.T) {
	before := `package fixture

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func call(f func(*Guarded)) {}

func Register() {
	call(func(g *Guarded) {
		_ = g
	})
}
`
	root := writeFixturePkg(t, "closurecosmeticfix", map[string]string{"fixture.go": before})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation (the unassigned closure's), got %d: %v", len(res.Findings), res.Findings)
	}
	beforeKey := res.Findings[0].Key
	beforeMsg := res.Findings[0].Message

	// A purely cosmetic edit: one comment block inserted above the import
	// line, exactly like TestFindingKey_StableAcrossCosmeticEditAboveIt's
	// attack — nothing about Register or its closure's shape changes, only
	// line numbers below the insertion point (including the closure's own
	// line, which is what this test is targeting).
	after := `package fixture

// This comment is a cosmetic addition that shifts every subsequent line
// number in the file, including the unassigned closure inside Register
// below, without changing anything semantic about it — the BUG-306
// attack: the same BUG-119 class, one level down inside funcLitName's
// fallback for a closure with no direct var/field assignment.

import "sync"

type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

func call(f func(*Guarded)) {}

func Register() {
	call(func(g *Guarded) {
		_ = g
	})
}
`
	fixturePath := filepath.Join(root, "internal", "closurecosmeticfix", "fixture.go")
	if err := os.WriteFile(fixturePath, []byte(after), 0o644); err != nil {
		t.Fatalf("rewriting fixture with cosmetic edit: %v", err)
	}

	res2, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run (after cosmetic edit): %v", err)
	}
	if len(res2.Findings) != 1 {
		t.Fatalf("fixture setup broken after cosmetic edit: expected exactly 1 violation, got %d: %v", len(res2.Findings), res2.Findings)
	}
	afterKey := res2.Findings[0].Key
	afterMsg := res2.Findings[0].Message

	if beforeMsg == afterMsg {
		t.Fatalf("fixture setup broken: expected the cosmetic edit to shift the closure's line number (and therefore its "+
			"message text), but the message was unchanged: %q", beforeMsg)
	}
	if beforeKey != afterKey {
		t.Fatalf("BUG-306 REGRESSION: a purely cosmetic edit above an unassigned closure changed the finding's ratchet key "+
			"(%q -> %q) even though the closure's own shape and position among its enclosing scope's closures never "+
			"changed. Message before: %q; message after: %q. This is the BUG-119 false-positive class reborn via "+
			"funcLitName's line-keyed fallback.",
			beforeKey, afterKey, beforeMsg, afterMsg)
	}

	// The end-to-end proof: an allowlist entry recorded against the
	// closure's finding BEFORE the cosmetic edit must still match AFTER
	// it, via the exact enforceRatchet code path TestRun_LiveTree_ReportsFindings uses.
	accepted := AcceptedFindings{beforeKey: "test fixture: accepted before the cosmetic edit"}
	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", res2.Findings, accepted)
	if fake.errorCalls != 0 {
		t.Fatalf("BUG-306 REGRESSION: an allowlist entry recorded before a cosmetic edit above an unassigned closure no "+
			"longer matches the same violation after the edit -- enforceRatchet reported %d error(s)", fake.errorCalls)
	}
}

// --- SEC-051 regression: orphaned (fabricated or stale) allowlist entries ---
//
// TestOrphanedEntry_FabricatedFinding_FailsBuild reproduces the Destructive
// agent's own attack verbatim in shape: an allowlist entry for a finding
// that does not (yet) exist anywhere in the live scan — a fabricated,
// speculative entry pre-approved before the violating code was ever
// written — must be caught as a hard failure by enforceNoOrphanedEntries.
// Pre-fix, LoadAcceptedFindings never cross-referenced an entry against
// real scan output at all, and TestRun_LiveTree_ReportsFindings's only
// related check was an advisory, non-failing "STALE allowlist entry"
// t.Logf line — SEC-051 demonstrated live that a fabricated entry sailed
// through that untouched.
func TestOrphanedEntry_FabricatedFinding_FailsBuild(t *testing.T) {
	root := writeFixturePkg(t, "orphanfabfix", map[string]string{"fixture.go": ratchetFixtureSrc})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation, got %d: %v", len(res.Findings), res.Findings)
	}

	accepted := AcceptedFindings{
		res.Findings[0].Key: "the one real, live finding -- accepted correctly",
		// A fabricated, speculative entry: no such finding exists anywhere
		// in this fixture tree. SEC-051's exact attack shape.
		"internal/orphanfabfix|SpeculativeType|function (parameter)|FutureFunc|x": "SEC-051 attack: fabricated entry pre-approved before FutureFunc exists anywhere in the tree",
	}

	fake := &fakeRatchetReporter{}
	enforceNoOrphanedEntries(fake, "accepted-findings.json", res.Findings, accepted)

	if fake.errorCalls == 0 {
		t.Fatal("SEC-051 REGRESSION: enforceNoOrphanedEntries did NOT report any error for a fabricated allowlist entry " +
			"with no matching live violation -- a fabricated/speculative finding can be silently pre-approved, which is " +
			"exactly what SEC-051 demonstrated live")
	}
}

// TestOrphanedEntry_AllEntriesLive_DoesNotFailBuild is the mirror-image
// proof: when every allowlist entry DOES correspond to a live violation,
// enforceNoOrphanedEntries must report zero errors. Without this half, a
// check that simply fails on any non-empty allowlist would also "pass"
// the test above -- this proves the live-match check does real work.
func TestOrphanedEntry_AllEntriesLive_DoesNotFailBuild(t *testing.T) {
	root := writeFixturePkg(t, "orphanlivefix", map[string]string{"fixture.go": ratchetFixtureSrc})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("fixture setup broken: expected exactly 1 violation, got %d: %v", len(res.Findings), res.Findings)
	}

	accepted := AcceptedFindings{
		res.Findings[0].Key: "the one real, live finding -- accepted correctly",
	}

	fake := &fakeRatchetReporter{}
	enforceNoOrphanedEntries(fake, "accepted-findings.json", res.Findings, accepted)

	if fake.errorCalls != 0 {
		t.Fatalf("an allowlist consisting entirely of live-matching entries reported %d error(s) via "+
			"enforceNoOrphanedEntries -- false positive", fake.errorCalls)
	}
}

// --- SEC-051 regression: malformed-SHAPE (fabricated) allowlist keys ----
//
// The two tests above (TestOrphanedEntry_*) exercise enforceNoOrphanedEntries,
// which only fires once a live Run scan result exists to compare an entry
// against. The tests below exercise the EARLIER, load-time check
// (validateFindingKeyShape, wired into LoadAcceptedFindings, accepted.go):
// a key that is not shaped the way violationKey (gate.go) could ever have
// produced it is rejected the moment accepted-findings.json is READ, with
// no live scan involved at all -- a distinct failure mode (MET-F708) from
// the orphan-detection one (which has no error code of its own -- it is a
// t.Errorf inside a test, not a LoadAcceptedFindings-time hard error).

// TestLoadAcceptedFindings_FabricatedKeyShape_RejectsAtLoadTime writes a
// fixture accepted-findings.json whose one entry has a plausible-LOOKING
// but wrong-shaped key -- SEC-051's own reproduction shape, five
// pipe-delimited fields instead of violationKey's six (missing the
// MatchedExprPrinted component) -- and confirms LoadAcceptedFindings
// rejects it with MET-F708, distinct from MET-F706/MET-F707's
// empty-field/duplicate failure modes.
func TestLoadAcceptedFindings_FabricatedKeyShape_RejectsAtLoadTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accepted-findings.json")
	const fixtureJSON = `[
  {
    "finding": "internal/orphanfabfix|SpeculativeType|function (parameter)|FutureFunc|x",
    "reason": "SEC-051 attack: fabricated entry pre-approved before FutureFunc exists anywhere in the tree"
  }
]`
	if err := os.WriteFile(path, []byte(fixtureJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadAcceptedFindings(path)
	e := assertRegistryError(t, err, "MET-F708")
	if got, _ := e.Ctx["finding"].(string); got == "" {
		t.Error("expected ctx[\"finding\"] to name the offending malformed key")
	}
	if got, _ := e.Ctx["reason"].(string); !strings.Contains(got, "6") {
		t.Errorf("expected the shape-validation reason to explain the field-count mismatch, got %q", got)
	}
	t.Logf("SEC-051 load-time shape check fired as designed: %s", e.Display())
}

// TestLoadAcceptedFindings_WellFormedRealEntries_LoadCleanly is the
// mirror-image proof: LoadAcceptedFindings against the project's REAL,
// checked-in accepted-findings.json (the actual SEC-049-triaged entries,
// currently 142 of them) must load with no error at all -- proving
// validateFindingKeyShape does not false-positive on any genuine,
// scanner-produced key, only on fabricated ones.
func TestLoadAcceptedFindings_WellFormedRealEntries_LoadCleanly(t *testing.T) {
	root, err := resolveRepoRoot()
	if err != nil {
		t.Fatalf("resolveRepoRoot: %v", err)
	}
	acceptedPath := filepath.Join(root, filepath.FromSlash(acceptedFindingsFile))
	accepted, err := LoadAcceptedFindings(acceptedPath)
	if err != nil {
		t.Fatalf("SEC-051 REGRESSION: LoadAcceptedFindings rejected the real, checked-in %s (every entry should already be "+
			"well-shaped -- SEC-049's triage): %v", acceptedFindingsFile, err)
	}
	if len(accepted) == 0 {
		t.Fatal("fixture setup broken: the real accepted-findings.json loaded with zero entries -- this test proves nothing " +
			"without a substantial, genuinely populated registry to validate against")
	}
	t.Logf("SEC-051: all %d real accepted-findings.json entries pass validateFindingKeyShape", len(accepted))
}

// TestValidateFindingKeyShape_TableDriven unit-tests validateFindingKeyShape
// directly against a small table of malformed shapes (each isolating ONE
// way a fabricated/hand-typed key can go wrong) plus one well-formed
// control, proving the check catches each failure mode independently
// rather than only the exact SEC-051 reproduction string above.
func TestValidateFindingKeyShape_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "well-formed control (6 fields, real kind, .go file)",
			key:     "internal/foundation/errs/log.go|*Logger|receiver method|SetSink|l|*Logger",
			wantErr: false,
		},
		{
			name:    "well-formed free function (empty receiver-expr field is legal)",
			key:     "internal/foundation/errs/log.go||function (parameter)|SetSink|l|*Logger",
			wantErr: false,
		},
		{
			name:    "SEC-051 reproduction: only 5 fields, missing matchedExpr",
			key:     "internal/orphanfabfix|SpeculativeType|function (parameter)|FutureFunc|x",
			wantErr: true,
		},
		{
			name:    "too many fields (7)",
			key:     "a.go|b|receiver method|c|d|e|f",
			wantErr: true,
		},
		{
			name:    "unrecognised kind label",
			key:     "internal/foo.go|*T|totally made up kind|F|v|*T",
			wantErr: true,
		},
		{
			name:    "file component does not end in .go",
			key:     "internal/foo.txt|*T|receiver method|F|v|*T",
			wantErr: true,
		},
		{
			name:    "absolute file path",
			key:     "/internal/foo.go|*T|receiver method|F|v|*T",
			wantErr: true,
		},
		{
			name:    "backslashed (Windows-style) file path",
			key:     `internal\foo.go|*T|receiver method|F|v|*T`,
			wantErr: true,
		},
		{
			name:    "empty funcName",
			key:     "internal/foo.go|*T|receiver method||v|*T",
			wantErr: true,
		},
		{
			name:    "empty valueName",
			key:     "internal/foo.go|*T|receiver method|F||*T",
			wantErr: true,
		},
		{
			name:    "empty matchedExpr",
			key:     "internal/foo.go|*T|receiver method|F|v|",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateFindingKeyShape(c.key)
			if c.wantErr && err == nil {
				t.Errorf("validateFindingKeyShape(%q) = nil, want a shape error", c.key)
			}
			if !c.wantErr && err != nil {
				t.Errorf("validateFindingKeyShape(%q) = %v, want nil (well-formed key)", c.key, err)
			}
		})
	}
}

// --- SEC-048: registry-sourced errors, not bare fmt.Errorf --------------
//
// The three tests below are the direct regression coverage for SEC-048's
// AC-16: each triggers the REAL error condition at one of gate.go's three
// former bare-fmt.Errorf sites (a go/parser syntax error, a WalkDir
// failure on a missing directory, and resolveRepoRoot's no-go.mod-found
// case) and asserts the returned error is a genuine *errs.E carrying the
// registered MET-F70x code and a non-empty, non-placeholder correlation
// ID — not merely "an error was returned". Run against the pre-fix bare
// fmt.Errorf code, each of these three tests fails at the `err.(*errs.E)`
// type assertion (a bare fmt.Errorf-wrapped error is not an *errs.E), so
// this is real detection of the conversion, not a test that would pass
// either way.

// assertRegistryError is the shared assertion for all three SEC-048
// tests below: err must be a non-nil *errs.E with the given code and a
// real (non-empty, non-placeholder) correlation ID.
func assertRegistryError(t *testing.T, err error, wantCode string) *errs.E {
	t.Helper()
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected a *errs.E (registry-sourced error, SEC-048), got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("expected code %s, got %s", wantCode, e.Code)
	}
	if e.CorrelationID == "" {
		t.Error("expected a non-empty correlation ID")
	}
	if e.CorrelationID == "MISSING-CORRELATION-ID" {
		t.Error("expected a real correlation ID minted via errs.NewCorrelationID(), got the empty-string fallback placeholder — this is the exact 'lazy implementation' SEC-048's AC-16 forbids")
	}
	return e
}

// TestLoadPackages_ParseErrorIsRegistryError triggers loadPackages'
// go/parser failure site (formerly line 619's bare
// fmt.Errorf("parsing %s: %w", ...)) with a deliberately malformed .go
// fixture file and asserts MET-F700 with a preserved wrapped cause.
func TestLoadPackages_ParseErrorIsRegistryError(t *testing.T) {
	root := writeFixturePkg(t, "parsefail", map[string]string{
		"bad.go": "package fixture\n\nfunc broken(x int {\n", // deliberately malformed
	})

	_, err := loadPackages(root, "internal")
	e := assertRegistryError(t, err, "MET-F700")
	if e.Wrapped == nil {
		t.Error("expected the underlying go/parser error to be preserved as Wrapped (GR#1 — no information lost in the fmt.Errorf->errs.Wrap conversion)")
	}
	if got, _ := e.Ctx["path"].(string); !strings.Contains(got, "bad.go") {
		t.Errorf("expected ctx[\"path\"] to name the offending file, got %q", got)
	}
}

// TestLoadPackages_MissingDirIsRegistryError triggers loadPackages'
// post-WalkDir failure site (formerly line 631's bare
// fmt.Errorf("scanning %s: %w", ...)) by pointing it at a directory that
// does not exist, and asserts MET-F701 with a preserved wrapped cause.
func TestLoadPackages_MissingDirIsRegistryError(t *testing.T) {
	root := t.TempDir() // deliberately has no "internal" subdirectory

	_, err := loadPackages(root, "internal")
	e := assertRegistryError(t, err, "MET-F701")
	if e.Wrapped == nil {
		t.Error("expected the underlying filepath.WalkDir error to be preserved as Wrapped (GR#1)")
	}
	if got, _ := e.Ctx["root"].(string); !strings.Contains(got, "internal") {
		t.Errorf("expected ctx[\"root\"] to name the missing directory, got %q", got)
	}
}

// TestResolveRepoRoot_NoGoModIsRegistryError triggers resolveRepoRoot's
// self-contained failure site (formerly line 737's bare
// fmt.Errorf("astgate: no go.mod found walking up from %s", dir)) by
// running it from a temp directory tree with no go.mod anywhere above it,
// and asserts MET-F702. This site has no wrapped cause in the original
// (a self-contained astgate condition, not a wrapped stdlib error), so
// only the code/correlation-ID/ctx are checked, not Wrapped.
func TestResolveRepoRoot_NoGoModIsRegistryError(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	// t.TempDir()'s own removal cleanup is registered here, BEFORE our
	// chdir-back cleanup below — t.Cleanup runs LIFO, so registering
	// ours second makes it run FIRST, restoring cwd out of tmp before
	// TempDir tries to RemoveAll it. Windows refuses to remove a
	// directory that is still the process's current working directory,
	// so the reverse registration order would fail cleanup non-
	// deterministically.
	tmp := t.TempDir()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir(%s): %v", tmp, err)
	}
	t.Cleanup(func() {
		if cerr := os.Chdir(origWD); cerr != nil {
			t.Fatalf("restoring cwd to %s: %v", origWD, cerr)
		}
	})

	_, err = resolveRepoRoot()
	e := assertRegistryError(t, err, "MET-F702")
	if _, ok := e.Ctx["dir"]; !ok {
		t.Error("expected ctx[\"dir\"] to record the starting directory the upward search began from")
	}
}

// --- BUG-119 round 2 regression: KindParameterFunc receiver-type collision ---

// TestViolationKey_DistinctReceiversSameParamName_KeysDoNotCollide
// reproduces the Destructive reattack's ("Vantage") EXACT collision shape
// that rejected round 1's fix: two UNRELATED types (TypeA, TypeB), each
// declaring their own method named Attach, each taking the SAME candidate
// type Guarded by parameter under the SAME parameter name g, in the SAME
// file. Round 1's violationKey was (File, TypeName, Kind, FuncName,
// ValueName) -- for both of these ReachableFunc entries that tuple is
// identically ("fixture.go", "Guarded", KindParameterFunc, "Attach", "g"),
// because TypeName there names the MATCHED PARAMETER's type (Guarded),
// not either method's own receiver (TypeA / TypeB). Go allows this: methods
// are namespaced by receiver, not globally unique by name, so TypeA.Attach
// and TypeB.Attach can coexist even though free function "Attach" declared
// twice could not.
//
// The real-world danger this proves closed: an allowlist entry a human
// reviewed and accepted for TypeA.Attach's hazard must NOT also silently
// suppress TypeB.Attach's genuinely different, unreviewed hazard.
//
// This test is written to FAIL against round 1's code (violationKey
// without ReceiverTypeName) and PASS after this round's fix -- verified by
// temporarily reverting the ReceiverTypeName addition and re-running
// (see BUG-119 round 2 report).
func TestViolationKey_DistinctReceiversSameParamName_KeysDoNotCollide(t *testing.T) {
	src := `package fixture

import "sync"

// Guarded matches AC-1: a sync.Mutex value field plus an aliasable field.
type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// TypeA and TypeB are two UNRELATED types -- neither is itself a
// candidate type (no mutex field) -- that each declare their own method
// named Attach, taking *Guarded by parameter under the identical
// parameter name g. This is Vantage's exact collision shape: same
// (TypeName=Guarded, Kind=KindParameterFunc, FuncName=Attach,
// ValueName=g) tuple for two genuinely different, both-unguarded hazards.
type TypeA struct{}

func (a *TypeA) Attach(g *Guarded) {
	_ = g
}

type TypeB struct{}

func (b *TypeB) Attach(g *Guarded) {
	_ = g
}
`
	root := writeFixturePkg(t, "collisionfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Locate the two Attach findings (TypeA.Attach and TypeB.Attach).
	// res.Findings is built (see Run, gate.go) in the same order as
	// res.Reachable's UNGUARDED subsequence, one Finding per unguarded
	// ReachableFunc -- so walking both in lockstep pairs each unguarded rf
	// with its Finding directly, no separate lookup needed.
	var attachFindings []Finding
	findingIdx := 0
	for _, rf := range res.Reachable {
		if rf.Guarded {
			continue
		}
		f := res.Findings[findingIdx]
		findingIdx++
		if rf.Kind == KindParameterFunc && rf.FuncName == "Attach" && rf.ValueName == "g" {
			attachFindings = append(attachFindings, f)
		}
	}
	if len(attachFindings) != 2 {
		t.Fatalf("fixture setup broken: expected exactly 2 unguarded Attach(g *Guarded) findings (TypeA.Attach, TypeB.Attach), got %d: %v",
			len(attachFindings), res.Findings)
	}

	keyA, keyB := attachFindings[0].Key, attachFindings[1].Key
	if keyA == keyB {
		t.Fatalf("BUG-119 ROUND 2 REGRESSION: TypeA.Attach(g *Guarded) and TypeB.Attach(g *Guarded) -- two DIFFERENT, both-unguarded "+
			"hazards on unrelated receiver types -- produced the IDENTICAL violationKey %q. An allowlist entry reviewed and accepted "+
			"for one receiver's hazard would silently also suppress the other, unreviewed one -- Vantage's exact reattack finding.",
			keyA)
	}

	// End-to-end proof via the real enforcement path: accepting ONLY
	// TypeA.Attach's key must NOT tolerate TypeB.Attach's finding -- it
	// must still be reported as a hard, unaccepted violation.
	accepted := AcceptedFindings{keyA: "test fixture: TypeA.Attach reviewed and accepted -- TypeB.Attach must remain unaccepted"}
	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", []Finding{attachFindings[0], attachFindings[1]}, accepted)
	// enforceRatchet emits one Errorf per unaccepted finding PLUS one
	// summary Errorf -- with exactly one unaccepted finding (TypeB.Attach)
	// that is 2 calls. If TypeA.Attach's acceptance silently covered
	// TypeB.Attach too, errorCalls would be 0.
	if fake.errorCalls != 2 {
		t.Fatalf("BUG-119 ROUND 2 REGRESSION: expected exactly 2 error calls (1 per-violation + 1 summary) for TypeB.Attach, "+
			"not covered by the TypeA.Attach-only allowlist entry, got %d -- if this is 0, TypeA.Attach's acceptance is "+
			"silently covering TypeB.Attach too", fake.errorCalls)
	}
}

// TestViolationKey_ReceiverMethod_StillUnique proves the opposite-direction
// check Vantage also verified: adding ReceiverTypeName to the key must NOT
// break KindReceiverMethod's existing collision-freedom. Two methods with
// the SAME name (Attach) but on DIFFERENT receiver types (both candidate
// types this time, reached via the receiver-method path, not the
// parameter path) must produce a genuine rename/move key change and never
// collide with each other, exactly as before this fix.
func TestViolationKey_ReceiverMethod_StillUnique(t *testing.T) {
	src := `package fixture

import "sync"

type GuardedA struct {
	mu    sync.Mutex
	items []int
}

func (g *GuardedA) checkNotCopied() bool { return true }

func (g *GuardedA) Attach() {}

type GuardedB struct {
	mu    sync.Mutex
	items []int
}

func (g *GuardedB) checkNotCopied() bool { return true }

func (g *GuardedB) Attach() {}
`
	root := writeFixturePkg(t, "recvuniquefix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("fixture setup broken: expected exactly 2 findings (GuardedA.Attach, GuardedB.Attach), got %d: %v", len(res.Findings), res.Findings)
	}
	if res.Findings[0].Key == res.Findings[1].Key {
		t.Fatalf("REGRESSION: GuardedA.Attach and GuardedB.Attach (distinct receiver-method findings) collided on key %q after "+
			"adding ReceiverTypeName to violationKey", res.Findings[0].Key)
	}
}

// TestViolationKey_GenericReceiverVsFreeFunction_KeysDoNotCollide is
// BUG-119 round 5's regression test, reproducing Riftline's EXACT round-4
// Destructive reattack scenario: a GENERIC receiver method --
// func (s *Set[T]) Attach(g *Guarded) -- and a genuinely unrelated free
// function -- func Attach(g *Guarded) -- both unguarded, same package,
// same parameter name. Before this round's fix, baseTypeName had no case
// for *ast.IndexExpr (Set[T]'s type-parameter list), so the generic
// receiver's own type name was unrecognised; findReachableFuncs then
// silently treated that "unrecognised" the same as "no receiver at all"
// and fell back to noReceiverSentinel for BOTH ReachableFuncs, producing
// an IDENTICAL violationKey for two genuinely different, both-unguarded
// hazards -- exactly the same failure MODE as round 2's TypeA/TypeB
// collision (TestViolationKey_DistinctReceiversSameParamName_KeysDoNotCollide),
// reached via a new mechanism.
func TestViolationKey_GenericReceiverVsFreeFunction_KeysDoNotCollide(t *testing.T) {
	src := `package fixture

import "sync"

// Guarded matches AC-1: a sync.Mutex value field plus an aliasable field.
type Guarded struct {
	mu    sync.Mutex
	items []int
}

func (g *Guarded) checkNotCopied() bool { return true }

// Set is a generic container. It is NOT itself a candidate type (no
// mutex field) -- it exists purely to give Attach a generic receiver
// shape, Riftline's exact round-4 finding.
type Set[T any] struct {
	values []T
}

// Attach (generic receiver) takes the candidate type Guarded by
// parameter, unguarded.
func (s *Set[T]) Attach(g *Guarded) {
	_ = g
}

// Attach (free function) is a GENUINELY UNRELATED hazard with the same
// name and same parameter shape as Set[T].Attach above -- pre-fix, both
// resolved ReceiverTypeName == noReceiverSentinel and collided.
func Attach(g *Guarded) {
	_ = g
}
`
	root := writeFixturePkg(t, "genericcollisionfix", map[string]string{"fixture.go": src})
	res, err := Run(root, "internal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Locate the two Attach(g *Guarded) findings: the generic receiver
	// method (KindParameterFunc, since Guarded is matched via parameter,
	// not via Set[T]'s own receiver -- Set is not a candidate type) and
	// the free function.
	var attachFindings []Finding
	var attachRFs []*ReachableFunc
	findingIdx := 0
	for _, rf := range res.Reachable {
		if rf.Guarded {
			continue
		}
		f := res.Findings[findingIdx]
		findingIdx++
		if rf.Kind == KindParameterFunc && rf.FuncName == "Attach" && rf.ValueName == "g" {
			attachFindings = append(attachFindings, f)
			attachRFs = append(attachRFs, rf)
		}
	}
	if len(attachFindings) != 2 {
		t.Fatalf("fixture setup broken: expected exactly 2 unguarded Attach(g *Guarded) findings (Set[T].Attach, free Attach), got %d: %v",
			len(attachFindings), res.Findings)
	}

	// Sanity: confirm the fixture actually reproduces Riftline's shape --
	// one ReachableFunc has a real (non-sentinel) receiver, the other has
	// no receiver at all. If baseTypeName regresses back to not
	// recognising Set[T], this ReceiverTypeName would silently become
	// noReceiverSentinel again and this assertion would catch it directly,
	// independent of the key-collision check below.
	var sawGenericReceiver, sawFreeFunction bool
	for _, rf := range attachRFs {
		switch rf.ReceiverTypeName {
		case "Set":
			sawGenericReceiver = true
		case noReceiverSentinel:
			sawFreeFunction = true
		}
	}
	if !sawGenericReceiver {
		t.Fatalf("BUG-119 ROUND 5 REGRESSION: Set[T].Attach's ReceiverTypeName was not resolved to \"Set\" -- baseTypeName is not "+
			"recognising the generic receiver shape *ast.IndexExpr; got ReceiverTypeNames %v", receiverTypeNames(attachRFs))
	}
	if !sawFreeFunction {
		t.Fatalf("fixture setup broken: the free Attach(g *Guarded) function's ReceiverTypeName was not noReceiverSentinel; "+
			"got ReceiverTypeNames %v", receiverTypeNames(attachRFs))
	}

	keyGeneric, keyFree := attachFindings[0].Key, attachFindings[1].Key
	if keyGeneric == keyFree {
		t.Fatalf("BUG-119 ROUND 5 REGRESSION (Riftline): Set[T].Attach(g *Guarded) (generic receiver method) and the free function "+
			"Attach(g *Guarded) -- two DIFFERENT, both-unguarded hazards -- produced the IDENTICAL violationKey %q. An allowlist "+
			"entry reviewed and accepted for one would silently also suppress the other, unreviewed one.", keyGeneric)
	}

	// End-to-end proof via the real enforcement path, mirroring round 2's
	// test: accepting ONLY the generic receiver's key must NOT tolerate
	// the free function's finding -- it must still be reported as a hard,
	// unaccepted violation.
	accepted := AcceptedFindings{keyGeneric: "test fixture: Set[T].Attach reviewed and accepted -- the free Attach function must remain unaccepted"}
	fake := &fakeRatchetReporter{}
	enforceRatchet(fake, "accepted-findings.json", []Finding{attachFindings[0], attachFindings[1]}, accepted)
	if fake.errorCalls != 2 {
		t.Fatalf("BUG-119 ROUND 5 REGRESSION: expected exactly 2 error calls (1 per-violation + 1 summary) for the free Attach "+
			"function, not covered by the generic-receiver-only allowlist entry, got %d -- if this is 0, the generic receiver's "+
			"acceptance is silently covering the free function too", fake.errorCalls)
	}
}

// receiverTypeNames is a small diagnostic helper for test failure
// messages above -- not part of the gate's production logic.
func receiverTypeNames(rfs []*ReachableFunc) []string {
	out := make([]string, len(rfs))
	for i, rf := range rfs {
		out[i] = rf.ReceiverTypeName
	}
	return out
}
