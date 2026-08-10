package debug

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// runBoundedSEC020 runs fn (a call into a guarded *Screen method, with
// its result captured by the caller's own closure) in its own goroutine
// and asserts it completes within 3 seconds, rather than calling it
// synchronously with no bound. A regression that reintroduces a pre-lock
// guard gap on a copy taken mid-lock hangs the guarded method forever
// (SEC-016's exact failure mode: the copy's mu bytes read as permanently
// "locked" by nobody who will ever unlock THIS copy's address) —
// without a per-case bound, that regression is only caught by Go's
// default 10-minute test timeout and a goroutine-dump panic naming a
// stuck select, not the guarded method itself.
//
// Ported from internal/foundation/registry/sec020_test.go's
// runBoundedRejection (itself ported from internal/engine/core's
// sec018_poc_test.go/sec019_poc_test.go), this initiative's reference
// shape for exactly this class of test — a synchronous call in this
// position is a defect in the TEST, not just a style gap, because these
// tests exist to be re-run by Testers and Destructive agents on every
// future change to this file, and a check that takes ten minutes (or
// needs a -timeout override) to fail is a check people learn to skip.
// Takes a bare func() rather than func() error (registry's shape)
// because Collect has no error return at all — the caller captures
// whatever result it needs into its own local via closure and asserts it
// after this returns.
func runBoundedSEC020(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", name)
	}
}

// --- Enumeration method (Weakness pattern #3) -------------------------
//
// Every s.mu.Lock() site in this package's non-test source, found via:
//
//	grep -n "mu\.\(Lock\|Unlock\)()" internal/ui/screens/debug/*.go
//
// cross-checked against the full exported-method list:
//
//	grep -n "^func (s \*Screen)" internal/ui/screens/debug/*.go
//
// Results — every exported method, its guard, and its fail-closed value:
//
//	screen.go  Collect()          -- pre-check + own mu.Lock() site, pre+post -- fails closed to Snapshot{} (DebugOn=false, so Render draws nothing — ASM-014)
//	screen.go  RequestToggle()    -- pre-check + TWO separate mu.Lock() sites, each pre+post checked -- propagates the checkNotCopied error
//	screen.go  LastToggleError()  -- pre-check + own mu.Lock() site, pre+post -- returns the checkNotCopied error itself (never a fabricated nil)
//	screen.go  TailEntry()        -- NOT guarded (see its own doc comment) -- reads zero *Screen fields, nothing for a copy to corrupt or leak
//
// recordToggleFailure (unexported, its own separate mu.Lock() site,
// reached only from within RequestToggle's own already-guarded flow) is
// ALSO guarded directly (screen.go), on the same "grep for the shape,
// not the instance" reasoning RegisterPhaseHook's defence-in-depth
// re-check uses (internal/engine/core/engine.go) — a future caller added
// ahead of a check must not silently inherit an unguarded path to mu.
//
// That is 4 exported methods (3 guarded, 1 deliberately not) plus 1
// guarded unexported lock site — 5 direct mu.Lock() sites total in this
// package's non-test source (Collect: 1, RequestToggle: 2,
// recordToggleFailure: 1, LastToggleError: 1), every guarded one
// exercised below by name.
//
// (5, not 4 or 6 — recount deliberately spelled out per Weakness pattern
// #3's audit-trail instruction, exactly as engine/debug's
// copyguard_test.go corrected an 8-vs-9 miscount the same way.)

// screenByteCopy performs SEC-020's attack — a plain Screen struct copy
// — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/debug/copyguard_test.go's stateByteCopy: a literal
// `s2 := *s` is legal, unsafe-free Go but is exactly what `go vet`'s
// copylocks check flags, and this package's VERIFY step requires
// `go vet ./...` clean. The byte-level copy produces IDENTICAL runtime
// semantics (mu's bytes copied as-is, reg/events's pointer/slice headers
// copied — aliasing the same registry/backing array — self's pointer
// bytes copied unchanged) without a statically flaggable copy
// expression.
func screenByteCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

