package menu

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestNewGame_IssuesCommandCarryingSeedAndDebugFlag is MEN-5's binding
// check: the new-game setup form takes a seed and a debug flag and issues
// a new-game protocol.Command carrying exactly those two fields (ASM-255:
// the field set is not expanded).
func TestNewGame_IssuesCommandCarryingSeedAndDebugFlag(t *testing.T) {
	s := New("corr-newgame")
	var got protocol.Command
	if err := s.NewGame("12345", true, func(cmd protocol.Command) error { got = cmd; return nil }); err != nil {
		t.Fatalf("NewGame(): %v", err)
	}

	p, ok := got.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("NewGame payload = %T, want protocol.DebugPayload", got.Payload)
	}
	if p.Op != opNewGame {
		t.Errorf("NewGame op = %q, want %q", p.Op, opNewGame)
	}
	if p.Args["seed"] != "12345" {
		t.Errorf("NewGame seed arg = %q, want 12345", p.Args["seed"])
	}
	if p.Args["debug"] != "true" {
		t.Errorf("NewGame debug arg = %q, want true", p.Args["debug"])
	}

	req, have := s.LastNewGameRequest()
	if !have || req.Seed != "12345" || !req.Debug {
		t.Fatalf("LastNewGameRequest() = %+v have=%v, want {seed 12345 debug true}", req, have)
	}
}

func TestNewGame_DebugFalseCarriesFalseFlag(t *testing.T) {
	s := New("corr-newgame-2")
	var got protocol.Command
	if err := s.NewGame("7", false, func(cmd protocol.Command) error { got = cmd; return nil }); err != nil {
		t.Fatalf("NewGame(): %v", err)
	}
	p := got.Payload.(protocol.DebugPayload)
	if p.Args["debug"] != "false" {
		t.Errorf("NewGame debug arg = %q, want false", p.Args["debug"])
	}
	if p.Args["seed"] != "7" {
		t.Errorf("NewGame seed arg = %q, want 7", p.Args["seed"])
	}
}

// TestNewGame_SendErrorNotRecorded proves a failed send does not record the
// request as submitted (GR#1: a rejected action is "not sent", never
// treated as a success).
func TestNewGame_SendErrorNotRecorded(t *testing.T) {
	s := New("corr-newgame-err")
	boom := protocol.ErrCommandQueueFull
	if err := s.NewGame("9", false, func(protocol.Command) error { return boom }); err == nil {
		t.Fatalf("NewGame() returned nil despite a failing send")
	}
	if _, have := s.LastNewGameRequest(); have {
		t.Fatalf("LastNewGameRequest() have = true after a failed send — must not be recorded")
	}
}
