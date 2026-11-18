package main

// BUG-1220 Parts B and C: discoverability and the F3 catalogue / refusal text,
// asserted on the real composed frame.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

func hintRow(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[plH-2]
}

func statusRow(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[plH-1]
}

// Part B: the status line names every live screen, and each screen's own hint
// row names its action keys.
func TestBUG1220_Hints_StatusAndPerScreen(t *testing.T) {
	r := rcNew(t)
	screens := []struct {
		key  tcell.Key
		want string
		id   core.ScreenID
	}{
		{tcell.KeyF1, screenHintMap, screenIDMap},
		{tcell.KeyF2, screenHintFinance, screenIDFinance},
		{tcell.KeyF3, screenHintBuild, screenIDBuild},
		{tcell.KeyF4, screenHintServices, screenIDServices},
		{tcell.KeyF10, screenHintMenu, screenIDMenu},
		{tcell.KeyF12, screenHintDebug, screenIDDebug},
	}
	for _, s := range screens {
		f := r.snap(fmt.Sprintf("hints %s", s.id), s.key)
		if got := hintRow(f); !strings.HasPrefix(got, s.want) {
			t.Fatalf("screen %s: hint row %q, want %q\n%s", s.id, got, s.want, f)
		}
		st := statusRow(f)
		for _, want := range []string{statusBarScreens, "[Space]", "[q] quit"} {
			if !strings.Contains(st, want) {
				t.Fatalf("screen %s: status row %q lacks %q", s.id, st, want)
			}
		}
	}
	// F3's own key line is no longer hidden under the status bar.
	f3 := r.snap("F3 keys line", tcell.KeyF3)
	if !strings.Contains(f3, "keys: n/p select, b build at map cursor") {
		t.Fatalf("F3 keys line not visible:\n%s", f3)
	}
	rcSave(t, "hints_f1.txt", r.snap("F1", tcell.KeyF1))
	rcSave(t, "hints_f3.txt", f3)
}

// Every key a hint advertises really is bound on that screen's grammar.
func TestBUG1220_Hints_AdvertisedKeysAreBound(t *testing.T) {
	r := rcNew(t)
	w := r.w
	feed := func(g *keys.KeyGrammar, k keys.Key) keys.FeedResult { return g.Feed(k) }
	for _, k := range []keys.Key{{Special: "Up"}, {Special: "Down"}, {Special: "Left"}, {Special: "Right"}, keys.KeyRune('c')} {
		if res := feed(w.mapGrammar, k); res.Status != keys.GlobalDispatched && res.Status != keys.Dispatched {
			t.Fatalf("F1 hint advertises %v but mapGrammar did not dispatch it (%v)", k, res.Status)
		}
	}
	// y and b are verified by the working buy/build in the other frame tests;
	// here only check they are registered (dispatch sends a command).
	for _, k := range []keys.Key{keys.KeyRune('y'), keys.KeyRune('b')} {
		if res := feed(w.mapGrammar, k); res.Status != keys.GlobalDispatched && res.Status != keys.Dispatched {
			t.Fatalf("F1 hint advertises %v but mapGrammar did not dispatch it (%v)", k, res.Status)
		}
	}
	for _, k := range []keys.Key{keys.KeyRune('n'), keys.KeyRune('p'), keys.KeyRune('b')} {
		if res := feed(w.extra.buildGrm, k); res.Status != keys.GlobalDispatched && res.Status != keys.Dispatched {
			t.Fatalf("F3 hint advertises %v but buildGrm did not dispatch it (%v)", k, res.Status)
		}
	}
}

var catRowRe = regexp.MustCompile(`cost £\d+\.\d\d\s+cap \d+ homes, \d+ jobs`)

// Part C: the F3 catalogue's cost and cap columns show real values.
func TestBUG1220_F3Catalogue_CostAndCapShowValues(t *testing.T) {
	r := rcNew(t)
	f := r.snap("F3 catalogue", tcell.KeyF3)
	n := 0
	dwelling := ""
	for _, ln := range strings.Split(f, "\n") {
		if strings.Contains(ln, "cost ") && strings.Contains(ln, "(zone)") {
			if !catRowRe.MatchString(ln) {
				t.Fatalf("catalogue row has blank or malformed cost/cap: %q", ln)
			}
			n++
			if strings.Contains(strings.ToLower(ln), "dwelling") {
				dwelling = ln
			}
		}
	}
	if n < 8 {
		t.Fatalf("expected the eight zone rows, found %d:\n%s", n, f)
	}
	// The dwelling row must show its real capacity (non-zero homes).
	if dwelling == "" || strings.Contains(dwelling, "cap 0 homes") {
		t.Fatalf("dwelling row missing or shows no homes: %q", dwelling)
	}
	rcSave(t, "f3_catalogue.txt", f)
}

// Part C: a refused buy's text clears on the next cursor key.
func TestBUG1220_RefusalNoticeClearsOnNextKey(t *testing.T) {
	r := rcNew(t)
	rcKey(r.w, tcell.KeyF1, 0)
	rcKey(r.w, tcell.KeyRune, 'y')
	rcKey(r.w, tcell.KeyRune, 'y') // second buy of the same cell is refused
	shown := ""
	for i := 0; i < 20 && !strings.Contains(shown, "Build rejected"); i++ {
		shown = r.snap("F1 after refused y", tcell.KeyF1)
	}
	if !strings.Contains(shown, "Build rejected") {
		t.Fatalf("a refused second buy shows no notice:\n%s", shown)
	}
	rcKey(r.w, tcell.KeyRight, 0)
	after := r.snap("F1 after cursor key", tcell.KeyF1)
	if strings.Contains(after, "Build rejected") {
		t.Fatalf("refusal text still on F1 after the next key:\n%s", after)
	}
	rcSave(t, "refusal_shown.txt", shown)
	rcSave(t, "refusal_cleared.txt", after)
}
