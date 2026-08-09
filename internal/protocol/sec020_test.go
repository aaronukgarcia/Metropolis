package protocol

// SEC-020 wave 1: InProcTransport copy-rejection guard tests.
//
// ENUMERATION METHOD (Weakness pattern #3 — state the method, not just
// the count). Every t.closeMu.Lock()/RLock() site in transport.go, plus
// every exported method that hands out a reference to shared state
// (Commands/Results/Events/Deltas), was found with:
//
//	awk '
//	/^func \(t \*InProcTransport\)/ { fn=$0; sub(/^func \(t \*InProcTransport\) /,"",fn); sub(/\(.*/,"",fn) }
//	/t\.closeMu\.(RLock|Lock)\(\)/ { print NR": "fn" -> "$0 }
//	/t\.checkNotCopied\(/ { print NR": "fn" [guard] "$0 }
//	' internal/protocol/transport.go
//
// which maps every closeMu acquisition and every checkNotCopied call
// back to its enclosing method, mirroring SEC-018's awk-pass precedent
// (engine/core). Result — nine methods, all now guarded, all
// checkNotCopied calls immediately preceding their closeMu acquisition
// (or, for the four receive-side accessors, with no closeMu acquisition
// at all — see transport.go's Results doc comment for why those are
// still guarded):
//
//	SendCommand -> closeMu.RLock (line 283), guard at 276
//	Close       -> closeMu.Lock  (line 388), guard at 385
//	SendResult  -> closeMu.RLock (line 429), guard at 423
//	SendEvent   -> closeMu.RLock (line 441), guard at 438
//	SendDelta   -> closeMu.RLock (line 455), guard at 452
//	Results     -> no closeMu, guard at 315
//	Events      -> no closeMu, guard at 324
//	Deltas      -> no closeMu, guard at 333
//	Commands    -> no closeMu, guard at 401
//
// Zero remaining t.closeMu.Lock()/RLock() sites without an immediately
// preceding guard, and zero exported InProcTransport methods without a
// checkNotCopied call — cross-checked by hand against the full method
// list in transport.go (grep '^func (t \*InProcTransport)').

