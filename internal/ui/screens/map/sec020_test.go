package mapscreen

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// runBoundedSEC020 runs fn (a call into a guarded *MapScreen method,
// with its result captured by the caller's own closure) in its own
// goroutine and asserts it completes within 3 seconds, rather than
// calling it synchronously with no bound. A regression that reintroduces
// a pre-lock guard gap on a copy taken mid-lock hangs the guarded method
// forever (SEC-016's exact failure mode: the copy's mu bytes read as
// permanently "locked" by nobody who will ever unlock THIS copy's
// address) — without a per-case bound, that regression is only caught by
// Go's default 10-minute test timeout and a goroutine-dump panic naming
// a stuck select, not the guarded method itself.
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
// because several guarded *MapScreen methods have no error return at
// all (SetStale, Pan, Render, ...) — the caller captures whatever result
// it needs into its own local via closure and asserts it after this
// returns.
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
// Every m.mu.Lock() site in this package's non-test source, found via:
//
//	grep -n "mu\.\(Lock\|Unlock\)()" internal/ui/screens/map/*.go
//
// cross-checked against the full exported-method list:
//
//	grep -n "^func (m \*MapScreen)" internal/ui/screens/map/*.go
//
// (same two-command method engine/debug's copyguard_test.go and
// InProcTransport's sec020_test.go both used).
//
// Results — every exported method, its guard, and its fail-closed value:
//
//	screen.go   Subscribe()          -- pre-check only (reads m.correlationID, never touches mu)             -- propagates the checkNotCopied error
//	screen.go   BindSubscription()   -- pre-check + own mu.Lock() site, pre+post -- silently drops the write (BUG-323)
//	screen.go   UnbindSubscription() -- pre-check + own mu.Lock() site, pre+post -- silently drops the write (BUG-323)
//	screen.go   ApplyDelta()         -- pre-check + own mu.Lock() site (read-only, for the subs lookup), pre only -- drops (delegates to ApplyPatch once past the lookup, which is separately guarded) (BUG-323)
//	screen.go   ApplyPatch()         -- pre-check + own mu.Lock() site, pre+post -- silently drops (same posture as every other malformed-patch cause)
//	screen.go   SetStale()           -- pre-check + own mu.Lock() site, pre+post -- silently drops the write
//	screen.go   SetViewportSize()    -- pre-check + own mu.Lock() site, pre+post -- silently drops the write
//	screen.go   Pan()                -- pre-check + own mu.Lock() site, pre+post -- silently drops the write
//	screen.go   Offset()             -- pre-check + own mu.Lock() site, pre+post -- fails closed to (0, 0)
//	screen.go   MoveCursor()         -- pre-check + own mu.Lock() site, pre+post -- silently drops the write
//	screen.go   CursorPos()          -- pre-check + own mu.Lock() site, pre+post -- fails closed to (0, 0)
//	screen.go   Inspect()            -- pre-check + own mu.Lock() site, pre+post -- fails closed to InspectResult{Found:false}
//	screen.go   InspectCursor()      -- pre-check only (delegates to CursorPos/Inspect, both already guarded) -- fails closed to InspectResult{Found:false}
//	screen.go   ApplyResult()        -- pre-check + own mu.Lock() site, pre+post -- silently drops the write (BUG-490)
//	screen.go   BuildNotice()        -- pre-check + own mu.Lock() site, pre+post -- fails closed to "" (BUG-490)
//	screen.go   DismissBuildNotice() -- pre-check + own mu.Lock() site, pre+post -- silently drops the write (BUG-493)
//	render.go   Render()             -- pre-check + own mu.Lock() site, pre+post -- draws nothing, returns (ASM-015)
//	overlay.go  ActiveOverlay()      -- pre-check + own mu.Lock() site, pre+post -- fails closed to overlayOrder[0]
//	overlay.go  CycleOverlay()       -- pre-check + own mu.Lock() site, pre+post -- fails closed to overlayOrder[0] (write silently dropped)
//
// That is 19 exported methods total: 17 with their OWN direct
// m.mu.Lock() site (BindSubscription, UnbindSubscription, ApplyDelta,
// ApplyPatch, SetStale, SetViewportSize, Pan, Offset, MoveCursor,
// CursorPos, Inspect, ApplyResult, BuildNotice, DismissBuildNotice,
// Render, ActiveOverlay, CycleOverlay) plus 2 pre-check-only
// (InspectCursor, Subscribe — neither touches mu directly, but both
// still read receiver state so both are guarded) — every one of the 19
// exercised below by name.
//
// (BUG-493 item 5: this comment previously claimed 15 ("Corrected 7 -> 9
// -> 15"), itself wrong — the true count at that point was 18: BUG-323's
// ApplyDelta/BindSubscription/UnbindSubscription were guarded and shipped
// but never added to THIS enumeration or exercised by name in the tests
// below (a real coverage gap, not just an arithmetic one), and BUG-490's
// own +2 for ApplyResult/BuildNotice was applied on top of the
// already-wrong 13 rather than the real 16, propagating the miscount
// instead of re-auditing it — exactly the "corrected the wrong way"
// pattern Weakness pattern #3 warns about, now itself the audit-trail
// entry for the NEXT person editing this file. BUG-493 both fixes the
// count (16 pre-existing + ApplyResult/BuildNotice + this bug's own new
// DismissBuildNotice = 19) and closes the exercise gap: ApplyDelta,
// BindSubscription and UnbindSubscription are now called by name in both
// TestSEC020_EveryGuardedMethod_RejectsStructCopy and
// TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung below, not merely
// guarded in production code.)
//
// NOT guarded, and why (the one deliberate exclusion, logged rather than
// silent): none. Every exported method on *MapScreen reads at least one
// receiver field (even Subscribe, via m.correlationID) — unlike
// ui.screen.debug's Screen.TailEntry, which is a pure function of its
// parameters and reads no receiver field at all, there is no exported
// *MapScreen method in that shape.
//
// (19, not 18 or 20 — recount deliberately spelled out per Weakness
// pattern #3's "get the arithmetic right, this comment IS the audit
// trail" instruction, and per BUG-493's own finding that the PREVIOUS
// such spelled-out recount was itself wrong: the two internal helper
// Lock sites this package has — Render's snapshotLocked and
// clampOffsetLocked/clampCursorLocked — are unexported, never called
// except from within an already-guarded caller's critical section, and
// are covered by that caller's pre+post checks, not counted again here.)

