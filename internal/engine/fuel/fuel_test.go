package fuel

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

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

// newTestFuel loads the real data/fuel.json via LoadDefault.
func newTestFuel(t *testing.T) *FuelAPI {
	t.Helper()
	f, err := LoadDefault("test-fuel-" + t.Name())
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return f
}

func newTestTax(t *testing.T) *tax.TaxAPI {
	t.Helper()
	a, err := tax.LoadDefault("test-tax-" + t.Name())
	if err != nil {
		t.Fatalf("tax LoadDefault: %v", err)
	}
	return a
}

func newTestLogistics(t *testing.T) *logistics.LogisticsAPI {
	t.Helper()
	l, err := logistics.LoadDefault("test-logistics-" + t.Name())
	if err != nil {
		t.Fatalf("logistics LoadDefault: %v", err)
	}
	return l
}

func newTestConsumption(t *testing.T) *consumption.UtilityAPI {
	t.Helper()
	u, err := consumption.LoadDefault("test-consumption-" + t.Name())
	if err != nil {
		t.Fatalf("consumption LoadDefault: %v", err)
	}
	return u
}

// TestFleetEraShift (AC-2a): the car segment's EV share strictly increases
// across at least three successive eras — the EV transition reads as a real,
// staged mechanic, not a single toggle.
func TestFleetEraShiftEVShareRisesAcrossEras(t *testing.T) {
	f := newTestFuel(t)
	eras := f.Eras()
	if len(eras) < 3 {
		t.Fatalf("need at least three successive eras, got %d", len(eras))
	}
	prev := -1.0
	for _, era := range eras {
		fe, err := f.FleetComposition(era)
		if err != nil {
			t.Fatalf("FleetComposition(%q): %v", era, err)
		}
		if fe.CarEVShare <= prev {
			t.Fatalf("car EV share did not strictly increase: era %q = %v, previous = %v", era, fe.CarEVShare, prev)
		}
		prev = fe.CarEVShare
	}
}

// TestTrucksLast (AC-2b): at every era, the truck segment's EV share lags the
// car segment's — the spec's explicit "trucks last" ordering, which a single
// undifferentiated citywide adoption percentage could not produce.
func TestTrucksLastTruckEVShareLagsCar(t *testing.T) {
	f := newTestFuel(t)
	for _, era := range f.Eras() {
		fe, err := f.FleetComposition(era)
		if err != nil {
			t.Fatalf("FleetComposition(%q): %v", era, err)
		}
		if fe.TruckEVShare > fe.CarEVShare {
			t.Fatalf("era %q: truck EV share %v exceeds car EV share %v (trucks must lag cars)", era, fe.TruckEVShare, fe.CarEVShare)
		}
	}
}

// TestUnknownEraRejected: FleetComposition on an era not in the data file
// returns ErrUnknownEra, never a silently-valid zero-value fleet.
func TestUnknownEraRejected(t *testing.T) {
	f := newTestFuel(t)
	if _, err := f.FleetComposition("prehistory"); err == nil {
		t.Fatal("FleetComposition(unknown era) succeeded without error")
	} else {
		assertCode(t, err, ErrUnknownEra)
	}
}

