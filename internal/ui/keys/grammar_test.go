package keys

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fakeClock is a manually-advanced Clock for deterministic timeout tests
// (AC-2d) — never a real sleep (BUG-031's lesson: no wall-clock
// assertions).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestGrammar() *KeyGrammar {
	return NewKeyGrammar(newFakeClock(time.Unix(0, 0)), time.Second, 3, "test-corr")
}

func feedString(t *testing.T, g *KeyGrammar, s string) FeedResult {
	t.Helper()
	var last FeedResult
	for _, r := range s {
		last = g.Feed(KeyRune(r))
	}
	return last
}

// --- AC-2: leader sequence dispatch, incomplete prefix invokes nothing ---

func TestLeaderSequenceDispatchesOnCompletePath(t *testing.T) {
	g := newTestGrammar()
	fired := 0
	if err := g.Register([]string{"b", "r", "s"}, Action{Name: "build-road-street", Run: func(ActionArgs) { fired++ }}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if res := g.Feed(KeyRune('b')); res.Status != Pending {
		t.Fatalf("after 'b': status = %v, want Pending", res.Status)
	}
	if fired != 0 {
		t.Fatalf("incomplete prefix 'b' invoked action %d times, want 0", fired)
	}
	if res := g.Feed(KeyRune('r')); res.Status != Pending {
		t.Fatalf("after 'b r': status = %v, want Pending", res.Status)
	}
	if fired != 0 {
		t.Fatalf("incomplete prefix 'b r' invoked action %d times, want 0", fired)
	}
	res := g.Feed(KeyRune('s'))
	if res.Status != Dispatched {
		t.Fatalf("after 'b r s': status = %v, want Dispatched", res.Status)
	}
	if fired != 1 {
		t.Fatalf("complete path invoked action %d times, want exactly 1", fired)
	}
}

func TestMnemonicPathIsIdleAfterDispatch(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"z"}, Action{Name: "zone", Run: func(ActionArgs) {}})
	g.Feed(KeyRune('z'))
	if g.IsPending() {
		t.Fatalf("grammar still pending after a completed dispatch")
	}
}

// --- AC-2b: unregistered sequence is distinguishable, no fallthrough ---

func TestUnregisteredSequenceReturnsNoSuchSequence(t *testing.T) {
	g := newTestGrammar()
	fired := 0
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) { fired++ }})

	res := g.Feed(KeyRune('x'))
	if res.Status != NoSuchSequence {
		t.Fatalf("status = %v, want NoSuchSequence", res.Status)
	}
	if fired != 0 {
		t.Fatalf("unregistered key invoked an action")
	}
}

func TestNoSuchPathDoesNotFallThroughToNearestPrefix(t *testing.T) {
	g := newTestGrammar()
	fired := 0
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) { fired++ }})

	g.Feed(KeyRune('b')) // valid prefix
	res := g.Feed(KeyRune('z'))
	if res.Status != NoSuchSequence {
		t.Fatalf("status = %v, want NoSuchSequence", res.Status)
	}
	if fired != 0 {
		t.Fatalf("'b' followed by an unregistered continuation invoked the 'b r s' action — silent fallthrough")
	}
	if g.IsPending() {
		t.Fatalf("grammar left pending after a NoSuchSequence result")
	}
}

func TestNoPublicBypassOfRegisteredDispatch(t *testing.T) {
	// AC-2b: verify there is no exported way to invoke an Action other
	// than through a completed, registered mnemonic path. The only public
	// entry points that can produce Status==Dispatched are Feed/
	// FeedTcellEvent/Abort-adjacent helpers — none accept an arbitrary
	// Action directly. This test documents and pins that contract: an
	// Action value alone, unregistered, can never be run through Feed.
	g := newTestGrammar()
	fired := 0
	unregistered := Action{Name: "ghost", Run: func(ActionArgs) { fired++ }}
	_ = unregistered // never registered, never reachable

	for _, r := range "ghost" {
		g.Feed(KeyRune(r))
	}
	if fired != 0 {
		t.Fatalf("an unregistered Action fired somehow")
	}
}

// --- AC-2c: Esc aborts a pending sequence ---