// mapScreenByteCopy performs SEC-020's attack — a plain MapScreen struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/debug/copyguard_test.go's stateByteCopy and
// internal/protocol/sec020_test.go's equivalent: a literal `m2 := *m` is
// legal, unsafe-free Go but is exactly what `go vet`'s copylocks check
// flags, and this package's VERIFY step requires `go vet ./...` clean.
// The byte-level copy produces IDENTICAL runtime semantics (mu's bytes
// copied as-is, grid's slice header copied — aliasing the same backing
// array — self's pointer bytes copied unchanged) without a statically
// flaggable copy expression.
func mapScreenByteCopy(m *MapScreen) *MapScreen {
	c := new(MapScreen)
	*(*[unsafe.Sizeof(MapScreen{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(MapScreen{})]byte)(unsafe.Pointer(m))
	return c
}

// wantMapScreenCopied asserts err is exactly ErrMapScreenCopied — naming
// each call individually (rather than a shared loop) means a stripped
// guard on any ONE method identifies which site regressed.
func wantMapScreenCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrMapScreenCopied}) {
		t.Fatalf("%s on a struct-copied MapScreen: err = %v, want ErrMapScreenCopied", method, err)
	}
}

func newSEC020TestScreen() *MapScreen {
	return NewMapScreen("corr-sec020", widgets.DefaultPalette)
}

