package helper_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/helper"
	"github.com/aaronukgarcia/Metropolis/internal/engine/helper/helperfixture"
)

const testCorrelationID = "helper-test"

// --- AC-1: the Registrant contract shape ---

// TestRegistrant_HasExactlyThreeMembers is AC-1's structural check via
// reflection: go doc ./internal/engine/helper Registrant must show
// exactly three methods — this test fails the moment a fourth member
// (e.g. a RenderHint/IconID/OnHover field AC-4 warns against) is added.
func TestRegistrant_HasExactlyThreeMembers(t *testing.T) {
	typ := reflect.TypeOf((*helper.Registrant)(nil)).Elem()
	if got, want := typ.NumMethod(), 3; got != want {
		names := make([]string, typ.NumMethod())
		for i := range names {
			names[i] = typ.Method(i).Name
		}
		t.Fatalf("Registrant has %d methods (%v), want exactly %d (AC-1)", got, names, want)
	}
	for _, want := range []string{"TaxonomyID", "Preconditions", "ProjectConsequence"} {
		if _, ok := typ.MethodByName(want); !ok {
			t.Errorf("Registrant is missing expected method %q (AC-1)", want)
		}
	}
}

// TestFixtureAction_SatisfiesRegistrant is the AC-1 fixture-implements-
// interface check, exercised via a real (non-nil) instance rather than
// only the compile-time `var _ helper.Registrant = (*FixtureAction)(nil)`
// assertion in helperfixture itself.
func TestFixtureAction_SatisfiesRegistrant(t *testing.T) {
	var reg helper.Registrant = helperfixture.NewFixtureAction("fixture.buy-thing", testCorrelationID)
	if reg.TaxonomyID() != "fixture.buy-thing" {
		t.Fatalf("TaxonomyID() = %q, want %q", reg.TaxonomyID(), "fixture.buy-thing")
	}
	preconds := reg.Preconditions()
	if len(preconds) == 0 {
		t.Fatal("expected at least one precondition from the fixture registrant")
	}
	for _, p := range preconds {
		if p.ID() == "" {
			t.Error("precondition ID() must be non-empty")
		}
		if p.Description() == "" {
			t.Error("precondition Description() must be non-empty")
		}
	}
}

// --- AC-1(a) UI-shape false-pass guard: registering with the CONTRACT
// package alone (no UI import anywhere) must work standalone, and this
// package's own source must carry zero internal/ui references (AC-4). ---

// TestHelperPackage_NoUIImports mechanically checks AC-4(b): neither
// internal/engine/helper nor internal/engine/helper/helperfixture
// imports anything under internal/ui, and neither reacts to a
// selection/hover/focus event. Mirrors the check command
// (`grep -rn "internal/ui" internal/engine/helper/*.go`) as a permanent,
// CI-enforced regression test rather than a one-time manual grep.
func TestHelperPackage_NoUIImports(t *testing.T) {
	repoRoot := findRepoRoot(t)
	dirs := []string{
		filepath.Join(repoRoot, "internal", "engine", "helper"),
		filepath.Join(repoRoot, "internal", "engine", "helper", "helperfixture"),
	}
	// Matches AC-4b's exact check command literally
	// (`grep -rn "internal/ui" internal/engine/helper/*.go`, non-test
	// files only) — this is a source-import check, not a prose-content
	// check, so it looks only for the import-path substring.
	forbidden := []string{"internal/ui"}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			content := string(raw)
			for _, needle := range forbidden {
				if strings.Contains(content, needle) {
					t.Errorf("%s contains forbidden substring %q — engine.helper must have zero UI-facing code (AC-4b)", path, needle)
				}
			}
		}
	}
}

// findRepoRoot walks up from the working directory looking for go.mod —
// robust to `go test` being invoked from either the module root or this
// package's own directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found walking up from test working directory)")
		}
		dir = parent
	}
}

// --- AC-2: preconditions are independently evaluable, never a panic ---

func TestPrecondition_MissingFieldReturnsRegistryError_NeverSilentFalse(t *testing.T) {
	p := helperfixture.NewTreasuryPrecondition(testCorrelationID)
	state := helper.NewGameStateView(map[string]any{}) // treasuryMicropounds absent

	passed, err := p.Evaluate(state)
	if err == nil {
		t.Fatal("expected a registry-sourced error for a missing required field, got nil")
	}
	if passed {
		t.Error("Evaluate must not report passed=true alongside a non-nil error")
	}
	if !strings.Contains(err.Error(), helper.ErrPreconditionEvalFailed) {
		t.Errorf("error %v does not carry ErrPreconditionEvalFailed (%s)", err, helper.ErrPreconditionEvalFailed)
	}
}

func TestPrecondition_ZeroValueGameStateView_NeverPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Evaluate panicked on a zero-value GameStateView: %v", r)
		}
	}()
	p := helperfixture.NewTreasuryPrecondition(testCorrelationID)
	if _, err := p.Evaluate(helper.GameStateView{}); err == nil {
		t.Fatal("expected a registry-sourced error for a zero-value GameStateView, got nil")
	}
}

