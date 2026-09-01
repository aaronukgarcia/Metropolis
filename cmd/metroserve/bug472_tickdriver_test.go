package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// bug472_tickdriver_test.go -- BUG-472 r2 finding #3: metroserve's tickLoop
// (main.go) kept firing AdvanceTicks every tickInterval forever into a
// halted engine, with no log and no stop. This file proves BOTH halves of
// the r2 fix, driven through the REAL production wiring (tickLoop +
// startCommandLoop, the same pair main.go's run()/cityhost.go's buildCity
// wire), never a white-box call into engine.core internals:
//
//	(a) once the engine halts, tickLoop stops sending FURTHER AdvanceTicks
//	    commands -- proven by counting CommandResults for the tick driver's
//	    own correlation ID that arrive AFTER the halting one; a
//	    still-spinning tickLoop would keep producing new ones every
//	    interval, a fixed one produces zero;
//	(b) a real, transport-connected subscriber (protocol.InProcTransport's
//	    own Deltas() channel -- the same client-facing surface wsserver
//	    forwards to a real websocket client, not a white-box e.subs read)
//	    observes the halt via a pushed engine.status delta carrying
//	    PersistHalted=true, without the subscriber ever sending another
//	    command itself.
func TestBUG472_TickLoopStopsAdvancingAfterHalt(t *testing.T) {
	dir := t.TempDir()
	city := persist.CityKey{TenantID: persistTenantID, CityID: "tickhalt"}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	// Fail the 2nd append: the second AdvanceTicks the tick driver sends.
	// The first tick succeeds so there is an established "ticking normally"
	// baseline to compare the post-halt silence against.
	failing := &attackFailingAppendStore{Store: disk, failCall: 2}

	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, failing, city, &discardWriter{})
	if err != nil {
		t.Fatalf("wireAndRehydrate: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pumpDone, err := e.StartSubscriptionPump(ctx, transport)
	if err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}

	const tickInterval = 20 * time.Millisecond
	tickCorrID := string(protocol.NewCorrelationID())
	loopDone := startCommandLoop(ctx, e, transport, comp, failing, city, 0, tickCorrID, &discardWriter{})
	tickDone := tickLoop(ctx, e, transport, tickInterval, tickCorrID)

	// Drain transport.Results() into a channel-backed counter on its own
	// goroutine for the lifetime of the test, so no result is ever lost to
	// scheduler jitter regardless of how slow the machine is (GR#28/
	// Verification Standards: never assert a wall-clock UPPER bound as the
	// precondition -- poll for the event, not for a fixed short window).
	tickResults := make(chan struct{}, 4096)
	go func() {
		for r := range transport.Results() {
			if string(r.CorrelationID) == tickCorrID {
				select {
				case tickResults <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Precondition: POLL for the halt to actually latch (no fixed window --
	// this can take arbitrarily long under a loaded CI runner, and a slow
	// poll interval never produces a false pass, only a slow one).
	pollDeadline := time.Now().Add(10 * time.Second)
	for {
		if _, _, ok := e.PersistHalted(); ok {
			break
		}
		if time.Now().After(pollDeadline) {
			t.Fatalf("engine never halted within 10s -- precondition unmet, cannot test tickLoop's post-halt behaviour")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Drain whatever result(s) already arrived (the halting tick's own
	// rejection, plus possibly the earlier accepted tick) before measuring
	// what comes AFTER the halt is already established.
	drain := time.After(200 * time.Millisecond)
drainLoop:
	for {
		select {
		case <-tickResults:
		case <-drain:
			break drainLoop
		}
	}

	// Now measure a GENEROUS window (well beyond several tickIntervals): a
	// broken tickLoop keeps sending -> keeps producing halt-rejection
	// results; the fixed one stops sending entirely, so nothing more
	// arrives. This window can only ever UNDER-count a real bug (never
	// fabricate a false failure), so it is safe under CI load.
	further := 0
	after := time.After(2 * time.Second)
countLoop:
	for {
		select {
		case <-tickResults:
			further++
		case <-after:
			break countLoop
		}
	}
	if further != 0 {
		t.Fatalf("FINDING NOT FIXED: tickLoop kept sending AdvanceTicks after the halt -- %d further CommandResult(s) for the tick driver's correlation ID arrived in a 2s window after the halt was already established", further)
	}

	cancel()
	<-tickDone
	<-loopDone
	<-pumpDone
	_ = transport.Close()
}

// TestBUG472_RealSubscriberObservesHaltOverTheWire is the #3(b) end-to-end
// proof: a client that only ever talks to the transport (SendCommand to
// Subscribe, then reads Deltas() -- exactly wsserver's own connection
// shape, internal/protocol/wsserver) observes PersistHalted=true on the
// pushed engine.status delta, without the subscriber ever sending another
// command of its own after subscribing.
func TestBUG472_RealSubscriberObservesHaltOverTheWire(t *testing.T) {
	// failCall:2 halts on the first AdvanceTicks the tick driver sends --
	// call 1 is the client's own Subscribe (also journaled: accept()
	// journals every accepted command, not only AdvanceTicks), which must
	// succeed so there is a live subscriber in place before the halt.
	dir := t.TempDir()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	journaler := &attackFailingAppendStore{Store: disk, failCall: 2}
	city := persist.CityKey{TenantID: persistTenantID, CityID: "wirehalt"}

	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, journaler, city, &discardWriter{})
	if err != nil {
		t.Fatalf("wireAndRehydrate: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pumpDone, err := e.StartSubscriptionPump(ctx, transport)
	if err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	tickCorrID := string(protocol.NewCorrelationID())
	loopDone := startCommandLoop(ctx, e, transport, comp, journaler, city, 0, tickCorrID, &discardWriter{})

	// The client subscribes exactly the way a real WS client would: send a
	// Subscribe command over the transport, await ITS OWN result, then only
	// ever read Deltas() -- it never inspects e or e.subs directly. This
	// happens BEFORE the tick driver starts (started right after), so the
	// subscription is guaranteed to land before the halting AdvanceTicks
	// does -- otherwise, with failCall:1, the tick driver's own first
	// AdvanceTicks could halt the engine before the subscriber ever gets a
	// chance to subscribe, which would make this test about Subscribe's own
	// halt-rejection rather than about what a PRE-EXISTING subscriber sees.
	subRes := sendAndAwait(t, transport, protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: core.EngineStatusViewName},
	})
	if !subRes.Accepted {
		t.Fatalf("Subscribe: rejected, error = %+v", subRes.Error)
	}
	tickDone := tickLoop(ctx, e, transport, 20*time.Millisecond, tickCorrID)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case d := <-transport.Deltas():
			var view core.EngineStatusView
			if err := json.Unmarshal(d.Patch, &view); err != nil {
				t.Fatalf("Unmarshal delta patch: %v (patch = %s)", err, d.Patch)
			}
			if view.PersistHalted {
				if view.PersistHaltError == nil {
					t.Fatal("delta reports PersistHalted=true but PersistHaltError is nil")
				}
				if view.PersistHaltError.Code != core.ErrSimulationPersistHalted {
					t.Errorf("PersistHaltError.Code = %q, want %q", view.PersistHaltError.Code, core.ErrSimulationPersistHalted)
				}
				// Success: the subscriber observed the halt purely through
				// the transport's own delta stream, having sent nothing
				// after its own Subscribe.
				cancel()
				<-tickDone
				<-loopDone
				<-pumpDone
				_ = transport.Close()
				return
			}
		case <-deadline:
			t.Fatal("no delta with PersistHalted=true arrived within 3s -- the real transport-connected subscriber never observed the halt")
		}
	}
}

// discardWriter is a minimal io.Writer that discards everything, for the
// wireAndRehydrate/startCommandLoop log parameters this test does not care
// about reading back.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
