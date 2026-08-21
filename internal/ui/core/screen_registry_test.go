package core

import (
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// copyScreenRegistryBytes performs a raw byte-for-byte memcpy of a
// ScreenRegistry — identical in effect to the illegal-but-compilable
// `c := *r` (both alias every reference field and give the copy its own,
// independent mu byte-pattern) — same technique as
// internal/protocol/sec020_test.go's copyTransportBytes: this package
// cannot contain a literal `*r` struct copy and still pass `go vet ./...`
// (copylocks, since ScreenRegistry embeds a sync.Mutex), so the
// byte-level copy is the sanctioned way to exercise the copy-guard
// regression.
func copyScreenRegistryBytes(r *ScreenRegistry) *ScreenRegistry {
	c := new(ScreenRegistry)
	*(*[unsafe.Sizeof(ScreenRegistry{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(ScreenRegistry{})]byte)(unsafe.Pointer(r))
	return c
}

func mustCode(t *testing.T, err error) string {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a registry-sourced *errs.E", err)
	}
	return e.Code
}

// --- Register: registration order + duplicate rejection ---

func TestScreenRegistry_RegisteredIDs_IsRegistrationOrder(t *testing.T) {
	r := NewScreenRegistry("test-order")
	ids := []ScreenID{"services", "map", "finance", "trade"}
	for _, id := range ids {
		if err := r.Register(ScreenEntry{ID: id, Draw: noopDraw}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}
	got := r.RegisteredIDs()
	if len(got) != len(ids) {
		t.Fatalf("RegisteredIDs() = %v, want %d entries", got, len(ids))
	}
	for i, id := range ids {
		if got[i] != id {
			t.Errorf("RegisteredIDs()[%d] = %q, want %q (registration order must be preserved, never map iteration order — GR#21)", i, got[i], id)
		}
	}
}

func TestScreenRegistry_Register_DuplicateID_Rejected(t *testing.T) {
	r := NewScreenRegistry("test-dup")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("first Register(map): %v", err)
	}
	err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw})
	if err == nil {
		t.Fatal("second Register(map) with the same ID = nil error, want rejection (MET-U004)")
	}
	if code := mustCode(t, err); code != ErrScreenAlreadyRegistered {
		t.Errorf("error code = %q, want %q", code, ErrScreenAlreadyRegistered)
	}
	// The first registration must survive a rejected second attempt —
	// proof of failure: if Register silently overwrote instead of
	// rejecting, RegisteredIDs would still show exactly one "map" entry
	// either way, so this also checks entries didn't somehow duplicate.
	ids := r.RegisteredIDs()
	if len(ids) != 1 || ids[0] != "map" {
		t.Errorf("RegisteredIDs() after rejected duplicate = %v, want exactly [map]", ids)
	}
}

// --- Activate: unknown ID rejection, switching ---

func TestScreenRegistry_Activate_UnknownID_Rejected(t *testing.T) {
	r := NewScreenRegistry("test-unknown")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	err := r.Activate("finance")
	if err == nil {
		t.Fatal("Activate(finance) with no such registered screen = nil error, want rejection (MET-U005)")
	}
	if code := mustCode(t, err); code != ErrScreenUnknown {
		t.Errorf("error code = %q, want %q", code, ErrScreenUnknown)
	}
	// Proof of failure: activation must not have silently moved anyway.
	if got := r.ActiveID(); got != "map" {
		t.Errorf("ActiveID() after a rejected Activate = %q, want unchanged %q", got, "map")
	}
}

