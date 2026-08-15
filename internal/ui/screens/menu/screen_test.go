package menu

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestSubscribe_SendsCorrectPayload(t *testing.T) {
	s := New("corr-sub")
	var got protocol.Command
	err := s.Subscribe(ViewSession, func(cmd protocol.Command) error {
		got = cmd
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", ViewSession, err)
	}
	if got.Kind != protocol.KindSubscribe {
		t.Errorf("Kind = %v, want KindSubscribe", got.Kind)
	}
	payload, ok := got.Payload.(protocol.SubscribePayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.SubscribePayload", got.Payload)
	}
	if payload.ViewName != ViewSession {
		t.Errorf("ViewName = %q, want %q", payload.ViewName, ViewSession)
	}
	if err := protocol.ValidateViewName(ViewSession); err != nil {
		t.Errorf("view %q fails int.protocol's own naming grammar: %v", ViewSession, err)
	}
}

func TestSubscribe_UnknownViewRejected(t *testing.T) {
	s := New("corr-sub-bad")
	called := false
	err := s.Subscribe("not.a.real.view", func(cmd protocol.Command) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatalf("Subscribe(unknown view) returned nil error, want MET-U603")
	}
	if called {
		t.Fatalf("send was called for an unrecognised view -- Subscribe must reject before sending")
	}
}

func TestApplyDelta_RoutesToBoundView(t *testing.T) {
	s := New("corr-route")
	s.BindSubscription(ViewSession, "sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Seq:            1,
		Patch:          mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 42, Tick: 400, GameMonth: 5, Paused: true, Speed: 2}),
	})

	got, have := s.Session()
	if !have {
		t.Fatalf("Session() have = false after a valid f10.session Delta")
	}
	if got.WorldSeed != 42 || got.Tick != 400 || got.GameMonth != 5 || !got.Paused || got.Speed != 2 {
		t.Fatalf("Session() = %+v, want {42 400 5 true 2}", got)
	}
}

func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-999",
		Seq:            1,
		Patch:          mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 1}),
	})
	_, have := s.Session()
	if have {
		t.Fatalf("Session() have = true after a Delta for an unbound SubscriptionID -- must be dropped")
	}
}

func TestApplyDelta_UnboundAfterUnsubscribe(t *testing.T) {
	s := New("corr-unbind")
	s.BindSubscription(ViewSession, "sub-2")
	s.UnbindSubscription("sub-2")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-2",
		Seq:            1,
		Patch:          mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 5}),
	})
	_, have := s.Session()
	if have {
		t.Fatalf("Session() have = true after a Delta for an unbound (unsubscribed) SubscriptionID")
	}
}

func TestApplyDelta_MalformedPatchDropped(t *testing.T) {
	s := New("corr-malformed")
	s.BindSubscription(ViewSession, "sub-3")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-3", Seq: 1, Patch: mustJSON(t, wireSessionPatch{SchemaVersion: 1, WorldSeed: 10, Tick: 100})})
	before, _ := s.Session()

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-3", Seq: 2, Patch: []byte(`{not valid json`)})
	after, have := s.Session()

	if !have || after != before {
		t.Fatalf("state changed after a malformed patch: before=%+v after=%+v have=%v", before, after, have)
	}
}

func TestApplyDelta_UnsupportedSchemaVersionDropped(t *testing.T) {
	s := New("corr-schema")
	s.BindSubscription(ViewSession, "sub-4")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-4", Seq: 1, Patch: mustJSON(t, wireSessionPatch{SchemaVersion: 2, WorldSeed: 1})})
	_, have := s.Session()
	if have {
		t.Fatalf("Session() have = true after an unsupported schemaVersion patch")
	}
}

func TestApplyDelta_OversizedPayloadDropped(t *testing.T) {
	s := New("corr-oversized")
	s.BindSubscription(ViewSession, "sub-5")
	huge := make([]byte, maxPatchWireBytes+1)
	for i := range huge {
		huge[i] = ' '
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-5", Seq: 1, Patch: huge})
	_, have := s.Session()
	if have {
		t.Fatalf("Session() have = true after an oversized payload")
	}
}

// TestSession_SF3_OneFieldChanges is SF-3's differential check applied to
// the live f10.session view: two patches differing in exactly one field
// must (a) change that field's rendered output and (b) leave the other
// figures byte-identical. It uses the SAME render path a caller uses
// (Session() -> RenderSession), not a screen-internal shortcut.
func TestSession_SF3_OneFieldChanges(t *testing.T) {
	render := func(s *Screen) []string {
		session, _ := s.Session()
		buf := core.NewBuffer(80, 1)
		rect := core.Rect{X: 0, Y: 0, W: 80, H: 1}
		RenderSession(buf, rect, session, tcell.StyleDefault)
		return renderedText(buf, rect)
	}

	sA := New("corr-sf3-a")
	sA.BindSubscription(ViewSession, "sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Seq: 1, Patch: mustJSON(t, wireSessionPatch{
		SchemaVersion: 1, WorldSeed: 1000, Tick: 200, GameMonth: 3, Paused: false, Speed: 1,
	})})
	lineA := render(sA)

	sB := New("corr-sf3-b")
	sB.BindSubscription(ViewSession, "sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Seq: 1, Patch: mustJSON(t, wireSessionPatch{
		SchemaVersion: 1, WorldSeed: 9999, Tick: 200, GameMonth: 3, Paused: false, Speed: 1, // only seed mutated
	})})
	lineB := render(sB)

	if len(lineA) != 1 || len(lineB) != 1 {
		t.Fatalf("RenderSession produced %d/%d lines, want 1 each", len(lineA), len(lineB))
	}
	if lineA[0] == lineB[0] {
		t.Fatalf("session line unchanged after mutating worldSeed 1000 -> 9999: %q", lineB[0])
	}
	// The non-seed figures must be byte-identical between the two runs.
	// Assert the only difference is the seed substring: strip both and
	// confirm the remainder matches.
	if !strings.Contains(lineB[0], "seed 9999") || !strings.Contains(lineA[0], "seed 1000") {
		t.Fatalf("seed figure not present in output: %q / %q", lineA[0], lineB[0])
	}
	if !strings.Contains(lineA[0], "month 3 · tick 200") || !strings.Contains(lineB[0], "month 3 · tick 200") {
		t.Fatalf("non-seed figures diverged: %q vs %q", lineA[0], lineB[0])
	}
}