func TestPrecondition_SufficientTreasury_Passes(t *testing.T) {
	p := helperfixture.NewTreasuryPrecondition(testCorrelationID)
	state := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 1,
	})
	passed, err := p.Evaluate(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("expected the treasury precondition to pass with a balance above the fixture cost")
	}
}

// --- AC-3: registration sealed after boot ---

func TestRegistry_RegisterAfterRecommendFails(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	first := helperfixture.NewFixtureAction("fixture.first", testCorrelationID)
	if err := reg.Register(first); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Recommend implicitly seals the registry (AC-3).
	_ = reg.Recommend(helper.GameStateView{})

	second := helperfixture.NewFixtureAction("fixture.second", testCorrelationID)
	err := reg.Register(second)
	if err == nil {
		t.Fatal("expected Register to fail after the registry has been read from (sealed), got nil error")
	}
	if !strings.Contains(err.Error(), helper.ErrRegistrationSealed) {
		t.Errorf("error %v does not carry ErrRegistrationSealed (%s)", err, helper.ErrRegistrationSealed)
	}

	// The target set must be unchanged — "fixture.second" must never be
	// answerable by Preview.
	if _, err := reg.Preview(helper.GameStateView{}, "fixture.second", nil); err == nil {
		t.Fatal("expected Preview(\"fixture.second\") to fail — late registration must not have taken effect")
	}
}

func TestRegistry_RegisterAfterExplicitSealFails(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	reg.Seal()

	err := reg.Register(helperfixture.NewFixtureAction("fixture.late", testCorrelationID))
	if err == nil {
		t.Fatal("expected Register to fail after an explicit Seal, got nil error")
	}
	if !strings.Contains(err.Error(), helper.ErrRegistrationSealed) {
		t.Errorf("error %v does not carry ErrRegistrationSealed (%s)", err, helper.ErrRegistrationSealed)
	}
}

func TestRegistry_RegisterEmptyTaxonomyIDFails(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	err := reg.Register(helperfixture.NewFixtureAction("", testCorrelationID))
	if err == nil {
		t.Fatal("expected Register to reject an empty ActionTaxonomyID, got nil error")
	}
	if !strings.Contains(err.Error(), helper.ErrEmptyTaxonomyID) {
		t.Errorf("error %v does not carry ErrEmptyTaxonomyID (%s)", err, helper.ErrEmptyTaxonomyID)
	}
}

func TestRegistry_RegisterDuplicateTaxonomyIDFails(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewFixtureAction("fixture.dup", testCorrelationID)); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	err := reg.Register(helperfixture.NewFixtureAction("fixture.dup", testCorrelationID))
	if err == nil {
		t.Fatal("expected Register to reject a duplicate ActionTaxonomyID, got nil error")
	}
	if !strings.Contains(err.Error(), helper.ErrDuplicateTaxonomyID) {
		t.Errorf("error %v does not carry ErrDuplicateTaxonomyID (%s)", err, helper.ErrDuplicateTaxonomyID)
	}
}

// --- AC-6: determinism ---

func TestPreview_DeterministicAcrossRepeatedCalls(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewFixtureAction("fixture.det", testCorrelationID)); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	state := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 100,
	})

	first, err := reg.Preview(state, "fixture.det", map[string]any{"qty": int64(3)})
	if err != nil {
		t.Fatalf("first Preview failed: %v", err)
	}
	second, err := reg.Preview(state, "fixture.det", map[string]any{"qty": int64(3)})
	if err != nil {
		t.Fatalf("second Preview failed: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Preview is not deterministic: first=%+v second=%+v", first, second)
	}
}

// --- AC-7: Recommend returns an unordered candidate set ---

func TestRecommend_ReturnsAllNonDominatedCandidates_NoRankField(t *testing.T) {
	// Structural guard: Recommendation must not carry a rank/score/best
	// field at all (AC-7's false-pass warning).
	typ := reflect.TypeOf(helper.Recommendation{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "rank") || strings.Contains(name, "score") || strings.Contains(name, "best") {
			t.Fatalf("Recommendation has a field %q — AC-7 forbids any ordering-implies-verdict signal", typ.Field(i).Name)
		}
	}

	reg := helper.NewRegistry(testCorrelationID)
	ids := []helper.ActionTaxonomyID{"fixture.alpha", "fixture.beta", "fixture.gamma"}
	for _, id := range ids {
		if err := reg.Register(helperfixture.NewFixtureAction(id, testCorrelationID)); err != nil {
			t.Fatalf("Register(%s) failed: %v", id, err)
		}
	}

	state := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 1,
	})
	got := reg.Recommend(state)

	if len(got) != len(ids) {
		t.Fatalf("Recommend returned %d candidates, want %d: %+v", len(got), len(ids), got)
	}
	seen := map[helper.ActionTaxonomyID]bool{}
	for _, r := range got {
		seen[r.ActionID] = true
		if r.Description == "" {
			t.Errorf("candidate %s has empty Description", r.ActionID)
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("Recommend result is missing non-dominated candidate %s", id)
		}
	}
}

