package why

import (
	"math"
	"testing"
)

// FEAT-9995385616 lane A3 (round r1 blocker B2): absurd but finite values must
// be refused whole, and the tenths helpers must be safe for any input.

func TestRegression_FEAT9995385616_TenthsHelpersSaturate(t *testing.T) {
	for _, v := range []float64{1e300, -1e300, math.MaxFloat64, -math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1), 1e19, -1e19} {
		got := roundTenths(v) // must not panic or wrap
		if v > 0 && got < 0 {
			t.Errorf("roundTenths(%v) = %d: sign inverted", v, got)
		}
		if v < 0 && got > 0 {
			t.Errorf("roundTenths(%v) = %d: sign inverted", v, got)
		}
		_ = fmtTenths(got, true)
	}
	if roundTenths(math.NaN()) != 0 {
		t.Errorf("roundTenths(NaN) = %d, want 0", roundTenths(math.NaN()))
	}
	for _, tn := range []int64{math.MinInt64, math.MaxInt64, math.MinInt64 + 1, 0, -5, 5} {
		s := fmtTenths(tn, true)
		if tn < 0 && (len(s) == 0 || s[0] != '-' || s[1] == '-') {
			t.Errorf("fmtTenths(%d) = %q: bad sign", tn, s)
		}
		if tn > 0 && s[0] != '+' {
			t.Errorf("fmtTenths(%d) = %q: want a plus sign", tn, s)
		}
		for _, c := range s[1:] {
			if c != '.' && (c < '0' || c > '9') {
				t.Errorf("fmtTenths(%d) = %q: stray character %q", tn, s, c)
			}
		}
	}
	if got := fmtTenths(math.MinInt64, true); got[0] != '-' {
		t.Errorf("fmtTenths(MinInt64) = %q, want a minus sign", got)
	}
}

func TestRegression_FEAT9995385616_BoundsAreInclusive(t *testing.T) {
	ok := map[string]func(p *Patch){
		"net at bound":    func(p *Patch) { p.Net = maxCount },
		"net neg bound":   func(p *Patch) { p.Net = -maxCount },
		"score at bound":  func(p *Patch) { p.Score = maxScalar },
		"wanted at bound": func(p *Patch) { p.Wanted = -maxCount; p.Room = maxCount },
		"factor at 0":     func(p *Patch) { p.WellbeingFactor = 0 },
		"moved at bound":  func(p *Patch) { v := int64(maxCount); p.MovedIn = &v },
		"homeless bound":  func(p *Patch) { p.Homeless = &Homeless{Built: true, Count: maxCount} },
	}
	for name, f := range ok {
		s := boundScreen()
		apply(s, rr1Mut(t, f))
		if !s.HaveData() {
			t.Errorf("%s: an in-range patch was refused", name)
		}
	}
}
