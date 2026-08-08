package solver

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNoFallback is returned by Get when no registered backend supports
// the requested ProblemKind. The mandatory local-fallback rule (M0-ENG
// §1, GDD §15) is that a CPU backend must be registered for every
// ProblemKind before the engine ever calls Get for it; ErrNoFallback
// means that invariant has been violated — this is a wiring bug in the
// binary's init sequence, not a runtime condition callers should expect
// to recover from.
var ErrNoFallback = errors.New("solver: no backend registered for problem kind (mandatory CPU fallback missing)")

// FailoverEvent is emitted, via a Registry's failover hook if one is set,
// every time a higher-priority backend errors and the chain solver
// returned by Get falls through to the next candidate. Tests use this to
// observe failover behaviour without inspecting internal state.
type FailoverEvent struct {
	Problem ProblemKind
	Backend string // name of the backend that failed
	Err     error
}

// registration is one Register call's bookkeeping.
type registration struct {
	name     string
	solver   Solver
	priority int
	seq      int // registration order; breaks priority ties deterministically
}

// Registry holds backend registrations and resolves the priority-ordered,
// fallback-wrapped Solver for a ProblemKind. The zero value is not ready
// for use; construct with NewRegistry. Safe for concurrent use by
// multiple goroutines.
type Registry struct {
	mu         sync.RWMutex
	regs       []registration
	nextSeq    int
	onFailover func(FailoverEvent)
}

// NewRegistry constructs an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a backend under the given name and priority. Get tries
// backends supporting a given ProblemKind highest-priority first,
// falling back to lower-priority ones on error; ties are broken by
// registration order (first registered wins). A backend is a candidate
// for every ProblemKind for which its Supports method returns true —
// Register itself is not scoped to a single ProblemKind.
//
// Register does not deduplicate: registering the same name twice adds
// two independent candidates. Callers should register each backend
// exactly once, normally at process init (see cpu.go's convention).
func (r *Registry) Register(name string, s Solver, priority int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.regs = append(r.regs, registration{name: name, solver: s, priority: priority, seq: r.nextSeq})
	r.nextSeq++
}

// SetFailoverHook installs a callback invoked synchronously every time a
// chain solver returned by Get falls over from one backend to the next
// after an error. Pass nil to clear. Intended for tests and diagnostic
// logging; production simulation logic must not depend on call timing or
// treat the hook as anything but observational.
func (r *Registry) SetFailoverHook(fn func(FailoverEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFailover = fn
}

// Get returns the fallback-wrapped Solver for problem: a chain that
// tries every registered backend supporting problem, highest priority
// first, transparently retrying the next candidate if one returns an
// error. The caller sees only total success or total failure from the
// returned Solver's Solve method — never a bare error from an
// intermediate backend (though every candidate's error is preserved,
// wrapped and joined, in the final failure).
//
// Get itself fails loudly with ErrNoFallback if no backend supports
// problem at all — see ErrNoFallback's doc comment. Note this deviates
// from a bare `Get(problem) Solver` signature on purpose: a silent nil or
// a panic would violate Golden Rule #1 (aggressive error trapping), so
// the missing-fallback case is a typed, returned error instead.
func (r *Registry) Get(problem ProblemKind) (Solver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var candidates []registration
	for _, reg := range r.regs {
		if reg.solver.Supports(problem) {
			candidates = append(candidates, reg)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: problem=%s", ErrNoFallback, problem)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].seq < candidates[j].seq
	})

	return &chainSolver{problem: problem, candidates: candidates, onFailover: r.onFailover}, nil
}

// chainSolver implements Solver by trying its candidates in priority
// order, falling back to the next on error. It is returned by Get; it is
// never itself registered into a Registry.
type chainSolver struct {
	problem    ProblemKind
	candidates []registration
	onFailover func(FailoverEvent)
}

// Supports reports true only for the ProblemKind this chain was built
// for — a chainSolver is single-purpose, unlike the backends it wraps.
func (c *chainSolver) Supports(problem ProblemKind) bool { return problem == c.problem }

// Solve tries each candidate backend in priority order. The first
// success wins. Every failure is wrapped with the backend's name,
// reported via the failover hook (if any candidates remain), and
// accumulated; if every candidate fails, Solve returns a single joined
// error covering all of them.
func (c *chainSolver) Solve(req Request) (Response, error) {
	var errs []error
	for i, cand := range c.candidates {
		resp, err := cand.solver.Solve(req)
		if err == nil {
			return resp, nil
		}

		wrapped := fmt.Errorf("solver backend %q failed: %w", cand.name, err)
		errs = append(errs, wrapped)

		hasNext := i < len(c.candidates)-1
		if hasNext && c.onFailover != nil {
			c.onFailover(FailoverEvent{Problem: c.problem, Backend: cand.name, Err: err})
		}
	}
	return Response{}, fmt.Errorf("solver: all %d backend(s) failed for problem %s: %w",
		len(c.candidates), c.problem, errors.Join(errs...))
}

// Default is the process-wide registry that backends register into at
// init, and that engine code resolves against via the package-level
// Register/Get functions below. Tests that need isolation from other
// tests' registrations should construct their own Registry with
// NewRegistry instead of using Default.
var Default = NewRegistry()

// Register adds a backend to the Default registry. See (*Registry).Register.
func Register(name string, s Solver, priority int) {
	Default.Register(name, s, priority)
}

// Get resolves the fallback-wrapped Solver for problem against the
// Default registry. See (*Registry).Get.
func Get(problem ProblemKind) (Solver, error) {
	return Default.Get(problem)
}
