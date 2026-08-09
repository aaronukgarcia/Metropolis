package core

// BUG-009: proves handleSetSpeed actually consults a debug gate before
// accepting Speed8xDebug, from three angles — no gate wired at all
// (default-deny), a real feat.debugmode gate with debug off (refused),
// and the same real gate with debug on (accepted). Every assertion goes
// through the real command path (HandleCommand -> handleSetSpeed), not
// a direct call to the gate or to clock.setSpeed, per the BUG-009
// dispatch report's requirement 2/3/4.
//
// This file deliberately imports internal/engine/debug (a white-box
// "package core" test, exactly like debug's own
// TestTogglingDebugDoesNotAffectEngineState imports internal/engine/core
// the other direction). Neither package's PRODUCTION source imports the
// other — engine.core only depends on the Speed8xGate function type it
// declares itself (engine.go) — so this is a test-only cross-module
// wire-up, not a GR#20 violation: nothing in commands.go, engine.go, or
// any other non-_test.go file here names feat.debugmode. See
// engine.go's Speed8xGate doc comment for the full reasoning, and
// ASM-* (BUG-009 dispatch report) for the assumption this rests on.
import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func speed8xCommand() protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: int(Speed8xDebug)},
	}
}

func newTestDebugState(t *testing.T) *debug.State {
	t.Helper()
	h := serialize.NewHeader(1, 0, 0, "test")
	return debug.NewState(debug.WithHeader(&h))
}

// TestHandleCommand_SetSpeed_Speed8x_DefaultDeny is requirement 4: an
// Engine built with no Speed8xGate injected at all (a bare NewEngine(),
// exactly what a caller who forgot to wire feat.debugmode would produce)
// refuses Speed8xDebug rather than silently permitting it — the safe
// default a release build must fall back to.
func TestHandleCommand_SetSpeed_Speed8x_DefaultDeny(t *testing.T) {
	e := NewEngine()

	result := e.HandleCommand(speed8xCommand())
	if result.Accepted {
		t.Fatal("SetSpeed(8x) with no gate injected: accepted, want rejected (unsafe default)")
	}
	wantPlaceholderCode(t, result.Error, ErrInvalidSpeed)
	if clockOrFatal(t, e).Speed() != Speed1x {
		t.Errorf("Speed() = %d after a rejected SetSpeed(8x), want unchanged Speed1x (%d)", clockOrFatal(t, e).Speed(), Speed1x)
	}
}

// TestHandleCommand_SetSpeed_Speed8x_RefusedWithDebugOff is requirement
// 2, wired through feat.debugmode's REAL gate (not a bespoke fake) with
// debug left off: HandleCommand rejects a genuine SetSpeed(Speed8xDebug)
// command and the clock's speed does not change. This also proves the
// other v1 speeds are untouched by the gate: 4x is accepted first, then
// the 8x attempt is rejected without disturbing it.
func TestHandleCommand_SetSpeed_Speed8x_RefusedWithDebugOff(t *testing.T) {
	dbg := newTestDebugState(t)
	// Deliberately never call dbg.Enable — debug stays off (AC-2's
	// default), which is the condition under test.
	e := NewEngine(WithSpeed8xGate(dbg.AllowSpeed8x))

	setFour := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: int(Speed4x)},
	})
	if !setFour.Accepted {
		t.Fatalf("SetSpeed(4x) with debug off: rejected, error = %+v (4x must be unaffected by the 8x gate)", setFour.Error)
	}
	if clockOrFatal(t, e).Speed() != Speed4x {
		t.Fatalf("Speed() = %d after SetSpeed(4x), want %d", clockOrFatal(t, e).Speed(), Speed4x)
	}

	result := e.HandleCommand(speed8xCommand())
	if result.Accepted {
		t.Fatal("SetSpeed(8x) with debug off (real feat.debugmode gate): accepted, want rejected")
	}
	if result.Error == nil || result.Error.Code != debug.ErrDebugRequired {
		t.Errorf("SetSpeed(8x) with debug off: error = %+v, want code %s (feat.debugmode's own registry code)", result.Error, debug.ErrDebugRequired)
	}
	if clockOrFatal(t, e).Speed() != Speed4x {
		t.Errorf("Speed() = %d after a rejected SetSpeed(8x), want unchanged %d", clockOrFatal(t, e).Speed(), Speed4x)
	}
}

// TestHandleCommand_SetSpeed_Speed8x_AcceptedWithDebugOn is requirement
// 3: the same real feat.debugmode gate, this time with debug switched
// on via dbg.Enable, accepts a genuine SetSpeed(Speed8xDebug) command
// and the clock's speed actually becomes Speed8xDebug — proving
// engine.core and feat.debugmode fit together end to end, not just that
// engine.core's own default-deny path works.
func TestHandleCommand_SetSpeed_Speed8x_AcceptedWithDebugOn(t *testing.T) {
	dbg := newTestDebugState(t)
	if err := dbg.Enable(debug.SourceFlag, "corr-enable"); err != nil {
		t.Fatalf("dbg.Enable: %v", err)
	}
	e := NewEngine(WithSpeed8xGate(dbg.AllowSpeed8x))

	result := e.HandleCommand(speed8xCommand())
	if !result.Accepted {
		t.Fatalf("SetSpeed(8x) with debug on (real feat.debugmode gate): rejected, error = %+v", result.Error)
	}
	if clockOrFatal(t, e).Speed() != Speed8xDebug {
		t.Errorf("Speed() = %d after accepted SetSpeed(8x), want %d", clockOrFatal(t, e).Speed(), Speed8xDebug)
	}
}
