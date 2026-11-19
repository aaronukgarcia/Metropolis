package compose

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1243 (P0): migration stopped for good at about 1,400 people with homes
// standing free, because the safety and environment readings only ever fall
// (refuse is never collected and there is no police in Baseline One) and the
// player can do nothing about either. Aaron's ruling of 2026-11-18: hold both
// at neutral until refuse collection and policing exist. These tests drive a
// real city through the protocol for many game months and read the same
// numbers the diagnosis read.

// bug1243Snapshot is one month's reading.
type bug1243Snapshot struct {
	month   int
	pop     int
	free    int64
	movedIn int64
	wanted  float64
}

// bug1243Wanted returns the migration the formula wants this month: the same
// reading the diagnosis took (final score minus the world baseline, run
// through the migration curve).
func bug1243Wanted(c *Composition) float64 {
	st := c.state
	a := st.attract
	pre, _ := a.A()
	sat, _ := st.attractWellbeing()
	net, _ := a.G(pre*sat - a.AWorld())
	return net
}

// bug1243Build puts 200 dwellings on 200 distinct cells in month 1.
func bug1243Build(t *testing.T, r *ev83) { bug1243BuildRange(t, r, 0, 200) }

// bug1243BuildRange builds dwellings number from..to-1 (cell i is (i%40, i/40)).
func bug1243BuildRange(t *testing.T, r *ev83, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		c := protocol.CellRef{X: i % 40, Y: i / 40}
		sendAndAwait(t, r.transport, protocol.KindBuy, protocol.BuyPayload{Cell: c})
		if res := sendAndAwait(t, r.transport, protocol.KindZone, protocol.ZonePayload{Cell: c, ZoneType: "dwelling"}); !res.Accepted {
			t.Fatalf("zone %d rejected: %+v", i, res.Error)
		}
		if res := sendAndAwait(t, r.transport, protocol.KindBuild, protocol.BuildPayload{Cell: c, BuildingType: "dwelling"}); !res.Accepted {
			t.Fatalf("build %d rejected: %+v", i, res.Error)
		}
	}
}

// bug1243Run builds the 200 dwellings and runs the given number of game
// months, returning a snapshot per month. flip, when non-nil, overrides both
// modifier flags before the first month. Data variants are selected by the
// caller (bug1243DataVariant) before this is called.
func bug1243Run(t *testing.T, months int, flip *bool) []bug1243Snapshot {
	t.Helper()
	r := ev83New(t)
	if flip != nil {
		bug1243SetBuilt(r.comp, *flip)
	}
	bug1243Build(t, r)
	out := make([]bug1243Snapshot, 0, months)
	for m := 1; m <= months; m++ {
		r.advance(30)
		st := r.comp.state
		h := buildChromeHousing(&st.housingPub, st.citizens, st.cid)
		out = append(out, bug1243Snapshot{
			month: m, pop: r.comp.Population(), free: int64(h.Free), movedIn: int64(h.MovedIn),
			wanted: bug1243Wanted(r.comp),
		})
	}
	return out
}

func bug1243Line(s []bug1243Snapshot, m int) string {
	x := s[m-1]
	return fmt.Sprintf("m%03d pop=%d free=%d movedIn=%d wanted=%.3f", x.month, x.pop, x.free, x.movedIn, x.wanted)
}

// bug1243Months is the long run: 288 months is the diagnosis' full length;
// the stall began about month 120.
const bug1243Months = 288

// The multi-year runs take minutes: they run only with METROPOLIS_MEASURE=1,
// like the option table (BUG-1246: the compose package must stay inside CI's
// time limit). The committed fast tests below carry the same claims in a few
// months.
func bug1243RequireMeasure(t *testing.T) {
	t.Helper()
	if os.Getenv("METROPOLIS_MEASURE") != "1" {
		t.Skip("set METROPOLIS_MEASURE=1 to run the BUG-1243 multi-year runs")
	}
}

// Option A (both modifiers neutral): migration never stalls while homes are free.
func TestBUG1243_Measure_NeutralModifiers_MigrationNeverStallsWhileHomesFree(t *testing.T) {
	bug1243RequireMeasure(t)
	bug1243DataVariant(t, bug1243Neutral)
	s := bug1243Run(t, bug1243Months, nil)
	for _, m := range []int{1, 12, 60, 130, bug1243Months} {
		t.Log(bug1243Line(s, m))
	}
	last := s[len(s)-1]
	if last.wanted <= 0 {
		t.Fatalf("wanted migration at month %d = %.3f, want > 0: %s", last.month, last.wanted, bug1243Line(s, last.month))
	}
	for _, x := range s {
		if x.wanted <= 0 {
			t.Logf("dip: wanted %.3f at month %d", x.wanted, x.month)
			if x.month >= 60 {
				t.Fatalf("wanted migration fell to %.3f at month %d (want positive from month 60)", x.wanted, x.month)
			}
		}
	}
	for m := 12; m+12 <= len(s); m += 12 {
		if s[m-1].free > 0 && s[m+11].pop <= s[m-1].pop {
			t.Fatalf("no growth between month %d and %d with homes free: %s -> %s", m, m+12, bug1243Line(s, m), bug1243Line(s, m+12))
		}
	}
}

