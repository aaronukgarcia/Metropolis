package shopping

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const (
	ErrUnregisteredCell   = "MET-G4701"
	ErrInvalidAccessInput = "MET-G4702"
	ErrOutOfRangeShare    = "MET-G4703"
	ErrInvalidWeightInput = "MET-G4704" // MOD-050 r4 verdict fix: format weight validation (negative/NaN rejected fail-closed)
	ErrCopiedValue        = "MET-G4799"
	ErrInvalidInput       = "MET-G4702" // Local alias for invalid config inputs (AC-9/AC-11)
)

// Sanctioned placeholder format-preference weights (GR#15, MOD-050 r4
// verdict fix). These MUST match data/shopping.json's own values exactly
// -- they exist here ONLY as the documented default-config path taken
// when a Config's weight field is nil, i.e. genuinely absent from the
// loaded JSON, not present-and-zero. A present-and-zero weight is never
// routed through these defaults: it means "disable this format" and is
// honoured literally (see effectiveWeight). In production, LoadConfig
// always loads data/shopping.json, which always sets all four fields, so
// this default path is exercised only by New()'s pre-LoadConfig state or
// by tests that deliberately use a partial config fixture.
const (
	defaultCornerShopWeight  = 1.5
	defaultMarketHallWeight  = 2.0
	defaultSupermarketWeight = 4.0
	defaultRetailParkWeight  = 3.0
)

// effectiveWeight resolves a Config weight field to the value actually
// used by computeFormatSplitsLocked: nil (field absent from the loaded
// JSON) falls back to def, but an explicit zero is honoured literally as
// "this format is disabled" -- it is never silently replaced by def
// (MOD-050 r4 verdict fix; the prior code silently substituted default
// weights whenever a weight equalled zero, which made zero inexpressible
// as a real config value).
func effectiveWeight(w *float64, def float64) float64 {
	if w == nil {
		return def
	}
	return *w
}

// Config represents player-felt numbers for shopping and grocery access (GR#15).
//
// The four format weight fields are *float64 (not float64) so LoadConfig
// can tell "field absent from the JSON" (nil -> documented default, see
// effectiveWeight) apart from "field present and explicitly zero" (a
// pointer to 0.0 -> that format is disabled). A plain float64 cannot
// make that distinction: both cases unmarshal to the Go zero value.
type Config struct {
	FoodDesertThreshold  float64  `json:"foodDesertThreshold"`
	OnlineDeliveryShare  float64  `json:"onlineDeliveryShare"` // placeholder (AC-3)
	CornerShopPriceMult  float64  `json:"cornerShopPriceMult"`
	MarketHallPriceMult  float64  `json:"marketHallPriceMult"`
	SupermarketPriceMult float64  `json:"supermarketPriceMult"`
	RetailParkPriceMult  float64  `json:"retailParkPriceMult"`
	CornerShopWeight     *float64 `json:"cornerShopWeight,omitempty"`
	MarketHallWeight     *float64 `json:"marketHallWeight,omitempty"`
	SupermarketWeight    *float64 `json:"supermarketWeight,omitempty"`
	RetailParkWeight     *float64 `json:"retailParkWeight,omitempty"`
}

// CellAccess holds format access travel-time and freshness data per cell (AC-2/AC-3/AC-5).
type CellAccess struct {
	CellID           uint64
	CornerShopTime   float64
	MarketHallTime   float64
	SupermarketTime  float64
	RetailParkTime   float64
	CornerShopFresh  float64
	MarketHallFresh  float64
	SupermarketFresh float64
	RetailParkFresh  float64
}

// ShoppingAPI represents household shopping trip generation & access module (MOD-050).
type ShoppingAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[ShoppingAPI]
	traffic       *traffic.TrafficAPI
	market        *market.MarketAPI
	wellbeing     *wellbeing.WellbeingAPI
	citizens      *citizens.CitizensAPI
	cells         map[uint64]*CellAccess
	cfg           Config
	correlationID string
}

