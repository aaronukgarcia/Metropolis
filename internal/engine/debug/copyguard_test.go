package debug

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// stateByteCopy performs SEC-020's attack — a plain State struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, exactly
// mirroring internal/engine/core/sec014_poc_test.go's e2Copy (see that
// file's doc comment for why this is the sanctioned TEST-ONLY mechanism:
// a literal `s2 := *s` is legal, unsafe-free, reflect-free Go from
// outside this package too, but it is also something `go vet`'s
// copylocks check statically flags — this package cannot contain that
// literal line and still pass `go vet ./...`, which this fix's VERIFY
// step requires. The byte-level copy produces IDENTICAL runtime
// semantics (mu's bytes copied as-is, header/cheatLog's pointer/slice
// headers copied — aliasing the same header and backing array — self's
// pointer bytes copied unchanged) without a statically-flaggable copy
// expression.
func stateByteCopy(s *State) *State {
	c := new(State)
	*(*[unsafe.Sizeof(State{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(State{})]byte)(unsafe.Pointer(s))
	return c
}

// wantStateCopied asserts err is exactly ErrStateCopied — the whole
// point of naming every guarded method individually is that a stripped
// guard on any ONE of them identifies which site regressed, so each
// assertion below is per-method, not a shared loop with a generic
// message.
func wantStateCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrStateCopied}) {
		t.Fatalf("%s on a struct-copied State: err = %v, want ErrStateCopied", method, err)
	}
}

// --- Enumeration method (Weakness pattern #3) -----------------------------
//
// Every s.mu.Lock() site in this package's non-test source, found via:
//
//	grep -n "mu\.Lock()\|mu\.Unlock()" internal/engine/debug/*.go
//
// cross-checked against the full exported-method list:
//
//	grep -n "^func (s \*State)" internal/engine/debug/*.go
//
// (the same two-command method SEC-018/InProcTransport used — an awk
// pass mapping each Lock() back to its enclosing func, cross-checked
// against every exported method so nothing reachable from outside the
// package is missed even if it never itself calls mu.Lock() directly).
//
// Results — every s.mu.Lock() site, its enclosing method, and its guard:
//
//	state.go   IsOn()               -- pre-lock + post-lock check (fails closed to false; ASM-002)
//	state.go   Enable()             -- pre-lock + post-lock check (returns ErrStateCopied)
//	state.go   Disable()            -- pre-lock + post-lock check (silently drops; no error return to carry one)
//	state.go   nowFunc()            -- pre-lock + post-lock check (fails closed to zeroClock())
//	cheats.go  recordCheatUsed()    -- pre-lock + post-lock check (silently drops the append)
//	cheats.go  CheatLog()           -- pre-lock + post-lock check (returns empty slice)
//	inspector.go InspectEntity()    -- requireOn() check, THEN its own separate pre/post-lock check
//	                                    over its own separate mu.Lock() site (entityLookup)
//	fidelity.go  FidelityDial()     -- requireOn() check, THEN its own separate pre/post-lock check
//	                                    over its own separate mu.Lock() site (fidelityDial)
//
// Every OTHER exported method on *State touches mu only indirectly, via
// requireOn (which itself now checks, in addition to IsOn's own internal
// check) — covered without a direct mu.Lock() of their own:
//
//	cheats.go  InvokeCheat()   -- calls requireOn() first, before effect()/nowFunc()/recordCheatUsed()
//	gates.go   AllowSpeed8x()  -- calls requireOn() only
//	gates.go   RequireConsole()          -- calls requireOn() only
//	gates.go   RequireFixtureControls()  -- calls requireOn() only
//
// That is 8 direct mu.Lock() sites (4 in state.go, 2 in cheats.go, 1
// each in inspector.go/fidelity.go) and 4 requireOn-only methods — 12
// exported/relevant methods total, every one exercised below by name.
//
// (Counts corrected 8/12 from an original 9/13 — the itemised breakdown
// above was always right, the summary line's arithmetic was not. Caught
// by a Tester re-running the grep rather than reading the total. This
// comment IS the audit trail for the next person auditing this package,
// so a wrong number in it is a real defect even when every real site is
// genuinely guarded — which, verified independently, they are.)

