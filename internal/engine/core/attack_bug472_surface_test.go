package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// attack_bug472_surface_test.go -- regression suite for the INDEPENDENT
// destructive round r1 finding on BUG-472 (GR#23 independence amendment:
// the r1 attacker was not the r2 fixer). r1's headline finding was that the
// SURFACE half of Aaron's "HALT + SURFACE" ruling never reached a live
// subscriber on 7 of 13 command Kinds, because handleGameplay/
// handleInspectEntity/handleDebug (and dispatchCommand's own halt
// short-circuit) never called signalSubscriptionPump. r2's fix moved the
// signal to ONE place -- HandleCommand itself, wrapping every
// dispatchCommand return unconditionally (commands.go) -- so these tests
// now prove the FIXED behaviour: every command Kind, including a gameplay
// halt and the halt short-circuit itself, wakes the pump.

// haltRecordingSink is a DeltaSink that records every delta pushed to it,
// so a test can distinguish "the view WOULD say halted if anyone asked"
// from "the halt was actually PUSHED to a live subscriber".
type haltRecordingSink struct {
	mu     sync.Mutex
	deltas []protocol.Delta
}

func (s *haltRecordingSink) SendDelta(d protocol.Delta) bool {
	s.mu.Lock()
	s.deltas = append(s.deltas, d)
	s.mu.Unlock()
	return true
}

func (s *haltRecordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deltas)
}

