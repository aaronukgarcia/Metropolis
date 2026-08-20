package tax

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// gbp converts a whole-pounds value to micro-pounds (finance.Money).
func gbp(p int64) finance.Money { return finance.Money(p) * finance.MicropoundsPerPound }

// newTestAPI loads the real data/tax_instruments.json via LoadDefault.
func newTestAPI(t *testing.T) *TaxAPI {
	t.Helper()
	api, err := LoadDefault("test-tax-" + t.Name())
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return api
}

// newTestFinance builds a FinanceAPI one monthly tick in, so tax postings
// land in a non-empty tick log that TaxRevenue can read.
func newTestFinance(t *testing.T) *finance.FinanceAPI {
	t.Helper()
	f := finance.NewFinanceAPI("test-fin-" + t.Name())
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	return f
}

// seedAccount posts an external inflow into acct so it can be debited by a
// tax collection (Post rejects an overdrawn RoleMoney account).
func seedAccount(t *testing.T, f *finance.FinanceAPI, acct finance.AccountID, amt finance.Money) {
	t.Helper()
	if _, err := f.Post(finance.Transaction{
		Description: "test seed",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amt, Category: finance.Category("seed")},
			{Account: acct, Side: finance.SideCredit, Amount: amt, Category: finance.Category("seed")},
		},
	}); err != nil {
		t.Fatalf("seedAccount(%s): %v", acct, err)
	}
}

// assertCode fails unless err is a registry error carrying exactly code.
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected error code %s, got %v", code, err)
	}
}

// shareOf returns the share of a bearer category within an incidence display
// (-1 when absent).
func shareOf(d IncidenceDisplay, cat string) float64 {
	for _, s := range d.Shares {
		if s.Category == cat {
			return s.Share
		}
	}
	return -1
}

