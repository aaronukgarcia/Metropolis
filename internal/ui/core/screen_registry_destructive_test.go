package core

import (
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// FEAT-211 increment 1 — INDEPENDENT DESTRUCTIVE ROUND (GR#23).
//
// These tests were written by an attacker who did not write
// screen_registry.go, against the five mandated axes: switching storm,
// pending-sequence/key routing, draw-during-switch (reentrancy/blocking/
// panic), registration abuse, and contract honesty (every doc-comment
// safety claim treated as a target, never as a fact). Routing-side
// attacks live in cmd/metropolis/feat211_inc1_destructive_test.go.

// --- Axis 1: switching storm -------------------------------------------

// TestScreenRegistry_SwitchingStorm_ConcurrentActivateAndReads is the
// -race workhorse: N writers hammering Activate across the whole
// registered set while N readers call every accessor and INVOKE the
// returned DrawFunc. Each screen's Draw records which screen it belongs
// to; a torn/aliased read (a Draw closure that is not one of the
// registered ones, or a corrupted slice/index) shows up either as a
// -race report, an index panic, or an unknown marker here.
func TestScreenRegistry_SwitchingStorm_ConcurrentActivateAndReads(t *testing.T) {
	r := NewScreenRegistry("destructive-storm")
	const n = 8
	ids := make([]ScreenID, n)
	known := map[ScreenID]bool{}
	for i := 0; i < n; i++ {
		id := ScreenID(strconvItoa(i))
		ids[i] = id
		known[id] = true
		drawn := id
		if err := r.Register(ScreenEntry{
			ID:      id,
			Draw:    func(b *Buffer, _ *ViewModels) { b.Set(0, 0, rune('a'+len(drawn)), tcell.StyleDefault) },
			Grammar: keys.NewKeyGrammar(nil, 0, 0, "storm-"+string(id)),
		}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}

	var wg sync.WaitGroup
	const iters = 500
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if err := r.Activate(ids[(i+seed)%n], nil); err != nil {
					t.Errorf("Activate: %v", err)
					return
				}
			}
		}(w)
	}
	for rd := 0; rd < 4; rd++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := NewBuffer(4, 4)
			for i := 0; i < iters; i++ {
				if id := r.ActiveID(); !known[id] {
					t.Errorf("ActiveID() = %q, which was never registered (torn read)", id)
					return
				}
				d := r.ActiveDraw()
				if d == nil {
					t.Error("ActiveDraw() = nil (contract says never nil)")
					return
				}
				d(buf, nil)
				_ = r.ActiveGrammar()
				if got := len(r.RegisteredIDs()); got != n {
					t.Errorf("RegisteredIDs() length = %d during a switching storm, want %d", got, n)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestScreenRegistry_AccessorsAreNotAnAtomicSnapshot is a CHARACTERIZATION
// test, deterministic by handshake (no timing race): the registry exposes
// ActiveID/ActiveDraw/ActiveGrammar as three independently-locked reads,
// not the single Active() ScreenEntry the FEAT-211 design §7(a) sketched.
// A reader that samples ActiveID and then ActiveGrammar can therefore
// observe screen A's ID paired with screen B's grammar. No caller in
// cmd/metropolis combines two accessors today (render.go uses ActiveDraw
// only; routeKeyInput uses ActiveGrammar only), so this is a latent API
// hazard rather than a live defect — but it is recorded here so the next
// caller that needs a consistent (ID, Draw, Grammar) triple discovers the
// gap from a test instead of from a mis-rendered screen.
func TestScreenRegistry_AccessorsAreNotAnAtomicSnapshot(t *testing.T) {
	r := NewScreenRegistry("destructive-snapshot")
	gMap := keys.NewKeyGrammar(nil, 0, 0, "map")
	gFin := keys.NewKeyGrammar(nil, 0, 0, "finance")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw, Grammar: gMap}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw, Grammar: gFin}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	sampledID := make(chan ScreenID)
	switched := make(chan struct{})
	var gotGrammar *keys.KeyGrammar
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampledID <- r.ActiveID() // read #1, under its own lock
		<-switched                // a switch lands in the gap between the two reads
		gotGrammar = r.ActiveGrammar()
	}()

	id := <-sampledID
	if id != "map" {
		t.Fatalf("sampled ActiveID() = %q, want map", id)
	}
	if err := r.Activate("finance", nil); err != nil {
		t.Fatalf("Activate(finance): %v", err)
	}
	close(switched)
	<-done

	if gotGrammar != gFin {
		t.Fatalf("ActiveGrammar() = %p, want finance's %p", gotGrammar, gFin)
	}
	// The observed pair is (ID=map, Grammar=finance): inconsistent by
	// construction. Asserted, not merely narrated, so the day someone adds
	// an atomic Active() this test fails loudly and gets updated.
	t.Logf("observed inconsistent accessor pair: ID=%q with finance's grammar — the three accessors are not one snapshot", id)
}

