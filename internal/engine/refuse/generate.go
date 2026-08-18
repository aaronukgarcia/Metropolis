package refuse

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// RegisterCell registers (or re-registers) a cell's bin stock for the
// given land use and street (AC-2). An unknown land use is rejected with
// ErrUnknownLandUse (AC-13) rather than a silently-created zero-capacity
// bin. The bin's capacity is read from data/refuse.json's binCapacities
// for that land use (GR#15). Re-registering an existing cell updates the
// land use, capacity, and street; on a SMALLER-capacity re-register each
// in-bin stream level is clamped to the new capacity and the excess is
// spilled into overflow exactly once (AC-2/AC-7 — a bin must never hold
// more than its capacity, and the pre-existing excess must not be re-spilled
// as phantom overflow by a later Generate).
func (r *RefuseAPI) RegisterCell(cellID string, landUse LandUse, street string) error {
	if err := r.checkNotCopied("RegisterCell"); err != nil {
		return err
	}
	if cellID == "" {
		return errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	capRec, ok := r.cfg.BinCapacities[string(landUse)]
	if !ok {
		return errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{
			"cell":    cellID,
			"landUse": string(landUse),
		})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if c, exists := r.cells[cellID]; exists {
		c.landUse = landUse
		c.street = street
		// A smaller-capacity re-register (e.g. industrial skip 6000kg ->
		// residential wheelie 240kg) must not leave an in-bin stream above the
		// new capacity. Clamp each stream's level to the new capacity and spill
		// the excess into overflow exactly once (AC-2/AC-7): the bin never
		// holds more than its capacity, and the next Generate's negative-
		// headroom branch has no pre-existing excess to re-spill into phantom
		// overflow. A larger (or equal) capacity leaves levels untouched —
		// they already fit.
		for i := 0; i < 3; i++ {
			if c.levels[i] > capRec.CapacityKg {
				excess := num.SatSub(c.levels[i], capRec.CapacityKg)
				c.overflow[i] = num.SatAdd(c.overflow[i], excess)
				c.levels[i] = capRec.CapacityKg
			}
		}
		c.capacity = capRec.CapacityKg
		return nil
	}
	r.cells[cellID] = &cellState{
		landUse:  landUse,
		street:   street,
		capacity: capRec.CapacityKg,
	}
	return nil
}

// BinStock returns the read-only snapshot of a cell's typed bin stock
// (AC-1/AC-2). A cell that was never registered has no land-use type and
// is rejected with ErrUnknownLandUse (AC-13) — never a silently-created
// zero-value entry.
func (r *RefuseAPI) BinStock(cellID string) (BinStock, error) {
	if err := r.checkNotCopied("BinStock"); err != nil {
		return BinStock{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cells[cellID]
	if !ok {
		return BinStock{}, errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	return snapshotBin(cellID, c), nil
}

// snapshotBin builds the exported BinStock view from an internal cellState.
func snapshotBin(cellID string, c *cellState) BinStock {
	return BinStock{
		CellID:            cellID,
		LandUse:           c.landUse,
		Street:            c.street,
		Capacity:          c.capacity,
		General:           c.levels[0],
		Recycling:         c.levels[1],
		Food:              c.levels[2],
		OverflowGeneral:   c.overflow[0],
		OverflowRecycling: c.overflow[1],
		OverflowFood:      c.overflow[2],
		VerminIndex:       c.vermin,
		MissCause:         copyMissCause(c.missCause),
	}
}

// Generate produces one tick's waste into a cell's bin stock (AC-2): the
// cell's driver quantity (population for residential, output units for
// commercial/industrial) times the land-use's data-sourced per-driver
// rate, split into the three §25 streams (AC-3). The in-bin levels are
// capped at the bin's capacity; the excess becomes overflow (the real
// state transition AC-2 requires). driver <= 0 generates nothing.
//
// When the cell is left in overflow, the vermin index accumulates (AC-7)
// and, if the wellbeing seam is wired, the pollution-exposure consequence
// is reported through the registered interface.
func (r *RefuseAPI) Generate(cellID string, driver float64) error {
	if err := r.checkNotCopied("Generate"); err != nil {
		return err
	}
	if !num.IsFinite(driver) {
		driver = 0
	}
	if driver <= 0 {
		return nil
	}

	r.mu.Lock()
	c, ok := r.cells[cellID]
	if !ok {
		r.mu.Unlock()
		return errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	rate := r.cfg.WasteRates[string(c.landUse)].PerDriverPerTickKg
	gen := num.ClampInt64FromFloat(math.Floor(driver * rate))
	if gen <= 0 {
		r.mu.Unlock()
		return nil
	}

	// Integer split of gen into the three streams. Recycling and food are
	// floor'd fractions; general is the remainder, so the three allocations
	// always sum exactly to gen (the AC-11 identity's per-tick source term).
	recycling := num.ClampInt64FromFloat(math.Floor(float64(gen) * r.cfg.StreamMix.Recycling))
	food := num.ClampInt64FromFloat(math.Floor(float64(gen) * r.cfg.StreamMix.Food))
	if recycling > gen {
		recycling = gen
	}
	if food > gen-recycling {
		food = gen - recycling
	}
	general := gen - recycling - food

	adds := [3]int64{general, recycling, food}
	overflowed := false
	for i := 0; i < 3; i++ {
		a := adds[i]
		headroom := c.capacity - c.levels[i]
		if a <= headroom {
			c.levels[i] = num.SatAdd(c.levels[i], a)
		} else {
			c.levels[i] = c.capacity
			c.overflow[i] = num.SatAdd(c.overflow[i], num.SatSub(a, headroom))
		}
		if c.overflow[i] > 0 {
			overflowed = true
		}
		r.generated[i] = num.SatAdd(r.generated[i], a)
	}

	if overflowed {
		r.accumulateVerminLocked(c)
	}

	w := r.wellbeing
	exposure := c.vermin
	r.mu.Unlock()

	// Report the pollution-exposure consequence outside the lock (the seam
	// may call back). Only when wired.
	if overflowed && w != nil && exposure > 0 {
		_ = w.ReportPollutionExposure(cellID, exposure)
	}
	return nil
}

// SetContamination sets the city-wide recycling contamination level
// (AC-3). A value outside [0,1] is rejected with ErrInvalidContamination
// (AC-14) — never silently clamped.
func (r *RefuseAPI) SetContamination(level float64) error {
	if err := r.checkNotCopied("SetContamination"); err != nil {
		return err
	}
	if !num.IsFinite(level) || level < 0 || level > 1 {
		return errs.New(ErrInvalidContamination, r.correlationID, map[string]any{"level": level})
	}
	r.mu.Lock()
	r.contamination = level
	r.mu.Unlock()
	return nil
}

// Contamination returns the current city-wide recycling contamination
// level (0..1).
func (r *RefuseAPI) Contamination() float64 {
	if err := r.checkNotCopied("Contamination"); err != nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contamination
}

// RecyclingResaleValue returns the recycling stream's resale value in
// micro-pounds (AC-3): the collected recycling tonnage times the
// data-sourced per-kg baseline, discounted by the contamination level
// times the data-sourced penalty. Raising contamination lowers ONLY this
// stream's output — general and food are untouched by design.
func (r *RefuseAPI) RecyclingResaleValue() int64 {
	if err := r.checkNotCopied("RecyclingResaleValue"); err != nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	collected := r.collected[1] // recycling is index 1
	baseline := r.cfg.Contamination.ResaleValuePerKgMicropounds
	penalty := r.cfg.Contamination.PenaltyPerContamination
	factor := 1 - r.contamination*penalty
	if factor < 0 {
		factor = 0
	}
	return num.ClampInt64FromFloat(math.Floor(float64(collected) * baseline * factor))
}

// GeneralLevel returns a cell's in-bin general-waste level (kg) — the
// first of the three named per-stream accessors (AC-3).
func (r *RefuseAPI) GeneralLevel(cellID string) (int64, error) {
	if err := r.checkNotCopied("GeneralLevel"); err != nil {
		return 0, err
	}
	return r.streamLevel(cellID, 0)
}

// RecyclingLevel returns a cell's in-bin recycling level (kg).
func (r *RefuseAPI) RecyclingLevel(cellID string) (int64, error) {
	if err := r.checkNotCopied("RecyclingLevel"); err != nil {
		return 0, err
	}
	return r.streamLevel(cellID, 1)
}

// FoodLevel returns a cell's in-bin food-waste level (kg).
func (r *RefuseAPI) FoodLevel(cellID string) (int64, error) {
	if err := r.checkNotCopied("FoodLevel"); err != nil {
		return 0, err
	}
	return r.streamLevel(cellID, 2)
}

func (r *RefuseAPI) streamLevel(cellID string, idx int) (int64, error) {
	if err := r.checkNotCopied("streamLevel"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cells[cellID]
	if !ok {
		return 0, errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	return c.levels[idx], nil
}
