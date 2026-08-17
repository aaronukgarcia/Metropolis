package coastal

import (
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CoastalAPI is code.json's "engine.coastal" inbound contract (CoastalAPI,
// GUID aa2f1db7-481e-468a-be1c-63cdebf2b0e9, "event stream + pipeline
// stages; policies move throughput coefficients"): §30's coastal arrivals
// module — the small-boat arrival event stream (never player-triggered), the
// coastguard/lifeboat rescue response, the reception-and-processing capacity
// with a caseworker-throughput ceiling and hotel-requisition overflow, the
// granted/not-granted status pipeline, the three policy sliders with real
// trade-offs, and factual news reporting through engine.news.
//
// The zero value is not usable; construct via [New] or [Load], then wire the
// outbound dependencies ([SetCitizens]/[SetServices]/[SetNews]) and the shore
// source ([SetShore]). A *CoastalAPI is safe for concurrent use: mutable state
// is guarded by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020 class, mirroring engine.comms/engine.services).
type CoastalAPI struct {
	mu            sync.RWMutex
	correlationID string

	seed uint64
	cfg  Config

	// Outbound dependencies, wired via Set* and read under mu (GR#20 — each
	// consumed through its registered inbound contract alone).
	citizens *citizens.CitizensAPI
	services *services.ServicesAPI
	news     *news.NewsAPI
	shore    ShoreSource

	// World-state inputs, pushed by the tick/composition root (AC-3).
	eraTier         int
	seasonIndex     int
	worldConditions float64

	// Policy sliders (AC-11), each in [0,1].
	processingFunding     float64
	housingApproach       float64
	integrationInvestment float64

	// Pipeline state.
	arrivals      []ArrivalEvent
	cases         map[CaseID]Case
	caseOrder     []CaseID // insertion order, for deterministic iteration (GR#21)
	backlog       []CaseID // FIFO queue of cases awaiting a caseworker
	nextCaseID    CaseID
	nextArrivalID uint64

	// Accounting (micro-pounds / satisfaction points), accumulated over the
	// run — the "both sides of the ledger honest" figures (§30).
	processingOpex  int64
	integrationOpex int64
	hotelCost       int64
	departureCost   int64
	friction        float64

	// self is the SEC-020 copy guard, stored exactly once in New before the
	// value is returned to any caller (mirroring engine.comms).
	self atomic.Pointer[CoastalAPI]
}

// New constructs a ready-to-wire CoastalAPI from a validated Config and a
// fixed world seed (AC-15's determinism anchor). correlationID is attached
// to every error the returned API constructs (GR#1); an empty one mints a
// fresh ID. Dependencies are wired later via the Set* methods.
func New(seed uint64, cfg Config, correlationID string) (*CoastalAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{"cause": err.Error()})
	}
	c := &CoastalAPI{
		correlationID:         correlationID,
		seed:                  seed,
		cfg:                   cfg,
		eraTier:               0,
		seasonIndex:           0,
		worldConditions:       0,
		processingFunding:     cfg.Policy.ProcessingFundingDefault,
		housingApproach:       cfg.Policy.HousingApproachDefault,
		integrationInvestment: cfg.Policy.IntegrationInvestmentDefault,
		cases:                 make(map[CaseID]Case),
		nextCaseID:            1,
		nextArrivalID:         1,
	}
	c.self.Store(c) // armed exactly once, before c is returned (SEC-020)
	return c, nil
}

