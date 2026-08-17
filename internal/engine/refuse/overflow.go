package refuse

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is AC-7's overflow chain: a cell whose bin stock exceeds
// capacity accumulates a vermin index (rising with overflow duration and
// magnitude), which drives — in documented order — a physical-health
// penalty routed through the registered WellbeingAPI seam (never a
// refuse-owned health number), a land-value penalty, a fire-risk increase,
// and a street-naming ticker event.

// accumulateVerminLocked advances a cell's vermin index by its total
// overflow tonnage times the data-sourced per-kg rate (GR#15). Caller
// holds r.mu. The index rises with BOTH overflow magnitude (more spilled
// tonnes) and duration (every tick in overflow adds more), so a sustained
// missed collection compounds the consequence.
func (r *RefuseAPI) accumulateVerminLocked(c *cellState) {
	var totalOverflow int64
	for i := 0; i < 3; i++ {
		totalOverflow = num.SatAdd(totalOverflow, c.overflow[i])
	}
	if totalOverflow <= 0 {
		return
	}
	rate := r.cfg.Vermin.PerKgOverflowPerTick
	c.vermin += float64(totalOverflow) * rate
}

// VerminIndex returns a cell's accumulated vermin index (AC-7): 0 for a
// cell that has never overflowed, rising monotonically with sustained
// overflow. An unregistered cell is rejected with ErrUnknownLandUse.
func (r *RefuseAPI) VerminIndex(cellID string) (float64, error) {
	if err := r.checkNotCopied("VerminIndex"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cells[cellID]
	if !ok {
		return 0, errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	return c.vermin, nil
}

// LandValuePenalty returns a cell's land-value penalty (AC-7), the vermin
// index times the data-sourced per-vermin rate. It is a directional
// placeholder (see data/refuse.json) — the magnitude is balance data, the
// monotonic direction is the contract.
func (r *RefuseAPI) LandValuePenalty(cellID string) (float64, error) {
	if err := r.checkNotCopied("LandValuePenalty"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cells[cellID]
	if !ok {
		return 0, errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	return c.vermin * r.cfg.Vermin.LandValuePenaltyPerVermin, nil
}

// FireRiskIncrease returns a cell's fire-risk increase (AC-7), the vermin
// index times the data-sourced per-vermin rate.
func (r *RefuseAPI) FireRiskIncrease(cellID string) (float64, error) {
	if err := r.checkNotCopied("FireRiskIncrease"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cells[cellID]
	if !ok {
		return 0, errs.New(ErrUnknownLandUse, r.correlationID, map[string]any{"cell": cellID})
	}
	return c.vermin * r.cfg.Vermin.FireRiskPerVermin, nil
}

// OverflowTickerEvents returns one street-naming [TickerEvent] per cell
// currently in overflow, in deterministic (ascending cell ID) order
// (AC-7/US-5). Each event names the affected street and the stream that
// overflowed most, so the consequence is legible and locatable rather than
// a city-wide aggregate.
func (r *RefuseAPI) OverflowTickerEvents() []TickerEvent {
	if err := r.checkNotCopied("OverflowTickerEvents"); err != nil {
		return nil
	}
	r.mu.RLock()
	var ids []string
	for id, c := range r.cells {
		if c.overflow[0] > 0 || c.overflow[1] > 0 || c.overflow[2] > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]TickerEvent, 0, len(ids))
	for _, id := range ids {
		c := r.cells[id]
		// Pick the stream with the largest overflow for the ticker's headline.
		stream, kg := StreamGeneral, c.overflow[0]
		if c.overflow[1] > kg {
			stream, kg = StreamRecycling, c.overflow[1]
		}
		if c.overflow[2] > kg {
			stream, kg = StreamFood, c.overflow[2]
		}
		out = append(out, TickerEvent{
			Street:     c.street,
			CellID:     id,
			Stream:     stream,
			OverflowKg: kg,
			MissCause:  copyMissCause(c.missCause),
		})
	}
	r.mu.RUnlock()
	return out
}
