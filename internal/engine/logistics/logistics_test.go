package logistics

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// copyMarketJSON copies the repository's real data/market.json into dir so
// that Load(dir, ...) (which loads market.json via the registered
// engine.market edge) succeeds against a synthetic logistics.json fixture.
func copyMarketJSON(t *testing.T, dir string) {
	t.Helper()
	dataDir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dataDir, data.FileMarket))
	if err != nil {
		t.Fatalf("read data/market.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, data.FileMarket), b, 0o644); err != nil {
		t.Fatalf("write market.json fixture: %v", err)
	}
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

// fixtureLogisticsJSON builds a schema-valid logistics.json carrying all
// nine §6 commodities at the given throughput and shortfallFactor, with a
// lean/fat buffer-policy pair. Used by fixture tests that need precise,
// deterministic numbers rather than the repo's real balance data.
func fixtureLogisticsJSON(throughput int64, shortfallFactor float64) string {
	names := []string{
		"water", "power", "gas", "foodStaples", "foodFresh", "fuel",
		"constructionMaterials", "consumerGoods", "waste",
	}
	var b strings.Builder
	b.WriteString(`{"version":1,`)
	b.WriteString(`"bufferPolicies":{"lean":{"safetyBuffer":0.1},"fat":{"safetyBuffer":0.5}},`)
	b.WriteString(`"commodities":{`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%s":{"unit":"u","throughput":%d,"shortfallFactor":%g,"shelfLifeTicks":0,"holdingCostMicropoundsPerUnitPerTick":0,"defaultBufferPolicy":"fat"}`,
			n, throughput, shortfallFactor)
	}
	b.WriteString("}}")
	return b.String()
}

// fixtureAPI builds a *LogisticsAPI from a temp dir holding the real
// market.json plus a synthetic logistics.json at the given numbers.
func fixtureAPI(t *testing.T, throughput int64, shortfallFactor float64) *LogisticsAPI {
	t.Helper()
	dir := t.TempDir()
	copyMarketJSON(t, dir)
	writeFixture(t, dir, data.FileLogistics, fixtureLogisticsJSON(throughput, shortfallFactor))
	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	return api
}

// fixtureMarketJSON builds a schema-valid data/market.json carrying all
// nine §6 commodities at the given capacityCeiling, so a test can drive
// MarketAPI.Availability's ceiling to math.MaxInt64 and exercise the
// int64 -> float64 -> int64 round-trip overflow path in Deliverable
// (which bounds local throughput by that ceiling).
func fixtureMarketJSON(ceiling int64) string {
	names := []string{
		"water", "power", "gas", "foodStaples", "foodFresh", "fuel",
		"constructionMaterials", "consumerGoods", "waste",
	}
	var b strings.Builder
	b.WriteString(`{"version":1,"pricingMode":"static","commodities":{`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		if n == "waste" {
			fmt.Fprintf(&b, `"%s":{"supplyMode":"hybrid","unit":"u","exportPriceMicropounds":50000,"capacityCeiling":%d}`, n, ceiling)
		} else {
			fmt.Fprintf(&b, `"%s":{"supplyMode":"hybrid","unit":"u","importPriceMicropounds":1000,"capacityCeiling":%d}`, n, ceiling)
		}
	}
	b.WriteString("}}")
	return b.String()
}

// realAPI loads a *LogisticsAPI against the repository's own
// data/logistics.json + data/market.json, for tests that check the real
// spec-transcribed figures rather than a synthetic fixture.
func realAPI(t *testing.T) *LogisticsAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load real data/logistics.json: %v", err)
	}
	return api
}

func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// --- AC-2: Stock carries capacity/holding-cost/shelf-life ---------------