// TestScreenRegistry_Activate_LateAbortCanCancelALiveSequence_Characterization
// is a CHARACTERIZATION test — same treatment as
// TestScreenRegistry_AccessorsAreNotAnAtomicSnapshot above, recording an
// honest limitation rather than asserting a fixed contract. It is r2's
// finding F3 on the F1 fix (FEAT-211 increment 1, 2026-08-21): Activate
// releases r.mu BEFORE calling Abort() on the grammar it captured as
// "outgoing" (see Activate's own doc comment). With Activate called only
// from one goroutine — InputLoop's OnDelivered, the shipped contract — that
// window is never observable. But this package's own switching-storm test
// above storms Activate from 4 goroutines, which is a BROADER guarantee
// than the one actually delivered: if a second Activate call completes a
// full capture-switch-abort cycle in the gap between the first call's
// unlock and its own (delayed) Abort(), the first call's late Abort can
// cancel a LIVE sequence belonging to whichever screen is active BY THEN —
// not the screen it was originally captured for.
//
// Reproduced deterministically, not by racing timers: this test performs
// Activate's own two steps (capture-under-lock, then Abort after unlock)
// by hand, using the same unexported fields Activate itself reads
// (r.mu/r.active/r.entries — legal here, this file is package core), so the
// gap between them is held open exactly as long as the test needs rather
// than raced against the scheduler.
//
// Not reachable from the shipped binary: Activate has exactly one caller
// goroutine there today (FEAT-211 design §7(a)). Recorded here so the next
// person who lets a second caller into Activate (a background trigger, a
// scripted demo driver, anything besides InputLoop's own goroutine) finds
// this test instead of a live "a mistyped sequence on the screen I'm
// looking at right now silently vanished" bug report.
//
// FIX DIRECTION (for whoever closes this): a dedicated switchMu held across
// BOTH the capture and the Abort() call would close the window — Activate
// would hold one lock from "read who's outgoing" through "cancel their
// sequence", serializing concurrent switches against EACH OTHER without
// holding the registry's own r.mu (which guards entries/index/active) across
// a caller-supplied Abort call — the same "never hold a mutex across a user
// callback" discipline this package's own draw-during-switch tests already
// enforce for Draw, just extended to Abort.
func TestScreenRegistry_Activate_LateAbortCanCancelALiveSequence_Characterization(t *testing.T) {
	r := NewScreenRegistry("test-late-abort-race")
	gA := keys.NewKeyGrammar(nil, 0, 0, "gA")
	gB := keys.NewKeyGrammar(nil, 0, 0, "gB")
	if err := gA.Register([]string{"a", "b"}, keys.Action{Name: "a-b", Run: func(keys.ActionArgs) {}}); err != nil {
		t.Fatalf("gA.Register: %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "A", Draw: noopDraw, Grammar: gA}); err != nil {
		t.Fatalf("Register(A): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "B", Draw: noopDraw, Grammar: gB}); err != nil {
		t.Fatalf("Register(B): %v", err)
	}

	// A is active (first registered). Start the pending sequence that a
	// concurrent Activate(B) below will capture as "outgoing" and intend
	// to abort.
	if res := gA.Feed(keys.Key{Rune: 'a'}); res.Status != keys.Pending {
		t.Fatalf("gA.Feed('a') = %+v, want Pending — fixture assumption broken", res)
	}

	// Manually perform Activate("B")'s CAPTURE step — identical to what
	// Activate itself does under r.mu — then release the lock, but hold the
	// resulting Abort() call back. This simulates a goroutine that has
	// completed the pointer swap but has not yet reached "outgoing.Abort()".
	r.mu.Lock()
	idxB, ok := r.index["B"]
	if !ok {
		r.mu.Unlock()
		t.Fatal("B not registered — fixture assumption broken")
	}
	outgoing := r.entries[r.active].Grammar // captures gA, exactly as Activate would
	r.active = idxB
	r.mu.Unlock()

	// While that Abort is still delayed, a SECOND, fully real Activate call
	// — indistinguishable from a genuine concurrent caller — switches back
	// to A. This is exactly what a second Activate could interleave for
	// real between the first call's unlock and its own Abort().
	if err := r.Activate("A", nil); err != nil {
		t.Fatalf("Activate(A): %v", err)
	}

	// Complete the OLD pending sequence first ("a","b" dispatches and
	// resets gA to root), so what comes next is unambiguously a BRAND-NEW
	// sequence, not merely the original one that happened to survive —
	// exactly the player typing a fresh mnemonic on the screen they are
	// CURRENTLY looking at, well after the switch above.
	if res := gA.Feed(keys.Key{Rune: 'b'}); res.Status != keys.Dispatched {
		t.Fatalf("gA.Feed('b') (completing the old sequence) = %+v, want Dispatched — fixture assumption broken", res)
	}
	if res := gA.Feed(keys.Key{Rune: 'a'}); res.Status != keys.Pending {
		t.Fatalf("gA.Feed('a') (the new, live sequence) = %+v, want Pending — fixture assumption broken", res)
	}
	if !gA.IsPending() {
		t.Fatal("gA.IsPending() = false right after starting the live sequence — fixture assumption broken")
	}

	// The FIRST caller's delayed Abort() finally runs now.
	outgoing.Abort()

	// CHARACTERIZED, not asserted as desirable: the late Abort cancels the
	// LIVE sequence on the screen that is active by now, even though
	// "outgoing" was captured for a switch that is no longer the most
	// recent one. If this assertion ever starts failing, either the hazard
	// was closed (update this test's doc comment and Activate's own
	// alongside it) or the fixture stopped reproducing the race — it is
	// not a signal that the codebase quietly got better on its own.
	if gA.IsPending() {
		t.Fatal("characterization assumption broken: the late Abort did not cancel the live sequence it should have (per the documented hazard) — see this test's own doc comment")
	}
	t.Log("characterized: a late Abort() captured by an earlier Activate call cancelled a LIVE pending sequence on the screen that is active by the time the abort actually ran — not reachable from the shipped single-caller binary, but broader than the switching-storm test's own multi-goroutine Activate guarantee (see this test's doc comment for the fix direction: a dedicated switchMu held across capture+abort)")
}

// --- Axis 3: draw-during-switch ----------------------------------------

// TestScreenRegistry_DrawCallbackCallingActivate_DoesNotDeadlock is the
// FEAT-208 r2 lesson as an executable check ("never hold a mutex across a
// user callback"): a Draw closure that re-enters the registry — calling
// Activate, ActiveID and RegisteredIDs on the very registry that handed
// it out — must complete. If ActiveDraw held mu across the callback this
// test would hang and the package's test binary would die on the go test
// timeout with a self-explaining stack; no wall-clock assertion is made
// or needed.
func TestScreenRegistry_DrawCallbackCallingActivate_DoesNotDeadlock(t *testing.T) {
	r := NewScreenRegistry("destructive-reentrant")
	var reentered bool
	reentrant := func(*Buffer, *ViewModels) {
		reentered = true
		if err := r.Activate("finance", nil); err != nil {
			t.Errorf("re-entrant Activate from inside a Draw: %v", err)
		}
		_ = r.ActiveID()
		_ = r.RegisteredIDs()
		_ = r.ActiveGrammar()
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: reentrant}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	r.ActiveDraw()(NewBuffer(4, 4), nil)
	if !reentered {
		t.Fatal("the re-entrant Draw never ran")
	}
	if got := r.ActiveID(); got != "finance" {
		t.Fatalf("ActiveID() after a Draw that called Activate = %q, want finance (the re-entrant switch must actually take)", got)
	}
}

// TestScreenRegistry_BlockingDraw_DoesNotBlockActivate proves the lock is
// released before the Draw closure runs, deterministically via a
// handshake rather than by racing timers: the Draw blocks on an unbuffered
// channel, and Activate must complete WHILE it is still blocked. If
// ActiveDraw held mu across the callback, Activate would block until the
// draw was released and the test would deadlock (test-binary timeout, a
// failure) rather than reaching the assertion.
func TestScreenRegistry_BlockingDraw_DoesNotBlockActivate(t *testing.T) {
	r := NewScreenRegistry("destructive-blocking-draw")
	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := func(*Buffer, *ViewModels) {
		close(entered)
		<-release
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: blocking}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	drawDone := make(chan struct{})
	go func() {
		defer close(drawDone)
		r.ActiveDraw()(NewBuffer(4, 4), nil)
	}()

	<-entered // the Draw callback is provably mid-flight right now
	if err := r.Activate("finance", nil); err != nil {
		t.Fatalf("Activate while a Draw is mid-flight: %v", err)
	}
	if got := r.ActiveID(); got != "finance" {
		t.Fatalf("ActiveID() = %q while the previous screen's Draw is still running, want finance", got)
	}
	close(release)
	<-drawDone
}

// TestScreenRegistry_PanickingDraw_LeavesRegistryUsable: a Draw that
// panics must not poison the registry (a mutex held across the callback
// would stay locked forever after the panic unwound past it, wedging
// every later Activate/ActiveID call).
func TestScreenRegistry_PanickingDraw_LeavesRegistryUsable(t *testing.T) {
	r := NewScreenRegistry("destructive-panicking-draw")
	if err := r.Register(ScreenEntry{ID: "map", Draw: func(*Buffer, *ViewModels) { panic("draw exploded") }}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("the panicking Draw did not panic — fixture assumption broken")
			}
		}()
		r.ActiveDraw()(NewBuffer(4, 4), nil)
	}()

	// Every method must still work after the panic unwound through the
	// callback (i.e. mu was not held across it).
	if err := r.Activate("finance", nil); err != nil {
		t.Fatalf("Activate after a panicking Draw: %v", err)
	}
	if got := r.ActiveID(); got != "finance" {
		t.Fatalf("ActiveID() after a panicking Draw = %q, want finance", got)
	}
	if got := len(r.RegisteredIDs()); got != 2 {
		t.Fatalf("RegisteredIDs() = %d entries after a panicking Draw, want 2", got)
	}
}

