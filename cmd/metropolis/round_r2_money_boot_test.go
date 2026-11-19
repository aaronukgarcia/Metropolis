package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

var (
	rr2Top  = regexp.MustCompile(`money (-?£[0-9,]+)`)
	rr2Tre  = regexp.MustCompile(`Treasury\s+(-?£[0-9,]+)`)
	rr2Net  = regexp.MustCompile(`Net treasury change\s+([+-]?£[0-9,]+)`)
	rr2NW   = regexp.MustCompile(`Net [Ww]orth\s+(-?£[0-9,]+)`)
	rr2Debt = regexp.MustCompile(`(?:Debt|Loans?)[A-Za-z ]*\s+(-?£[0-9,]+)`)
)

func TestRoundR2_BootTopLineMatchesF2AfterBuilds(t *testing.T) {
	r := rcNew(t)
	for i := 0; i < 6; i++ {
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRune, 'y')
		rcKey(r.w, tcell.KeyF3, 0)
		rcKey(r.w, tcell.KeyRune, 'b')
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRight, 0)
	}
	for m := 1; m <= 14; m++ {
		plAdvance(t, r.w, 30)
		f2 := r.snap(fmt.Sprintf("m%d", m), tcell.KeyF2)
		top := rr2Top.FindStringSubmatch(strings.Split(f2, "\n")[0])
		tre := rr2Tre.FindStringSubmatch(f2)
		if top == nil || tre == nil {
			t.Fatalf("month %d: missing top %v / treasury %v\n%s", m, top, tre, f2)
		}
		if top[1] != tre[1] {
			t.Fatalf("month %d: top %q vs F2 %q", m, top[1], tre[1])
		}
		t.Logf("m%d units %d top %s F2 %s net=%v nw=%v debt=%v", m, r2TreasuryUnits(r.w), top[1], tre[1],
			rr2Net.FindStringSubmatch(f2), rr2NW.FindStringSubmatch(f2), rr2Debt.FindStringSubmatch(f2))
		if m == 3 {
			t.Log("\n" + f2)
		}
	}
}
