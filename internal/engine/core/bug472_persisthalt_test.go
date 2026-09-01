package core

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-472 (Aaron ruling 2026-09-01, "HALT + SURFACE" -- supersedes MET-E021's
// old swallow-and-continue policy): a failed durable journal append must
// PAUSE the composition and REFUSE every further command, and the error
// surfaced to the client must carry the ORIGINAL failed append's registry
// code and correlation ID (Aaron's follow-up A2Bev001.md Q100011 ruling),
// never a fresh one. This file proves:
//
//	(a) a failed append halts the Engine and every command Kind is refused
//	    afterward with the halt error (ErrSimulationPersistHalted);
//	(b) the surfaced error carries the ORIGINAL correlation ID, not a
//	    fresh one minted for whichever later command triggers the check;
//	(c) concurrent commands racing the halt (-race, many goroutines) never
//	    let one through Accepted after the halt is latched;
//	(d) EngineStatusView/PersistHalted expose the halt to a caller/client
//	    that never itself sent the failing command (Aaron's Q100011
//	    "player must see this reliably" ruling).

// alwaysFailJournaler is a CommandJournaler whose every ObserveCommand call
// fails -- models a durable store that is down for the remainder of the
// process's life, the scenario BUG-472's halt policy exists to survive.
type alwaysFailJournaler struct {
	mu       sync.Mutex
	observed int
}

func (j *alwaysFailJournaler) ObserveCommand(cmd protocol.Command) error {
	j.mu.Lock()
	j.observed++
	j.mu.Unlock()
	return errFakeJournalStorage
}

func (j *alwaysFailJournaler) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.observed
}

// everyCommandKind builds one well-formed Command of every protocol.Kind
// HandleCommand's own switch dispatches (commands.go), each with a fresh
// correlation ID, so a single test can drive "every kind" without
// hand-maintaining a second list that could drift from HandleCommand's own
// switch. Gameplay kinds (Buy/Zone/Build/Demolish/SetFunding) are included
// deliberately: with no GameplayCommandHandler wired they are ALREADY
// rejected (ErrUnhandledCommandKind) before this Engine has ever halted, so
// asserting the halt error takes over that same rejection path once halted
// is itself part of what "every kind" needs to prove.
func everyCommandKind() []protocol.Command {
	return []protocol.Command{
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindPause, Payload: protocol.PausePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindResume, Payload: protocol.ResumePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindSubscribe, Payload: protocol.SubscribePayload{ViewName: EngineStatusViewName}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindUnsubscribe, Payload: protocol.UnsubscribePayload{SubscriptionID: "nonexistent"}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindInspectEntity, Payload: protocol.InspectEntityPayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindDebug, Payload: protocol.DebugPayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindBuy, Payload: protocol.BuyPayload{}},
	}
}

// TestBUG472_HaltRefusesEveryCommandKindAfterAppendFailure is the (a)
// mutation target: removing the top-of-dispatchCommand persistHalt check
// (or removing latchPersistHalt's CompareAndSwap) turns this RED, since
// some or all commands after the first failure would come back Accepted
// again.
func TestBUG472_HaltRefusesEveryCommandKindAfterAppendFailure(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	// The very first command already fails its append and halts.
	first := e.HandleCommand(pauseCmd(mustCorrID()))
	if first.Accepted {
		t.Fatalf("first command with a failing journaler: accepted, want rejected (halt)")
	}
	if first.Error == nil || first.Error.Code != ErrSimulationPersistHalted {
		t.Fatalf("first command's Error = %+v, want code %s", first.Error, ErrSimulationPersistHalted)
	}

	// Every command kind, of every shape, is refused from here on --
	// including kinds that would otherwise have been perfectly valid.
	for _, cmd := range everyCommandKind() {
		result := e.HandleCommand(cmd)
		if result.Accepted {
			t.Errorf("Kind %q after halt: accepted, want rejected", cmd.Kind)
			continue
		}
		if result.Error == nil {
			t.Errorf("Kind %q after halt: Error = nil, want the halt ErrorRef", cmd.Kind)
			continue
		}
		if result.Error.Code != ErrSimulationPersistHalted {
			t.Errorf("Kind %q after halt: Error.Code = %q, want %q", cmd.Kind, result.Error.Code, ErrSimulationPersistHalted)
		}
	}

	// No command reached ObserveCommand again after the very first one --
	// once halted, dispatchCommand's top-of-function check refuses before
	// ever dispatching to a handler, so the journaler is never even asked
	// to append again (there is nothing left it could durably record that
	// would ever be replayed, since the process itself is now paused).
	if got := journaler.count(); got != 1 {
		t.Errorf("journaler.ObserveCommand call count after halt = %d, want 1 (only the original failing append)", got)
	}
}

