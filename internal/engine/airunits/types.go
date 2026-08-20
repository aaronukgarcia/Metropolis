package airunits

import "github.com/aaronukgarcia/Metropolis/internal/foundation/det"

// UnitType enumerates the four distinct rotary-wing unit types (AC-1): police,
// fire, ambulance, and VIP/commercial. Each is a distinct typed identity, not
// a role string carried on one shared struct — a chopper's role is its
// [UnitType], and each type resolves to a distinct effect (see [RoleEffect]).
type UnitType uint8

const (
	// UnitPolice is the police chopper: extends patrol coverage/deterrence.
	UnitPolice UnitType = iota
	// UnitFire is the fire chopper: reaches remote/blaze sites without traffic.
	UnitFire
	// UnitAmbulance is the air ambulance: reduces hospital-landing time.
	UnitAmbulance
	// UnitVIP is the VIP/commercial chopper: a non-emergency prestige asset.
	UnitVIP
)

// String returns the canonical type key (also data/helicopters.json's
// "units" element). Log/debug use — never the GR#1 user-visible error text.
func (t UnitType) String() string {
	switch t {
	case UnitPolice:
		return "police"
	case UnitFire:
		return "fire"
	case UnitAmbulance:
		return "ambulance"
	case UnitVIP:
		return "vip"
	default:
		return "unknown"
	}
}

// UnitTypes is the fixed registry of the four distinct unit types, ordered by
// enum value — used wherever a deterministic iteration over all types is
// required (never a map range on the tick path, GR#21).
var UnitTypes = []UnitType{UnitPolice, UnitFire, UnitAmbulance, UnitVIP}

// resolveTypeKey maps a data/helicopters.json "units" key to its UnitType,
// reporting whether the key is a recognised type (AC-12's unrecognised-type
// rejection). The mapping is a fixed switch, never a string-keyed map iteration.
func resolveTypeKey(key string) (UnitType, bool) {
	switch key {
	case "police":
		return UnitPolice, true
	case "fire":
		return UnitFire, true
	case "ambulance":
		return UnitAmbulance, true
	case "vip":
		return UnitVIP, true
	default:
		return 0, false
	}
}

// EffectKind enumerates the four role-specific effect paths (AC-8). The four
// roles resolve to four DISTINCT effects — a single shared "response-time
// bonus" applied identically to all four is exactly what this is designed to
// prevent.
type EffectKind uint8

const (
	// EffectPoliceCoverage: coverage-radius extension (deterrence/response).
	EffectPoliceCoverage EffectKind = iota
	// EffectFireReach: remote/blaze reach bonus (fire-spread/block-loss
	// reduction).
	EffectFireReach
	// EffectAmbulanceLanding: hospital-landing-time reduction.
	EffectAmbulanceLanding
	// EffectVIPCommercial: commercial revenue (a non-emergency benefit).
	EffectVIPCommercial
)

// String returns the effect kind's canonical name.
func (k EffectKind) String() string {
	switch k {
	case EffectPoliceCoverage:
		return "coverage-radius-extension"
	case EffectFireReach:
		return "remote-fire-reach"
	case EffectAmbulanceLanding:
		return "hospital-landing-reduction"
	case EffectVIPCommercial:
		return "commercial-revenue"
	default:
		return "unknown"
	}
}

// effectKindFor maps a unit type to its distinct effect path (AC-1). A fixed
// switch, never a switch on a role string.
func effectKindFor(t UnitType) EffectKind {
	switch t {
	case UnitPolice:
		return EffectPoliceCoverage
	case UnitFire:
		return EffectFireReach
	case UnitAmbulance:
		return EffectAmbulanceLanding
	case UnitVIP:
		return EffectVIPCommercial
	default:
		return EffectKind(0xFF)
	}
}

// RoleEffect is the data-driven effect contribution of one role (AC-8).
// Exactly one field is meaningful per role, selected by Kind; the other three
// read as zero. The units are documented per field and restated in
// data/helicopters.json's meta block (AC-17).
type RoleEffect struct {
	Kind EffectKind

	// CoverageRadiusExtension is the police chopper's coverage-radius
	// extension, in coverage units.
	CoverageRadiusExtension int64

	// RemoteFireReachBonus is the fire chopper's remote/blaze reach bonus, in
	// block-loss-reduction units.
	RemoteFireReachBonus int64

	// HospitalLandingReductionMinutes is the air ambulance's hospital-landing
	// time reduction, in simulation-minutes.
	HospitalLandingReductionMinutes int64

	// CommercialRevenuePerMonth is the VIP chopper's commercial revenue, in
	// micro-pounds per month (a non-emergency prestige/commercial benefit).
	CommercialRevenuePerMonth int64
}

// UnitID identifies one purchased chopper (a stable, queryable identity —
// AC-10 fleet conservation).
type UnitID uint64

// PilotID identifies a pilot (a citizen ID from engine.staffing's skill pool).
type PilotID uint64

// FlightState is a chopper's per-tick disposition. The four states are the
// AC-10 conservation identity's four right-hand terms; each is counted
// independently from the fleet's per-unit state (never as a remainder).
type FlightState uint8

const (
	// StateAvailable: parked at base, pilot assigned, dispatchable.
	StateAvailable FlightState = iota
	// StateEnRoute: flying to an incident (a dispatch assignment).
	StateEnRoute
	// StateOnScene: on scene at an incident.
	StateOnScene
	// StateOutOfService: grounded by maintenance wear, pilot removal, or
	// adverse weather.
	StateOutOfService
)

