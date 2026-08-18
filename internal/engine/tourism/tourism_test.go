package tourism_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tourism"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const testCorrelationID = "test-tourism"

// --- dependency fakes (narrow seams, GR#20) ---

type fakeAttract struct{ rep float64 }

func (f *fakeAttract) Reputation() float64 { return f.rep }

type fakeLeisure struct {
	mix [leisure.NumCategories]float64
}

func (f *fakeLeisure) VenueMix(district uint16, correlationID string) ([leisure.NumCategories]float64, error) {
	return f.mix, nil
}

type fakeSeason struct{ beach float64 }

func (f *fakeSeason) LeisureMix(monthIndex int64) (season.LeisureWeights, error) {
	return season.LeisureWeights{Beach: f.beach, Indoor: 0.1}, nil
}

type fakeNews struct {
	mu     sync.Mutex
	events []news.Event
}

func (f *fakeNews) Ingest(ev news.Event) (news.Story, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return news.Story{}, nil
}

// --- helpers ---

func testConfig() tourism.Config {
	return tourism.Config{
		AccessTierReach:          [3]float64{1, 2, 4},
		ReputationScale:          100,
		ReputationLagMonths:      1,
		StayingVisitorStayMonths: 1,
		StayingVisitorRate:       1,
		DayTripRate:              2,
		PortfolioWeights:         [5]float64{1, 1, 1, 1, 1},
		Accommodation:            [4]int64{0, 0, 0, 0},
		Load: tourism.LoadConfig{
			DayTripperTransport: 1.0,
			DayTripperWaste:     0.3,
			DayTripperPolicing:  0.2,
			StayingTransport:    0.5,
			StayingWaste:        1.0,
			StayingPolicing:     1.0,
		},
		Spend: tourism.SpendConfig{
			DayTripHours:               8,
			DayTripMicroPounds:         5000,
			StayingPerNightMicroPounds: 20000,
		},
	}
}

// newTestAPI builds a TourismAPI with all dependencies wired to benign fakes
// (zero venues, constant summer-season 0.8, neutral reputation).
func newTestAPI(t *testing.T) (*tourism.TourismAPI, *fakeAttract) {
	t.Helper()
	api, err := tourism.New(testConfig(), 42, testCorrelationID)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attract := &fakeAttract{rep: 0}
	if err := api.SetAttract(attract); err != nil {
		t.Fatalf("SetAttract: %v", err)
	}
	if err := api.SetLeisure(&fakeLeisure{}); err != nil {
		t.Fatalf("SetLeisure: %v", err)
	}
	if err := api.SetSeason(&fakeSeason{beach: 0.8}); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	return api, attract
}

func realSeason(t *testing.T) *season.SeasonAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := season.Load(dir, testCorrelationID)
	if err != nil {
		t.Fatalf("season.Load: %v", err)
	}
	return api
}

func registryCode(t *testing.T, err error) string {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected a registry-sourced *errs.E, got %T: %v", err, err)
	}
	return e.Code
}

// assertConserved asserts the AC-13a visitor-conservation invariant:
// admitted == departed + present for staying visitors, and day-tripper
// admissions equal day-tripper departures.
func assertConserved(t *testing.T, api *tourism.TourismAPI) {
	t.Helper()
	if got := api.StayingAdmitted(); got != api.StayingDeparted()+api.StayingPresent() {
		t.Fatalf("staying-visitor conservation violation: admitted=%d departed=%d present=%d",
			api.StayingAdmitted(), api.StayingDeparted(), api.StayingPresent())
	}
	if got := api.DayTripperAdmitted(); got != api.DayTripperDeparted() {
		t.Fatalf("day-tripper conservation violation: admitted=%d departed=%d",
			api.DayTripperAdmitted(), api.DayTripperDeparted())
	}
}

func almostEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

// --- AC-2: decomposed portfolio, per-term isolation ---