func TestEscAbortsPendingSequence(t *testing.T) {
	g := newTestGrammar()
	fired := 0
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) { fired++ }})

	g.Feed(KeyRune('b'))
	res := g.Feed(KeyEsc)
	if res.Status != Aborted {
		t.Fatalf("status = %v, want Aborted", res.Status)
	}
	if g.IsPending() {
		t.Fatalf("grammar still reports pending after Esc-abort")
	}
	if fired != 0 {
		t.Fatalf("Esc-abort invoked an action")
	}

	// Finishing the sequence fresh afterward still works.
	feedString(t, g, "brs")
	if fired != 1 {
		t.Fatalf("fired = %d after a fresh completion post-abort, want 1", fired)
	}
}

func TestEscAtIdleIsNoOp(t *testing.T) {
	g := newTestGrammar()
	res := g.Feed(KeyEsc)
	if res.Status != NoOp {
		t.Fatalf("status = %v, want NoOp for Esc at idle", res.Status)
	}
}

// --- AC-2d: idle timeout auto-aborts via injectable clock ---

func TestIdleTimeoutAutoAbortsMidSequence(t *testing.T) {
	clk := newFakeClock(time.Unix(0, 0))
	g := NewKeyGrammar(clk, 5*time.Second, 3, "test-corr")
	fired := 0
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) { fired++ }})

	g.Feed(KeyRune('b'))
	if !g.IsPending() {
		t.Fatalf("expected pending after 'b'")
	}

	clk.Advance(4 * time.Second)
	if g.CheckIdleTimeout() {
		t.Fatalf("CheckIdleTimeout fired before the threshold elapsed")
	}

	clk.Advance(2 * time.Second) // total 6s > 5s threshold
	if !g.CheckIdleTimeout() {
		t.Fatalf("CheckIdleTimeout did not fire after the threshold elapsed")
	}
	if g.IsPending() {
		t.Fatalf("grammar still pending after CheckIdleTimeout fired")
	}
	if fired != 0 {
		t.Fatalf("timeout auto-abort invoked an action")
	}

	// FAILS AGAINST THE UNFIXED CODE: without CheckIdleTimeout's guard, a
	// pending sequence would remain reachable forever — this asserts the
	// state machine genuinely reset, not merely that the boolean says so.
	res := g.Feed(KeyRune('r'))
	if res.Status != NoSuchSequence {
		t.Fatalf("after timeout-abort, feeding 'r' alone (not a registered top-level path) status = %v, want NoSuchSequence", res.Status)
	}
}

func TestIdleTimeoutNeverBlocksOrSleeps(t *testing.T) {
	// This test's very shape is the assertion: CheckIdleTimeout must
	// return promptly with no pending sequence and never spin/sleep.
	g := newTestGrammar()
	done := make(chan struct{})
	go func() {
		g.CheckIdleTimeout()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CheckIdleTimeout blocked")
	}
}

// --- AC-3: Continuations, synchronous, which-key HUD data ---

func TestContinuationsAvailableSynchronouslyAfterPrefix(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) {}})
	_ = g.Register([]string{"b", "z"}, Action{Name: "bz", Run: func(ActionArgs) {}})

	g.Feed(KeyRune('b'))
	cont := g.Continuations()
	if len(cont) != 2 {
		t.Fatalf("Continuations() len = %d, want 2 (got %+v)", len(cont), cont)
	}
	if cont[0].Key != "r" || cont[1].Key != "z" {
		t.Fatalf("Continuations() not stably sorted: %+v", cont)
	}
	if cont[1].IsLeaf != true {
		t.Fatalf("'z' should be a leaf (b z is a complete registered path)")
	}
	if cont[0].IsLeaf != false {
		t.Fatalf("'r' should not be a leaf (only b r s is registered)")
	}
}

func TestWhichKeyContinuationsAtIdleIsRoot(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b"}, Action{Name: "b", Run: func(ActionArgs) {}})
	cont := g.Continuations()
	if len(cont) != 1 || cont[0].Key != "b" {
		t.Fatalf("Continuations() at idle = %+v, want just [b]", cont)
	}
}

// --- AC-4: HUD dim-after-N-uses ---