// String returns the flight state's canonical name.
func (s FlightState) String() string {
	switch s {
	case StateAvailable:
		return "available"
	case StateEnRoute:
		return "en-route"
	case StateOnScene:
		return "on-scene"
	case StateOutOfService:
		return "out-of-service"
	default:
		return "unknown"
	}
}

// groundReason is the drill-through cause of an out-of-service grounding
// (AC-10's "maintenance/pilot/weather grounding scheduler").
type groundReason uint8

const (
	groundNone groundReason = iota
	groundMaintenance
	groundNoPilot
	groundWeather
)

// Weather is the adverse-weather read the chopper must still respect (AC-7,
// ASM-589). A wind speed at or above the data-loaded grounding threshold
// grounds air dispatch.
type Weather struct {
	WindKnots int64
}

// RunningCost is a chopper's per-month running-cost breakdown (AC-4): the four
// named components — fuel, hangar, insurance, crew. A grounded chopper still
// incurs the standing components (hangar, insurance, crew); a flying chopper
// additionally incurs fuel.
type RunningCost struct {
	Fuel      det.Micropounds
	Hangar    det.Micropounds
	Insurance det.Micropounds
	Crew      det.Micropounds
}

// Total returns the full flying running cost (all four components).
func (r RunningCost) Total() det.Micropounds {
	return r.Fuel + r.Hangar + r.Insurance + r.Crew
}

// GroundedTotal returns the standing running cost a grounded chopper incurs
// (hangar + insurance + crew, no fuel).
func (r RunningCost) GroundedTotal() det.Micropounds {
	return r.Hangar + r.Insurance + r.Crew
}

// UnitStatus is the queryable snapshot of one chopper (AC-2's UnitStatus).
type UnitStatus struct {
	ID       UnitID
	Type     UnitType
	State    FlightState
	Pilot    PilotID // 0 = no pilot assigned
	Wear     int64   // accumulated wear points
	Flying   bool    // EnRoute or OnScene
	Grounded bool    // OutOfService
}

// FleetCounts is the AC-10 fleet-conservation snapshot. Each of the four
// right-hand terms is counted independently from the fleet's per-unit state;
// none is derived as a remainder of the others.
type FleetCounts struct {
	Total        int
	Available    int
	EnRoute      int
	OnScene      int
	OutOfService int
}

// Conserved reports whether the AC-10 identity Total == Available + EnRoute +
// OnScene + OutOfService holds exactly.
func (c FleetCounts) Conserved() bool {
	return c.Total == c.Available+c.EnRoute+c.OnScene+c.OutOfService
}

// --- seams (AC-2: consume registered edges only through interfaces) ---

// FinanceSeam is airunits' registered edge to engine.finance (MOD-022): the
// CAPEX/OPEX settlement surface. Purchase posts capital, running cost posts
// operating expenditure. Amounts are int64 micro-pounds (det.Micropounds).
type FinanceSeam interface {
	// SettleCapital posts a capital (CAPEX) outflow for a chopper purchase
	// (CatConstruction/CatInvestment path, AC-3). A non-nil error (e.g. an
	// insolvent treasury) means the settlement did not post.
	SettleCapital(amount det.Micropounds) error

	// SettleOpex posts one operating-expenditure (OPEX) outflow (CatOpex path,
	// AC-4). Called once per named running-cost component so the drain is
	// independently visible.
	SettleOpex(amount det.Micropounds) error
}

// StaffingSeam is airunits' registered edge to engine.staffing (MOD-073): the
// trained-citizen skill pool. airunits consumes an injected pilot assignment
// and never trains a pilot (AC-5 boundary).
type StaffingSeam interface {
	// PilotQualified reports whether the citizen is a trained pilot in the
	// staffing skill pool.
	PilotQualified(pilotID PilotID) (bool, error)
}

// MaintenanceSeam is airunits' registered edge to engine.maintenance
// (MOD-072): the per-instance engineer-hour demand surface. airunits surfaces
// each chopper's accrued burden here and requests service here; it never keeps
// a chopper-local maintenance ledger (AC-6 boundary).
type MaintenanceSeam interface {
	// ReportDemand surfaces one chopper's accrued engineer-hour burden.
	ReportDemand(unitID UnitID, engineerHours int64) error

	// Service applies engineer-hours of maintenance to one chopper and returns
	// the wear cleared (0 if the seam has nothing to apply).
	Service(unitID UnitID, engineerHours int64) (int64, error)
}

// DispatchSeam is airunits' registered edge to engine.dispatch (MOD-040): the
// unified incident-queue/assignment engine. airunits supplies each chopper's
// role contribution here for dispatch to apply to its own outcome curves;
// airunits never reimplements dispatch routing or outcomes (AC-8 boundary,
// ASM-585).
type DispatchSeam interface {
	// ReportContribution hands one chopper's role-specific effect contribution
	// to the dispatch outcome path.
	ReportContribution(unitID UnitID, role UnitType, effect RoleEffect) error
}

// WorldSeam is airunits' registered edge to engine.world's weather surface
// (ASM-589): the adverse-weather read the traffic-immune chopper must still
// respect. Nil means "calm weather" (no grounding).
type WorldSeam interface {
	// CurrentWeather returns the current weather condition.
	CurrentWeather() Weather
}