// --- Axis 4: registration abuse ----------------------------------------

// TestScreenRegistry_DuplicateID_LeavesFirstEntryIntact goes past the
// author's own duplicate test (which only re-checks RegisteredIDs): it
// proves the REJECTED registration's Draw and Grammar never displaced the
// winner's. Proof of failure: swapping Register's duplicate check for a
// silent overwrite (index[e.ID] = len(entries); entries = append(...))
// makes ActiveDraw run loserDraw and ActiveGrammar return loserGrammar,
// and both assertions below fail.
func TestScreenRegistry_DuplicateID_LeavesFirstEntryIntact(t *testing.T) {
	r := NewScreenRegistry("destructive-dup-state")
	winnerGrammar := keys.NewKeyGrammar(nil, 0, 0, "winner")
	loserGrammar := keys.NewKeyGrammar(nil, 0, 0, "loser")
	var winnerCalls, loserCalls int

	if err := r.Register(ScreenEntry{ID: "map", Draw: func(*Buffer, *ViewModels) { winnerCalls++ }, Grammar: winnerGrammar}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: func(*Buffer, *ViewModels) { loserCalls++ }, Grammar: loserGrammar}); err == nil {
		t.Fatal("duplicate Register = nil error, want MET-U004")
	} else if code := mustCode(t, err); code != ErrScreenAlreadyRegistered {
		t.Fatalf("duplicate Register code = %q, want %q", code, ErrScreenAlreadyRegistered)
	}

	r.ActiveDraw()(NewBuffer(4, 4), nil)
	if winnerCalls != 1 || loserCalls != 0 {
		t.Errorf("after a rejected duplicate: winnerCalls=%d loserCalls=%d, want 1,0 (the rejected entry must not have replaced the registered Draw)", winnerCalls, loserCalls)
	}
	if g := r.ActiveGrammar(); g != winnerGrammar {
		t.Errorf("ActiveGrammar() after a rejected duplicate = %p, want the FIRST registration's %p", g, winnerGrammar)
	}
	if got := r.RegisteredIDs(); len(got) != 1 {
		t.Errorf("RegisteredIDs() = %v after a rejected duplicate, want exactly one entry", got)
	}
}

