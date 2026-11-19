package main

import (
	"sync/atomic"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// countingLocked counts cell writes and Show calls on a lockedSim.
type countingLocked struct {
	*lockedSim
	sets  atomic.Int64
	shows atomic.Int64
}

func (c *countingLocked) SetContent(x, y int, m rune, comb []rune, st tcell.Style) {
	c.sets.Add(1)
	c.lockedSim.SetContent(x, y, m, comb, st)
}
func (c *countingLocked) Show() { c.shows.Add(1); c.lockedSim.Show() }

// The claim: on an idle, PAUSED screen the real render loop writes no cell
// and the screen stays drawn. BUG-1239: the clock is confirmed paused (boot
// Resume applied, Space, clock and status view both read paused, every view at
// the engine tick) before the first frame, so nothing legitimate can still
// change; each screen is waited for by frame count, and every screen read goes
// through the lockedSim so it never overlaps a draw.
func TestRoundR1_BUG1234_IdleFramesWriteNothingAndStayDrawn(t *testing.T) {
	w := bootPausedForBlankTest(t, "r1-idle")
	cols, rows := 120, 30
	sim := newLockedSim(t, cols, rows)
	cs := &countingLocked{lockedSim: sim}
	fl := startFramedLoop(t, w, sim, cs)
	defer fl.close()
	for _, sc := range []struct {
		n string
		k tcell.Key
	}{{"F1", tcell.KeyF1}, {"F2", tcell.KeyF2}, {"F3", tcell.KeyF3}, {"F4", tcell.KeyF4}, {"F10", tcell.KeyF10}, {"F12", tcell.KeyF12}} {
		pressSpecial(w, sc.k)
		// Two complete frames after the key: the first draws the new screen,
		// the second proves it is stable. No timing guess.
		fl.framesAfterNow(t, 2)
		s0 := cs.sets.Load()
		before := sim.rows(cols, rows)
		start := fl.starts.Load()
		// ~12 idle frames, counted, not timed.
		pollUntil(t, "12 idle frames", func() bool { return fl.starts.Load() >= start+12 })
		s1 := cs.sets.Load()
		after := sim.rows(cols, rows)
		for y := 0; y < rows; y++ {
			if before[y] != after[y] {
				t.Errorf("%s row %d changed while idle:\n%q\n%q", sc.n, y, before[y], after[y])
			}
		}
		t.Logf("%s: cell writes over ~12 idle frames = %d", sc.n, s1-s0)
		glyphs := 0
		for y := 0; y < rows; y++ {
			for _, r := range after[y] {
				if r != ' ' {
					glyphs++
				}
			}
		}
		if glyphs < 20 {
			t.Errorf("%s: screen nearly blank after idle (%d glyphs)", sc.n, glyphs)
		}
	}
}