// TestScreenRegistry_FirstRegistered_IsInitiallyActive proves Register's
// documented default (the first screen registered becomes active,
// matching this binary's pre-FEAT-211 baseline of always drawing
// mapScreen) — proof of failure: registering a second/third screen must
// NOT move activation away from the first.
func TestScreenRegistry_FirstRegistered_IsInitiallyActive(t *testing.T) {
	r := NewScreenRegistry("test-default-active")
	if got := r.ActiveID(); got != "" {
		t.Fatalf("ActiveID() before any Register = %q, want empty", got)
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if got := r.ActiveID(); got != "map" {
		t.Fatalf("ActiveID() after first Register = %q, want %q", got, "map")
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}
	if got := r.ActiveID(); got != "map" {
		t.Fatalf("ActiveID() after a SECOND Register = %q, want still %q (registering must never move activation)", got, "map")
	}
}

// TestScreenRegistry_Activate_SwitchesDraw is the "switch changes draw"
// proof: ActiveDraw must return the just-activated screen's own Draw
// closure, not the previous one, and calling it must actually reach the
// closure that was registered (a real behavioural difference, not just a
// pointer-equality check that could pass by accident).
func TestScreenRegistry_Activate_SwitchesDraw(t *testing.T) {
	r := NewScreenRegistry("test-switch-draw")
	var mapCalls, financeCalls int
	mapDraw := func(*Buffer, *ViewModels) { mapCalls++ }
	financeDraw := func(*Buffer, *ViewModels) { financeCalls++ }

	if err := r.Register(ScreenEntry{ID: "map", Draw: mapDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: financeDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	r.ActiveDraw()(nil, nil)
	if mapCalls != 1 || financeCalls != 0 {
		t.Fatalf("before switching: mapCalls=%d financeCalls=%d, want 1,0", mapCalls, financeCalls)
	}

	if err := r.Activate("finance"); err != nil {
		t.Fatalf("Activate(finance): %v", err)
	}
	r.ActiveDraw()(nil, nil)
	if mapCalls != 1 || financeCalls != 1 {
		t.Fatalf("after switching to finance: mapCalls=%d financeCalls=%d, want 1,1 (switch must change which closure ActiveDraw returns)", mapCalls, financeCalls)
	}
}

// TestScreenRegistry_Activate_SwitchesGrammar proves ActiveGrammar
// tracks the active screen too, including the nil-Grammar-is-legal case
// (a screen with no registered actions of its own).
func TestScreenRegistry_Activate_SwitchesGrammar(t *testing.T) {
	r := NewScreenRegistry("test-switch-grammar")
	svcGrammar := keys.NewKeyGrammar(nil, 0, 0, "svc")

	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw, Grammar: nil}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "services", Draw: noopDraw, Grammar: svcGrammar}); err != nil {
		t.Fatalf("Register(services): %v", err)
	}

	if g := r.ActiveGrammar(); g != nil {
		t.Fatalf("ActiveGrammar() while map (nil-Grammar) is active = %v, want nil", g)
	}
	if err := r.Activate("services"); err != nil {
		t.Fatalf("Activate(services): %v", err)
	}
	if g := r.ActiveGrammar(); g != svcGrammar {
		t.Fatalf("ActiveGrammar() after switching to services = %p, want %p (the exact registered instance)", g, svcGrammar)
	}
}

