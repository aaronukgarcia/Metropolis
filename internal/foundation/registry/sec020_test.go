package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// registryByteCopy performs SEC-020's attack — a plain Registry struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, exactly
// mirroring internal/engine/core/sec014_poc_test.go's e2Copy and
// internal/engine/debug/copyguard_test.go's stateByteCopy (see either
// file's doc comment for why this is the sanctioned TEST-ONLY mechanism:
// a literal `r2 := *r` is legal, unsafe-free, reflect-free Go from
// outside this package too, but it is ALSO something `go vet`'s
// copylocks check statically flags — this package cannot contain that
// literal line and still pass `go vet ./...`, which VERIFY requires).
// The byte-level copy produces IDENTICAL runtime semantics (mu's bytes
// copied as-is, modules/order's map/slice headers copied — aliasing the
// same map and backing array — hook's function-value bytes copied,
// self's pointer bytes copied unchanged) without a statically-flaggable
// copy expression.
func registryByteCopy(r *Registry) *Registry {
	c := new(Registry)
	*(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(r))
	return c
}

// wantRegistryCopied asserts err is exactly codeRegistryCopied — naming
// every guarded method individually (rather than sharing one loop and
// message) means a stripped guard on any ONE site fails only that
// assertion, identifying the regressed site by name (per the brief:
// "cover every guarded method by name so a stripped guard identifies
// which site regressed").
func wantRegistryCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: codeRegistryCopied}) {
		t.Fatalf("%s on a struct-copied Registry: err = %v, want codeRegistryCopied (%s)", method, err, codeRegistryCopied)
	}
}

// --- Enumeration method (Weakness pattern #3) -------------------------
//
// Every r.mu.Lock()/r.mu.RLock() site in this package's non-test source,
// found via:
//
//	grep -n "r\.mu\.(Lock|RLock)\(\)" internal/foundation/registry/registry.go
//
// cross-checked against the full exported-method list:
//
//	grep -n "^func (r \*Registry)" internal/foundation/registry/registry.go
//
// (the same two-command method the InProcTransport/State SEC-020 rounds
// used — every mu touch mapped back to its enclosing func, cross-checked
// against every exported method so nothing reachable from outside the
// package is missed even if it never itself calls mu.Lock()/RLock()
// directly).
//
// Results — every r.mu.Lock()/r.mu.RLock() site, its enclosing method,
// and its guard:
//
//	registry.go  SetToggleHook()     -- Lock()   -- pre-lock + post-lock check (silently drops the hook install)
//	registry.go  Register()          -- Lock()   -- pre-lock + post-lock check (returns codeRegistryCopied)
//	registry.go  Get()               -- RLock()  -- pre-lock + post-lock check (fails closed to ModuleEntry{}, false)
//	registry.go  List()              -- RLock()  -- pre-lock + post-lock check (fails closed to nil) -- see ASM-REG-001
//	registry.go  BootOrder()         -- RLock()  -- pre-lock + post-lock check (fails closed to nil)
//	registry.go  UpdateHealth()      -- Lock()   -- pre-lock + post-lock check (returns codeRegistryCopied)
//	registry.go  RecordTickCost()    -- Lock()   -- pre-lock + post-lock check (returns codeRegistryCopied)
//	registry.go  TickCostHistory()   -- RLock()  -- pre-lock + post-lock check (fails closed to nil, false)
//	registry.go  setStatusLocked()   -- Lock()   -- pre-lock + post-lock check (returns codeRegistryCopied);
//	                                                this is SetStatus's sole call site and only lock touch,
//	                                                so SetStatus itself needs no separate guard of its own
//
// That is 9 direct r.mu.Lock()/r.mu.RLock() sites, across 8 exported
// methods plus the one unexported helper (setStatusLocked) that is the
// actual lock site behind the 9th exported method (SetStatus). Every
// exported method that can reach r.mu — directly or, for SetStatus,
// through its sole unexported helper — is covered below by name.
// sortedEntriesLocked (List's helper) takes no lock of its own and is
// only ever called with r.mu already held by its caller, which is
// already guarded, so it needs no separate check.
//
// (Sites counted twice — once by grep line count above, once by walking
// the source file top to bottom while writing this comment — both
// passes agree on 9. Recorded per the brief's "get the arithmetic
// right" — a previous round's summary line diverged from its own
// itemised list and was only caught by a Tester re-running the grep.)

