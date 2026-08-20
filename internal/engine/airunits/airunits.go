package airunits

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// chopper is the internal, mutable per-unit state. Unexported — the only read
// surface is [AirUnitsAPI.Fleet]/[AirUnitsAPI.UnitStatus]'s exported
// UnitStatus snapshots, and no consumer can write a chopper's fields directly.
type chopper struct {
	id     UnitID
	typ    UnitType
	state  FlightState
	reason groundReason // the out-of-service cause (groundNone when in service)
	pilot  PilotID      // 0 = no pilot assigned
	wear   int64        // accumulated wear points (AC-6)
}

// AirUnitsAPI is code.json's "engine.airunits" inbound contract (GUID
// b9658c5f-1d3b-4830-acd3-f3c6f919e66f, AirUnitsAPI, "four typed roles;
// dispatch integration; maintenance/opex fed to finance"): the four distinct
// chopper unit types, the data-driven CAPEX/OPEX economics, pilot gating,
// per-chopper maintenance burden, role-specific effects, the approval weight,
// and traffic-immune/weather-limited routing — all behind this single API.
//
// The zero value is not usable; construct via [New] or [Load]. A
// *AirUnitsAPI is safe for concurrent use: every mutable field is guarded by
// mu, and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020 family, mirroring engine.crime/engine.maintenance).
type AirUnitsAPI struct {
	correlationID string
	seed          uint64
	cfg           config

	mu sync.RWMutex

	fleet        map[UnitID]*chopper
	nextUnitID   UnitID
	currentMonth int64
	weather      Weather

	// commercialRevenue is the aggregate VIP commercial-revenue earned so far,
	// in micro-pounds (AC-8's non-emergency benefit).
	commercialRevenue det.Micropounds

	// Seams (AC-2): each registered outbound edge is consumed through an
	// injected interface, never a concrete sibling-engine import. Nil means
	// "not wired": the corresponding effect is skipped (finance/maintenance/
	// dispatch) or fails closed (purchase, pilot assignment, service).
	finance     FinanceSeam
	staffing    StaffingSeam
	maintenance MaintenanceSeam
	dispatch    DispatchSeam
	world       WorldSeam

	// self is the SEC-020 copy guard, stored exactly once in New before the
	// value is returned to any caller.
	self atomic.Pointer[AirUnitsAPI]
}