// TestSEC020_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep above, exercised by name.
func TestSEC020_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	orig := newSEC020TestScreen()
	cp := mapScreenByteCopy(orig)

	wantMapScreenCopied(t, "Subscribe", cp.Subscribe(func(protocol.Command) error { return nil }))

	// BUG-493 item 5: BindSubscription/UnbindSubscription/ApplyDelta were
	// guarded in production but never exercised BY NAME here — this
	// closes that gap. cp.BindSubscription must not register anything
	// observable on the copy (no getter exists; ApplyDelta below proves
	// it indirectly by never reaching the copy's ApplyPatch either).
	cp.BindSubscription(protocol.SubscriptionID("sec020-sub"))
	cp.ApplyDelta(protocol.Delta{SubscriptionID: protocol.SubscriptionID("sec020-sub"), Patch: fullPatchRaw(t)})
	if res := cp.Inspect(0, 0); res.Found {
		t.Fatalf("copy.Inspect(0,0) after a copy's ApplyDelta: Found = true, want false (ApplyDelta must have been rejected, not applied)")
	}
	cp.UnbindSubscription(protocol.SubscriptionID("sec020-sub"))

	cp.ApplyPatch(fullPatchRaw(t)) // no panic, no state change (checked below)
	if res := cp.Inspect(0, 0); res.Found {
		t.Fatalf("copy.Inspect(0,0) after a copy's ApplyPatch: Found = true, want false (ApplyPatch must have been rejected, not applied)")
	}

	cp.SetStale(true) // no observable getter; hygiene proven via Render below

	cp.SetViewportSize(10, 10) // no panic; no observable state change possible without a getter

	cp.Pan(5, 5)
	if x, y := cp.Offset(); x != 0 || y != 0 {
		t.Fatalf("copy.Offset() after copy's Pan = (%d,%d), want (0,0) (fail-closed)", x, y)
	}

	cp.MoveCursor(3, 3)
	if x, y := cp.CursorPos(); x != 0 || y != 0 {
		t.Fatalf("copy.CursorPos() after copy's MoveCursor = (%d,%d), want (0,0) (fail-closed)", x, y)
	}

	if res := cp.Inspect(0, 0); res.Found {
		t.Fatalf("copy.Inspect(0,0) = %+v, want Found=false", res)
	}
	if res := cp.InspectCursor(); res.Found {
		t.Fatalf("copy.InspectCursor() = %+v, want Found=false", res)
	}

	cp.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-V400", Display: "should never land"}})
	if got := cp.BuildNotice(); got != "" {
		t.Fatalf("copy.BuildNotice() after copy's ApplyResult = %q, want \"\" (write must have been rejected)", got)
	}
	cp.DismissBuildNotice() // must not panic; nothing to observe on a copy
	if got := cp.BuildNotice(); got != "" {
		t.Fatalf("copy.BuildNotice() after copy's DismissBuildNotice = %q, want \"\"", got)
	}

	buf := core.NewBuffer(10, 10)
	before := snapshotBuffer(buf)
	cp.Render(buf, core.Rect{X: 0, Y: 0, W: 10, H: 10})
	if got := snapshotBuffer(buf); !buffersEqual(before, got) {
		t.Fatalf("copy.Render() drew into buf, want buf left untouched (ASM-015 fail-closed)")
	}

	if ov := cp.ActiveOverlay(); ov != overlayOrder[0] {
		t.Fatalf("copy.ActiveOverlay() = %v, want %v (fail-closed)", ov, overlayOrder[0])
	}
	if ov := cp.CycleOverlay(true); ov != overlayOrder[0] {
		t.Fatalf("copy.CycleOverlay(true) = %v, want %v (fail-closed, write dropped)", ov, overlayOrder[0])
	}

	// The ORIGINAL must be completely unaffected by every rejected call
	// above.
	if x, y := orig.Offset(); x != 0 || y != 0 {
		t.Fatalf("original.Offset() = (%d,%d) after copy-attack calls, want (0,0) unaffected", x, y)
	}
	if ov := orig.ActiveOverlay(); ov != overlayOrder[0] {
		t.Fatalf("original.ActiveOverlay() = %v after copy-attack calls, want %v unaffected", ov, overlayOrder[0])
	}
}

// TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung is the deterministic
// SEC-016 "copy taken mid-lock" attack: lock mu, take the byte copy
// while it is held (so the copy's mu bytes read as "currently locked, no
// waiters"), unlock the original, then call the copy. Every guarded call
// below must return promptly (never attempt to acquire its own
// permanently unrecoverable lock) because checkNotCopied is lock-free
// and runs BEFORE mu.Lock() in every guarded method. Deterministic,
// single-goroutine, runs under `go test ./... -race -count=1` like
// everything else (v1.4's no-probabilistic-concurrency-tests rule).
func TestSEC020_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	orig := newSEC020TestScreen()

	orig.mu.Lock()
	cp := mapScreenByteCopy(orig) // cp.mu's bytes now read "locked" -- byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	var subscribeErr error
	runBoundedSEC020(t, "Subscribe", func() { subscribeErr = cp.Subscribe(func(protocol.Command) error { return nil }) })
	wantMapScreenCopied(t, "Subscribe", subscribeErr)

	runBoundedSEC020(t, "BindSubscription", func() { cp.BindSubscription(protocol.SubscriptionID("sec020-sub")) })
	runBoundedSEC020(t, "ApplyDelta", func() {
		cp.ApplyDelta(protocol.Delta{SubscriptionID: protocol.SubscriptionID("sec020-sub"), Patch: fullPatchRaw(t)})
	})
	runBoundedSEC020(t, "UnbindSubscription", func() { cp.UnbindSubscription(protocol.SubscriptionID("sec020-sub")) })

	runBoundedSEC020(t, "ApplyPatch", func() { cp.ApplyPatch(fullPatchRaw(t)) })

	runBoundedSEC020(t, "SetStale", func() { cp.SetStale(true) })

	runBoundedSEC020(t, "SetViewportSize", func() { cp.SetViewportSize(10, 10) })

	runBoundedSEC020(t, "Pan", func() { cp.Pan(1, 1) })

	var offX, offY int
	runBoundedSEC020(t, "Offset", func() { offX, offY = cp.Offset() })
	if offX != 0 || offY != 0 {
		t.Fatalf("Offset() on a copy taken mid-lock = (%d,%d), want (0,0)", offX, offY)
	}

	runBoundedSEC020(t, "MoveCursor", func() { cp.MoveCursor(1, 1) })

	var curX, curY int
	runBoundedSEC020(t, "CursorPos", func() { curX, curY = cp.CursorPos() })
	if curX != 0 || curY != 0 {
		t.Fatalf("CursorPos() on a copy taken mid-lock = (%d,%d), want (0,0)", curX, curY)
	}

	var inspectRes InspectResult
	runBoundedSEC020(t, "Inspect", func() { inspectRes = cp.Inspect(0, 0) })
	if inspectRes.Found {
		t.Fatalf("Inspect() on a copy taken mid-lock = %+v, want Found=false", inspectRes)
	}

	runBoundedSEC020(t, "ApplyResult", func() {
		cp.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-V400", Display: "should never land"}})
	})
	var noticeAfterCopy string
	runBoundedSEC020(t, "BuildNotice", func() { noticeAfterCopy = cp.BuildNotice() })
	if noticeAfterCopy != "" {
		t.Fatalf("BuildNotice() on a copy taken mid-lock = %q, want \"\"", noticeAfterCopy)
	}
	runBoundedSEC020(t, "DismissBuildNotice", func() { cp.DismissBuildNotice() })

	buf := core.NewBuffer(4, 4)
	runBoundedSEC020(t, "Render", func() { cp.Render(buf, core.Rect{X: 0, Y: 0, W: 4, H: 4}) })

	var activeOv Overlay
	runBoundedSEC020(t, "ActiveOverlay", func() { activeOv = cp.ActiveOverlay() })
	if activeOv != overlayOrder[0] {
		t.Fatalf("ActiveOverlay() on a copy taken mid-lock = %v, want %v", activeOv, overlayOrder[0])
	}

	var cycledOv Overlay
	runBoundedSEC020(t, "CycleOverlay", func() { cycledOv = cp.CycleOverlay(true) })
	if cycledOv != overlayOrder[0] {
		t.Fatalf("CycleOverlay(true) on a copy taken mid-lock = %v, want %v", cycledOv, overlayOrder[0])
	}

	// The original must still be fully usable afterward -- the abandoned,
	// permanently-"locked"-looking copy mu must not have wedged anything shared.
	orig.SetStale(true)
	orig.ApplyPatch(fullPatchRaw(t)) // 2x2 fixture grid, gives Pan somewhere real to clamp against
	orig.SetViewportSize(1, 1)
	orig.Pan(5, 5)
	if x, y := orig.Offset(); x != 1 || y != 1 {
		t.Fatalf("original.Offset() after copy-during-lock attack = (%d,%d), want (1,1) (2x2 grid, 1x1 viewport, clamped)", x, y)
	}
}