func TestStockCarriesCapacityHoldingCostShelfLife(t *testing.T) {
	api := realAPI(t)

	// foodFresh is the §6 JIT poster child: short shelf life, high holding
	// cost. constructionMaterials is non-perishable and cheap to hold.
	fresh, err := api.Provision("d1", market.FoodFresh, 1000, 100)
	if err != nil {
		t.Fatalf("Provision foodFresh: %v", err)
	}
	mats, err := api.Provision("d1", market.ConstructionMaterials, 5000, 0)
	if err != nil {
		t.Fatalf("Provision constructionMaterials: %v", err)
	}

	if fresh.Capacity != 1000 {
		t.Errorf("foodFresh Capacity = %d, want 1000", fresh.Capacity)
	}
	if fresh.ShelfLife <= 0 {
		t.Errorf("foodFresh ShelfLife = %d, want > 0 (perishable)", fresh.ShelfLife)
	}
	if fresh.HoldingCost <= 0 {
		t.Errorf("foodFresh HoldingCost = %d, want > 0", fresh.HoldingCost)
	}
	if mats.ShelfLife != 0 {
		t.Errorf("constructionMaterials ShelfLife = %d, want 0 (non-perishable)", mats.ShelfLife)
	}
	if mats.Capacity != 5000 {
		t.Errorf("constructionMaterials Capacity = %d, want 5000", mats.Capacity)
	}
}

// --- AC-2 / AC-9: partial fill + nonzero shortfall (positive case) ------

func TestDrawPartialFillAndShortfall(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100, 40); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	res, err := api.Draw("d1", market.FoodStaples, 60, ConsumerHousehold)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if res.Fulfilled != 40 {
		t.Errorf("Fulfilled = %d, want 40 (capped by available stock)", res.Fulfilled)
	}
	if res.Shortfall != 20 {
		t.Errorf("Shortfall = %d, want 20 (requested - fulfilled)", res.Shortfall)
	}
	after, _ := api.Stock("d1", market.FoodStaples)
	if after.Level != 0 {
		t.Errorf("Level after draw = %d, want 0", after.Level)
	}
}

// --- AC-9: adequate stock produces zero shortfall (negative case) -------

func TestDrawAdequateStockNoShortfall(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100, 100); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	// A handler must NOT fire when there is no shortfall.
	fired := 0
	if err := api.SubscribeShortfalls(func(ShortfallEvent) { fired++ }); err != nil {
		t.Fatalf("SubscribeShortfalls: %v", err)
	}

	res, err := api.Draw("d1", market.FoodStaples, 60, ConsumerHousehold)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if res.Shortfall != 0 {
		t.Errorf("Shortfall = %d, want 0 (adequate stock)", res.Shortfall)
	}
	if res.Fulfilled != 60 {
		t.Errorf("Fulfilled = %d, want 60", res.Fulfilled)
	}
	if fired != 0 {
		t.Errorf("shortfall hook fired %d times, want 0 (no shortfall)", fired)
	}
}

// --- AC-10: shortfall hook fires exactly once with correct data ---------

func TestShortfallHookFiresOnce(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.ConstructionMaterials, 100, 5); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	var mu sync.Mutex
	var events []ShortfallEvent
	if err := api.SubscribeShortfalls(func(e ShortfallEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("SubscribeShortfalls: %v", err)
	}

	res, err := api.Draw("d1", market.ConstructionMaterials, 10, ConsumerConstruction)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if res.Shortfall != 5 {
		t.Fatalf("Shortfall = %d, want 5", res.Shortfall)
	}

	mu.Lock()
	got := append([]ShortfallEvent(nil), events...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("shortfall hook fired %d times, want exactly 1", len(got))
	}
	if got[0].Commodity != market.ConstructionMaterials {
		t.Errorf("event Commodity = %v, want constructionMaterials", got[0].Commodity)
	}
	if got[0].Shortfall != 5 {
		t.Errorf("event Shortfall = %d, want 5", got[0].Shortfall)
	}
	if got[0].ConsumerClass != ConsumerConstruction {
		t.Errorf("event ConsumerClass = %v, want construction", got[0].ConsumerClass)
	}
	if got[0].District != "d1" {
		t.Errorf("event District = %q, want d1", got[0].District)
	}
}

// --- AC-3: fat buffer orders more than lean for identical demand --------