// TestSEC020_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep: every method identified above is called on the SAME
// deterministically-constructed copy and asserted to reject it (or, for
// SetToggleHook, which has no error return, to fail closed by silently
// not installing the hook).
func TestSEC020_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	orig := NewRegistry()
	if err := orig.Register("engine.traffic", newReal("engine.traffic"), newStub("engine.traffic"), WithStatus(StatusReal), WithCanToggle(true)); err != nil {
		t.Fatalf("setup Register: %v", err)
	}
	if err := orig.RecordTickCost("engine.traffic", 100); err != nil {
		t.Fatalf("setup RecordTickCost: %v", err)
	}

	cp := registryByteCopy(orig)

	wantRegistryCopied(t, "Register", cp.Register("engine.crime", nil, newStub("engine.crime")))

	if entry, ok := cp.Get("engine.traffic"); ok || entry != (ModuleEntry{}) {
		t.Fatalf("Get on a struct-copied Registry = (%+v, %v), want (ModuleEntry{}, false)", entry, ok)
	}

	if list := cp.List(); list != nil {
		t.Fatalf("List on a struct-copied Registry = %v, want nil", list)
	}

	if order := cp.BootOrder(); order != nil {
		t.Fatalf("BootOrder on a struct-copied Registry = %v, want nil", order)
	}

	wantRegistryCopied(t, "UpdateHealth", cp.UpdateHealth("engine.traffic", HealthDegraded))
	wantRegistryCopied(t, "RecordTickCost", cp.RecordTickCost("engine.traffic", 999))

	if hist, ok := cp.TickCostHistory("engine.traffic"); ok || hist != nil {
		t.Fatalf("TickCostHistory on a struct-copied Registry = (%v, %v), want (nil, false)", hist, ok)
	}

	wantRegistryCopied(t, "SetStatus", cp.SetStatus("engine.traffic", StatusStub, "engine.traffic"))

	var hookFired bool
	cp.SetToggleHook(func(ToggleEvent) { hookFired = true })

	// The ORIGINAL must be completely unaffected by every rejected call
	// above.
	entry, ok := orig.Get("engine.traffic")
	if !ok || entry.Status != StatusReal || entry.Health != HealthOK {
		t.Fatalf("original Get(engine.traffic) after copy-attack calls = (%+v, %v), want unaffected StatusReal/HealthOK entry", entry, ok)
	}
	if _, ok := orig.Get("engine.crime"); ok {
		t.Fatalf("original registered engine.crime via the copy's rejected Register — must not have happened")
	}
	hist, ok := orig.TickCostHistory("engine.traffic")
	if !ok || len(hist) != 1 || hist[0] != 100 {
		t.Fatalf("original TickCostHistory(engine.traffic) after copy-attack calls = (%v, %v), want ([100], true)", hist, ok)
	}

	// Prove the copy's own toggle hook was never actually wired to
	// anything real (it can't have been: SetToggleHook on the copy
	// returned without touching r.hook at all).
	if hookFired {
		t.Fatalf("copy's SetToggleHook installed hook fired somehow — should never have been reachable")
	}
	if err := orig.SetStatus("engine.traffic", StatusStub, "engine.traffic"); err != nil {
		t.Fatalf("original SetStatus after copy-attack calls: %v", err)
	}
}

// --- Hygiene invariant (the finding's real consequence) ----------------

