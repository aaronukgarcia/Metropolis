package core

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079852 increment 3 -- engine-owns-journal. These tests prove the
// four lead-default rulings on the proposal's open inc3 questions (Aaron not
// yet consulted; see commands.go's journalAccepted doc comment for the full
// rulings):
//
//  1. TIMING: journaled from accept(), after the command is decided accepted.
//  2. SIDE-EFFECT ORDER: journal-then-apply is a REPLAY-time property, not
//     asserted directly here (that belongs to a full replay-driver test,
//     out of scope for engine.core's own package) -- what IS asserted here
//     is that recording is deterministic byte-for-byte, which is the
//     property replay depends on.
//  3. REJECTED COMMANDS are never journaled.
//  4. ERROR POLICY: a failing journaler surfaces MET-E021 (GR#17) without
//     rejecting the already-accepted command and without crashing.
//
// spyJournaler is a minimal CommandJournaler test double: records every
// ObserveCommand call (for asserting accepted-vs-rejected journaling), and
// can be configured to fail every call (for the error-policy test).
type spyJournaler struct {
	observed []protocol.Command
	failWith error
}

func (s *spyJournaler) ObserveCommand(cmd protocol.Command) error {
	s.observed = append(s.observed, cmd)
	if s.failWith != nil {
		return s.failWith
	}
	return nil
}

func pauseCmd(corrID protocol.CorrelationID) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
}

// TestJournalAccepted_AcceptedCommandIsRecorded is the "accepted command IS
// recorded" mutation target: removing journalAccepted's call to
// e.journaler.ObserveCommand (or removing the call to journalAccepted from
// accept()) turns this RED.
func TestJournalAccepted_AcceptedCommandIsRecorded(t *testing.T) {
	spy := &spyJournaler{}
	e := NewEngine(WithCommandJournaler(spy))
	corrID := mustCorrID()
	result := e.HandleCommand(pauseCmd(corrID))
	if !result.Accepted {
		t.Fatalf("Pause: rejected, error = %+v", result.Error)
	}
	if len(spy.observed) != 1 {
		t.Fatalf("ObserveCommand call count = %d, want 1", len(spy.observed))
	}
	if spy.observed[0].CorrelationID != corrID {
		t.Errorf("journaled CorrelationID = %q, want %q", spy.observed[0].CorrelationID, corrID)
	}
	if spy.observed[0].Kind != protocol.KindPause {
		t.Errorf("journaled Kind = %q, want %q", spy.observed[0].Kind, protocol.KindPause)
	}
}

// TestJournalAccepted_RejectedCommandIsNotRecorded is the "rejected command
// is NOT recorded" mutation target: making journalAccepted (or an
// equivalent call) fire from reject() as well as accept() turns this RED.
func TestJournalAccepted_RejectedCommandIsNotRecorded(t *testing.T) {
	spy := &spyJournaler{}
	e := NewEngine(WithCommandJournaler(spy))
	invalidSpeed := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   mustCorrID(),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 3}, // not a valid multiplier
	}
	result := e.HandleCommand(invalidSpeed)
	if result.Accepted {
		t.Fatal("SetSpeed(3): accepted, want rejected")
	}
	if len(spy.observed) != 0 {
		t.Fatalf("ObserveCommand call count = %d, want 0 (rejected commands must not be journaled)", len(spy.observed))
	}
}

// TestJournalAccepted_NilJournalerIsNoOp proves a bare NewEngine() (no
// WithCommandJournaler/SetCommandJournaler) behaves exactly as it did before
// this feature landed -- nil journaler is a documented no-op, not a
// deny-by-default rejection.
func TestJournalAccepted_NilJournalerIsNoOp(t *testing.T) {
	e := NewEngine()
	result := e.HandleCommand(pauseCmd(mustCorrID()))
	if !result.Accepted {
		t.Fatalf("Pause with no journaler configured: rejected, error = %+v", result.Error)
	}
}

// errFakeJournalStorage simulates a Recorder-backing storage failure (e.g.
// ASM-470's durability gap manifesting as an encode/write error).
var errFakeJournalStorage = errors.New("fake journal storage failure")

type failingJournaler struct{}

func (failingJournaler) ObserveCommand(cmd protocol.Command) error {
	return errFakeJournalStorage
}

