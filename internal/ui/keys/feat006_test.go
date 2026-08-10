package keys_test

// This file discharges FEAT-033 (AC-20/AC-21): it proves a REAL
// *tcell.EventKey — not a scripted/synthetic sequence, that is
// ui.harness's (MOD-014) job — travels through this package's grammar
// into a genuine protocol.Command sent on a real protocol.Transport. It
// deliberately lives in an external _test package (keys_test) so the
// import of internal/protocol below is visibly a TEST-ONLY dependency —
// production ui.keys code never imports internal/protocol (doc.go's
// standing rule; grep -rn "internal/protocol" internal/ui/keys/*.go,
// excluding _test.go, returns nothing).

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// TestFeat006RealKeyEventReachesRealCommand is the walking skeleton's
// boot key sequence, driven with a genuine tcell.EventKey (constructed
// exactly as tcell itself would deliver one — the same constructor
// internal/harness/uitest's own DSL uses for named keys) through
// KeyGrammar.FeedTcellEvent, resolving to an Action whose Run closure —
// owned entirely by this test, standing in for a real screen's boot
// wiring — constructs and sends a real protocol.Command on a real
// protocol.InProcTransport. No mock transport, no translation layer
// outside this package: the closure below is the ONLY place a
// protocol.Command gets built, and it only runs because FeedTcellEvent
// resolved a real key event to a registered action.
func TestFeat006RealKeyEventReachesRealCommand(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()

	corrID := errs.NewCorrelationID()
	g := keys.NewKeyGrammar(nil, 0, 0, corrID)

	sendPause := func(keys.ActionArgs) {
		_ = transport.SendCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			Kind:            protocol.KindPause,
			Payload:         protocol.PausePayload{},
			CorrelationID:   protocol.CorrelationID(corrID),
		})
	}
	if err := g.Register([]string{"p"}, keys.Action{Name: "pause", Run: sendPause}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// A REAL tcell.EventKey — the same constructor a real terminal's
	// event source hands to core.InputLoop's PollEvent, not a
	// hand-rolled keys.Key literal.
	ev := tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone)

	res := g.FeedTcellEvent(ev)
	if res.Status != keys.Dispatched {
		t.Fatalf("FeedTcellEvent(real 'p') status = %v, want Dispatched", res.Status)
	}

	select {
	case cmd := <-transport.Commands():
		if cmd.Kind != protocol.KindPause {
			t.Fatalf("Kind = %v, want KindPause", cmd.Kind)
		}
		if _, ok := cmd.Payload.(protocol.PausePayload); !ok {
			t.Fatalf("Payload type = %T, want protocol.PausePayload", cmd.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no command arrived on the real Transport within the deadline")
	}
}

// TestFeat006UnboundRealKeyNeverConstructsACommand is the negative half:
// a real key event that resolves to nothing registered must never reach
// the transport at all — proving the translation genuinely gates on
// KeyGrammar's own dispatch decision rather than a caller-side habit of
// always sending something.
func TestFeat006UnboundRealKeyNeverConstructsACommand(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()

	corrID := errs.NewCorrelationID()
	g := keys.NewKeyGrammar(nil, 0, 0, corrID)
	_ = g.Register([]string{"p"}, keys.Action{Name: "pause", Run: func(keys.ActionArgs) {
		_ = transport.SendCommand(protocol.Command{Kind: protocol.KindPause, Payload: protocol.PausePayload{}, ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(corrID)})
	}})

	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone) // never registered
	res := g.FeedTcellEvent(ev)
	if res.Status != keys.NoSuchSequence {
		t.Fatalf("status = %v, want NoSuchSequence", res.Status)
	}

	// FeedTcellEvent's Action.Run (if any) already ran synchronously,
	// inline, before returning (grammar.go's Feed doc comment) — a
	// NoSuchSequence result means Run was never invoked, so nothing was
	// ever sent. A non-blocking check (never a wall-clock wait, BUG-031)
	// is enough to confirm that deterministically.
	select {
	case cmd := <-transport.Commands():
		t.Fatalf("an unbound key produced a command: %+v", cmd)
	default:
		// expected: nothing arrived.
	}
}