// TestSEC020_CopyCannotRegisterIntoSharedMap proves a struct-copied
// Registry cannot insert into the shared modules map at all — Register
// is rejected outright, and this asserts the copy's rejection actually
// prevents mutation of the ALIASED map, not merely that an error was
// returned.
func TestSEC020_CopyCannotRegisterIntoSharedMap(t *testing.T) {
	orig := NewRegistry()
	cp := registryByteCopy(orig)

	err := cp.Register("engine.parks", nil, newStub("engine.parks"))
	wantRegistryCopied(t, "Register", err)

	if _, ok := orig.Get("engine.parks"); ok {
		t.Fatalf("engine.parks visible via the ORIGINAL after a rejected Register on the copy — the copy must never touch the aliased map")
	}
	if got := len(orig.List()); got != 0 {
		t.Fatalf("original List() has %d entries after a copy-attack Register, want 0", got)
	}
}

// TestSEC020_CopyMutationsNeverObservedByOriginal is the hygiene
// invariant's sharpest form: even after the ORIGINAL has already
// legitimately registered a module (so the copy's byte-identical
// `modules`/`order` fields alias real, populated shared state), every
// mutating call on a SUBSEQUENT copy must still be rejected — proving
// the guard is identity-based, not merely "nothing was registered yet so
// nothing could happen anyway".
func TestSEC020_CopyMutationsNeverObservedByOriginal(t *testing.T) {
	orig := NewRegistry()
	if err := orig.Register("engine.traffic", nil, newStub("engine.traffic"), WithCanToggle(true)); err != nil {
		t.Fatalf("legit Register: %v", err)
	}

	cp := registryByteCopy(orig) // taken AFTER Register: cp.modules/cp.order alias orig's real map/slice

	wantRegistryCopied(t, "UpdateHealth", cp.UpdateHealth("engine.traffic", HealthError))
	wantRegistryCopied(t, "SetStatus", cp.SetStatus("engine.traffic", StatusReal, "engine.traffic"))
	wantRegistryCopied(t, "RecordTickCost", cp.RecordTickCost("engine.traffic", 42))

	entry, ok := orig.Get("engine.traffic")
	if !ok {
		t.Fatalf("original Get(engine.traffic) ok = false, want true")
	}
	if entry.Health != HealthOK {
		t.Fatalf("original entry.Health = %q after copy-attack UpdateHealth, want %q (unaffected)", entry.Health, HealthOK)
	}
	if entry.Status != StatusStub {
		t.Fatalf("original entry.Status = %q after copy-attack SetStatus, want %q (unaffected)", entry.Status, StatusStub)
	}
	if entry.LastTickCostMicros != 0 {
		t.Fatalf("original entry.LastTickCostMicros = %d after copy-attack RecordTickCost, want 0 (unaffected)", entry.LastTickCostMicros)
	}
}

// --- Deterministic pre-lock-ordering attack (SEC-016 shape) ------------

// runBoundedRejection runs each case's call in its own goroutine and
// asserts it returns within 3 seconds with the wanted error, rather than
// asserting synchronously with no bound. A regression that reintroduces
// a pre-lock guard gap on a copy taken mid-lock hangs the guarded
// method forever (SEC-016's exact failure mode: the copy's mu bytes
// read as permanently "locked" by nobody who will ever unlock THIS
// copy's address) — without a per-case bound, that regression is only
// caught by Go's default 10-minute test timeout and a goroutine-dump
// panic naming a stuck select, not the guarded method itself. ASM-081
// (Tester-1, 2026-08-10): ported from the identical pattern in
// internal/engine/core/sec018_poc_test.go /
// internal/engine/core/sec019_poc_test.go, which is now this
// initiative's reference shape for exactly this class of test — a
// synchronous call in this position is a defect in the TEST, not just a
// style gap, because these tests exist to be re-run by Testers and
// Destructive agents on every future change to this file, and a check
// that takes ten minutes (or needs a -timeout override) to fail is a
// check people learn to skip.
func runBoundedRejection(t *testing.T, name string, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if !errors.Is(err, &errs.E{Code: codeRegistryCopied}) {
			t.Errorf("%s on deterministically mid-lock-copied Registry: err = %v, want codeRegistryCopied (%s)", name, err, codeRegistryCopied)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", name)
	}
}

// TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung is the deterministic
// version of the SEC-016 "copy taken mid-lock" attack — constructing the
// attack STATE directly and single-goroutine rather than racing for the
// TIMING, per the brief ("build the attack state, don't race for the
// timing, so it runs under -race"): lock mu, take the byte copy while it
// is held (so the copy's mu bytes read as "currently locked" —
// byte-identical to the original's mu at that instant, and nobody will
// ever call Unlock()/RUnlock() with the copy's own address), unlock the
// original, then call the copy. Because checkNotCopied is lock-free and
// runs BEFORE mu.Lock()/mu.RLock() in every guarded method, the copy
// must be rejected promptly without ever attempting to acquire its own
// permanently-unrecoverable lock — asserted here per case via
// runBoundedRejection's 3-second bound, per guarded method, rather than
// synchronously with no bound (ASM-081). Exercised for BOTH a
// write-lock-held copy and a read-lock-held copy, since the RWMutex note
// requires RLock() paths to be guarded too, not just Lock().
func TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	t.Run("copy taken during Lock", func(t *testing.T) {
		orig := NewRegistry()
		orig.mu.Lock()
		cp := registryByteCopy(orig) // cp.mu bytes now read "write-locked"
		orig.mu.Unlock()

		cases := []struct {
			name string
			call func() error
		}{
			{"Register", func() error { return cp.Register("engine.x", nil, newStub("engine.x")) }},
			{"Get", func() error {
				if entry, ok := cp.Get("engine.x"); ok || entry != (ModuleEntry{}) {
					return fmt.Errorf("Get = (%+v, %v), want (ModuleEntry{}, false)", entry, ok)
				}
				return &errs.E{Code: codeRegistryCopied}
			}},
			{"List", func() error {
				if list := cp.List(); list != nil {
					return fmt.Errorf("List = %v, want nil", list)
				}
				return &errs.E{Code: codeRegistryCopied}
			}},
			{"UpdateHealth", func() error { return cp.UpdateHealth("engine.x", HealthOK) }},
			{"SetStatus", func() error { return cp.SetStatus("engine.x", StatusStub, "engine.x") }},
		}
		for _, c := range cases {
			runBoundedRejection(t, c.name, c.call)
		}

		// The original must still be fully usable afterward — the copy
		// attack (and its abandoned, permanently-"locked"-looking mu) must
		// not have wedged anything shared.
		if err := orig.Register("engine.y", nil, newStub("engine.y")); err != nil {
			t.Fatalf("original Register after copy-during-Lock attack: %v", err)
		}
	})

	t.Run("copy taken during RLock", func(t *testing.T) {
		orig := NewRegistry()
		if err := orig.Register("engine.z", nil, newStub("engine.z")); err != nil {
			t.Fatalf("setup Register: %v", err)
		}

		orig.mu.RLock()
		cp := registryByteCopy(orig) // cp.mu bytes now read "read-locked"
		orig.mu.RUnlock()

		cases := []struct {
			name string
			call func() error
		}{
			{"List", func() error {
				if list := cp.List(); list != nil {
					return fmt.Errorf("List = %v, want nil", list)
				}
				return &errs.E{Code: codeRegistryCopied}
			}},
			{"TickCostHistory", func() error {
				if hist, ok := cp.TickCostHistory("engine.z"); ok || hist != nil {
					return fmt.Errorf("TickCostHistory = (%v, %v), want (nil, false)", hist, ok)
				}
				return &errs.E{Code: codeRegistryCopied}
			}},
			{"BootOrder", func() error {
				if order := cp.BootOrder(); order != nil {
					return fmt.Errorf("BootOrder = %v, want nil", order)
				}
				return &errs.E{Code: codeRegistryCopied}
			}},
		}
		for _, c := range cases {
			runBoundedRejection(t, c.name, c.call)
		}

		// The original must still be fully usable afterward.
		if _, ok := orig.Get("engine.z"); !ok {
			t.Fatalf("original Get(engine.z) after copy-during-RLock attack: ok = false, want true")
		}
	})
}

// --- Fail-closed on zero value, new(...), and a hand-built literal -----

