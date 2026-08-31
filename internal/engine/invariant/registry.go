package invariant

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Invariant computes/verifies one conservation check against the
// current tick's Snapshot (AC-1). Check returns a Result whose Ran
// field distinguishes "this stock was not registered/reported for this
// tick" (skipped, AC-12) from "ran and balanced" — see Result's doc
// comment. A balanced/skipped check's Result.Violation is the zero
// value (IsZero() == true).
type Invariant interface {
	// Name identifies this invariant (e.g. "people", "money") — used as
	// the Registry's duplicate-registration key and as
	// Violation.InvariantName/InvariantOutcome.Name.
	Name() string

	// Check verifies conservation for state.Tick. Called once per tick
	// per registered invariant, from RunSuite.
	Check(state Snapshot) Result
}

// Registry holds the set of Invariants RunSuite runs each tick.
//
// # No exported Unregister (AC-1b, weakness pattern #1)
//
// There is no way to remove a previously-registered Invariant through
// this package's public API — the set of invariants actually run each
// tick can only grow, never silently shrink. A legitimate
// partial-registration state (some stocks registered, some not — the
// AC-12 case) is a property of the SNAPSHOT a registered invariant is
// checked against (StockReading.Registered), never of the Registry
// itself being starved of an invariant it once had.
type Registry struct {
	mu         sync.Mutex
	invariants []Invariant
	names      map[string]bool

	// self is Metropolis's standard struct-copy guard (SEC-014/SEC-016/
	// SEC-020 shape — see engine/core/engine.go's Engine.self,
	// foundation/registry/registry.go's Registry.self for the full
	// argument this mirrors verbatim). Registry combines a mutex with
	// two reference-type fields (invariants, a slice; names, a map), so
	// AC-16b applies directly: `r2 := *r` is legal, unsafe-free,
	// reflect-free Go from outside this package, and would give r2 its
	// OWN independent mu while r2.invariants/r2.names still ALIAS r's —
	// exactly the shape that can either fatal-crash on an unsynchronized
	// concurrent map write (SEC-003/SEC-019's shape) or, worse, hang a
	// copy's own Lock() forever if the copy was taken mid-lock on the
	// original (SEC-016's shape). atomic.Pointer, not a plain *Registry
	// field, so the identity check is race-safe and can run BEFORE mu is
	// ever touched — see checkNotCopied's doc comment.
	self atomic.Pointer[Registry]
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	r := &Registry{names: make(map[string]bool)}
	// Stored exactly once, here, before r is returned to any caller — no
	// goroutine can have a reference to r to race this Store against
	// (mirrors NewEngine/NewRegistry(foundation.registry)/etc.).
	r.self.Store(r)
	return r
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Registry value (AC-16b). Deliberately lock-free — a single
// atomic.Pointer.Load — so it is safe and correct to call BEFORE r.mu
// is ever touched; see foundation/registry/registry.go's
// checkNotCopied doc comment for the full SEC-016 argument this
// mirrors: a copy's mu can read as "currently locked" if it was copied
// while the original's mu was held, and attempting Lock() on such a
// copy before rejecting it can hang forever.
// BUG-456 perf: correlationID minted LAZILY (only on the never-taken copy
// path). Invariants() is called every tick by the invariant hook and used
// to call checkNotCopied twice per call, eagerly minting two crypto/rand
// UUIDs each tick that were thrown away on a live registry — a large share
// of the +33% alloc-count regression. Hot callers pass ""; behaviour on a
// real copy is unchanged (a valid ID is still minted).
func (r *Registry) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		if correlationID == "" {
			correlationID = errs.NewCorrelationID()
		}
		return errs.New(ErrRegistryCopied, correlationID, ctx)
	}
	return nil
}

// Register adds inv to the registry, keyed by its Name(). Rejects a nil
// Invariant (ErrNilInvariant) and a duplicate Name() (ErrDuplicateInvariant,
// never a silent overwrite — mirrors foundation.registry.Register's
// duplicate-key behaviour).
func (r *Registry) Register(inv Invariant) error {
	// Identity check BEFORE r.mu is touched at all (SEC-016 ordering —
	// see checkNotCopied's doc comment).
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Register"}); err != nil {
		return err
	}
	if inv == nil {
		return errs.New(ErrNilInvariant, errs.NewCorrelationID(), nil)
	}
	name := inv.Name()

	r.mu.Lock()
	defer r.mu.Unlock()
	// Defence-in-depth re-check under the lock (cheap, one more atomic
	// load) — mirrors every other guarded type in this codebase.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Register", "name": name}); err != nil {
		return err
	}
	if r.names[name] {
		return errs.New(ErrDuplicateInvariant, errs.NewCorrelationID(), map[string]any{"name": name})
	}
	r.names[name] = true
	r.invariants = append(r.invariants, inv)
	return nil
}

// Invariants returns a defensive copy of the registered invariants, in
// registration order — mutating the returned slice never affects the
// Registry's own state (same pattern as foundation.registry.List()).
func (r *Registry) Invariants() []Invariant {
	// BUG-456 perf: "" — per-tick hot path; the correlation ID is minted
	// only on the (never-taken) copy path, not twice on every tick.
	if err := r.checkNotCopied("", map[string]any{"method": "Invariants"}); err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied("", map[string]any{"method": "Invariants"}); err != nil {
		return nil
	}
	out := make([]Invariant, len(r.invariants))
	copy(out, r.invariants)
	return out
}

// InvariantOutcome is one Invariant's per-tick result within a
// SuiteResult — the structural "ran vs skipped" record AC-1b requires,
// so a consumer can never mistake a partial run for a full clean one.
type InvariantOutcome struct {
	Name      string
	Ran       bool
	Violation Violation
}

// SuiteResult is RunSuite's per-tick verdict across every registered
// invariant.
type SuiteResult struct {
	Tick int64

	// Outcomes holds one InvariantOutcome per registered invariant, in
	// registration order (AC-13's determinism, AC-1b's structural
	// ran/skipped reporting).
	Outcomes []InvariantOutcome

	// AnyViolation is true iff at least one Outcome's Violation is
	// Detected.
	AnyViolation bool

	// AllRan is true iff every Outcome's Ran is true. False means at
	// least one registered invariant's stock was not yet reported for
	// this tick (AC-12) — a legitimate, expected state during partial
	// registration, structurally distinct from AnyViolation (AC-1b: a
	// consumer must never conflate "3 of 4 ran clean" with "4 of 4 ran
	// clean").
	AllRan bool
}

// RunSuite runs every invariant registered in reg against state, in
// registration order, and returns the aggregate SuiteResult (AC-10:
// this is the standalone entry point harness.headless/CI drive per
// tick, and the function hook.go's PhaseHook wraps for the live tick
// path). Pure and side-effect-free: RunSuite itself never logs, panics,
// or mutates reg — see hook.go for what a caller does with the result
// on the live tick path.
func RunSuite(reg *Registry, state Snapshot) SuiteResult {
	invariants := reg.Invariants()
	result := SuiteResult{Tick: state.Tick, AllRan: true}
	result.Outcomes = make([]InvariantOutcome, 0, len(invariants))

	for _, inv := range invariants {
		r := inv.Check(state)
		result.Outcomes = append(result.Outcomes, InvariantOutcome{
			Name:      inv.Name(),
			Ran:       r.Ran,
			Violation: r.Violation,
		})
		if !r.Ran {
			result.AllRan = false
		}
		if r.Violation.Detected {
			result.AnyViolation = true
		}
	}
	return result
}