// haltedGameplayEngine wires a journaler that fails and a gameplay handler
// that succeeds, plus a live subscription + pump, and returns them.
func haltedGameplayEngine(t *testing.T, sink *haltRecordingSink) (*Engine, *alwaysFailJournaler) {
	t.Helper()
	journaler := &alwaysFailJournaler{}
	e := NewEngine(
		WithCommandJournaler(journaler),
		WithGameplayCommandHandler(func(protocol.Command) error { return nil }),
	)
	if _, err := e.subs.Subscribe(engineStatusView, nil, "", "attack-bug472-seed"); err != nil {
		t.Fatalf("seed Subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if _, err := e.StartSubscriptionPump(ctx, sink); err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	return e, journaler
}

// TestRegressionBUG472_GameplayHalt_ReachesASubscriber is the r1 headline
// finding's fix proof (r1: TestAttackBUG472_GameplayHalt_NeverReachesASubscriber,
// which asserted the DEFECTIVE behaviour). handleGameplay's own
// signalSubscriptionPump call is gone (removed as part of centralising the
// signal, see commands.go's handleGameplay/HandleCommand doc comments),
// but HandleCommand itself now signals unconditionally after every
// dispatchCommand return -- including this one and the halt short-circuit
// that refuses every command after it -- so a subscriber DOES see the
// halt even though the triggering command was a gameplay Kind.
func TestRegressionBUG472_GameplayHalt_ReachesASubscriber(t *testing.T) {
	sink := &haltRecordingSink{}
	e, journaler := haltedGameplayEngine(t, sink)

	// Drain any delta produced by the seed subscription itself before the
	// attack, so the count below measures only post-halt pushes.
	time.Sleep(100 * time.Millisecond)
	baseline := sink.count()

	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{},
	}
	res := e.HandleCommand(buy)
	if res.Accepted {
		t.Fatalf("precondition: gameplay command accepted, want rejected by the halt")
	}
	if res.Error == nil || res.Error.Code != ErrSimulationPersistHalted {
		t.Fatalf("precondition: Error = %+v, want %s", res.Error, ErrSimulationPersistHalted)
	}
	if got := journaler.count(); got != 1 {
		t.Fatalf("precondition: ObserveCommand called %d times, want 1", got)
	}

	deadline := time.After(2 * time.Second)
	for sink.count() == baseline {
		select {
		case <-deadline:
			t.Fatalf("FINDING NOT FIXED: no delta pushed after a gameplay-triggered halt within 2s "+
				"(baseline %d) -- HandleCommand's centralised signalSubscriptionPump call did not fire "+
				"for this Kind", baseline)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	view := e.EngineStatusView()
	if !view.PersistHalted {
		t.Fatal("EngineStatusView().PersistHalted = false, want true (the halt did latch)")
	}
	t.Logf("FIXED: %d delta(s) pushed after the gameplay halt", sink.count()-baseline)
}

// TestRegressionBUG472_HaltCheckShortCircuit_StillSignals proves the OTHER
// half of r1's finding: dispatchCommand's own top-of-function halt
// short-circuit (reached by every command AFTER the first failure) also
// wakes the pump now, not just the triggering command. This matters
// because once halted, every later command is refused before any handler
// runs -- if the short-circuit itself never signalled, a client that
// missed the ORIGINAL halting command's own delta (e.g. subscribed a
// moment too late) could never be woken by anything that came after.
func TestRegressionBUG472_HaltCheckShortCircuit_StillSignals(t *testing.T) {
	sink := &haltRecordingSink{}
	e, _ := haltedGameplayEngine(t, sink)

	// Establish the halt via a Debug command (one of the three r1 flagged
	// as silent) and drain whatever that alone already pushed.
	dbg := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{},
	}
	if res := e.HandleCommand(dbg); res.Accepted {
		t.Fatalf("precondition: Debug command accepted, want rejected by the halt")
	}
	deadline := time.After(2 * time.Second)
	for sink.count() == 0 {
		select {
		case <-deadline:
			t.Fatalf("no delta pushed after the halting Debug command itself within 2s")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	afterFirst := sink.count()

	// Now a LATER command, refused purely by the top-of-function
	// short-circuit (never reaches handleInspectEntity at all) -- this
	// alone must still wake the pump.
	inspect := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindInspectEntity,
		Payload:         protocol.InspectEntityPayload{},
	}
	if res := e.HandleCommand(inspect); res.Accepted {
		t.Fatalf("precondition: InspectEntity after halt accepted, want rejected")
	}
	deadline = time.After(2 * time.Second)
	for sink.count() == afterFirst {
		select {
		case <-deadline:
			t.Fatalf("FINDING NOT FIXED: the halt short-circuit's own rejection never woke the pump "+
				"(count stayed at %d after the InspectEntity rejection)", afterFirst)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Logf("FIXED: short-circuit rejection pushed a further delta (%d -> %d)", afterFirst, sink.count())
}

// TestAttackBUG472_ThreeLaterCommandsAllCarryTheOriginalCorrelationID is
// Aaron's Q100011 requirement stated exactly: after a halt, issue three
// further DISTINCT commands and prove all three rejections name the
// ORIGINAL failed append's correlation ID -- not their own, and not a
// fresh one minted per rejection.
func TestAttackBUG472_ThreeLaterCommandsAllCarryTheOriginalCorrelationID(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	originalID := mustCorrID()
	first := e.HandleCommand(pauseCmd(originalID))
	if first.Error == nil || first.Error.Code != ErrSimulationPersistHalted {
		t.Fatalf("precondition: first Error = %+v, want %s", first.Error, ErrSimulationPersistHalted)
	}

	later := []protocol.Command{
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindResume, Payload: protocol.ResumePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindDebug, Payload: protocol.DebugPayload{}},
	}
	var displays []string
	for _, cmd := range later {
		res := e.HandleCommand(cmd)
		if res.Error == nil {
			t.Fatalf("Kind %q: Error = nil, want the halt ErrorRef", cmd.Kind)
		}
		if !strings.Contains(res.Error.Display, string(originalID)) {
			t.Errorf("Kind %q: Display = %q, does NOT carry the ORIGINAL correlation ID %q", cmd.Kind, res.Error.Display, originalID)
		}
		if strings.Contains(res.Error.Display, string(cmd.CorrelationID)) {
			t.Errorf("Kind %q: Display = %q leaks the LATER command's own correlation ID %q -- Aaron's Q100011 ruling requires the ORIGINAL failure's ID", cmd.Kind, res.Error.Display, cmd.CorrelationID)
		}
		// The wire envelope's own CorrelationID must still echo the
		// caller's command (request/response correlation), while the
		// ERROR names the original fault -- two different jobs.
		if res.CorrelationID != cmd.CorrelationID {
			t.Errorf("Kind %q: result.CorrelationID = %q, want the request's own %q", cmd.Kind, res.CorrelationID, cmd.CorrelationID)
		}
		displays = append(displays, res.Error.Display)
	}
	for i := 1; i < len(displays); i++ {
		if displays[i] != displays[0] {
			t.Errorf("rejection %d Display = %q differs from rejection 0's %q -- a fresh error is being constructed per rejection", i, displays[i], displays[0])
		}
	}
	// Copy-paste-ability: Aaron asked for the actual registry code AND
	// correlation ID visible in the string a player can copy.
	if !strings.Contains(displays[0], ErrSimulationPersistHalted) {
		t.Errorf("Display = %q does not contain the literal registry code %q", displays[0], ErrSimulationPersistHalted)
	}
	if strings.Contains(displays[0], "{") || strings.Contains(displays[0], "}") {
		t.Errorf("Display = %q contains an unrendered {token} -- the BUG-317 class", displays[0])
	}
}

// TestAttackBUG472_FiveHundredCommandsDoNotFloodTheRegistryLog re-verifies
// the builder's no-flood claim with an independent count at a much higher
// command volume than the builder's own test uses, over the REAL
// package-level registry log rather than a per-test double.
func TestAttackBUG472_FiveHundredCommandsDoNotFloodTheRegistryLog(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(WithCommandJournaler(journaler))

	e.HandleCommand(pauseCmd(mustCorrID()))
	after := len(errs.Recent())

	for i := 0; i < 500; i++ {
		res := e.HandleCommand(pauseCmd(mustCorrID()))
		if res.Accepted {
			t.Fatalf("command %d accepted after halt", i)
		}
	}
	// EngineStatusView is read far more often than commands arrive.
	for i := 0; i < 500; i++ {
		_ = e.EngineStatusView()
	}

	grew := len(errs.Recent()) - after
	if grew != 0 {
		t.Errorf("errs.Recent() grew by %d entries across 500 refused commands + 500 status reads, want 0 "+
			"(persistHaltResult/EngineStatusView must reuse the precomputed display, never call errs.New/Wrap)", grew)
	}
}

// TestAttackBUG472_MixedKindsRacingTheLatch is a stronger race than the
// same-Kind concurrency test in bug472_persisthalt_test.go: N goroutines
// each drive a DIFFERENT command Kind concurrently against the halt latch
// firing, rather than all sending the same kind. This is the r1 finding
// #4 regression: mutating latchPersistHalt's genuine
// e.persistHalt.CompareAndSwap(nil, ...) to a plain Store did NOT redden
// the same-Kind concurrency test, but DID redden here, producing 2-4
// distinct halt Displays where there should be exactly one -- proving the
// "original correlation ID" guarantee (Aaron's Q100011 ask) can be
// violated under real concurrency if the CAS is ever weakened. This test
// is the PERMANENT regression that catches that class of mutation.
//
// Requirements: zero acceptances once ANY goroutine has observed a halt,
// far fewer ObserveCommand calls than the total command count (the halt
// must throttle the append path, not merely tag it), and exactly one
// stable halt identity observed by all.
func TestAttackBUG472_MixedKindsRacingTheLatch(t *testing.T) {
	journaler := &alwaysFailJournaler{}
	e := NewEngine(
		WithCommandJournaler(journaler),
		WithGameplayCommandHandler(func(protocol.Command) error { return nil }),
	)

	kinds := everyCommandKind()
	const perKind = 24
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	displays := map[string]int{}
	accepted := 0

	for i := 0; i < perKind; i++ {
		for k := range kinds {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				<-start
				proto := kinds[k]
				proto.CorrelationID = mustCorrID()
				res := e.HandleCommand(proto)
				mu.Lock()
				if res.Accepted {
					accepted++
				} else if res.Error != nil && res.Error.Code == ErrSimulationPersistHalted {
					displays[res.Error.Display]++
				}
				mu.Unlock()
			}(k)
		}
	}
	close(start)
	wg.Wait()

	if accepted != 0 {
		t.Errorf("%d command(s) accepted while racing the halt latch, want 0", accepted)
	}
	if len(displays) != 1 {
		t.Errorf("observed %d distinct halt Display strings, want exactly 1 (CompareAndSwap must latch one winner permanently): %v", len(displays), keysOf(displays))
	}
	// Every append attempt that lost the race still called ObserveCommand
	// once (they were all in flight before the latch published), but the
	// count must be far below the total command count -- the halt must
	// stop the flow, not merely tag it.
	total := perKind * len(kinds)
	if journaler.count() >= total {
		t.Errorf("ObserveCommand called %d times for %d commands -- the halt never throttled the append path", journaler.count(), total)
	}
	code, corrID, ok := e.PersistHalted()
	if !ok || code != ErrSimulationPersistHalted || corrID == "" {
		t.Errorf("PersistHalted() = (%q, %q, %v), want (%s, <non-empty>, true)", code, corrID, ok, ErrSimulationPersistHalted)
	}
	for d := range displays {
		if !strings.Contains(d, corrID) {
			t.Errorf("the latched Display %q does not contain PersistHalted()'s own correlation ID %q -- two sources of truth", d, corrID)
		}
	}
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
