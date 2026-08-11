package world

import "testing"

// TestVerifyGeorefAnchorsFlagsJ13 confirms georef_verify.go's structural
// check reproduces the exact discrepancy data/georef.json's own
// openQuestions flags: M20 Junction 13's documented position
// (621100, 137500) sits outside the documented start-tile bounds
// (maxN: 137000).
func TestVerifyGeorefAnchorsFlagsJ13(t *testing.T) {
	reports := VerifyGeorefAnchors()
	var j13 *GeorefVerificationReport
	for i := range reports {
		if reports[i].AnchorName == "M20 Junction 13 / Castle Hill Interchange" {
			j13 = &reports[i]
		}
	}
	if j13 == nil {
		t.Fatal("expected a J13 report row")
	}
	if j13.InsideStartTile {
		t.Fatal("expected J13 to be flagged OUTSIDE the documented start-tile bounds, per georef.json's own openQuestions")
	}
}

// TestVerifyGeorefAnchorsFlagsJ13_ProvenFail: PROOF — an anchor
// genuinely inside the tile (Folkestone West station) must NOT be
// flagged, confirming the check discriminates real position data.
func TestVerifyGeorefAnchorsFlagsJ13_ProvenFail(t *testing.T) {
	reports := VerifyGeorefAnchors()
	for _, r := range reports {
		if r.AnchorName == "Folkestone West railway station" {
			if !r.InsideStartTile {
				t.Fatal("sanity check failed: Folkestone West should be inside the documented start-tile bounds")
			}
			return
		}
	}
	t.Fatal("expected a Folkestone West report row")
}
