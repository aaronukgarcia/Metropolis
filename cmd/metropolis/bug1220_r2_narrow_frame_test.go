package main

// BUG-1220 r2: at the standard 80x24 (and still at 60 columns) every screen's
// status row leads with the screen list, F3 build included, and the F4 hint
// advertises nothing that does nothing.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// sizedFrame draws the real composed frame at width x height and returns the
// rows with trailing spaces trimmed.
func sizedFrame(w *skeletonWiring, width, height int) []string {
	buf := core.NewBuffer(width, height)
	composeDraw(w)(buf, nil)
	rows := make([]string, height)
	for y := 0; y < height; y++ {
		rows[y] = strings.TrimRight(bufferRowString(buf, width, y), " ")
	}
	return rows
}

func TestBUG1220_R2_ScreenListVisibleAtNarrowWidths(t *testing.T) {
	r := rcNew(t)
	screens := []struct {
		key  tcell.Key
		name string
	}{{tcell.KeyF1, "F1"}, {tcell.KeyF2, "F2"}, {tcell.KeyF3, "F3"}, {tcell.KeyF4, "F4"}}
	for _, dims := range [][2]int{{80, 24}, {60, 24}} {
		for _, s := range screens {
			rcKey(r.w, s.key, 0)
			rows := sizedFrame(r.w, dims[0], dims[1])
			st := rows[dims[1]-1]
			for _, want := range []string{"F1 map", "F2 money", "F3 build", "F4 services"} {
				if !strings.Contains(st, want) {
					t.Fatalf("%dx%d on %s: status row %q lacks %q", dims[0], dims[1], s.name, st, want)
				}
			}
			t.Logf("%dx%d %s status: %s", dims[0], dims[1], s.name, st)
			t.Logf("%dx%d %s hint:   %s", dims[0], dims[1], s.name, rows[dims[1]-2])
		}
	}
}

func TestBUG1220_R2_F4HintIsTrue(t *testing.T) {
	r := rcNew(t)
	rcKey(r.w, tcell.KeyF4, 0)
	rows := sizedFrame(r.w, 80, 24)
	hint := rows[22]
	if !strings.Contains(hint, "F4 SERVICES") || !strings.Contains(hint, "view only") {
		t.Fatalf("F4 hint at 80x24 is not the view-only hint: %q", hint)
	}
	for _, bad := range []string{"s f +", "s f -", "raise or lower"} {
		if strings.Contains(hint, bad) {
			t.Fatalf("F4 hint advertises %q, which does nothing in baseline one: %q", bad, hint)
		}
	}
}

// Evidence: the 80x24 first frame, and an 80x24 frame with the HOMES FULL cue.
func TestBUG1220_R2_Evidence80x24(t *testing.T) {
	r := rcNew(t)
	rcKey(r.w, tcell.KeyF1, 0)
	first := strings.Join(sizedFrame(r.w, 80, 24), "\n") + "\n"
	rcSave(t, "r2_f1_first_frame_80x24.txt", first)
	for m := 1; m <= 8; m++ {
		plAdvance(t, r.w, 30)
		rows := sizedFrame(r.w, 80, 24)
		if strings.Contains(strings.Join(rows, "\n"), "HOMES FULL") {
			rcSave(t, "r2_homes_full_80x24.txt", strings.Join(rows, "\n")+"\n")
			return
		}
	}
	t.Fatalf("control city never showed HOMES FULL at 80x24 within 8 months")
}