// TestSEC020_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep: every method identified above is called on the SAME
// deterministically-constructed copy and asserted to reject it (or, for
// the three methods with no error return — IsOn, Disable, nowFunc — to
// fail closed to their documented safe value). Naming each call
// individually means a stripped guard on any one site fails ONLY that
// assertion, identifying the regressed site by name.
func TestSEC020_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	h := newTestHeader()
	orig := NewState(
		WithHeader(h),
		WithEntityLookup(func(ref string) (any, error) { return map[string]string{"ref": ref}, nil }),
		WithFidelityDial(&fakeFidelityDial{radius: 5}),
	)
	if err := orig.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("setup Enable: %v", err)
	}

	cp := stateByteCopy(orig)

	// state.go
	if got := cp.IsOn(); got != false {
		t.Fatalf("IsOn() on a struct-copied State = %v, want false (fail-closed, ASM-002) even though the copied `on` field is true", got)
	}
	wantStateCopied(t, "Enable", cp.Enable(SourcePalette, "corr-1"))
	cp.Disable() // no return value; hygiene-invariant test below proves this is a genuine no-op, not merely "didn't panic"
	if got := cp.nowFunc(); !got.IsZero() {
		t.Fatalf("nowFunc() on a struct-copied State = %v, want zero time.Time (fail-closed)", got)
	}

	// cheats.go
	wantStateCopied(t, "InvokeCheat", cp.InvokeCheat("corr-2", CheatFreeMoney, nil, func() error { return nil }))
	if log := cp.CheatLog(); len(log) != 0 {
		t.Fatalf("CheatLog() on a struct-copied State = %d entries, want 0 (must not read the aliased original's log)", len(log))
	}

	// gates.go (requireOn-only)
	wantStateCopied(t, "AllowSpeed8x", cp.AllowSpeed8x("corr-3"))
	wantStateCopied(t, "RequireConsole", cp.RequireConsole("corr-4"))
	wantStateCopied(t, "RequireFixtureControls", cp.RequireFixtureControls("corr-5"))

	// inspector.go / fidelity.go
	if _, err := cp.InspectEntity("corr-6", "citizen:1"); !errors.Is(err, &errs.E{Code: ErrStateCopied}) {
		t.Fatalf("InspectEntity on a struct-copied State: err = %v, want ErrStateCopied", err)
	}
	if _, err := cp.FidelityDial("corr-7"); !errors.Is(err, &errs.E{Code: ErrStateCopied}) {
		t.Fatalf("FidelityDial on a struct-copied State: err = %v, want ErrStateCopied", err)
	}

	// The ORIGINAL must be completely unaffected by every rejected call
	// above.
	if !orig.IsOn() {
		t.Fatalf("original State.IsOn() = false after copy-attack calls, want true (original must be unaffected)")
	}
	if !h.DebugTouched() {
		t.Fatalf("original header.DebugTouched = false after copy-attack calls, want true (sticky, must survive)")
	}
}

// --- Hygiene invariant (the finding's real consequence) --------------------

// TestSEC020_CopyCannotEnableDebug proves a struct-copied State cannot
// itself become "on": Enable is rejected outright, and — unlike a
// generic rejection check — this asserts the copy's OWN on-field (which,
// being a plain bool, the byte-copy DID duplicate as a value) never
// becomes observably true via IsOn(), which fails closed regardless.
func TestSEC020_CopyCannotEnableDebug(t *testing.T) {
	h := newTestHeader()
	orig := NewState(WithHeader(h))
	cp := stateByteCopy(orig)

	err := cp.Enable(SourceFlag, "corr-attack")
	wantStateCopied(t, "Enable", err)
	if cp.IsOn() {
		t.Fatalf("copy.IsOn() = true after a rejected Enable, want false")
	}
	if h.DebugTouched() {
		t.Fatalf("header.DebugTouched = true after a copy's rejected Enable, want false — the copy must never be able to sticky-flag the header")
	}
	if orig.IsOn() {
		t.Fatalf("original.IsOn() = true after only the COPY's Enable was called, want false")
	}
}