// TestLoadInstruments (AC-2): all six instruments load from the data file —
// names, categories, rate bounds and incidence bearers all present, none
// hardcoded in Go.
func TestLoadInstruments(t *testing.T) {
	api := newTestAPI(t)
	infos := api.Instruments()
	if len(infos) != 6 {
		t.Fatalf("expected 6 instruments, got %d", len(infos))
	}

	ids := make([]string, 0, len(infos))
	have := map[string]bool{}
	for _, in := range infos {
		ids = append(ids, in.ID)
		have[in.ID] = true
	}
	for _, want := range []string{"vat", "import-duties", "corporation-tax", "paye", "council-tax", "business-rates"} {
		if !have[want] {
			t.Errorf("missing instrument %q", want)
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("Instruments() not in sorted order: %v", ids)
	}
	for _, in := range infos {
		if in.Name == "" {
			t.Errorf("instrument %s has an empty name", in.ID)
		}
		if in.Category == "" {
			t.Errorf("instrument %s has an empty category", in.ID)
		}
		if in.RateMin >= in.RateMax {
			t.Errorf("instrument %s has invalid rate bounds [%v,%v]", in.ID, in.RateMin, in.RateMax)
		}
		if len(in.Incidence) == 0 {
			t.Errorf("instrument %s has no incidence bearers", in.ID)
		}
	}
}

// TestListInstruments (AC-1): the query surface lists every instrument with
// its current rate, revenue and incidence.
func TestListInstruments(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("vat", gbp(1_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	infos := api.Instruments()
	found := false
	for _, in := range infos {
		if in.ID == "vat" {
			found = true
			if in.Rate != 20 {
				t.Errorf("vat default rate = %v, want 20 (reference rate)", in.Rate)
			}
			if in.Revenue <= 0 {
				t.Errorf("vat revenue should be positive with a base set, got %d", in.Revenue)
			}
		}
	}
	if !found {
		t.Fatal("vat not listed")
	}
}

// TestSetRate (AC-1): the rate-setting command mutates the instrument's rate.
func TestSetRate(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetRate("vat", 25); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	info, err := api.Instrument("vat")
	if err != nil {
		t.Fatalf("Instrument: %v", err)
	}
	if info.Rate != 25 {
		t.Fatalf("vat rate = %v, want 25", info.Rate)
	}
}

// TestElasticBaseShrinksWithRate (AC-3): at the same full base, a higher rate
// yields a strictly smaller taxed base — elasticity is a real input-to-output
// relationship, not a decorative coefficient.
func TestElasticBaseShrinksWithRate(t *testing.T) {
	api := newTestAPI(t)
	const id = "council-tax"
	base := gbp(10_000_000)

	// Low rate: at the reference rate the base is full.
	if err := api.SetBase(id, base); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate(id, 100); err != nil {
		t.Fatalf("SetRate low: %v", err)
	}
	low, err := api.TaxedBase(id)
	if err != nil {
		t.Fatalf("TaxedBase low: %v", err)
	}

	// Reset to the identical starting state, then a high rate.
	if err := api.SetBase(id, base); err != nil {
		t.Fatalf("SetBase reset: %v", err)
	}
	if err := api.SetRate(id, 250); err != nil {
		t.Fatalf("SetRate high: %v", err)
	}
	high, err := api.TaxedBase(id)
	if err != nil {
		t.Fatalf("TaxedBase high: %v", err)
	}

	if high >= low {
		t.Fatalf("taxed base did not shrink: low=%d high=%d (want high < low)", low, high)
	}
}

// TestLafferMarginalRevenueDecelerates (AC-4): sweeping a rate low→mid→high,
// the marginal revenue (Δrevenue/Δrate) must fall as the rate climbs —
// revenue is concave, not a straight rate × fixedBase line.
func TestLafferMarginalRevenueDecelerates(t *testing.T) {
	api := newTestAPI(t)
	const id = "council-tax"
	if err := api.SetBase(id, gbp(10_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	rev := func(rate float64) finance.Money {
		if err := api.SetRate(id, rate); err != nil {
			t.Fatalf("SetRate(%v): %v", rate, err)
		}
		r, err := api.Revenue(id)
		if err != nil {
			t.Fatalf("Revenue(%v): %v", rate, err)
		}
		return r
	}

	rLow := rev(100)
	rMid := rev(150)
	rHigh := rev(200)

	marginalLow := float64(rMid-rLow) / 50
	marginalHigh := float64(rHigh-rMid) / 50
	if marginalHigh >= marginalLow {
		t.Fatalf("marginal revenue did not decelerate: low→mid=%v, mid→high=%v (want mid→high < low→mid)",
			marginalLow, marginalHigh)
	}
	if rHigh <= rMid || rMid <= rLow {
		t.Fatalf("revenue should still rise over this sweep: %d, %d, %d", rLow, rMid, rHigh)
	}
}

// TestIncidenceShift (AC-5): changing the rate moves the bearer-category
// split — proportions, not just the total — and the split always sums to 1.0.
func TestIncidenceShift(t *testing.T) {
	api := newTestAPI(t)
	const id = "council-tax"

	if err := api.SetRate(id, 100); err != nil {
		t.Fatalf("SetRate low: %v", err)
	}
	low, err := api.IncidenceDisplay(id)
	if err != nil {
		t.Fatalf("IncidenceDisplay low: %v", err)
	}

	if err := api.SetRate(id, 250); err != nil {
		t.Fatalf("SetRate high: %v", err)
	}
	high, err := api.IncidenceDisplay(id)
	if err != nil {
		t.Fatalf("IncidenceDisplay high: %v", err)
	}

	if shareOf(low, "tenant") == shareOf(high, "tenant") {
		t.Fatalf("incidence split did not move: tenant share is %v at both rates", shareOf(low, "tenant"))
	}
	if shareOf(low, "ownerOccupier") == shareOf(high, "ownerOccupier") {
		t.Fatalf("incidence split did not move: owner share is %v at both rates", shareOf(low, "ownerOccupier"))
	}
	sumShares := func(d IncidenceDisplay) float64 {
		s := 0.0
		for _, x := range d.Shares {
			s += x.Share
		}
		return s
	}
	if math.Abs(sumShares(low)-1.0) > 1e-9 {
		t.Fatalf("low-rate shares sum to %v, want 1.0", sumShares(low))
	}
	if math.Abs(sumShares(high)-1.0) > 1e-9 {
		t.Fatalf("high-rate shares sum to %v, want 1.0", sumShares(high))
	}
}

// TestBearerSharesSumToOne (quality bar): for every instrument, at several
// rates across its range, the incidence shares sum to exactly 1.0.
func TestBearerSharesSumToOne(t *testing.T) {
	api := newTestAPI(t)
	for id, st := range api.instruments {
		rRef := referenceRate(st.def)
		max := st.def.RateRange.MaxPercent
		rates := []float64{rRef, (rRef + max) / 2, max}
		for _, r := range rates {
			shares := incidenceSharesAt(st.def, r)
			if len(shares) == 0 {
				t.Errorf("instrument %s: no shares at rate %v", id, r)
				continue
			}
			sum := 0.0
			for _, s := range shares {
				sum += s.Share
			}
			if math.Abs(sum-1.0) > 1e-9 {
				t.Errorf("instrument %s at rate %v: shares sum to %v, want exactly 1.0", id, r, sum)
			}
		}
	}
}

// TestDistrictMultiplier (AC-6): a per-district multiplier stacks with the
// citywide rate and changes the district-scoped revenue and incidence.
func TestDistrictMultiplier(t *testing.T) {
	api := newTestAPI(t)
	const id = "council-tax"
	if err := api.SetBase(id, gbp(5_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate(id, 100); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	cityRev, err := api.Revenue(id)
	if err != nil {
		t.Fatalf("Revenue: %v", err)
	}
	cityInc, err := api.IncidenceDisplay(id)
	if err != nil {
		t.Fatalf("IncidenceDisplay: %v", err)
	}

	if err := api.SetDistrictMultiplier("harbour", id, 1.5); err != nil {
		t.Fatalf("SetDistrictMultiplier: %v", err)
	}
	districtRev, err := api.RevenueInDistrict(id, "harbour")
	if err != nil {
		t.Fatalf("RevenueInDistrict: %v", err)
	}
	districtInc, err := api.IncidenceDisplayInDistrict(id, "harbour")
	if err != nil {
		t.Fatalf("IncidenceDisplayInDistrict: %v", err)
	}

	if districtRev == cityRev {
		t.Fatalf("district multiplier had no revenue effect: city=%d district=%d", cityRev, districtRev)
	}
	if shareOf(districtInc, "tenant") == shareOf(cityInc, "tenant") {
		t.Fatalf("district multiplier had no incidence effect: tenant share is %v in both", shareOf(cityInc, "tenant"))
	}
}

// TestGetDistrictMultiplierReadBack (AC-6 read-back): the getter returns the
// multiplier SetDistrictMultiplier actually stored, and 1.0 (neutral) when
// none has been set for that (district, instrument) — the applied-state
// read-back consumers use instead of each maintaining a private mirror.
func TestGetDistrictMultiplierReadBack(t *testing.T) {
	api := newTestAPI(t)
	const id = "business-rates"

	// Unset: the neutral multiplier is 1.0, never a zero value.
	if got, err := api.GetDistrictMultiplier("harbour", id); err != nil {
		t.Fatalf("GetDistrictMultiplier unset: %v", err)
	} else if got != 1.0 {
		t.Fatalf("unset multiplier = %v, want 1.0", got)
	}

	// A set multiplier is read back exactly as applied.
	if err := api.SetDistrictMultiplier("harbour", id, 0.9); err != nil {
		t.Fatalf("SetDistrictMultiplier: %v", err)
	}
	if got, err := api.GetDistrictMultiplier("harbour", id); err != nil {
		t.Fatalf("GetDistrictMultiplier: %v", err)
	} else if got != 0.9 {
		t.Fatalf("read-back multiplier = %v, want 0.9", got)
	}

	// A different district is independent: still neutral.
	if got, err := api.GetDistrictMultiplier("other", id); err != nil {
		t.Fatalf("GetDistrictMultiplier other: %v", err)
	} else if got != 1.0 {
		t.Fatalf("other-district multiplier = %v, want 1.0", got)
	}

	// Unknown instrument and empty district are rejected, never silently valid.
	if _, err := api.GetDistrictMultiplier("harbour", "fuelDuty"); err == nil {
		t.Fatal("unknown instrument silently accepted")
	} else {
		assertCode(t, err, ErrUnknownInstrument)
	}
	if _, err := api.GetDistrictMultiplier("", id); err == nil {
		t.Fatal("empty district silently accepted")
	} else {
		assertCode(t, err, ErrInvalidDistrictMultiplier)
	}
}

// TestBusinessRateUsesLandPrice (AC-7): revenue is computed from
// finance.LandPrice — two cells with different land values give different
// revenue, and an unknown zone class is rejected.
func TestBusinessRateUsesLandPrice(t *testing.T) {
	api := newTestAPI(t)

	urban := ZoneCell{ZoneClass: "shop", Cell: finance.LandCell{Terrain: finance.TerrainUrban}}
	grass := ZoneCell{ZoneClass: "shop", Cell: finance.LandCell{Terrain: finance.TerrainGrass}}

	revUrban, err := api.BusinessRateRevenue([]ZoneCell{urban})
	if err != nil {
		t.Fatalf("BusinessRateRevenue(urban): %v", err)
	}
	revGrass, err := api.BusinessRateRevenue([]ZoneCell{grass})
	if err != nil {
		t.Fatalf("BusinessRateRevenue(grass): %v", err)
	}
	if revUrban == revGrass {
		t.Fatalf("two cells with different land values gave identical revenue %d", revUrban)
	}
	if revUrban <= revGrass {
		t.Fatalf("urban (higher land value) should yield more revenue than grass: %d <= %d", revUrban, revGrass)
	}

	// The heavy-industry zone override (0.7) discounts the full-rate liability.
	heavy := ZoneCell{ZoneClass: "heavyIndustry", Cell: finance.LandCell{Terrain: finance.TerrainUrban}}
	revHeavy, err := api.BusinessRateRevenue([]ZoneCell{heavy})
	if err != nil {
		t.Fatalf("BusinessRateRevenue(heavyIndustry): %v", err)
	}
	if revHeavy >= revUrban {
		t.Fatalf("heavy-industry discount should reduce revenue: %d >= %d", revHeavy, revUrban)
	}

	if _, err := api.BusinessRateRevenue([]ZoneCell{{ZoneClass: "warehouse", Cell: finance.LandCell{Terrain: finance.TerrainUrban}}}); err == nil {
		t.Fatal("unknown zone class was silently accepted")
	} else {
		assertCode(t, err, ErrUnknownZoneClass)
	}
}

// TestCollectedRevenueViaFinance (AC-8): Collect posts revenue through
// finance's ledger, CollectedRevenue derives from a FinanceAPI query, and no
// independent running total is maintained.
func TestCollectedRevenueViaFinance(t *testing.T) {
	f := newTestFinance(t)
	api := newTestAPI(t)
	if err := api.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	seedAccount(t, f, finance.AcctFirms, gbp(1_000_000)) // vat payer is firms

	if err := api.SetBase("vat", gbp(100_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate("vat", 20); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	got, err := api.Collect("vat")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := gbp(20_000) // 20% of £100k
	if got != want {
		t.Fatalf("Collect = %d, want %d", got, want)
	}

	// The treasury was credited.
	if bal, ok := f.AccountBalance(finance.AcctTreasury); !ok || bal < want {
		t.Fatalf("treasury balance %d (ok=%v) does not reflect the collected %d", bal, ok, want)
	}
	// The firms account was debited by the same amount.
	if bal, _ := f.AccountBalance(finance.AcctFirms); bal > gbp(1_000_000)-want {
		t.Fatalf("firms account %d was not debited the collected amount", bal)
	}

	// CollectedRevenue derives from the finance ledger, not an accumulator.
	collected, err := api.CollectedRevenue()
	if err != nil {
		t.Fatalf("CollectedRevenue: %v", err)
	}
	if collected != want {
		t.Fatalf("CollectedRevenue = %d, want %d", collected, want)
	}
	if f.TaxRevenue() != want {
		t.Fatalf("finance.TaxRevenue = %d, want %d", f.TaxRevenue(), want)
	}
}

// TestCollectWithoutFinanceRejected (GR#17): finance-dependent operations fail
// loudly rather than silently no-op when finance was never wired.
func TestCollectWithoutFinanceRejected(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("vat", gbp(100)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if _, err := api.Collect("vat"); err == nil {
		t.Fatal("Collect without SetFinance silently succeeded")
	} else {
		assertCode(t, err, ErrFinanceNotWired)
	}
	if _, err := api.CollectedRevenue(); err == nil {
		t.Fatal("CollectedRevenue without SetFinance silently succeeded")
	} else {
		assertCode(t, err, ErrFinanceNotWired)
	}
}

// TestFuelDutyEVShareErosion (AC-9): pushing a growing EV-share erodes the
// taxed base and therefore revenue. The fuel-duty instrument itself is out of
// scope (later sprint); this exercises the generic base-erosion input that a
// future fuel-duty instrument will consume.
func TestFuelDutyEVShareErosion(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("vat", gbp(1_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate("vat", 20); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if err := api.SetEVShare("vat", 0.2); err != nil {
		t.Fatalf("SetEVShare 0.2: %v", err)
	}
	lowErosion, err := api.Revenue("vat")
	if err != nil {
		t.Fatalf("Revenue: %v", err)
	}

	if err := api.SetEVShare("vat", 0.8); err != nil {
		t.Fatalf("SetEVShare 0.8: %v", err)
	}
	highErosion, err := api.Revenue("vat")
	if err != nil {
		t.Fatalf("Revenue: %v", err)
	}

	if highErosion >= lowErosion {
		t.Fatalf("EV-share did not erode revenue: ev=0.2→%d, ev=0.8→%d (want smaller)", lowErosion, highErosion)
	}

	// The EV-share is a documented field on the query response (AC-9).
	info, err := api.Instrument("vat")
	if err != nil {
		t.Fatalf("Instrument: %v", err)
	}
	if info.EVShare != 0.8 {
		t.Fatalf("Instrument EVShare = %v, want 0.8", info.EVShare)
	}
}

// TestInvalidRateRejected (AC-11): an out-of-range rate is rejected with a
// registry-sourced error naming the instrument, and the current rate is left
// unchanged — never clamped or silently accepted.
func TestInvalidRateRejected(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetRate("vat", 20); err != nil {
		t.Fatalf("SetRate valid: %v", err)
	}

	err := api.SetRate("vat", 50) // vat max is 30
	assertCode(t, err, ErrRateOutOfRange)

	info, err := api.Instrument("vat")
	if err != nil {
		t.Fatalf("Instrument: %v", err)
	}
	if info.Rate != 20 {
		t.Fatalf("rate was mutated/clamped by the rejected SetRate: got %v, want 20", info.Rate)
	}

	// Non-finite rates are a distinct rejection, also leaving the rate alone.
	err = api.SetRate("vat", math.NaN())
	assertCode(t, err, ErrNonFiniteRate)
	info, _ = api.Instrument("vat")
	if info.Rate != 20 {
		t.Fatalf("NaN SetRate mutated the rate: got %v, want 20", info.Rate)
	}
}

// TestUnregisteredInstrument (AC-12): querying/setting an unknown instrument
// key returns ErrUnknownInstrument, never a silently-valid zero value.
func TestUnregisteredInstrument(t *testing.T) {
	api := newTestAPI(t)

	if _, err := api.Revenue("fuelDuty"); err == nil {
		t.Fatal("Revenue(unknown) returned a zero-value instrument without error")
	} else {
		assertCode(t, err, ErrUnknownInstrument)
	}
	if err := api.SetRate("fuelDuty", 10); err == nil {
		t.Fatal("SetRate(unknown) accepted an unregistered key")
	} else {
		assertCode(t, err, ErrUnknownInstrument)
	}
	if _, err := api.IncidenceDisplay("fuelDuty"); err == nil {
		t.Fatal("IncidenceDisplay(unknown) returned a zero-value instrument without error")
	} else {
		assertCode(t, err, ErrUnknownInstrument)
	}

	// The unregistered key never leaks into the listing.
	for _, in := range api.Instruments() {
		if in.ID == "fuelDuty" {
			t.Fatal("unregistered instrument leaked into Instruments()")
		}
	}
}

// TestDeterministicTotals (AC-13): repeated runs produce identical sorted
// ordering and identical cross-instrument totals — never map-iteration order.
func TestDeterministicTotals(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("vat", gbp(3_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetBase("paye", gbp(7_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate("vat", 25); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if err := api.SetRate("paye", 30); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	var firstIDs []string
	var firstTotal finance.Money
	for i := 0; i < 100; i++ {
		infos := api.Instruments()
		ids := make([]string, len(infos))
		for j, in := range infos {
			ids[j] = in.ID
		}
		total := api.RevenueTotal()
		if i == 0 {
			firstIDs = ids
			firstTotal = total
			if !sort.StringsAreSorted(ids) {
				t.Fatalf("Instruments() not sorted: %v", ids)
			}
			continue
		}
		if !reflect.DeepEqual(ids, firstIDs) {
			t.Fatalf("instrument order changed across runs: %v vs %v", ids, firstIDs)
		}
		if total != firstTotal {
			t.Fatalf("RevenueTotal drifted across runs: %d vs %d", total, firstTotal)
		}
	}
}

// TestConcurrentQuerySet (AC-15): concurrent rate-setting and querying across
// shards is race-free — every operation succeeds or returns a specific error,
// never a panic or a third outcome.
func TestConcurrentQuerySet(t *testing.T) {
	api := newTestAPI(t)
	const workers = 16
	const iters = 100

	var wg sync.WaitGroup
	var errCount int64
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				rate := 20 + float64((i+j)%10) // 20..29, all valid for vat
				if err := api.SetRate("vat", rate); err != nil {
					atomic.AddInt64(&errCount, 1)
				}
				if _, err := api.Revenue("vat"); err != nil {
					atomic.AddInt64(&errCount, 1)
				}
				if len(api.Instruments()) == 0 {
					atomic.AddInt64(&errCount, 1)
				}
				if _, err := api.IncidenceDisplay("vat"); err != nil {
					atomic.AddInt64(&errCount, 1)
				}
			}
		}()
	}
	wg.Wait()
	if errCount != 0 {
		t.Fatalf("%d concurrent operations returned an unexpected error", errCount)
	}
}

// apiCopy takes a same-package value copy of *TaxAPI via an unsafe
// byte-copy (mirrors engine.unlocks' apiCopy / engine.world's w2Copy
// convention): a plain `cp := *api` is legal Go producing the identical
// attack shape, but go vet's copylocks check would flag the LITERAL
// assignment at its own call site and fail this package's own `go vet`
// gate. The byte-copy reaches the same struct-value copy through a route
// copylocks does not statically recognise.
func apiCopy(t *TaxAPI) *TaxAPI {
	c := new(TaxAPI)
	*(*[unsafe.Sizeof(TaxAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(TaxAPI{})]byte)(unsafe.Pointer(t))
	return c
}

// TestCopiedValueRejected (SEC-020-class): a struct-copied TaxAPI rejects
// method calls rather than silently sharing the original's state.
func TestCopiedValueRejected(t *testing.T) {
	api := newTestAPI(t)
	cp := apiCopy(api)
	if _, err := cp.Revenue("vat"); err == nil {
		t.Fatal("copied TaxAPI accepted a method call")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
}

// TestZoneClassEnumCoversData (weakness pattern #2 drift guard): every zone
// class the loaded data file actually uses is in this package's local
// 8-way enum.
func TestZoneClassEnumCoversData(t *testing.T) {
	api := newTestAPI(t)
	if len(zoneClassEnum) != 8 {
		t.Errorf("zoneClassEnum has %d entries, want 8 (§34)", len(zoneClassEnum))
	}
	for id, st := range api.instruments {
		for z := range st.def.ZoneOverrides {
			if !zoneClassEnum[z] {
				t.Errorf("instrument %s uses zone class %q not in the local 8-way enum", id, z)
			}
		}
	}
}

// TestDistrictMultiplierRejectsExcessiveMultiplier (SEC-098): a district
// multiplier that would push the effective rate (citywide rate × multiplier)
// past the instrument's data-declared maximum is rejected, never stored —
// the AC-11 rate cap is not bypassable at district level.
func TestDistrictMultiplierRejectsExcessiveMultiplier(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetBase("council-tax", gbp(10_000_000)); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	if err := api.SetRate("council-tax", 100); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	// council tax max is 400%; a 5x multiplier gives an effective 500%.
	err := api.SetDistrictMultiplier("harbour", "council-tax", 5)
	assertCode(t, err, ErrInvalidDistrictMultiplier)

	city, err := api.Revenue("council-tax")
	if err != nil {
		t.Fatalf("Revenue: %v", err)
	}
	district, err := api.RevenueInDistrict("council-tax", "harbour")
	if err != nil {
		t.Fatalf("RevenueInDistrict: %v", err)
	}
	if district != city {
		t.Fatalf("rejected multiplier was stored: district=%d city=%d", district, city)
	}

	// A multiplier that keeps the effective rate within bounds is accepted.
	if err := api.SetDistrictMultiplier("harbour", "council-tax", 2); err != nil {
		t.Fatalf("in-range multiplier rejected: %v", err)
	}
}

// TestRevenueMonotonicInRate (SEC-098): revenue must be monotonic
// non-decreasing in the rate even at rates far past the instrument's
// declared range, where the elasticity has shrunk the base below one
// micro-pound. The pre-fix code rounded that sub-micro-pound base to 0
// before the rate multiply, collapsing revenue from MaxInt64 to 0.
func TestRevenueMonotonicInRate(t *testing.T) {
	const fullBase = finance.Money(10_000_000) * finance.MicropoundsPerPound // £10M
	prev := revenueAt(fullBase, 0, 100, 0.25, 100)
	for _, r := range []float64{1e3, 1e6, 1e9, 1e30, 1e54, 1e56} {
		got := revenueAt(fullBase, 0, 100, 0.25, r)
		if got < prev {
			t.Fatalf("revenue not monotonic: rate %g produced %d after %d", r, got, prev)
		}
		prev = got
	}
}

// TestMoneyFromFloatClampsNonFinite (SEC-099): the float→money choke point
// clamps NaN to 0 (never MinInt64 — money is never negative, GR#16) and
// +Inf to MaxInt64, reusing num.ClampInt64FromFloat.
func TestMoneyFromFloatClampsNonFinite(t *testing.T) {
	if got := moneyFromFloat(math.NaN()); got != 0 {
		t.Fatalf("moneyFromFloat(NaN) = %d, want 0 (negative money is GR#16 corruption)", got)
	}
	if got := moneyFromFloat(math.Inf(1)); got != finance.Money(math.MaxInt64) {
		t.Fatalf("moneyFromFloat(+Inf) = %d, want MaxInt64", got)
	}
	if got := moneyFromFloat(math.Inf(-1)); got != 0 {
		t.Fatalf("moneyFromFloat(-Inf) = %d, want 0 (money is never negative)", got)
	}
}
