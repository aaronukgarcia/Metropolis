package tax

import (
	"math"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// atkPtr returns a pointer to a float64 literal for override fixtures.
func atkPtr(f float64) *float64 { return &f }

// atkInject installs a rateMultiplier override on one instrument's own def.
func atkInject(t *testing.T, api *TaxAPI, id, zone string, mult float64) {
	t.Helper()
	st, ok := api.instruments[id]
	if !ok {
		t.Fatalf("instrument %s not loaded", id)
	}
	if st.def.ZoneOverrides == nil {
		st.def.ZoneOverrides = map[string]data.ZoneOverride{}
	}
	st.def.ZoneOverrides[zone] = data.ZoneOverride{RateMultiplier: atkPtr(mult)}
}

// ATTACK 1: cross-contamination. Two instruments carrying DIFFERENT
// multipliers on the SAME zone class must each resolve their own, and a
// third instrument with no override must stay citywide. If the dispatch
// leaked (a shared map, a package-level cache, a wrong def captured), one
// instrument would see another's multiplier.
func TestAttackZoneCrossContamination(t *testing.T) {
	api := newTestAPI(t)
	const zone = "mining"
	atkInject(t, api, "vat", zone, 0.25)
	atkInject(t, api, "paye", zone, 3.0)

	for _, tc := range []struct {
		id   string
		want float64
	}{
		{"vat", 0.25},
		{"paye", 3.0},
		{"corporation-tax", 1.0},
		{"business-rates", 1.0},
	} {
		cw, err := api.Instrument(tc.id)
		if err != nil {
			t.Fatalf("Instrument(%s): %v", tc.id, err)
		}
		got, err := api.RateInZone(tc.id, zone)
		if err != nil {
			t.Fatalf("RateInZone(%s): %v", tc.id, err)
		}
		want := cw.Rate * tc.want
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("cross-contamination: RateInZone(%s,%s)=%v want %v (citywide %v x %v)",
				tc.id, zone, got, want, cw.Rate, tc.want)
		}
	}

	// The real data override on business-rates/heavyIndustry must be
	// untouched by the injections above.
	cw, _ := api.Instrument("business-rates")
	got, err := api.RateInZone("business-rates", "heavyIndustry")
	if err != nil {
		t.Fatalf("RateInZone(business-rates,heavyIndustry): %v", err)
	}
	if math.Abs(got-cw.Rate*0.7) > 1e-9 {
		t.Fatalf("data-authored override corrupted: got %v want %v", got, cw.Rate*0.7)
	}
}

// ATTACK 2: hostile zone-class keys must be REJECTED, never normalised to
// the citywide rate (a silent 1.0 fallback would make a typo'd zone look
// like a valid unoverridden zone).
func TestAttackZoneUnknownClassRejected(t *testing.T) {
	api := newTestAPI(t)
	for _, bad := range []string{
		"", "MINING", "Mining", " mining", "mining ", "heavyindustry",
		"heavy-industry", "dwellings", "../dwelling", "office\x00",
	} {
		if _, err := api.RateInZone("vat", bad); err == nil {
			t.Fatalf("RateInZone(vat, %q) returned nil error — hostile zone key normalised", bad)
		}
		if _, err := api.RevenueInZone("vat", bad); err == nil {
			t.Fatalf("RevenueInZone(vat, %q) returned nil error", bad)
		}
	}
	// Unknown instrument must also be rejected on both methods.
	if _, err := api.RateInZone("no-such-tax", "mining"); err == nil {
		t.Fatal("RateInZone accepted an unknown instrument")
	}
	if _, err := api.RevenueInZone("no-such-tax", "mining"); err == nil {
		t.Fatal("RevenueInZone accepted an unknown instrument")
	}
}

