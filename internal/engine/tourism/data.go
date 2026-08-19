package tourism

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §44 leaves unquantified lives here, never as a Go literal.
const fileName = "tourism.json"

// tourismData is the JSON shape of data/tourism.json. The access-tier,
// accommodation and portfolio-weight maps are keyed by the names the
// corresponding String() methods render, and are only ever ACCESSED by key
// (never ranged), so JSON object-key order is irrelevant to determinism
// (GR#21).
type tourismData struct {
	Version                  int                       `json:"version"`
	AccessTiers              map[string]accessTierData `json:"accessTiers"`
	ReputationScale          float64                   `json:"reputationScale"`
	ReputationLagMonths      int                       `json:"reputationLagMonths"`
	StayingVisitorStayMonths int                       `json:"stayingVisitorStayMonths"`
	StayingVisitorRate       float64                   `json:"stayingVisitorRate"`
	DayTripRate              float64                   `json:"dayTripRate"`
	PortfolioWeights         map[string]float64        `json:"portfolioWeights"`
	Accommodation            map[string]int64          `json:"accommodation"`
	Load                     loadData                  `json:"load"`
	Spend                    spendData                 `json:"spend"`
}

type accessTierData struct {
	ReachMultiplier float64 `json:"reachMultiplier"`
}

type loadData struct {
	DayTripperTransport float64 `json:"dayTripperTransport"`
	DayTripperWaste     float64 `json:"dayTripperWaste"`
	DayTripperPolicing  float64 `json:"dayTripperPolicing"`
	StayingTransport    float64 `json:"stayingTransport"`
	StayingWaste        float64 `json:"stayingWaste"`
	StayingPolicing     float64 `json:"stayingPolicing"`
}

type spendData struct {
	DayTripHours               float64 `json:"dayTripHours"`
	DayTripMicroPounds         int64   `json:"dayTripMicroPounds"`
	StayingPerNightMicroPounds int64   `json:"stayingPerNightMicroPounds"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. It checks field presence; the numeric domain checks
// run in Config.validate after conversion (the split mirrors
// foundation.data's "per-field vs semantic" division).
func (d *tourismData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	for tier := AccessDomestic; int(tier) < numAccessTiers; tier++ {
		if _, ok := d.AccessTiers[tier.String()]; !ok {
			return &data.FieldError{Field: "accessTiers." + tier.String(), Rule: "required for every access tier"}
		}
	}
	for term := TermKind(0); int(term) < numPortfolioTerms; term++ {
		if _, ok := d.PortfolioWeights[term.String()]; !ok {
			return &data.FieldError{Field: "portfolioWeights." + term.String(), Rule: "required for every portfolio term"}
		}
	}
	for kind := AccommodationKind(0); int(kind) < numAccommodationKinds; kind++ {
		if _, ok := d.Accommodation[kind.String()]; !ok {
			return &data.FieldError{Field: "accommodation." + kind.String(), Rule: "required for every accommodation kind"}
		}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *tourismData.
func (d *tourismData) Validate() error { return d.validate() }

// config converts the decoded JSON shape into the runtime Config. The
// access-tier/accommodation/weight maps are looked up by the fixed enums in
// order, so the resulting arrays are deterministic.
func (d *tourismData) config() Config {
	var c Config
	for tier := AccessDomestic; int(tier) < numAccessTiers; tier++ {
		c.AccessTierReach[tier] = d.AccessTiers[tier.String()].ReachMultiplier
	}
	c.ReputationScale = d.ReputationScale
	c.ReputationLagMonths = d.ReputationLagMonths
	c.StayingVisitorStayMonths = d.StayingVisitorStayMonths
	c.StayingVisitorRate = d.StayingVisitorRate
	c.DayTripRate = d.DayTripRate
	for term := TermKind(0); int(term) < numPortfolioTerms; term++ {
		c.PortfolioWeights[term] = d.PortfolioWeights[term.String()]
	}
	for kind := AccommodationKind(0); int(kind) < numAccommodationKinds; kind++ {
		c.Accommodation[kind] = d.Accommodation[kind.String()]
	}
	c.Load = LoadConfig{
		DayTripperTransport: d.Load.DayTripperTransport,
		DayTripperWaste:     d.Load.DayTripperWaste,
		DayTripperPolicing:  d.Load.DayTripperPolicing,
		StayingTransport:    d.Load.StayingTransport,
		StayingWaste:        d.Load.StayingWaste,
		StayingPolicing:     d.Load.StayingPolicing,
	}
	c.Spend = SpendConfig{
		DayTripHours:               d.Spend.DayTripHours,
		DayTripMicroPounds:         d.Spend.DayTripMicroPounds,
		StayingPerNightMicroPounds: d.Spend.StayingPerNightMicroPounds,
	}
	return c
}

// Load reads and schema-validates data/tourism.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *TourismAPI with its balance Config populated and its per-kind
// accommodation facilities seeded. correlationID is attached to every error
// this call (and the returned API's methods) construct (GR#1). Every
// failure is a registry-sourced *errs.E — never a silent default
// substitution, never a panic (AC-16). Dependencies are wired later via
// SetAttract/SetLeisure/SetSeason/SetNews.
func Load(dir, correlationID string) (*TourismAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[tourismData, *tourismData](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrTourismDataInvalid, correlationID, err, map[string]any{
			"dir":    dir,
			"field":  "tourism.json",
			"reason": "load/schema",
			"cause":  err.Error(),
		})
	}
	cfg := d.config()
	if err := cfg.validate(correlationID); err != nil {
		return nil, errs.Wrap(ErrTourismDataInvalid, correlationID, err, map[string]any{
			"dir":    dir,
			"field":  "tourism.json",
			"reason": "semantic",
			"cause":  err.Error(),
		})
	}
	return New(cfg, 0, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then Loads it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*TourismAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
