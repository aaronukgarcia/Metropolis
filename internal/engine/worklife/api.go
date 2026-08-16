package worklife

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// WorkScheduleAPI is code.json's "engine.worklife" inbound contract
// (GUID 7069c291-d733-4ce3-9204-525be1325267, "WorkScheduleAPI"): the
// pattern vocabulary (core-hours/shift/any-time), the at-work query, the
// per-role coverage-requirement query, the commute-demand-by-hour query,
// and the working-hours query that reflects the active working-week policy.
// It consumes engine.policies and engine.wellbeing through their registered
// contracts alone (GR#20), and it feeds engine.staffing (coverage) and
// engine.traffic (commute demand) as pure outputs — it owns no traffic
// assignment and no happiness arithmetic (AC-7/AC-12).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *WorkScheduleAPI is safe for concurrent use (AC-20); a method call on a
// struct-copied value is rejected (SEC-020 family).
type WorkScheduleAPI struct {
	correlationID string
	seed          uint64
	cfg           WorklifeFile
	patterns      map[PatternKind]PatternDef

	// Dependencies, wired via SetPolicies/SetWellbeing and read under mu.
	policies  PoliciesAPI
	wellbeing WellbeingAPI

	mu sync.RWMutex

	// self is the SEC-020 copy guard, stored exactly once before the value
	// is returned to any caller.
	self atomic.Pointer[WorkScheduleAPI]
}

// New constructs a WorkScheduleAPI from a schema-validated WorklifeFile and
// a world seed (used for the deterministic rotation / flexible-placement
// hash, GR#21). The policies/wellbeing seams start unwired; wire them with
// SetPolicies / SetWellbeing. correlationID is attached to every error the
// returned API constructs (GR#1); an empty one mints a fresh ID.
func New(cfg WorklifeFile, seed uint64, correlationID string) (*WorkScheduleAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{"cause": err.Error()})
	}
	patterns := make(map[PatternKind]PatternDef, len(cfg.Patterns))
	for _, p := range cfg.Patterns {
		patterns[PatternKind(p.ID)] = p
	}
	w := &WorkScheduleAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		patterns:      patterns,
	}
	w.self.Store(w)
	return w, nil
}

