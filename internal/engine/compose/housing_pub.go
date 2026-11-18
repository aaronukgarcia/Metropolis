package compose

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// BUG-1220 (FEAT-083 leg 4): the player-facing housing and migration figures
// on chrome.topbar. The engine already decides migration from home capacity
// (compose_vacancy.go); this publishes the SAME numbers so a player can see
// why population stalls and what building does about it.
//
// One source: the stock comes from housingStockFor (the function the vacancy
// rule itself uses) and the full test is housingVacancyUnits == 0 (the rule's
// own zero). Nothing is recomputed per tick: the stock is captured once per
// month, at the migration step, and the publish only adds the live population.
// It posts nothing and never feeds the simulation.

// housingSnapshot is one month's captured housing stock and migration counts.
// Immutable once stored.
type housingSnapshot struct {
	stockPeople int64
	movedIn     int64
	left        int64
}

// housingPubHolder is the publish-only mirror (atomic pointer, safe to read
// from the subscription pump goroutine).
type housingPubHolder = atomic.Pointer[housingSnapshot]

// recordHousingMonth captures the month's stock and migration counts. A plain
// function over its inputs (no copy-guarded receiver).
func recordHousingMonth(h *housingPubHolder, b *build.BuildAPI, seedOverride int64, res attract.MigrationResult) {
	h.Store(&housingSnapshot{
		stockPeople: housingStockFor(b, seedOverride),
		movedIn:     res.Inflow,
		left:        res.Outflow,
	})
}

// chromeHousing mirrors ui.screen.chrome's HousingFigures field-for-field.
type chromeHousing struct {
	Known   bool  `json:"known"`
	Places  int64 `json:"places"`
	Lived   int64 `json:"lived"`
	Free    int64 `json:"free"`
	MovedIn int64 `json:"movedIn"`
	Left    int64 `json:"left"`
	Full    bool  `json:"full"`
}

// buildChromeHousing derives the figures from the month snapshot and the live
// population. Unknown (Known false) until the first migration step of this
// timeline has run, so nothing is invented before or just after a load.
func buildChromeHousing(h *housingPubHolder, c *citizens.CitizensAPI, cid string) chromeHousing {
	snap := h.Load()
	if snap == nil {
		return chromeHousing{}
	}
	var pop int64
	if c != nil {
		pop = int64(c.TotalPopulation(cid))
	}
	free := snap.stockPeople - pop
	if free < 0 {
		free = 0
	}
	return chromeHousing{
		Known:   true,
		Places:  snap.stockPeople,
		Lived:   pop,
		Free:    free,
		MovedIn: snap.movedIn,
		Left:    snap.left,
		Full:    housingVacancyUnits(snap.stockPeople, 0, pop) == 0,
	}
}