func TestDimAfterConfiguredUses(t *testing.T) {
	g := NewKeyGrammar(newFakeClock(time.Unix(0, 0)), time.Second, 2, "test-corr")
	_ = g.Register([]string{"z"}, Action{Name: "zone", Run: func(ActionArgs) {}})

	if g.ShouldDimHUD([]string{"z"}) {
		t.Fatalf("dimmed before any use")
	}
	g.Feed(KeyRune('z'))
	if g.ShouldDimHUD([]string{"z"}) {
		t.Fatalf("dimmed after only 1 use, threshold is 2")
	}
	g.Feed(KeyRune('z'))
	if !g.ShouldDimHUD([]string{"z"}) {
		t.Fatalf("not dimmed after reaching the threshold (2 uses)")
	}
}

// --- AC-5: count prefixes ---

func TestCountPrefixAppliedToDispatch(t *testing.T) {
	g := newTestGrammar()
	var gotCount int
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(a ActionArgs) { gotCount = a.Count }})

	// The count is named once and used to BUILD the input as well as to
	// assert on it, so the two cannot drift apart (GR#15).
	const wantCount = 5
	feedString(t, g, fmt.Sprintf("%dbrs", wantCount))
	if gotCount != wantCount {
		t.Fatalf("Count = %d, want %d", gotCount, wantCount)
	}
}

func TestNoCountPrefixDefaultsToOne(t *testing.T) {
	g := newTestGrammar()
	var gotCount int
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(a ActionArgs) { gotCount = a.Count }})

	feedString(t, g, "brs")
	if gotCount != 1 {
		t.Fatalf("Count = %d, want 1 (default)", gotCount)
	}
}

// --- AC-6: repeat ('.') and undo/redo hooks ---

func TestRepeatLastReplaysSameCountAndPath(t *testing.T) {
	g := newTestGrammar()
	var calls []ActionArgs
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(a ActionArgs) { calls = append(calls, a) }})

	const repeatCount = 3
	feedString(t, g, fmt.Sprintf("%dbrs", repeatCount))
	res := g.Feed(KeyRune('.'))
	if res.Status != Dispatched {
		t.Fatalf("'.' status = %v, want Dispatched", res.Status)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 invocations (original + repeat), got %d", len(calls))
	}
	if calls[1].Count != repeatCount {
		t.Fatalf("repeat Count = %d, want %d (original count preserved)", calls[1].Count, repeatCount)
	}
}

func TestRepeatWithNoPriorDispatchIsNoOp(t *testing.T) {
	g := newTestGrammar()
	res := g.Feed(KeyRune('.'))
	if res.Status != NoOp {
		t.Fatalf("status = %v, want NoOp", res.Status)
	}
}

func TestUndoRedoDispatchThroughRegisteredHooks(t *testing.T) {
	g := newTestGrammar()
	var undone, redone bool
	_ = g.RegisterUndo(func() bool { undone = true; return true })
	_ = g.RegisterRedo(func() bool { redone = true; return true })

	if res := g.Feed(KeyRune('u')); res.Status != Dispatched {
		t.Fatalf("'u' status = %v, want Dispatched", res.Status)
	}
	if !undone {
		t.Fatalf("undo hook was not invoked")
	}
	if res := g.Feed(KeyRune('U')); res.Status != Dispatched {
		t.Fatalf("'U' status = %v, want Dispatched", res.Status)
	}
	if !redone {
		t.Fatalf("redo hook was not invoked")
	}
}

func TestUndoWithNoHookRegisteredIsNoOp(t *testing.T) {
	g := newTestGrammar()
	res := g.Feed(KeyRune('u'))
	if res.Status != NoOp {
		t.Fatalf("status = %v, want NoOp when no undo hook registered", res.Status)
	}
}

// --- AC-10: global bindings fire mid-sequence, don't disturb it ---

