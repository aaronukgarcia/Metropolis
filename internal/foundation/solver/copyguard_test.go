package solver

import (
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// registryByteCopy performs SEC-020's attack — a plain Registry struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/core/sec014_poc_test.go's e2Copy and
// internal/engine/debug/copyguard_test.go's stateByteCopy exactly (see
// either's doc comment for why this is the sanctioned TEST-ONLY
// mechanism): a literal `r2 := *r` is legal, unsafe-free, reflect-free Go
// from outside this package too, but it is also something `go vet`'s
// copylocks check statically flags — this package cannot contain that
// literal line and still pass `go vet ./...`, which this fix's VERIFY
// step requires. The byte-level copy produces IDENTICAL runtime
// semantics (mu's bytes copied as-is, regs' slice header copied —
// aliasing the same backing array — onFailover's func value copied,
// self's pointer bytes copied unchanged) without a statically-flaggable
// copy expression.
func registryByteCopy(r *Registry) *Registry {
	c := new(Registry)
	*(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(r))
	return c
}

// wantRegistryCopied asserts err is exactly ErrRegistryCopied — naming
// every guarded method individually means a stripped guard on any ONE of
// them identifies which site regressed, so each assertion is per-method,
// not a shared loop with a generic message.
func wantRegistryCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrRegistryCopied}) {
		t.Fatalf("%s on a struct-copied Registry: err = %v, want ErrRegistryCopied", method, err)
	}
}

// --- Enumeration method (Weakness pattern #3) -------------------------
//
// Every r.mu.Lock()/r.mu.RLock() site in this package's non-test source,
// found via:
//
//	grep -n "mu\.Lock()\|mu\.RLock()\|mu\.Unlock()\|mu\.RUnlock()" internal/foundation/solver/*.go
//
// cross-checked against the full exported-method list on *Registry:
//
//	grep -n "^func (r \*Registry)" internal/foundation/solver/*.go
//
// Results — every lock site, its enclosing method, and its guard:
//
//	registry.go  Register(name, s, priority)        -- r.mu.Lock()   -- pre-lock + post-lock check (returns ErrRegistryCopied)
//	registry.go  SetFailoverHook(fn)                  -- r.mu.Lock()   -- pre-lock + post-lock check (returns ErrRegistryCopied)
//	registry.go  Get(problem)                         -- r.mu.RLock()  -- pre-lock + post-lock check (returns ErrRegistryCopied)
//
// That is exactly 3 exported methods on *Registry, all 3 of which take
// r.mu (one write lock each for Register/SetFailoverHook, one read lock
// for Get) — no exported method reaches r.mu indirectly through another
// unguarded helper (chainSolver, returned by Get, holds its own
// candidates snapshot and never touches r.mu again once constructed, so
// it needs no guard of its own — it is not itself a Registry and is
// never registered into one). 3 guarded call sites total, all 3
// exercised below by name, including the RLock() site (Get) — a reader
// on a copy is just as broken as a writer, per the brief.
//
// Package-level Register/Get (the Default-registry convenience wrappers)
// are thin pass-throughs to (*Registry).Register/Get against the
// package-level Default var, which is never copied by this codebase — no
// separate guard needed for them; they inherit whatever *Registry.Get
// does.

// TestRegistry_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep: every method identified above is called on the SAME
// deterministically-constructed copy and asserted to reject it. Naming
// each call individually means a stripped guard on any one site fails
// ONLY that assertion, identifying the regressed site by name.
func TestRegistry_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	orig := NewRegistry()
	if err := orig.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
		t.Fatalf("setup Register: %v", err)
	}

	cp := registryByteCopy(orig)

	wantRegistryCopied(t, "Register", cp.Register("gpu.attack", NewCPUBackend(), 100))
	wantRegistryCopied(t, "SetFailoverHook", cp.SetFailoverHook(func(FailoverEvent) {}))
	_, err := cp.Get(EchoProblem)
	wantRegistryCopied(t, "Get", err)

	// The ORIGINAL must be completely unaffected by every rejected call
	// above: still exactly one registration, still resolvable, still
	// answering with the pre-attack backend.
	s, err := orig.Get(EchoProblem)
	if err != nil {
		t.Fatalf("original Get after copy-attack calls: %v", err)
	}
	resp, err := s.Solve(Request{Problem: EchoProblem, Payload: []byte("x")})
	if err != nil {
		t.Fatalf("original Solve after copy-attack calls: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("original Registry resolved backend %q after copy-attack calls, want cpu.v1 (must be unaffected)", resp.Backend)
	}
}