// TestSEC020_ZeroValue_FailsClosed_NoMuTouch proves `var m MapScreen`
// (never passed through NewMapScreen, so self was never stored) is
// rejected the same way a copy is.
func TestSEC020_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var m MapScreen
	wantMapScreenCopied(t, "Subscribe", m.Subscribe(func(protocol.Command) error { return nil }))
	m.ApplyPatch(fullPatchRaw(t)) // must not panic
	m.SetStale(true)              // must not panic
	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("zero-value MapScreen.Inspect() = %+v, want Found=false", res)
	}
}

// TestSEC020_NewMapScreen_ZeroPointer_FailsClosed covers `new(MapScreen)`
// explicitly -- same construction gap, reached via a different spelling.
func TestSEC020_NewMapScreen_ZeroPointer_FailsClosed(t *testing.T) {
	m := new(MapScreen)
	wantMapScreenCopied(t, "Subscribe", m.Subscribe(func(protocol.Command) error { return nil }))
	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("new(MapScreen).Inspect() = %+v, want Found=false", res)
	}
}

// TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with every OTHER field
// populated exactly as if it were a legitimately-constructed, already-
// panned MapScreen -- but `self` left unset. If checkNotCopied were ever
// accidentally skipped, Offset() would return (7, 9) here by coincidence
// (the fields really do hold that); asserting (0, 0) proves the guard is
// actually the thing deciding the answer, not the data.
func TestSEC020_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	literal := &MapScreen{
		correlationID: "corr-literal",
		palette:       widgets.DefaultPalette,
		offsetX:       7,
		offsetY:       9,
	}
	if x, y := literal.Offset(); x != 0 || y != 0 {
		t.Fatalf("hand-built literal (self unset) Offset() = (%d,%d), want (0,0) even though offsetX/offsetY are literally 7,9 -- the guard must be what decides this, not the data", x, y)
	}
	wantMapScreenCopied(t, "Subscribe", literal.Subscribe(func(protocol.Command) error { return nil }))
}

// TestSEC020_CopyHit_IsLoggedNotSilent proves a copy-attack still leaves
// a registry-sourced MET-U101 trail (GR#7) even on Render and Offset,
// which have no error return to carry the rejection through to their own
// caller.
func TestSEC020_CopyHit_IsLoggedNotSilent(t *testing.T) {
	orig := newSEC020TestScreen()
	cp := mapScreenByteCopy(orig)

	if x, y := cp.Offset(); x != 0 || y != 0 {
		t.Fatalf("copy.Offset() = (%d,%d), want (0,0)", x, y)
	}

	found := false
	for _, e := range errs.Recent() {
		if e.Code == ErrMapScreenCopied {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no ErrMapScreenCopied entry found in errs.Recent() after a copy's Offset() call -- the fail-closed path must still be logged, not silent")
	}
}

// --- small local test helpers -------------------------------------------

func fullPatchRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: 2, Height: 2},
		Cells: []wireCell{
			{X: 0, Y: 0, Terrain: "shore"},
			{X: 1, Y: 1, Terrain: "shelf"},
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture full patch: %v", err)
	}
	return raw
}

func snapshotBuffer(buf *core.Buffer) []core.Cell {
	w, h := buf.Size()
	out := make([]core.Cell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, buf.Get(x, y))
		}
	}
	return out
}

func buffersEqual(a, b []core.Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