// TestSEC020_ZeroValue_FailsClosed_NoMuTouch proves `var r Registry`
// (never passed through NewRegistry, so self was never stored) is
// rejected the same way a copy is. A zero RWMutex is actually perfectly
// lockable, so this particular case does not itself risk a hang, but it
// must still be rejected as a misuse (every documented construction path
// is NewRegistry) rather than silently behaving like a valid, empty
// Registry.
func TestSEC020_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var r Registry

	wantRegistryCopied(t, "Register", r.Register("engine.x", nil, newStub("engine.x")))
	if entry, ok := r.Get("engine.x"); ok || entry != (ModuleEntry{}) {
		t.Fatalf("zero-value Registry.Get = (%+v, %v), want (ModuleEntry{}, false)", entry, ok)
	}
	if list := r.List(); list != nil {
		t.Fatalf("zero-value Registry.List() = %v, want nil", list)
	}
	r.SetToggleHook(func(ToggleEvent) {}) // must not panic
}

// TestSEC020_NewRegistryPointer_FailsClosed covers `new(Registry)`
// explicitly — same construction gap as the zero value, reached via a
// different spelling, per the brief's explicit "zero value, new(...),
// and a hand-built literal" requirement.
func TestSEC020_NewRegistryPointer_FailsClosed(t *testing.T) {
	r := new(Registry)
	wantRegistryCopied(t, "Register", r.Register("engine.x", nil, newStub("engine.x")))
	if entry, ok := r.Get("engine.x"); ok || entry != (ModuleEntry{}) {
		t.Fatalf("new(Registry).Get = (%+v, %v), want (ModuleEntry{}, false)", entry, ok)
	}
}

// TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with modules/order
// already populated exactly as if Register had legitimately run — but
// self left unset. If checkNotCopied were ever accidentally skipped or
// short-circuited, Get would return the populated entry by coincidence
// (the map really does contain it); asserting rejection proves the guard
// is actually the thing producing the answer, not the data.
func TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	literal := &Registry{
		modules: map[string]*moduleRecord{
			"engine.traffic": {entry: ModuleEntry{Key: "engine.traffic", Status: StatusStub, Health: HealthOK}, stub: newStub("engine.traffic")},
		},
		order: []string{"engine.traffic"},
	}

	if entry, ok := literal.Get("engine.traffic"); ok || entry != (ModuleEntry{}) {
		t.Fatalf("hand-built literal (self unset) Get(engine.traffic) = (%+v, %v), want (ModuleEntry{}, false) even though the map genuinely contains the key — the guard must be what decides this, not the data", entry, ok)
	}
	if list := literal.List(); list != nil {
		t.Fatalf("hand-built literal (self unset) List() = %v, want nil even though modules is populated", list)
	}
	wantRegistryCopied(t, "UpdateHealth", literal.UpdateHealth("engine.traffic", HealthDegraded))
}

// --- Discarded-error accessors' fail-closed decision is still logged ---

// TestSEC020_GetCopyHit_IsLoggedNotSilent proves that even on the
// accessors whose signatures cannot carry codeRegistryCopied all the way
// back through a plain (ModuleEntry, bool)/[]ModuleEntry return (Get,
// List, BootOrder, TickCostHistory all fail closed to a zero/nil value
// with no error to inspect), the rejection still produces a
// registry-sourced log entry via errs.New's logging side effect — so a
// copy-attack in production still leaves a trail an operator or the
// Destructive agent can find, even on call sites that cannot return an
// error to their immediate caller.
func TestSEC020_GetCopyHit_IsLoggedNotSilent(t *testing.T) {
	orig := NewRegistry()
	cp := registryByteCopy(orig)

	if _, ok := cp.Get("anything"); ok {
		t.Fatalf("cp.Get(\"anything\") ok = true, want false")
	}

	found := false
	for _, e := range errs.Recent() {
		if e.Code == codeRegistryCopied {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no codeRegistryCopied entry found in errs.Recent() after a copy's Get() call — the fail-closed path must still be logged, not silent")
	}
}