// Load reads and schema-validates data/coastal.json from dir and returns a
// ready-to-wire *CoastalAPI (GR#15). Every failure is a registry-sourced
// *errs.E — never a panic or a silent default.
func Load(seed uint64, dir, correlationID string) (*CoastalAPI, error) {
	cfg, err := LoadConfig(filepath.Join(dir, fileCoastal), correlationID)
	if err != nil {
		return nil, err
	}
	return New(seed, cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(seed uint64, correlationID string) (*CoastalAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(seed, dir, correlationID)
}

// coastalErr builds a registry-sourced error under the API's correlation ID
// (GR#7/GR#1). It is a package-level function (not a method) so checkNotCopied
// can call it without recursing.
func (c *CoastalAPI) coastalErr(code string, ctx map[string]any) *errs.E {
	return errs.New(code, c.correlationID, ctx)
}

// checkNotCopied rejects a method call on a struct-copied *CoastalAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and safe to
// run before mu is ever touched.
func (c *CoastalAPI) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return errs.New(ErrCopiedValue, c.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency (AC-6's granted-citizen
// creation).
func (c *CoastalAPI) SetCitizens(cit *citizens.CitizensAPI) error {
	if err := c.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.citizens = cit
	return nil
}

// SetServices wires the engine.services dependency (AC-4's rescue-capacity
// read).
func (c *CoastalAPI) SetServices(s *services.ServicesAPI) error {
	if err := c.checkNotCopied("SetServices"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services = s
	return nil
}

// SetNews wires the engine.news dependency (AC-12's factual reporting).
func (c *CoastalAPI) SetNews(n *news.NewsAPI) error {
	if err := c.checkNotCopied("SetNews"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.news = n
	return nil
}

// SetShore wires the shore-cell membership source (engine.world's geography
// through the local seam — see [ShoreSource] and ASM-207).
func (c *CoastalAPI) SetShore(src ShoreSource) error {
	if err := c.checkNotCopied("SetShore"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shore = src
	return nil
}

// SetEraTier sets the current §4 milestone tier (0..13), the era input to the
// arrival-frequency multiplier table (AC-3). A tier outside 0..13 is rejected
// with ErrInvalidEraTier, never an out-of-bounds index.
func (c *CoastalAPI) SetEraTier(tier int) error {
	if err := c.checkNotCopied("SetEraTier"); err != nil {
		return err
	}
	if tier < 0 || tier >= numEraTiers {
		return c.coastalErr(ErrInvalidEraTier, map[string]any{"tier": tier})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraTier = tier
	return nil
}

// SetSeasonIndex sets the current §27 season (0..3), the season input to the
// arrival-frequency multiplier table (AC-3). An index outside 0..3 is rejected
// with ErrInvalidSeasonIndex.
func (c *CoastalAPI) SetSeasonIndex(season int) error {
	if err := c.checkNotCopied("SetSeasonIndex"); err != nil {
		return err
	}
	if season < 0 || season >= numSeasons {
		return c.coastalErr(ErrInvalidSeasonIndex, map[string]any{"season": season})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seasonIndex = season
	return nil
}

// SetWorldConditions sets the §30 "world conditions" push factor, a finite
// value in [0,1] (regional instability) that raises arrival frequency. A
// non-finite value is ErrNonFinite; outside [0,1] is ErrOutOfRange — never
// silently clamped.
func (c *CoastalAPI) SetWorldConditions(cond float64) error {
	if err := c.checkNotCopied("SetWorldConditions"); err != nil {
		return err
	}
	if !num.IsFinite(cond) {
		return c.coastalErr(ErrNonFinite, map[string]any{"field": "worldConditions"})
	}
	if cond < 0 || cond > 1 {
		return c.coastalErr(ErrOutOfRange, map[string]any{"field": "worldConditions", "value": cond})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.worldConditions = cond
	return nil
}

// Arrivals returns a snapshot of every arrival event, in generation order
// (AC-1's event-stream accessor). The returned slice is a defensive copy.
func (c *CoastalAPI) Arrivals() []ArrivalEvent {
	if err := c.checkNotCopied("Arrivals"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ArrivalEvent, len(c.arrivals))
	copy(out, c.arrivals)
	return out
}

// ArrivalCount returns the number of arrival events generated so far.
func (c *CoastalAPI) ArrivalCount() int {
	if err := c.checkNotCopied("ArrivalCount"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.arrivals)
}

// CaseStage returns a case's current pipeline stage (AC-1's per-case query).
// An unknown case ID is rejected with ErrUnknownCase (AC-13), never a
// fabricated zero-value stage.
func (c *CoastalAPI) CaseStage(id CaseID) (CaseStage, error) {
	if err := c.checkNotCopied("CaseStage"); err != nil {
		return 0, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	kase, ok := c.cases[id]
	if !ok {
		return 0, c.coastalErr(ErrUnknownCase, map[string]any{"case": uint64(id)})
	}
	return kase.Stage, nil
}

// Case returns a case's full queryable record. An unknown case ID is rejected
// with ErrUnknownCase. Terminal state is never deleted (AC-7): a granted case
// keeps its CitizenID, a not-granted case keeps its DepartureCost.
func (c *CoastalAPI) Case(id CaseID) (Case, error) {
	if err := c.checkNotCopied("Case"); err != nil {
		return Case{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	kase, ok := c.cases[id]
	if !ok {
		return Case{}, c.coastalErr(ErrUnknownCase, map[string]any{"case": uint64(id)})
	}
	return kase, nil
}

// Backlog returns the number of cases still awaiting a caseworker (AC-5/AC-11's
// backlog metric).
func (c *CoastalAPI) Backlog() int64 {
	if err := c.checkNotCopied("Backlog"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return int64(len(c.backlog))
}

// ProcessingOpex returns the cumulative processing-funding opex (AC-11's cost
// metric) in micro-pounds.
func (c *CoastalAPI) ProcessingOpex() int64 {
	if err := c.checkNotCopied("ProcessingOpex"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.processingOpex
}

// IntegrationOpex returns the cumulative integration-investment opex.
func (c *CoastalAPI) IntegrationOpex() int64 {
	if err := c.checkNotCopied("IntegrationOpex"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.integrationOpex
}

// HotelCost returns the cumulative hotel-requisition cost (AC-5).
func (c *CoastalAPI) HotelCost() int64 {
	if err := c.checkNotCopied("HotelCost"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hotelCost
}

// DepartureCost returns the cumulative managed-departure cost (AC-7).
func (c *CoastalAPI) DepartureCost() int64 {
	if err := c.checkNotCopied("DepartureCost"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.departureCost
}

// SatisfactionFriction returns the cumulative local satisfaction friction
// (AC-5's friction signal).
func (c *CoastalAPI) SatisfactionFriction() float64 {
	if err := c.checkNotCopied("SatisfactionFriction"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.friction
}

// TotalCost returns the summed coastal cost ledger (processing + integration
// opex + hotel + departure), saturated at the int64 extremes (GR#16).
func (c *CoastalAPI) TotalCost() int64 {
	if err := c.checkNotCopied("TotalCost"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	t := num.SatAdd(c.processingOpex, c.integrationOpex)
	t = num.SatAdd(t, c.hotelCost)
	return num.SatAdd(t, c.departureCost)
}

// sortedCellCoords returns a deterministic (X, then Y) ordering of the shore
// cells so the per-arrival cell draw is order-independent of the source's
// iteration order (GR#21). The input is not mutated.
func sortedCellCoords(cells []CellCoord) []CellCoord {
	out := make([]CellCoord, len(cells))
	copy(out, cells)
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}