// TestFuelShortageStrandsLogistics (AC-3): injecting a fuel-supply shortfall
// (reduced tanker throughput, no reserve) degrades the replenishment delivery
// read back through engine.logistics' own Deliverable state for a
// fuel-transported commodity — not merely a fuel-side flag.
func TestFuelShortageStrandsLogistics(t *testing.T) {
	f := newTestFuel(t)
	l := newTestLogistics(t)
	if err := f.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	const era = "early"
	const district = "north"
	const requested = int64(10000)

	// Baseline: tanker at default (covers demand), no reserve → factor 1.0.
	base, err := f.ReplenishmentDelivery(era, district, ReplenishFoodStaples, requested)
	if err != nil {
		t.Fatalf("baseline ReplenishmentDelivery: %v", err)
	}
	if base.Delivered <= 0 {
		t.Fatalf("baseline delivered = %d, want > 0", base.Delivered)
	}

	// Shortfall: cut tanker throughput far below liquid demand, no reserve.
	if err := f.SetTankerThroughput(50000); err != nil {
		t.Fatalf("SetTankerThroughput: %v", err)
	}
	if err := f.SetStrategicReserve(0); err != nil {
		t.Fatalf("SetStrategicReserve: %v", err)
	}
	short, err := f.ReplenishmentDelivery(era, district, ReplenishFoodStaples, requested)
	if err == nil {
		t.Fatal("unmitigated shortage returned no error")
	}
	assertCode(t, err, ErrFuelShortage)
	if short.Delivered >= base.Delivered {
		t.Fatalf("fuel shortage did not degrade logistics delivery: delivered %d vs baseline %d", short.Delivered, base.Delivered)
	}
	if short.Shortfall == 0 {
		t.Fatalf("degraded delivery reports zero shortfall: %+v", short)
	}
}

// TestUnmitigatedShortageReturnsRegistryError (AC-7, GR#7): a fuel shortage
// with no strategic reserve and no alternative supply path returns the
// registry-sourced ErrFuelShortage AND a degraded logistics delivery — never a
// silent stall in the dependent engine.logistics deliveries without a recorded
// event. The error code is asserted directly (BUG-100: not merely a
// matching-named test function).
func TestUnmitigatedShortageReturnsRegistryError(t *testing.T) {
	f := newTestFuel(t)
	l := newTestLogistics(t)
	if err := f.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	if err := f.SetTankerThroughput(1000); err != nil {
		t.Fatalf("SetTankerThroughput: %v", err)
	}
	if err := f.SetStrategicReserve(0); err != nil {
		t.Fatalf("SetStrategicReserve: %v", err)
	}

	delivery, err := f.ReplenishmentDelivery("early", "north", ReplenishFoodFresh, 10000)
	if err == nil {
		t.Fatal("unmitigated shortage returned no error")
	}
	assertCode(t, err, ErrFuelShortage)
	if delivery.Shortfall == 0 {
		t.Fatalf("unmitigated shortage recorded no delivery degradation (silent stall): %+v", delivery)
	}
}

// TestStrategicReserveMitigatesShortage (AC-3): in an otherwise-identical
// shortfall scenario, a stocked strategic reserve prevents the logistics
// degradation — proving the reserve is a real mitigant, not a cosmetic dial.
func TestStrategicReserveMitigatesShortage(t *testing.T) {
	f := newTestFuel(t)
	l := newTestLogistics(t)
	if err := f.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	const era = "early"
	const district = "north"
	const requested = int64(10000)

	if err := f.SetTankerThroughput(50000); err != nil {
		t.Fatalf("SetTankerThroughput: %v", err)
	}
	if err := f.SetStrategicReserve(0); err != nil {
		t.Fatalf("SetStrategicReserve(0): %v", err)
	}
	_, noReserveErr := f.ReplenishmentDelivery(era, district, ReplenishFoodStaples, requested)
	if noReserveErr == nil {
		t.Fatal("expected the no-reserve scenario to be a shortage")
	}

	// Same shortfall, but a stocked reserve covers it.
	if err := f.SetStrategicReserve(400000); err != nil {
		t.Fatalf("SetStrategicReserve(cover): %v", err)
	}
	mitigated, err := f.ReplenishmentDelivery(era, district, ReplenishFoodStaples, requested)
	if err != nil {
		t.Fatalf("stocked reserve should prevent the shortage, got %v", err)
	}
	if mitigated.Delivered != requested {
		t.Fatalf("stocked reserve delivery = %d, want full %d", mitigated.Delivered, requested)
	}
	if mitigated.Shortfall != 0 {
		t.Fatalf("stocked reserve delivery reports shortfall %d, want 0", mitigated.Shortfall)
	}
}