// Load reads and validates data/worklife.json (via LoadWorklife) and
// returns a ready *WorkScheduleAPI. Every failure is a registry-sourced
// *errs.E.
func Load(dir string, seed uint64, correlationID string) (*WorkScheduleAPI, error) {
	f, err := LoadWorklife(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(f, seed, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers that don't already have a resolved data directory in hand.
func LoadDefault(seed uint64, correlationID string) (*WorkScheduleAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, seed, correlationID)
}

// SetPolicies wires the engine.policies seam (the working-week effect
// source, AC-8).
func (w *WorkScheduleAPI) SetPolicies(p PoliciesAPI) error {
	if err := w.checkNotCopied("SetPolicies"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.policies = p
	return nil
}

// SetWellbeing wires the engine.wellbeing seam (the overwork input push,
// AC-12).
func (w *WorkScheduleAPI) SetWellbeing(b WellbeingAPI) error {
	if err := w.checkNotCopied("SetWellbeing"); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wellbeing = b
	return nil
}

// checkNotCopied rejects a method call on a struct-copied *WorkScheduleAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load.
func (w *WorkScheduleAPI) checkNotCopied(method string) error {
	if w.self.Load() != w {
		return errs.New(ErrCopiedValue, w.correlationID, map[string]any{"method": method})
	}
	return nil
}

// pattern returns the loaded PatternDef for kind, or a registry-sourced
// ErrUnknownPattern if kind is not one of the three documented kinds
// (AC-14) — never a silently-invented "never at work" profile.
func (w *WorkScheduleAPI) pattern(kind PatternKind) (PatternDef, error) {
	p, ok := w.patterns[kind]
	if !ok {
		return PatternDef{}, errs.New(ErrUnknownPattern, w.correlationID, map[string]any{"kind": string(kind)})
	}
	return p, nil
}

// AtWork reports whether worker is at work at the absolute simulation-hour
// tick (AC-4). It is a pure, deterministic function of the worker's pattern
// and the tick: a CoreHours worker is at work only within the core window
// on a working day; a Shift worker is at work on exactly one of the
// rotations (derived deterministically from worker ID and week index,
// AC-5); an AnyTime worker is at work in a deterministic flexible window
// within their weekly hours.
func (w *WorkScheduleAPI) AtWork(worker Worker, tick int64) (bool, error) {
	if err := w.checkNotCopied("AtWork"); err != nil {
		return false, err
	}
	p, err := w.pattern(worker.Pattern)
	if err != nil {
		return false, err
	}
	if dayOfWeek(tick) >= p.DaysPerWeek {
		return false, nil // not a working day
	}
	h := hourOfDay(tick)
	switch worker.Pattern {
	case PatternCoreHours:
		return h >= p.StartHour && h < p.EndHour, nil
	case PatternShift:
		if len(p.Rotations) == 0 {
			return false, errs.New(ErrDataInvalid, w.correlationID, map[string]any{"kind": string(worker.Pattern), "rule": "no rotations"})
		}
		rot := w.rotation(worker.ID, weekIndex(tick), p.Rotations)
		return h >= rot.StartHour && h < rot.EndHour, nil
	case PatternAnyTime:
		start := w.flexStart(worker.ID, dayIndex(tick), p)
		return h >= start && h < start+p.HoursPerDay, nil
	default:
		return false, errs.New(ErrUnknownPattern, w.correlationID, map[string]any{"kind": string(worker.Pattern)})
	}
}

// CoverageRequirement returns the number of workers that must be rostered
// for a role of kind to keep perHourOnDuty workers on duty at every hour
// the role must be staffed (AC-3). It is a pure function of the pattern's
// data-driven coverage span and daily hours: a Shift role spans all 24
// hours while a CoreHours role spans only the working day, so for the same
// per-hour demand the Shift role's required headcount is strictly greater.
// Arithmetic routes through num's saturating helpers (GR#16/AC-18).
func (w *WorkScheduleAPI) CoverageRequirement(kind PatternKind, perHourOnDuty int64) (int64, error) {
	if err := w.checkNotCopied("CoverageRequirement"); err != nil {
		return 0, err
	}
	if perHourOnDuty < 0 {
		return 0, errs.New(ErrInvalidHours, w.correlationID, map[string]any{"perHourOnDuty": perHourOnDuty})
	}
	p, err := w.pattern(kind)
	if err != nil {
		return 0, err
	}
	demand, overflow := num.SafeMul(perHourOnDuty, p.CoverageSpanHours)
	if overflow {
		return 0, errs.New(ErrInvalidHours, w.correlationID, map[string]any{
			"perHourOnDuty": perHourOnDuty, "coverageSpanHours": p.CoverageSpanHours, "rule": "overflow",
		})
	}
	// headcount = ceil(demand / hoursPerDay), via integer ceil division.
	numerator := num.SatAdd(demand, p.HoursPerDay-1)
	return numerator / p.HoursPerDay, nil
}

// WorkingHours returns the weekly working hours for a role of kind under
// the active working-week policy (AC-1/AC-9): the policy's hours when one
// is enacted, else the data-driven default (hoursPerDay × daysPerWeek).
func (w *WorkScheduleAPI) WorkingHours(kind PatternKind, correlationID string) (int64, error) {
	if err := w.checkNotCopied("WorkingHours"); err != nil {
		return 0, err
	}
	p, err := w.pattern(kind)
	if err != nil {
		return 0, err
	}
	effect, active, err := w.activeEffect(correlationID)
	if err != nil {
		return 0, err
	}
	if active {
		return effect.HoursPerWeek, nil
	}
	hours, overflow := num.SafeMul(p.HoursPerDay, p.DaysPerWeek)
	if overflow {
		return 0, errs.New(ErrInvalidHours, w.correlationID, map[string]any{"rule": "default hours overflow"})
	}
	return hours, nil
}

// WeeklyWage returns a worker's weekly wage under the active policy
// (AC-11): baseWage × the policy's wage coefficient (1.0 default, >1 for
// 996). The coefficient application routes through num's safe-coercion path
// (GR#16/AC-18), never a raw untrusted float multiply.
func (w *WorkScheduleAPI) WeeklyWage(baseWage int64, correlationID string) (int64, error) {
	if err := w.checkNotCopied("WeeklyWage"); err != nil {
		return 0, err
	}
	effect, active, err := w.activeEffect(correlationID)
	if err != nil {
		return 0, err
	}
	if !active {
		return baseWage, nil
	}
	scaled := float64(baseWage) * effect.WageCoefficient
	if !num.IsFinite(scaled) {
		return 0, errs.New(ErrNonFiniteInput, w.correlationID, map[string]any{
			"baseWage": baseWage, "wageCoefficient": effect.WageCoefficient,
		})
	}
	return num.ClampInt64FromFloat(scaled), nil
}

// OverworkWellbeingInput computes the overwork/work-life balance input for
// worker under the active policy and pushes it through the WellbeingAPI
// seam, returning the value pushed (AC-12). The balance is the §42
// discretionary-hours fraction (168 − workHours)/168 minus the policy's
// overwork weight; higher work hours yield a strictly lower value, so a
// 996 worker's balance is strictly below a default-week worker's. The wage
// gain and this wellbeing cost come from the SAME policy effect (AC-13):
// more hours always both raises the wage and lowers the balance.
func (w *WorkScheduleAPI) OverworkWellbeingInput(worker Worker, correlationID string) (float64, error) {
	if err := w.checkNotCopied("OverworkWellbeingInput"); err != nil {
		return 0, err
	}
	p, err := w.pattern(worker.Pattern)
	if err != nil {
		return 0, err
	}
	effect, active, err := w.activeEffect(correlationID)
	if err != nil {
		return 0, err
	}
	hours := p.HoursPerDay * p.DaysPerWeek // data-validated: <= 24*7
	if active {
		hours = effect.HoursPerWeek
	}
	balance := float64(int64(hoursPerWeek)-hours) / float64(int64(hoursPerWeek))
	if active {
		balance -= effect.WellbeingWeight
	}
	balance, ok := num.GuardFinite(balance)
	if !ok {
		return 0, errs.New(ErrNonFiniteInput, w.correlationID, map[string]any{"balance": balance})
	}

	w.mu.RLock()
	wellbeing := w.wellbeing
	w.mu.RUnlock()
	if wellbeing == nil {
		return 0, errs.New(ErrDependencyMissing, w.correlationID, map[string]any{
			"dependency": "wellbeing", "operation": "OverworkWellbeingInput",
		})
	}
	if err := wellbeing.PushWorkLifeBalance(worker.ID, balance, correlationID); err != nil {
		return 0, err
	}
	return balance, nil
}

// HourDemand is one absolute simulation hour's commute demand: the count of
// workers arriving at shift-start and departing at shift-end during that
// hour (AC-6/AC-7).
type HourDemand struct {
	Hour       int64
	Arrivals   int64
	Departures int64
}

// CommuteDemandByHour returns the schedule-driven per-hour commute demand
// for workers over the half-open window [startTick, endTick) (AC-6/AC-7):
// every employed worker arrives at their pattern's shift-start and departs
// at shift-end, so the distribution across the day has a peak at core-hours
// start rather than a flat daily average. The result is sorted by hour
// (GR#21 — the internal map is only a scratch index, never the iteration
// source for the result). It contains no traffic-assignment, routing, or
// travel-time logic (AC-7): that is engine.traffic's (MOD-023, deferred).
func (w *WorkScheduleAPI) CommuteDemandByHour(workers []Worker, startTick, endTick int64) ([]HourDemand, error) {
	if err := w.checkNotCopied("CommuteDemandByHour"); err != nil {
		return nil, err
	}
	if startTick < 0 || endTick < startTick {
		return nil, errs.New(ErrInvalidHours, w.correlationID, map[string]any{"startTick": startTick, "endTick": endTick})
	}

	acc := make(map[int64]HourDemand)
	startDay := dayIndex(startTick)
	endDay := dayIndex(endTick - 1)
	for _, worker := range workers {
		p, err := w.pattern(worker.Pattern)
		if err != nil {
			return nil, err
		}
		for d := startDay; d <= endDay; d++ {
			if d%daysPerWeek >= p.DaysPerWeek {
				continue // not a working day
			}
			arrive, depart := w.dayShift(worker, p, d)
			if arrive >= startTick && arrive < endTick {
				e := acc[arrive]
				e.Arrivals++
				acc[arrive] = e
			}
			if depart >= startTick && depart < endTick {
				e := acc[depart]
				e.Departures++
				acc[depart] = e
			}
		}
	}

	hours := make([]int64, 0, len(acc))
	for h := range acc {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })

	out := make([]HourDemand, 0, len(hours))
	for _, h := range hours {
		e := acc[h]
		e.Hour = h
		out = append(out, e)
	}
	return out, nil
}

// activeEffect reads the active working-week policy through the PoliciesAPI
// seam, validating the returned effect's boundary values (AC-8/AC-14).
// ok=false (or an unwired seam) means the default week.
func (w *WorkScheduleAPI) activeEffect(correlationID string) (WorkingWeekEffect, bool, error) {
	w.mu.RLock()
	policies := w.policies
	w.mu.RUnlock()
	if policies == nil {
		return WorkingWeekEffect{}, false, nil
	}
	effect, active, err := policies.ActiveWorkingWeek(correlationID)
	if err != nil {
		return WorkingWeekEffect{}, false, err
	}
	if !active {
		return WorkingWeekEffect{}, false, nil
	}
	if err := validateEffect(effect, correlationID); err != nil {
		return WorkingWeekEffect{}, false, err
	}
	return effect, true, nil
}

// validateEffect validates a policy effect's boundary values (GR#16): hours
// positive and <= 168/week, wage coefficient finite and >= 1, wellbeing
// weight finite and >= 0. Non-finite or out-of-domain values are rejected
// rather than propagated (SEC-093 / AC-14).
func validateEffect(effect WorkingWeekEffect, correlationID string) error {
	if effect.HoursPerWeek <= 0 || effect.HoursPerWeek > int64(hoursPerWeek) {
		return errs.New(ErrInvalidHours, correlationID, map[string]any{"hoursPerWeek": effect.HoursPerWeek})
	}
	if !num.IsFinite(effect.WageCoefficient) || !num.IsFinite(effect.WellbeingWeight) {
		return errs.New(ErrNonFiniteInput, correlationID, map[string]any{
			"wageCoefficient": effect.WageCoefficient, "wellbeingWeight": effect.WellbeingWeight,
		})
	}
	if effect.WageCoefficient < 1 {
		return errs.New(ErrInvalidHours, correlationID, map[string]any{"wageCoefficient": effect.WageCoefficient})
	}
	if effect.WellbeingWeight < 0 {
		return errs.New(ErrInvalidHours, correlationID, map[string]any{"wellbeingWeight": effect.WellbeingWeight})
	}
	return nil
}

// rotation deterministically assigns a shift worker their rotation for a
// week (AC-5): a pure hash of (seed, workerID, weekIndex) via foundation/
// det's counter-based stream, never map-iteration order.
func (w *WorkScheduleAPI) rotation(workerID uint64, wk int64, rots []RotationDef) RotationDef {
	if len(rots) == 0 {
		return RotationDef{}
	}
	draw := det.NewStream(w.seed, workerID, wk, "worklife-rotation").At(0)
	return rots[int(draw%uint64(len(rots)))]
}

// flexStart deterministically places an AnyTime worker's flexible window
// (AC-5): a pure hash of (seed, workerID, dayIndex) via foundation/det's
// counter-based stream, never map-iteration order. The window is
// hoursPerDay long and fits within the day.
func (w *WorkScheduleAPI) flexStart(workerID uint64, d int64, p PatternDef) int64 {
	span := int64(hoursPerDay) - p.HoursPerDay
	if span <= 0 {
		return 0
	}
	draw := det.NewStream(w.seed, workerID, d, "worklife-flextime").At(0)
	return int64(draw % uint64(span))
}

// dayShift returns the absolute arrive/depart ticks for worker on day d (d
// is an absolute day index), used by CommuteDemandByHour.
func (w *WorkScheduleAPI) dayShift(worker Worker, p PatternDef, d int64) (arrive, depart int64) {
	base := d * int64(hoursPerDay)
	switch worker.Pattern {
	case PatternCoreHours:
		return base + p.StartHour, base + p.EndHour
	case PatternShift:
		rot := w.rotation(worker.ID, d/daysPerWeek, p.Rotations)
		return base + rot.StartHour, base + rot.EndHour
	default: // PatternAnyTime
		start := w.flexStart(worker.ID, d, p)
		return base + start, base + start + p.HoursPerDay
	}
}
