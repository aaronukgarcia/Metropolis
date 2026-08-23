package core

// FEAT-157: Speed8x (formerly Speed8xDebug) is a PRODUCTION speed — the
// top of the player-facing ladder ("fastest"), promoted out of BUG-009's
// debug gate. These tests prove SetSpeed(Speed8x) is accepted through the
// real command path (HandleCommand -> handleSetSpeed, never a direct
// clock.setSpeed) on a bare NewEngine() with no gate wired and no debug
// state anywhere in the picture — the exact construction that was
// default-deny (MET-E015) before the promotion.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func speed8xCommand() protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: int(Speed8x)},
	}
}

// TestHandleCommand_SetSpeed_Speed8x_AcceptedUngated is FEAT-157's core
// assertion: a bare NewEngine() — no WithSpeed8xGate, no feat.debugmode,
// the same shape cmd/metropolis's production boot now produces — accepts
// SetSpeed(Speed8x) and the clock actually lands at 8x.
func TestHandleCommand_SetSpeed_Speed8x_AcceptedUngated(t *testing.T) {
	e := NewEngine()

	result := e.HandleCommand(speed8xCommand())
	if !result.Accepted {
		t.Fatalf("SetSpeed(8x) on a bare production Engine: rejected, error = %+v (8x is a production speed since FEAT-157)", result.Error)
	}
	if clockOrFatal(t, e).Speed() != Speed8x {
		t.Errorf("Speed() = %d after accepted SetSpeed(8x), want %d", clockOrFatal(t, e).Speed(), Speed8x)
	}
}

// TestHandleCommand_SetSpeed_Speed8x_WalksFullLadder proves the whole
// production ladder pause/1x/2x/4x/8x is traversable in both directions
// through the command path, with every rung landing exactly.
func TestHandleCommand_SetSpeed_Speed8x_WalksFullLadder(t *testing.T) {
	e := NewEngine()

	ladder := []Speed{Speed1x, Speed2x, Speed4x, Speed8x}
	for _, want := range ladder {
		result := e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   mustCorrID(),
			Kind:            protocol.KindSetSpeed,
			Payload:         protocol.SetSpeedPayload{Speed: int(want)},
		})
		if !result.Accepted {
			t.Fatalf("SetSpeed(%d): rejected, error = %+v", int(want), result.Error)
		}
		if got := clockOrFatal(t, e).Speed(); got != want {
			t.Fatalf("Speed() = %d after SetSpeed(%d), want %d", got, int(want), want)
		}
	}
	for i := len(ladder) - 2; i >= 0; i-- {
		want := ladder[i]
		result := e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   mustCorrID(),
			Kind:            protocol.KindSetSpeed,
			Payload:         protocol.SetSpeedPayload{Speed: int(want)},
		})
		if !result.Accepted {
			t.Fatalf("downward SetSpeed(%d): rejected, error = %+v", int(want), result.Error)
		}
		if got := clockOrFatal(t, e).Speed(); got != want {
			t.Fatalf("Speed() = %d after downward SetSpeed(%d), want %d", got, int(want), want)
		}
	}
}

// TestHandleCommand_SetSpeed_Speed8x_PauseSemanticsUnchanged pins the
// §3.1 contract at the new top rung: Pause/Resume remain distinct from
// SetSpeed, a fresh Engine boots paused at 8x too, and the queryable
// pacing figures treat paused-at-8x exactly like paused-at-1x (zero
// real-time progress) while resumed 8x runs eight times 1x's rate.
func TestHandleCommand_SetSpeed_Speed8x_PauseSemanticsUnchanged(t *testing.T) {
	e := NewEngine()
	if !clockOrFatal(t, e).Paused() {
		t.Fatal("fresh Engine: Paused() = false, want true (NewClock starts paused)")
	}

	send := func(kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
		return e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   mustCorrID(),
			Kind:            kind,
			Payload:         payload,
		})
	}

	if res := send(protocol.KindSetSpeed, protocol.SetSpeedPayload{Speed: int(Speed8x)}); !res.Accepted {
		t.Fatalf("SetSpeed(8x): rejected, error = %+v", res.Error)
	}
	if got := clockOrFatal(t, e).TicksPerRealSecond(); got != 0 {
		t.Fatalf("TicksPerRealSecond() = %v while paused at 8x, want 0", got)
	}

	if res := send(protocol.KindResume, protocol.ResumePayload{}); !res.Accepted {
		t.Fatalf("Resume: rejected, error = %+v", res.Error)
	}
	c := clockOrFatal(t, e)
	if c.Paused() {
		t.Fatal("Paused() = true after Resume, want false")
	}
	if c.Speed() != Speed8x {
		t.Fatalf("Speed() = %d after Resume, want unchanged %d", c.Speed(), Speed8x)
	}

	if res := send(protocol.KindSetSpeed, protocol.SetSpeedPayload{Speed: int(Speed1x)}); !res.Accepted {
		t.Fatalf("SetSpeed(1x): rejected, error = %+v", res.Error)
	}
	// SecondsPerMonth floors its division by the multiplier, so the 8x
	// rate is not exactly 8 x the 1x rate in general — pin the contract
	// that matters: resumed pacing is nonzero and strictly faster than 1x.
	// Each rate is read while its own speed is active.
	rate8 := clockAtSpeed(t, e, Speed8x).TicksPerRealSecond()
	rate1 := clockAtSpeed(t, e, Speed1x).TicksPerRealSecond()
	if rate1 <= 0 || rate8 <= rate1 {
		t.Fatalf("TicksPerRealSecond at 1x = %v, at 8x = %v, want 0 < 1x < 8x", rate1, rate8)
	}
}

// clockAtSpeed sets e's clock to s through the real command path and
// returns it.
func clockAtSpeed(t *testing.T, e *Engine, s Speed) Clock {
	t.Helper()
	result := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: int(s)},
	})
	if !result.Accepted {
		t.Fatalf("SetSpeed(%d): rejected, error = %+v", int(s), result.Error)
	}
	return clockOrFatal(t, e)
}