// New constructs an AirUnitsAPI from a world seed (carried for the counter-
// based VIP revenue draw — AC-13) and a validated balance config. correlationID
// is attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid config is rejected with a registry-sourced
// error — never a silently-defaulted rate.
func New(seed uint64, cfg config, correlationID string) (*AirUnitsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &AirUnitsAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cloneConfig(cfg),
		fleet:         make(map[UnitID]*chopper),
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *AirUnitsAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *AirUnitsAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// --- seam wiring (AC-2) ---

// SetFinance wires the engine.finance CAPEX/OPEX settlement seam. A nil value
// un-wires it: running-cost settlement is skipped, and Purchase fails closed
// with ErrInsufficientFunds (never a free chopper).
func (a *AirUnitsAPI) SetFinance(f FinanceSeam) error {
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finance = f
	return nil
}

// SetStaffing wires the engine.staffing pilot skill-pool seam (AC-5).
func (a *AirUnitsAPI) SetStaffing(s StaffingSeam) error {
	if err := a.checkNotCopied("SetStaffing"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.staffing = s
	return nil
}

// SetMaintenance wires the engine.maintenance demand seam (AC-6).
func (a *AirUnitsAPI) SetMaintenance(m MaintenanceSeam) error {
	if err := a.checkNotCopied("SetMaintenance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maintenance = m
	return nil
}

// SetDispatch wires the engine.dispatch outcome seam (AC-8).
func (a *AirUnitsAPI) SetDispatch(d DispatchSeam) error {
	if err := a.checkNotCopied("SetDispatch"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dispatch = d
	return nil
}

// SetWorld wires the engine.world weather seam (AC-7, ASM-589).
func (a *AirUnitsAPI) SetWorld(w WorldSeam) error {
	if err := a.checkNotCopied("SetWorld"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.world = w
	return nil
}

// --- commands ---

// Purchase buys one chopper of the given type (AC-3): it validates the type
// and its unlock milestone, posts the type's data-loaded purchase cost as a
// capital (CAPEX) event through the finance seam, and only then creates the
// chopper as Available. A below-milestone purchase, an insufficient treasury,
// or an unknown type is rejected with a registry-sourced error and mutates
// nothing.
func (a *AirUnitsAPI) Purchase(typ UnitType, currentMilestone int64) (UnitID, error) {
	if err := a.checkNotCopied("Purchase"); err != nil {
		return 0, err
	}
	if currentMilestone < 0 {
		return 0, errs.New(ErrInvalidInput, a.correlationID, map[string]any{"field": "currentMilestone", "value": currentMilestone})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	uc, ok := a.cfg.Units[typ]
	if !ok {
		return 0, errs.New(ErrUnknownUnitType, a.correlationID, map[string]any{"unitType": typ.String()})
	}
	if currentMilestone < uc.UnlockMilestone {
		return 0, errs.New(ErrMilestoneLocked, a.correlationID, map[string]any{
			"unitType": typ.String(), "unlockMilestone": uc.UnlockMilestone, "currentMilestone": currentMilestone,
		})
	}
	if a.finance == nil {
		return 0, errs.New(ErrInsufficientFunds, a.correlationID, map[string]any{"reason": "finance seam not wired"})
	}
	if err := a.finance.SettleCapital(uc.PurchaseCost); err != nil {
		return 0, errs.Wrap(ErrInsufficientFunds, a.correlationID, err, map[string]any{
			"unitType": typ.String(), "cost": int64(uc.PurchaseCost),
		})
	}
	a.nextUnitID++
	id := a.nextUnitID
	a.fleet[id] = &chopper{id: id, typ: typ, state: StateAvailable, reason: groundNone}
	return id, nil
}

// AssignPilot assigns a trained pilot to a chopper through the staffing seam
// (AC-5). A chopper may be dispatchable only with a trained pilot; assigning a
// qualified pilot to a chopper grounded for lack of a pilot restores it to
// Available (weather and maintenance permitting). An unqualified citizen, a
// missing staffing seam, a zero pilot, a nonexistent chopper, or a pilot
// already assigned to a different chopper (MOD-074 r1) is rejected with a
// registry-sourced error and mutates nothing.
func (a *AirUnitsAPI) AssignPilot(id UnitID, pilot PilotID) error {
	if err := a.checkNotCopied("AssignPilot"); err != nil {
		return err
	}
	if pilot == 0 {
		return errs.New(ErrInvalidInput, a.correlationID, map[string]any{"field": "pilot", "reason": "pilot id must be non-zero"})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.weather = a.readWeatherLocked()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	if a.staffing == nil {
		return errs.New(ErrUnqualifiedPilot, a.correlationID, map[string]any{"pilot": uint64(pilot), "reason": "staffing seam not wired"})
	}
	qualified, err := a.staffing.PilotQualified(pilot)
	if err != nil {
		return errs.Wrap(ErrUnqualifiedPilot, a.correlationID, err, map[string]any{"pilot": uint64(pilot)})
	}
	if !qualified {
		return errs.New(ErrUnqualifiedPilot, a.correlationID, map[string]any{"pilot": uint64(pilot)})
	}
	// MOD-074 r1: one pilot may never be live on two choppers at once. Reject
	// (fail-closed) a reassignment while the pilot still holds a slot on a
	// DIFFERENT chopper — any state; the slot is held until RemovePilot
	// releases it. Reassigning to the SAME chopper is a harmless idempotent
	// no-op and is allowed.
	if conflict := a.findPilotLocked(pilot, id); conflict != nil {
		return errs.New(ErrPilotAlreadyAssigned, a.correlationID, map[string]any{
			"pilot": uint64(pilot), "unit": uint64(id), "assignedUnit": uint64(conflict.id),
		})
	}
	ch.pilot = pilot
	if ch.state == StateOutOfService && ch.reason == groundNoPilot &&
		ch.wear < a.cfg.Maintenance.OutOfServiceWearThreshold && !a.weatherAdverseLocked() {
		ch.state = StateAvailable
		ch.reason = groundNone
	}
	return nil
}

// RemovePilot removes a chopper's pilot, grounding it (AC-5): no pilot, no
// flight. Removing a pilot from a chopper that has none is a harmless no-op.
func (a *AirUnitsAPI) RemovePilot(id UnitID) error {
	if err := a.checkNotCopied("RemovePilot"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	ch.pilot = 0
	ch.state = StateOutOfService
	ch.reason = groundNoPilot
	return nil
}

// Dispatch sends an available chopper to an incident (EnRoute) (AC-5/AC-7):
// it requires a trained pilot, an in-service (Available) chopper, and calm
// weather. The chopper's role contribution is reported through the dispatch
// seam (AC-8 boundary). A missing pilot, a grounded/out-of-service chopper,
// or adverse weather is rejected with a registry-sourced error and mutates
// nothing.
func (a *AirUnitsAPI) Dispatch(id UnitID) error {
	if err := a.checkNotCopied("Dispatch"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.weather = a.readWeatherLocked()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	if a.weatherAdverseLocked() {
		return errs.New(ErrWeatherGrounded, a.correlationID, map[string]any{"unit": uint64(id), "windKnots": a.weather.WindKnots})
	}
	if ch.state != StateAvailable {
		return errs.New(ErrGroundedDispatch, a.correlationID, map[string]any{"unit": uint64(id), "state": ch.state.String()})
	}
	if ch.pilot == 0 {
		return errs.New(ErrNoPilot, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	ch.state = StateEnRoute
	if a.dispatch != nil {
		_ = a.dispatch.ReportContribution(ch.id, ch.typ, a.roleEffectLocked(ch.typ))
	}
	return nil
}

// ArriveOnScene advances an en-route chopper to OnScene (AC-10's dispatch
// lifecycle). A chopper not currently en-route is rejected.
func (a *AirUnitsAPI) ArriveOnScene(id UnitID) error {
	if err := a.checkNotCopied("ArriveOnScene"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	if ch.state != StateEnRoute {
		return errs.New(ErrGroundedDispatch, a.correlationID, map[string]any{"unit": uint64(id), "state": ch.state.String()})
	}
	ch.state = StateOnScene
	return nil
}

// ReleaseFromScene returns an on-scene chopper to base (Available) (AC-10's
// dispatch lifecycle). A chopper not currently on-scene is rejected.
func (a *AirUnitsAPI) ReleaseFromScene(id UnitID) error {
	if err := a.checkNotCopied("ReleaseFromScene"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	if ch.state != StateOnScene {
		return errs.New(ErrGroundedDispatch, a.correlationID, map[string]any{"unit": uint64(id), "state": ch.state.String()})
	}
	ch.state = StateAvailable
	return nil
}

// Service applies engineer-hours of maintenance to one chopper through the
// maintenance seam (AC-6), clearing wear by whatever the seam returns. A
// chopper grounded for maintenance returns to Available once its wear drops
// below the out-of-service threshold (pilot and weather permitting). A missing
// maintenance seam, a nonexistent chopper, or a negative hour count is
// rejected with a registry-sourced error and mutates nothing.
func (a *AirUnitsAPI) Service(id UnitID, engineerHours int64) error {
	if err := a.checkNotCopied("Service"); err != nil {
		return err
	}
	if engineerHours < 0 {
		return errs.New(ErrInvalidInput, a.correlationID, map[string]any{"field": "engineerHours", "value": engineerHours})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.weather = a.readWeatherLocked()
	ch := a.fleet[id]
	if ch == nil {
		return errs.New(ErrUnknownUnit, a.correlationID, map[string]any{"unit": uint64(id)})
	}
	if a.maintenance == nil {
		return errs.New(ErrInvalidInput, a.correlationID, map[string]any{"reason": "maintenance seam not wired"})
	}
	cleared, err := a.maintenance.Service(id, engineerHours)
	if err != nil {
		return err
	}
	ch.wear = num.SatSub(ch.wear, cleared)
	if ch.wear < 0 {
		ch.wear = 0
	}
	if ch.state == StateOutOfService && ch.reason == groundMaintenance &&
		ch.wear < a.cfg.Maintenance.OutOfServiceWearThreshold && ch.pilot != 0 && !a.weatherAdverseLocked() {
		ch.state = StateAvailable
		ch.reason = groundNone
	}
	return nil
}

// AdvanceMonth advances the whole module one simulated month (AC-13): for each
// chopper in sorted UnitID order it posts the running-cost components to the
// finance seam (hangar/insurance/crew always; fuel additionally while flying),
// accrues flight wear (and VIP commercial revenue), surfaces the engineer-hour
// burden to the maintenance seam, and applies the grounding scheduler
// (maintenance wear, adverse weather). It is the single tick mutation path —
// accessors never mutate.
func (a *AirUnitsAPI) AdvanceMonth(month int64) error {
	if err := a.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}
	if month < 0 {
		return errs.New(ErrInvalidInput, a.correlationID, map[string]any{"field": "month", "value": month})
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.currentMonth = month
	a.weather = a.readWeatherLocked()
	var firstErr error
	threshold := a.cfg.Maintenance.OutOfServiceWearThreshold

	for _, id := range a.sortedUnitIDsLocked() {
		ch := a.fleet[id]
		wasFlying := ch.state == StateEnRoute || ch.state == StateOnScene

		// Running-cost components post independently (AC-4): standing
		// components always, fuel only while flying.
		if a.finance != nil {
			for _, post := range a.opexPostsLocked(ch.typ, wasFlying) {
				if err := a.finance.SettleOpex(post); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}

		if wasFlying {
			ch.wear = num.SatAdd(ch.wear, a.cfg.Maintenance.WearPerFlightCycle)
			if ch.typ == UnitVIP {
				a.commercialRevenue = det.Micropounds(num.SatAdd(int64(a.commercialRevenue), a.vipRevenueLocked(ch.id, month)))
			}
		}

		// Maintenance demand surface (AC-6) — never a chopper-local ledger.
		if a.maintenance != nil {
			hours, _ := num.SafeMul(ch.wear, a.cfg.Maintenance.EngineerHoursPerWearPoint)
			if err := a.maintenance.ReportDemand(ch.id, hours); err != nil && firstErr == nil {
				firstErr = err
			}
		}

		// Grounding scheduler (AC-10): maintenance wear, then adverse weather.
		if ch.wear >= threshold {
			ch.state = StateOutOfService
			ch.reason = groundMaintenance
		} else if wasFlying && a.weatherAdverseLocked() {
			ch.state = StateOutOfService
			ch.reason = groundWeather
		} else if ch.state == StateOutOfService && ch.reason == groundWeather && !a.weatherAdverseLocked() && ch.pilot != 0 {
			// Weather cleared: a weather-grounded chopper returns to base.
			ch.state = StateAvailable
			ch.reason = groundNone
		}
	}

	return firstErr
}

// --- queries ---

// Fleet returns a sorted snapshot of every chopper (AC-2). Deterministic:
// ascending UnitID order, never map iteration order (GR#21).
func (a *AirUnitsAPI) Fleet() []UnitStatus {
	if err := a.checkNotCopied("Fleet"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]UnitStatus, 0, len(a.fleet))
	for _, id := range a.sortedUnitIDsLocked() {
		out = append(out, a.unitStatusLocked(a.fleet[id]))
	}
	return out
}

// UnitStatus returns one chopper's snapshot and whether it exists (AC-2).
func (a *AirUnitsAPI) UnitStatus(id UnitID) (UnitStatus, bool, error) {
	if err := a.checkNotCopied("UnitStatus"); err != nil {
		return UnitStatus{}, false, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ch := a.fleet[id]
	if ch == nil {
		return UnitStatus{}, false, nil
	}
	return a.unitStatusLocked(ch), true, nil
}

// FleetCounts returns the AC-10 fleet-conservation snapshot. Each of the four
// terms is counted independently from the fleet's per-unit state — none is
// derived as a remainder of the others.
func (a *AirUnitsAPI) FleetCounts() FleetCounts {
	if err := a.checkNotCopied("FleetCounts"); err != nil {
		return FleetCounts{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	c := FleetCounts{Total: len(a.fleet)}
	for _, ch := range a.fleet {
		switch ch.state {
		case StateAvailable:
			c.Available++
		case StateEnRoute:
			c.EnRoute++
		case StateOnScene:
			c.OnScene++
		case StateOutOfService:
			c.OutOfService++
		}
	}
	return c
}

// TotalChoppers returns the total number of purchased choppers (AC-10's
// left-hand side).
func (a *AirUnitsAPI) TotalChoppers() int {
	if err := a.checkNotCopied("TotalChoppers"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.fleet)
}

// RoleEffect returns a role's data-driven effect contribution (AC-2/AC-8).
func (a *AirUnitsAPI) RoleEffect(typ UnitType) (RoleEffect, error) {
	if err := a.checkNotCopied("RoleEffect"); err != nil {
		return RoleEffect{}, err
	}
	uc, ok := a.cfg.Units[typ]
	if !ok {
		return RoleEffect{}, errs.New(ErrUnknownUnitType, a.correlationID, map[string]any{"unitType": typ.String()})
	}
	return uc.Effect, nil
}

// PoliceEffect returns the police chopper's coverage-radius extension (AC-8).
func (a *AirUnitsAPI) PoliceEffect() (int64, error) {
	re, err := a.RoleEffect(UnitPolice)
	if err != nil {
		return 0, err
	}
	return re.CoverageRadiusExtension, nil
}

// FireEffect returns the fire chopper's remote/blaze reach bonus (AC-8).
func (a *AirUnitsAPI) FireEffect() (int64, error) {
	re, err := a.RoleEffect(UnitFire)
	if err != nil {
		return 0, err
	}
	return re.RemoteFireReachBonus, nil
}

// AmbulanceEffect returns the air ambulance's hospital-landing-time reduction,
// in simulation-minutes (AC-8).
func (a *AirUnitsAPI) AmbulanceEffect() (int64, error) {
	re, err := a.RoleEffect(UnitAmbulance)
	if err != nil {
		return 0, err
	}
	return re.HospitalLandingReductionMinutes, nil
}

// VIPEffect returns the VIP chopper's commercial revenue per month, in
// micro-pounds (AC-8's non-emergency benefit).
func (a *AirUnitsAPI) VIPEffect() (int64, error) {
	re, err := a.RoleEffect(UnitVIP)
	if err != nil {
		return 0, err
	}
	return re.CommercialRevenuePerMonth, nil
}

// RunningCostFor returns a type's per-month running-cost breakdown (AC-4): the
// four named components fuel, hangar, insurance, crew.
func (a *AirUnitsAPI) RunningCostFor(typ UnitType) (RunningCost, error) {
	if err := a.checkNotCopied("RunningCostFor"); err != nil {
		return RunningCost{}, err
	}
	uc, ok := a.cfg.Units[typ]
	if !ok {
		return RunningCost{}, errs.New(ErrUnknownUnitType, a.correlationID, map[string]any{"unitType": typ.String()})
	}
	return RunningCost{Fuel: uc.Fuel, Hangar: uc.Hangar, Insurance: uc.Insurance, Crew: uc.Crew}, nil
}

// ApprovalWeight returns the fleet's population-approval contribution (AC-9):
// the data-loaded weight per actively-flying (response-time-cutting) chopper.
// A grounded, weather-grounded, or out-of-service chopper contributes zero.
func (a *AirUnitsAPI) ApprovalWeight() int64 {
	if err := a.checkNotCopied("ApprovalWeight"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var flying int64
	for _, ch := range a.fleet {
		if (ch.state == StateEnRoute || ch.state == StateOnScene) && ch.pilot != 0 {
			flying++
		}
	}
	weight, _ := num.SafeMul(flying, a.cfg.Approval.ApprovalWeightPerActiveChopper)
	return weight
}

// CommercialRevenue returns the aggregate VIP commercial revenue earned so
// far, in micro-pounds (AC-8's non-emergency benefit).
func (a *AirUnitsAPI) CommercialRevenue() det.Micropounds {
	if err := a.checkNotCopied("CommercialRevenue"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.commercialRevenue
}

// AirTravelTimeMinutes computes a chopper's road-independent travel time to a
// point at the given distance (AC-7): distance × the data-loaded air speed.
// It is congestion-immune by construction — no ground-path or congestion term
// enters the computation.
func (a *AirUnitsAPI) AirTravelTimeMinutes(distance int64) (int64, error) {
	if err := a.checkNotCopied("AirTravelTimeMinutes"); err != nil {
		return 0, err
	}
	if distance < 0 {
		return 0, errs.New(ErrInvalidInput, a.correlationID, map[string]any{"field": "distance", "value": distance})
	}
	t, _ := num.SafeMul(distance, a.cfg.Travel.AirSpeedMinutesPerUnit)
	return t, nil
}

// WeatherGrounded reports whether the given weather grounds or degrades air
// dispatch (AC-7): wind speed at or above the data-loaded grounding threshold.
func (a *AirUnitsAPI) WeatherGrounded(w Weather) bool {
	if err := a.checkNotCopied("WeatherGrounded"); err != nil {
		return false
	}
	return w.WindKnots >= a.cfg.Weather.GroundingWindKnots
}

// --- internal helpers (all require a.mu held) ---

// readWeatherLocked reads the current weather from the world seam. A nil seam
// means calm weather (no grounding).
func (a *AirUnitsAPI) readWeatherLocked() Weather {
	if a.world == nil {
		return Weather{}
	}
	return a.world.CurrentWeather()
}

// weatherAdverseLocked reports whether the cached weather grounds air dispatch.
func (a *AirUnitsAPI) weatherAdverseLocked() bool {
	return a.weather.WindKnots >= a.cfg.Weather.GroundingWindKnots
}

// opexPostsLocked returns the running-cost components to post for a type this
// month, in a fixed order (hangar, insurance, crew, fuel) — fuel only while
// flying (AC-4). Fixed slice order, never map iteration (GR#21).
func (a *AirUnitsAPI) opexPostsLocked(typ UnitType, flying bool) []det.Micropounds {
	uc := a.cfg.Units[typ]
	posts := []det.Micropounds{uc.Hangar, uc.Insurance, uc.Crew}
	if flying {
		posts = append(posts, uc.Fuel)
	}
	return posts
}

// roleEffectLocked returns a type's role effect from config.
func (a *AirUnitsAPI) roleEffectLocked(typ UnitType) RoleEffect {
	return a.cfg.Units[typ].Effect
}

// vipRevenueLocked draws this month's VIP commercial revenue for one chopper
// (AC-13): a counter-based hash(worldSeed, unitID, month, purpose) draw in
// [0, data-loaded max], never a shared/global RNG (GR#21).
func (a *AirUnitsAPI) vipRevenueLocked(id UnitID, month int64) int64 {
	max := a.cfg.Units[UnitVIP].Effect.CommercialRevenuePerMonth
	if max <= 0 {
		return 0
	}
	s := det.NewStream(a.seed, uint64(id), month, "airunits.vip-revenue")
	return s.IntN(max + 1)
}

// unitStatusLocked snapshots one chopper into its queryable UnitStatus.
func (a *AirUnitsAPI) unitStatusLocked(ch *chopper) UnitStatus {
	return UnitStatus{
		ID:       ch.id,
		Type:     ch.typ,
		State:    ch.state,
		Pilot:    ch.pilot,
		Wear:     ch.wear,
		Flying:   ch.state == StateEnRoute || ch.state == StateOnScene,
		Grounded: ch.state == StateOutOfService,
	}
}

// findPilotLocked returns the chopper other than exclude that currently holds
// the given pilot, or nil if none. The caller holds a.mu. Iteration is in
// sorted UnitID order (deterministic, GR#21); the invariant this enforces
// (one pilot on one unit) guarantees at most one match, so the returned
// conflict is stable.
func (a *AirUnitsAPI) findPilotLocked(pilot PilotID, exclude UnitID) *chopper {
	if pilot == 0 {
		return nil
	}
	for _, id := range a.sortedUnitIDsLocked() {
		c := a.fleet[id]
		if c.id != exclude && c.pilot == pilot {
			return c
		}
	}
	return nil
}

// sortedUnitIDsLocked returns the fleet's chopper ids in ascending order
// (deterministic — never map-iteration order, GR#21). The caller holds a.mu.
func (a *AirUnitsAPI) sortedUnitIDsLocked() []UnitID {
	ids := make([]UnitID, 0, len(a.fleet))
	for id := range a.fleet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
