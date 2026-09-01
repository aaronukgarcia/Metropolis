// Package helperfixture is FEAT-063's fixture-only proof package
// (AC-11): since no real player-action feature exists yet to register
// with engine.helper, every acceptance criterion that references "a
// real feature registering" is proven here instead, mirroring
// tool.astgate.md's/BUG-024's fixture-driven proof pattern. This
// package makes NO change to any existing internal/engine/<other>/
// package to add a registration (AC-11) — it is entirely self-contained.
//
// This package has ZERO UI-facing code anywhere (AC-4a/AC-4b): it
// imports nothing from the UI package tree, and has no field or method
// reacting to a selection/hover/cursor event. That is proven both by
// construction (read the source) and mechanically (this package's own
// source is grepped for the UI package's import path, mirroring AC-4b's
// check command, and returns no matches).
package helperfixture

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/helper"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FixtureCorrelationID is the correlation ID this package's fixture
// errors are constructed with, unless a caller supplies its own via
// NewTreasuryPrecondition/NewFixtureAction's correlationID parameter.
const FixtureCorrelationID = "helperfixture-default"

// errPreconditionEvalFailed is the registry code for TreasuryPrecondition
// evaluation failures. Defined locally as a literal (not as
// helper.ErrPreconditionEvalFailed) so the errs gate's AST scanner can
// resolve it (the scanner only recognizes string literal const values).
const errPreconditionEvalFailed = "MET-E704"

// --- the shared pricing data source (AC-5's drift-test pattern) ---

// sharedFixtureCostMicropounds is the SINGLE data source both
// FixtureAction.ProjectConsequence and FixtureExecuteCost read from —
// AC-5's required pattern: a registrant's projection and its real
// execution path must never be two independently hand-maintained
// numbers that can silently drift apart. Because both functions below
// read this one variable, the "good" fixture cannot drift by
// construction; TestFixtureAction_ProvesDriftDetectable (in
// fixture_test.go) proves the comparison ITSELF is capable of catching a
// real divergence, per weakness pattern #2 step 4 ("mutate the fixture's
// duplicated constant and confirm the test catches it, don't just add
// the test") — see that test for the deliberately-duplicated
// counter-example it constructs to demonstrate the check can fail.
// BUG-452 (2026-09-01): rebased 5_000_000 -> 5_000 alongside the money
// base-unit rebase (1e-6 GBP/unit -> 1e-3 GBP/unit) so this fixture keeps
// reading as the same real £5.00, not a value 1000x too large.
var sharedFixtureCostMicropounds int64 = 5_000 // £5.00, arbitrary fixture value

// FixtureExecuteCost stands in for the cost a real feature's ACTUAL
// execution path would charge, for the drift test to compare
// FixtureAction.ProjectConsequence's quoted cost against. Reads the same
// sharedFixtureCostMicropounds variable FixtureAction.ProjectConsequence
// reads (AC-5).
func FixtureExecuteCost() int64 {
	return sharedFixtureCostMicropounds
}

// --- the fixture precondition ---

// treasuryFieldName is the GameStateView field TreasuryPrecondition
// requires to be present — a fixture stand-in for "the player can
// afford this action".
const treasuryFieldName = "treasuryMicropounds"

// treasuryPrecondition is a fixture Precondition (AC-2): it requires
// GameStateView's "treasuryMicropounds" field, passes when that value is
// >= sharedFixtureCostMicropounds, and returns a registry-sourced
// ErrPreconditionEvalFailed (never a silent false) when the field is
// absent or is not an int64.
type treasuryPrecondition struct {
	correlationID string
}

// NewTreasuryPrecondition constructs the fixture treasury-affordability
// precondition, attaching correlationID to any error it constructs.
func NewTreasuryPrecondition(correlationID string) helper.Precondition {
	return treasuryPrecondition{correlationID: correlationID}
}

func (p treasuryPrecondition) ID() string { return "fixture.treasury-sufficient" }

func (p treasuryPrecondition) Description() string {
	return "the treasury holds at least the fixture action's projected cost"
}

func (p treasuryPrecondition) Evaluate(state helper.GameStateView) (bool, error) {
	// BUG-146: uses GameStateView.RequireField rather than a hand-rolled
	// Field/ok check, so a missing field is diagnosed by the package's
	// own ErrMalformedStateView (MET-E705) sentinel first, then wrapped
	// (via errs.Wrap, preserving the cause for errors.Unwrap/As) into
	// this precondition's ErrPreconditionEvalFailed — RequireField's doc
	// comment's "a Precondition/Registrant MAY wrap
	// ErrPreconditionEvalFailed with" case. This is the real call site
	// RequireField was missing (it had zero callers before this fix).
	raw, err := state.RequireField(treasuryFieldName, p.correlationID)
	if err != nil {
		// AC-2: a genuinely-unevaluable state (field absent) is a
		// registry-sourced error, never (false, nil) — (false, nil)
		// would read to a caller as "the treasury is insufficient",
		// which is a different, false claim from "this could not be
		// checked at all".
		return false, errs.Wrap(errPreconditionEvalFailed, p.correlationID, err, map[string]any{
			"preconditionID": p.ID(),
			"cause":          "GameStateView missing field \"" + treasuryFieldName + "\"",
		})
	}
	balance, ok := raw.(int64)
	if !ok {
		return false, errs.New(errPreconditionEvalFailed, p.correlationID, map[string]any{
			"preconditionID": p.ID(),
			"cause":          "GameStateView field \"" + treasuryFieldName + "\" is not an int64",
		})
	}
	return balance >= sharedFixtureCostMicropounds, nil
}

