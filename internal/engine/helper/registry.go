package helper

import (
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Registry holds every Registrant a player-action feature module has
// registered at boot, and answers the ask-driven panel's two pull-only
// queries (Recommend/Preview) against them (US-1, US-2, US-3).
//
// The zero value is not usable — construct via NewRegistry. A *Registry
// is safe for concurrent use by multiple goroutines once sealed (AC-13):
// every exported method takes the internal mutex, so Register/Recommend/
// Preview never race even under `go test -race`, regardless of sealing
// state.
type Registry struct {
	mu            sync.RWMutex
	registrants   map[ActionTaxonomyID]Registrant
	sealed        bool
	correlationID string
}

// NewRegistry constructs an empty, unsealed *Registry. correlationID is
// attached to every registry-sourced error this Registry (and its
// Register/Recommend/Preview calls) construct (GR#1).
func NewRegistry(correlationID string) *Registry {
	return &Registry{
		registrants:   make(map[ActionTaxonomyID]Registrant),
		correlationID: correlationID,
	}
}

// Register adds reg to the registry. Boot-wiring-only (AC-3): once the
// registry has been sealed — by an explicit Seal call, or implicitly by
// the first Recommend/Preview read — every further Register call
// returns ErrRegistrationSealed, the target set is left unchanged, and
// no panic occurs. This is enforced by the sealed flag under the same
// mutex Recommend/Preview take, not stated only in a doc comment
// (dev-team-process.md's "a comment saying 'never X at runtime' is a
// code smell, not a control").
//
// Rejects a Registrant with an empty ActionTaxonomyID
// (ErrEmptyTaxonomyID) or one already registered
// (ErrDuplicateTaxonomyID, AC-1a's "registry-unique" requirement).
func (r *Registry) Register(reg Registrant) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if reg == nil {
		// A nil Registrant would panic on the TaxonomyID() call below
		// (calling a method on a nil interface value) — treated the same
		// as an empty taxonomy id (AC-14: no exported call may panic).
		return errs.New(ErrEmptyTaxonomyID, r.correlationID, map[string]any{
			"cause": "Register called with a nil Registrant",
		})
	}

	if r.sealed {
		return errs.New(ErrRegistrationSealed, r.correlationID, map[string]any{
			"actionID": string(safeTaxonomyID(reg)),
		})
	}

	id := reg.TaxonomyID()
	if id == "" {
		return errs.New(ErrEmptyTaxonomyID, r.correlationID, nil)
	}
	if _, exists := r.registrants[id]; exists {
		return errs.New(ErrDuplicateTaxonomyID, r.correlationID, map[string]any{
			"actionID": string(id),
		})
	}

	r.registrants[id] = reg
	return nil
}

// safeTaxonomyID calls reg.TaxonomyID() defensively for error-context
// purposes only — reg is never nil on any exported call path today, but
// an error-message helper is exactly the kind of code that must not
// itself be a new panic source (AC-14) if that ever changes.
func safeTaxonomyID(reg Registrant) ActionTaxonomyID {
	if reg == nil {
		return ""
	}
	return reg.TaxonomyID()
}

// Seal explicitly closes registration ahead of any Recommend/Preview
// call. Idempotent — calling Seal on an already-sealed *Registry is a
// no-op, never an error. Recommend and Preview call this implicitly on
// their first read, so most callers never need to call Seal directly;
// it exists for boot wiring that wants the seal to happen at a known
// point rather than on first query (AC-3).
func (r *Registry) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Recommend answers "what should I do?" (US-2, AC-7): the set of
// currently-registered actions whose preconditions ALL currently
// evaluate true against state, in a fixed deterministic order
// (ActionTaxonomyID ascending — GR#21), each carrying its taxonomy id,
// a human-readable description, and (where its projection was cheap and
// error-free to compute) a headline consequence summary. This is a
// candidate SET the player chooses among — never a ranked "do this"
// imperative; Recommendation deliberately carries no rank/score field
// (see Recommendation's doc comment).
//
// A Precondition that returns a registry-sourced evaluation error (AC-2)
// for a candidate excludes that candidate from the result rather than
// failing the whole call — one mis-evaluable action must not make every
// OTHER currently-available action invisible to the player. Recommend
// itself never returns a non-nil error for this reason; a genuinely
// broken registry (e.g. this call racing a concurrent mutation, which
// the mutex rules out) has no other failure mode to report here.
func (r *Registry) Recommend(state GameStateView) []Recommendation {
	r.Seal()

	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []Recommendation
	for _, id := range sortedTaxonomyIDs(r.registrants) {
		reg := r.registrants[id]
		allPass := true
		for _, p := range reg.Preconditions() {
			if p == nil {
				// A nil entry in a Registrant's own Preconditions slice
				// (AC-14: "a Registrant returning a nil precondition
				// slice" must never panic) — treat a nil precondition
				// itself as "cannot evaluate" and exclude the
				// candidate, rather than dereferencing it.
				allPass = false
				break
			}
			ok, err := p.Evaluate(state)
			if err != nil || !ok {
				allPass = false
				break
			}
		}
		if !allPass {
			continue
		}

		rec := Recommendation{ActionID: id, Description: string(id)}
		proj, err := reg.ProjectConsequence(state, nil)
		if err == nil {
			p := proj
			rec.HeadlineConsequence = &p
			if p.Summary != "" {
				rec.Description = p.Summary
			}
		}
		out = append(out, rec)
	}
	return out
}

// Preview answers "what if I do X?" (US-3, AC-8): resolves actionID
// against the registry (ErrUnknownAction for an unregistered id — never
// a silent zero-value projection), evaluates that action's preconditions
// against state and surfaces their pass/fail state alongside the
// projection, and calls the registrant's ProjectConsequence with
// state/params, returning its result unmodified. Preview never mutates
// state (GameStateView is a value type — see its doc comment) and never
// calls any action-execution path; it is read-only by construction
// (v1's pull-only, no-side-effects requirement, AC-8's "single most
// direct violation of pull-only possible" warning).
func (r *Registry) Preview(state GameStateView, actionID ActionTaxonomyID, params map[string]any) (PreviewResult, error) {
	r.Seal()

	r.mu.RLock()
	reg, ok := r.registrants[actionID]
	r.mu.RUnlock()
	if !ok {
		return PreviewResult{}, errs.New(ErrUnknownAction, r.correlationID, map[string]any{
			"actionID": string(actionID),
		})
	}

	var results []PreconditionResult
	for _, p := range reg.Preconditions() {
		if p == nil {
			continue
		}
		passed, err := p.Evaluate(state)
		if err != nil {
			return PreviewResult{}, err
		}
		results = append(results, PreconditionResult{
			ID:          p.ID(),
			Description: p.Description(),
			Passed:      passed,
		})
	}

	proj, err := reg.ProjectConsequence(state, params)
	if err != nil {
		return PreviewResult{}, err
	}

	return PreviewResult{
		ActionID:      actionID,
		Preconditions: results,
		Projection:    proj,
	}, nil
}