// wantScreenCopied asserts err is exactly ErrScreenCopied — naming each
// call individually (rather than a shared loop) means a stripped guard
// on any ONE method identifies which site regressed.
func wantScreenCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrScreenCopied}) {
		t.Fatalf("%s on a struct-copied Screen: err = %v, want ErrScreenCopied", method, err)
	}
}

func newSEC020TestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	if err := reg.Register("crime", nil, sec020FakeModule{}, registry.WithCanToggle(true)); err != nil {
		t.Fatalf("register crime: %v", err)
	}
	return reg
}

type sec020FakeModule struct{}

func (sec020FakeModule) Name() string            { return "crime" }
func (sec020FakeModule) Version() string         { return "0.1.0" }
func (sec020FakeModule) Health() registry.Health { return registry.HealthOK }

// TestSEC020_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep above, exercised by name.
func TestSEC020_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	reg := newSEC020TestRegistry(t)
	orig := NewScreen(reg, "corr-setup", WithDebugFlag(func() bool { return true }))
	if err := orig.RequestToggle("crime", registry.StatusStub, "crime"); err != nil {
		t.Fatalf("setup RequestToggle: %v", err)
	}

	cp := screenByteCopy(orig)

	snap := cp.Collect()
	if snap.DebugOn {
		t.Fatalf("copy.Collect().DebugOn = true, want false (fail-closed zero Snapshot, ASM-014) even though the copied debugFlag func would return true")
	}
	if len(snap.Events) != 0 || snap.RegistryAvailable {
		t.Fatalf("copy.Collect() = %+v, want the zero Snapshot (must not read the aliased original's state)", snap)
	}

	wantScreenCopied(t, "RequestToggle", cp.RequestToggle("crime", registry.StatusReal, "crime"))
	wantScreenCopied(t, "LastToggleError", cp.LastToggleError())

	// The ORIGINAL must be completely unaffected by every rejected call
	// above.
	origSnap := orig.Collect()
	if !origSnap.DebugOn {
		t.Fatalf("original.Collect().DebugOn = false after copy-attack calls, want true (original must be unaffected)")
	}
	entry, ok := reg.Get("crime")
	if !ok || entry.Status != registry.StatusStub {
		t.Fatalf("registry crime status = %+v after copy-attack RequestToggle, want still StatusStub (copy must not have reached the real registry)", entry)
	}
}

// TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung is the deterministic
// SEC-016 "copy taken mid-lock" attack: lock mu, take the byte copy
// while it is held, unlock the original, then call the copy. Every
// guarded call below must return promptly (never attempt to acquire its
// own permanently unrecoverable lock) because checkNotCopied is
// lock-free and runs BEFORE mu.Lock() in every guarded method.
// Deterministic, single-goroutine, runs under
// `go test ./... -race -count=1` (v1.4's no-probabilistic-concurrency-
// tests rule).
func TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	reg := newSEC020TestRegistry(t)
	orig := NewScreen(reg, "corr-hung")

	orig.mu.Lock()
	cp := screenByteCopy(orig) // cp.mu's bytes now read "locked" -- byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	var snap Snapshot
	runBoundedSEC020(t, "Collect", func() { snap = cp.Collect() })
	if snap.DebugOn {
		t.Fatalf("Collect() on a copy taken mid-lock: DebugOn = true, want false")
	}

	var toggleErr error
	runBoundedSEC020(t, "RequestToggle", func() { toggleErr = cp.RequestToggle("crime", registry.StatusStub, "crime") })
	wantScreenCopied(t, "RequestToggle", toggleErr)

	var lastErr error
	runBoundedSEC020(t, "LastToggleError", func() { lastErr = cp.LastToggleError() })
	wantScreenCopied(t, "LastToggleError", lastErr)

	// The original must still be fully usable afterward -- the abandoned,
	// permanently-"locked"-looking copy mu must not have wedged anything shared.
	if err := orig.RequestToggle("crime", registry.StatusStub, "crime"); err != nil {
		t.Fatalf("original RequestToggle after copy-during-lock attack: %v", err)
	}
	entry, ok := reg.Get("crime")
	if !ok || entry.Status != registry.StatusStub {
		t.Fatalf("registry crime status = %+v after original's post-attack RequestToggle, want StatusStub", entry)
	}
}