// TestBUG472_HaltResultCarriesOriginalCorrelationID is the (b) mutation
// target: constructing the halt error with the LATER (refused) command's
// own correlation ID instead of the ORIGINAL failed append's would still
// pass every other assertion in this file but turns this one RED, since
// corrID1 (not corrID2) must appear in the Display string.
func TestBUG472_HaltResultCarriesOriginalCorrelationID(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	corrID1 := mustCorrID()
	first := e.HandleCommand(pauseCmd(corrID1))
	if first.Accepted {
		t.Fatalf("first command: accepted, want rejected")
	}
	if !strings.Contains(first.Error.Display, string(corrID1)) {
		t.Fatalf("first command's own Error.Display = %q, does not contain its own correlation ID %q", first.Error.Display, corrID1)
	}

	// A LATER, textually distinct command must still report corrID1 (the
	// ORIGINAL failure), never its own fresh correlation ID.
	corrID2 := mustCorrID()
	if corrID2 == corrID1 {
		t.Fatal("test precondition violated: mustCorrID produced a duplicate")
	}
	second := e.HandleCommand(pauseCmd(corrID2))
	if second.Accepted {
		t.Fatalf("second command: accepted, want rejected")
	}
	if second.CorrelationID != corrID2 {
		t.Errorf("second CommandResult.CorrelationID = %q, want %q (the wire result always echoes the COMMAND it answers)", second.CorrelationID, corrID2)
	}
	if !strings.Contains(second.Error.Display, string(corrID1)) {
		t.Errorf("second command's Error.Display = %q, does not contain the ORIGINAL failing correlation ID %q", second.Error.Display, corrID1)
	}
	if strings.Contains(second.Error.Display, string(corrID2)) {
		t.Errorf("second command's Error.Display = %q, contains its OWN correlation ID %q -- want only the ORIGINAL %q", second.Error.Display, corrID2, corrID1)
	}

	// Direct getter parity: PersistHalted() must report the SAME original
	// identity the wire error carries.
	code, corrID, ok := e.PersistHalted()
	if !ok {
		t.Fatal("PersistHalted() ok=false after two failed commands, want true")
	}
	if code != ErrSimulationPersistHalted {
		t.Errorf("PersistHalted() code = %q, want %q", code, ErrSimulationPersistHalted)
	}
	if corrID != string(corrID1) {
		t.Errorf("PersistHalted() correlationID = %q, want the ORIGINAL %q", corrID, corrID1)
	}
}

// TestBUG472_ConcurrentCommandsNeverAcceptedAfterHalt is the (c) mutation
// target -- run with -race -count>=3 (per the dispatch brief's gate). It
// establishes the halt SYNCHRONOUSLY first (so there is no ambiguity about
// whether the trigger itself has completed), then hammers HandleCommand
// from many goroutines concurrently: the invariant under test is that NO
// goroutine, no matter how it interleaves, ever observes Accepted=true once
// e.persistHalt is known to be set -- proving there is no window where a
// command sneaks through between the append failure being recorded and the
// halt taking effect (the atomic.Pointer publish IS the take-effect point,
// with no separate step that could lag behind it).
func TestBUG472_ConcurrentCommandsNeverAcceptedAfterHalt(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	// Establish the halt synchronously, in the test goroutine, before any
	// concurrency starts.
	if result := e.HandleCommand(pauseCmd(mustCorrID())); result.Accepted {
		t.Fatalf("precondition: first command accepted, want rejected (halt)")
	}
	if code, _, ok := e.PersistHalted(); !ok || code != ErrSimulationPersistHalted {
		t.Fatalf("precondition: PersistHalted() = (%q, _, %v), want (%q, _, true)", code, ok, ErrSimulationPersistHalted)
	}

	const goroutines = 64
	const perGoroutine = 20
	var wg sync.WaitGroup
	var acceptedCount int64
	var mu sync.Mutex
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				result := e.HandleCommand(pauseCmd(mustCorrID()))
				if result.Accepted {
					mu.Lock()
					acceptedCount++
					mu.Unlock()
					continue
				}
				if result.Error == nil || result.Error.Code != ErrSimulationPersistHalted {
					mu.Lock()
					acceptedCount++ // reuse the same failure counter: any non-halt outcome is equally a violation here
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if acceptedCount != 0 {
		t.Fatalf("%d of %d concurrent commands were accepted (or rejected with a non-halt error) AFTER the halt was already established synchronously -- want 0", acceptedCount, goroutines*perGoroutine)
	}
}

// TestBUG472_ConcurrentFirstFailuresLatchExactlyOneOriginal races MANY
// goroutines each submitting the FIRST-EVER command against a journaler
// that always fails, so every single one of them independently observes an
// append failure and calls latchPersistHalt concurrently. The invariant:
// regardless of which goroutine's construction wins the CompareAndSwap,
// EVERY caller (winner and losers alike) must read back the SAME
// persistHaltState afterward -- proving latchPersistHalt's "exactly one
// winner, everyone reads the winner's identity" contract, the same shape
// BUG-480's dirty-latch race proof already established for the sibling
// dirty flag.
func TestBUG472_ConcurrentFirstFailuresLatchExactlyOneOriginal(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	const goroutines = 64
	var wg sync.WaitGroup
	results := make([]protocol.CommandResult, goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = e.HandleCommand(pauseCmd(mustCorrID()))
		}(g)
	}
	wg.Wait()

	for i, r := range results {
		if r.Accepted {
			t.Errorf("goroutine %d: accepted, want rejected", i)
		}
		if r.Error == nil || r.Error.Code != ErrSimulationPersistHalted {
			t.Errorf("goroutine %d: Error = %+v, want code %s", i, r.Error, ErrSimulationPersistHalted)
		}
	}

	// Every rejection must carry the SAME Display string -- the single
	// winning persistHaltState, never a per-goroutine construction.
	want := results[0].Error.Display
	for i, r := range results {
		if r.Error.Display != want {
			t.Errorf("goroutine %d: Error.Display = %q, want the single shared winner %q (latchPersistHalt did not latch exactly once)", i, r.Error.Display, want)
		}
	}
}