// TestRegistry_CopyRegisterNeverMutatesOriginal proves the aliasing
// hazard specifically: a copy's rejected Register call must not silently
// append to the shared backing array in a way that becomes visible from
// the original (which CAN happen with a plain slice-header copy whenever
// the original's slice still has spare capacity at copy time — appending
// through the copy would then write into the original's own backing
// array without even growing a new one). Register is rejected outright
// before ever reaching r.regs, so this must hold regardless of capacity
// headroom.
func TestRegistry_CopyRegisterNeverMutatesOriginal(t *testing.T) {
	orig := NewRegistry()
	// Register with spare capacity deliberately unknown/uncontrolled —
	// the guard must hold regardless, which is the point: this test does
	// not rely on forcing a specific append-aliasing scenario, only on
	// the copy's Register never reaching r.regs at all.
	if err := orig.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
		t.Fatalf("setup Register: %v", err)
	}

	cp := registryByteCopy(orig)
	wantRegistryCopied(t, "Register", cp.Register("gpu.attack", NewCPUBackend(), 100))

	s, err := orig.Get(EchoProblem)
	if err != nil {
		t.Fatalf("original Get: %v", err)
	}
	resp, err := s.Solve(Request{Problem: EchoProblem, Payload: []byte("y")})
	if err != nil {
		t.Fatalf("original Solve: %v", err)
	}
	if resp.Backend != "cpu.v1" {
		t.Fatalf("original Registry resolved backend %q, want cpu.v1 — the copy's rejected Register must never have reached the shared backing array", resp.Backend)
	}
}

// --- Deterministic pre-lock-ordering attack (SEC-016 shape), Lock AND RLock ---

