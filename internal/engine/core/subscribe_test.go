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
	defer func() { _ = transport.Close() }()

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

	// Drive a few more commands, each of which should signal the pump.
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

	var lastSeq uint64
	var sawFirstDeltaCorrID bool
	deadline := time.After(3 * time.Second)
	received := 0
	for received < 2 { // Subscribe's own delta, plus at least one from an AdvanceTicks signal
		select {
		case d := <-transport.Deltas():
			if d.Seq <= lastSeq {
				t.Fatalf("Delta.Seq = %d, want > previous %d (monotonic)", d.Seq, lastSeq)
			}
			lastSeq = d.Seq
			if d.Seq == 1 {
				if d.CorrelationID != subCorrID {
					t.Errorf("first delta CorrelationID = %q, want %q (echoes Subscribe)", d.CorrelationID, subCorrID)
				}
				sawFirstDeltaCorrID = true
			}
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
