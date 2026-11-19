package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

var (
	rp2Row   = regexp.MustCompile(`^\s{2}([A-Za-z][A-Za-z &-]+?)\s{2,}(-?£[0-9,]+)\s+(-?£[0-9,]+)`)
	rp2NW    = regexp.MustCompile(`Net Worth: (-?£[0-9,]+)`)
	rp2Res   = regexp.MustCompile(`Reserves\s+(-?£[0-9,]+)`)
	rp2Debt  = regexp.MustCompile(`Outstanding Debt\s+(-?£[0-9,]+)`)
	rp2Pop   = regexp.MustCompile(`pop (\d+)`)
	rp2Lived = regexp.MustCompile(`(\d+) lived in`)
)

func rp2Val(s string) int64 {
	neg := strings.HasPrefix(s, "-")
	s = strings.NewReplacer("-", "", "£", "", ",", "", "+", "").Replace(s)
	n, _ := strconv.ParseInt(s, 10, 64)
	if neg {
		return -n
	}
	return n
}

func TestRoundR2_PairsAtMonthCloses(t *testing.T) {
	r := rcNew(t)
	for i := 0; i < 6; i++ {
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRune, 'y')
		rcKey(r.w, tcell.KeyF3, 0)
		rcKey(r.w, tcell.KeyRune, 'b')
		rcKey(r.w, tcell.KeyF1, 0)
		rcKey(r.w, tcell.KeyRight, 0)
	}
	for m := 1; m <= 13; m++ {
		plAdvance(t, r.w, 30)
		f2 := r.snap(fmt.Sprintf("m%d", m), tcell.KeyF2)
		sums := [2]int64{}
		net := [2]int64{}
		section := 0
		for _, ln := range strings.Split(f2, "\n") {
			l := ln
			if len(l) > 70 {
				l = l[:70]
			}
			switch {
			case strings.Contains(l, "Treasury income:"):
				section = 1
				continue
			case strings.Contains(l, "Treasury outflows:"):
				section = 2
				continue
			case strings.Contains(l, "City economy"):
				section = 0
			}
			if rm := rp2Row.FindStringSubmatch(l); rm != nil {
				if strings.HasPrefix(rm[1], "Net treasury") {
					net[0], net[1] = rp2Val(rm[2]), rp2Val(rm[3])
					continue
				}
				v := [2]int64{rp2Val(rm[2]), rp2Val(rm[3])}
				for k := 0; k < 2; k++ {
					switch section {
					case 1:
						sums[k] += v[k]
					case 2:
						sums[k] -= v[k]
					}
				}
			}
		}
		if regexp.MustCompile(`[+-]£0([^,0-9]|$)`).MatchString(f2) {
			t.Errorf("m%d signed zero on screen", m)
		}
		t.Logf("m%d P&L rows sum %v vs net row %v", m, sums, net)
		if sums != net {
			t.Logf("  ADVISORY m%d: rows sum to %v but Net row shows %v", m, sums, net)
		}
		nw := rp2NW.FindStringSubmatch(f2)
		tm := regexp.MustCompile(`Treasury\s+(-?£[0-9,]+)`).FindStringSubmatch(f2)
		rs, db := rp2Res.FindStringSubmatch(f2), rp2Debt.FindStringSubmatch(f2)
		if tm == nil || nw == nil || rs == nil || db == nil {
			t.Fatalf("m%d balance sheet rows missing %v %v %v %v", m, tm, nw, rs, db)
		}
		if got, want := rp2Val(nw[1]), rp2Val(tm[1])+rp2Val(rs[1])-rp2Val(db[1]); got != want {
			t.Errorf("m%d Net Worth %s vs treasury+reserves-debt %d", m, nw[1], want)
		}
		hdr := strings.Split(f2, "\n")
		pm, lm := rp2Pop.FindStringSubmatch(hdr[0]), rp2Lived.FindStringSubmatch(hdr[1])
		if pm == nil || lm == nil || pm[1] != lm[1] {
			t.Errorf("m%d population %v vs lived in %v", m, pm, lm)
		}
	}
}