// --- the fixture Registrant ---

// FixtureAction is FEAT-063's minimal fixture Registrant (AC-1): it
// implements exactly Registrant's three members and NOTHING else — no
// UI-facing field or method anywhere on it, proving AC-4's "registering
// costs ONLY metadata" constraint from the "skip nothing because nothing
// calls it yet" direction.
type FixtureAction struct {
	id            helper.ActionTaxonomyID
	correlationID string
}

// NewFixtureAction constructs a fixture Registrant with the given
// taxonomy id. Its single precondition is a fresh
// NewTreasuryPrecondition, and its ProjectConsequence reads
// sharedFixtureCostMicropounds (the AC-5 shared data source).
func NewFixtureAction(id helper.ActionTaxonomyID, correlationID string) *FixtureAction {
	return &FixtureAction{id: id, correlationID: correlationID}
}

// compile-time assertion this package's fixture actually satisfies
// helper.Registrant (AC-1's check).
var _ helper.Registrant = (*FixtureAction)(nil)

func (f *FixtureAction) TaxonomyID() helper.ActionTaxonomyID { return f.id }

func (f *FixtureAction) Preconditions() []helper.Precondition {
	return []helper.Precondition{NewTreasuryPrecondition(f.correlationID)}
}

// ProjectConsequence returns a projection sourced from
// sharedFixtureCostMicropounds — the SAME variable FixtureExecuteCost
// reads (AC-5) — never a second, independently-authored number.
func (f *FixtureAction) ProjectConsequence(state helper.GameStateView, params map[string]any) (helper.ConsequenceProjection, error) {
	return helper.ConsequenceProjection{
		ActionID:        f.id,
		Summary:         "fixture action: a fixed fixture cost, no other effect",
		CostMicropounds: sharedFixtureCostMicropounds,
		Fields:          map[string]any{},
	}, nil
}

// --- a second fixture: precondition-always-fails, to prove Recommend
// correctly excludes it and Preview correctly surfaces the failure
// (AC-7, AC-8) ---

// alwaysFailPrecondition is a fixture Precondition that evaluates
// cleanly (no error) but always reports false — the "you don't
// currently qualify" case Preview must surface honestly (AC-8),
// distinct from an evaluation error (treasuryPrecondition above).
type alwaysFailPrecondition struct{}

func (alwaysFailPrecondition) ID() string          { return "fixture.always-fails" }
func (alwaysFailPrecondition) Description() string { return "a fixture precondition that never passes" }
func (alwaysFailPrecondition) Evaluate(helper.GameStateView) (bool, error) {
	return false, nil
}

// UnreachableFixtureAction is a fixture Registrant whose single
// precondition always evaluates false — used to prove Recommend
// excludes it and Preview still returns its projection alongside the
// failing-precondition information (AC-8).
type UnreachableFixtureAction struct {
	id helper.ActionTaxonomyID
}

// NewUnreachableFixtureAction constructs the always-unreachable fixture
// Registrant.
func NewUnreachableFixtureAction(id helper.ActionTaxonomyID) *UnreachableFixtureAction {
	return &UnreachableFixtureAction{id: id}
}

var _ helper.Registrant = (*UnreachableFixtureAction)(nil)

func (u *UnreachableFixtureAction) TaxonomyID() helper.ActionTaxonomyID { return u.id }

func (u *UnreachableFixtureAction) Preconditions() []helper.Precondition {
	return []helper.Precondition{alwaysFailPrecondition{}}
}

func (u *UnreachableFixtureAction) ProjectConsequence(state helper.GameStateView, params map[string]any) (helper.ConsequenceProjection, error) {
	return helper.ConsequenceProjection{
		ActionID: u.id,
		Summary:  "fixture action: currently unreachable",
		// BUG-452 (2026-09-01): rebased 1_000_000 -> 1_000 alongside its
		// sibling sharedFixtureCostMicropounds (same money base-unit
		// change, same real £1.00) — this action's own Preconditions()
		// always fail (alwaysFailPrecondition{}), so this figure is never
		// actually charged, but is kept consistent with the rest of this
		// file's money constants rather than left silently stale.
		CostMicropounds: 1_000,
	}, nil
}

// --- a third fixture: zero preconditions and zero-value-safe, for the
// AC-14 no-panic sweep ---

// NoPreconditionFixtureAction is a fixture Registrant with a nil
// Preconditions() result (AC-14: "a Registrant returning a nil
// precondition slice" must never panic downstream) and a
// ProjectConsequence that tolerates a zero-value GameStateView/nil
// params without panicking.
type NoPreconditionFixtureAction struct {
	id helper.ActionTaxonomyID
}

// NewNoPreconditionFixtureAction constructs the fixture Registrant with
// no preconditions at all.
func NewNoPreconditionFixtureAction(id helper.ActionTaxonomyID) *NoPreconditionFixtureAction {
	return &NoPreconditionFixtureAction{id: id}
}

var _ helper.Registrant = (*NoPreconditionFixtureAction)(nil)

func (n *NoPreconditionFixtureAction) TaxonomyID() helper.ActionTaxonomyID { return n.id }

func (n *NoPreconditionFixtureAction) Preconditions() []helper.Precondition { return nil }

func (n *NoPreconditionFixtureAction) ProjectConsequence(state helper.GameStateView, params map[string]any) (helper.ConsequenceProjection, error) {
	return helper.ConsequenceProjection{
		ActionID:        n.id,
		Summary:         "fixture action: always available, no preconditions",
		CostMicropounds: 0,
	}, nil
}