// TestSEC020_CopyCannotTouchHeaderViaEnable is the hygiene invariant's
// sharpest form: even after the ORIGINAL has already legitimately
// enabled debug (so header.DebugTouched is already true and both the
// original's and the copy's `on` bool read true, since the byte-copy
// happened after Enable), a SUBSEQUENT copy taken at that point must
// still be rejected by every mutating call — proving the guard is
// identity-based, not merely "debug was off so nothing could happen
// anyway".
func TestSEC020_CopyCannotTouchHeaderViaEnable(t *testing.T) {
	h := newTestHeader()
	orig := NewState(WithHeader(h))
	if err := orig.Enable(SourceFlag, "corr-legit"); err != nil {
		t.Fatalf("legit Enable: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatalf("setup: header.DebugTouched = false after a legit Enable, want true")
	}

	cp := stateByteCopy(orig) // taken AFTER Enable: cp.on is byte-true, cp.header aliases h

	// A re-Enable via the copy (e.g. a different source) must still be
	// rejected — it must not be treated as "already on, harmless re-touch"
	// just because the copied `on` bool happens to already read true.
	wantStateCopied(t, "Enable", cp.Enable(SourcePalette, "corr-attack"))

	// The real test: cp.Disable() must NOT be able to clear
	// header.DebugTouched (it never legitimately could — Disable never
	// clears the sticky flag even on the original, see state.go's
	// Disable doc comment — but a copy must additionally be unable to
	// reach h at all through its own independent mu).
	cp.Disable()
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched = false after a copy's Disable, want true (sticky; must survive even a copy-identity attack)")
	}

	// And the ORIGINAL's own on-state must be completely unaffected by
	// anything done to the copy.
	if !orig.IsOn() {
		t.Fatalf("original.IsOn() = false after copy-attack calls, want true — the original must be unaffected by anything done to a copy")
	}
}

// TestSEC020_CopyCheatLogNeverAliasesOriginal proves a copy cannot
// observe (via CheatLog) or extend the original's audit trail — the
// balance-data-hygiene consequence extended to the cheat-usage log,
// which AC-6 requires to be visible and accurate.
func TestSEC020_CopyCheatLogNeverAliasesOriginal(t *testing.T) {
	orig := NewState(WithHeader(newTestHeader()))
	if err := orig.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := orig.InvokeCheat("corr-real", CheatFreeMoney, nil, func() error { return nil }); err != nil {
		t.Fatalf("InvokeCheat on original: %v", err)
	}
	if got := len(orig.CheatLog()); got != 1 {
		t.Fatalf("original CheatLog() has %d entries, want 1", got)
	}

	cp := stateByteCopy(orig)
	if err := cp.InvokeCheat("corr-attack", CheatInstantBuild, nil, func() error { return nil }); !errors.Is(err, &errs.E{Code: ErrStateCopied}) {
		t.Fatalf("InvokeCheat on copy: err = %v, want ErrStateCopied", err)
	}
	if got := len(cp.CheatLog()); got != 0 {
		t.Fatalf("copy CheatLog() has %d entries, want 0 (must not read the aliased original's log as its own)", got)
	}
	if got := len(orig.CheatLog()); got != 1 {
		t.Fatalf("original CheatLog() has %d entries after a copy-attack, want still 1 (unaffected)", got)
	}
}

// --- Deterministic pre-lock-ordering attack (SEC-016 shape) -----------------

// TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung is the
// deterministic version of the SEC-016 "copy taken mid-lock" attack.
// Rather than racing a concurrent locker against concurrent copies for
// the TIMING (as sec016_poc_test.go's Engine equivalent does, under a
// documented !race build tag because that attack IS a data race by
// construction), this test CONSTRUCTS the attack STATE directly and
// deterministically, single-goroutine: lock mu, take the byte copy while
// it is held (so the copy's mu bytes read as "currently locked, no
// waiters" — nobody will ever call Unlock() with the copy's address),
// unlock the original, then call the copy. Because checkNotCopied is
// lock-free and runs BEFORE mu.Lock() in every guarded method, the copy
// must be rejected promptly without ever attempting to acquire its own
// (permanently unrecoverable) lock — this is a correctness assertion
// runnable under `go test ./... -race -count=1` like everything else in
// this package, per the no-probabilistic-concurrency-tests rule (v1.4).
func TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	orig := NewState(WithHeader(newTestHeader()))

	orig.mu.Lock()
	cp := stateByteCopy(orig) // cp.mu's bytes now read "locked" — byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	// Every one of these would hang forever (SEC-016's exact failure
	// mode) if checkNotCopied ran after mu.Lock() instead of before it.
	// Each call below is expected to return promptly (the test's own
	// timeout bounds the whole run) with ErrStateCopied / the documented
	// fail-closed value, never block.
	wantStateCopied(t, "Enable", cp.Enable(SourceFlag, "corr-hung-1"))
	if got := cp.IsOn(); got != false {
		t.Fatalf("IsOn() on a copy taken mid-lock = %v, want false", got)
	}
	cp.Disable()
	if got := cp.nowFunc(); !got.IsZero() {
		t.Fatalf("nowFunc() on a copy taken mid-lock = %v, want zero time.Time", got)
	}
	wantStateCopied(t, "InvokeCheat", cp.InvokeCheat("corr-hung-2", CheatFreeMoney, nil, func() error { return nil }))
	if log := cp.CheatLog(); len(log) != 0 {
		t.Fatalf("CheatLog() on a copy taken mid-lock = %d entries, want 0", len(log))
	}

	// The original must still be fully usable afterward — the copy
	// attack (and its abandoned, permanently-"locked"-looking mu) must
	// not have wedged anything shared.
	if err := orig.Enable(SourceFlag, "corr-original-still-works"); err != nil {
		t.Fatalf("original Enable after copy-during-lock attack: %v", err)
	}
	if !orig.IsOn() {
		t.Fatalf("original.IsOn() = false after re-Enable, want true")
	}
}