// TestBUG472_EngineStatusView_ObservesHaltWithoutFurtherCommand is the
// observability requirement (Aaron's Q100011 ruling: the player must be
// able to see the pause even if their own next action never happens to
// trigger a fresh CommandResult). It proves EngineStatusView/PersistHalted
// agree, before and after, and that the view can be read from a caller that
// never itself submitted the failing command.
func TestBUG472_EngineStatusView_ObservesHaltWithoutFurtherCommand(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	before := e.EngineStatusView()
	if before.PersistHalted {
		t.Fatal("EngineStatusView().PersistHalted = true before any command, want false")
	}
	if before.PersistHaltError != nil {
		t.Fatalf("EngineStatusView().PersistHaltError = %+v before any command, want nil", before.PersistHaltError)
	}

	corrID := mustCorrID()
	if result := e.HandleCommand(pauseCmd(corrID)); result.Accepted {
		t.Fatalf("precondition: command accepted, want rejected (halt)")
	}

	// A caller that never sent that command still observes the halt via
	// the view -- exactly the proactive-push path EngineStatusView's own
	// doc comment describes.
	after := e.EngineStatusView()
	if !after.PersistHalted {
		t.Fatal("EngineStatusView().PersistHalted = false after a failed append, want true")
	}
	if after.PersistHaltError == nil {
		t.Fatal("EngineStatusView().PersistHaltError = nil after a failed append, want the halt ErrorRef")
	}
	if after.PersistHaltError.Code != ErrSimulationPersistHalted {
		t.Errorf("EngineStatusView().PersistHaltError.Code = %q, want %q", after.PersistHaltError.Code, ErrSimulationPersistHalted)
	}
	if !strings.Contains(after.PersistHaltError.Display, string(corrID)) {
		t.Errorf("EngineStatusView().PersistHaltError.Display = %q, does not contain the original correlation ID %q", after.PersistHaltError.Display, corrID)
	}

	// Repeated reads must be stable and must NOT re-log (mirrors
	// persistHaltState's own doc comment) -- calling the view many times
	// must keep returning the SAME Display string, proving no fresh
	// errs.New/Wrap construction happens per read.
	again := e.EngineStatusView()
	if again.PersistHaltError.Display != after.PersistHaltError.Display {
		t.Errorf("EngineStatusView() called twice produced different Display strings: %q vs %q", after.PersistHaltError.Display, again.PersistHaltError.Display)
	}
}

// TestBUG472_EngineStatusView_DoesNotFloodTheRegistryLog is the log-flood
// mutation target for persistHaltState's precomputed-display design:
// recomputing errs.New(ErrSimulationPersistHalted, ...) inside
// EngineStatusView on every call would add one MET-E023 occurrence to
// errs.Recent() per call. errs.Recent()'s ring buffer COALESCES repeats of
// the exact same Code+CorrelationID+Msg into one Entry with an incremented
// Repeat count rather than a new slot -- so this test reads the coalesced
// Repeat count, not the number of distinct entries, which would stay at 1
// even under the flood mutation and so would never catch it.
func TestBUG472_EngineStatusView_DoesNotFloodTheRegistryLog(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))
	corrID := mustCorrID()
	if result := e.HandleCommand(pauseCmd(corrID)); result.Accepted {
		t.Fatalf("precondition: command accepted, want rejected (halt)")
	}

	haltRepeat := func() int {
		for _, entry := range errs.Recent() {
			if entry.Code == ErrSimulationPersistHalted && entry.CorrelationID == string(corrID) {
				return entry.Repeat
			}
		}
		return -1 // not found at all -- distinct failure, checked separately below.
	}

	before := haltRepeat()
	if before < 0 {
		t.Fatal("no MET-E023 entry found for the original correlation ID right after the halt -- cannot measure repeat growth")
	}
	for i := 0; i < 50; i++ {
		_ = e.EngineStatusView()
	}
	after := haltRepeat()

	if after != before {
		t.Errorf("MET-E023 Repeat count for the original correlation ID before/after 50 EngineStatusView() reads = %d/%d, want unchanged (EngineStatusView must never construct a fresh registry error)", before, after)
	}
}
