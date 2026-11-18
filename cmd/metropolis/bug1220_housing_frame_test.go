package main

// BUG-1220 Part A (FEAT-083 leg 4): the player can SEE why population moves.
// The second chrome row shows homes, people living there, free places and the
// last month's migration, and says HOMES FULL when the engine's own vacancy
// rule admits nobody. Frames are the real composed frame.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

var hsRe = regexp.MustCompile(`homes (\d+) places, (\d+) lived in, (\d+) free \| moved in (\d+), left (\d+) this month`)

type hsLine struct {
	places, lived, free, in, out int
	full                         bool
	raw                          string
}

func hsParse(t *testing.T, frame string) hsLine {
	t.Helper()
	for _, ln := range strings.Split(frame, "\n") {
		if m := hsRe.FindStringSubmatch(ln); m != nil {
			v := func(i int) int { n, _ := strconv.Atoi(m[i]); return n }
			return hsLine{v(1), v(2), v(3), v(4), v(5), strings.Contains(ln, "HOMES FULL - build dwellings (F3) to grow"), ln}
		}
	}
	t.Fatalf("no housing row in frame:\n%s", frame)
	return hsLine{}
}

// TestBUG1220_HousingRow_FullCueBindsAndClears: the control city shows the cue
// from the month it binds and not before; building dwellings raises places and
// free, and clears the cue.
func TestBUG1220_HousingRow_FullCueBindsAndClears(t *testing.T) {
	r := rcNew(t)
	if f := plFrame(r.w); !strings.Contains(f, "homes: no month closed yet") {
		t.Fatalf("before the first close the housing row must say so:\n%s", f)
	}
	firstFull := -1
	var last hsLine
	for m := 1; m <= 8; m++ {
		plAdvance(t, r.w, 30)
		f := r.snap(fmt.Sprintf("CONTROL month %d", m), tcell.KeyF1)
		last = hsParse(t, f)
		tick := m * 30
		if last.places != 120 {
			t.Fatalf("tick %d: control places %d, want the 120 seed homes: %s", tick, last.places, last.raw)
		}
		if last.lived <= 0 || last.free != max(0, last.places-last.lived) {
			t.Fatalf("tick %d: figures do not add up: %s", tick, last.raw)
		}
		if last.full && firstFull < 0 {
			firstFull = tick
		}
		if firstFull < 0 && last.full {
			t.Fatalf("tick %d: cue shown early: %s", tick, last.raw)
		}
		if !last.full && firstFull >= 0 {
			t.Fatalf("tick %d: cue vanished with no new homes: %s", tick, last.raw)
		}
		if !last.full && last.free < 2 {
			t.Fatalf("tick %d: cue missing at zero vacancy: %s", tick, last.raw)
		}
	}
	if firstFull < 0 {
		t.Fatalf("control city never reached HOMES FULL by tick 240; last: %s", last.raw)
	}
	t.Logf("control: HOMES FULL first shown at tick %d; last row: %s", firstFull, last.raw)

	// Build dwellings: buy cells and queue builds, then run until places rise.
	for i := 0; i < 6; i++ {
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRune, 'y')
		rcKey(r.w, tcell.KeyF3, 0)
		rcKey(r.w, tcell.KeyRune, 'b')
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRight, 0)
	}
	cleared := false
	for m := 9; m <= 20 && !cleared; m++ {
		plAdvance(t, r.w, 30)
		f := r.snap(fmt.Sprintf("AFTER BUILD month %d", m), tcell.KeyF1)
		h := hsParse(t, f)
		if h.places > 120 {
			if h.free <= 0 || h.full {
				t.Fatalf("tick %d: dwellings stand (places %d) but free/cue not updated: %s", m*30, h.places, h.raw)
			}
			cleared = true
			t.Logf("after build: %s", h.raw)
			rcSave(t, "housing_cleared.txt", f)
		}
	}
	if !cleared {
		t.Fatalf("places never rose above 120 after building; last frame:\n%s", plFrame(r.w))
	}
	rcSave(t, "housing_control_full.txt", "control city at the month the cue bound; last row: "+last.raw+"\n")
}

// TestBUG1220_HousingCue_SurvivesAn80ColumnTerminal: the cue is the whole point
// of the row, so at 80 columns it must be fully visible, not cut off the end
// of a long figures line.
func TestBUG1220_HousingCue_SurvivesAn80ColumnTerminal(t *testing.T) {
	r := rcNew(t)
	for m := 1; m <= 8; m++ {
		plAdvance(t, r.w, 30)
		if !strings.Contains(plFrame(r.w), "HOMES FULL") {
			continue
		}
		const cols = 80
		line := strings.TrimRight(row(renderAt(r.w, cols, 24), chromeTopBarRows), " ")
		if !strings.Contains(line, "HOMES FULL - build dwellings (F3) to grow") {
			t.Fatalf("tick %d: at %d columns the cue is cut off or missing: %q", m*30, cols, line)
		}
		return
	}
	t.Fatal("control city never reached HOMES FULL by tick 240")
}
