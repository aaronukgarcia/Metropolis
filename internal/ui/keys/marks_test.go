package keys

import "testing"

func TestMarkTwelveValidSlotsAndThirteenthRejected(t *testing.T) {
	g := newTestGrammar()
	valid := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	if len(valid) != 12 {
		t.Fatalf("test setup: want exactly 12 slots, have %d", len(valid))
	}
	for _, id := range valid {
		if !ValidMarkID(id) {
			t.Fatalf("ValidMarkID(%q) = false, want true", id)
		}
		if err := g.SetMark(id, "loc-"+id); err != nil {
			t.Fatalf("SetMark(%q): %v", id, err)
		}
	}
	for _, id := range valid {
		got, ok := g.GetMark(id)
		if !ok || got != "loc-"+id {
			t.Fatalf("GetMark(%q) = %v,%v want loc-%s,true", id, got, ok, id)
		}
	}

	// A 13th distinct identifier is rejected outright, not silently
	// folded into slot 'a'.
	if ValidMarkID("m") {
		t.Fatalf("ValidMarkID(%q) = true, want false (13th slot)", "m")
	}
	if err := g.SetMark("m", "should-not-land"); err == nil {
		t.Fatalf("SetMark(13th slot) did not error")
	}
	// slot 'a' must be untouched by the rejected 13th-slot attempt.
	got, ok := g.GetMark("a")
	if !ok || got != "loc-a" {
		t.Fatalf("mark 'a' corrupted by a rejected 13th-slot SetMark: got=%v ok=%v", got, ok)
	}
}

func TestMarkGetInvalidIDReturnsNotOK(t *testing.T) {
	g := newTestGrammar()
	if _, ok := g.GetMark("z"); ok {
		t.Fatalf("GetMark on an invalid id reported ok=true")
	}
	if _, ok := g.GetMark(""); ok {
		t.Fatalf("GetMark(\"\") reported ok=true")
	}
}
