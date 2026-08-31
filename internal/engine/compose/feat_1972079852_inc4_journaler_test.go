package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079852 increment 4 -- wire the real journaler into the composed
// engine. inc3 (a7f55fe) gave engine.core the CommandJournaler seam and the
// SetCommandJournaler boot-only setter, but nothing called it from the
// composition root -- so a command accepted by the REAL, RUNNING metroserve
// engine (composed via Wire, not a bare core.NewEngine() the way
// engine.core's own inc3 tests build one) was never actually journaled.
// This is the "wired, not just built" tripwire (docs/METROPOLIS-MASTER
// built-but-not-wired defect class): these tests boot through compose.Wire
// exactly as cmd/metropolis and internal/harness/headless do, not through
// core.NewEngine(WithCommandJournaler(...)) directly.
//
// MUTATION-PROVE (per the dispatch brief): commenting out compose.go's
// `e.SetCommandJournaler(journaler)` call turns
// TestWire_AcceptedCommandIsJournaled RED -- the composed engine still
// builds and every other compose test still passes (SetGameplayCommandHandler
// alone doesn't journal), which is exactly the built-not-wired shape this
// test exists to catch.

func setSpeedCmd(corrID protocol.CorrelationID, speed int) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corrID,
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: speed},
	}
}

// TestWire_AcceptedCommandIsJournaled proves the composed engine's
// Composition.Journaler() is the same *replay.Recorder e actually records
// accepted commands into -- not merely a Recorder compose happens to
// construct and never wire up.
func TestWire_AcceptedCommandIsJournaled(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	rec, ok := comp.Journaler().(*replay.Recorder)
	if !ok {
		t.Fatalf("Composition.Journaler() = %T, want *replay.Recorder", comp.Journaler())
	}

	corrID := protocol.NewCorrelationID()
	// SetSpeed(2) is a valid multiplier (protocol.ValidSpeedMultipliers) --
	// this is a genuinely ACCEPTED command through the real composed
	// engine, not a gameplay command routed through the build/world
	// handler (that path is proved separately by the existing
	// handleGameplay tests; this file only proves the journaler wiring).
	result := e.HandleCommand(setSpeedCmd(corrID, 2))
	if !result.Accepted {
		t.Fatalf("SetSpeed(2) on composed engine: rejected, error = %+v", result.Error)
	}

	records, err := rec.Records()
	if err != nil {
		t.Fatalf("Records(): %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("Records() count = %d, want 1 (the composed engine did not journal the accepted command -- built but not wired)", len(records))
	}
	if records[0].Kind != "command" {
		t.Errorf("Records()[0].Kind = %q, want %q", records[0].Kind, "command")
	}
}

// TestWire_RejectedCommandIsNotJournaled proves a command the composed
// engine rejects is absent from the journal -- the same accepted-only
// discipline engine.core's own inc3 tests prove at the bare-engine level,
// re-proved here through the real composed path.
func TestWire_RejectedCommandIsNotJournaled(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	rec, ok := comp.Journaler().(*replay.Recorder)
	if !ok {
		t.Fatalf("Composition.Journaler() = %T, want *replay.Recorder", comp.Journaler())
	}

	// SetSpeed(3) is not a valid multiplier -- rejected before ever
	// reaching accept()/journalAccepted.
	result := e.HandleCommand(setSpeedCmd(protocol.NewCorrelationID(), 3))
	if result.Accepted {
		t.Fatal("SetSpeed(3) on composed engine: accepted, want rejected")
	}

	records, err := rec.Records()
	if err != nil {
		t.Fatalf("Records(): %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("Records() count = %d, want 0 (rejected commands must not be journaled)", len(records))
	}
}

// TestWire_JournalerDeterministicAcrossBoots proves two independent
// compose.Wire boots, fed the identical accepted-command sequence, produce
// byte-identical journal records -- the determinism property replay
// depends on, re-proved at the composed level (engine.core's own inc3
// tests already prove it at the bare-engine level).
func TestWire_JournalerDeterministicAcrossBoots(t *testing.T) {
	corrID := protocol.CorrelationID("fixed-correlation-id-for-determinism")

	run := func() []byte {
		e := core.NewEngine(core.WithWorldSeed(7), core.WithPoolSize(1))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		rec := comp.Journaler().(*replay.Recorder)
		if result := e.HandleCommand(setSpeedCmd(corrID, 2)); !result.Accepted {
			t.Fatalf("SetSpeed(2): rejected, error = %+v", result.Error)
		}
		records, err := rec.Records()
		if err != nil {
			t.Fatalf("Records(): %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("Records() count = %d, want 1", len(records))
		}
		return records[0].Data
	}

	first := run()
	second := run()
	if string(first) != string(second) {
		t.Fatalf("journal record bytes differ across two Wire boots with the identical command:\n  first:  %x\n  second: %x", first, second)
	}
}

// TestWire_DefaultJournalerCanBeOverridden proves Deps.CommandJournaler
// (the test/override seam) is honoured -- a caller-supplied journaler
// replaces the default *replay.NewRecorder(), the same override shape
// Deps.LoadMarket already gives callers for market construction.
func TestWire_DefaultJournalerCanBeOverridden(t *testing.T) {
	spy := &spyComposeJournaler{}
	e := core.NewEngine(core.WithWorldSeed(1), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CommandJournaler: spy})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp.Journaler() != core.CommandJournaler(spy) {
		t.Fatalf("Composition.Journaler() = %v, want the injected spy (Deps.CommandJournaler override not honoured)", comp.Journaler())
	}
	corrID := protocol.NewCorrelationID()
	if result := e.HandleCommand(setSpeedCmd(corrID, 2)); !result.Accepted {
		t.Fatalf("SetSpeed(2): rejected, error = %+v", result.Error)
	}
	if len(spy.observed) != 1 {
		t.Fatalf("spy.observed count = %d, want 1 (Deps.CommandJournaler override was not wired into the composed engine)", len(spy.observed))
	}
}

type spyComposeJournaler struct {
	observed []protocol.Command
}

func (s *spyComposeJournaler) ObserveCommand(cmd protocol.Command) error {
	s.observed = append(s.observed, cmd)
	return nil
}