func TestBufferPolicyFatOrdersMoreThanLean(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100, 0); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if err := api.SetBufferPolicy("d1", market.FoodStaples, BufferLean); err != nil {
		t.Fatalf("SetBufferPolicy lean: %v", err)
	}
	lean, err := api.OrderSize("d1", market.FoodStaples, 1000)
	if err != nil {
		t.Fatalf("OrderSize lean: %v", err)
	}

	if err := api.SetBufferPolicy("d1", market.FoodStaples, BufferFat); err != nil {
		t.Fatalf("SetBufferPolicy fat: %v", err)
	}
	fat, err := api.OrderSize("d1", market.FoodStaples, 1000)
	if err != nil {
		t.Fatalf("OrderSize fat: %v", err)
	}

	if fat <= lean {
		t.Errorf("fat order %d is not strictly greater than lean order %d for identical demand", fat, lean)
	}
}

// --- AC-8: construction draws through the same Draw/Stock mechanism -----

func TestConstructionUsesSameDrawMechanism(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("site-1", market.ConstructionMaterials, 100, 30); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	res, err := api.Draw("site-1", market.ConstructionMaterials, 50, ConsumerConstruction)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if res.Fulfilled != 30 || res.Shortfall != 20 {
		t.Errorf("construction Draw = %+v, want fulfilled 30 / shortfall 20 (same mechanism as any consumer)", res)
	}
}

// --- Deliverable: the coarse throughput/shortfall number ----------------

func TestDeliverableCapacityShortfall(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)

	// Request 1500 against a per-tick effective throughput of 1000.
	d, err := api.Deliverable("d1", market.Water, 1500)
	if err != nil {
		t.Fatalf("Deliverable: %v", err)
	}
	if d.Delivered != 1000 {
		t.Errorf("Delivered = %d, want 1000 (bounded by throughput)", d.Delivered)
	}
	if d.Shortfall != 500 {
		t.Errorf("Shortfall = %d, want 500", d.Shortfall)
	}
	if d.Shortfall != d.Requested-d.Delivered {
		t.Errorf("Shortfall %d != requested - delivered %d", d.Shortfall, d.Requested-d.Delivered)
	}

	// Adequate request: no shortfall.
	d2, err := api.Deliverable("d1", market.Water, 500)
	if err != nil {
		t.Fatalf("Deliverable: %v", err)
	}
	if d2.Delivered != 500 || d2.Shortfall != 0 {
		t.Errorf("adequate Deliverable = %+v, want delivered 500 / shortfall 0", d2)
	}
}

func TestDeliverableShortfallFactorReducesThroughput(t *testing.T) {
	api := fixtureAPI(t, 1000, 0.5)

	// Effective throughput = floor(1000 * 0.5) = 500.
	d, err := api.Deliverable("d1", market.Water, 1000)
	if err != nil {
		t.Fatalf("Deliverable: %v", err)
	}
	if d.Throughput != 500 {
		t.Errorf("Throughput = %d, want 500 (shortfall factor applied)", d.Throughput)
	}
	if d.Delivered != 500 || d.Shortfall != 500 {
		t.Errorf("Deliverable = %+v, want delivered 500 / shortfall 500", d)
	}
}

// --- AC-11: throughput is data-driven, never a hardcoded literal --------

func TestDeliverableIsDataDriven(t *testing.T) {
	low := fixtureAPI(t, 1000, 1.0)
	high := fixtureAPI(t, 2000, 1.0)

	lo, err := low.Deliverable("d1", market.Water, 5000)
	if err != nil {
		t.Fatalf("Deliverable low: %v", err)
	}
	hi, err := high.Deliverable("d1", market.Water, 5000)
	if err != nil {
		t.Fatalf("Deliverable high: %v", err)
	}
	if lo.Delivered != 1000 || hi.Delivered != 2000 {
		t.Errorf("Deliverable not data-driven: throughput 1000 -> %d, throughput 2000 -> %d",
			lo.Delivered, hi.Delivered)
	}
}

