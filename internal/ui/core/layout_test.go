package core

import "testing"

func TestBelowMinimum(t *testing.T) {
	cases := []struct {
		w, h int
		want bool
	}{
		{120, 30, false},
		{160, 45, false},
		{119, 30, true},
		{120, 29, true},
		{80, 24, true},
	}
	for _, c := range cases {
		if got := BelowMinimum(c.w, c.h); got != c.want {
			t.Errorf("BelowMinimum(%d,%d) = %v, want %v", c.w, c.h, got, c.want)
		}
	}
}

func TestReflowPane_CollapsesBelowMinimum(t *testing.T) {
	spec := PaneSpec{Name: "map", MinW: 40, MinH: 20}

	fits := ReflowPane(Rect{X: 0, Y: 0, W: 60, H: 30}, spec)
	if fits.Stub {
		t.Fatalf("pane at 60x30 (spec min 40x20) should not stub, got %+v", fits)
	}

	tooNarrow := ReflowPane(Rect{X: 0, Y: 0, W: 30, H: 30}, spec)
	if !tooNarrow.Stub {
		t.Fatalf("pane at 30x30 (min width 40) should stub, got %+v", tooNarrow)
	}

	tooShort := ReflowPane(Rect{X: 0, Y: 0, W: 60, H: 10}, spec)
	if !tooShort.Stub {
		t.Fatalf("pane at 60x10 (min height 20) should stub, got %+v", tooShort)
	}

	// A degenerate/negative rect must not panic and must stub.
	degenerate := ReflowPane(Rect{X: 0, Y: 0, W: -5, H: 0}, spec)
	if !degenerate.Stub {
		t.Fatalf("degenerate rect should stub, got %+v", degenerate)
	}
}

func TestReflowPanes_MatchesByNameAndSkipsUnknown(t *testing.T) {
	specs := []PaneSpec{
		{Name: "map", MinW: 40, MinH: 20},
		{Name: "hud", MinW: 20, MinH: 5},
	}
	rects := map[string]Rect{
		"map":     {W: 60, H: 30},
		"hud":     {W: 10, H: 5},
		"unknown": {W: 1, H: 1},
	}
	states := ReflowPanes(rects, specs)

	if len(states) != 2 {
		t.Fatalf("got %d pane states, want 2 (unknown skipped)", len(states))
	}
	if states["map"].Stub {
		t.Errorf("map should fit, got stub")
	}
	if !states["hud"].Stub {
		t.Errorf("hud at 10x5 (min 20x5) should stub")
	}
	if _, ok := states["unknown"]; ok {
		t.Errorf("unknown pane with no matching spec should be skipped")
	}
}