// TestDutyErosion (AC-4): holding the tax instrument's rate fixed, raising the
// era EV share strictly decreases posted fuel-duty revenue — the erosion is a
// consequence of the shrinking ICE taxable base, not a rate cut.
func TestDutyErosionHoldsRateConstant(t *testing.T) {
	f := newTestFuel(t)
	ta := newTestTax(t)
	if err := f.SetTax(ta); err != nil {
		t.Fatalf("SetTax: %v", err)
	}

	prev := int64(-1)
	for i, era := range f.Eras() {
		rev, err := f.PostFuelDuty(era)
		if err != nil {
			t.Fatalf("PostFuelDuty(%q): %v", era, err)
		}
		if rev <= 0 {
			t.Fatalf("era %q: duty revenue %d, want > 0", era, rev)
		}
		if i > 0 && int64(rev) >= prev {
			t.Fatalf("era %q: duty revenue %d did not strictly decrease below %d (rate was held constant)", era, rev, prev)
		}
		prev = int64(rev)
	}
}

// TestFuelDutyRevenue (AC-4/GR#7): the duty revenue is a real queryable flow
// posted through engine.tax, and an unwired tax fails closed rather than
// dropping the revenue.
func TestFuelDutyRevenueRequiresTaxWired(t *testing.T) {
	f := newTestFuel(t)
	if _, err := f.PostFuelDuty("early"); err == nil {
		t.Fatal("PostFuelDuty without SetTax silently succeeded")
	} else {
		assertCode(t, err, ErrTaxNotWired)
	}
}

// TestEveningPeakStack (AC-5): EV charging load is time-of-day-aware (an
// evening-peak concentration, not a flat daily average), and stacking it with
// a separately-modelled electric-heating peak through the same UtilityAPI
// produces a combined peak strictly greater than either load alone.
func TestEveningPeakStack(t *testing.T) {
	f := newTestFuel(t)

	evening, err := f.ChargingLoad(19) // 19:00 — the data's peak hour
	if err != nil {
		t.Fatalf("ChargingLoad(19): %v", err)
	}
	overnight, err := f.ChargingLoad(3) // 03:00 — off-peak
	if err != nil {
		t.Fatalf("ChargingLoad(3): %v", err)
	}
	if evening.Power <= overnight.Power {
		t.Fatalf("charging load is not evening-peak concentrated: evening %v <= overnight %v", evening.Power, overnight.Power)
	}

	// The profile must not be a flat daily average (which would mask the
	// evening-peak mechanic AC-5 rejects).
	profile := f.ChargingLoadProfile()
	minW, maxW := profile[0], profile[0]
	for _, w := range profile {
		if w < minW {
			minW = w
		}
		if w > maxW {
			maxW = w
		}
	}
	if maxW == minW {
		t.Fatal("charging profile is flat (uniform weights) — not an evening-peak concentration")
	}

	// Electric-heating peak: all-electric (no gas network) winter residential
	// demand, via the SAME UtilityAPI — §17/§49's shared grid.
	u := newTestConsumption(t)
	heating, err := u.ResidentialDemand(1000, consumption.DemandOptions{MonthIndex: 0, GasNetworkPresent: false})
	if err != nil {
		t.Fatalf("ResidentialDemand: %v", err)
	}
	if heating.Power <= 0 {
		t.Fatalf("electric-heating peak power = %v, want > 0", heating.Power)
	}

	combined := evening.Power + heating.Power
	if combined <= evening.Power {
		t.Fatalf("combined peak %v <= EV charging alone %v", combined, evening.Power)
	}
	if combined <= heating.Power {
		t.Fatalf("combined peak %v <= electric heating alone %v", combined, heating.Power)
	}
}