// --- GR#16 regression: int64 -> float64 -> int64 round-trip overflows ---
//
// Both tests reproduce the Destructive finding on MOD-025: a
// validation-passing input at math.MaxInt64 must never wrap negative.
// Pre-fix, OrderSize(MaxInt64) returned int64(ceil(2^63*1.5)) == MinInt64
// and Deliverable(MaxInt64, MaxInt64) returned delivered == MinInt64 with
// shortfall == -1. Each assertion below is therefore a real gate that
// fails before the num.ClampInt64FromFloat choke point.

func TestOrderSizeMaxInt64Saturates(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100, 0); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := api.SetBufferPolicy("d1", market.FoodStaples, BufferFat); err != nil {
		t.Fatalf("SetBufferPolicy: %v", err)
	}

	order, err := api.OrderSize("d1", market.FoodStaples, math.MaxInt64)
	if err != nil {
		t.Fatalf("OrderSize: %v", err)
	}
	if order < 0 {
		t.Errorf("OrderSize(MaxInt64) wrapped negative: %d", order)
	}
	if order != math.MaxInt64 {
		t.Errorf("OrderSize(MaxInt64) = %d, want saturation to math.MaxInt64", order)
	}
}

func TestDeliverableMaxInt64Saturates(t *testing.T) {
	for _, factor := range []float64{1.0, 0.9, 0.5} {
		dir := t.TempDir()
		writeFixture(t, dir, data.FileMarket, fixtureMarketJSON(math.MaxInt64))
		writeFixture(t, dir, data.FileLogistics, fixtureLogisticsJSON(math.MaxInt64, factor))
		api, err := Load(dir, testCorrelationID())
		if err != nil {
			t.Fatalf("Load (factor %v): %v", factor, err)
		}

		d, err := api.Deliverable("d1", market.Water, math.MaxInt64)
		if err != nil {
			t.Fatalf("Deliverable (factor %v): %v", factor, err)
		}
		if d.Throughput < 0 {
			t.Errorf("factor %v: Throughput wrapped negative: %d", factor, d.Throughput)
		}
		if d.Delivered < 0 || d.Delivered > d.Requested {
			t.Errorf("factor %v: Delivered = %d outside [0, requested=%d]", factor, d.Delivered, d.Requested)
		}
		if d.Shortfall < 0 {
			t.Errorf("factor %v: Shortfall wrapped negative: %d", factor, d.Shortfall)
		}
		if d.Shortfall != d.Requested-d.Delivered {
			t.Errorf("factor %v: Shortfall %d != requested - delivered %d", factor, d.Shortfall, d.Requested-d.Delivered)
		}
	}
}

// --- AC-13: unregistered commodity / district → registry errors ---------