import (
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// assertClosedNotNilTimeout bounds how long assertClosedNotNil waits for
// a receive before concluding the channel was nil (and therefore
// blocking forever) rather than closed. Generous relative to how fast a
// closed-channel receive actually resolves (effectively instant), so
// this never flakes on a slow CI machine while still failing fast on the
// real bug.
const assertClosedNotNilTimeout = 2 * time.Second

// copyTransportBytes performs a raw byte-for-byte memcpy of an
// InProcTransport — identical in effect to the illegal-but-compilable
// `c := *t` (both alias every reference field: cmdCh, resultCh, eventCh,
// deltaCh, closed, closeOnce, and self's pointee; both give the copy its
// own, independent closeMu byte-pattern) — same technique as
// engine/core/sec014_poc_test.go's e2Copy, used here for the identical
// reason: this package cannot contain a literal `*t` and still pass `go
// vet ./...` (copylocks), which this fix's VERIFY step requires, so the
// byte-level copy is the sanctioned way to keep exercising the
// regression once the vet-catchable form can no longer live in this
// repository.
func copyTransportBytes(t *InProcTransport) *InProcTransport {
	c := new(InProcTransport)
	*(*[unsafe.Sizeof(InProcTransport{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(InProcTransport{})]byte)(unsafe.Pointer(t))
	return c
}

// errorIsCode checks err against a *errs.E carrying code, via
// errors.Is — the same pattern engine/core's SEC-014/016/018 tests use
// (errors.Is(err, &errs.E{Code: ...})), since *errs.E.Is compares by
// Code alone (see foundation/errs.E.Is's doc comment), letting a test
// assert "was this ErrTransportCopied" without needing the exact
// correlation ID/message/timestamp errs.New constructed.
func errorIsCode(err error, code string) bool {
	return errors.Is(err, &errs.E{Code: code})
}

// TestSEC020_ConstructedCopy_ZeroValue_AllGuardedSitesRejected exercises
// requirement 3 (fail closed on a zero value): a bare
// InProcTransport{}/new(InProcTransport), never touched by
// NewInProcTransport, so self was never stored. Every guarded method
// must reject it with ErrTransportCopied — including the four receive
// accessors, which must return a distinct closed channel rather than the
// zero value's nil channel (a nil channel receive blocks forever, which
// would itself be a hang bug this guard specifically exists to prevent).
func TestSEC020_ConstructedCopy_ZeroValue_AllGuardedSitesRejected(t *testing.T) {
	zero := new(InProcTransport)

	if err := zero.SendCommand(testCommand("corr")); !errorIsCode(err, ErrTransportCopied) {
		t.Errorf("zero.SendCommand: err = %v, want ErrTransportCopied", err)
	}
	if err := zero.Close(); !errorIsCode(err, ErrTransportCopied) {
		t.Errorf("zero.Close: err = %v, want ErrTransportCopied", err)
	}
	if ok := zero.SendResult(CommandResult{CorrelationID: "c"}); ok {
		t.Error("zero.SendResult returned true, want false (copy rejected)")
	}
	if ok := zero.SendEvent(Event{Kind: "x"}); ok {
		t.Error("zero.SendEvent returned true, want false (copy rejected)")
	}
	if ok := zero.SendDelta(Delta{SubscriptionID: "s"}); ok {
		t.Error("zero.SendDelta returned true, want false (copy rejected)")
	}

	// Receive accessors: must return a closed channel (immediate zero
	// value + ok=false), NEVER the zero value's nil channel (which would
	// hang any receiver forever — exactly the class of bug SEC-016
	// established pre-lock ordering to prevent).
	assertClosedNotNil(t, "Commands", func() bool { _, ok := <-zero.Commands(); return ok })
	assertClosedNotNil(t, "Results", func() bool { _, ok := <-zero.Results(); return ok })
	assertClosedNotNil(t, "Events", func() bool { _, ok := <-zero.Events(); return ok })
	assertClosedNotNil(t, "Deltas", func() bool { _, ok := <-zero.Deltas(); return ok })
}

// assertClosedNotNil runs recv (a single non-generic receive expression)
// with a hard deadline: on a nil channel it would block forever, so a
// timeout firing IS the failure mode being guarded against, not merely
// an unlucky schedule.
func assertClosedNotNil(t *testing.T, method string, recv func() bool) {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- recv() }()
	select {
	case ok := <-done:
		if ok {
			t.Errorf("%s() on a copy: receive returned ok=true, want ok=false (closed sentinel channel)", method)
		}
	case <-time.After(assertClosedNotNilTimeout):
		t.Fatalf("%s() on a copy: receive did not return within the deadline — the accessor returned a nil channel (hangs forever) instead of a closed sentinel", method)
	}
}

// TestSEC020_ConstructedCopy_HandBuiltLiteral_AllGuardedSitesRejected
// exercises requirement 3's third form: a hand-built struct literal with
// SOME fields populated (real channels, so a naive implementation might
// "work") but self left at its zero value (nil pointer) because the
// literal was built outside NewInProcTransport. Must be rejected exactly
// like the all-zero case — self being unset is the misuse, independent
// of which other fields happen to be populated.
func TestSEC020_ConstructedCopy_HandBuiltLiteral_AllGuardedSitesRejected(t *testing.T) {
	partial := &InProcTransport{
		cmdCh:    make(chan Command, 4),
		resultCh: make(chan CommandResult, 4),
		eventCh:  make(chan Event, 4),
		deltaCh:  make(chan Delta, 4),
		closed:   make(chan struct{}),
	}
	// closeOnce deliberately left nil — a real attacker who skips
	// NewInProcTransport has no reason to also hand-build this internal
	// closure, and the guard must reject before ever reaching it (if it
	// didn't, Close() would nil-pointer-panic calling closeOnce(), a
	// second, independent bug the identity check also happens to
	// prevent).

	if err := partial.SendCommand(testCommand("corr")); !errorIsCode(err, ErrTransportCopied) {
		t.Errorf("partial.SendCommand: err = %v, want ErrTransportCopied", err)
	}
	if err := partial.Close(); !errorIsCode(err, ErrTransportCopied) {
		t.Errorf("partial.Close: err = %v, want ErrTransportCopied", err)
	}
	if ok := partial.SendResult(CommandResult{CorrelationID: "c"}); ok {
		t.Error("partial.SendResult returned true, want false (copy rejected)")
	}
	if ok := partial.SendEvent(Event{Kind: "x"}); ok {
		t.Error("partial.SendEvent returned true, want false (copy rejected)")
	}
	if ok := partial.SendDelta(Delta{SubscriptionID: "s"}); ok {
		t.Error("partial.SendDelta returned true, want false (copy rejected)")
	}
	assertClosedNotNil(t, "Commands", func() bool { _, ok := <-partial.Commands(); return ok })
	assertClosedNotNil(t, "Results", func() bool { _, ok := <-partial.Results(); return ok })
	assertClosedNotNil(t, "Events", func() bool { _, ok := <-partial.Events(); return ok })
	assertClosedNotNil(t, "Deltas", func() bool { _, ok := <-partial.Deltas(); return ok })
}

// TestSEC020_DeterministicStructCopy_AllGuardedSitesRejected builds the
// attack STATE deterministically (per the dispatch brief: "build the
// attack state, don't race for the timing, so it runs under -race")
// rather than relying on scheduler luck: the original's closeMu is held
// (write-locked, simulating "mid-Close" / "mid-exclusive-section")
// across the byte-copy, exactly the SEC-016 shape ("a copy taken while
// the original holds the lock carries mutex bytes reading as 'locked,
// waiter expected'"). Every guarded method on the copy must reject
// OUTRIGHT — not merely "eventually" — proving the pre-lock check runs
// before the copy's own (poisoned) closeMu is ever touched; if the check
// ran after the lock instead, every RLock/Lock call below would hang
// forever on this specific copy, since nothing will ever unlock ITS
// closeMu.
func TestSEC020_DeterministicStructCopy_AllGuardedSitesRejected(t *testing.T) {
	tr := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer func() { _ = tr.Close() }()

	// Build the attack state: hold the ORIGINAL's closeMu across the
	// copy, so the copy's own closeMu bytes are captured mid-lock.
	tr.closeMu.Lock()
	cp := copyTransportBytes(tr)
	tr.closeMu.Unlock()

	// Every guarded method on cp must reject via ErrTransportCopied,
	// deterministically, every single call — proven by running each site
	// several times, not just once, since a check that races rather than
	// orders correctly could pass on the first call and hang or misbehave
	// on a later one.
	const attempts = 50
	for i := 0; i < attempts; i++ {
		if err := cp.SendCommand(testCommand("corr")); !errorIsCode(err, ErrTransportCopied) {
			t.Fatalf("cp.SendCommand attempt %d: err = %v, want ErrTransportCopied", i, err)
		}
		if err := cp.Close(); !errorIsCode(err, ErrTransportCopied) {
			t.Fatalf("cp.Close attempt %d: err = %v, want ErrTransportCopied", i, err)
		}
		if ok := cp.SendResult(CommandResult{CorrelationID: "c"}); ok {
			t.Fatalf("cp.SendResult attempt %d returned true, want false", i)
		}
		if ok := cp.SendEvent(Event{Kind: "x"}); ok {
			t.Fatalf("cp.SendEvent attempt %d returned true, want false", i)
		}
		if ok := cp.SendDelta(Delta{SubscriptionID: "s"}); ok {
			t.Fatalf("cp.SendDelta attempt %d returned true, want false", i)
		}
	}
	assertClosedNotNil(t, "Commands", func() bool { _, ok := <-cp.Commands(); return ok })
	assertClosedNotNil(t, "Results", func() bool { _, ok := <-cp.Results(); return ok })
	assertClosedNotNil(t, "Events", func() bool { _, ok := <-cp.Events(); return ok })
	assertClosedNotNil(t, "Deltas", func() bool { _, ok := <-cp.Deltas(); return ok })

	// The ORIGINAL must be completely unaffected by the copy attack —
	// still open, still able to send and be closed normally.
	if err := tr.SendCommand(testCommand("orig-still-works")); err != nil {
		t.Fatalf("original tr.SendCommand after copy attack: %v", err)
	}
	if ok := tr.SendResult(CommandResult{CorrelationID: "orig"}); !ok {
		t.Fatal("original tr.SendResult after copy attack returned false, want true")
	}
}

// TestSEC020_ConcurrentDeterministicCopyHammer_NoHangNoRace hammers a
// deterministically-constructed copy (built while the original's
// closeMu was held, so the copy's closeMu bytes are captured mid-lock —
// same construction as the test above) from many goroutines
// concurrently, alongside the ORIGINAL being driven normally. This is
// the concurrent-hammer shape SEC-014's PoC used (Tester-1 reproduced
// SEC-018's ordering bug this same way: "1,786 of 3,000 calls returned;
// the rest wedged permanently" when a check ran after the lock instead
// of before it). Every call on the copy must resolve to ErrTransportCopied
// (or false) — never hang, never panic, never silently succeed.
func TestSEC020_ConcurrentDeterministicCopyHammer_NoHangNoRace(t *testing.T) {
	tr := NewInProcTransport(4, 4, 4, 4)
	defer func() { _ = tr.Close() }()

	tr.closeMu.Lock()
	cp := copyTransportBytes(tr)
	tr.closeMu.Unlock()

	const hammerCalls = 3000
	var wg sync.WaitGroup
	var badMu sync.Mutex
	var bad int

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hammerCalls; i++ {
			if err := cp.SendCommand(testCommand("hammer")); !errorIsCode(err, ErrTransportCopied) {
				badMu.Lock()
				bad++
				badMu.Unlock()
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hammerCalls; i++ {
			if err := cp.Close(); !errorIsCode(err, ErrTransportCopied) {
				badMu.Lock()
				bad++
				badMu.Unlock()
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < hammerCalls; i++ {
			if ok := cp.SendResult(CommandResult{CorrelationID: "hammer"}); ok {
				badMu.Lock()
				bad++
				badMu.Unlock()
			}
		}
	}()

	// The original must keep working normally throughout, unaffected.
	for i := 0; i < 2000; i++ {
		if err := tr.SendCommand(testCommand("orig-hammer")); err != nil {
			t.Fatalf("original tr.SendCommand call %d during copy hammer: unexpected error %v", i, err)
		}
		select {
		case <-tr.Commands():
		default:
		}
	}

	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d of the copy's calls returned something other than the copy-rejected outcome", bad)
	}
}

// TestSEC020_BUG007PanicPath_RejectedNotPanicked is the direct
// before/after regression demonstration the dispatch brief asks for.
// BEFORE this fix (see the dispatch report for the git-archive(HEAD)
// scratch-copy run against the code exactly as committed, which reliably
// produced "panic: send on closed channel" from this identical
// construction), a copy's Close() raced the original's in-flight sends
// because the copy's closeMu was independent of the original's. AFTER
// this fix, checkNotCopied rejects the copy's Close() before closeMu (or
// closeOnce) is ever touched, so the original's senders are never
// exposed to a concurrent close from the copy at all — proven here by
// running the exact attack shape (many senders on the original, Close()
// hammered on the copy, tiny buffers to maximise mid-flight overlap)
// under -race and asserting it completes without panicking and without
// a single sender being disrupted.
func TestSEC020_BUG007PanicPath_RejectedNotPanicked(t *testing.T) {
	const senders = 16

	tr := NewInProcTransport(1, 1, 1, 1)
	cp := copyTransportBytes(tr)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(senders)
	for i := 0; i < senders; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tr.SendResult(CommandResult{CorrelationID: "race"})
				tr.SendEvent(Event{Kind: "race.event"})
				tr.SendDelta(Delta{SubscriptionID: "sub-race"})
			}
		}()
	}

	// cp.Close() must be rejected outright — it must never reach
	// closeOnce(), so it can never race the original's in-flight sends,
	// however they're scheduled.
	if err := cp.Close(); !errorIsCode(err, ErrTransportCopied) {
		t.Fatalf("cp.Close(): err = %v, want ErrTransportCopied", err)
	}
	close(stop)
	wg.Wait()

	// The original must still be fully open and usable — cp.Close() must
	// have had zero effect on it.
	if err := tr.SendCommand(testCommand("orig-after-copy-close-rejected")); err != nil {
		t.Fatalf("original tr.SendCommand after rejected copy Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("original tr.Close(): %v", err)
	}
}

// TestSEC020_FreshTransports_SelfIdentityNeverCollides is the
// non-attack-path sanity check (mirrors
// TestEngine_SelfIdentity_FreshEnginesAreDistinct): two independently
// constructed InProcTransports must never reject each other or
// themselves — the guard must never false-positive on ordinary use.
func TestSEC020_FreshTransports_SelfIdentityNeverCollides(t *testing.T) {
	t1 := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	t2 := NewInProcTransport(DefaultCommandBuffer, DefaultResultBuffer, DefaultEventBuffer, DefaultDeltaBuffer)
	defer func() { _ = t1.Close() }()
	defer func() { _ = t2.Close() }()

	if err := t1.SendCommand(testCommand("t1")); err != nil {
		t.Fatalf("t1.SendCommand: %v", err)
	}
	if err := t2.SendCommand(testCommand("t2")); err != nil {
		t.Fatalf("t2.SendCommand: %v", err)
	}
	if ok := t1.SendResult(CommandResult{CorrelationID: "t1"}); !ok {
		t.Fatal("t1.SendResult: want true")
	}
	if ok := t2.SendResult(CommandResult{CorrelationID: "t2"}); !ok {
		t.Fatal("t2.SendResult: want true")
	}
}
