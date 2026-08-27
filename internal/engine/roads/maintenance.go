package roads

import "github.com/aaronukgarcia/Metropolis/internal/foundation/det"

// This file is the per-road maintenance state (§20/US-6/AC-2): a condition
// value in [0,1] that decays over simulated months absent upkeep, with
// potholes (condition < 1) lowering the effective speed and raising the
// maintenance cost multiplier — the "maintenance budget is real" trade-off.
// This is the per-instance maintenance precedent later modules generalise
// from: each road owns its own condition, decayed by [RoadsAPI.Advance] and
// restored by [RoadsAPI.RepairRoad]. All values are data-driven placeholders
// from data/roads.json's "maintenance" block (GR#15).

// MaintenanceState is the read-only maintenance view [RoadsAPI.MaintenanceState]
// returns (US-6): the raw condition plus its two felt effects.
type MaintenanceState struct {
	Condition         float64 // [0,1]; 1 = perfect, 0 = ruinous
	EffectiveSpeedKPH float64 // speed limit reduced by potholes
	CostMultiplier    float64 // >= 1; maintenance cost scaled up by potholes
}

// MaintenanceState returns a road's current condition and its pothole
// effects (lowered effective speed, raised cost multiplier — US-6). An
// unknown road is ErrRoadNotFound.
func (a *RoadsAPI) MaintenanceState(id RoadID) (MaintenanceState, error) {
	if err := a.checkNotCopied("MaintenanceState"); err != nil {
		return MaintenanceState{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	rs, ok := a.roads[id]
	if !ok {
		return MaintenanceState{}, roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(id)})
	}
	return a.maintenanceStateLocked(rs), nil
}

// maintenanceStateLocked computes a road's condition effects. The caller
// holds a.mu.
func (a *RoadsAPI) maintenanceStateLocked(rs *roadState) MaintenanceState {
	degraded := 1 - rs.condition
	m := a.cfg.maintenance
	eff := float64(rs.speedLimit) * (1 - m.SpeedPenaltyPerConditionBelow*degraded)
	cost := 1 + m.CostMultiplierPerConditionBelow*degraded
	return MaintenanceState{Condition: rs.condition, EffectiveSpeedKPH: eff, CostMultiplier: cost}
}

// RepairRoadCommand spends money to restore a road's condition.
type RepairRoadCommand struct {
	CorrelationID     string
	RoadID            RoadID
	AmountMicropounds int64
}

// RepairRoad restores a road's condition proportionally to the money spent
// (data/roads.json's repairConditionPerPound, US-6), clamped at perfect
// (1.0). A negative amount is ErrInvalidInput; an unknown road is
// ErrRoadNotFound.
func (a *RoadsAPI) RepairRoad(cmd RepairRoadCommand) error {
	if err := a.checkNotCopied("RepairRoad"); err != nil {
		return err
	}
	if cmd.AmountMicropounds < 0 {
		return invalidInputError(a.correlationID, "AmountMicropounds", "must be non-negative")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs, ok := a.roads[cmd.RoadID]
	if !ok {
		return roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(cmd.RoadID)})
	}
	pounds := float64(cmd.AmountMicropounds) / float64(det.MicropoundsPerPound)
	rs.condition += pounds * a.cfg.maintenance.RepairConditionPerPound
	if rs.condition > 1 {
		rs.condition = 1
	}
	return nil
}
