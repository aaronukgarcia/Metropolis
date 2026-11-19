package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Round r1 (sonnet-round-r1-bug1243): the parks option must not punish the
// player for opening the first venue. Reads the parks lever before and after a
// REAL entertainment venue completes (not a faked registry entry), with the
// option on and off. With the option on the value after the venue must be no
// lower than before it, and never below the baseline (r2: the option is now a
// floor, leisure.floorAtBaseline). With it off (shipped) the readings are
// logged: today's behaviour, unchanged.
func TestRoundR1_BUG1243_ParksOption_FirstVenueDoesNotLowerPoints(t *testing.T) {
	run := func(on bool) (before, after, aw float64) {
		if on {
			bug1243DataVariant(t, bug1243ParksFloor)
		}
		r := ev83New(t)
		bug1243BuildRange(t, r, 0, 10)
		r.advance(30)
		a := r.comp.state.attract
		before, _ = a.LeverValue(attract.LeverParks)
		aw = a.AWorld()
		bug1243OpenVenue(t, r, protocol.CellRef{X: 30, Y: 30})
		after, _ = a.LeverValue(attract.LeverParks)
		return
	}
	offB, offA, aw := run(false)
	t.Logf("option OFF: parks reading before venue %.3f, after %.3f, baseline %.1f, points (w0.02) %.4f -> %.4f", offB, offA, aw, 0.02*(offB-aw), 0.02*(offA-aw))
	onB, onA, aw2 := run(true)
	t.Logf("option ON : parks reading before venue %.3f, after %.3f, baseline %.1f, points (w0.02) %.4f -> %.4f", onB, onA, aw2, 0.02*(onB-aw2), 0.02*(onA-aw2))
	if onA < onB {
		t.Fatalf("option ON: opening the first venue LOWERED parks (%.3f -> %.3f)", onB, onA)
	}
	if onB < aw2 {
		t.Fatalf("option ON: parks %.3f before any venue is below the baseline %.3f", onB, aw2)
	}
}
