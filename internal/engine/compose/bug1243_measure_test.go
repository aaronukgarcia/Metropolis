package compose

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// BUG-1243 option measurements. NOT part of the normal run: it skips unless
// METROPOLIS_MEASURE=1 (the long runs are minutes, not seconds). It drives the
// same harness as bug1243_longrun_test.go through every option the data
// switches allow (see bug1243_options_test.go) and prints one table.
//
//	METROPOLIS_MEASURE=1 go test -count=1 -run TestBUG1243_MeasureOptions -v ./internal/engine/compose/
//
// METROPOLIS_MEASURE_OUT, when set, is a file the table is also written to.

type bug1243Scenario struct {
	name string
	edit func(doc map[string]any) // nil = shipped data
	pin  *bool                    // modifier flags forced after wiring (nil = as the data says)
}

// bug1243Run2 is bug1243Run's generalisation: n1 dwellings in month 1, n2 more
// after month 11 (so they exist in month 12), run for months months, with the
// per-month migration delta and housing affordability recorded.
type bug1243Row struct {
	bug1243Snapshot
	delta int64
	aff   float64
}

func bug1243Run2(t *testing.T, sc bug1243Scenario, n1, n2, months int) []bug1243Row {
	t.Helper()
	if sc.edit != nil {
		bug1243DataVariant(t, sc.edit)
	}
	r := ev83New(t)
	if sc.pin != nil {
		bug1243SetBuilt(r.comp, *sc.pin)
	}
	bug1243BuildRange(t, r, 0, n1)
	out := make([]bug1243Row, 0, months)
	var prev int64
	for m := 1; m <= months; m++ {
		if m == 12 && n2 > 0 {
			bug1243BuildRange(t, r, n1, n1+n2)
		}
		r.advance(30)
		st := r.comp.state
		h := buildChromeHousing(&st.housingPub, st.citizens, st.cid)
		out = append(out, bug1243Row{
			bug1243Snapshot: bug1243Snapshot{month: m, pop: r.comp.Population(), free: int64(h.Free), movedIn: int64(h.MovedIn), wanted: bug1243Wanted(r.comp)},
			delta:           st.netMigration - prev,
			aff:             st.housingAffordability,
		})
		prev = st.netMigration
	}
	return out
}

func bug1243Summ(rows []bug1243Row) string {
	var sb strings.Builder
	for _, m := range []int{1, 3, 6, 12, 24, 60, 130, 288} {
		if m <= len(rows) {
			x := rows[m-1]
			fmt.Fprintf(&sb, " m%d:%d/%+.1f", m, x.pop, x.wanted)
		}
	}
	first, last := 0, 0
	var left int64
	firstLeave := 0
	for _, x := range rows {
		if x.delta > 0 {
			if first == 0 {
				first = x.month
			}
			last = x.month
		}
		if x.delta < 0 {
			left += -x.delta
			if firstLeave == 0 {
				firstLeave = x.month
			}
		}
	}
	stop := "arrivals continue to the end"
	if last > 0 && last < len(rows) {
		nx := rows[last] // the month after the last arrival
		why := "score"
		if nx.free <= 0 {
			why = "homes full"
		}
		stop = fmt.Sprintf("last arrival m%d then none (%s: wanted %+.1f free %d)", last, why, nx.wanted, nx.free)
	} else if last == 0 {
		stop = "no arrivals"
	}
	return fmt.Sprintf("%s | first arrival m%d | %s | left=%d (first m%d)", sb.String(), first, stop, left, firstLeave)
}

func TestBUG1243_MeasureOptions(t *testing.T) {
	if os.Getenv("METROPOLIS_MEASURE") != "1" {
		t.Skip("set METROPOLIS_MEASURE=1 to run the BUG-1243 option measurements")
	}
	setW := func(v float64) func(map[string]any) {
		return bug1243Baseline(v)
	}
	floor := bug1243Floor
	parks := func(doc map[string]any) { bug1243Neutral(doc); bug1243ParksFloor(doc) }

	// B's baselines: month-1 wanted migration as a function of the baseline
	// (200 dwellings, one month), then bisected to +12 when reachable.
	wanted1 := func(v float64) float64 {
		var w float64
		t.Run(fmt.Sprintf("probe%.2f", v), func(t *testing.T) {
			rows := bug1243Run2(t, bug1243Scenario{name: "probe", edit: setW(v)}, 200, 0, 1)
			w = rows[0].wanted
		})
		return w
	}
	var out strings.Builder
	say := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		out.WriteString(line + "\n")
		t.Log(line)
	}
	for _, v := range []float64{40, 30, 20, 10, 0} {
		say("probe: baseline %.0f -> month-1 wanted %+.2f", v, wanted1(v))
	}
	bLo := 0.0
	if wanted1(0) >= 12 {
		lo, hi := 0.0, 40.0
		for i := 0; i < 14; i++ {
			mid := (lo + hi) / 2
			if wanted1(mid) >= 12 {
				lo = mid
			} else {
				hi = mid
			}
		}
		bLo = lo
	}
	say("B low baseline = %.2f (month-1 wanted %+.2f; 12 reachable: %v)", bLo, wanted1(bLo), wanted1(0) >= 12)
	bMid := (bLo + 40) / 2

	scs := []bug1243Scenario{
		{name: "today (both built, as shipped)"},
		{name: "A as ruled", edit: bug1243Neutral},
		{name: "C floorAtBaseline", edit: floor},
		{name: fmt.Sprintf("B baseline %.1f", bLo), edit: setW(bLo)},
		{name: fmt.Sprintf("B baseline %.1f (mid)", bMid), edit: setW(bMid)},
		{name: "B baseline 40 (= A)", edit: setW(40)},
		{name: "A + parks floor", edit: parks},
	}
	for _, size := range []struct {
		label     string
		n1, n2, m int
	}{{"BIG 200 dwellings, 288 months", 200, 0, 288}, {"SMALL 10 dwellings m1 + 10 at m12, 60 months", 10, 10, 60}} {
		say("== %s", size.label)
		for _, sc := range scs {
			var rows []bug1243Row
			sc, size := sc, size
			t.Run(size.label+"/"+sc.name, func(t *testing.T) { rows = bug1243Run2(t, sc, size.n1, size.n2, size.m) })
			say("%-26s%s", sc.name, bug1243Summ(rows))
			// month-37 housing reading, for the dip report
			for _, x := range rows {
				if x.aff == 0 && x.month > 1 {
					say("   %s: housing affordability 0.00 at m%d (wanted %+.1f)", sc.name, x.month, x.wanted)
				}
			}
		}
	}
	if p := os.Getenv("METROPOLIS_MEASURE_OUT"); p != "" {
		_ = os.WriteFile(p, []byte(out.String()), 0o644)
	}
}
