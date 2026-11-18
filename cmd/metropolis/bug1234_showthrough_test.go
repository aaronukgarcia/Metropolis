package main

// BUG-1234 part 1: every screen must start from a blank area. The render loop
// kept its back buffer between frames, so after the map was drawn, any cell an
// F-key screen did not write still showed the map's glyphs. These tests run the
// REAL boot path through a REAL RenderLoop (persistent back buffer) on a
// simulation screen, and require that what ends up on screen after a switch is
// exactly what the screen draws into a brand-new buffer: no cell may differ.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func pressSpecial(w *skeletonWiring, k tcell.Key) {
	ev := tcell.NewEventKey(k, 0, tcell.ModNone)
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: k, Rune: 0, Raw: ev})
}

func simRow(sim tcell.SimulationScreen, w, y int) string {
	cells, cw, _ := sim.GetContents()
	var sb strings.Builder
	for x := 0; x < w; x++ {
		c := cells[y*cw+x]
		if len(c.Runes) == 0 || c.Runes[0] == 0 {
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(c.Runes[0])
		}
	}
	return sb.String()
}

func cleanFrame(w *skeletonWiring, cols, rows int) *core.Buffer {
	buf := core.NewBuffer(cols, rows)
	composeDraw(w)(buf, nil)
	return buf
}

func bootPausedForBlankTest(t *testing.T, slug string) *skeletonWiring {
	t.Helper()
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return 1000000, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })
	w, err := bootCore(slug, registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	pressKey(w, ' ')
	waitForPaused(t, w, true, 5*time.Second)
	return w
}

// liveLoop runs a real RenderLoop on a simulation screen of the given size.
type liveLoop struct {
	sim  tcell.SimulationScreen
	rl   *core.RenderLoop
	stop chan struct{}
	done chan struct{}
}

func startLiveLoop(t *testing.T, w *skeletonWiring, cols, rows int) *liveLoop {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(cols, rows)
	l := &liveLoop{sim: sim, rl: core.NewRenderLoop(sim, w.viewStore, composeDraw(w)), stop: make(chan struct{}), done: make(chan struct{})}
	go func() { l.rl.Run(l.stop); close(l.done) }()
	return l
}

func (l *liveLoop) settle() {
	l.rl.TriggerRender()
	time.Sleep(350 * time.Millisecond)
}

func (l *liveLoop) close() {
	close(l.stop)
	<-l.done
	l.sim.Fini()
}

func TestBUG1234_EveryScreenStartsBlank_NoMapShowThrough(t *testing.T) {
	w := bootPausedForBlankTest(t, "bug1234-blank")
	screens := []struct {
		name string
		key  tcell.Key
	}{
		{"F2 finance", tcell.KeyF2},
		{"F3 build", tcell.KeyF3},
		{"F4 services", tcell.KeyF4},
		{"F10 menu", tcell.KeyF10},
		{"F12 debug", tcell.KeyF12},
	}
	for _, size := range [][2]int{{120, 30}, {140, 40}} {
		cols, rows := size[0], size[1]
		l := startLiveLoop(t, w, cols, rows)
		pressSpecial(w, tcell.KeyF1)
		l.settle()
		mapGlyphs := 0
		for y := 1; y < rows-1; y++ {
			mapGlyphs += len(strings.ReplaceAll(simRow(l.sim, cols, y), " ", ""))
		}
		if mapGlyphs < 50 {
			l.close()
			t.Fatalf("%dx%d: precondition failed, the map frame has only %d glyphs", cols, rows, mapGlyphs)
		}
		for _, sc := range screens {
			pressSpecial(w, sc.key)
			l.settle()
			want := cleanFrame(w, cols, rows)
			for y := 0; y < rows; y++ {
				got := simRow(l.sim, cols, y)
				exp := bufferRowString(want, cols, y)
				if got != exp {
					l.close()
					t.Fatalf("%dx%d %s row %d shows something the screen did not draw:\n got: %q\nwant: %q", cols, rows, sc.name, y, got, exp)
				}
			}
			// back to the map so the next screen is switched to from a map frame
			pressSpecial(w, tcell.KeyF1)
			l.settle()
		}
		l.close()
	}
}

func TestBUG1234_F3FrameEvidence(t *testing.T) {
	w := bootPausedForBlankTest(t, "bug1234-evidence")
	cols, rows := 120, 30
	l := startLiveLoop(t, w, cols, rows)
	pressSpecial(w, tcell.KeyF1)
	l.settle()
	pressSpecial(w, tcell.KeyF3)
	l.settle()
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		sb.WriteString(strings.TrimRight(simRow(l.sim, cols, y), " "))
		sb.WriteByte('\n')
	}
	l.close()
	if dir := evidenceDir(); dir != "" {
		writeEvidence(t, dir, sb.String())
	}
	t.Log("F3 at 120x30 after the map:\n" + sb.String())
}
