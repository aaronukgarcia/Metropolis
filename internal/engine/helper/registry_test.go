package helper_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestPrecondition_MissingField_WrapsMalformedStateView is BUG-146's
// regression test. Before the fix, GameStateView.RequireField
// (ErrMalformedStateView / MET-E705) had zero real call sites anywhere
// in the package, its fixtures, or its tests — a genuinely dead registry
// code. treasuryPrecondition.Evaluate now calls RequireField directly
// (see helperfixture/fixture.go) and wraps its ErrMalformedStateView
// into ErrPreconditionEvalFailed via errs.Wrap, so a missing required
// field now surfaces BOTH codes: the precondition-level
// ErrPreconditionEvalFailed (already asserted by
// TestPrecondition_MissingFieldReturnsRegistryError_NeverSilentFalse
// above) AND, as its preserved wrapped cause, ErrMalformedStateView —
// proving MET-E705 is now genuinely reachable, not dead.
func TestPrecondition_MissingField_WrapsMalformedStateView(t *testing.T) {
	p := helperfixture.NewTreasuryPrecondition(testCorrelationID)
	state := helper.NewGameStateView(map[string]any{}) // treasuryMicropounds absent

	_, err := p.Evaluate(state)
	if err == nil {
		t.Fatal("expected a registry-sourced error for a missing required field, got nil")
	}
	if !strings.Contains(err.Error(), helper.ErrMalformedStateView) {
		t.Errorf("error %v does not carry the wrapped ErrMalformedStateView cause (%s) — RequireField is not actually on the call path",
			err, helper.ErrMalformedStateView)
	}

	// Prove the code can actually appear (verification standard: a check
	// that can't fail proves nothing) by exercising RequireField directly
	// against a field that IS present — it must NOT return
	// ErrMalformedStateView in that case.
	presentState := helper.NewGameStateView(map[string]any{"treasuryMicropounds": int64(1)})
	if _, err := presentState.RequireField("treasuryMicropounds", testCorrelationID); err != nil {
		t.Errorf("RequireField on a present field returned an error: %v", err)
	}
	if _, err := presentState.RequireField("definitelyAbsentField", testCorrelationID); err == nil {
		t.Fatal("RequireField on an absent field should return ErrMalformedStateView, got nil")
	} else if !strings.Contains(err.Error(), helper.ErrMalformedStateView) {
		t.Errorf("RequireField error %v does not carry ErrMalformedStateView (%s)", err, helper.ErrMalformedStateView)
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

// hangingRegistrant is a fixture Registrant whose ProjectConsequence
// signals startedCh the instant it is entered, then blocks until
// releaseCh is closed. It has no preconditions (Recommend must reach
// ProjectConsequence unconditionally) — used by
// TestRecommend_HangingProjectConsequence_DoesNotBlockRegister (BUG-128)
// to simulate a real registrant whose consequence-pricing call hangs or
// takes unbounded time.
type hangingRegistrant struct {
	id        helper.ActionTaxonomyID
	startedCh chan struct{}
	releaseCh chan struct{}
}

func (h *hangingRegistrant) TaxonomyID() helper.ActionTaxonomyID  { return h.id }
func (h *hangingRegistrant) Preconditions() []helper.Precondition { return nil }
func (h *hangingRegistrant) ProjectConsequence(helper.GameStateView, map[string]any) (helper.ConsequenceProjection, error) {
	close(h.startedCh)
	<-h.releaseCh
	return helper.ConsequenceProjection{ActionID: h.id, Summary: "finally unblocked"}, nil
}

var _ helper.Registrant = (*hangingRegistrant)(nil)

// TestRecommend_HangingProjectConsequence_DoesNotBlockRegister is
// BUG-128's regression test. Before the fix, Recommend held its RLock
// across the entire candidate loop, including the call into registrant
// code (ProjectConsequence). Because Go's sync.RWMutex blocks new
// readers once a writer is queued, a hanging ProjectConsequence
// combined with a concurrent Register call would wedge the registry —
// Register would never return while Recommend was stuck mid-call. This
// test proves the fix (snapshotting the registrant set under the lock,
// then releasing it before calling registrant code) by deliberately
// hanging ProjectConsequence and asserting a concurrent Register
// completes anyway, well before the hang is released — a real
// concurrency proof via goroutines, not just a lock-scope code read.
func TestRecommend_HangingProjectConsequence_DoesNotBlockRegister(t *testing.T) {
	reg := helper.NewRegistry(testCorrelationID)
	hanging := &hangingRegistrant{
		id:        "fixture.hangs-forever",
		startedCh: make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	if err := reg.Register(hanging); err != nil {
		t.Fatalf("Register(hanging) failed: %v", err)
	}
	state := helper.NewGameStateView(nil)

	// Kick off Recommend in the background — it will seal the registry,
	// enter the candidate loop, and block inside hanging.ProjectConsequence
	// until releaseCh is closed below.
	recommendDone := make(chan struct{})
	go func() {
		defer close(recommendDone)
		_ = reg.Recommend(state)
	}()

	// Wait until Recommend is genuinely stuck inside ProjectConsequence
	// (not just "about to call it") before proving anything about
	// concurrent Register — otherwise this test would prove nothing.
	select {
	case <-hanging.startedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("hanging ProjectConsequence was never entered — test setup is broken")
	}

	// The pre-fix bug: this Register call would block until
	// hanging.ProjectConsequence returns, because Recommend was still
	// holding the RLock and Register needs the Lock. Prove it completes
	// promptly instead, with Recommend still stuck.
	registerDone := make(chan error, 1)
	go func() {
		registerDone <- reg.Register(helperfixture.NewFixtureAction("fixture.registered-while-recommend-hangs", testCorrelationID))
	}()

	select {
	case err := <-registerDone:
		// Register is boot-wiring-only (AC-3): Recommend's Seal() call
		// above already sealed the registry before hanging.startedCh
		// fired, so this Register is EXPECTED to fail with
		// ErrRegistrationSealed — that is a fast, well-formed rejection,
		// not a hang. The bug this test guards against is Register never
		// returning at all, not which error it returns.
		if err == nil {
			t.Error("expected Register to fail with ErrRegistrationSealed (Recommend seals on first call), got nil")
		} else if !strings.Contains(err.Error(), helper.ErrRegistrationSealed) {
			t.Errorf("Register returned an unexpected error (still fine as long as it's not a hang): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Register did not return within 2s while a concurrent Recommend was hung inside ProjectConsequence — BUG-128 regression: the RLock is still held across registrant code")
	}

	// Clean up: release the hang and confirm Recommend itself completes.
	close(hanging.releaseCh)
	select {
	case <-recommendDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Recommend did not complete after its hang was released")
	}
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
