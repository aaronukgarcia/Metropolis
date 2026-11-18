package main

// FEAT-9995385616 lane A3: the F5 WHY screen on the REAL boot path. The
// engine does not serve "f5.why" yet, so the screen shows its honest
// "no month closed yet" state; boot must still succeed.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/screens/why"
)

func TestFEAT9995385616_WhyScreen_F5RealBootFrame(t *testing.T) {
	r := rcNew(t)
	f := r.snap("F5 no data", tcell.KeyF5)
	rcSave(t, "f5_why_real_boot_no_data.txt", f)

	if got := r.w.screens.ActiveID(); got != screenIDWhy {
		t.Fatalf("F5 did not activate the why screen: active %q\n%s", got, f)
	}
	wants := []string{"no month closed yet", "homelessness"}
	wants = append(wants, why.LeverNames[:]...)
	wants = append(wants, why.ModifierNames[:]...)
	for _, want := range wants {
		if !strings.Contains(f, want) {
			t.Fatalf("F5 frame lacks %q:\n%s", want, f)
		}
	}
	if !strings.Contains(strings.ToLower(f), "why") {
		t.Fatalf("F5 frame has no title:\n%s", f)
	}
	if got := hintRow(f); !strings.HasPrefix(got, screenHintWhy) {
		t.Fatalf("F5 hint row %q, want %q", got, screenHintWhy)
	}

	// Pressing F1 returns to the map.
	rcKey(r.w, tcell.KeyF1, 0)
	if got := r.w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("F1 after F5: active %q, want %q", got, screenIDMap)
	}
}

func TestFEAT9995385616_WhyScreen_StatusRowShowsF5AtNarrowWidths(t *testing.T) {
	r := rcNew(t)
	keys := []tcell.Key{tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF4, tcell.KeyF5}
	names := []string{"F1", "F2", "F3", "F4", "F5"}
	for _, dims := range [][2]int{{80, 24}, {60, 24}} {
		for i, k := range keys {
			rcKey(r.w, k, 0)
			rows := sizedFrame(r.w, dims[0], dims[1])
			st := rows[dims[1]-1]
			if !strings.Contains(st, "F5 why") {
				t.Fatalf("%dx%d on %s: status row %q lacks \"F5 why\"", dims[0], dims[1], names[i], st)
			}
		}
	}
	// F5 at 80x24: the panel is on screen, not clipped to nothing.
	rcKey(r.w, tcell.KeyF5, 0)
	rows := sizedFrame(r.w, 80, 24)
	rcSave(t, "f5_why_real_boot_no_data_80x24.txt", strings.Join(rows, "\n")+"\n")
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "no month closed yet") {
		t.Fatalf("F5 at 80x24 lacks \"no month closed yet\":\n%s", joined)
	}
	if hint := rows[22]; len(hint) > 80 || !strings.HasPrefix(hint, screenHintWhy[:40]) {
		t.Fatalf("F5 hint at 80x24 wrong: %q", hint)
	}
}