func TestPortfolioIsolation(t *testing.T) {
	api, _ := newTestAPI(t)
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermLandmarks, Score: 25}); err != nil {
		t.Fatalf("AddAttraction(landmarks): %v", err)
	}
	beforeBeach := api.BeachPromenadePier()
	beforeEvents := api.EventsTerm()
	beforeCountry := api.CountrysideBDI()
	beforeLandmarks := api.LandmarksHeritage()
	beforePortfolio, err := api.PortfolioScore()
	if err != nil {
		t.Fatalf("PortfolioScore: %v", err)
	}

	if err := api.AddAttraction(tourism.Attraction{ID: 2, Term: tourism.TermBeach, Score: 40}); err != nil {
		t.Fatalf("AddAttraction(beach): %v", err)
	}

	if after := api.BeachPromenadePier(); !almostEqual(after, beforeBeach+40) {
		t.Errorf("beach term = %v, want %v", after, beforeBeach+40)
	}
	if after := api.EventsTerm(); after != beforeEvents {
		t.Errorf("events term changed to %v, want %v", after, beforeEvents)
	}
	if after := api.CountrysideBDI(); after != beforeCountry {
		t.Errorf("countryside term changed to %v, want %v", after, beforeCountry)
	}
	if after := api.LandmarksHeritage(); after != beforeLandmarks {
		t.Errorf("landmarks term changed to %v, want %v", after, beforeLandmarks)
	}
	if after, err := api.PortfolioScore(); err != nil || !almostEqual(after, beforePortfolio+40) {
		t.Errorf("composite portfolio = %v (err %v), want %v", after, err, beforePortfolio+40)
	}
}

// --- AC-3: reputation multiplier reuses engine.attract ---

func TestReputationMultiplier(t *testing.T) {
	cfg := testConfig()
	cfg.ReputationLagMonths = 1
	api, err := tourism.New(cfg, 42, testCorrelationID)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attract := &fakeAttract{rep: 0}
	if err := api.SetAttract(attract); err != nil {
		t.Fatalf("SetAttract: %v", err)
	}
	if err := api.SetLeisure(&fakeLeisure{}); err != nil {
		t.Fatalf("SetLeisure: %v", err)
	}
	if err := api.SetSeason(&fakeSeason{beach: 0.8}); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", i, err)
		}
	}
	low, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}
	beachBefore := api.BeachPromenadePier()

	attract.rep = 50 // raise reputation
	for i := 0; i < 2; i++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	high, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}
	if !(high > low) {
		t.Errorf("draw score did not rise with reputation: low=%v high=%v", low, high)
	}
	if after := api.BeachPromenadePier(); after != beachBefore {
		t.Errorf("portfolio term changed with reputation: %v -> %v", beachBefore, after)
	}
}

// --- AC-4: the seasonal multiplier is read live from engine.season ---

func TestAugustMultiplierMovesWithData(t *testing.T) {
	dir, err := data.ResolveDataDir(testCorrelationID)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "seasonal.json"))
	if err != nil {
		t.Fatalf("read seasonal.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal seasonal.json: %v", err)
	}
	curves := doc["curves"].(map[string]any)
	beach := curves["leisureBeachWeight"].(map[string]any)
	multipliers := beach["multipliers"].([]any)
	originalAugust := multipliers[7].(float64) // August, 0=Jan

	// Mutate the data: move the August beach weight.
	multipliers[7] = 1.5

	tmp := t.TempDir()
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated seasonal.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "seasonal.json"), out, 0o644); err != nil {
		t.Fatalf("write mutated seasonal.json: %v", err)
	}
	mutated, err := season.Load(tmp, testCorrelationID)
	if err != nil {
		t.Fatalf("season.Load(mutated): %v", err)
	}
	mutatedAugust, err := mutated.LeisureMix(7)
	if err != nil {
		t.Fatalf("LeisureMix(7): %v", err)
	}
	if mutatedAugust.Beach != 1.5 {
		t.Fatalf("mutated August beach weight = %v, want 1.5", mutatedAugust.Beach)
	}
	if mutatedAugust.Beach == originalAugust {
		t.Fatalf("August beach weight did not move: still %v", originalAugust)
	}

	// Wire the mutated season into tourism and assert the draw multiplier
	// reflects the data change (never a hardcoded ×3).
	api, _ := newTestAPI(t)
	if err := api.SetSeason(mutated); err != nil {
		t.Fatalf("SetSeason(mutated): %v", err)
	}
	proj, err := api.ProjectDraw(7)
	if err != nil {
		t.Fatalf("ProjectDraw(7): %v", err)
	}
	if proj.SeasonalMultiplier != 1.5 {
		t.Errorf("tourism August seasonal multiplier = %v, want 1.5", proj.SeasonalMultiplier)
	}
}