// With both modifiers built (today's shipped data) the old stall reproduces:
// this proves the test above can fail.
func TestBUG1243_Measure_BuiltModifiers_ReproduceTheStall(t *testing.T) {
	bug1243RequireMeasure(t)
	s := bug1243Run(t, 160, nil)
	for _, m := range []int{1, 12, 60, 130, 160} {
		t.Log(bug1243Line(s, m))
	}
	var arrivals int64
	for _, x := range s[129:] {
		arrivals += x.movedIn
	}
	last := s[len(s)-1]
	if arrivals != 0 || last.free <= 0 || last.wanted >= 0.5 {
		t.Fatalf("expected the stall (no arrivals from month 130 with homes free, wanted under half a person a month), got arrivals=%d free=%d wanted=%.3f", arrivals, last.free, last.wanted)
	}
}

// The shipped data is TODAY'S behaviour exactly (BUG-1243 lead ruling: the
// mechanism lands with no change to the game until Aaron chooses A, B or C):
// the score and the wanted migration of a fixed scenario (200 dwellings, one
// month) equal the same scenario with both modifiers forced to built, and the
// wanted migration equals the figure measured on main at 6d109c1 (+12.2).
func TestBUG1243_ShippedData_IsTodaysBehaviour_Exactly(t *testing.T) {
	r := ev83New(t)
	bug1243Build(t, r)
	r.advance(30)
	preShipped, err := r.comp.state.attract.A()
	if err != nil {
		t.Fatal(err)
	}
	wantedShipped := bug1243Wanted(r.comp)

	f := ev83New(t)
	bug1243SetBuilt(f.comp, true)
	bug1243Build(t, f)
	f.advance(30)
	preForced, _ := f.comp.state.attract.A()
	if preShipped != preForced || wantedShipped != bug1243Wanted(f.comp) {
		t.Fatalf("shipped data differs from forced-built: score %v vs %v, wanted %v vs %v", preShipped, preForced, wantedShipped, bug1243Wanted(f.comp))
	}
	if math.Abs(wantedShipped-12.2) > 0.05 {
		t.Fatalf("month-1 wanted migration %.4f, want +12.2 (measured on main at 6d109c1, 200 dwellings)", wantedShipped)
	}
	for i := 0; i < attract.ModifierCount; i++ {
		if _, built := r.comp.state.attract.ModifierValue(attract.ModifierID(i)); !built {
			t.Fatalf("modifier %d published not built under the shipped data", i)
		}
	}
	if got := r.comp.state.attract.AWorld(); got != 40 {
		t.Fatalf("shipped baseline %v, want 40", got)
	}
}

// The flags are data: set not built (option A) the two modifiers publish "not
// built" and add exactly nothing to the score while their readings are still
// computed and stored; the score then differs from the built city by exactly
// weight x (reading - world) for each modifier.
func TestBUG1243_ModifierFlags_NeutralOptionAndFlippable(t *testing.T) {
	scoreAfterMonth := func() (float64, *attract.AttractAPI) {
		r := ev83New(t)
		bug1243Build(t, r)
		r.advance(30)
		a := r.comp.state.attract
		pre, err := a.A()
		if err != nil {
			t.Fatalf("A: %v", err)
		}
		return pre, a
	}
	preYes, aYes := scoreAfterMonth() // shipped = built
	bug1243DataVariant(t, bug1243Neutral)
	preNo, aNo := scoreAfterMonth()
	var want float64
	for i := 0; i < attract.ModifierCount; i++ {
		id := attract.ModifierID(i)
		v, built := aNo.ModifierValue(id)
		if built {
			t.Fatalf("modifier %s published built while its flag is false", id)
		}
		if v <= 0 || v >= 100 {
			t.Fatalf("modifier %s reading %.3f was not computed and stored", id, v)
		}
		vy, builtY := aYes.ModifierValue(id)
		if !builtY || vy != v {
			t.Fatalf("modifier %s built: built=%v value %.3f, want built with the same reading %.3f", id, builtY, vy, v)
		}
		want += 0.1 * (v - aNo.AWorld())
	}
	if d := preYes - preNo; math.Abs(d-want) > 1e-9 || d == 0 {
		t.Fatalf("built modifiers moved the score by %.6f, want %.6f (weight 0.1 x (reading - world), nonzero)", d, want)
	}
}