// TestScreenRegistry_Activate_AbortsOutgoingScreensPendingGrammar is the
// FIX PROOF for r2's finding F1 (FEAT-211 increment 1 independent
// destructive round, 2026-08-21) — pinned IN THIS PACKAGE, not only
// through cmd/metropolis's wiring tests, because the abort itself lives in
// Activate (screen_registry.go) and removing it leaves `go test
// ./internal/ui/core/` fully green with only cmd/metropolis catching the
// regression otherwise.
//
// Two sub-cases, both required by the overturned ruling: a switch AWAY
// from a screen with a pending leader prefix aborts it (the original,
// still-correct half), and re-activating the ALREADY-active screen aborts
// it too (the new half — the carve-out that made self-switch a no-op is
// what r2 broke: F4, "s", "f", F4, "+" fired a real funding command
// because the self-switch left "s f" pending for a bare "+" to complete).
//
// Can-it-fail proof (2026-08-21): with the `outgoing.Abort()` call in
// Activate scratch-copied out (replaced with a no-op), this test fails at
// both "still pending after switching away" and "still pending after a
// self-switch". Restored, it passes. See this task's final report for the
// actual scratch-copy transcript.
func TestScreenRegistry_Activate_AbortsOutgoingScreensPendingGrammar(t *testing.T) {
	r := NewScreenRegistry("test-activate-aborts-pending")
	gMap := keys.NewKeyGrammar(nil, 0, 0, "map-grammar")
	gFin := keys.NewKeyGrammar(nil, 0, 0, "finance-grammar")
	if err := gMap.Register([]string{"a", "b"}, keys.Action{Name: "map-ab", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatalf("gMap.Register: %v", err)
	}
	if err := gFin.Register([]string{"a", "b"}, keys.Action{Name: "fin-ab", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatalf("gFin.Register: %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw, Grammar: gMap}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw, Grammar: gFin}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	// --- switch-away case ---
	if res := gMap.Feed(keys.Key{Rune: 'a'}); res.Status != keys.Pending {
		t.Fatalf("gMap.Feed('a') = %+v, want Pending — fixture assumption broken", res)
	}
	if !gMap.IsPending() {
		t.Fatal("gMap.IsPending() = false right after feeding 'a' — fixture assumption broken")
	}
	if err := r.Activate("finance"); err != nil {
		t.Fatalf("Activate(finance): %v", err)
	}
	if gMap.IsPending() {
		t.Fatal("gMap.IsPending() = true after switching AWAY to finance, want false: Activate must Abort() the outgoing screen's grammar")
	}

	// --- self-switch case (the overturned carve-out) ---
	if res := gFin.Feed(keys.Key{Rune: 'a'}); res.Status != keys.Pending {
		t.Fatalf("gFin.Feed('a') = %+v, want Pending — fixture assumption broken", res)
	}
	if !gFin.IsPending() {
		t.Fatal("gFin.IsPending() = false right after feeding 'a' — fixture assumption broken")
	}
	if err := r.Activate("finance"); err != nil { // finance is ALREADY active
		t.Fatalf("self Activate(finance): %v", err)
	}
	if gFin.IsPending() {
		t.Fatal("gFin.IsPending() = true after a SELF-switch to the already-active finance screen, want false: re-activating the active screen must abort its pending grammar too (r2 F1 — GR#23 lead override, 2026-08-21)")
	}
}

// TestScreenRegistry_WrongKeyDoesNotSwitch is the "wrong key does not
// switch" proof at the registry layer: Activate on an ID that was never
// registered must leave the CURRENT active screen exactly as it was
// (already covered by the unknown-ID test above), and a caller that
// simply never calls Activate must never see ActiveID move on its own —
// proving there is no hidden auto-advance/timer-driven switch anywhere
// in this type.
func TestScreenRegistry_WrongKeyDoesNotSwitch(t *testing.T) {
	r := NewScreenRegistry("test-no-autoswitch")
	for _, id := range []ScreenID{"map", "finance", "services"} {
		if err := r.Register(ScreenEntry{ID: id, Draw: noopDraw}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}
	for i := 0; i < 5; i++ {
		if got := r.ActiveID(); got != "map" {
			t.Fatalf("iteration %d: ActiveID() = %q, want unchanged %q with no Activate call in between", i, got, "map")
		}
	}
	_ = r.Activate("nonexistent-screen")
	if got := r.ActiveID(); got != "map" {
		t.Fatalf("ActiveID() after a rejected Activate = %q, want still %q", got, "map")
	}
}

// TestScreenRegistry_ActiveAccessors_EmptyRegistry proves the
// zero-registration case is safe: no nil-func panic on ActiveDraw, a
// nil (not a bogus) Grammar, and an empty ScreenID.
func TestScreenRegistry_ActiveAccessors_EmptyRegistry(t *testing.T) {
	r := NewScreenRegistry("test-empty")
	if got := r.ActiveID(); got != "" {
		t.Errorf("ActiveID() on empty registry = %q, want empty", got)
	}
	if g := r.ActiveGrammar(); g != nil {
		t.Errorf("ActiveGrammar() on empty registry = %v, want nil", g)
	}
	// Must not panic.
	r.ActiveDraw()(nil, nil)
}

// --- Copy guard (SEC-020 class) ---

func TestScreenRegistry_StructCopy_MethodsRejected(t *testing.T) {
	r := NewScreenRegistry("test-copyguard")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	cp := copyScreenRegistryBytes(r) // byte-level copy — self now points at the ORIGINAL, not cp

	if err := cp.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err == nil {
		t.Error("cp.Register on a struct copy = nil error, want MET-U006 rejection")
	} else if code := mustCode(t, err); code != ErrScreenRegistryCopied {
		t.Errorf("cp.Register error code = %q, want %q", code, ErrScreenRegistryCopied)
	}
	if err := cp.Activate("map"); err == nil {
		t.Error("cp.Activate on a struct copy = nil error, want MET-U006 rejection")
	}
	if got := cp.ActiveID(); got != "" {
		t.Errorf("cp.ActiveID() on a struct copy = %q, want empty (copy-guard short-circuit)", got)
	}
	if got := cp.ActiveGrammar(); got != nil {
		t.Errorf("cp.ActiveGrammar() on a struct copy = %v, want nil", got)
	}
	cp.ActiveDraw()(nil, nil) // must not panic
	if got := cp.RegisteredIDs(); got != nil {
		t.Errorf("cp.RegisteredIDs() on a struct copy = %v, want nil", got)
	}

	// The ORIGINAL must be entirely unaffected by anything attempted on
	// the copy.
	if got := r.ActiveID(); got != "map" {
		t.Errorf("original r.ActiveID() after copy misuse = %q, want unchanged %q", got, "map")
	}
}

// --- Activate structural cost proof (UI-SPEC §5's <30ms budget,
// BUG-291/292 class: count work, never assert wall-clock) ---

// TestScreenRegistry_Activate_CostIsConstant_NotProportionalToScreenCount
// is this design's structural stand-in for the UI-SPEC §5 <30ms F-key
// switch budget (BUG-291/292 class: count work, never assert
// wall-clock — a millisecond assertion flakes under CI load; a
// proportionality claim does not). Activate's only per-call cost is the
// SEC-020-class copy-guard's correlation-ID mint (errs.NewCorrelationID,
// the same fixed cost every checkNotCopied call in this codebase pays,
// e.g. RenderLoop.TriggerRender) plus a map lookup and an int
// assignment — NONE of which scale with how many screens are registered
// or which screen was previously active. This proves that directly: the
// measured allocation count with 3 registered screens must equal the
// measured allocation count with 30, which a re-subscribe/reconstruct-
// on-switch design (the thing the warm-state approach deliberately
// avoids, design §4/§7(d)) could never satisfy.
//
// # Why this is skipped under -race (investigated 2026-08-21)
//
// testing.AllocsPerRun works by diffing the PROCESS-WIDE mallocs counter
// before and after N calls to the measured func — it cannot attribute an
// allocation to "this goroutine only" (runtime.MemStats carries no such
// breakdown). Reproduced under `go test -race -count=10`: this failed
// 1/10 (small=10.0, large=9.0), while `go test -count=10` (no -race) was
// 10/10 stable, and a wider run (`go test -count=50`, no -race) was
// 50/50 stable — so the noise is specifically a -race artifact, not a
// pre-existing flake of the assertion itself.
//
// Two fixes were tried and rejected before landing on the skip:
//   - Disabling GC for the measurement window (debug.SetGCPercent(-1))
//     made it WORSE (3/10 failures) — starving the collector shifts heap
//     growth/scavenge work into the window instead of removing noise.
//   - Interleaving both configurations' Activate calls in one shared
//     loop (so any periodic background cost hits both equally) still
//     showed ~2% run-to-run variance in the summed mallocs count under
//     -race across 30 trials (measured via manual runtime.ReadMemStats
//     diffing, not just AllocsPerRun) — i.e. -race's own bookkeeping
//     (shadow-memory tracking, its background sync machinery) injects
//     allocation noise on the same order of magnitude as Activate's own
//     ~9-10 mallocs/call cost, which no amount of averaging removes
//     because it is not stationary noise around a fixed mean; it is
//     phase-dependent on how many prior allocations (crypto/rand's
//     buffered UUID entropy, GC bookkeeping) happened to fall inside
//     which measurement window.
//
// Activate's freedom from any loop over r.entries/r.index is verified by
// direct source inspection (see Activate's own doc comment and
// implementation: one map lookup, one slice index, no iteration) — the
// property this test exists to protect is real and true; what's
// unreliable is specifically AllocsPerRun as an INSTRUMENT under -race.
// The assertion stays live (and exact) for every other test invocation,
// including the plain `go test ./...` and `go vet`/build parts of CI —
// -race is the one configuration where this measurement tool itself is
// documented to be unreliable (testing.AllocsPerRun's doc: "the return
// value ... may vary"), so this is the sanctioned fallback (named
// skip-under-race), not a deleted or weakened property.
func TestScreenRegistry_Activate_CostIsConstant_NotProportionalToScreenCount(t *testing.T) {
	if !allocCountingReliable {
		t.Skip("testing.AllocsPerRun's process-wide mallocs counter is not reliable under -race (see this test's doc comment for the 2026-08-21 investigation) — this assertion runs under the plain `go test` (no -race) leg of CI instead")
	}

	measure := func(screenCount int) float64 {
		r := NewScreenRegistry("test-alloc")
		ids := make([]ScreenID, screenCount)
		for i := range ids {
			ids[i] = ScreenID(strconvItoa(i))
			if err := r.Register(ScreenEntry{ID: ids[i], Draw: noopDraw}); err != nil {
				t.Fatalf("Register(%q): %v", ids[i], err)
			}
		}
		i := 0
		return testing.AllocsPerRun(200, func() {
			if err := r.Activate(ids[i%len(ids)]); err != nil {
				t.Fatalf("Activate: %v", err)
			}
			i++
		})
	}

	small := measure(3)
	large := measure(30)
	if small != large {
		t.Errorf("Activate allocation count with 3 registered screens = %.1f, with 30 = %.1f — want EQUAL (switching must cost the same regardless of how many screens are registered; a design that re-subscribes/reconstructs on switch would fail this)", small, large)
	}
}

// strconvItoa is a tiny local decimal formatter (mirrors
// internal/ui/keys/key.go's own itoa) so this test file doesn't need to
// import strconv just to mint distinct ScreenIDs.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestScreenRegistry_Register_RejectsPathologicalID(t *testing.T) {
	r := NewScreenRegistry("test-empty-id")
	// An empty ScreenID is not itself banned by Register (ScreenID is a
	// plain string, no reserved-token concept the way ui.keys has) — but
	// it must still behave exactly like any other ID: registrable once,
	// duplicate-rejected the second time, activatable.
	if err := r.Register(ScreenEntry{ID: "", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(\"\"): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "", Draw: noopDraw}); err == nil {
		t.Error("second Register(\"\") = nil error, want MET-U004 duplicate rejection")
	}
	if got := r.ActiveID(); got != "" {
		t.Errorf("ActiveID() = %q, want empty string ID (the only registered one)", got)
	}
}

func TestErrorCodes_HaveExpectedPrefix(t *testing.T) {
	for _, code := range []string{ErrScreenAlreadyRegistered, ErrScreenUnknown, ErrScreenRegistryCopied} {
		if !strings.HasPrefix(code, "MET-U0") {
			t.Errorf("code %q does not use ui.core's registered U000-U099 range", code)
		}
	}
}