// --- AC-5: day-tripper vs staying-visitor structural split ---

func TestDayTripperVsStaying(t *testing.T) {
	dt := reflect.TypeOf(tourism.DayTripper{})
	if _, ok := dt.FieldByName("Nights"); ok {
		t.Error("DayTripper carries an accommodation-nights field; a day-tripper consumes no accommodation capacity")
	}
	if _, ok := dt.FieldByName("Accommodation"); ok {
		t.Error("DayTripper carries an accommodation field; a day-tripper consumes no accommodation capacity")
	}
	sv := reflect.TypeOf(tourism.StayingVisitor{})
	if _, ok := sv.FieldByName("Nights"); !ok {
		t.Error("StayingVisitor has no accommodation-nights field; a staying visitor is accommodation-bound")
	}

	// Behavioral: with zero accommodation capacity, day-trippers still arrive
	// while no staying visitor is realised.
	api, _ := newTestAPI(t)
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.AdvanceMonth(); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	if api.DayTrippers().Count <= 0 {
		t.Errorf("day-trippers should be non-zero, got %d", api.DayTrippers().Count)
	}
	if api.StayingPresent() != 0 {
		t.Errorf("staying visitors with zero capacity = %d, want 0", api.StayingPresent())
	}
}

// --- AC-6: accommodation-stock hard cap ---

func TestAccommodationCapEnforced(t *testing.T) {
	api, _ := newTestAPI(t)
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 1000}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.SetAccommodationCapacity(tourism.AccommodationHotel, 10); err != nil {
		t.Fatalf("SetAccommodationCapacity: %v", err)
	}
	sawCap := false
	for i := 0; i < 5; i++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", i, err)
		}
		if got := api.StayingPresent(); got > 10 {
			t.Fatalf("month %d: realised staying visitors %d exceed capacity 10", i, got)
		}
		if api.StayingPresent() == 10 {
			sawCap = true
		}
	}
	if !sawCap {
		t.Error("the capacity cap never bound; the scenario should have forced a large draw against a small capacity")
	}
}

// --- AC-10: reputation shock with a lag ---

func TestReputationLag(t *testing.T) {
	cfg := testConfig()
	cfg.ReputationLagMonths = 2
	api, err := tourism.New(cfg, 42, testCorrelationID)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attract := &fakeAttract{rep: 0}
	if err := api.SetAttract(attract); err != nil {
		t.Fatalf("SetAttract: %v", err)
	}
	if err := api.SetLeisure(&fakeLeisure{}); err != nil {
		t.Fatalf("SetLeisure: %v", err)
	}
	if err := api.SetSeason(&fakeSeason{beach: 0.8}); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}

	// Fill the history with a high reputation.
	for i := 0; i < 3; i++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	high, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}

	// Drop reputation. The first advance records the drop at month M but the
	// draw for month M still uses the lagged (pre-drop) reputation.
	attract.rep = -50
	if err := api.AdvanceMonth(); err != nil {
		t.Fatalf("AdvanceMonth(M): %v", err)
	}
	atDrop, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}
	if !almostEqual(atDrop, high) {
		t.Errorf("draw score at the drop month = %v, want %v (the reduction must not be visible yet)", atDrop, high)
	}

	// Advance through the lag window: the reduction becomes visible.
	for i := 0; i < 2; i++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	afterLag, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}
	if !(afterLag < atDrop) {
		t.Errorf("draw score after the lag = %v, want < %v (the reduction should now be visible)", afterLag, atDrop)
	}
}

// --- AC-11: volume-proportional visitor load ---