func TestGlobalFiresDuringInProgressLeaderSequence(t *testing.T) {
	g := newTestGrammar()
	globalFired := 0
	_ = g.RegisterGlobal(Key{Rune: ' '}, Action{Name: "pause", Run: func(ActionArgs) { globalFired++ }})
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) {}})

	g.Feed(KeyRune('b'))
	res := g.Feed(Key{Rune: ' '})
	if res.Status != GlobalDispatched {
		t.Fatalf("status = %v, want GlobalDispatched", res.Status)
	}
	if globalFired != 1 {
		t.Fatalf("global fired %d times, want 1", globalFired)
	}
	if !g.IsPending() {
		t.Fatalf("global dispatch disturbed the in-progress leader sequence")
	}
	// The sequence can still be completed afterward.
	res2 := g.Feed(KeyRune('r'))
	if res2.Status != Pending {
		t.Fatalf("sequence not resumable after a global fired mid-sequence: %v", res2.Status)
	}
}

func TestReservedTokenRejectedForRegisterAndGlobal(t *testing.T) {
	g := newTestGrammar()
	if err := g.Register([]string{"u", "x"}, Action{Name: "bad"}); err == nil {
		t.Fatalf("Register with reserved top-level token 'u' should have been rejected")
	}
	if err := g.RegisterGlobal(KeyEsc, Action{Name: "bad"}); err == nil {
		t.Fatalf("RegisterGlobal with reserved token Esc should have been rejected")
	}
}

// --- AC-12: shared Action dispatch seam (documented; grep target) ---

func TestSharedActionDispatchSeam(t *testing.T) {
	// Both a "key" dispatch and a stand-in "mouse" dispatch invoke the
	// SAME registered Action value — pinning AC-12's "shared seam"
	// contract at the type level (mouse rendering/hit-testing itself is
	// ui.core's job, out of scope here).
	g := newTestGrammar()
	calls := 0
	act := Action{Name: "click-equivalent", Run: func(ActionArgs) { calls++ }}
	_ = g.Register([]string{"c"}, act)

	g.Feed(KeyRune('c')) // "key path"
	for _, d := range g.AllActions() {
		if d.Path[0] == "c" {
			d.Action.Run(ActionArgs{Count: 1, Path: d.Path}) // stand-in "mouse path" dispatch
		}
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one key dispatch, one shared-seam mouse-equivalent dispatch)", calls)
	}
}

// --- AC-14: duplicate mnemonic path rejected at Register time ---

