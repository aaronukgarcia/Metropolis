package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// --- BUG-310 independent Destructive round (GR#23) ---
// Permanent regression tests for the core/commands.go claims: a valid
// CorrelationID must actually reach the constructed registry error (not
// be silently dropped to the MISSING-CORRELATION-ID placeholder), and an
// arbitrary (non-registry) error handed to toErrorRef must not be
// mislabeled as ErrUnhandledCommandKind (MET-E009) -- a code whose own
// registry message literally reads "HandleCommand received unhandled
// command kind {kind}", which is a false diagnostic for an unrelated
// failure.

// TestRegression_InvalidEnvelope_CorrelationIDPassesThrough proves that
// when cmd.Validate() fails for a reason OTHER than a missing/invalid
// CorrelationID (here: wrong ProtocolVersion), the caller's real,
// well-formed CorrelationID reaches the constructed ErrInvalidEnvelope
// error -- it must not be silently dropped to errs' empty-correlation
// placeholder.
func TestRegression_InvalidEnvelope_CorrelationIDPassesThrough(t *testing.T) {
	e := NewEngine()
	corrID := mustCorrID()
	cmd := protocol.Command{
		ProtocolVersion: "bogus-version", // fails Validate before CorrelationID is even checked... except CorrelationID IS valid, so this exercises "Validate failed, but CorrelationID was fine"
		CorrelationID:   corrID,
		IssuedAtTick:    0,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("HandleCommand with bad ProtocolVersion: accepted, want rejected")
	}
	if result.Error == nil {
		t.Fatal("result.Error is nil, want ErrInvalidEnvelope")
	}
	if strings.Contains(result.Error.Display, "MISSING-CORRELATION-ID") {
		t.Fatalf("ErrInvalidEnvelope was constructed with the empty-correlation placeholder despite cmd carrying a valid CorrelationID: %s", result.Error.Display)
	}
	if !strings.Contains(result.Error.Display, string(corrID)) {
		t.Fatalf("ErrInvalidEnvelope does not carry the command's real CorrelationID %q: %s", corrID, result.Error.Display)
	}
}

// TestRegression_ToErrorRef_DoesNotMislabelArbitraryError proves the
// fallback branch of toErrorRef (reached when a handler returns a plain,
// non-*errs.E error) does not tag that error as MET-E009
// (ErrUnhandledCommandKind) -- a code whose registered message is
// specifically "HandleCommand received unhandled command kind {kind}",
// which is actively misleading for a gameplay handler failure that has
// nothing to do with command-kind dispatch.
func TestRegression_ToErrorRef_DoesNotMislabelArbitraryError(t *testing.T) {
	e := NewEngine()
	plainErr := errors.New("gameplay handler: insufficient funds")
	if err := e.SetGameplayCommandHandler(func(cmd protocol.Command) error {
		return plainErr
	}); err != nil {
		t.Fatalf("SetGameplayCommandHandler: %v", err)
	}

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: protocol.CellRef{X: 1, Y: 1}},
	}
	result := e.HandleCommand(cmd)
	if result.Accepted {
		t.Fatal("HandleCommand: accepted, want rejected (handler returned an error)")
	}
	if result.Error == nil {
		t.Fatal("result.Error is nil, want a populated ErrorRef")
	}
	if result.Error.Code == ErrUnhandledCommandKind {
		t.Fatalf("toErrorRef mislabeled an arbitrary gameplay-handler error as %s (%q) -- the registered message for that code claims an unhandled COMMAND KIND, which is false here: %s",
			ErrUnhandledCommandKind, "HandleCommand received unhandled command kind {kind}", result.Error.Display)
	}
}
