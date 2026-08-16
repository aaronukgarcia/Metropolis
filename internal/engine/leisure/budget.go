package leisure

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// TimeBudget is the §42 weekly 168-hour decomposition for one citizen —
// the load-bearing core of this module. It is a DETERMINISTIC budget, never
// a flat constant: the work/education/sleep/chore baselines vary by life
// stage (data), the commute varies per citizen with engine.traffic's own
// figure, and overtime is a per-citizen choice. The residual discretionary
// time is split between leisure (going out) and rest (staying home) by the
// citizen's own taste weights.
type TimeBudget struct {
	WorkHours      float64 // firm hours (baseline + overtime)
	EducationHours float64 // school hours (students/children)
	SleepHours     float64
	ChoreHours     float64
	CommuteHours   float64 // engine.traffic's door-to-door figure (AC-2)
	OvertimeHours  float64

	// Discretionary is 168 − work − education − sleep − chores − commute.
	Discretionary float64

	// LeisureHours/RestHours split Discretionary by the citizen's going-out
	// vs home taste share.
	LeisureHours float64
	RestHours    float64

	// OvertimeWage is overtime × wage rate — the "overtime generates wages"
	// half of the trade-off (the "harms wellbeing" half is the leisure-time
	// squeeze it causes).
	OvertimeWage float64
}

// computeBudget assembles the weekly budget for one citizen. Pure. A
// non-finite or negative commute/overtime figure is treated as zero rather
// than leaked into the discretionary arithmetic (GR#16) — NaN in never yields
// NaN out.
func computeBudget(cfg Config, stage LifeStage, commute, overtime float64, w citizens.LeisureWeights) TimeBudget {
	if !num.IsFinite(commute) || commute < 0 {
		commute = 0
	}
	if !num.IsFinite(overtime) || overtime < 0 {
		overtime = 0
	}
	work := cfg.Work[stage] + overtime
	edu := cfg.Education[stage]
	sleep := cfg.Sleep[stage]
	chores := cfg.Chores[stage]
	disc := cfg.HoursPerWeek - work - edu - sleep - chores - commute
	if disc < 0 {
		disc = 0
	}
	out := goingOutShare(w)
	leisure := disc * out
	return TimeBudget{
		WorkHours:      work,
		EducationHours: edu,
		SleepHours:     sleep,
		ChoreHours:     chores,
		CommuteHours:   commute,
		OvertimeHours:  overtime,
		Discretionary:  disc,
		LeisureHours:   leisure,
		RestHours:      disc - leisure,
		OvertimeWage:   overtime * cfg.OvertimeWageRate,
	}
}

// commuteHours reads one citizen's weekly door-to-door commute figure from the
// traffic dependency — the single policy shared by DiscretionaryHours and
// Patronage. A nil traffic (the unwired stub default) yields zero (no commute
// term). A CommuteHours error is propagated (never silently zeroed); a
// non-finite or negative figure is treated as unset (zero) rather than leaked
// into budget arithmetic (GR#16).
func commuteHours(traffic TrafficAPI, citizenID uint64, correlationID string) (float64, error) {
	if traffic == nil {
		return 0, nil
	}
	h, err := traffic.CommuteHours(citizenID, correlationID)
	if err != nil {
		return 0, err
	}
	if num.IsFinite(h) && h >= 0 {
		return h, nil
	}
	return 0, nil
}

// DiscretionaryHours computes a citizen's weekly time budget (AC-2). The
// work/sleep/chore baselines come from the citizen's life stage; the commute
// term is read from engine.traffic's door-to-door figure — so two citizens
// identical except for commute differ in Discretionary by exactly the
// commute delta. An unknown citizen returns ErrUnknownCitizen, never a
// silently-zero budget.
func (a *LeisureAPI) DiscretionaryHours(citizenID uint64, correlationID string) (TimeBudget, error) {
	if err := a.checkNotCopied("DiscretionaryHours"); err != nil {
		return TimeBudget{}, err
	}
	a.mu.RLock()
	citizensAPI := a.citizens
	traffic := a.traffic
	cfg := a.cfg
	overtime := a.overtime[citizenID]
	a.mu.RUnlock()

	if citizensAPI == nil {
		return TimeBudget{}, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "DiscretionaryHours", "dependency": "citizens",
		})
	}
	cit, ok := citizensAPI.CitizenAt(citizenID, correlationID)
	if !ok {
		return TimeBudget{}, errs.New(ErrUnknownCitizen, correlationID, map[string]any{
			"citizenId": citizenID,
		})
	}

	commute, err := commuteHours(traffic, citizenID, correlationID)
	if err != nil {
		return TimeBudget{}, err
	}
	return computeBudget(cfg, lifeStageFor(cit), commute, overtime, cit.Leisure), nil
}

// SetOvertimeHours sets a citizen's weekly overtime hours (the player/citizen
// choice driving the overtime trade-off: overtime reduces discretionary
// leisure time but generates OvertimeWage). A non-finite or negative figure
// is rejected (ErrInvalidInput), never silently clamped.
func (a *LeisureAPI) SetOvertimeHours(citizenID uint64, hours float64, correlationID string) error {
	if err := a.checkNotCopied("SetOvertimeHours"); err != nil {
		return err
	}
	if !num.IsFinite(hours) || hours < 0 {
		return errs.New(ErrInvalidInput, correlationID, map[string]any{
			"reason": "overtime hours must be finite and non-negative",
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overtime[citizenID] = hours
	return nil
}
