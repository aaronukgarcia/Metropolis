package demo

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestSubscribe_KnownViewsSendCorrectPayload(t *testing.T) {
	s := New("corr-sub")
	for _, view := range []string{ViewPopulation, ViewLeisure, ViewHousing, ViewCommute} {
		var got protocol.Command
		err := s.Subscribe(view, func(cmd protocol.Command) error {
			got = cmd
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe(%q): %v", view, err)
		}
		if got.Kind != protocol.KindSubscribe {
			t.Errorf("Kind = %v, want KindSubscribe", got.Kind)
		}
		payload, ok := got.Payload.(protocol.SubscribePayload)
		if !ok {
			t.Fatalf("Payload type = %T, want protocol.SubscribePayload", got.Payload)
		}
		if payload.ViewName != view {
			t.Errorf("ViewName = %q, want %q", payload.ViewName, view)
		}
		if err := protocol.ValidateViewName(view); err != nil {
			t.Errorf("view %q fails int.protocol's own naming grammar: %v", view, err)
		}
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
		t.Fatalf("Subscribe(unknown view) returned nil error, want MET-U502")
	}
	if called {
		t.Fatalf("send was called for an unrecognised view -- Subscribe must reject before sending")
	}
}

func TestSubscribeAll_SubscribesAllFourViews(t *testing.T) {
	s := New("corr-sub-all")
	var views []string
	err := s.SubscribeAll(func(cmd protocol.Command) error {
		views = append(views, cmd.Payload.(protocol.SubscribePayload).ViewName)
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeAll: %v", err)
	}
	want := []string{ViewPopulation, ViewLeisure, ViewHousing, ViewCommute}
	if len(views) != len(want) {
		t.Fatalf("views = %v, want %v", views, want)
	}
	for i, w := range want {
		if views[i] != w {
			t.Errorf("views[%d] = %q, want %q", i, views[i], w)
		}
	}
}

// TestApplyDelta_RoutesToBoundView proves ApplyDelta actually decodes
// and applies real f6.* patches once a SubscriptionID is bound, using
// the SAME real int.protocol Delta type production code uses -- not a
// screen-internal shortcut.
func TestApplyDelta_RoutesToBoundView(t *testing.T) {
	s := New("corr-route")
	s.BindSubscription(ViewCommute, "sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Seq:            1,
		Patch:          mustJSON(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: 42, InCommuters: 7}),
	})

	got, have := s.Commute()
	if !have || got.OutCommuters != 42 || got.InCommuters != 7 {
		t.Fatalf("Commute() = %+v have=%v, want {42 7} have=true", got, have)
	}
}

// TestApplyDelta_LeisureTasteDecodesThroughWire is BUG-202's regression
// check: no other test in this package builds a wireLeisurePatch and
// drives it through the real ApplyDelta -> applyLeisure -> wireTasteBucket
// decode path (screen.go's applyLeisure) -- the personality/determinism
// tests all construct TasteBucket literals directly, which would miss a
// conversion regression at that decode site. This sends a genuine
// f6.leisure wire patch with non-zero taste weights via ApplyDelta (the
// same route production Deltas take) and asserts LeisureTaste() reflects
// exactly those wire values.
func TestApplyDelta_LeisureTasteDecodesThroughWire(t *testing.T) {
	s := New("corr-leisure-taste")
	s.BindSubscription(ViewLeisure, "sub-taste")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-taste",
		Seq:            1,
		Patch: mustJSON(t, wireLeisurePatch{
			SchemaVersion: 1,
			LeisureTaste: []wireTasteBucket{
				{Taste: "Sport", Weight: 0.4},
				{Taste: "Culture", Weight: 0.35},
			},
		}),
	})

	got, have := s.LeisureTaste()
	want := []TasteBucket{
		{Taste: "Sport", Weight: 0.4},
		{Taste: "Culture", Weight: 0.35},
	}
	if !have {
		t.Fatalf("LeisureTaste() haveLeisure = false after a valid f6.leisure Delta")
	}
	if len(got) != len(want) {
		t.Fatalf("LeisureTaste() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LeisureTaste()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestApplyDelta_UnknownSubscriptionDropped is SF-7/DEMO-9's core check:
// a Delta for an unbound SubscriptionID is dropped, never applied, and
// never panics.
func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	// No BindSubscription call at all -- "sub-999" is unknown.
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-999",
		Seq:            1,
		Patch:          mustJSON(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: 1, InCommuters: 1}),
	})
	_, have := s.Commute()
	if have {
		t.Fatalf("Commute() haveCommute = true after a Delta for an unbound SubscriptionID -- must be dropped, not applied")
	}
}