// TestGridCoupling (AC-5/AC-9): the charging load accessor is a pure function
// of the hour index — an out-of-range hour is rejected, never clamped to a
// silently-plausible bucket.
func TestGridCouplingInvalidHourRejected(t *testing.T) {
	f := newTestFuel(t)
	if _, err := f.ChargingLoad(24); err == nil {
		t.Fatal("ChargingLoad(24) succeeded without error")
	} else {
		assertCode(t, err, ErrInvalidHour)
	}
	if _, err := f.ChargingLoad(-1); err == nil {
		t.Fatal("ChargingLoad(-1) succeeded without error")
	} else {
		assertCode(t, err, ErrInvalidHour)
	}
}

// TestForecourtCoverage (AC-6): holding the forecourt count fixed, a growing
// served population strictly degrades the coverage-adequacy figure — "a
// growing city needs forecourts like it needs substations".
func TestForecourtCoverageDegradesAsPopulationGrows(t *testing.T) {
	f := newTestFuel(t)
	const forecourts = 35

	small, err := f.ForecourtCoverageAdequacy(forecourts, 100_000)
	if err != nil {
		t.Fatalf("ForecourtCoverageAdequacy(small): %v", err)
	}
	large, err := f.ForecourtCoverageAdequacy(forecourts, 400_000)
	if err != nil {
		t.Fatalf("ForecourtCoverageAdequacy(large): %v", err)
	}
	if large >= small {
		t.Fatalf("coverage adequacy did not degrade as population grew: small=%v large=%v", small, large)
	}
}

// TestMalformedFuelData (AC-8): a malformed fleet-era fixture (missing
// EV-share) and a malformed charging-load fixture (negative weight) each
// produce the registry-sourced load-time error — never a silent default-to-
// zero that would mask a data-authoring bug.
func TestMalformedFuelData(t *testing.T) {
	valid := func() string {
		return `{
			"version": 1,
			"eras": [
				{"era":"early","carEVShare":0.02,"vanEVShare":0.01,"truckEVShare":0.0},
				{"era":"mid","carEVShare":0.3,"vanEVShare":0.18,"truckEVShare":0.06}
			],
			"fuelDemand":{"carLitresPerTick":200000,"vanLitresPerTick":80000,"truckLitresPerTick":120000,"logisticsFleetLitresPerTick":40000},
			"chargingProfile":{"baseKWhPerTick":120000,"hourlyWeight":[0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1]},
			"strategicReserve":{"daysOfCover":90},
			"duty":{"ratePencePerLitre":52.95,"taxInstrument":"import-duties"},
			"forecourt":{"targetForecourtsPerThousandPopulation":0.35},
			"tanker":{"portThroughputLitresPerTick":440000}
		}`
	}

	cases := []struct {
		name string
		body string
	}{
		{
			name: "missing EV-share figure",
			body: `{
				"version": 1,
				"eras": [
					{"era":"early","carEVShare":0.02,"vanEVShare":0.01,"truckEVShare":0.0},
					{"era":"mid","carEVShare":0.3,"vanEVShare":0.18}
				],
				"fuelDemand":{"carLitresPerTick":200000,"vanLitresPerTick":80000,"truckLitresPerTick":120000,"logisticsFleetLitresPerTick":40000},
				"chargingProfile":{"baseKWhPerTick":120000,"hourlyWeight":[0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1]},
				"strategicReserve":{"daysOfCover":90},
				"duty":{"ratePencePerLitre":52.95,"taxInstrument":"import-duties"},
				"forecourt":{"targetForecourtsPerThousandPopulation":0.35},
				"tanker":{"portThroughputLitresPerTick":440000}
			}`,
		},
		{
			name: "negative charging-load value",
			body: `{
				"version": 1,
				"eras": [
					{"era":"early","carEVShare":0.02,"vanEVShare":0.01,"truckEVShare":0.0}
				],
				"fuelDemand":{"carLitresPerTick":200000,"vanLitresPerTick":80000,"truckLitresPerTick":120000,"logisticsFleetLitresPerTick":40000},
				"chargingProfile":{"baseKWhPerTick":120000,"hourlyWeight":[-0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1]},
				"strategicReserve":{"daysOfCover":90},
				"duty":{"ratePencePerLitre":52.95,"taxInstrument":"import-duties"},
				"forecourt":{"targetForecourtsPerThousandPopulation":0.35},
				"tanker":{"portThroughputLitresPerTick":440000}
			}`,
		},
	}

	// First prove the valid fixture actually loads (so a rejection is not just
	// "fixture writer broken").
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileFuel), []byte(valid()), 0o644); err != nil {
		t.Fatalf("write valid fixture: %v", err)
	}
	if _, err := Load(dir, "test-valid"); err != nil {
		t.Fatalf("valid fixture failed to load: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, fileFuel), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := Load(dir, "test-malformed"); err == nil {
				t.Fatal("malformed fixture loaded without error")
			} else {
				assertCode(t, err, ErrFuelDataInvalid)
			}
		})
	}
}

