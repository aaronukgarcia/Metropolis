package main

// FEAT-9995385625 round 2: one screen must never show two treasury figures a
// pound apart. The engine publishes the top line's whole pounds
// (det.WholePoundsRounded) and the UI rounds the balance sheet's units
// (widgets.FormatPounds); both must apply the SAME rule, half away from zero.

import (
	"math"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// The engine helper and the UI formatter give the same whole-pound value for
// every input: the formatter's text must equal FormatWholePounds of the engine's
// value.
func TestFEAT9995385625_R2_EngineRoundingAndUIFormatterAgree(t *testing.T) {
	check := func(u int64) {
		want := widgets.FormatWholePounds(det.WholePoundsRounded(u))
		if got := widgets.FormatPounds(u); got != want {
			t.Fatalf("units %d: the UI formatter shows %q but the engine rounds to %q", u, got, want)
		}
	}
	for _, u := range []int64{0, 1, 499, 500, 501, 999, 1000, 1499, 1500, -1, -499, -500, -501, -1500,
		438_609_500, 438_609_499, 438_609_999, math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1} {
		check(u)
	}
	rng := rand.New(rand.NewSource(99525))
	n := 0
	for i := 0; i < 10_000; i++ {
		check(int64(rng.Uint64()))
		n++
	}
	// Within 2,000 units of every multiple of 1,000 near zero and near the
	// limits; a sum that would overflow int64 is skipped.
	for _, base := range []int64{0, math.MaxInt64 - math.MaxInt64%1000, math.MinInt64 - math.MinInt64%1000} {
		for k := int64(-40); k <= 40; k++ {
			for d := int64(-2000); d <= 2000; d++ {
				u := base + k*1000 + d
				if (d > 0 && base+k*1000 > math.MaxInt64-d) || (d < 0 && base+k*1000 < math.MinInt64-d) {
					continue
				}
				if (k > 0 && base > math.MaxInt64-k*1000) || (k < 0 && base < math.MinInt64-k*1000) {
					continue
				}
				check(u)
				n++
			}
		}
	}
	if n < 10_000 {
		t.Fatalf("only %d values checked", n)
	}
}

var (
	r2TopMoneyRe = regexp.MustCompile(`money (-?£[0-9,]+)`)
	r2BSTreasury = regexp.MustCompile(`Treasury\s+(-?£[0-9,]+)`)
)

// Real boot: after month closes, the top line's money and the F2 balance
// sheet's Treasury are the same string, and the treasury in units is not a
// whole number of pounds with a remainder of 500 or more on at least one
// sampled month (so the truncating and rounding rules would differ).
func TestFEAT9995385625_R2_TopLineAndBalanceSheetTreasuryMatch(t *testing.T) {
	r := rcNew(t)
	sawHalfUp := false
	for i := 0; i < 8; i++ {
		advanceAndSettle(t, r.w, 30)
		f2 := r.snap(t, "F2 money screen", tcell.KeyF2)
		top := strings.Split(f2, "\n")[0]
		tm := r2TopMoneyRe.FindStringSubmatch(top)
		bm := r2BSTreasury.FindStringSubmatch(f2)
		if tm == nil || bm == nil {
			continue
		}
		if tm[1] != bm[1] {
			t.Fatalf("one screen, two treasuries: top line %q vs balance sheet %q\n%s", tm[1], bm[1], f2)
		}
		t.Logf("month %d: treasury units %d top %s balance sheet %s", i+1, r2TreasuryUnits(r.w), tm[1], bm[1])
		if r2TreasuryUnits(r.w)%1000 >= 500 {
			sawHalfUp = true
		}
	}
	if !sawHalfUp {
		t.Fatalf("no sampled month ended with a treasury remainder of 500 or more; the rounding rules were not distinguished in this run")
	}
}

// r2TreasuryUnits reads the balance sheet's Treasury row in raw money units
// from the same screen state the frame is drawn from (0 when absent).
func r2TreasuryUnits(w *skeletonWiring) int64 {
	bs, ok := w.financeScreen.BalanceSheet()
	if !ok {
		return 0
	}
	for _, it := range bs.Assets {
		if it.Label == "Treasury" {
			return it.ValueMicropounds
		}
	}
	return 0
}