func TestVisitorLoad(t *testing.T) {
	makeAPI := func(score float64) (*tourism.TourismAPI, error) {
		api, _ := newTestAPI(t)
		if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: score}); err != nil {
			return nil, err
		}
		return api, nil
	}
	small, err := makeAPI(50)
	if err != nil {
		t.Fatalf("makeAPI(small): %v", err)
	}
	large, err := makeAPI(100)
	if err != nil {
		t.Fatalf("makeAPI(large): %v", err)
	}
	if err := small.AdvanceMonth(); err != nil {
		t.Fatalf("small.AdvanceMonth: %v", err)
	}
	if err := large.AdvanceMonth(); err != nil {
		t.Fatalf("large.AdvanceMonth: %v", err)
	}
	if large.VisitorLoad().Transport <= small.VisitorLoad().Transport {
		t.Errorf("transport load did not rise with volume: small=%v large=%v",
			small.VisitorLoad().Transport, large.VisitorLoad().Transport)
	}
}

// --- AC-12: future-month projection ---

func TestFutureMonthProjection(t *testing.T) {
	api, _ := newTestAPI(t)
	if err := api.SetSeason(realSeason(t)); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	august, err := api.ProjectDraw(7)
	if err != nil {
		t.Fatalf("ProjectDraw(August): %v", err)
	}
	january, err := api.ProjectDraw(0)
	if err != nil {
		t.Fatalf("ProjectDraw(January): %v", err)
	}
	if august.Month != 7 {
		t.Errorf("August projection month = %d, want 7", august.Month)
	}
	if august.SeasonalMultiplier <= january.SeasonalMultiplier {
		t.Errorf("August multiplier %v not > January %v", august.SeasonalMultiplier, january.SeasonalMultiplier)
	}
	if !(august.DrawScore > january.DrawScore) {
		t.Errorf("August draw %v not > January draw %v", august.DrawScore, january.DrawScore)
	}
}

// --- AC-13: the August stress scenario ---

func TestAugustStress(t *testing.T) {
	api, _ := newTestAPI(t)
	if err := api.SetSeason(realSeason(t)); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	// A single beach attraction drives portfolio sum = 100; with stayingRate
	// 1 the July/August draw is 80 (season 0.8) against capacity 70, so the
	// waitlist grows through the August peak and drains in September (0.5).
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.SetAccommodationCapacity(tourism.AccommodationHotel, 70); err != nil {
		t.Fatalf("SetAccommodationCapacity: %v", err)
	}

	var julyQueue, augustQueue, septemberQueue int64
	for m := 0; m < 9; m++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", m, err)
		}
		assertConserved(t, api)
		if got := api.StayingPresent(); got > 70 {
			t.Fatalf("month %d: capacity-cap breach: realised %d > 70", m, got)
		}
		switch m {
		case 6: // July processed
			julyQueue = api.QueueLength()
		case 7: // August processed
			augustQueue = api.QueueLength()
		case 8: // September processed
			septemberQueue = api.QueueLength()
		}
	}

	if augustQueue <= julyQueue {
		t.Fatalf("the August peak did not grow the queue: July=%d August=%d (scenario is not exercising the boss-fight)",
			julyQueue, augustQueue)
	}
	bound := julyQueue + julyQueue/10 // within 10% of the July baseline
	if septemberQueue > bound {
		t.Fatalf("the August backlog did not drain: September queue %d > July baseline %d (+10%% = %d)",
			septemberQueue, julyQueue, bound)
	}
}

// --- AC-15: registry-sourced unknown-ID errors (no silent zero) ---

func TestUnknownAttraction(t *testing.T) {
	api, _ := newTestAPI(t)
	score, err := api.AttractionScore(999)
	if err == nil {
		t.Fatal("AttractionScore(unknown) returned nil error")
	}
	if code := registryCode(t, err); code != "MET-G4401" {
		t.Errorf("AttractionScore error code = %q, want MET-G4401", code)
	}
	if score != 0 {
		t.Errorf("AttractionScore returned a non-zero value %v alongside the error", score)
	}
}

func TestInvalidAccommodation(t *testing.T) {
	api, _ := newTestAPI(t)
	beds, err := api.AccommodationBeds(999)
	if err == nil {
		t.Fatal("AccommodationBeds(unknown) returned nil error")
	}
	if code := registryCode(t, err); code != "MET-G4402" {
		t.Errorf("AccommodationBeds error code = %q, want MET-G4402", code)
	}
	if beds != 0 {
		t.Errorf("AccommodationBeds returned a non-zero value %v alongside the error", beds)
	}
}

// --- AC-16: malformed config → load-time registry error ---