// runBoundedRejection runs call in its own goroutine and asserts it
// returns within 3 seconds with ErrRegistryCopied, rather than asserting
// synchronously with no bound. A regression that reintroduces a pre-lock
// guard gap on a copy taken mid-lock hangs the guarded method forever
// (SEC-016's exact failure mode: the copy's mu bytes read as permanently
// "locked"/"read-locked" by nobody who will ever unlock THIS copy's
// address) — without a per-case bound, that regression is only caught by
// Go's default 10-minute test timeout and a goroutine-dump panic naming
// a stuck select, not the guarded method itself. Ported from
// internal/foundation/registry/sec020_test.go's identical pattern, this
// initiative's reference shape for exactly this class of test (Bill,
// 2026-08-10, citing Tester-1's reproduction) — a synchronous call in
// this position is a defect in the TEST, not just a style gap, since
// these tests exist to be re-run on every future change to this file and
// a check that needs a -timeout override to fail fast is a check people
// learn to skip.
func runBoundedRejection(t *testing.T, name string, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if !errors.Is(err, &errs.E{Code: ErrRegistryCopied}) {
			t.Errorf("%s on deterministically mid-lock-copied Registry: err = %v, want ErrRegistryCopied", name, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", name)
	}
}

// TestRegistry_CopyTakenWhileLockHeld_RejectedNotHung is the
// deterministic version of the SEC-016 "copy taken mid-lock" attack,
// exercised for BOTH a write-lock-held copy (Register/SetFailoverHook)
// and a read-lock-held copy (Get) — the brief explicitly calls out that
// a reader on a copy is just as broken as a writer. Rather than racing a
// concurrent locker against concurrent copies for the TIMING, each
// subtest CONSTRUCTS the attack STATE directly and deterministically,
// single-goroutine: lock/RLock r.mu, take the byte copy while it is held
// (so the copy's mu bytes read as "currently locked"/"read-locked" —
// byte-identical to the original's mu at that instant, and nobody will
// ever call Unlock()/RUnlock() with the copy's own address), unlock the
// original, then call the copy. Because checkNotCopied is lock-free and
// runs BEFORE mu.Lock()/mu.RLock() in every guarded method, the copy
// must be rejected promptly without ever attempting to acquire its own
// permanently-unrecoverable lock — asserted per case via
// runBoundedRejection's 3-second bound rather than synchronously with no
// bound.
func TestRegistry_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	t.Run("copy taken during Lock", func(t *testing.T) {
		orig := NewRegistry()

		orig.mu.Lock()
		cp := registryByteCopy(orig) // cp.mu's bytes now read "write-locked" — byte-identical to orig.mu at this instant
		orig.mu.Unlock()

		runBoundedRejection(t, "Register", func() error {
			return cp.Register("gpu.attack", NewCPUBackend(), 100)
		})
		runBoundedRejection(t, "SetFailoverHook", func() error {
			return cp.SetFailoverHook(func(FailoverEvent) {})
		})

		// The original must still be fully usable afterward — the copy
		// attack (and its abandoned, permanently-"locked"-looking mu)
		// must not have wedged anything shared.
		if err := orig.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
			t.Fatalf("original Register after copy-during-Lock attack: %v", err)
		}
	})

	// Nuance (Tester-1, 2026-08-10): stripping Get's pre-lock guard does
	// NOT hang on this subtest — a copy taken while the original holds a
	// READ lock does not block a subsequent RLock() (RWMutex readers
	// don't exclude each other), so this subtest proves rejection but
	// cannot stress the SEC-016 hang-ordering the way the Lock subtest
	// above does. The guard placement on Get is still correct (a reader
	// on a copy is just as broken as a writer, per the brief, and the
	// pre-lock check is still what makes this reject at all rather than
	// silently reading stale/aliased state) — this comment exists so a
	// future reader does not assume this subtest exercises the same
	// ordering hazard its sibling does.
	t.Run("copy taken during RLock", func(t *testing.T) {
		orig := NewRegistry()
		if err := orig.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
			t.Fatalf("setup Register: %v", err)
		}

		orig.mu.RLock()
		cp := registryByteCopy(orig) // cp.mu's bytes now read "read-locked" — byte-identical to orig.mu at this instant
		orig.mu.RUnlock()

		runBoundedRejection(t, "Get", func() error {
			_, err := cp.Get(EchoProblem)
			return err
		})

		// The original must still resolve correctly afterward.
		s, err := orig.Get(EchoProblem)
		if err != nil {
			t.Fatalf("original Get after RLock-copy attack: %v", err)
		}
		if !s.Supports(EchoProblem) {
			t.Fatal("original chain solver should support EchoProblem after RLock-copy attack")
		}
	})
}

// --- Fail-closed on zero value, new(...), and a hand-built literal ----

// TestRegistry_ZeroValue_FailsClosed proves `var r Registry` (never
// passed through NewRegistry, so self was never stored) is rejected the
// same way a copy is — every documented construction path is
// NewRegistry, so an unset self is itself a misuse this same rejection
// correctly names, rather than silently behaving like a valid, empty
// Registry (which the zero value would otherwise do perfectly happily,
// since a zero sync.RWMutex and a nil regs slice are both individually
// usable).
func TestRegistry_ZeroValue_FailsClosed(t *testing.T) {
	var r Registry

	wantRegistryCopied(t, "Register", r.Register("cpu.v1", NewCPUBackend(), 0))
	wantRegistryCopied(t, "SetFailoverHook", r.SetFailoverHook(func(FailoverEvent) {}))
	_, err := r.Get(EchoProblem)
	wantRegistryCopied(t, "Get", err)
}

// TestRegistry_NewRegistryPointer_ZeroValue_FailsClosed covers
// `new(Registry)` explicitly — same construction gap as the zero value,
// reached via a different spelling, per the brief's explicit "zero
// value, new(...), and a hand-built literal" requirement.
func TestRegistry_NewRegistryPointer_ZeroValue_FailsClosed(t *testing.T) {
	r := new(Registry)
	wantRegistryCopied(t, "Register", r.Register("cpu.v1", NewCPUBackend(), 0))
}