func TestRecommend_ExcludesActionsWithFailingPreconditions(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewUnreachableFixtureAction("fixture.unreachable")); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := reg.Register(helperfixture.NewNoPreconditionFixtureAction("fixture.always-available")); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got := reg.Recommend(helper.GameStateView{})
	for _, r := range got {
		if r.ActionID == "fixture.unreachable" {
			t.Error("Recommend must exclude an action whose precondition currently evaluates false")
		}
	}
	found := false
	for _, r := range got {
		if r.ActionID == "fixture.always-available" {
			found = true
		}
	}
	if !found {
		t.Error("Recommend must include an action with zero preconditions")
	}
}

// --- AC-8: Preview ---

func TestPreview_UnknownActionReturnsErrUnknownAction(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	_, err := reg.Preview(helper.GameStateView{}, "fixture.does-not-exist", nil)
	if err == nil {
		t.Fatal("expected an error for an unregistered action id, got nil")
	}
	if !strings.Contains(err.Error(), helper.ErrUnknownAction) {
		t.Errorf("error %v does not carry ErrUnknownAction (%s)", err, helper.ErrUnknownAction)
	}
}

func TestPreview_FailingPrecondition_CarriesBothProjectionAndFailure(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewUnreachableFixtureAction("fixture.unreachable")); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	result, err := reg.Preview(helper.GameStateView{}, "fixture.unreachable", nil)
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if result.Projection.ActionID != "fixture.unreachable" {
		t.Errorf("Preview result missing its projection: %+v", result)
	}
	if len(result.Preconditions) == 0 {
		t.Fatal("Preview result missing precondition information")
	}
	if result.Preconditions[0].Passed {
		t.Error("expected the always-fail precondition to be reported as failing")
	}
}

func TestPreview_NeverMutatesState(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewFixtureAction("fixture.readonly", testCorrelationID)); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	before := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 1,
	})
	afterCopy := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 1,
	})

	if _, err := reg.Preview(before, "fixture.readonly", map[string]any{"x": int64(1)}); err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if !reflect.DeepEqual(before, afterCopy) {
		t.Errorf("Preview mutated its GameStateView argument: before=%+v after=%+v", afterCopy, before)
	}
}

// --- AC-13: concurrency safety once sealed ---

func TestRegistry_ConcurrentRecommendAndPreview_NoRace(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	for _, id := range []helper.ActionTaxonomyID{"fixture.c1", "fixture.c2", "fixture.c3"} {
		if err := reg.Register(helperfixture.NewFixtureAction(id, testCorrelationID)); err != nil {
			t.Fatalf("Register(%s) failed: %v", id, err)
		}
	}
	state := helper.NewGameStateView(map[string]any{
		"treasuryMicropounds": helperfixture.FixtureExecuteCost() + 1,
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = reg.Recommend(state)
		}()
		go func(id helper.ActionTaxonomyID) {
			defer wg.Done()
			_, _ = reg.Preview(state, id, nil)
		}(helper.ActionTaxonomyID("fixture.c1"))
	}
	wg.Wait()
}

// --- AC-14: no panic reachable from any exported surface ---

func TestNoPanic_ZeroValueGameStateView_RecommendAndPreview(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewNoPreconditionFixtureAction("fixture.zero-safe")); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	_ = reg.Recommend(helper.GameStateView{})
	if _, err := reg.Preview(helper.GameStateView{}, "fixture.zero-safe", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := reg.Preview(helper.GameStateView{}, "fixture.zero-safe", map[string]any{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNoPanic_NilPreconditionSlice(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on a Registrant with a nil Preconditions() slice: %v", r)
		}
	}()
	reg := helper.NewRegistry(testCorrelationID)
	if err := reg.Register(helperfixture.NewNoPreconditionFixtureAction("fixture.nil-preconds")); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	got := reg.Recommend(helper.GameStateView{})
	found := false
	for _, r := range got {
		if r.ActionID == "fixture.nil-preconds" {
			found = true
		}
	}
	if !found {
		t.Error("a Registrant with a nil Preconditions() slice should be treated as having no preconditions (always eligible), not excluded")
	}
}

func TestNoPanic_NilRegistrant(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register(nil) panicked: %v", r)
		}
	}()
	reg := helper.NewRegistry(testCorrelationID)
	// A nil Registrant is a malformed caller, but Register must still
	// fail cleanly (registry-sourced error) rather than dereference/call
	// a method on a nil interface value (AC-14).
	err := reg.Register(nil)
	if err == nil {
		t.Fatal("expected Register(nil) to return a registry-sourced error, got nil")
	}
}

// --- AC-12: GR#7 registry-sourced errors only ---

func TestErrors_AllConstructedFromRegistry(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	_, err := reg.Preview(helper.GameStateView{}, "fixture.missing", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(err.Error(), "[MET-E") {
		t.Errorf("error %q is not a registry-sourced *errs.E (GR#7)", err.Error())
	}
}