// TestScreenRegistry_NilDraw_IsRejected is FINDING 5 of this round,
// INVERTED after the fix. As originally written it recorded the gap:
// Register accepted a ScreenEntry with a nil Draw without any error and
// ActiveDraw silently substituted noopDraw, so a screen wired with a nil
// adapter switched, reported itself active, and rendered a blank terminal
// with no registry-sourced error anywhere — GR#1's silent-failure rule,
// and a stark contrast with Register's own loud MET-U004 for a duplicate
// ID. It now asserts the rejection (MET-U007).
//
// Can-it-fail proof (2026-08-21): with the nil-Draw check scratch-copied
// out of Register, this test fails at "Register accepted a nil Draw".
// Restored, it passes.
func TestScreenRegistry_NilDraw_IsRejected(t *testing.T) {
	r := NewScreenRegistry("destructive-nil-draw")
	err := r.Register(ScreenEntry{ID: "map", Draw: nil})
	if err == nil {
		t.Fatal("Register accepted a nil Draw: a nil-adapter wiring mistake would render a blank screen with no diagnostic (GR#1)")
	}
	if !strings.Contains(err.Error(), ErrScreenNilDraw) {
		t.Fatalf("Register(nil Draw) = %v, want a registry-sourced %s", err, ErrScreenNilDraw)
	}

	// The rejection must be total: nothing registered, nothing active, and
	// the registry still usable for a correctly-wired screen afterwards.
	if got := r.RegisteredIDs(); len(got) != 0 {
		t.Fatalf("RegisteredIDs() = %v after a rejected nil-Draw Register, want empty", got)
	}
	if got := r.ActiveID(); got != "" {
		t.Fatalf("ActiveID() = %q after a rejected nil-Draw Register, want empty", got)
	}
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register with a real Draw after a rejected one: %v", err)
	}
	if got := r.ActiveID(); got != "map" {
		t.Fatalf("ActiveID() = %q, want map", got)
	}
}

