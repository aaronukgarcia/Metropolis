package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// countingScreen counts cell writes and Show calls reaching the terminal.
type countingScreen struct {
	tcell.SimulationScreen
	sets  atomic.Int64
	shows atomic.Int64
}

func (c *countingScreen) SetContent(x, y int, m rune, comb []rune, st tcell.Style) {
	c.sets.Add(1)
	c.SimulationScreen.SetContent(x, y, m, comb, st)
}
func (c *countingScreen) Show() { c.shows.Add(1); c.SimulationScreen.Show() }

func TestRoundR1_BUG1234_IdleFramesWriteNothingAndStayDrawn(t *testing.T) {
	w := bootPausedForBlankTest(t, "r1-idle")
	cols, rows := 120, 30
	base := tcell.NewSimulationScreen("")
	if err := base.Init(); err != nil {
		t.Fatal(err)
	}
	base.SetSize(cols, rows)
	cs := &countingScreen{SimulationScreen: base}
	rl := core.NewRenderLoop(cs, w.viewStore, composeDraw(w))
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { rl.Run(stop); close(done) }()
	defer func() { close(stop); <-done; base.Fini() }()
	for _, sc := range []struct {
		n string
		k tcell.Key
	}{{"F1", tcell.KeyF1}, {"F2", tcell.KeyF2}, {"F3", tcell.KeyF3}, {"F4", tcell.KeyF4}, {"F10", tcell.KeyF10}, {"F12", tcell.KeyF12}} {
		pressSpecial(w, sc.k)
		// Wait until the frame has stopped changing before the idle window
		// starts: on a slow runner the status row catches up with the clock
		// late (CI run 44406336316 counted that catch-up as an idle write).
		// Settled = no cell write for a full second; no fixed wait.
		settleBy := time.Now().Add(60 * time.Second)
		last, lastAt := cs.sets.Load(), time.Now()
		for {
			time.Sleep(20 * time.Millisecond)
			if n := cs.sets.Load(); n != last {
				last, lastAt = n, time.Now()
			} else if time.Since(lastAt) >= time.Second {
				break
			}
			if time.Now().After(settleBy) {
				t.Fatalf("%s: frame never settled (still being written after 60s)", sc.n)
			}
		}
		s0 := cs.sets.Load()
		var before []string
		for y := 0; y < rows; y++ {
			before = append(before, simRow(base, cols, y))
		}
		time.Sleep(1200 * time.Millisecond) // ~12 idle frames
		s1 := cs.sets.Load()
		for y := 0; y < rows; y++ {
			if g := simRow(base, cols, y); g != before[y] {
				t.Errorf("%s row %d changed while idle:\n%q\n%q", sc.n, y, before[y], g)
			}
		}
		t.Logf("%s: cell writes over ~12 idle frames = %d", sc.n, s1-s0)
		glyphs := 0
		for y := 0; y < rows; y++ {
			for _, r := range simRow(base, cols, y) {
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
