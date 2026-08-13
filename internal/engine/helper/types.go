package helper

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ActionTaxonomyID identifies one registered player action (AC-1a). The
// underlying string is the action's registry identity — there is no
// separate name-mapping table, mirroring engine.market's CommodityType
// pattern (its JSON/Go identity is the same value).
type ActionTaxonomyID string

// GameStateView is a read-only, deliberately minimal/extensible view of
// game state a Precondition or Registrant.ProjectConsequence may query
// (US-4; ASM logged 2026-08-12 on FEAT-063 — "GameStateView field set
// left minimal/extensible", since which fields exist depends on which
// engine modules are queryable at the time a real feature registers).
// It carries an open string-keyed field map rather than a fixed struct
// so this item does not need to pin a shape ahead of the engine modules
// that will eventually populate it — a later feature module extends the
// set of fields it populates without any change to this type or to
// Registry's exported signatures.
//
// GameStateView is a value type (not a pointer), so passing one never
// lets a callee mutate the caller's state — Preview's "never mutates
// state" obligation (AC-8) is enforced structurally by this choice, not
// just by convention.
type GameStateView struct {
	fields map[string]any
}

// NewGameStateView builds a GameStateView from a field map. The map is
// copied so the returned view cannot be mutated through the caller's
// original reference afterward (GR#21-adjacent defensive copy — this
// package's determinism guarantees would otherwise depend on the
// caller's discipline, not the type).
func NewGameStateView(fields map[string]any) GameStateView {
	copied := make(map[string]any, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	return GameStateView{fields: copied}
}

// Field returns the named field's value and whether it was present.
// Never panics on a zero-value GameStateView (AC-14) — a zero-value
// view simply has no fields.
func (v GameStateView) Field(name string) (any, bool) {
	if v.fields == nil {
		return nil, false
	}
	val, ok := v.fields[name]
	return val, ok
}

// RequireField is the convenience a Precondition/Registrant uses to
// fetch a field it cannot proceed without, returning a registry-sourced
// ErrMalformedStateView (GR#7) — never a bare bool/nil pair a caller
// could mistake for "field present but empty" — when name is absent.
// correlationID is the caller's own, so the error is attributable to the
// query that actually failed, not to this package generically.
func (v GameStateView) RequireField(name, correlationID string) (any, error) {
	val, ok := v.Field(name)
	if !ok {
		return nil, errs.New(ErrMalformedStateView, correlationID, map[string]any{
			"field": name,
		})
	}
	return val, nil
}

// Precondition is one independently-evaluable gate on whether a
// registered action is currently available (AC-1b, AC-2). Deliberately
// an interface with three named members — never a single opaque bool —
// so Recommend/Preview can surface WHICH preconditions pass/fail, not
// just a final verdict.
type Precondition interface {
	// ID returns a stable identifier for this precondition, unique
	// within its owning Registrant (used to report pass/fail per
	// precondition — AC-8).
	ID() string
	// Description returns a human-readable explanation of what this
	// precondition checks, for panel display.
	Description() string
	// Evaluate reports whether this precondition currently holds against
	// state. It must never panic on a well-formed GameStateView (AC-2)
	// and must return a registry-sourced error — never a silent false —
	// when it genuinely cannot determine pass/fail (e.g. a required
	// field is absent from state).
	Evaluate(state GameStateView) (bool, error)
}

// ConsequenceProjection is the structured result of a consequence-
// pricing projection query (AC-1c) — never a bare string or
// interface{}, so a caller can inspect its numeric fields rather than
// parse prose. Fields is an open, extensible bag for whatever a real
// registrant's projection needs to carry beyond the two named fields
// below (US-4 — the data model must not need rework for a later
// contextual-preview layer).
type ConsequenceProjection struct {
	// ActionID echoes the action this projection is for, so a caller
	// holding only a ConsequenceProjection (e.g. after a drift-test
	// comparison) can still attribute it.
	ActionID ActionTaxonomyID
	// Summary is a short, human-readable headline for this projection
	// (e.g. "costs approximately X, unlocks Y") — Recommend uses this as
	// both a candidate's headline consequence AND, absent any other
	// contract member for it, its human-readable description (see
	// Recommendation's doc comment for why: AC-1 fixes Registrant to
	// exactly three members, none of which is a standalone description
	// accessor, so Summary is deliberately reused for both roles rather
	// than adding a fourth Registrant member — logged as an ASM on
	// FEAT-063, "Recommendation-level description source").
	Summary string
	// CostMicropounds is the projected cost in M0-ENG §1.2 fixed-point
	// micropounds (0 for a zero/no-cost action — a real registrant that
	// has no monetary cost sets this to 0 explicitly, never omits it).
	CostMicropounds int64
	// Fields is the open extensibility bag (US-4) for anything a real
	// registrant's projection needs beyond Summary/CostMicropounds.
	Fields map[string]any
}

// PreconditionResult is one Precondition's evaluated pass/fail state,
// as surfaced by Preview (AC-8) — so a preview can honestly report "here
// is what would happen, but you don't currently qualify" rather than
// presenting an unreachable fantasy as available.
type PreconditionResult struct {
	ID          string
	Description string
	Passed      bool
}

// Recommendation is one candidate action Recommend returns (AC-7). It
// deliberately carries NO rank/score/best field — Recommend returns an
// unordered candidate SET the player chooses among, never a single
// ranked "do this" imperative (the north-star-preserving shape this
// item's design framing requires). Do not add a Rank/Score/Best field
// to this type; see AC-7's "what a lazy implementation looks like".
type Recommendation struct {
	ActionID ActionTaxonomyID
	// Description is this candidate's human-readable description
	// (Recommend requirement) — sourced from HeadlineConsequence.Summary
	// when a headline consequence was computed, per ConsequenceProjection's
	// doc comment above; falls back to string(ActionID) when the headline
	// consequence could not be computed (registrant returned an error),
	// so Recommend never omits a candidate outright just because its
	// projection failed.
	Description string
	// HeadlineConsequence is the candidate's projected consequence,
	// computed with a zero-value params map (AC-7: "where cheap to
	// compute"). Nil when ProjectConsequence returned an error for this
	// candidate — the candidate still appears (its preconditions passed),
	// just without a headline.
	HeadlineConsequence *ConsequenceProjection
}

// PreviewResult is Preview's return value (AC-8): the resolved action's
// current precondition pass/fail state alongside its consequence
// projection, so a preview can honestly report an unreachable action as
// unreachable rather than silently presenting it as available.
type PreviewResult struct {
	ActionID      ActionTaxonomyID
	Preconditions []PreconditionResult
	Projection    ConsequenceProjection
}

// Registrant is the advisor-metadata registration contract (AC-1) every
// future player-action feature module implements and registers with a
// *Registry at boot. It has EXACTLY three members — go doc on this type
// must show exactly these three, no more — per AC-1's structural check
// and AC-4's "registering costs ONLY metadata" standing constraint: no
// UI rendering hook, no RenderHint/IconID/hover-callback field, ever, on
// this interface (see doc.go).
type Registrant interface {
	// TaxonomyID returns this action's stable, non-empty,
	// registry-unique ActionTaxonomyID (AC-1a).
	TaxonomyID() ActionTaxonomyID
	// Preconditions returns this action's independently-evaluable
	// preconditions (AC-1b) — never a single opaque boolean. May return
	// an empty (or nil) slice for an action with no preconditions; never
	// panics.
	Preconditions() []Precondition
	// ProjectConsequence computes this action's consequence-pricing
	// projection for the given parameters against a read-only state view
	// (AC-1c). MUST read from the same pricing/data source the action's
	// real execution path would use — never a second, hand-maintained
	// duplicate that can silently drift out of truth (AC-5; weakness
	// pattern #2 — see doc.go's worked example). Returns a structured
	// ConsequenceProjection or a registry-sourced error — never a bare
	// string or interface{}. Must be side-effect-free and deterministic
	// (GR#21, AC-6): no time.Now(), no unseeded RNG, no mutation of
	// state or any package-level variable observable outside this call.
	ProjectConsequence(state GameStateView, params map[string]any) (ConsequenceProjection, error)
}

// sortedTaxonomyIDs returns ids in a fixed, deterministic order (GR#21)
// — every exported Registry method that ranges over the registrants map
// (a Go map, whose iteration order is intentionally randomised) uses
// this rather than ranging directly, so two calls with the same
// registered set always process/return actions in the same order.
func sortedTaxonomyIDs(m map[ActionTaxonomyID]Registrant) []ActionTaxonomyID {
	ids := make([]ActionTaxonomyID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