// TestScreenRegistry_WhitespaceIDs_AreDistinctAndNotTrimmed pins the
// (unstated) ID normalisation contract: ScreenID is a raw string, so
// "map", " map" and "map " are three different screens. Any future
// trimming/canonicalisation would break this test, which is the point.
func TestScreenRegistry_WhitespaceIDs_AreDistinctAndNotTrimmed(t *testing.T) {
	r := NewScreenRegistry("destructive-ws-id")
	for _, id := range []ScreenID{"map", " map", "map ", "  ", ""} {
		if err := r.Register(ScreenEntry{ID: id, Draw: noopDraw}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}
	if got := len(r.RegisteredIDs()); got != 5 {
		t.Fatalf("RegisteredIDs() = %d entries, want 5 (whitespace-variant IDs must not collide silently)", got)
	}
	if err := r.Activate(" map", nil); err != nil {
		t.Fatalf("Activate(\" map\"): %v", err)
	}
	if got := r.ActiveID(); got != " map" {
		t.Fatalf("ActiveID() = %q, want %q (IDs must not be trimmed)", got, " map")
	}
}

// TestScreenRegistry_ActivateBeforeAnyRegister rejects with MET-U005 and
// leaves the empty registry's accessors in their documented empty state.
func TestScreenRegistry_ActivateBeforeAnyRegister(t *testing.T) {
	r := NewScreenRegistry("destructive-activate-empty")
	err := r.Activate("map", nil)
	if err == nil {
		t.Fatal("Activate on an empty registry = nil error, want MET-U005")
	}
	if code := mustCode(t, err); code != ErrScreenUnknown {
		t.Errorf("code = %q, want %q", code, ErrScreenUnknown)
	}
	if got := r.ActiveID(); got != "" {
		t.Errorf("ActiveID() = %q, want empty", got)
	}
	if got := r.ActiveGrammar(); got != nil {
		t.Errorf("ActiveGrammar() = %v, want nil", got)
	}
	if got := r.RegisteredIDs(); len(got) != 0 {
		t.Errorf("RegisteredIDs() = %v, want empty", got)
	}
	r.ActiveDraw()(NewBuffer(4, 4), nil) // must not panic
}

// TestScreenRegistry_RegisterRacingActivate: registration is documented
// as boot-time only, but Register takes mu "defensive against a future
// caller that registers a screen after boot" — that claim is a test
// target, not a fact. A registrar goroutine appends screens while a
// switcher goroutine activates already-registered ones and a reader
// invokes ActiveDraw; under -race this must be clean, and every observed
// active ID must be one that was genuinely registered.
func TestScreenRegistry_RegisterRacingActivate(t *testing.T) {
	r := NewScreenRegistry("destructive-register-race")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}

	const n = 100
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := r.Register(ScreenEntry{ID: ScreenID("late-" + strconvItoa(i)), Draw: noopDraw}); err != nil {
				t.Errorf("late Register: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := r.Activate("map", nil); err != nil {
				t.Errorf("Activate(map) during concurrent registration: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := NewBuffer(4, 4)
		for i := 0; i < n; i++ {
			r.ActiveDraw()(buf, nil)
			if got := r.ActiveID(); got != "map" {
				t.Errorf("ActiveID() = %q during concurrent registration, want map (registering must never move activation)", got)
				return
			}
		}
	}()
	wg.Wait()

	if got := len(r.RegisteredIDs()); got != n+1 {
		t.Fatalf("RegisteredIDs() = %d, want %d (every concurrent Register must have landed exactly once)", got, n+1)
	}
}

// TestScreenRegistry_StructCopy_DoesNotMutateOriginal_UnderConcurrency
// extends the author's copy-guard test into the concurrent case: a
// struct-copied registry's rejected calls must not race the original's
// real ones (-race), and the original's state must be untouched.
func TestScreenRegistry_StructCopy_DoesNotMutateOriginal_UnderConcurrency(t *testing.T) {
	r := NewScreenRegistry("destructive-copy-race")
	if err := r.Register(ScreenEntry{ID: "map", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(map): %v", err)
	}
	if err := r.Register(ScreenEntry{ID: "finance", Draw: noopDraw}); err != nil {
		t.Fatalf("Register(finance): %v", err)
	}
	cp := copyScreenRegistryBytes(r)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = cp.Activate("finance", nil)
			_ = cp.ActiveID()
			cp.ActiveDraw()(NewBuffer(2, 2), nil)
			_ = cp.RegisteredIDs()
			_ = cp.Register(ScreenEntry{ID: "trade", Draw: noopDraw})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := r.Activate("map", nil); err != nil {
				t.Errorf("original Activate: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if got := r.ActiveID(); got != "map" {
		t.Fatalf("original ActiveID() = %q after concurrent copy misuse, want map", got)
	}
	if got := len(r.RegisteredIDs()); got != 2 {
		t.Fatalf("original RegisteredIDs() = %d after the copy attempted a Register, want 2", got)
	}
}
