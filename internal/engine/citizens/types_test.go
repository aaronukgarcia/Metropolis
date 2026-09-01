package citizens

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// TestIncomeBandFor_RebasedScale is BUG-452's regression for the 7th
// hand-duplicated copy of the money base scale that an independent
// destructive round found: IncomeBandFor's wealth->pounds divisor was a
// raw 1_000_000 literal, left over from before the money base-unit
// rebase (1e-6 GBP/unit -> 1e-3 GBP/unit), which pinned the entire
// population to IncomeBand0 (a citizen needed £15,000,000 of real wealth
// to ever leave it). No prior test exercised this mapping — that is why
// the full suite stayed green through the original rebase. This test
// drives every band boundary using det.FromPounds (the canonical
// pounds->base-unit conversion), so it is scale-agnostic: it exercises
// whatever the CURRENT det.MicropoundsPerPound is, not a hardcoded
// literal, and therefore keeps proving the mapping correct across any
// future rebase too.
//
// PROOF THIS CAN FAIL: scratch-reverting IncomeBandFor's divisor back to
// the raw literal 1_000_000 (pre-BUG-452-fix) while det.MicropoundsPerPound
// is 1_000 (the current, rebased scale) makes every sub-test below FAIL
// RED — every wealth figure this test drives (up to £150,000) divides
// down to 0 whole pounds under the stale divisor, so IncomeBandFor
// returns IncomeBand0 for all of them, including the £150,000 case that
// should be IncomeBand4. Verified by hand during this fix (see the
// destructive round's REJECT note); restoring the derived divisor turns
// it GREEN again.
func TestIncomeBandFor_RebasedScale(t *testing.T) {
	cases := []struct {
		name   string
		pounds int64
		want   IncomeBand
	}{
		{"band0: £0", 0, IncomeBand0},
		{"band0: just under the band0/1 boundary (£14,999)", 14_999, IncomeBand0},
		{"band1: exactly the band0/1 boundary (£15,000)", 15_000, IncomeBand1},
		{"band1: just under the band1/2 boundary (£29,999)", 29_999, IncomeBand1},
		{"band2: exactly the band1/2 boundary (£30,000)", 30_000, IncomeBand2},
		{"band2: just under the band2/3 boundary (£59,999)", 59_999, IncomeBand2},
		{"band3: exactly the band2/3 boundary (£60,000)", 60_000, IncomeBand3},
		{"band3: just under the band3/4 boundary (£119,999)", 119_999, IncomeBand3},
		{"band4: exactly the band3/4 boundary (£120,000)", 120_000, IncomeBand4},
		{"band4: well above every boundary (£150,000)", 150_000, IncomeBand4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wealth := int64(det.FromPounds(c.pounds))
			if got := IncomeBandFor(wealth); got != c.want {
				t.Fatalf("IncomeBandFor(FromPounds(%d)=%d) = %v, want %v", c.pounds, wealth, got, c.want)
			}
		})
	}
}
