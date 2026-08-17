package mining

import (
	"math"
	"testing"
)

// This file is the SEC-208 regression suite: the maxDataMagnitude overflow
// guard must live at the CONSTRUCTOR (NewDepositMap), not only in the
// loader. A caller who builds a DepositParams by hand — bypassing
// LoadDepositParams — used to reach the shuffle with a +Inf/NaN curve span
// (ShuffleTile then wrote 40,000 Inf/NaN-Size deposits in one tile) or a
// +Inf weight total (every draw fell through to the LAST candidate,
// silently inverting geology co-location: a coal-measures tile returned the
// final resource instead of coal/gas). NewDepositMap must now reject such
// params fail-closed with ErrDepositDataInvalid, holding a
// caller-constructed config to exactly the domain the loader enforces.

// TestNewDepositMapRejectsCurveOverflow reproduces repro 1 from the
// verdict: SizeCurve{Min:-1e308, Max:1e308} makes (Max-Min) overflow to
// +Inf, so drawCurve returns +Inf and the shuffle writes Inf/NaN-Size
// deposits. The constructor must reject the span before any shuffle runs,
// returning (nil, ErrDepositDataInvalid) rather than a map that would
// produce Inf/NaN deposits.
func TestNewDepositMapRejectsCurveOverflow(t *testing.T) {
	p := realParams(t)
	p.SizeCurve = CurveParams{Shape: 1, Min: -1e308, Max: 1e308}

	m, err := NewDepositMap(1, newWorld(t), p)
	assertErrCode(t, err, ErrDepositDataInvalid)
	if m != nil {
		t.Fatalf("NewDepositMap returned a non-nil map alongside a rejection — fail-closed means nil map, no Inf/NaN deposits")
	}
}

// TestNewDepositMapRejectsNonFiniteCurve covers the non-finite half of the
// guard: a NaN/Inf curve Shape/Min/Max must be rejected at construction too
// (a bare < / > comparison would let NaN sail through — GR#16).
func TestNewDepositMapRejectsNonFiniteCurve(t *testing.T) {
	cases := []struct {
		name  string
		curve CurveParams
	}{
		{"min-nan", CurveParams{Shape: 1, Min: math.NaN(), Max: 100}},
		{"max-inf", CurveParams{Shape: 1, Min: 0, Max: math.Inf(1)}},
		{"shape-nan", CurveParams{Shape: math.NaN(), Min: 0, Max: 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := realParams(t)
			p.SizeCurve = tc.curve

			m, err := NewDepositMap(1, newWorld(t), p)
			assertErrCode(t, err, ErrDepositDataInvalid)
			if m != nil {
				t.Fatalf("NewDepositMap returned a non-nil map alongside a rejection")
			}
		})
	}
}

// TestNewDepositMapRejectsNilWorld is the SEC-217 regression test: a nil
// *world.WorldAPI used to sail through NewDepositMap (the params were valid,
// so the constructor returned a non-nil map), and that map's first
// ShuffleTile dereferenced the nil world in PocketGeology/CellAt — an untyped
// nil-pointer panic (GR#1 violation). The constructor must now trap the nil
// world fail-closed, mirroring NewBlightAPI, returning (nil,
// ErrDepositDataInvalid) so a map that would panic is never returned.
func TestNewDepositMapRejectsNilWorld(t *testing.T) {
	p := realParams(t) // valid params, so nil world is the ONLY rejection cause

	m, err := NewDepositMap(1, nil, p)
	assertErrCode(t, err, ErrDepositDataInvalid)
	if m != nil {
		t.Fatalf("NewDepositMap returned a non-nil map alongside a nil-world rejection — fail-closed means nil map, no ShuffleTile panic")
	}
}

// TestNewDepositMapRejectsHostileWeights reproduces repro 2: a hostile
// weight magnitude (CoalCoalFactor, GenerosityMultiplier, or a resource
// CountWeight at ~1e308) drives chooseType's weight total to +Inf, so
// `pick := unitFloat(draw) * total` is +Inf/NaN and the pick loop falls
// through to the LAST candidate — a coal-measures tile silently returns the
// final resource instead of coal/gas (inverted geology co-location). The
// constructor must reject such weights before any tile is shuffled.
func TestNewDepositMapRejectsHostileWeights(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DepositParams)
	}{
		{"coal-coal-factor-overflow", func(p *DepositParams) {
			p.CoLocation.CoalCoalFactor = 1e308
		}},
		{"coal-coal-factor-inf", func(p *DepositParams) {
			p.CoLocation.CoalCoalFactor = math.Inf(1)
		}},
		{"generosity-overflow", func(p *DepositParams) {
			p.EastKentCoalfield.GenerosityMultiplier = 1e308
		}},
		{"count-weight-overflow", func(p *DepositParams) {
			for i := range p.Resources {
				p.Resources[i].CountWeight = 1e308
			}
		}},
		{"count-weight-nan", func(p *DepositParams) {
			for i := range p.Resources {
				p.Resources[i].CountWeight = math.NaN()
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := realParams(t)
			tc.mutate(&p)

			m, err := NewDepositMap(1, newWorld(t), p)
			assertErrCode(t, err, ErrDepositDataInvalid)
			if m != nil {
				t.Fatalf("NewDepositMap returned a non-nil map alongside a rejection")
			}
		})
	}
}