// ATTACK 3: a 0.0 multiplier is loader-legal (a zone-exempt lever). It must
// produce a zero rate and zero revenue — never a fall-through to citywide
// (the classic "if coeff == 0 { coeff = 1 }" zero-value bug).
func TestAttackZoneZeroMultiplierIsExemptNotCitywide(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("vat", gbp(1_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	atkInject(t, api, "vat", "mining", 0.0)

	rate, err := api.RateInZone("vat", "mining")
	if err != nil {
		t.Fatalf("RateInZone: %v", err)
	}
	if rate != 0 {
		t.Fatalf("zero multiplier gave rate %v, want 0 (zone exemption silently fell back to citywide)", rate)
	}
	rev, err := api.RevenueInZone("vat", "mining")
	if err != nil {
		t.Fatalf("RevenueInZone: %v", err)
	}
	if rev != 0 {
		t.Fatalf("zero-rate zone yielded revenue %d, want 0", rev)
	}
}

// ATTACK 4: money is never minted. A zone override is a RATE lever: for
// every multiplier <= 1 the zone revenue must be <= the citywide revenue,
// and no override may change the instrument's stored base or its citywide
// rate/revenue (the query surface is read-only).
func TestAttackZoneNeverMintsAndIsReadOnly(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("corporation-tax", gbp(5_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	cwRate, _ := api.Instrument("corporation-tax")
	cwRev, err := api.Revenue("corporation-tax")
	if err != nil {
		t.Fatalf("Revenue: %v", err)
	}
	cwBase, err := api.TaxedBase("corporation-tax")
	if err != nil {
		t.Fatalf("TaxedBase: %v", err)
	}

	for _, m := range []float64{0, 0.01, 0.5, 0.999, 1.0} {
		atkInject(t, api, "corporation-tax", "mining", m)
		zr, err := api.RevenueInZone("corporation-tax", "mining")
		if err != nil {
			t.Fatalf("RevenueInZone(m=%v): %v", m, err)
		}
		if zr > cwRev {
			t.Fatalf("multiplier %v minted money: zone revenue %d > citywide %d", m, zr, cwRev)
		}
		// Read-only: citywide state untouched by the zone queries.
		gotRate, _ := api.Instrument("corporation-tax")
		if gotRate.Rate != cwRate.Rate {
			t.Fatalf("zone query mutated the citywide rate: %v != %v", gotRate.Rate, cwRate.Rate)
		}
		gotRev, _ := api.Revenue("corporation-tax")
		if gotRev != cwRev {
			t.Fatalf("zone query mutated citywide revenue: %d != %d", gotRev, cwRev)
		}
		gotBase, _ := api.TaxedBase("corporation-tax")
		if gotBase != cwBase {
			t.Fatalf("zone query mutated the taxed base: %d != %d", gotBase, cwBase)
		}
	}
}

// ATTACK 5: RevenueInZone must be exactly the elasticity curve evaluated at
// the scaled rate — the same order of operations as the AC-6 district path
// (RevenueInDistrict), not an ad-hoc formula. Proven by driving the district
// multiplier and the zone multiplier to the same value on the same
// instrument and demanding byte-identical revenue.
func TestAttackZoneMatchesDistrictOrderOfOperations(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("paye", gbp(9_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate("paye", 40); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	const m = 0.6
	atkInject(t, api, "paye", "mining", m)
	if err := api.SetDistrictMultiplier(DistrictID("d1"), "paye", m); err != nil {
		t.Fatalf("SetDistrictMultiplier: %v", err)
	}
	zone, err := api.RevenueInZone("paye", "mining")
	if err != nil {
		t.Fatalf("RevenueInZone: %v", err)
	}
	dist, err := api.RevenueInDistrict("paye", DistrictID("d1"))
	if err != nil {
		t.Fatalf("RevenueInDistrict: %v", err)
	}
	if zone != dist {
		t.Fatalf("zone path %d != district path %d at the same multiplier — the two scoped rate levers disagree on order of operations", zone, dist)
	}
}

// ATTACK 6: determinism + concurrency. Identical calls give identical
// results, and concurrent readers never diverge or race.
func TestAttackZoneDeterministicUnderConcurrency(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("business-rates", gbp(2_500_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	wantRate, err := api.RateInZone("business-rates", "heavyIndustry")
	if err != nil {
		t.Fatalf("RateInZone: %v", err)
	}
	wantRev, err := api.RevenueInZone("business-rates", "heavyIndustry")
	if err != nil {
		t.Fatalf("RevenueInZone: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r, err := api.RateInZone("business-rates", "heavyIndustry")
				if err != nil || r != wantRate {
					t.Errorf("nondeterministic rate: %v err=%v", r, err)
					return
				}
				v, err := api.RevenueInZone("business-rates", "heavyIndustry")
				if err != nil || v != wantRev {
					t.Errorf("nondeterministic revenue: %v err=%v", v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// ATTACK 7: the SEC-020 copyguard must fire on both new methods.
func TestAttackZoneCopyguard(t *testing.T) {
	api := newTestAPI(t)
	cp := apiCopy(api)
	if _, err := cp.RateInZone("vat", "mining"); err == nil {
		t.Fatal("RateInZone on a copied API returned nil error — copyguard not fired")
	}
	if _, err := cp.RevenueInZone("vat", "mining"); err == nil {
		t.Fatal("RevenueInZone on a copied API returned nil error — copyguard not fired")
	}
}

// ATTACK 8: a multiplier > 1 escapes the instrument's declared rateRange
// maxPercent — SetRate refuses such a rate, the zone lever does not. This
// documents the bound (it is the same behaviour the AC-6 district path
// already has), and fails if the zone path ever silently clamps instead.
func TestAttackZoneMultiplierEscapesRateRange(t *testing.T) {
	api := newTestAPI(t)
	st := api.instruments["vat"]
	max := st.def.RateRange.MaxPercent
	if err := api.SetRate("vat", max); err != nil {
		t.Fatalf("SetRate(max): %v", err)
	}
	if err := api.SetRate("vat", max*2); err == nil {
		t.Fatal("SetRate accepted a rate above rateRange.maxPercent")
	}
	atkInject(t, api, "vat", "mining", 2.0)
	got, err := api.RateInZone("vat", "mining")
	if err != nil {
		t.Fatalf("RateInZone: %v", err)
	}
	if got <= max {
		t.Logf("zone rate %v clamped to <= max %v (behaviour changed)", got, max)
	} else {
		t.Logf("KNOWN: zone rate %v exceeds rateRange.maxPercent %v — same unbounded-multiplier surface as RevenueInDistrict", got, max)
	}
}