// TestApplyDelta_UnboundAfterUnsubscribe proves UnbindSubscription makes
// a subsequently-arriving stale Delta for that same SubscriptionID
// unknown again (SF-7's "stale subscription" half of the check).
func TestApplyDelta_UnboundAfterUnsubscribe(t *testing.T) {
	s := New("corr-unbind")
	s.BindSubscription(ViewCommute, "sub-2")
	s.UnbindSubscription("sub-2")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-2",
		Seq:            1,
		Patch:          mustJSON(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: 5, InCommuters: 5}),
	})
	_, have := s.Commute()
	if have {
		t.Fatalf("Commute() haveCommute = true after a Delta for an unbound (unsubscribed) SubscriptionID")
	}
}

// TestApplyDelta_MalformedPatchDropped proves a malformed patch (bad
// JSON) is dropped, leaving prior state intact -- never a panic.
func TestApplyDelta_MalformedPatchDropped(t *testing.T) {
	s := New("corr-malformed")
	s.BindSubscription(ViewCommute, "sub-3")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-3", Seq: 1, Patch: mustJSON(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: 10, InCommuters: 20})})
	before, _ := s.Commute()

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-3", Seq: 2, Patch: []byte(`{not valid json`)})
	after, have := s.Commute()

	if !have || after != before {
		t.Fatalf("state changed after a malformed patch: before=%+v after=%+v have=%v", before, after, have)
	}
}

// TestApplyDelta_UnsupportedSchemaVersionDropped proves a patch
// declaring a schema version this package doesn't understand is
// dropped rather than guessed at.
func TestApplyDelta_UnsupportedSchemaVersionDropped(t *testing.T) {
	s := New("corr-schema")
	s.BindSubscription(ViewCommute, "sub-4")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-4", Seq: 1, Patch: mustJSON(t, wireCommutePatch{SchemaVersion: 2, OutCommuters: 1, InCommuters: 1})})
	_, have := s.Commute()
	if have {
		t.Fatalf("Commute() haveCommute = true after an unsupported schemaVersion patch")
	}
}

// TestApplyDelta_OversizedPayloadDropped exercises the SEC-039-style
// byte-size gate: a payload over maxPatchWireBytes is rejected before
// (and without requiring) a successful JSON decode.
func TestApplyDelta_OversizedPayloadDropped(t *testing.T) {
	s := New("corr-oversized")
	s.BindSubscription(ViewCommute, "sub-5")
	huge := make([]byte, maxPatchWireBytes+1)
	for i := range huge {
		huge[i] = ' '
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-5", Seq: 1, Patch: huge})
	_, have := s.Commute()
	if have {
		t.Fatalf("Commute() haveCommute = true after an oversized payload")
	}
}

func TestPopulation_SortedByMonthAge(t *testing.T) {
	s := New("corr-sort")
	s.applyPopulation(mustJSON(t, wirePopulationPatch{
		SchemaVersion: 1,
		AgeMonths: []wireAgeBucket{
			{MonthAge: 50, Male: 1, Female: 1},
			{MonthAge: 5, Male: 2, Female: 2},
			{MonthAge: 20, Male: 3, Female: 3},
		},
	}))
	ages, have := s.Population()
	if !have || len(ages) != 3 {
		t.Fatalf("Population() = %v have=%v, want 3 rows", ages, have)
	}
	for i := 1; i < len(ages); i++ {
		if ages[i].MonthAge < ages[i-1].MonthAge {
			t.Fatalf("Population() not sorted ascending by MonthAge: %+v", ages)
		}
	}
}