// TestRegistry_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with regs already
// populated exactly as if it were a fully-configured, legitimately used
// Registry — but `self` left unset. If checkNotCopied were ever
// accidentally skipped or short-circuited, Get would resolve normally
// here by coincidence (the data really is there); asserting rejection
// proves the guard is actually the thing producing the answer, not the
// data.
func TestRegistry_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	literal := &Registry{
		regs: []registration{{name: "cpu.v1", solver: NewCPUBackend(), priority: 0, seq: 0}},
	}

	_, err := literal.Get(EchoProblem)
	wantRegistryCopied(t, "Get", err)
	wantRegistryCopied(t, "Register", literal.Register("gpu.extra", NewCPUBackend(), 100))
	wantRegistryCopied(t, "SetFailoverHook", literal.SetFailoverHook(func(FailoverEvent) {}))
}

// --- A copy hit is still observable in the log ------------------------

// TestRegistry_CopyHit_IsLoggedNotSilent proves a rejected copy call
// still produces a registry-sourced ErrRegistryCopied log entry
// (errs.New's logging side effect) — so a copy-attack in production
// still leaves a trail an operator or the Destructive agent can find.
func TestRegistry_CopyHit_IsLoggedNotSilent(t *testing.T) {
	orig := NewRegistry()
	cp := registryByteCopy(orig)

	_, err := cp.Get(EchoProblem)
	wantRegistryCopied(t, "Get", err)

	found := false
	for _, e := range errs.Recent() {
		if e.Code == ErrRegistryCopied {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no ErrRegistryCopied entry found in errs.Recent() after a copy's Get() call — the fail-closed path must still be logged, not silent")
	}
}

// --- Sanity check: two independently constructed Registries never collide ---

// TestRegistry_SelfIdentity_FreshRegistriesAreDistinct is a sanity check
// that two independently constructed Registries never collide on
// identity (the non-attack path checkNotCopied must never reject).
func TestRegistry_SelfIdentity_FreshRegistriesAreDistinct(t *testing.T) {
	r1 := NewRegistry()
	r2 := NewRegistry()
	if err := r1.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
		t.Fatalf("r1.Register: %v", err)
	}
	if err := r2.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
		t.Fatalf("r2.Register: %v", err)
	}
	if _, err := r1.Get(EchoProblem); err != nil {
		t.Fatalf("r1.Get: %v", err)
	}
	if _, err := r2.Get(EchoProblem); err != nil {
		t.Fatalf("r2.Get: %v", err)
	}
}

// TestRegistry_ConcurrentCopyGuardUse hammers Register/SetFailoverHook/Get
// on the ORIGINAL concurrently with repeated copy-and-reject attempts,
// to be exercised under `go test -race`. It asserts only that every copy
// call resolves to ErrRegistryCopied (never a silent success, never a
// hang, never a race) and that the original keeps working throughout —
// it does not assert on ordering, matching this package's existing
// TestRegistryConcurrentUse's stated scope.
func TestRegistry_ConcurrentCopyGuardUse(t *testing.T) {
	orig := NewRegistry()
	if err := orig.Register("cpu.v1", NewCPUBackend(), 0); err != nil {
		t.Fatalf("setup Register: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var badResults int
	var resMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cp := registryByteCopy(orig)
				_, err := cp.Get(EchoProblem)
				if !errors.Is(err, &errs.E{Code: ErrRegistryCopied}) {
					resMu.Lock()
					badResults++
					resMu.Unlock()
				}
			}
		}
	}()

	for i := 0; i < 500; i++ {
		if _, err := orig.Get(EchoProblem); err != nil {
			t.Fatalf("original Get call %d: unexpected error %v", i, err)
		}
	}

	close(stop)
	wg.Wait()

	if badResults != 0 {
		t.Fatalf("%d copy Get() calls did not resolve to ErrRegistryCopied", badResults)
	}
}