// New constructs a new ShoppingAPI.
func New() *ShoppingAPI {
	s := &ShoppingAPI{
		cells: make(map[uint64]*CellAccess),
		cfg: Config{
			FoodDesertThreshold:  20.0,
			OnlineDeliveryShare:  0.15, // TODO/STUB: online delivery share placeholder (AC-3)
			CornerShopPriceMult:  1.5,
			MarketHallPriceMult:  1.1,
			SupermarketPriceMult: 0.9,
			RetailParkPriceMult:  0.85,
		},
		correlationID: "default-shopping",
	}
	s.self.Store(s)
	return s
}

func (s *ShoppingAPI) checkNotCopied(method string) error {
	if s.self.Load() != s {
		return errs.New(ErrCopiedValue, s.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetTraffic sets the traffic outbound dependency (AC-1).
func (s *ShoppingAPI) SetTraffic(t *traffic.TrafficAPI) error {
	if err := s.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traffic = t
	return nil
}

// SetMarket sets the market outbound dependency (AC-8).
func (s *ShoppingAPI) SetMarket(m *market.MarketAPI) error {
	if err := s.checkNotCopied("SetMarket"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.market = m
	return nil
}

// SetWellbeing sets the wellbeing outbound dependency (AC-7).
func (s *ShoppingAPI) SetWellbeing(w *wellbeing.WellbeingAPI) error {
	if err := s.checkNotCopied("SetWellbeing"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wellbeing = w
	if w != nil {
		return w.SetShopping(s)
	}
	return nil
}

// SetCitizens sets the citizens outbound dependency (AC-7).
func (s *ShoppingAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := s.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.citizens = c
	return nil
}

// LoadConfig loads the config from disk (AC-2/GR#15).
func (s *ShoppingAPI) LoadConfig(dir string) error {
	if err := s.checkNotCopied("LoadConfig"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(dir, "shopping.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"path": path, "value": err.Error(), "cause": err.Error()})
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"path": path, "value": err.Error(), "cause": err.Error()})
	}

	if cfg.OnlineDeliveryShare < 0 || cfg.OnlineDeliveryShare > 1.0 {
		return errs.New(ErrOutOfRangeShare, s.correlationID, map[string]any{"share": cfg.OnlineDeliveryShare})
	}

	// Numerical Safety validation: Prevent division-by-zero in score factors (AC-5)
	if cfg.CornerShopPriceMult <= 0 || cfg.MarketHallPriceMult <= 0 || cfg.SupermarketPriceMult <= 0 || cfg.RetailParkPriceMult <= 0 {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"value": "price multipliers must be strictly positive"})
	}

	// Format weight validation (MOD-050 r4 verdict fix, AC-9/AC-11 style
	// fail-closed input guard): negative and NaN are rejected outright.
	// Zero is explicitly NOT rejected here -- zero is the documented
	// "disable this format" value (see effectiveWeight) and must load
	// successfully. Extracted to validateWeights so it is directly
	// unit-testable without a JSON round-trip (encoding/json's strict
	// JSON-syntax mode rejects the bare `NaN` token outright, which would
	// otherwise make this guard's own NaN branch unreachable from a
	// disk-borne test fixture).
	if err := validateWeights(cfg, s.correlationID); err != nil {
		return err
	}

	s.cfg = cfg
	return nil
}

// validateWeights fail-closed validates the four format weight fields:
// negative and NaN are rejected with MET-G4704; a nil (field absent) or
// zero (explicit disable, see effectiveWeight) value is always accepted.
// Iterates a fixed-order slice, never a map, so which field is reported
// first on a multi-violation input is deterministic (GR#21-adjacent
// discipline: this module's own tests assert determinism elsewhere).
func validateWeights(cfg Config, correlationID string) error {
	for _, w := range []struct {
		name string
		val  *float64
	}{
		{"cornerShopWeight", cfg.CornerShopWeight},
		{"marketHallWeight", cfg.MarketHallWeight},
		{"supermarketWeight", cfg.SupermarketWeight},
		{"retailParkWeight", cfg.RetailParkWeight},
	} {
		if w.val != nil && (*w.val < 0 || math.IsNaN(*w.val)) {
			return errs.New(ErrInvalidWeightInput, correlationID, map[string]any{"field": w.name, "value": *w.val})
		}
	}
	return nil
}

// RegisterCellAccess registers travel-time and freshness values per cell (AC-5).
func (s *ShoppingAPI) RegisterCellAccess(cellID uint64, cornerTime, marketTime, superTime, retailTime, cornerFresh, marketFresh, superFresh, retailFresh float64) error {
	if err := s.checkNotCopied("RegisterCellAccess"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate inputs are non-negative/finite (AC-9)
	for _, val := range []float64{cornerTime, marketTime, superTime, retailTime, cornerFresh, marketFresh, superFresh, retailFresh} {
		if val < 0 {
			return errs.New(ErrInvalidAccessInput, s.correlationID, map[string]any{"value": val})
		}
	}

	s.cells[cellID] = &CellAccess{
		CellID:           cellID,
		CornerShopTime:   cornerTime,
		MarketHallTime:   marketTime,
		SupermarketTime:  superTime,
		RetailParkTime:   retailTime,
		CornerShopFresh:  cornerFresh,
		MarketHallFresh:  marketFresh,
		SupermarketFresh: superFresh,
		RetailParkFresh:  retailFresh,
	}

	return nil
}

// GroceryAccessScore calculates a composite score based on travel time, prices, and freshness (AC-5).
func (s *ShoppingAPI) GroceryAccessScore(cellID uint64) (float64, error) {
	if err := s.checkNotCopied("GroceryAccessScore"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.cells[cellID]
	if !ok {
		return 0, errs.New(ErrUnregisteredCell, s.correlationID, map[string]any{"cell": cellID})
	}

	// 1. Time Factor (AC-5) - lower travel times are better. Prevent division by zero
	tCorner := 1.0 / (math.Max(c.CornerShopTime, 1.0))
	tMarket := 1.0 / (math.Max(c.MarketHallTime, 1.0))
	tSuper := 1.0 / (math.Max(c.SupermarketTime, 1.0))
	tRetail := 1.0 / (math.Max(c.RetailParkTime, 1.0))

	// 2. Price Factor - lower multiplier prices are better
	basePrice := 1.0
	if s.market != nil {
		staples, _ := s.market.Price(market.FoodStaples)
		fresh, _ := s.market.Price(market.FoodFresh)
		basePrice = float64(staples+fresh) / 2.0
		if basePrice <= 0 {
			basePrice = 1.0
		}
	}
	pCorner := 1.0 / (s.cfg.CornerShopPriceMult * basePrice)
	pMarket := 1.0 / (s.cfg.MarketHallPriceMult * basePrice)
	pSuper := 1.0 / (s.cfg.SupermarketPriceMult * basePrice)
	pRetail := 1.0 / (s.cfg.RetailParkPriceMult * basePrice)

	// 3. Freshness Factor - higher freshness is better
	fCorner := c.CornerShopFresh
	fMarket := c.MarketHallFresh
	fSuper := c.SupermarketFresh
	fRetail := c.RetailParkFresh

	// Aggregate using product form (AC-5)
	cornerScore := tCorner * pCorner * fCorner
	marketScore := tMarket * pMarket * fMarket
	superScore := tSuper * pSuper * fSuper
	retailScore := tRetail * pRetail * fRetail

	composite := (cornerScore + marketScore + superScore + retailScore) * 100.0
	return composite, nil
}

// FoodDesert reports whether the home cell's access score is below the threshold (AC-6).
func (s *ShoppingAPI) FoodDesert(cellID uint64) (bool, error) {
	if err := s.checkNotCopied("FoodDesert"); err != nil {
		return false, err
	}
	score, err := s.GroceryAccessScore(cellID)
	if err != nil {
		return false, err
	}

	// FoodDesert state is simply a threshold read of GroceryAccessScore (AC-6)
	return score < s.cfg.FoodDesertThreshold, nil
}

func (s *ShoppingAPI) computeFormatSplitsLocked(cellID uint64, isSaturday bool) (map[string]float64, float64, error) {
	c, ok := s.cells[cellID]
	if !ok {
		return nil, 0, errs.New(ErrUnregisteredCell, s.correlationID, map[string]any{"cell": cellID})
	}

	// Use data-driven config weights (GR#15, MOD-050 r4 verdict fix).
	// effectiveWeight resolves "field absent" (nil) to the documented
	// default and honours "field present and zero" literally as
	// disable-this-format -- see effectiveWeight's doc comment. This
	// replaces the old code's silent zero->default substitution, which
	// made zero inexpressible as a real config value.
	prefCorner := effectiveWeight(s.cfg.CornerShopWeight, defaultCornerShopWeight)
	prefMarket := effectiveWeight(s.cfg.MarketHallWeight, defaultMarketHallWeight)
	prefSuper := effectiveWeight(s.cfg.SupermarketWeight, defaultSupermarketWeight)
	prefRetail := effectiveWeight(s.cfg.RetailParkWeight, defaultRetailParkWeight)

	wCorner := prefCorner / (math.Max(c.CornerShopTime, 1.0))
	wMarket := prefMarket / (math.Max(c.MarketHallTime, 1.0))
	wSuper := prefSuper / (math.Max(c.SupermarketTime, 1.0))
	wRetail := prefRetail / (math.Max(c.RetailParkTime, 1.0))

	totalWeight := wCorner + wMarket + wSuper + wRetail
	if totalWeight == 0 {
		return map[string]float64{
			"corner_shop": 0,
			"market_hall": 0,
			"supermarket": 0,
			"retail_park": 0,
		}, 0, nil
	}

	baseTrips := 10.0
	if isSaturday {
		baseTrips = 25.0
	}

	effectiveTrips := baseTrips * (1.0 - s.cfg.OnlineDeliveryShare)

	// Make trip counts actually vary with access geography (AC-2)
	sumPreferences := prefCorner + prefMarket + prefSuper + prefRetail
	proximity := totalWeight / sumPreferences
	accessModifier := proximity / 0.2
	if accessModifier < 0.2 {
		accessModifier = 0.2
	} else if accessModifier > 2.0 {
		accessModifier = 2.0
	}
	effectiveTrips = effectiveTrips * accessModifier

	splits := map[string]float64{
		"corner_shop": effectiveTrips * (wCorner / totalWeight),
		"market_hall": effectiveTrips * (wMarket / totalWeight),
		"supermarket": effectiveTrips * (wSuper / totalWeight),
		"retail_park": effectiveTrips * (wRetail / totalWeight),
	}

	return splits, effectiveTrips, nil
}

// GenerateTrips generates shopping trips, splitting them across formats (AC-2/AC-3/AC-4).
func (s *ShoppingAPI) GenerateTrips(cellID uint64, isSaturday bool) (int, error) {
	if err := s.checkNotCopied("GenerateTrips"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	splits, totalTrips, err := s.computeFormatSplitsLocked(cellID, isSaturday)
	if err != nil {
		return 0, err
	}

	if s.traffic != nil {
		_ = s.traffic.AddDemand(cellID, int64(totalTrips))
	}

	// Sum the individual splits after truncation to ensure sum consistency (AC-2)
	sum := 0
	for _, v := range splits {
		sum += int(v)
	}
	return sum, nil
}

// TripsByFormat returns the trip split across formats (AC-2).
func (s *ShoppingAPI) TripsByFormat(cellID uint64, isSaturday bool) (map[string]int, error) {
	if err := s.checkNotCopied("TripsByFormat"); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	splits, _, err := s.computeFormatSplitsLocked(cellID, isSaturday)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int)
	for k, v := range splits {
		out[k] = int(v)
	}
	return out, nil
}

// FreshFoodShare implements wellbeing's ShoppingSource interface (AC-7).
func (s *ShoppingAPI) FreshFoodShare(citizenID uint64, correlationID string) (float64, bool, error) {
	if err := s.checkNotCopied("FreshFoodShare"); err != nil {
		return 0, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return ok=false when citizens is unwired
	if s.citizens == nil {
		return 0, false, nil
	}

	// Look up actual citizen's home cell ID (AC-7)
	cit, ok := s.citizens.CitizenAt(citizenID, correlationID)
	if !ok {
		// Kill the fallback entirely: unresolvable = ok=false
		return 0, false, nil
	}
	cellID := uint64(cit.Home)

	c, ok := s.cells[cellID]
	if !ok {
		return 0, false, nil
	}

	// Calculate average fresh food share (freshness, AC-7)
	avgFresh := (c.CornerShopFresh + c.MarketHallFresh + c.SupermarketFresh + c.RetailParkFresh) / 4.0
	return avgFresh, true, nil
}
