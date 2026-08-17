package roads

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the simulated roadworks surface (§51/AC-6): scheduling a
// phased sequence of lane closures over simulated time, exposed as a reduced
// current-lane-count on this package's own read path. engine.traffic re-
// routing around the reduction is proven by engine.traffic's own tests
// reading [RoadsAPI.CurrentLaneCount] — this package never calls traffic
// All timing is a function of the simulation month index (AC-17), never the
// wall clock.

// ScheduleRoadworksCommand schedules an explicit roadworks sequence on a
// road (the non-upgrade closure path — a maintenance closure). The upgrade
// path ([RoadsAPI.ApplyUpgrade]) schedules its own default phase.
type ScheduleRoadworksCommand struct {
	CorrelationID string
	RoadID        RoadID
	Phases        []RoadworksPhase
	Window        RoadworksWindow
}

// ScheduleRoadworks installs a phased roadworks schedule on a road (AC-6).
// Phases must be non-empty, non-overlapping and time-ordered, each with a
// non-negative start month, a positive duration, and an OpenLanes count in
// [0, steady-state lanes] (ErrInvalidRoadworks otherwise). If Window is
// WindowSummer, every phase start must fall in a calendar summer month (the
// month-index predicate, AC-17). Installing a schedule replaces any in-
// flight upgrade schedule and cancels its pending class change.
func (a *RoadsAPI) ScheduleRoadworks(cmd ScheduleRoadworksCommand) error {
	if err := a.checkNotCopied("ScheduleRoadworks"); err != nil {
		return err
	}
	if !validWindow(cmd.Window) {
		return roadsErr(a.correlationID, ErrInvalidRoadworks, map[string]any{"window": uint8(cmd.Window)})
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	rs, ok := a.roads[cmd.RoadID]
	if !ok {
		return roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(cmd.RoadID)})
	}
	steady := a.cfg.classes[rs.class].Lanes
	if err := validateRoadworks(cmd.Phases, steady, cmd.Window, a.correlationID); err != nil {
		return err
	}
	// Copy and re-sort defensively so the stored schedule is canonical.
	phases := make([]RoadworksPhase, len(cmd.Phases))
	copy(phases, cmd.Phases)
	sort.Slice(phases, func(i, j int) bool { return phases[i].StartMonth < phases[j].StartMonth })
	rs.roadworks = phases
	rs.pendingClass = nil
	return nil
}

// validWindow reports whether w is a known RoadworksWindow.
func validWindow(w RoadworksWindow) bool { return w == WindowAny || w == WindowSummer }

// validateRoadworks checks a phase list: non-empty, time-ordered and
// non-overlapping, each phase with a non-negative start month, a positive
// duration, and an OpenLanes count within [0, steadyLanes]; plus the window
// predicate when Window is WindowSummer. Any violation is ErrInvalidRoadworks.
func validateRoadworks(phases []RoadworksPhase, steadyLanes int, window RoadworksWindow, correlationID string) error {
	if len(phases) == 0 {
		return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{"reason": "empty schedule"})
	}
	var prevEnd int64
	for i, p := range phases {
		if p.StartMonth < 0 {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{"phase": i, "reason": "negative start month"})
		}
		if p.DurationMonths <= 0 {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{"phase": i, "reason": "non-positive duration"})
		}
		endMonth, overflowed := num.SatAddChecked(p.StartMonth, p.DurationMonths)
		if overflowed {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{"phase": i, "reason": "end month overflow"})
		}
		if p.OpenLanes < 0 || p.OpenLanes > steadyLanes {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{
				"phase": i, "openLanes": p.OpenLanes, "steadyLanes": steadyLanes,
			})
		}
		if i > 0 && p.StartMonth < prevEnd {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{"phase": i, "reason": "overlaps prior phase"})
		}
		if window == WindowSummer && !isSummerMonth(p.StartMonth) {
			return roadsErr(correlationID, ErrInvalidRoadworks, map[string]any{
				"phase": i, "startMonth": p.StartMonth, "reason": "not a summer month",
			})
		}
		prevEnd = endMonth
	}
	return nil
}