// TestFuelDataValidateRejectsNonFinite is the BUG-297 regression suite: every
// non-finite-unsafe float64 field in data/fuel.json's schema must be rejected
// as a NaN/±Inf value by Validate — never silently pass a raw <0/>0 comparison
// (which is false for NaN) and let a non-finite figure propagate into the
// fleet/duty/availability arithmetic (GR#16). Constructed directly (not via
// Load, whose json.Unmarshal already rejects non-finite literals) so the
// validation layer itself is what is exercised.
func TestFuelDataValidateRejectsNonFinite(t *testing.T) {
	const validJSON = `{
		"version": 1,
		"eras": [
			{"era":"early","carEVShare":0.02,"vanEVShare":0.01,"truckEVShare":0.0}
		],
		"fuelDemand":{"carLitresPerTick":200000,"vanLitresPerTick":80000,"truckLitresPerTick":120000,"logisticsFleetLitresPerTick":40000},
		"chargingProfile":{"baseKWhPerTick":120000,"hourlyWeight":[0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1]},
		"strategicReserve":{"daysOfCover":90},
		"duty":{"ratePencePerLitre":52.95,"taxInstrument":"import-duties"},
		"forecourt":{"targetForecourtsPerThousandPopulation":0.35},
		"tanker":{"portThroughputLitresPerTick":440000}
	}`

	base := func(t *testing.T) fuelData {
		t.Helper()
		var fd fuelData
		if err := json.Unmarshal([]byte(validJSON), &fd); err != nil {
			t.Fatalf("unmarshal valid fixture: %v", err)
		}
		return fd
	}

	cases := []struct {
		name   string
		mutate func(*fuelData)
	}{
		{"NaN carEVShare", func(fd *fuelData) { *fd.Eras[0].CarEVShare = math.NaN() }},
		{"+Inf vanEVShare", func(fd *fuelData) { *fd.Eras[0].VanEVShare = math.Inf(1) }},
		{"-Inf truckEVShare", func(fd *fuelData) { *fd.Eras[0].TruckEVShare = math.Inf(-1) }},
		{"NaN carLitresPerTick", func(fd *fuelData) { fd.FuelDemand.CarLitresPerTick = math.NaN() }},
		{"+Inf vanLitresPerTick", func(fd *fuelData) { fd.FuelDemand.VanLitresPerTick = math.Inf(1) }},
		{"NaN truckLitresPerTick", func(fd *fuelData) { fd.FuelDemand.TruckLitresPerTick = math.NaN() }},
		{"-Inf logisticsFleetLitresPerTick", func(fd *fuelData) { fd.FuelDemand.LogisticsFleetLitresPerTick = math.Inf(-1) }},
		{"NaN hourlyWeight", func(fd *fuelData) { fd.ChargingProfile.HourlyWeight[0] = math.NaN() }},
		{"+Inf baseKWhPerTick", func(fd *fuelData) { fd.ChargingProfile.BaseKWhPerTick = math.Inf(1) }},
		{"NaN daysOfCover", func(fd *fuelData) { fd.StrategicReserve.DaysOfCover = math.NaN() }},
		{"-Inf ratePencePerLitre", func(fd *fuelData) { fd.Duty.RatePencePerLitre = math.Inf(-1) }},
		{"NaN forecourtTarget", func(fd *fuelData) { fd.Forecourt.TargetForecourtsPerThousandPopulation = math.NaN() }},
		{"+Inf tankerThroughput", func(fd *fuelData) { fd.Tanker.PortThroughputLitresPerTick = math.Inf(1) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := base(t)
			tc.mutate(&fd)
			if err := fd.Validate(); err == nil {
				t.Fatalf("non-finite value accepted by Validate (BUG-297)")
			}
		})
	}
}

