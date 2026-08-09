package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func mustCorrID() protocol.CorrelationID { return protocol.NewCorrelationID() }

// wantPlaceholderCode asserts that ref names this package's placeholder
// code, either directly (once a maintainer registers it in
// data/errors.json) or via the current MET-F003 "unregistered code"
// fallback (errors.go's doc comment; GR#7's degrade-loudly path) whose
// Display embeds the originally requested code. This keeps these tests
// correct both today (codes unregistered) and after registration
// (see /new-error) with no test changes required either way.
func wantPlaceholderCode(t *testing.T, ref *protocol.ErrorRef, code string) {
	t.Helper()
	if ref == nil {
		t.Fatalf("ErrorRef is nil, want code %s (or MET-F003 fallback naming it)", code)
	}
	if ref.Code == code {
		return
	}
	if ref.Code == "MET-F003" && strings.Contains(ref.Display, code) {
		return
	}
	t.Errorf("ErrorRef = %+v, want code %s (or MET-F003 fallback naming it)", ref, code)
}

func TestHandleCommand_AdvanceTicks(t *testing.T) {
	e := NewEngine()
	corrID := mustCorrID()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 3},
	}
	result := e.HandleCommand(cmd)
	if !result.Accepted {
		t.Fatalf("AdvanceTicks(3): rejected, error = %+v", result.Error)
	}
	if result.CorrelationID != corrID {
		t.Errorf("CorrelationID = %q, want %q", result.CorrelationID, corrID)
	}
	if clockOrFatal(t, e).Tick() != 3 {
		t.Errorf("Tick() = %d, want 3", clockOrFatal(t, e).Tick())
	}
}

func TestHandleCommand_AdvanceTicks_InvalidN(t *testing.T) {
	e := NewEngine()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: -5},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("AdvanceTicks(-5): accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidAdvanceTicks)
}

func TestHandleCommand_SetSpeed(t *testing.T) {
	e := NewEngine()
	valid := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 4},
	}
	result := e.HandleCommand(valid)
	if !result.Accepted {
		t.Fatalf("SetSpeed(4): rejected, error = %+v", result.Error)
	}
	if clockOrFatal(t, e).Speed() != Speed4x {
		t.Errorf("Speed() = %d, want %d", clockOrFatal(t, e).Speed(), Speed4x)
	}

	invalid := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 3},
	}
	result = e.HandleCommand(invalid)
	if result.Accepted {
		t.Fatal("SetSpeed(3): accepted, want rejected (3 is not a documented multiplier)")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidSpeed)
}

func TestHandleCommand_PauseResume_Idempotent(t *testing.T) {
	e := NewEngine()
	pause := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	for i := 0; i < 2; i++ {
		result := e.HandleCommand(pause)
		if !result.Accepted {
			t.Fatalf("Pause call %d: rejected, error = %+v", i, result.Error)
		}
	}
	if !clockOrFatal(t, e).Paused() {
		t.Error("Paused() = false after Pause commands, want true")
	}

	resume := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: mustCorrID(), Kind: protocol.KindResume, Payload: protocol.ResumePayload{}}
	for i := 0; i < 2; i++ {
		result := e.HandleCommand(resume)
		if !result.Accepted {
			t.Fatalf("Resume call %d: rejected, error = %+v", i, result.Error)
		}
	}
	if clockOrFatal(t, e).Paused() {
		t.Error("Paused() = true after Resume commands, want false")
	}
}

func TestHandleCommand_InspectEntity_Debug_PlaceholderAccepted(t *testing.T) {
	e := NewEngine()
	inspect := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindInspectEntity,
		Payload:         protocol.InspectEntityPayload{EntityRef: "citizen:1"},
	}
	if result := e.HandleCommand(inspect); !result.Accepted {
		t.Errorf("InspectEntity: rejected, error = %+v", result.Error)
	}

	debug := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: "noop"},
	}
	if result := e.HandleCommand(debug); !result.Accepted {
		t.Errorf("Debug: rejected, error = %+v", result.Error)
	}
}

func TestHandleCommand_InvalidEnvelope(t *testing.T) {
	e := NewEngine()
	// Missing CorrelationID fails protocol.Command.Validate.
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("HandleCommand with empty CorrelationID: accepted, want rejected")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidEnvelope)
}

// TestRunCommandLoop_RoundTripOverInProcTransport drives the command
// loop over a real protocol.InProcTransport end to end: send an
// AdvanceTicks command from the "UI side", read back the CommandResult
// from the "engine side", and confirm the correlation ID echoes.
func TestRunCommandLoop_RoundTripOverInProcTransport(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()

	e := NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.RunCommandLoop(ctx, transport)

	corrID := mustCorrID()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 7},
	}
	if err := transport.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	select {
	case result := <-transport.Results():
		if !result.Accepted {
			t.Fatalf("result rejected: %+v", result.Error)
		}
		if result.CorrelationID != corrID {
			t.Errorf("CorrelationID = %q, want %q (echo)", result.CorrelationID, corrID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CommandResult")
	}

	if clockOrFatal(t, e).Tick() != 7 {
		t.Errorf("Tick() = %d, want 7", clockOrFatal(t, e).Tick())
	}
}
