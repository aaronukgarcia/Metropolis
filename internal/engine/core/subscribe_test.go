package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestSubscribe_UnknownView_Rejected(t *testing.T) {
	e := NewEngine()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "f1.viewport"},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("Subscribe(f1.viewport): accepted, want rejected (v1 only serves engine.status)")
	}
	wantPlaceholderCode(t, result.Error, ErrUnknownView)
}

func TestSubscribe_MalformedViewName_Rejected(t *testing.T) {
	e := NewEngine()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "NOT VALID"},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("Subscribe(malformed): accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidViewName)
}

func TestUnsubscribe_UnknownID_Rejected(t *testing.T) {
	e := NewEngine()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindUnsubscribe,
		Payload:         protocol.UnsubscribePayload{SubscriptionID: "sub-999"},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("Unsubscribe(unknown): accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrUnknownSubscription)
}

// TestSubscription_EngineStatusDeltas_MonotonicSeq subscribes to
// engine.status, drives a few AdvanceTicks commands (each of which
// signals the subscription pump), and asserts the deltas received have
// strictly monotonically increasing Seq starting at 1 (AC-7 + the
// dispatch brief's subscription test).
func TestSubscription_EngineStatusDeltas_MonotonicSeq(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	// Deliberately no transport.Close() here (unlike other tests in this
	// package): this test's whole point is proving the pump keeps
	// producing deltas across several driven commands, so by design the
	// pump goroutine can still be mid-SendDelta after this test's own
	// assertions are satisfied (e.g. a delta from the 2nd/3rd AdvanceTicks
	// signal that this test never needed to consume). protocol.Close()
	// closes the Results/Events/Deltas channels with no synchronisation
	// against an in-flight send from that goroutine (trySendEvictOldest's
	// `<-closed` check in internal/protocol/transport.go is TOCTOU against
	// a concurrent Close) — calling it here reliably trips `-race` on a
	// send-vs-close race, in a package this brief is out of scope to
	// touch. Cancelling ctx (below) is sufficient: both goroutines this
	// test starts (StartSubscriptionPump's pump, RunCommandLoop) select on
	// ctx.Done() and exit on their own; nothing here holds an OS resource
	// that leaking the channels would waste. Flagging the transport.go
	// Close/send race to Bill as a separate finding rather than fixing it
	// in this brief.
	e := NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.StartSubscriptionPump(ctx, transport)
	go e.RunCommandLoop(ctx, transport)

	subCorrID := mustCorrID()
	subCmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   subCorrID,
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
	}
	if err := transport.SendCommand(subCmd); err != nil {
		t.Fatalf("SendCommand(Subscribe): %v", err)
	}
	var subResult protocol.CommandResult
	select {
	case subResult = <-transport.Results():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Subscribe result")
	}
	if !subResult.Accepted {
		t.Fatalf("Subscribe rejected: %+v", subResult.Error)
	}

	// Wait for Subscribe's own delta (Seq==1) to be fully delivered
	// before driving any further commands. This is a deliberate
	// synchronisation point, not a longer timeout: signalSubscriptionPump
	// (commands.go) is a non-blocking, coalescing send on a
	// capacity-1 channel, and StartSubscriptionPump's loop only
	// re-enters its `select` (ready to accept the *next* signal) once
	// PublishEngineStatus has finished sending every target's delta —
	// see subscribe.go's PublishEngineStatus doc comment. Receiving
	// delta #1 here proves that round of computation is complete and
	// the signal channel has been drained, so a signal sent by any
	// command from this point on is guaranteed to be queued (not
	// silently coalesced into a round that already happened before the
	// signal existed). Without this synchronisation, a sufficiently
	// starved pump goroutine can have every AdvanceTicks signal below
	// collapse into the same single pending slot as Subscribe's own
	// signal — by design (T-SUBSCR coalesces rapid signals into one
	// recompute) — leaving only one delta ever pushed for the whole
	// test, which is what CI observed. Waiting for delta #1 first
	// removes that ambiguity without weakening what the test proves:
	// it still asserts monotonic Seq across at least two independently
	// observed deltas below.
	var lastSeq uint64
	var sawFirstDeltaCorrID bool
	firstDeadline := time.After(3 * time.Second)
	select {
	case d := <-transport.Deltas():
		if d.Seq != 1 {
			t.Fatalf("first delta Seq = %d, want 1", d.Seq)
		}
		lastSeq = d.Seq
		if d.CorrelationID != subCorrID {
			t.Errorf("first delta CorrelationID = %q, want %q (echoes Subscribe)", d.CorrelationID, subCorrID)
		}
		sawFirstDeltaCorrID = true
		var view EngineStatusView
		if err := json.Unmarshal(d.Patch, &view); err != nil {
			t.Fatalf("unmarshalling delta patch: %v", err)
		}
	case <-firstDeadline:
		t.Fatal("timed out waiting for Subscribe's own delta (Seq==1)")
	}

	// Now drive a few more commands, each of which should signal the
	// pump. Because the pump is provably idle again (drained, above),
	// at least one of these signals is guaranteed to be queued and
	// eventually drained into a fresh PublishEngineStatus call — even
	// under the same starvation that a benchmark running in this
	// package can induce.
	for i := 0; i < 3; i++ {
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   mustCorrID(),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		}
		if err := transport.SendCommand(cmd); err != nil {
			t.Fatalf("SendCommand(AdvanceTicks %d): %v", i, err)
		}
		select {
		case r := <-transport.Results():
			if !r.Accepted {
				t.Fatalf("AdvanceTicks %d rejected: %+v", i, r.Error)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for AdvanceTicks %d result", i)
		}
	}

	deadline := time.After(10 * time.Second)
	received := 1      // Subscribe's own delta, received above
	for received < 2 { // plus at least one from an AdvanceTicks signal
		select {
		case d := <-transport.Deltas():
			if d.Seq <= lastSeq {
				t.Fatalf("Delta.Seq = %d, want > previous %d (monotonic)", d.Seq, lastSeq)
			}
			lastSeq = d.Seq
			var view EngineStatusView
			if err := json.Unmarshal(d.Patch, &view); err != nil {
				t.Fatalf("unmarshalling delta patch: %v", err)
			}
			received++
		case <-deadline:
			t.Fatalf("timed out waiting for deltas, received %d so far", received)
		}
	}
	if !sawFirstDeltaCorrID {
		t.Error("never observed Seq==1 delta echoing the Subscribe command's correlation ID")
	}
}

func TestEngineStatusView_ReflectsRegisteredModules(t *testing.T) {
	e := NewEngine()
	if err := e.Registry().Register("test.mod", nil, fakeModule{name: "test.mod", version: "1.0.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	view := e.EngineStatusView()
	if len(view.Modules) != 1 {
		t.Fatalf("Modules = %v, want 1 entry", view.Modules)
	}
	if view.Modules[0].Key != "test.mod" {
		t.Errorf("Modules[0].Key = %q, want %q", view.Modules[0].Key, "test.mod")
	}
}
