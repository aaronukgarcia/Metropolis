package shopping

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestShopping_AC2_FormatAccessGeography(t *testing.T) {
	api := New()

	// Cell A has a corner shop close by (time 2) but no supermarket (time 40)
	_ = api.RegisterCellAccess(101, 2.0, 30.0, 40.0, 50.0, 0.9, 0.8, 0.7, 0.6)
	// Cell B has a supermarket close by (time 2) but no corner shop (time 40)
	_ = api.RegisterCellAccess(102, 40.0, 30.0, 2.0, 50.0, 0.9, 0.8, 0.7, 0.6)

	// Since we are not running a full simulation, let's verify that the trip weight/access
	// characteristics show corner shop proximity generates different weighting metrics
	scoreA, _ := api.GroceryAccessScore(101)
	scoreB, _ := api.GroceryAccessScore(102)

	if scoreA == scoreB {
		t.Error("access scores should differ based on format access geography")
	}
}

func TestShopping_AC3_OnlineDeliveryDisplacement(t *testing.T) {
	api := New()
	_ = api.RegisterCellAccess(101, 5.0, 10.0, 15.0, 20.0, 0.8, 0.8, 0.8, 0.8)

	// Trips with default 15% online delivery share
	trips15, _ := api.GenerateTrips(101, false)

	// Raise online delivery share to 40% (GR#15 data sourced)
	tempDir, err := os.MkdirTemp("", "shopping-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configData := `{"foodDesertThreshold": 20.0, "onlineDeliveryShare": 0.40, "cornerShopPriceMult": 1.5, "marketHallPriceMult": 1.1, "supermarketPriceMult": 0.9, "retailParkPriceMult": 0.85}`
	_ = os.WriteFile(filepath.Join(tempDir, "shopping.json"), []byte(configData), 0644)

	_ = api.LoadConfig(tempDir)
	trips40, _ := api.GenerateTrips(101, false)

	// Total household trips should fall with higher delivery share ("no trip, van instead")
	if trips40 >= trips15 {
		t.Errorf("expected trips with 40%% share (%d) < trips with 15%% share (%d)", trips40, trips15)
	}
}

func TestShopping_AC4_SaturdayPeakDistinct(t *testing.T) {
	api := New()
	_ = api.RegisterCellAccess(101, 5.0, 10.0, 15.0, 20.0, 0.8, 0.8, 0.8, 0.8)

	commutePeakTrips, _ := api.GenerateTrips(101, false) // weekday
	saturdayPeakTrips, _ := api.GenerateTrips(101, true) // Saturday

	if saturdayPeakTrips <= commutePeakTrips {
		t.Errorf("expected Saturday peak trips (%d) > weekday commute trips (%d)", saturdayPeakTrips, commutePeakTrips)
	}
}

func TestShopping_AC5_ThreeFactorAccessScore(t *testing.T) {
	api := New()

	// Initial Cell Access: Time = 5.0, Freshness = 0.8
	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)
	score0, _ := api.GroceryAccessScore(101)

	// 1. Holding price/freshness fixed, vary Time
	_ = api.RegisterCellAccess(101, 15.0, 15.0, 15.0, 15.0, 0.8, 0.8, 0.8, 0.8)
	scoreTime, _ := api.GroceryAccessScore(101)
	if scoreTime == score0 {
		t.Error("access score should vary with travel time")
	}

	// 2. Holding time/freshness fixed, vary Price (multiplier)
	api.cfg.CornerShopPriceMult = 2.0
	scorePrice, _ := api.GroceryAccessScore(101)
	if scorePrice == scoreTime {
		t.Error("access score should vary with price")
	}

	// 3. Holding time/price fixed, vary Freshness
	_ = api.RegisterCellAccess(101, 15.0, 15.0, 15.0, 15.0, 0.4, 0.4, 0.4, 0.4)
	scoreFresh, _ := api.GroceryAccessScore(101)
	if scoreFresh == scorePrice {
		t.Error("access score should vary with freshness")
	}
}

func TestShopping_AC6_EmergentFoodDeserts(t *testing.T) {
	api := New()

	// High access cell (low times) should NOT be in food desert
	_ = api.RegisterCellAccess(101, 2.0, 2.0, 2.0, 2.0, 0.9, 0.9, 0.9, 0.9)
	desert1, _ := api.FoodDesert(101)
	if desert1 {
		t.Error("expected cell 101 to NOT be in a food desert")
	}

	// Closing nearby options increases travel times (severe high times) -> score drops below threshold
	_ = api.RegisterCellAccess(101, 45.0, 45.0, 45.0, 45.0, 0.4, 0.4, 0.4, 0.4)
	desert2, _ := api.FoodDesert(101)
	if !desert2 {
		t.Error("expected cell 101 to emerge into a food desert naturally")
	}
}

