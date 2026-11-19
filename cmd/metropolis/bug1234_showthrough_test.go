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

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func pressSpecial(w *skeletonWiring, k tcell.Key) {
	ev := tcell.NewEventKey(k, 0, tcell.ModNone)
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: k, Rune: 0, Raw: ev})
}

func cleanFrame(w *skeletonWiring, cols, rows int) *core.Buffer {
	buf := core.NewBuffer(cols, rows)
	composeDraw(w)(buf, nil)
	return buf
}

// bootPausedForBlankTest boots the real game and returns with the clock
// confirmed paused (BUG-1239: the boot Resume applied first, then Space, then
// the clock and the status view both read paused). No settle loop.
func bootPausedForBlankTest(t *testing.T, slug string) *skeletonWiring {
	t.Helper()
	return bootPausedReady(t, slug)
}

// liveLoop runs a real RenderLoop on a simulation screen of the given size.
// All screen reads go through the lockedSim so they never overlap a draw.
type liveLoop struct {
	sim *lockedSim
	fl  *framedLoop
}

func startLiveLoop(t *testing.T, w *skeletonWiring, cols, rows int) *liveLoop {
	t.Helper()
	sim := newLockedSim(t, cols, rows)
	return &liveLoop{sim: sim, fl: startFramedLoop(t, w, sim, sim)}
}

// settle returns once the real loop has drawn two complete frames that began
// after the call (no sleep).
func (l *liveLoop) settle(t *testing.T) { t.Helper(); l.fl.framesAfterNow(t, 2) }

// row reads one screen row, race-free.
func (l *liveLoop) row(cols, rows, y int) string { return l.sim.rows(cols, rows)[y] }

func (l *liveLoop) close() { l.fl.close() }

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
		l.settle(t)
		mapGlyphs := 0
		for y := 1; y < rows-1; y++ {
			mapGlyphs += len(strings.ReplaceAll(l.row(cols, rows, y), " ", ""))
		}
		if mapGlyphs < 50 {
			l.close()
			t.Fatalf("%dx%d: precondition failed, the map frame has only %d glyphs", cols, rows, mapGlyphs)
		}
		for _, sc := range screens {
			pressSpecial(w, sc.key)
			l.settle(t)
			want := cleanFrame(w, cols, rows)
			for y := 0; y < rows; y++ {
				got := l.row(cols, rows, y)
				exp := bufferRowString(want, cols, y)
				if got != exp {
					l.close()
					t.Fatalf("%dx%d %s row %d shows something the screen did not draw:\n got: %q\nwant: %q", cols, rows, sc.name, y, got, exp)
				}
			}
			// back to the map so the next screen is switched to from a map frame
			pressSpecial(w, tcell.KeyF1)
			l.settle(t)
		}
		l.close()
	}
}

func TestBUG1234_F3FrameEvidence(t *testing.T) {
	w := bootPausedForBlankTest(t, "bug1234-evidence")
	cols, rows := 120, 30
	l := startLiveLoop(t, w, cols, rows)
	pressSpecial(w, tcell.KeyF1)
	l.settle(t)
	pressSpecial(w, tcell.KeyF3)
	l.settle(t)
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		sb.WriteString(strings.TrimRight(l.row(cols, rows, y), " "))
		sb.WriteByte('\n')
	}
	l.close()
	if dir := evidenceDir(); dir != "" {
		writeEvidence(t, dir, sb.String())
	}
	t.Log("F3 at 120x30 after the map:\n" + sb.String())
}