// TestReplenishmentCommoditiesMatchMarket holds the four untyped commodity
// constants in lockstep with engine.market's own constants (weakness pattern
// #2, mirroring engine.comms' parcelCommodity drift test).
func TestReplenishmentCommoditiesMatchMarket(t *testing.T) {
	if commodityFoodStaples != string(market.FoodStaples) {
		t.Errorf("commodityFoodStaples=%q but market.FoodStaples=%q", commodityFoodStaples, market.FoodStaples)
	}
	if commodityFoodFresh != string(market.FoodFresh) {
		t.Errorf("commodityFoodFresh=%q but market.FoodFresh=%q", commodityFoodFresh, market.FoodFresh)
	}
	if commodityConstructionMaterials != string(market.ConstructionMaterials) {
		t.Errorf("commodityConstructionMaterials=%q but market.ConstructionMaterials=%q", commodityConstructionMaterials, market.ConstructionMaterials)
	}
	if commodityConsumerGoods != string(market.ConsumerGoods) {
		t.Errorf("commodityConsumerGoods=%q but market.ConsumerGoods=%q", commodityConsumerGoods, market.ConsumerGoods)
	}
}

// TestUnknownCommodityRejected: ReplenishmentDelivery on a commodity outside
// the closed fuel-transported set is rejected, never silently ungated.
func TestUnknownCommodityRejected(t *testing.T) {
	f := newTestFuel(t)
	l := newTestLogistics(t)
	if err := f.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	if _, err := f.ReplenishmentDelivery("early", "north", ReplenishmentCommodity("waste"), 10); err == nil {
		t.Fatal("unknown replenishment commodity succeeded without error")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
}

// apiCopy takes a same-package value copy of *FuelAPI via an unsafe byte-copy
// (mirrors engine.tax's apiCopy / engine.world's w2Copy convention): a plain
// `cp := *f` is legal Go producing the identical attack shape, but go vet's
// copylocks check would flag the literal assignment and fail this package's
// own `go vet` gate. The byte-copy reaches the same struct-value copy through
// a route copylocks does not statically recognise.
func apiCopy(f *FuelAPI) *FuelAPI {
	c := new(FuelAPI)
	*(*[unsafe.Sizeof(FuelAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(FuelAPI{})]byte)(unsafe.Pointer(f))
	return c
}

// TestCopiedValueRejected: a struct-copied FuelAPI is rejected (SEC-020).
func TestCopiedValueRejected(t *testing.T) {
	f := newTestFuel(t)
	cp := apiCopy(f)
	if _, err := cp.FleetComposition("early"); err == nil {
		t.Fatal("FleetComposition on a copied FuelAPI succeeded")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
}

// TestChargingLoadProfileCopiedValueRejected (SEC-020): ChargingLoadProfile
// carries no error return, so a struct-copied *FuelAPI must fail by returning
// the all-zero profile rather than a silently "valid" non-zero load. A copied
// value's chargingWeight is byte-copied along with the struct, so without the
// checkNotCopied guard this would return the real weights and corrupt AC-5's
// charging-load path.
func TestChargingLoadProfileCopiedValueRejected(t *testing.T) {
	f := newTestFuel(t)
	cp := apiCopy(f)
	if profile := cp.ChargingLoadProfile(); profile != [24]float64{} {
		t.Fatalf("ChargingLoadProfile on a copied FuelAPI returned %v, want the all-zero profile", profile)
	}
}
