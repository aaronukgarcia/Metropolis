package coastal

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Snapshot is the durable, plain-data image of a CoastalAPI's mutable state
// (FEAT-2326609833 AC-9): world inputs, the three policy sliders, the event
// stream, every case in insertion order, the backlog queue, the id counters
// and the cumulative cost ledgers. The seed and Config are NOT in it -- they
// are reproduced from the world seed and data/coastal.json, exactly like every
// other module participant. The composition root serialises it; this package
// stays free of the save machinery (no registered coastal -> save edge).
type Snapshot struct {
	EraTier         int     `json:"eraTier"`
	SeasonIndex     int     `json:"seasonIndex"`
	WorldConditions float64 `json:"worldConditions"`

	ProcessingFunding     float64 `json:"processingFunding"`
	HousingApproach       float64 `json:"housingApproach"`
	IntegrationInvestment float64 `json:"integrationInvestment"`

	Arrivals      []ArrivalEvent `json:"arrivals"`
	Cases         []Case         `json:"cases"` // in caseOrder
	Backlog       []CaseID       `json:"backlog"`
	NextCaseID    CaseID         `json:"nextCaseID"`
	NextArrivalID uint64         `json:"nextArrivalID"`

	ProcessingOpex  int64   `json:"processingOpex"`
	IntegrationOpex int64   `json:"integrationOpex"`
	HotelCost       int64   `json:"hotelCost"`
	DepartureCost   int64   `json:"departureCost"`
	Friction        float64 `json:"friction"`
}

// Snapshot returns a deep copy of the module's mutable state.
func (c *CoastalAPI) Snapshot() Snapshot {
	if err := c.checkNotCopied("Snapshot"); err != nil {
		return Snapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Snapshot{
		EraTier:               c.eraTier,
		SeasonIndex:           c.seasonIndex,
		WorldConditions:       c.worldConditions,
		ProcessingFunding:     c.processingFunding,
		HousingApproach:       c.housingApproach,
		IntegrationInvestment: c.integrationInvestment,
		Arrivals:              append([]ArrivalEvent{}, c.arrivals...),
		Cases:                 make([]Case, 0, len(c.caseOrder)),
		Backlog:               append([]CaseID{}, c.backlog...),
		NextCaseID:            c.nextCaseID,
		NextArrivalID:         c.nextArrivalID,
		ProcessingOpex:        c.processingOpex,
		IntegrationOpex:       c.integrationOpex,
		HotelCost:             c.hotelCost,
		DepartureCost:         c.departureCost,
		Friction:              c.friction,
	}
	for _, id := range c.caseOrder {
		s.Cases = append(s.Cases, c.cases[id])
	}
	return s
}

// Restore replaces the module's mutable state with s after validating it
// (nothing is applied on a validation failure). A snapshot that references an
// unknown case, repeats a case id, carries a non-finite / out-of-range slider
// or world input, or a negative ledger is rejected with ErrOutOfRange /
// ErrNonFinite -- never partially applied.
func (c *CoastalAPI) Restore(s Snapshot) error {
	if err := c.checkNotCopied("Restore"); err != nil {
		return err
	}
	bad := func(field string, v any) error {
		return c.coastalErr(ErrOutOfRange, map[string]any{"field": "snapshot." + field, "value": fmt.Sprint(v)})
	}
	if s.EraTier < 0 || s.EraTier >= numEraTiers {
		return bad("eraTier", s.EraTier)
	}
	if s.SeasonIndex < 0 || s.SeasonIndex >= numSeasons {
		return bad("seasonIndex", s.SeasonIndex)
	}
	for name, v := range map[string]float64{
		"worldConditions":       s.WorldConditions,
		"processingFunding":     s.ProcessingFunding,
		"housingApproach":       s.HousingApproach,
		"integrationInvestment": s.IntegrationInvestment,
	} {
		if !num.IsFinite(v) {
			return c.coastalErr(ErrNonFinite, map[string]any{"field": "snapshot." + name})
		}
		if v < 0 || v > 1 {
			return bad(name, v)
		}
	}
	if !num.IsFinite(s.Friction) || s.Friction < 0 {
		return bad("friction", s.Friction)
	}
	for name, v := range map[string]int64{
		"processingOpex": s.ProcessingOpex, "integrationOpex": s.IntegrationOpex,
		"hotelCost": s.HotelCost, "departureCost": s.DepartureCost,
	} {
		if v < 0 {
			return bad(name, v)
		}
	}
	cases := make(map[CaseID]Case, len(s.Cases))
	order := make([]CaseID, 0, len(s.Cases))
	for _, k := range s.Cases {
		if _, dup := cases[k.ID]; dup {
			return bad("cases.duplicate", uint64(k.ID))
		}
		if k.ID >= s.NextCaseID {
			return bad("cases.id>=nextCaseID", uint64(k.ID))
		}
		cases[k.ID] = k
		order = append(order, k.ID)
	}
	seenBacklog := make(map[CaseID]bool, len(s.Backlog))
	for _, id := range s.Backlog {
		if _, ok := cases[id]; !ok || seenBacklog[id] {
			return bad("backlog", uint64(id))
		}
		seenBacklog[id] = true
	}
	for _, ev := range s.Arrivals {
		if ev.ID >= s.NextArrivalID {
			return bad("arrivals.id>=nextArrivalID", ev.ID)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraTier = s.EraTier
	c.seasonIndex = s.SeasonIndex
	c.worldConditions = s.WorldConditions
	c.processingFunding = s.ProcessingFunding
	c.housingApproach = s.HousingApproach
	c.integrationInvestment = s.IntegrationInvestment
	c.arrivals = append([]ArrivalEvent(nil), s.Arrivals...)
	c.cases = cases
	c.caseOrder = order
	c.backlog = append([]CaseID(nil), s.Backlog...)
	c.nextCaseID = s.NextCaseID
	c.nextArrivalID = s.NextArrivalID
	c.processingOpex = s.ProcessingOpex
	c.integrationOpex = s.IntegrationOpex
	c.hotelCost = s.HotelCost
	c.departureCost = s.DepartureCost
	c.friction = s.Friction
	return nil
}