func TestUnknownCommodityRejected(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	unknown := market.CommodityType("gold")

	if _, err := api.Stock("d1", unknown); err == nil {
		t.Fatal("Stock(gold) returned nil error, want ErrUnknownCommodity")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
	if _, err := api.Draw("d1", unknown, 5, ConsumerFirm); err == nil {
		t.Fatal("Draw(gold) returned nil error, want ErrUnknownCommodity")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
	if _, err := api.Deliverable("d1", unknown, 5); err == nil {
		t.Fatal("Deliverable(gold) returned nil error, want ErrUnknownCommodity")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
	// No silently-created zero-value stock entry (GR#7 assertion, BUG-100):
	if _, err := api.Stock("d1", unknown); err == nil {
		t.Error("a zero-value stock entry for an unknown commodity was created")
	}
}

func TestUnknownDistrictRejected(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)

	if _, err := api.Stock("never-provisioned", market.Water); err == nil {
		t.Fatal("Stock(unprovisioned) returned nil error, want ErrUnknownDistrict")
	} else {
		assertCode(t, err, ErrUnknownDistrict)
	}
	if _, err := api.Draw("never-provisioned", market.Water, 5, ConsumerHousehold); err == nil {
		t.Fatal("Draw(unprovisioned) returned nil error, want ErrUnknownDistrict")
	} else {
		assertCode(t, err, ErrUnknownDistrict)
	}
}

func TestInvalidBufferPolicyRejected(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100, 0); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := api.SetBufferPolicy("d1", market.FoodStaples, BufferPolicy("huge")); err == nil {
		t.Fatal("SetBufferPolicy(huge) returned nil error, want ErrInvalidBufferPolicy")
	} else {
		assertCode(t, err, ErrInvalidBufferPolicy)
	}
}

// --- AC-14: malformed / incomplete data → load-time registry errors -----

func TestLoadMalformedLogisticsJSON(t *testing.T) {
	dir := t.TempDir()
	copyMarketJSON(t, dir)
	writeFixture(t, dir, data.FileLogistics, `{ not valid json`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrLogisticsDataInvalid)
}

func TestLoadNegativeThroughputRejected(t *testing.T) {
	dir := t.TempDir()
	copyMarketJSON(t, dir)
	bad := strings.Replace(fixtureLogisticsJSON(1000, 1.0), `"throughput":1000`, `"throughput":-1`, 1)
	writeFixture(t, dir, data.FileLogistics, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrLogisticsDataInvalid)
}

func TestLoadMissingCommodity(t *testing.T) {
	dir := t.TempDir()
	copyMarketJSON(t, dir)
	// Only water present — the other eight are missing (GR#7 assertion:
	// load-time error, no silent default substitution).
	writeFixture(t, dir, data.FileLogistics, `{"version":1,
		"bufferPolicies":{"lean":{"safetyBuffer":0.1},"fat":{"safetyBuffer":0.5}},
		"commodities":{"water":{"unit":"L","throughput":1000,"shortfallFactor":1.0,"shelfLifeTicks":0,"holdingCostMicropoundsPerUnitPerTick":0,"defaultBufferPolicy":"fat"}}}`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMissingCommodity)
}

// --- AC-15: determinism -------------------------------------------------

func TestDeterministicAcrossRuns(t *testing.T) {
	run := func() []int64 {
		api := fixtureAPI(t, 1000, 1.0)
		_, _ = api.Provision("d1", market.ConstructionMaterials, 500, 300)
		_, _ = api.Provision("d1", market.FoodFresh, 100, 40)

		dr1, _ := api.Draw("d1", market.ConstructionMaterials, 350, ConsumerConstruction)
		dr2, _ := api.Draw("d1", market.FoodFresh, 50, ConsumerHousehold)
		dv, _ := api.Deliverable("d1", market.ConstructionMaterials, 2000)
		order, _ := api.OrderSize("d1", market.ConstructionMaterials, 1000)
		st, _ := api.Stock("d1", market.ConstructionMaterials)

		return []int64{
			dr1.Fulfilled, dr1.Shortfall,
			dr2.Fulfilled, dr2.Shortfall,
			dv.Delivered, dv.Shortfall,
			order, st.Level,
		}
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Errorf("run %d diverged: got %v, want %v (non-deterministic)", i, got, first)
		}
	}
}

// --- AC-17: concurrent query + mutation is race-free (go test -race) ----

func TestConcurrentDrawStockRaceFree(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	if _, err := api.Provision("d1", market.FoodStaples, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := api.SubscribeShortfalls(func(ShortfallEvent) {}); err != nil {
		t.Fatalf("SubscribeShortfalls: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = api.Draw("d1", market.FoodStaples, 1, ConsumerHousehold)
				_, _ = api.Stock("d1", market.FoodStaples)
				_, _ = api.Deliverable("d1", market.FoodStaples, 10)
			}
		}()
	}
	wg.Wait()
}

// --- real-data smoke: every registered commodity provisions -------------

func TestLoadDefaultAllCommoditiesProvision(t *testing.T) {
	api := realAPI(t)
	for _, c := range requiredCommodities {
		if _, err := api.Provision("d1", c, 100, 0); err != nil {
			t.Errorf("Provision %s: %v", c, err)
		}
	}
}