func TestMalformedAccommodation(t *testing.T) {
	tmp := t.TempDir()
	malformed := `{
	  "version": 1,
	  "accessTiers": {"domestic": {"reachMultiplier": 1}, "continental": {"reachMultiplier": 2}, "global": {"reachMultiplier": 4}},
	  "reputationScale": 100,
	  "reputationLagMonths": 1,
	  "stayingVisitorStayMonths": 1,
	  "stayingVisitorRate": 1,
	  "dayTripRate": 2,
	  "portfolioWeights": {"beach": 1, "venues": 1, "events": 1, "landmarks": 1, "countryside": 1},
	  "accommodation": {"hotel": -5, "bnb": 0, "campsite": 0, "holidayLet": 0},
	  "load": {"dayTripperTransport": 1, "dayTripperWaste": 0.3, "dayTripperPolicing": 0.2, "stayingTransport": 0.5, "stayingWaste": 1, "stayingPolicing": 1},
	  "spend": {"dayTripHours": 8, "dayTripMicroPounds": 5000, "stayingPerNightMicroPounds": 20000}
	}`
	if err := os.WriteFile(filepath.Join(tmp, "tourism.json"), []byte(malformed), 0o644); err != nil {
		t.Fatalf("write tourism.json: %v", err)
	}
	api, err := tourism.Load(tmp, testCorrelationID)
	if err == nil {
		t.Fatal("Load(malformed negative beds) returned a non-nil API")
	}
	_ = api
	if code := registryCode(t, err); code != "MET-G4400" {
		t.Errorf("Load error code = %q, want MET-G4400 (the load-time code, distinct from AC-15's)", code)
	}
}

// --- AC-18: determinism ---

func scenarioSnapshot(t *testing.T) string {
	t.Helper()
	api, _ := newTestAPI(t)
	if err := api.SetSeason(realSeason(t)); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.AddAttraction(tourism.Attraction{ID: 2, Term: tourism.TermLandmarks, Score: 40}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.SetAccommodationCapacity(tourism.AccommodationHotel, 70); err != nil {
		t.Fatalf("SetAccommodationCapacity: %v", err)
	}
	for m := 0; m < 12; m++ {
		if err := api.AdvanceMonth(); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	draw, err := api.DrawScore()
	if err != nil {
		t.Fatalf("DrawScore: %v", err)
	}
	return fmt.Sprintf("%d|%d|%d|%d|%d|%d|%d|%.6f",
		api.Month(), api.StayingPresent(), api.StayingAdmitted(), api.StayingDeparted(),
		api.DayTripperAdmitted(), api.QueueLength(), api.TotalAccommodationCapacity(), draw)
}

func TestDeterministic(t *testing.T) {
	a := scenarioSnapshot(t)
	b := scenarioSnapshot(t)
	if a != b {
		t.Fatalf("same seed produced different state:\n%s\n%s", a, b)
	}
}

// --- AC-19: concurrent readers are race-free ---

func TestConcurrentQueries(t *testing.T) {
	api, _ := newTestAPI(t)
	if err := api.AddAttraction(tourism.Attraction{ID: 1, Term: tourism.TermBeach, Score: 100}); err != nil {
		t.Fatalf("AddAttraction: %v", err)
	}
	if err := api.AdvanceMonth(); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = api.DrawScore()
				_, _ = api.PortfolioScore()
				_ = api.DayTrippers()
				_ = api.StayingVisitors()
				_ = api.VisitorLoad()
				_ = api.TotalAccommodationCapacity()
				_, _ = api.ProjectDraw(7)
			}
		}()
	}
	wg.Wait()
}

// --- news edge: supply the event, not the ticker copy ---

func TestReportEventSuppliesEvent(t *testing.T) {
	api, _ := newTestAPI(t)
	sink := &fakeNews{}
	if err := api.SetNews(sink); err != nil {
		t.Fatalf("SetNews: %v", err)
	}
	if _, err := api.ReportEvent(news.Event{ID: "tourism-august-stress", Tick: 7, Text: "seafront queues at capacity"}); err != nil {
		t.Fatalf("ReportEvent: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 || sink.events[0].ID != "tourism-august-stress" {
		t.Fatalf("news sink did not receive the supplied event: %+v", sink.events)
	}
}
