package solver

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ErrRegistryCopied is returned by Register, SetFailoverHook, and Get when
// called on a Registry value that is not the one NewRegistry constructed —
// i.e. a struct copy (SEC-020 wave 2, mirroring Engine.checkNotCopied/
// SubscriptionServer.checkNotCopied/InProcTransport.checkNotCopied — see
// this package's registry.go doc comments below for the reasoning, and
// internal/engine/core/engine.go for the canonical writeup this mirrors).
// Registered in data/errors.json under foundation.solver's F400-F499
// range as MET-F400.
const ErrRegistryCopied = "MET-F400"

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
// fallback-wrapped Solver for a ProblemKind. The zero value is NOT ready
// for use (SEC-020 wave 2 — see self's doc comment below); construct with
// NewRegistry. Safe for concurrent use by multiple goroutines.
type Registry struct {
	mu         sync.RWMutex
	regs       []registration
	nextSeq    int
	onFailover func(FailoverEvent)

	// self holds the address NewRegistry gave this Registry at
	// construction (self.Store(r), set once, at the end of NewRegistry,
	// before r is returned to any caller — no goroutine can have a
	// reference to r to race that Store against).
	//
	// SEC-020 wave 2: Registry is exported and so is NewRegistry, so any
	// caller can dereference-and-copy a live *Registry ('r2 := *r' is
	// legal, unsafe-free, reflect-free Go — every field here is
	// unexported, but Go does not stop a caller from copying the struct
	// value a *Registry points at). mu is a plain sync.RWMutex VALUE, so
	// the copy r2 gets its OWN, independent mu — but regs (a slice
	// header, a reference type once it has backing array elements) still
	// ALIASES the original's backing array, and onFailover (a func
	// value, itself typically a closure over shared state) is copied by
	// reference to whatever it closes over. Mutating r2.regs via
	// r2.Register (append) can silently share or diverge from r.regs
	// depending on capacity headroom at copy time — exactly the kind of
	// "sometimes aliases, sometimes doesn't, depending on incidental
	// capacity" bug class GR#20's stub-forever contract-first discipline
	// exists to keep out of the module registry itself, now the risk is
	// asked of.
	//
	// atomic.Pointer, not a plain *Registry field, for the SEC-016
	// ordering reason repeated at every SEC-020 site in this codebase
	// (see internal/engine/core/engine.go's self field for the full
	// writeup): a struct copy taken while the ORIGINAL's mu happened to
	// be held (RLock'd by an in-flight Get, or Lock'd by a concurrent
	// Register/SetFailoverHook) captures those mutex bytes read as
	// "locked" — the copy's own next Lock()/RLock() call on that
	// captured state can then park forever, since nothing will ever
	// Unlock() that specific copy's address. The identity check must
	// therefore be race-safe and run BEFORE mu is ever touched, not even
	// for RLock — a plain field read racing a concurrent struct copy has
	// no defined result under the Go memory model, but atomic.Pointer's
	// Load/Store do.
	self atomic.Pointer[Registry]
}