// TestJournalAccepted_WriteFailureSurfacesRegistryError is the "journal
// write failure surfaces a registry error" mutation target (lead-default
// ruling #4): swallowing journalAccepted's error (e.g. replacing the
// errs.Wrap call with a bare `_ = err` no-op, or removing the call
// entirely) turns this RED, since MET-E021 would never reach errs.Recent().
func TestJournalAccepted_WriteFailureSurfacesRegistryError(t *testing.T) {
	e := NewEngine(WithCommandJournaler(failingJournaler{}))
	corrID := mustCorrID()
	result := e.HandleCommand(pauseCmd(corrID))

	// GR#17 point 1: the command was genuinely accepted and its side
	// effects (the pause) already happened -- a journal-write fault must
	// not retroactively reject it, and protocol.CommandResult.Validate
	// forbids attaching Error when Accepted is true anyway.
	if !result.Accepted {
		t.Fatalf("Pause: rejected due to journal failure, want still accepted (result = %+v)", result)
	}
	if result.Error != nil {
		t.Errorf("CommandResult.Error = %+v, want nil (Accepted=true forbids a non-nil Error, protocol.CommandResult.Validate)", result.Error)
	}

	// GR#17 point 2: the fault must still be surfaced loudly through the
	// registry's own log sink (errs.Recent(), the in-memory ring buffer
	// populated whenever no file sink is configured -- exactly this
	// process's test configuration).
	found := false
	for _, entry := range errs.Recent() {
		if entry.Code == ErrJournalWriteFailed && entry.CorrelationID == string(corrID) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("errs.Recent() does not contain %s for correlationID %q -- journal write failure was not surfaced", ErrJournalWriteFailed, corrID)
	}
}

// TestJournalAccepted_SetCommandJournaler_RejectsAfterSeal proves
// SetCommandJournaler shares RegisterPhaseHook/SetGameplayCommandHandler's
// boot-time-only discipline: once the Engine has sealed (first
// AdvanceTicks), a late SetCommandJournaler call is rejected with
// ErrEngineSealed rather than silently accepted-but-racy.
func TestJournalAccepted_SetCommandJournaler_RejectsAfterSeal(t *testing.T) {
	e := NewEngine(WithPoolSize(1))
	if err := e.AdvanceTicks(string(mustCorrID()), 1); err != nil {
		t.Fatalf("AdvanceTicks(1): %v", err)
	}
	err := e.SetCommandJournaler(&spyJournaler{})
	if err == nil {
		t.Fatal("SetCommandJournaler after seal: nil error, want ErrEngineSealed")
	}
	var e2 *errs.E
	if !errors.As(err, &e2) || e2.Code != ErrEngineSealed {
		t.Errorf("SetCommandJournaler after seal: error = %+v, want code %s", err, ErrEngineSealed)
	}
}

// TestJournalAccepted_Determinism proves the property replay depends on:
// recording the SAME sequence of accepted commands against two independent
// Engine+Recorder pairs produces a byte-identical recorded sequence, and
// decoding each recorded entry back reproduces the original Command
// unchanged. This is the "replay reproduces the same accepted-command
// sequence byte-identically" requirement from the dispatch brief, exercised
// against harness.replay's real Recorder (not a spy) so the engine.core ->
// harness.replay edge itself is proven wired end-to-end, not just the seam
// interface.
func TestJournalAccepted_Determinism(t *testing.T) {
	corrIDs := []protocol.CorrelationID{mustCorrID(), mustCorrID(), mustCorrID()}
	cmds := []protocol.Command{
		pauseCmd(corrIDs[0]),
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrIDs[1], Kind: protocol.KindResume, Payload: protocol.ResumePayload{}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrIDs[2], Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}},
	}

	runOnce := func() [][]byte {
		rec := replay.NewRecorder()
		e := NewEngine(WithCommandJournaler(rec))
		for _, cmd := range cmds {
			result := e.HandleCommand(cmd)
			if !result.Accepted {
				t.Fatalf("HandleCommand(%v): rejected, error = %+v", cmd.Kind, result.Error)
			}
		}
		records, err := rec.Records()
		if err != nil {
			t.Fatalf("Records(): %v", err)
		}
		if len(records) != len(cmds) {
			t.Fatalf("len(records) = %d, want %d", len(records), len(cmds))
		}
		out := make([][]byte, len(records))
		for i, r := range records {
			if r.Kind != string(replay.KindCommand) {
				t.Errorf("records[%d].Kind = %q, want %q", i, r.Kind, replay.KindCommand)
			}
			decoded, err := protocol.DecodeCommand(r.Data)
			if err != nil {
				t.Fatalf("DecodeCommand(records[%d]): %v", i, err)
			}
			if decoded.CorrelationID != cmds[i].CorrelationID || decoded.Kind != cmds[i].Kind {
				t.Errorf("records[%d] decoded = %+v, want CorrelationID=%q Kind=%q", i, decoded, cmds[i].CorrelationID, cmds[i].Kind)
			}
			out[i] = r.Data
		}
		return out
	}

	first := runOnce()
	second := runOnce()
	if len(first) != len(second) {
		t.Fatalf("recorded sequence lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !bytes.Equal(first[i], second[i]) {
			t.Errorf("recorded entry %d not byte-identical across two runs:\n  run1 = %s\n  run2 = %s", i, first[i], second[i])
		}
	}
}
