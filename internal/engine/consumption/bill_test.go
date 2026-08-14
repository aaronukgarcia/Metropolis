package consumption

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
)

// TestBilledAmountProduct is AC-20's first half: the billed amount for a
// known delivered quantity equals delivered quantity × engine.market's
// per-unit price exactly, and the result is a money figure (micro-pounds),
// not a relabelled raw quantity.
func TestBilledAmountProduct(t *testing.T) {
	api := realAPI(t)

	// Read the same market prices the module uses, so the expected product
	// is computed from the same source of truth (GR#15).
	mkt, err := market.LoadDefault(testCorrelationID())
	if err != nil {
		t.Fatalf("market.LoadDefault: %v", err)
	}
	waterPrice, err := mkt.Price(market.Water)
	if err != nil {
		t.Fatalf("Price(water): %v", err)
	}
	powerPrice, err := mkt.Price(market.Power)
	if err != nil {
		t.Fatalf("Price(power): %v", err)
	}
	gasPrice, err := mkt.Price(market.Gas)
	if err != nil {
		t.Fatalf("Price(gas): %v", err)
	}

	delivered := DeliveredByCommodity{Water: 100, Power: 10, Gas: 20}
	got, err := api.BilledAmount(delivered)
	if err != nil {
		t.Fatalf("BilledAmount: %v", err)
	}

	if got.WaterMicropounds != delivered.Water*float64(waterPrice) {
		t.Errorf("water bill = %v, want %v × %v = %v",
			got.WaterMicropounds, delivered.Water, waterPrice, delivered.Water*float64(waterPrice))
	}
	if got.PowerMicropounds != delivered.Power*float64(powerPrice) {
		t.Errorf("power bill = %v, want %v × %v = %v",
			got.PowerMicropounds, delivered.Power, powerPrice, delivered.Power*float64(powerPrice))
	}
	if got.GasMicropounds != delivered.Gas*float64(gasPrice) {
		t.Errorf("gas bill = %v, want %v × %v = %v",
			got.GasMicropounds, delivered.Gas, gasPrice, delivered.Gas*float64(gasPrice))
	}
}

// TestBilledAmountRisesWithPrice is AC-20's false-pass guard: raising the
// Market price (with delivered quantity held fixed) raises the billed
// amount — the figure is genuinely price-scaled, not a quantity relabelled
// as a "bill".
func TestBilledAmountRisesWithPrice(t *testing.T) {
	delivered := DeliveredByCommodity{Water: 100}

	lowAPI := &UtilityAPI{market: marketAPIWithWaterPrice(t, 2000), correlationID: testCorrelationID()}
	highAPI := &UtilityAPI{market: marketAPIWithWaterPrice(t, 4000), correlationID: testCorrelationID()}

	low, err := lowAPI.BilledAmount(delivered)
	if err != nil {
		t.Fatalf("BilledAmount(low price): %v", err)
	}
	high, err := highAPI.BilledAmount(delivered)
	if err != nil {
		t.Fatalf("BilledAmount(high price): %v", err)
	}

	if low.WaterMicropounds != 100*2000 {
		t.Errorf("low-price water bill = %v, want %v", low.WaterMicropounds, 100*2000)
	}
	if high.WaterMicropounds != 100*4000 {
		t.Errorf("high-price water bill = %v, want %v", high.WaterMicropounds, 100*4000)
	}
	if low.WaterMicropounds >= high.WaterMicropounds {
		t.Errorf("raising the price did not raise the bill: %v >= %v (AC-20 false-pass)",
			low.WaterMicropounds, high.WaterMicropounds)
	}
}

// marketAPIWithWaterPrice loads a *market.MarketAPI from a temp-dir
// market.json whose water import price is waterPriceMicropounds, with the
// other eight commodities fixed at plausible valid values. This is the
// sanctioned way to vary a price without editing the repository's own
// data/market.json.
func marketAPIWithWaterPrice(t *testing.T, waterPriceMicropounds int64) *market.MarketAPI {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`{
  "version": 1,
  "pricingMode": "static",
  "commodities": {
    "water": {"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": %d, "capacityCeiling": 1000000},
    "power": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 150000, "capacityCeiling": 1000000},
    "gas": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 70000, "capacityCeiling": 1000000},
    "foodStaples": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 400000, "capacityCeiling": 1000000},
    "foodFresh": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 900000, "capacityCeiling": 1000000},
    "fuel": {"supplyMode": "importOnly", "unit": "L", "importPriceMicropounds": 1500000, "capacityCeiling": 1000000},
    "constructionMaterials": {"supplyMode": "hybrid", "unit": "tonne", "importPriceMicropounds": 45000000, "capacityCeiling": 1000000},
    "consumerGoods": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 3000000, "capacityCeiling": 1000000},
    "waste": {"supplyMode": "hybrid", "unit": "kg", "exportPriceMicropounds": 50000, "capacityCeiling": 1000000}
  }
}`, waterPriceMicropounds)
	if err := os.WriteFile(filepath.Join(dir, "market.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write market fixture: %v", err)
	}
	m, err := market.Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("market.Load(fixture): %v", err)
	}
	return m
}