// NewRegistry constructs an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	r := &Registry{}
	// Stored exactly once, here, before r is returned to any caller — no
	// goroutine can have a reference to r to race this Store against
	// (SEC-020 wave 2; mirrors NewEngine/NewSubscriptionServer/
	// NewInProcTransport — see self's doc comment above).
	r.self.Store(r)
	return r
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Registry value (SEC-020 wave 2, mirroring
// Engine.checkNotCopied/SubscriptionServer.checkNotCopied/
// InProcTransport.checkNotCopied). Deliberately lock-free — a single
// atomic.Pointer.Load, requiring nothing else, not r.mu — so it is safe
// and correct to call BEFORE r.mu is EVER touched, including RLock. That
// ordering is not optional (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" (or "currently read-locked, N
// readers") if the copy was taken while the original's mu was held, and
// acquiring — even just attempting, even via RLock — a copy's own mu in
// that state can block forever, since nothing will ever Unlock()/RUnlock()
// that specific copy's address. A guard placed AFTER the lock can never
// run for that attack, because the attack IS acquiring the lock;
// rejecting the copy here, before Lock()/RLock() is ever called, means
// that hang path is never reached at all.
//
// A nil r.self.Load() (a Registry constructed as a bare `Registry{}` or
// `new(Registry)` rather than via NewRegistry, so self was never stored)
// is treated the same as a mismatch and rejected the same way — every
// documented construction path is NewRegistry, so an unset self is
// itself a misuse this same error correctly names, and rejecting it here
// also means such a value's nil regs slice is never reached either
// (harmless on its own for regs specifically — append(nil, ...) works —
// but the mu-locked-copy hang above is the load-bearing reason, not this
// one).
func (r *Registry) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(ErrRegistryCopied, correlationID, ctx)
	}
	return nil
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
//
// SEC-020 wave 2: identity-checked BEFORE r.mu is touched (pre-lock,
// load-bearing — see checkNotCopied's doc comment) and again immediately
// after r.mu is acquired (defence in depth, mirroring
// Engine.RegisterPhaseHook/seal's ordering). A rejected copy returns
// ErrRegistryCopied and mutates nothing — r.regs on a copy is never
// silently appended to, which would otherwise sometimes alias and
// sometimes diverge from the original depending on the backing array's
// capacity at copy time.
func (r *Registry) Register(name string, s Solver, priority int) error {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"backend": name}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"backend": name}); err != nil {
		return err
	}
	r.regs = append(r.regs, registration{name: name, solver: s, priority: priority, seq: r.nextSeq})
	r.nextSeq++
	return nil
}

// SetFailoverHook installs a callback invoked synchronously every time a
// chain solver returned by Get falls over from one backend to the next
// after an error. Pass nil to clear. Intended for tests and diagnostic
// logging; production simulation logic must not depend on call timing or
// treat the hook as anything but observational.
//
// SEC-020 wave 2: identity-checked BEFORE r.mu is touched and again
// after acquisition — see Register's doc comment for the identical
// rationale.
func (r *Registry) SetFailoverHook(fn func(FailoverEvent)) error {
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	r.onFailover = fn
	return nil
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
//
// SEC-020 wave 2: identity-checked BEFORE r.mu is touched, including
// before RLock (pre-lock, load-bearing) and again immediately after
// RLock is acquired (defence in depth) — see checkNotCopied's doc
// comment for why a copy's RLock must never be attempted, not only its
// Lock. A rejected copy returns ErrRegistryCopied rather than
// ErrNoFallback, so a caller can distinguish "no backend registered" from
// "you are holding a struct-copied Registry" — collapsing the two would
// make a real wiring bug (ErrNoFallback) indistinguishable from a misuse
// bug, which is exactly the kind of ambiguity GR#1 exists to prevent.
func (r *Registry) Get(problem ProblemKind) (Solver, error) {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"problem": problem.String()}); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"problem": problem.String()}); err != nil {
		return nil, err
	}

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
	if err := validateRequestPayload(req, errs.NewCorrelationID()); err != nil {
		return Response{}, err
	}

	var errList []error
	for i, cand := range c.candidates {
		resp, err := cand.solver.Solve(req)
		if err == nil {
			return resp, nil
		}

		wrapped := fmt.Errorf("solver backend %q failed: %w", cand.name, err)
		errList = append(errList, wrapped)

		hasNext := i < len(c.candidates)-1
		if hasNext && c.onFailover != nil {
			c.onFailover(FailoverEvent{Problem: c.problem, Backend: cand.name, Err: err})
		}
	}
	return Response{}, fmt.Errorf("solver: all %d backend(s) failed for problem %s: %w",
		len(c.candidates), c.problem, errors.Join(errList...))
}

// Default is the process-wide registry that backends register into at
// init, and that engine code resolves against via the package-level
// Register/Get functions below. Tests that need isolation from other
// tests' registrations should construct their own Registry with
// NewRegistry instead of using Default.
var Default = NewRegistry()

// Register adds a backend to the Default registry. See (*Registry).Register.
func Register(name string, s Solver, priority int) error {
	return Default.Register(name, s, priority)
}

// Get resolves the fallback-wrapped Solver for problem against the
// Default registry. See (*Registry).Get.
func Get(problem ProblemKind) (Solver, error) {
	return Default.Get(problem)
}
