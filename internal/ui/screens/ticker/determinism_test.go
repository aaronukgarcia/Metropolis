package ticker

// SF-8 (GR#21 determinism): rendering is a pure function of its
// arguments and the ticker scroll is a deterministic function of
// (scroll step, event count) — identical inputs render identically
// across repeated calls; no time.Now()-driven content. None of this
// package's production code calls the wall clock directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestNoWallClockUsage mechanically encodes SF-8's own grep check
// ("grep -rn time.Now internal/ui/screens/ticker/*.go, excluding
// _test.go, returns no matches") as a real test, mirroring
// ui.screen.demo's TestNoWallClockUsage.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte("time.Now(")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if bytes.Contains(b, needle) {
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (SF-8/GR#21)", name)
		}
	}
}

// TestScrollPosition_DeterministicAndWrapping locks the deterministic
// scroll model (scroll.go): a pure function of (step, count) that wraps
// and never returns a negative index.
func TestScrollPosition_DeterministicAndWrapping(t *testing.T) {
	cases := []struct {
		step, n, want int
	}{
		{0, 3, 0},
		{1, 3, 1},
		{2, 3, 2},
		{3, 3, 0},  // wrap
		{5, 3, 2},  // wrap twice
		{0, 0, 0},  // no items
		{-1, 3, 2}, // negative step wraps defensively
	}
	for _, c := range cases {
		if got := scrollPosition(c.step, c.n); got != c.want {
			t.Errorf("scrollPosition(%d, %d) = %d, want %d", c.step, c.n, got, c.want)
		}
	}
}

// TestWindow_DeterministicAndNonAliasing locks the visible-window helper
// (scroll.go): the same inputs produce the same window, and the result is
// a fresh slice, not a sub-slice alias of the input.
func TestWindow_DeterministicAndNonAliasing(t *testing.T) {
	events := []Story{{EventID: "e1"}, {EventID: "e2"}, {EventID: "e3"}}
	got := window(events, 1, 2)
	want := []string{"e2", "e3"}
	if len(got) != len(want) {
		t.Fatalf("window len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].EventID != want[i] {
			t.Errorf("window[%d].EventID = %q, want %q", i, got[i].EventID, want[i])
		}
	}
	got[0].EventID = "mutated"
	if events[1].EventID != "e2" {
		t.Error("window returned an alias of the input slice; mutating it corrupted events")
	}
}