// TestSEC020_ZeroValue_FailsClosed_NoMuTouch proves `var s Screen` (never
// passed through NewScreen, so self was never stored) is rejected the
// same way a copy is, and does so without hanging (a zero Mutex is
// actually perfectly lockable, so this case does not itself risk a
// hang, but it must still be rejected as a misuse — every documented
// construction path is NewScreen).
func TestSEC020_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var s Screen
	snap := s.Collect()
	if snap.DebugOn || len(snap.Events) != 0 {
		t.Fatalf("zero-value Screen.Collect() = %+v, want the zero Snapshot", snap)
	}
	wantScreenCopied(t, "RequestToggle", s.RequestToggle("crime", registry.StatusStub, "crime"))
	wantScreenCopied(t, "LastToggleError", s.LastToggleError())
}

// TestSEC020_NewScreen_ZeroPointer_FailsClosed covers `new(Screen)`
// explicitly — same construction gap, reached via a different spelling.
func TestSEC020_NewScreen_ZeroPointer_FailsClosed(t *testing.T) {
	s := new(Screen)
	wantScreenCopied(t, "RequestToggle", s.RequestToggle("crime", registry.StatusStub, "crime"))
	if snap := s.Collect(); snap.DebugOn {
		t.Fatalf("new(Screen).Collect().DebugOn = true, want false")
	}
}

// TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with every OTHER field
// populated exactly as if it were a legitimately-constructed,
// already-toggled Screen — a real registry, a debugFlag that returns
// true, a non-empty events log — but `self` left unset. If
// checkNotCopied were ever accidentally skipped, Collect().DebugOn would
// read true here by coincidence (the field really is true); asserting
// false proves the guard is actually the thing deciding the answer, not
// the data.
func TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	reg := newSEC020TestRegistry(t)
	literal := &Screen{
		correlationID: "corr-literal",
		reg:           reg,
		errorTailFunc: errs.Recent,
		debugFlag:     func() bool { return true },
		events:        []string{"crime module -> STUB"},
	}

	snap := literal.Collect()
	if snap.DebugOn {
		t.Fatalf("hand-built literal (self unset) Collect().DebugOn = true, want false even though debugFlag literally returns true -- the guard must be what decides this, not the data")
	}
	if len(snap.Events) != 0 {
		t.Fatalf("hand-built literal (self unset) Collect().Events = %v, want empty (must not read the aliased events slice)", snap.Events)
	}
	wantScreenCopied(t, "RequestToggle", literal.RequestToggle("crime", registry.StatusStub, "crime"))
}

// TestSEC020_CollectCopyHit_IsLoggedNotSilent proves ASM-014's "not
// silent to the SYSTEM, only to this one caller" claim: even though
// Collect's Snapshot return cannot carry the distinction to its caller,
// a copy hit still produces a registry-sourced ErrScreenCopied log entry
// (errs.New's logging side effect) — so a copy-attack in production
// still leaves a trail an operator or the Destructive agent can find,
// even on the one call site whose signature has nothing to return it
// through.
func TestSEC020_CollectCopyHit_IsLoggedNotSilent(t *testing.T) {
	reg := newSEC020TestRegistry(t)
	orig := NewScreen(reg, "corr-log")
	cp := screenByteCopy(orig)

	_ = cp.Collect()

	found := false
	for _, e := range errs.Recent() {
		if e.Code == ErrScreenCopied {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no ErrScreenCopied entry found in errs.Recent() after a copy's Collect() call -- the fail-closed path must still be logged, not silent")
	}
}