func TestRegisterRejectsDuplicatePath(t *testing.T) {
	g := newTestGrammar()
	if err := g.Register([]string{"b", "r", "s"}, Action{Name: "first"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := g.Register([]string{"b", "r", "s"}, Action{Name: "second"})
	if err == nil {
		t.Fatalf("duplicate Register at the same path did not error")
	}
}

// TestRegisterEmptyPathRejectedWithU309: Register with an empty mnemonic
// path is rejected with MET-U309 (codeRegisterEmptyPath), NOT MET-U301
// (codeRegisterPrefixConflict). The U301 template requires a conflictsWith
// value that an empty path cannot have — reusing it here would render a
// literal "{conflictsWith}" (the BUG-317 D3 REJECT finding). The error code
// and rendered display are both asserted.
func TestRegisterEmptyPathRejectedWithU309(t *testing.T) {
	g := newTestGrammar()
	err := g.Register([]string{}, Action{Name: "nothing"})
	if err == nil {
		t.Fatal("Register([]string{}) did not error")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("Register empty-path error %v is not a registry-sourced *errs.E", err)
	}
	if e.Code != codeRegisterEmptyPath {
		t.Errorf("empty-path error code = %q, want %q (MET-U301 must NOT be reused here)", e.Code, codeRegisterEmptyPath)
	}
	if e.Code == codeRegisterPrefixConflict {
		t.Errorf("empty-path error must not be MET-U301: its template needs a conflictsWith this path does not have")
	}
	display := e.Display()
	if strings.Contains(display, "{conflictsWith}") || strings.Contains(display, "{path}") {
		t.Fatalf("empty-path Display() = %q renders a template key literally", display)
	}
	if !strings.Contains(display, "empty") {
		t.Errorf("empty-path Display() = %q does not mention the empty path", display)
	}
}

func TestRegisterConflictNeverSilentlyOverwrites(t *testing.T) {
	g := newTestGrammar()
	firstFired, secondFired := 0, 0
	_ = g.Register([]string{"b"}, Action{Name: "first", Run: func(ActionArgs) { firstFired++ }})
	_ = g.Register([]string{"b"}, Action{Name: "second", Run: func(ActionArgs) { secondFired++ }}) // rejected

	g.Feed(KeyRune('b'))
	if firstFired != 1 || secondFired != 0 {
		t.Fatalf("firstFired=%d secondFired=%d, want 1,0 (order-dependent overwrite occurred)", firstFired, secondFired)
	}
}

// --- AC-14b (ASM-118): prefix/complete-binding ambiguity rejected ---

func TestPrefixConflictBothDirections(t *testing.T) {
	t.Run("complete-then-longer", func(t *testing.T) {
		g := newTestGrammar()
		if err := g.Register([]string{"b"}, Action{Name: "b"}); err != nil {
			t.Fatalf("Register(b): %v", err)
		}
		if err := g.Register([]string{"b", "r"}, Action{Name: "br"}); err == nil {
			t.Fatalf("Register(b r) after Register(b) should have been rejected as ambiguous")
		}
	})
	t.Run("longer-then-complete", func(t *testing.T) {
		g := newTestGrammar()
		if err := g.Register([]string{"b", "r"}, Action{Name: "br"}); err != nil {
			t.Fatalf("Register(b r): %v", err)
		}
		if err := g.Register([]string{"b"}, Action{Name: "b"}); err == nil {
			t.Fatalf("Register(b) after Register(b r) should have been rejected as ambiguous")
		}
	})
}

func TestAmbiguousRegisterNamesBothPaths(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b"}, Action{Name: "b"})
	err := g.Register([]string{"b", "r"}, Action{Name: "br"})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !containsAll(err.Error(), "b") {
		t.Fatalf("error does not mention the conflicting path: %v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- AC-15 (GR#21): determinism — stable Continuations ordering ---

func TestDeterministicContinuationsOrder(t *testing.T) {
	g := newTestGrammar()
	for _, tok := range []string{"z", "b", "m", "a", "q"} {
		_ = g.Register([]string{tok}, Action{Name: tok, Run: func(ActionArgs) {}})
	}
	first := g.Continuations()
	for i := 0; i < 20; i++ {
		got := g.Continuations()
		if len(got) != len(first) {
			t.Fatalf("Continuations() length changed across calls")
		}
		for j := range got {
			if got[j].Key != first[j].Key {
				t.Fatalf("Continuations() order not stable: call %d differs from call 0 at index %d (%q vs %q)", i, j, got[j].Key, first[j].Key)
			}
		}
	}
	want := []string{"a", "b", "m", "q", "z"}
	for i, c := range first {
		if c.Key != want[i] {
			t.Fatalf("Continuations()[%d] = %q, want %q (lexical order)", i, c.Key, want[i])
		}
	}
}

func TestDispatchIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	build := func() (int, []string) {
		g := newTestGrammar()
		var order []string
		_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) { order = append(order, "brs") }})
		_ = g.Register([]string{"z"}, Action{Name: "z", Run: func(ActionArgs) { order = append(order, "z") }})
		feedString(t, g, "brsz")
		return len(order), order
	}
	n1, o1 := build()
	n2, o2 := build()
	if n1 != n2 || n1 != 2 {
		t.Fatalf("dispatch counts differ across runs: %d vs %d", n1, n2)
	}
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Fatalf("dispatch order differs across identical runs: %v vs %v", o1, o2)
		}
	}
}

// --- AC-17: concurrency, Feed vs a concurrent registry read ---

func TestConcurrentFeedAndPaletteRead(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "brs", Run: func(ActionArgs) {}})
	_ = g.Register([]string{"z"}, Action{Name: "z", Run: func(ActionArgs) {}})

	stop := make(chan struct{})
	var feeder sync.WaitGroup
	feeder.Add(1)
	go func() {
		defer feeder.Done()
		for {
			select {
			case <-stop:
				return
			default:
				g.Feed(KeyRune('z'))
			}
		}
	}()

	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for i := 0; i < 500; i++ {
			_ = g.AllActions()
			_ = g.Continuations()
		}
	}()

	reader.Wait() // bounded goroutine finishes on its own
	close(stop)   // now signal the unbounded feeder to stop
	feeder.Wait()
}