func TestShopping_RealMarketDependency(t *testing.T) {
	api := New()

	// Load real MarketAPI
	marketAPI, err := market.LoadDefault("test-shopping")
	if err != nil {
		t.Fatalf("failed to load market: %v", err)
	}

	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)
	scoreWithoutMarket, _ := api.GroceryAccessScore(101)

	_ = api.SetMarket(marketAPI)
	scoreWithMarket, _ := api.GroceryAccessScore(101)

	if scoreWithoutMarket == scoreWithMarket {
		// Market price > 1.0 causes the score to be lower than scoreWithoutMarket, proving dependency is wired
		t.Error("expected access score to change when market dependency is wired")
	}
}

func TestShopping_AC9_NoCellAccessError(t *testing.T) {
	api := New()
	_, err := api.GroceryAccessScore(999)
	if err == nil {
		t.Error("expected error for unregistered cell ID")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrUnregisteredCell {
		t.Errorf("expected unregistered cell error MET-G4701, got: %v", err)
	}

	// Test negative travel time validation
	err = api.RegisterCellAccess(101, -2.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)
	if err == nil {
		t.Error("expected error for negative travel time")
	}
	if !errors.As(err, &re) || re.Code != ErrInvalidAccessInput {
		t.Errorf("expected invalid input error MET-G4702, got: %v", err)
	}
}

func TestShopping_AC10_OutOfRangeShareError(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "shopping-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	badConfig := `{"onlineDeliveryShare": 1.5}`
	_ = os.WriteFile(filepath.Join(tempDir, "shopping.json"), []byte(badConfig), 0644)

	err = api.LoadConfig(tempDir)
	if err == nil {
		t.Error("expected error for out of range online delivery share")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrOutOfRangeShare {
		t.Errorf("expected out-of-range share error MET-G4703, got: %v", err)
	}
}

func TestShopping_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)
	_ = api2.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)

	s1, _ := api1.GroceryAccessScore(101)
	s2, _ := api2.GroceryAccessScore(101)

	if s1 != s2 {
		t.Errorf("expected deterministic score to be equal, got %f and %f", s1, s2)
	}
}

func TestShopping_AC13_Concurrency(t *testing.T) {
	api := New()
	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = api.GroceryAccessScore(101)
				_, _ = api.GenerateTrips(101, false)
			}
		}()
	}

	wg.Wait()
}

func TestShopping_RealWellbeingCitizensDependency(t *testing.T) {
	api := New()

	// 1. Setup real CitizensAPI
	citAPI, err := citizens.NewCitizensAPI(12345, "test-shopping")
	if err != nil {
		t.Fatalf("failed to create citizens api: %v", err)
	}
	_ = citAPI.SeedColdRecords([]citizens.ColdRecord{
		{ID: 10, BirthMonth: 120, Sex: citizens.SexFemale, Stage: citizens.StageNone, Home: 101},
	}, "test-seed")
	_ = api.SetCitizens(citAPI)

	// 2. Setup real WellbeingAPI
	wellbeingFile, err := wellbeing.LoadWellbeing("../../../data", "test-shopping")
	if err != nil {
		t.Fatalf("failed to load wellbeing config: %v", err)
	}
	wellbeingAPI, err := wellbeing.New(wellbeingFile, 12345, "test-shopping")
	if err != nil {
		t.Fatalf("failed to create wellbeing api: %v", err)
	}
	_ = api.SetWellbeing(wellbeingAPI)

	// Register cell 101 access
	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.9, 0.9, 0.9, 0.9)

	// FreshFoodShare should look up citizen 10's actual home cell (101) instead of fallback (1)
	share, ok, err := api.FreshFoodShare(10, "test-fresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected ok to be true")
	}
	if share != 0.9 {
		t.Errorf("expected fresh food share derived from cell 101 (0.9), got %f", share)
	}
}

func TestShopping_RealTrafficDependency(t *testing.T) {
	api := New()

	// Setup real TrafficAPI
	trafficAPI := traffic.New()
	_ = api.SetTraffic(trafficAPI)

	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.8, 0.8, 0.8, 0.8)

	// Generate trips should push demand into trafficAPI
	_, _ = api.GenerateTrips(101, false)

	// Query commute minutes on trafficAPI to see if demand multiplier changed
	commute1, _, _ := trafficAPI.CommuteMinutes(99, "test-commute")

	// Triggering more trips
	for i := 0; i < 50; i++ {
		_, _ = api.GenerateTrips(101, false)
	}

	commute2, _, _ := trafficAPI.CommuteMinutes(99, "test-commute")
	if commute2 <= commute1 {
		t.Error("expected traffic commute minutes to increase with generated shopping trip demands")
	}
}