// --- Fail-closed on zero value, new(...), and a hand-built literal --------

// TestSEC020_ZeroValue_FailsClosed_NoMuTouch proves `var s State` (never
// passed through NewState, so self was never stored) is rejected the
// same way a copy is, and — because the identity check runs before mu —
// does so without hanging: a zero Mutex is actually perfectly lockable,
// so this case does not itself risk a hang, but it must still be
// rejected as a misuse (every documented construction path is NewState)
// rather than silently behaving like a valid, permanently-off State.
func TestSEC020_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var s State

	if got := s.IsOn(); got != false {
		t.Fatalf("zero-value State.IsOn() = %v, want false", got)
	}
	wantStateCopied(t, "Enable", s.Enable(SourceFlag, "corr-zero"))
	wantStateCopied(t, "AllowSpeed8x", s.AllowSpeed8x("corr-zero-2"))
	if got := s.nowFunc(); !got.IsZero() {
		t.Fatalf("zero-value State.nowFunc() = %v, want zero time.Time", got)
	}
	s.Disable() // must not panic
}

// TestSEC020_NewState_Pointer_FailsClosed covers `new(State)` explicitly
// — same construction gap as the zero value, reached via a different
// spelling, per the brief's explicit "zero value, new(...), and a
// hand-built literal" requirement.
func TestSEC020_NewState_Pointer_FailsClosed(t *testing.T) {
	s := new(State)
	wantStateCopied(t, "Enable", s.Enable(SourceFlag, "corr-newstate"))
	if got := s.IsOn(); got != false {
		t.Fatalf("new(State).IsOn() = %v, want false", got)
	}
}

// TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with every OTHER field
// populated exactly as if it were a fully-configured, legitimately
// enabled State — `on: true`, a real header, header already
// DebugTouched — but `self` left unset. If checkNotCopied were ever
// accidentally skipped or short-circuited, IsOn() would return true here
// by coincidence (the field really is true); asserting false proves the
// guard is actually the thing producing the answer, not the data.
func TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	h := newTestHeader()
	h.TouchDebug()
	literal := &State{
		on:     true,
		header: h,
		now:    zeroClock,
	}

	if got := literal.IsOn(); got != false {
		t.Fatalf("hand-built literal (self unset) IsOn() = %v, want false even though the on field is literally true — the guard must be what decides this, not the field", got)
	}
	wantStateCopied(t, "Enable", literal.Enable(SourceFlag, "corr-literal"))
	wantStateCopied(t, "AllowSpeed8x", literal.AllowSpeed8x("corr-literal-2"))
	literal.Disable()
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched = false after a self-unset literal's Disable, want still true (sticky; the literal must never have been able to touch it in the first place)")
	}
}

// --- IsOn's fail-closed decision is still observable in the log ------------

// TestSEC020_IsOnCopyHit_IsLoggedNotSilent proves ASM-002's "not silent
// to the SYSTEM, only to this one caller" claim: even though IsOn's bool
// return cannot carry the distinction to its caller, a copy hit still
// produces a registry-sourced ErrStateCopied log entry (errs.New's
// logging side effect, per the same discarded-return pattern
// cheats.go's codeCheatUsed uses) — so a copy-attack in production still
// leaves a trail an operator or the Destructive agent can find, even on
// the one call site that cannot return an error.
func TestSEC020_IsOnCopyHit_IsLoggedNotSilent(t *testing.T) {
	orig := NewState(WithHeader(newTestHeader()))
	cp := stateByteCopy(orig)

	if got := cp.IsOn(); got != false {
		t.Fatalf("copy.IsOn() = %v, want false", got)
	}

	found := false
	for _, e := range errs.Recent() {
		if e.Code == ErrStateCopied {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no ErrStateCopied entry found in errs.Recent() after a copy's IsOn() call — the fail-closed path must still be logged, not silent")
	}
}
