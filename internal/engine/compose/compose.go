package compose

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/extcommute"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/gameinit"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/unlocks"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// baseline-one stub-mechanics constants. These are NOT player-facing
// balance numbers (GR#15 / the balance-number regime): they are the
// cheapest coarse knobs that keep the loop alive for FEAT-083, and every
// AC that depends on them is directional (population grew, money moved,
// conservation held) — never a hardcoded target.
const (
	// defaultStartCoordX/Y place the start tile at the centre of the 30x30
	// expansion grid. The real Folkestone start-tile placement is a world
	// (MOD-017) concern; this is a documented baseline-one placeholder.
	defaultStartCoordX = 15
	defaultStartCoordY = 15

	seedCitizenCount = 64 // baseline-one seed population (AC-8's non-zero seed)

	// BUG-452 (2026-09-01, Aaron's ruling): starting treasury moved from
	// the toy £10 to a realistic £1,500,000 grant (aligning with the
	// webconsole dogfood sim's committed STARTING_TREASURY=£1.5M), on top
	// of the money base-unit rebase itself (1e-6 GBP/unit -> 1e-3 GBP/unit,
	// see internal/foundation/det/money.go's MicropoundsPerPound doc
	// comment) — so the figure is £1,500,000 x 1,000 units/£ =
	// 1,500,000,000.
	//
	// BUG-737 (FEAT-143 wiring, round finding P3, GR#3): this used to be
	// a bare Go literal `initialTreasury` right here — a SECOND source of
	// truth duplicating the exact same figure data/gameinit.json's
	// startingCapitalMicropounds now carries (kept numerically identical
	// to this historical value on purpose, per that file's own disclosure
	// comment). Wire's opening-treasury seeding (seedOpeningBalances,
	// below) reads gi.StartingCapitalMicropounds() instead, so the
	// literal has been removed rather than left to silently drift out of
	// sync with the data file it duplicated. Every test that used to
	// assert against this constant now loads the same figure from
	// data/gameinit.json itself (see compose_gameinit_test.go's
	// testInitialTreasury helper) — one source of truth, not two.
	//
	// initialCitizenWealth keeps its pre-existing 0.5:1 ratio to the
	// (now data-sourced) opening treasury (Aaron's ruling: "keep
	// initialCitizenWealth's 0.5:1 ratio to treasury") — £750,000 x
	// 1,000 units/£. This one stays a literal: FEAT-143's scope is only
	// the treasury/StartingCapitalMicropounds side of genesis money, and
	// citizen wealth has no equivalent gameinit-mode dependency to keep
	// in sync with.
	initialCitizenWealth = 750_000_000 // money-unit base (£750,000, ratio-preserved)

	// monthlyWagesFloor is the pre-existing finance STUB (formerly named
	// monthlyWages, ALWAYS posted flat regardless of population) — kept as
	// a FLOOR, not the wage bill itself, by FEAT-083's de-stub
	// (2026-09-02, Aaron-authorized directly). Rescaled by the SAME factor
	// the opening treasury grew by when it moved from the old £10 literal
	// to today's £1,500,000 (data-sourced since BUG-737, see above)
	// figure (150,000x in real terms): £1 x 150,000 x 1,000 units/£.
	//
	// FEAT-083 makes the wage bill population/employment-derived
	// (employedResidentCount() x moneycirc.go's real UK gross wage,
	// see financeHook.ApplyEffect) — but a NAKED replacement (no floor)
	// was tried and empirically FAILED: at baseline-one's seed scale
	// (64 residents, ~75% employment placeholder), the realistic wage bill
	// (~£60-90k/month) divided across ~32 households comes out BELOW
	// households.RentBurdenOf's 35% threshold against
	// baselineOneMonthlyRentPerHousehold (moneycirc.go, £1,000/month) —
	// reproducing the EXACT catastrophic emigration collapse (population
	// 64 -> 49 within month 1, HousingAffordability pinned at 0) that
	// BUG-452's doc comment (moneycirc.go) already empirically ruled out
	// for the OLD flat £150,000/month figure. This floor is this ticket's
	// documented placeholder that keeps that already-validated safe
	// baseline: the wage bill is max(monthlyWagesFloor, employed x real
	// wage), so population-scaling only takes over once real employment
	// income would EXCEED the floor (roughly 72+ employed residents at the
	// current per-employed wage — reachable via migration growth over a
	// longer run, proven directly in this ticket's monotonic-scaling unit
	// test without needing to grow the full composed city that far).
	// FLAGGED FOR AARON: this floor-vs-realistic-income tension is the
	// same rent-burden fragility BUG-452 already surfaced — the 35%
	// threshold was calibrated against an unrealistically generous flat
	// wage, so a fully realistic (no-floor) wage bill sits right at that
	// threshold's edge. Retiring this floor entirely is a genuine
	// balance-pass call, not solved here.
	monthlyWagesFloor = 150_000_000 // money-unit base (£150,000, ratio-preserved)

	// firmsWageCreditLineMicropoundsRunwayMonths (BUG-548 round finding
	// #2, 2026-09-05) is the number of months of payroll-at-scale the
	// firms working-capital line is sized to cover. The round's
	// TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable measured the
	// PRIOR "1000x initialTreasury" constant as NOT actually
	// population/payroll-derived at all (a flat multiple of a starting
	// balance, unrelated to headcount) and its "generous headroom for
	// dogfood scales" doc comment as measurably wrong — a 60k-employed
	// city exhausts it in ~12 months because payroll scales with
	// population while the old cap did not scale with anything. This
	// replacement is GR#15 data-driven: it derives the cap from the same
	// real per-employed wage figure (monthlyWageGrossPerEmployedMicropounds)
	// times a documented population ceiling and runway, rather than an
	// arbitrary multiplier.
	firmsWageCreditLineMicropoundsRunwayMonths = 24

	// firmsWageCreditLinePopulationCeiling (BUG-548 finding #2) is the
	// employed-population scale this ticket sizes the credit line
	// against — the "100m individual citizens" northstar's nearer-term
	// dogfood milestone (docs/planning/northstar.md), not baseline-one's
	// current seed scale. Still a FIXED cap (auto-extending it from the
	// live employed count is the cleaner follow-up, flagged below) that a
	// sufficiently large employed population will eventually exceed, but
	// now an honestly-derived one: at this ceiling the line covers
	// firmsWageCreditLineMicropoundsRunwayMonths months of payroll, not
	// an arbitrary multiple of a starting balance.
	firmsWageCreditLinePopulationCeiling = 100_000

	// firmsWageCreditLineMicropounds (BUG-548, 2026-09-05) is the working-
	// capital line SetCreditLine grants AcctFirms so PRIVATE-sector wages
	// can be posted FROM firms rather than the treasury (see
	// moneycirc.go's PostWagesFromFirms usage in financeHook.ApplyEffect).
	// Documented placeholder pending the full firms P&L/revenue model
	// (deferred to FEAT-1972079929/engine.firms scope — the same "no
	// per-firm P&L tracking yet" gap industrialTaxRateBp's doc comment
	// already flags): baseline-one's only modelled firm REVENUE today is
	// the utility-consumption-spend leg (postConsumptionAndTax, ~£55/
	// household/month), far below any realistic payroll (employed x
	// monthlyWageGrossPerEmployedMicropounds, ~£2,100/employed/month) —
	// without a credit line, PostWagesFromFirms would overdraft-reject
	// (MET-G201) almost every month and wages would silently stop, which
	// is a worse regression than the bug this fixes (treasury paying
	// 100% of wages regardless of sector).
	//
	// Sized as firmsWageCreditLinePopulationCeiling employed residents x
	// the real gross wage x firmsWageCreditLineMicropoundsRunwayMonths
	// months of runway — GR#15 derived from data already in this file,
	// not an arbitrary multiplier. This is STILL a fixed cap that a large
	// enough employed population will eventually exceed (the round's
	// TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable proves
	// exhaustion is permanent once it happens, because the only modelled
	// firm revenue is orders of magnitude below payroll) — the
	// monthlyWagesFloor safety net is now UNCONDITIONAL (financeHook
	// falls back to the treasury when this line rejects, see
	// financeHook.ApplyEffect's wage-posting comment) specifically
	// because this cap is known to be exceedable, not a claim that it
	// never will be. Auto-extending the line from the LIVE employed
	// count each month (rather than a fixed ceiling) is the cleaner
	// long-term fix, flagged for Aaron's balance pass alongside
	// monthlyWagesFloor's own flagged tension — not solved here because
	// it changes the credit-line-exhaustion attack's reproduction shape
	// (a growing cap can outrun a fixed drain), which is out of this
	// round's scope.
	firmsWageCreditLineMicropounds = firmsWageCreditLinePopulationCeiling *
		monthlyWageGrossPerEmployedMicropounds * firmsWageCreditLineMicropoundsRunwayMonths
)

// baseline-one real-module placeholders. Like the block above, these are
// NOT player-facing balance numbers (GR#15 / the balance-number regime):
// they are the cheapest coarse knobs that let the REAL modules tick in the
// loop for FEAT-083. They are documented placeholders, not spec-transcribed
// figures, and they will be replaced by data-loading / real topology once
// their owning modules (utility networks, world pool) supply it.
const (
	// playerOwnerID is the single baseline-one player/owner identity. The
	// build module's §7 ownership gate keys on world ownership; a real
	// multi-player/owner model is a later sprint.
	playerOwnerID = uint32(1)

	// consumption source capacities (units/tick). The city's real utility
	// topology does not exist yet (build has built no networks); these
	// coarse single-source networks keep the consumption solve drawing.
	baselineOneWaterCapacity = 1_000_000.0
	baselineOnePowerCapacity = 1_000_000.0
	baselineOneGasCapacity   = 1_000_000.0

	// attract master-dial inputs. A_world (40) is a neutral-ish placeholder.
	// All seven §11 terms are now real, computed per month: Safety/
	// LeisureFit/Environment by safetyTerm/leisureFitTerm/environmentTerm
	// (FEAT-167 wave 1), ServiceCoverage/JobAvailability by
	// serviceCoverageTerm/jobAvailabilityTerm (servicesfirms_wire.go,
	// FEAT-167 completion), HousingAffordability/Reputation inside
	// attract itself. The old flat baselineOneTermValue=50.0 placeholder
	// (docs/planning/icd/engine.attract-terms.md §3/§12 open decision 3)
	// is gone: nothing in this package references it any more (its last
	// use was the two ServiceCoverage/JobAvailability SetTermInputs
	// fields below and the tripwire test that guarded them, both replaced
	// — see servicesfirms_wire_test.go's proof that no term reads the old
	// flat value after a warmed-up run).
	baselineOneAWorld        = 40.0
	baselineOneMigrationRate = 1.0
	// baselineOneMonthlyRentPerHousehold (FEAT-1972079927 Q2) replaces the
	// old always-vacant baselineOneMonthlyRent=0 placeholder — see
	// moneycirc.go's doc comment for the real-world grounding.

	// attract capacity constraints (people / dwelling units). Unbounded
	// placeholders — the real housing-vacancy and junction-throughput
	// signals come from households/logistics once those are wired to
	// produce them.
	baselineOneHousingVacancy     = int64(1_000_000)
	baselineOneJunctionThroughput = int64(1_000_000)

	// attract reputation-momentum parameters (asymmetric: fall faster than
	// rise — §11's Detroit-trap mechanic). Same shape the attract module's
	// own S6 scenario uses.
	baselineOneRepRise = 0.2
	baselineOneRepFall = 0.8
	baselineOneRepMax  = 100.0
)

// FEAT-167 (docs/planning/icd/engine.attract-terms.md): baseline one has no
// district/cell topology yet, so the Safety/Environment terms are computed
// against ONE compose-owned aggregate citywide district/cell — not a
// player-facing balance number, just the coarsest identity that lets
// engine.crime/engine.refuse's real per-district/per-cell accounting run at
// all before topology exists.
const (
	citywideCrimeDistrict = crime.DistrictID(0)
	citywideRefuseCellID  = "citywide"
)

// refuseStreams is the fixed, documented iteration order for summing
// engine.refuse's three §25 waste streams into the Environment term's
// composite (environmentTerm below) — a slice, never a map range (GR#21).
var refuseStreams = []refuse.Stream{refuse.StreamGeneral, refuse.StreamRecycling, refuse.StreamFood}

// defaultPersistCity is the PLACEHOLDER city identity used when durable
// persistence is enabled (Deps.PersistStore != nil) but Deps.PersistCity is
// left zero. Phase 1 is multi-tenant-single-player with a single fixed
// tenant placeholder (FEAT-1972079936 epic open-Q #3): real per-account
// tenant/city identity arrives with Phase 2 auth. Per the balance-number
// regime, this is a documented PLACEHOLDER, not a spec-transcribed value —
// nothing player-facing keys on it, and it flows in via Deps rather than
// being hardcoded inside the adapter (persistjournal.go).
var defaultPersistCity = persist.CityKey{TenantID: "local", CityID: "default"}

// Deps carries the real module dependencies Wire composes. A nil *Deps
// (the common boot case) means "construct the defaults" — world, citizens
// and market are built fresh inside Wire. Callers that need to observe or
// pre-build a dependency (tests, a future save/reload path) inject it
// here.
type Deps struct {
	// CorrelationID roots every registry-sourced error this composition
	// constructs. Empty mints a fresh one.
	CorrelationID string

	// GameMode is BUG-737's FEAT-143 wiring seam: the new-game mode CHOICE
	// as a plain wire string ("real" or "unlimited", gameinit.Mode's own
	// String() values) — never a *gameinit.GameInit or gameinit.Mode typed
	// value, so a caller in a package with NO registered edge to
	// feat.gameinit (cmd/metropolis's feat.skeleton, cmd/metroserve — see
	// compose_gameinit.go's doc comment for the exact edge-registration
	// check) can still choose the mode without importing the package.
	// Empty (the default, every existing Wire caller/test) resolves to
	// gameinit.ModeReal — real mode, the full financial-failure loop,
	// reproducing every pre-BUG-737 composed-engine test's behaviour
	// byte-for-byte (GR#26/GR#15: a wiring pass must never silently change
	// existing behaviour). An unrecognised non-empty value fails Wire with
	// ErrModuleFailed naming "gameinit" (AC-1's fail-closed contract —
	// never a silent default).
	GameMode string

	// World, Citizens and Market are the three REAL module dependencies.
	// A nil field means "construct the default". (A caller that wants to
	// prove a required module is absent uses the LoadMarket seam below —
	// the only one of the three whose default construction can genuinely
	// fail.)
	World    *world.WorldAPI
	Citizens *citizens.CitizensAPI
	Market   *market.MarketAPI

	// LoadMarket overrides market construction (defaults to
	// market.LoadDefault). It is the AC-4 test seam: a caller injects a
	// failing loader and asserts Wire returns ErrModuleFailed naming
	// "market" with zero hooks left behind.
	LoadMarket func(correlationID string) (*market.MarketAPI, error)

	// CommandJournaler installs the engine-owns-journal seam (Aaron's DD,
	// FEAT-1972079852 inc3/inc4; edge feat.compositionroot -> harness.replay
	// registered 61e41e9). A nil field (the common boot case) defaults to a
	// freshly constructed *replay.NewRecorder() so every composed engine
	// journals its accepted commands in-memory from boot — never a silent
	// no-op journaler in the real, running composed engine. Tests that need
	// to observe or fail journaling inject their own core.CommandJournaler
	// here (mirrors LoadMarket's override shape above).
	CommandJournaler core.CommandJournaler

	// PersistStore enables durable, server-side command-journal persistence
	// (FEAT-1972079936 Phase 1 inc2 — the localStorage/data-loss cure). A nil
	// field (the common boot case) means persistence is OFF and behaviour is
	// EXACTLY unchanged: the resolved CommandJournaler (the injected one, or
	// the default in-memory *replay.Recorder) is wired verbatim. A non-nil
	// Store makes Wire wrap the resolved journaler with the durable
	// write-through adapter (persistjournal.go) so every accepted command is
	// ALSO durably appended — the wrapped journaler stays whatever it was, so
	// in-memory replay and durable persist both happen. Persistence is a pure
	// side channel: it never influences engine state (AC-6).
	PersistStore persist.Store

	// PersistCity is the city/savegame identity durable records are keyed
	// under when PersistStore is set. A zero value defaults to
	// defaultPersistCity (the documented Phase 1 PLACEHOLDER) — real
	// tenant/city identity is Phase 2. Ignored when PersistStore is nil.
	PersistCity persist.CityKey

	// Logistics overrides construction of the engine.logistics dependency
	// build's Tick draws construction materials against (defaults to
	// logistics.LoadDefault). A nil field is the common boot case; the
	// BUG-266 regression test seam: a caller injects a pre-Provisioned
	// LogisticsAPI so a build order can actually complete (unprovisioned
	// stock never fulfils a materials draw) and drive a real demolish
	// through handleGameplay.
	Logistics *logistics.LogisticsAPI

	// InvariantOpts are threaded straight into invariant.WireDaily. Tests
	// use invariant.WithLogSink / invariant.WithPanicFunc to observe
	// conservation violations (AC-10).
	InvariantOpts []invariant.HookOption

	// Crime, Leisure and Refuse override construction of the three
	// FEAT-167 attract-term source modules (default: crime.New /
	// leisure.LoadDefault / refuse.LoadDefault). nil is the common boot
	// case; the test seam lets a caller inject a pre-configured instance
	// to prove a term value actually moves when its source module's state
	// changes (docs/planning/icd/engine.attract-terms.md §11).
	Crime   *crime.CrimeAPI
	Leisure *leisure.LeisureAPI
	Refuse  *refuse.RefuseAPI

	// ExtCommute overrides construction of the FEAT-207 off-map
	// external-commuting module (default: extcommute.LoadDefault). nil is
	// the common boot case; the test seam lets a caller inject a
	// pre-configured instance (docs/planning/icd/engine.extcommute-compose.md
	// §11's end-to-end unblock test).
	ExtCommute *extcommute.ExtCommuteAPI

	// Services overrides construction of the FEAT-167-completion
	// ServiceCoverage source module (default: services.LoadDefault). nil
	// is the common boot case; the test seam lets a caller inject a
	// pre-registered instance to prove ServiceCoverage actually moves
	// when its source module's state changes
	// (docs/planning/icd/engine.services-coverage.md §11).
	Services *services.ServicesAPI

	// Firms overrides construction of the FEAT-167-completion
	// JobAvailability source module (default: firms.LoadDefault). nil is
	// the common boot case; the test seam lets a caller inject a
	// pre-registered instance to prove JobAvailability actually moves
	// when its source module's state changes
	// (docs/planning/icd/engine.firms-labourmarket.md §11).
	Firms *firms.FirmsAPI

	// DeathServices overrides construction of BUG-689's engine.deathservices
	// dependency (default: LoadDeathServices, itself defaulting to
	// deathservices.LoadDefault). nil is the common boot case — a caller
	// wanting the exact pre-BUG-689 behaviour never sets this; the wiring
	// stays fail-safe regardless (see intakeDeathServices's own doc: a nil
	// st.deathServices/st.citizens is a documented no-op, and for the
	// CURRENT data/deathservices.json + data/mortality.json figures the
	// injected drain (hearseMonthlyTransportBudget=300) never binds tighter
	// than the ordinary mortality budget (monthlyDeathBudget=25) — see
	// TestBUG689_DrainCapacityNeverBindsForDefaultData — so wiring it on by
	// default reproduces the prior population trajectory byte-for-byte for
	// every existing baseline-one fixture). The test seam lets a caller
	// inject a pre-registered instance to prove FEAT-088's disposal state
	// actually moves when the live handoff stream feeds it
	// (docs/planning/acceptance/MOD-083-inc1.md).
	DeathServices *deathservices.DeathServicesAPI

	// LoadDeathServices overrides DeathServicesAPI construction (defaults
	// to deathservices.LoadDefault). nil is the common boot case; the AC-4
	// test seam: a caller injects a failing loader and asserts Wire returns
	// ErrModuleFailed naming "deathservices" with zero hooks left behind —
	// mirrors LoadMarket/LoadTraffic's identical shape above.
	LoadDeathServices func(correlationID string) (*deathservices.DeathServicesAPI, error)

	// DeathServiceCemeteries / DeathServiceCrematoria (BUG-720) pre-register
	// cemetery/crematorium instances at Wire time. This is a STOPGAP seam,
	// not the sanctioned long-term registration path: BUG-720's own
	// investigation found TWO real engine.build-owned API gaps that block
	// registering a cemetery/crematorium from an actual player-built
	// structure the way [build.BuildAPI]'s existing engine.services bridge
	// (registerCompletedServicesLocked/registerServiceLocked, build.go)
	// already does for generic ServiceKind buildings —
	//
	//  1. compose's own protocol.KindBuild handler (below, "case
	//     protocol.KindBuild") never populates [build.BuildCommand.
	//     BuildingID] from the player's protocol.BuildPayload — only Zone
	//     is set — so NO ServiceKind-declaring catalogue building (cemetery/
	//     crematorium among them; this is generic, not deathservices-
	//     specific) can ever be registered with engine.services via the
	//     live player build path today, only via a direct BuildCommand a
	//     test constructs by hand.
	//  2. Even with (1) fixed, [build.BuildOrder] (the public projection
	//     [build.BuildAPI.Queue] returns) does not expose BuildingID or any
	//     buildingID-keyed accessor, so compose has no exported way to
	//     discover WHICH completed structure is a cemetery/crematorium in
	//     order to call [deathservices.DeathServicesAPI.RegisterCemetery]/
	//     [deathservices.DeathServicesAPI.RegisterCrematorium] (a distinct
	//     registry from engine.services' generic ServiceSpec — plot/
	//     throughput tracking that module owns itself, so even a fixed (1)
	//     would only register the generic engine.services capacity term,
	//     never deathservices' own cemetery/crematorium state).
	//
	// Both are engine.build-owned changes (a BuildOrder field/accessor, and
	// a compose<->protocol BuildingID translation), filed as a follow-up
	// for the architect rather than invented here. Until they land, this
	// Deps seam is the only way a live composition gets cemetery/
	// crematorium capacity registered — nil/empty reproduces today's
	// "nothing ever runs" behaviour exactly (BUG-720's own starting
	// symptom). There is also no RegisterCemetery/RegisterCrematorium
	// "Unregister" — a bulldozed cemetery/crematorium cannot be
	// decommissioned from deathservices' registry today; that gap is
	// likewise left STOPPED, not invented around.
	DeathServiceCemeteries []DeathServiceCemeterySpec
	DeathServiceCrematoria []string

	// LoadTraffic overrides construction of the FEAT-206 engine.traffic
	// dependency (defaults to loadDefaultTraffic — traffic.New() +
	// LoadConfig against the resolved data/ dir, mirroring LoadMarket's
	// shape above). nil is the common boot case; the test seam lets a
	// caller inject a failing loader and assert Wire returns
	// ErrModuleFailed naming "traffic" with zero hooks left behind (AC-4's
	// discipline, docs/planning/icd/engine.traffic-tick.md §2/§8).
	LoadTraffic func(correlationID string) (*traffic.TrafficAPI, error)

	// SeedResidentIDBase/SeedResidentIDCount (BUG-665, independent round
	// finding) register a contiguous, externally-seeded citizen id range
	// — [SeedResidentIDBase+1, SeedResidentIDBase+SeedResidentIDCount] —
	// as compose-visible for moneycirc.go's four resident-scoped monthly
	// passes (markEmploymentAndCount/employedResidentCount/
	// formResidentHouseholds/distributeWagesToResidents, all keyed on
	// liveResidentIDs()). Without this, a caller that bulk-injects
	// citizens directly into Deps.Citizens' cold store (via
	// citizens.CitizensAPI.SeedColdRecords — bypassing spawnCitizens' own
	// per-citizen LifeEventBirth command path, which is what actually
	// grows nextCitizenID/residentIDs()) produces citizens compose's own
	// resident enumeration never sees: real in
	// CitizensAPI.TotalPopulation, invisible to employment marking, wage
	// distribution, and household formation. The round's own live proof:
	// Composition.MoneyFlows was byte-identical after one ticked month
	// whether SeedCitizenCount was 0 or 50,000.
	//
	// Deliberately widens ONLY liveResidentIDs(), never residentIDs()
	// itself: residentIDs() ALSO feeds attract.MigrationCommand.
	// ResidentIDs (the emigration-eligible set, compose.go's
	// applyMigration) and its own doc comment records a live, previously-
	// shipped regression from widening exactly that function (a
	// BUG-529/BUG-535 first cut caused "a sawtooth boom/bust population
	// collapse every few months" once migrants/fertility children became
	// emigration-eligible). Folding a large SeedResidentIDCount range
	// into emigration eligibility too would risk reproducing that same
	// class of regression at a much larger scale — out of scope for this
	// fix, which only needs the moneycirc wage/employment/household
	// surface to see the seeded population, never emigration.
	//
	// Wire validates the range stays disjoint from [1, seedCitizenCount],
	// attract.MigrantIDBase, and citizens.FertilityChildIDBase (the same
	// three-way id-seam CONTRACT spawnCitizens' own mint-time check
	// enforces, ErrCitizenIDNamespaceSeam) and returns
	// ErrSeedResidentIDRangeCollides (its own registry code, MET-G814 —
	// NOT ErrCitizenIDNamespaceSeam, whose message template renders a
	// different per-citizen-mint shape) rather than silently unioning a
	// colliding range. SeedResidentIDCount == 0 (the default, every
	// existing caller) is byte-identical to prior behaviour: no extra
	// range, liveResidentIDs() unchanged.
	SeedResidentIDBase  uint64
	SeedResidentIDCount int64
}

// moduleRegistration is one fixed slot in the composition order.
type moduleRegistration struct {
	name  string
	phase core.PhaseKind
	// hook builds this module's PhaseHook against the wired state. nil
	// means "wired via a dedicated path" (the invariant, via
	// invariant.WireDaily).
	hook func(st *simState) core.PhaseHook
}

// registrationOrder is the fixed, documented composition order (AC-2). It
// is a slice, NEVER a map: iteration order IS the contract, and nothing in
// this package ranges over a registration map (GR#21). The order is:
// world, citizens, market, consumption, finance, build, attract, then the
// invariant on PhaseDailyTick. Two modules share a phase in two places —
// market then consumption (both PhaseConsumptionShortfall), and citizens,
// build, then invariant (all three PhaseDailyTick — FEAT-169 moved citizens
// onto the daily tick alongside build/invariant, see below) — this slice
// order is what determines their intra-phase run order. attract remains
// alone on PhasePopulation now that citizens has moved off it.
var registrationOrder = []moduleRegistration{
	{name: "world", phase: core.PhaseProduction, hook: func(st *simState) core.PhaseHook { return noopHook{name: "world", st: st} }},
	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): traffic's
	// AdvanceTick registers on PhaseDailyTick, and MUST come before every
	// other PhaseDailyTick registration below it in this slice (citizens,
	// build, invariant) — traffic's own doc.go "Day-boundary contract"
	// requires the reset to run "before that day's demand-generating
	// systems... run their own tick logic for the day". Placed
	// immediately after "world" (a different, monthly phase — its
	// position relative to traffic carries no ordering meaning) so the
	// slice reads world -> traffic -> citizens -> ... and, restricted to
	// just the PhaseDailyTick subset, traffic -> citizens -> build ->
	// invariant.
	{name: "traffic", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &trafficTickHook{st: st} }},
	// FEAT-169: citizens registers on the DAILY tick (not PhasePopulation)
	// because CitizensAPI.AdvanceDayTick is itself a once-per-day-tick call
	// (its own amortised 1/30-shards-per-day cold pass) and the ICD's T0
	// update class requires the resulting births/deaths land in peopleDelta
	// the SAME tick they are computed — never queued past it. Registered
	// BEFORE build/invariant in this slice so citizens' births/deaths are
	// folded into peopleDelta before invariant's same-tick conservation
	// check observes it (the same ordering discipline BUG-268 established
	// for build -> invariant).
	{name: "citizens", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &coldPassHook{st: st} }},
	{name: "market", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return noopHook{name: "market", st: st} }},
	{name: "consumption", phase: core.PhaseConsumptionShortfall, hook: func(st *simState) core.PhaseHook { return &consumptionHook{st: st} }},
	{name: "finance", phase: core.PhaseFinance, hook: func(st *simState) core.PhaseHook { return &financeHook{st: st} }},
	// BUG-268: build was wired against the monthly PhaseLandValueDecay slot,
	// so BuildAPI.Tick's one-simulation-DAY-per-call cadence (build.go's
	// daysPerTick) only ever fired once per simulation MONTH — a 45-day
	// dwelling took 45 months, not 45 days. Moved to the daily
	// PhaseDailyTick (the only daily phase this package's fixed phase set
	// offers) so one sim-day of lead/materials/labour elapses per sim-day,
	// matching data/buildings.json's own "labourPerTick"/"leadTimeUnit"
	// documentation (both already written in daily terms). Registered
	// before "invariant" below so the queue advances, then the conservation
	// check observes the same day's result — deterministic intra-phase
	// order via this slice's iteration order (GR#21).
	{name: "build", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &buildHook{st: st} }},
	// BUG-720: crematoriums/cemeteries/hearses NOW RUN. Registered on the
	// DAILY tick (not PhasePopulation, where BUG-689's Intake sits) because
	// cremation's own data-sourced throughput is a PER-DAY cap
	// (deathservices/crematory.go's DailyThroughput, spec seed 12/d) — a
	// monthly call would only ever charge one day's worth of cremation
	// capacity per month, an ~30x under-run of the real budget. Hearse
	// transport/dispensation are month-scoped budgets internally
	// (hearse.go/dispensation.go's own resetMonthLocked), so calling them
	// once per DAY simply drains that monthly pool across ~30 daily calls —
	// the same amortised-across-the-month shape citizens' own daily cold
	// pass (coldPassHook, registered just above build in this slice) and
	// build's own daysPerTick materials/labour/lead-time draw already use.
	// Placed AFTER build (so a same-day newly-completed structure — once
	// the two engine.build gaps this bug's Deps.DeathServiceCemeteries doc
	// comment names are closed — would be visible to this sweep the same
	// tick it lands, mirroring build's own "before invariant" positioning
	// rationale) and BEFORE invariant (so this hook's SettleOpex finance
	// posting is observed by the SAME day's conservation check, exactly
	// the ordering build's own doc comment above establishes for its
	// demolish-compensation posting).
	{name: "deathservices-run", phase: core.PhaseDailyTick, hook: func(st *simState) core.PhaseHook { return &deathServicesRunHook{st: st} }},
	{name: "attract", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook { return &attractHook{st: st} }},
	// BUG-689: engine.deathservices' monthly Intake, matching MOD-083's own
	// month-level budget semantics (hearse/dispensation throughput are
	// both month-scoped counters, cemetery.go/hearse.go/dispensation.go)
	// — registered on the SAME monthly PhasePopulation slot as attract,
	// after it in this slice. The relative order carries no correctness
	// meaning today (deathservices' intake reads only citizens' handoff
	// stream and its own state, never attract's freshly-computed migration
	// numbers), but is placed after attract/citizens so a body that both
	// dies (citizens' daily-tick realisation, earlier in the tick) and is
	// then intaken (this monthly hook) never observes a same-tick
	// ordering ambiguity with the population-affecting hooks above it.
	{name: "deathservices", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook { return &deathServicesHook{st: st} }},
	// MOD-034 (engine.wellbeing, compose_wellbeing.go): monthly cohort
	// reconstruction, placed after deathservices on the same PhasePopulation
	// slot — it only reads the live citizen population and this month's
	// index, never anything the hooks above it produce, so its position
	// relative to them carries no ordering meaning (mirrors deathservices'
	// own doc comment on this point).
	{name: "wellbeing", phase: core.PhasePopulation, hook: func(st *simState) core.PhaseHook { return &wellbeingHook{st: st} }},
	{name: "invariant", phase: core.PhaseDailyTick, hook: nil},
}

// RegistrationOrder returns a defensive copy of the fixed composition
// order (module names), in the order Wire registers them. Exported so a
// test can bind the documented order mechanically (AC-2), and so
// harness.synth can read the real hook count without hand-asserting a
// stale literal (AC-14).
func RegistrationOrder() []string {
	out := make([]string, len(registrationOrder))
	for i, r := range registrationOrder {
		out[i] = r.name
	}
	return out
}

// BaselineOneHookCount returns the number of PhaseHooks Wire registers
// (one per moduleRegistration). The runtime ground truth is
// core.Engine.HookCount() after Wire; this mirrors it for callers that
// need the declared figure without constructing an engine (AC-14).
func BaselineOneHookCount() int { return len(registrationOrder) }

// viewRegistration is one fixed slot in the FEAT-208 view-publishing
// order, parallel to moduleRegistration above (AC-2's "zero hooks left
// behind" discipline extended to "zero views left behind"). fn resolves
// this view's ViewPatchFunc against the wired simState — it is not
// called until Wire actually registers it, mirroring
// moduleRegistration.hook's deferred-construction shape.
type viewRegistration struct {
	name string
	fn   func(st *simState) core.ViewPatchFunc
}

// viewRegistrationOrder is the fixed, documented FEAT-208 view
// registration order (AC-2 extended) — a slice, NEVER a map: this
// package never ranges a view registration table (GR#21), matching
// registrationOrder's own discipline above. Increment 1 (the FEAT-208
// design's §6 recommended first slice) registered exactly one view:
// "f4.services", serving only its capacityDemand sub-view
// (buildServicesCapacityDemandPatch, services_publish.go). Increment 2
// adds "f2.finance", serving only its balanceSheet sub-view
// (buildFinanceBalanceSheetPatch, finance_publish.go) — the design's §6
// fast-follow list's next entry, chosen because engine.finance is
// already composed and ui.screen.finance's ApplyDelta already exists.
// BUG-323 adds "f1.viewport", serving the start tile's real
// engine.world terrain (buildViewportPatch, viewport_publish.go) — the
// design's §6 fast-follow list's next entry, pulled forward to P0
// because F1 is the DEFAULT screen at boot and, with no view registered
// here, engine.core rejected its Subscribe and it rendered entirely
// blank. Later increments (f8.districts, f5.trade, f7.projections) are
// documented, deliberate fast-follows — see the design's §6 — each
// adding one more entry here, in the SAME slice, never a new
// registration mechanism.
var viewRegistrationOrder = []viewRegistration{
	{name: servicesViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildServicesCapacityDemandPatch }},
	{name: financeViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildFinanceBalanceSheetPatch }},
	// BUG-324: "chrome.topbar" — the global chrome's top-bar figures
	// (chrome_publish.go). Not one of the design's §6 F-screen
	// fast-follows: it is the view the ALWAYS-visible chrome renders
	// from, and internal/ui/screens/chrome could not be registered in
	// cmd/metropolis at all until it existed, because an unregistered
	// view's Subscribe is rejected and the top bar would have rendered
	// permanently empty — the same failure mode "f1.viewport" shows
	// today.
	{name: chromeViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildChromeTopBarPatch }},
	{name: viewportViewSubscriptionName, fn: func(st *simState) core.ViewPatchFunc { return st.buildViewportPatch }},
}

// RegisteredViewNames returns a defensive copy of the fixed view
// registration order (view names), in the order Wire registers them —
// mirrors RegistrationOrder() above, for a test or future harness that
// needs the declared FEAT-208 view set without constructing an engine.
func RegisteredViewNames() []string {
	out := make([]string, len(viewRegistrationOrder))
	for i, r := range viewRegistrationOrder {
		out[i] = r.name
	}
	return out
}

// Composition is the read-only handle Wire returns once the baseline-one
// hook set is registered. It exposes the composition's own live state
// (population, money flows) so a headless driver or test can assert the
// directional liveness ACs without reaching into unexported state.
type Composition struct {
	state *simState
}

// Population returns the current total citizen population (all fidelity
// tiers), read live from the citizens store.
func (c *Composition) Population() int {
	return c.state.citizens.TotalPopulation(c.state.cid)
}

// MoneyFlows returns the cumulative gross money flow (wages + tax) the
// finance stub has emitted — the AC-9 "money moved" figure, distinct from
// the conserved net total. Read-only; safe to call after a run completes
// (single-goroutine, see simState's doc comment).
func (c *Composition) MoneyFlows() int64 {
	return c.state.moneyFlows
}

// Treasury returns the current treasury balance (micropounds).
func (c *Composition) Treasury() int64 {
	return c.state.treasury
}

// CitizenWealth returns the current aggregate citizen wealth (micropounds).
func (c *Composition) CitizenWealth() int64 {
	return c.state.citizenWealth
}

// PopulationHash returns the citizens store's deterministic population
// fingerprint (citizens.PopulationHash) — the AC-11 determinism probe: two
// composed runs at the same seed must produce the identical hash.
func (c *Composition) PopulationHash() [32]byte {
	return c.state.citizens.PopulationHash(c.state.cid)
}

// ConsumptionDelivered returns the cumulative utility quantity the
// consumption hook has delivered (litres + kWh summed across water/power/
// gas) over the run so far. Non-zero proves the real consumption solve
// drew against the coarse networks rather than no-op'ing — the
// "consumption actually draws" liveness observable.
func (c *Composition) ConsumptionDelivered() float64 {
	return c.state.consumptionDelivered
}

// NetMigration returns the cumulative signed net migration (inflow −
// outflow) the attract hook has applied. It is the "migration is
// attractiveness-driven" observable: driven by AttractAPI.ApplyMigration's
// g(A − A_world), never a hardcoded +N/month.
func (c *Composition) NetMigration() int64 {
	return c.state.netMigration
}

// VitalBirths returns the cumulative real fertility births (FEAT-160) the
// citizens cold pass has produced and folded into peopleDelta so far — the
// "births are real, not the old flat-8/month fake" observable. Zero is a
// legitimate value (no eligible couples yet), unlike the old spawnHook
// fake, which was never zero after the first month.
func (c *Composition) VitalBirths() int64 {
	return c.state.vitalBirths
}

// VitalDeaths returns the cumulative real per-citizen mortality deaths the
// citizens cold pass has produced and folded into peopleDelta so far.
func (c *Composition) VitalDeaths() int64 {
	return c.state.vitalDeaths
}

// ExtCommute returns the wired FEAT-207 off-map external-commuting handle
// (docs/planning/icd/engine.extcommute-compose.md). Baseline one routes no
// gameplay command to Assign/Release/InCommute yet (ICD §12 open decision
// 4 — command routing is a later, separate item); this accessor is the
// seam a future gameplay handler, or a test driving the end-to-end
// assign/release proof, reaches it through.
func (c *Composition) ExtCommute() *extcommute.ExtCommuteAPI {
	return c.state.extCommute
}

// Traffic returns the wired FEAT-206 engine.traffic handle
// (docs/planning/icd/engine.traffic-tick.md). Baseline one routes no other
// demand-generating module (engine.shopping, engine.dispatch — neither
// exists in this codebase yet) through it besides this package's own
// AdvanceTick hook and extcommute's read-only Congestion seam
// (traffic_wire.go); this accessor is the seam a future demand-generating
// module's SetTraffic wiring, and today's tests (the AC-required
// unbounded-demand regression and day-boundary ordering proofs), reach the
// composed instance through — mirrors ExtCommute() above.
func (c *Composition) Traffic() *traffic.TrafficAPI {
	return c.state.traffic
}

// DeathServices returns the composed engine's BUG-689 engine.deathservices
// instance — the same one deathServicesHook's monthly Intake drives and
// Participants() (save_wire.go) saves/restores. Read-only accessor for
// tests and inspection tooling.
func (c *Composition) DeathServices() *deathservices.DeathServicesAPI {
	return c.state.deathServices
}

// DeathServiceCemeterySpec pre-registers one cemetery at Wire time with an
// explicit plot capacity (BUG-720's Deps.DeathServiceCemeteries stopgap
// seam — see that field's doc comment for why this exists instead of a
// real build-completion trigger). Capacity <= 0 uses
// [deathservices.DeathServicesAPI.RegisterCemetery]'s data-sourced default
// instead of [deathservices.DeathServicesAPI.RegisterCemeteryWithCapacity]
// (GR#15: never silently substitute 0 for "unset").
type DeathServiceCemeterySpec struct {
	ID       string
	Capacity int64
}

// DeathServicesRunStatus is BUG-720's GR#17 observability surface (AC per
// this bug's own brief): a point-in-time read of the live disposal
// pipeline, backed entirely by [deathservices.DeathServicesAPI]'s own
// registered accessors (AwaitingBacklog/DispensationActive) plus the two
// registry rosters this composition tracks (see simState.
// deathServiceCemeteryIDs/deathServiceCrematoriumIDs's doc comment for why
// deathservices itself exposes no enumeration accessor). A dedicated
// f4-style UI publish view (mirroring services_publish.go's
// buildServicesCapacityDemandPatch) is a legitimate fast-follow, not
// required for BUG-720's "make it run" scope — this accessor is the same
// class of test/inspection surface [Composition.DeathServices] already is.
type DeathServicesRunStatus struct {
	AwaitingBacklog      int
	DispensationActive   bool
	CemeteriesRegistered int
	CrematoriaRegistered int
}

// DeathServicesRunStatus returns the current disposal-pipeline status
// (BUG-720). Returns the zero value, no error, when deathservices is not
// wired (mirrors this package's other nil-wiring-is-a-documented-no-op
// accessors, e.g. intakeDeathServices).
func (c *Composition) DeathServicesRunStatus() DeathServicesRunStatus {
	st := c.state
	if st.deathServices == nil {
		return DeathServicesRunStatus{}
	}
	backlog, _ := st.deathServices.AwaitingBacklog(st.cid)
	active, _ := st.deathServices.DispensationActive(st.cid)
	return DeathServicesRunStatus{
		AwaitingBacklog:    backlog,
		DispensationActive: active,
		// BUG-743: the roster is now TWO sources — the Wire-time stopgap
		// plus the live build->deathservices bridge — summed here so this
		// status figure reflects every actually-registered id, not just
		// the stopgap half.
		CemeteriesRegistered: len(st.deathServiceCemeteryIDs) + len(st.deathServiceBridgeCemeteryIDs),
		CrematoriaRegistered: len(st.deathServiceCrematoriumIDs) + len(st.deathServiceBridgeCrematoriumIDs),
	}
}

// Journaler returns the composed engine's engine-owns-journal seam
// (FEAT-1972079852 inc4, Deps.CommandJournaler) — the same
// core.CommandJournaler instance e.SetCommandJournaler wired into the
// composed engine's accept() path. Read-only accessor for the
// wired-not-built proof: a test asserts Records() on the concrete
// *replay.Recorder this returns captured an accepted command and did not
// capture a rejected one.
func (c *Composition) Journaler() core.CommandJournaler {
	return c.state.journaler
}

// Wire registers the full baseline-one hook set against e in the fixed,
// documented order (world -> citizens -> market -> consumption -> finance
// -> build -> attract, invariant on PhaseDailyTick). It is the single
// wiring path every runnable top reaches real hooks through (AC-1/AC-13);
// no other package calls core.Engine.RegisterPhaseHook for the real
// modules.
//
// Wire fails loudly, never silently:
//   - a second call on an already-composed engine returns ErrAlreadyComposed
//     (AC-3);
//   - a required module whose construction fails returns ErrModuleFailed
//     naming the module, with zero hooks left behind (AC-4);
//   - a call after the engine has sealed returns ErrWiringAfterSeal wrapping
//     core.ErrEngineSealed (AC-6).
//
// deps may be nil (construct defaults). The returned *Composition exposes
// the live population/money state; callers that only need the engine (the
// headless driver, cmd/metropolis) may ignore it.
func Wire(e *core.Engine, deps *Deps) (*Composition, error) {
	if e == nil {
		return nil, errs.New(ErrRequiredModuleMissing, errs.NewCorrelationID(), map[string]any{"module": "engine"})
	}
	if deps == nil {
		deps = &Deps{}
	}
	cid := deps.CorrelationID
	if cid == "" {
		cid = errs.NewCorrelationID()
	}

	// BUG-665: validate Deps.SeedResidentIDCount's range stays disjoint
	// from compose's own [1, seedCitizenCount] founder range and from
	// engine.attract's/engine.citizens' migrant/fertility-child id bases
	// -- the SAME three-way id-seam CONTRACT spawnCitizens' own mint-time
	// check (below, ErrCitizenIDNamespaceSeam) enforces for its own
	// counter, checked here BEFORE any hook registers so a colliding
	// caller-supplied range never reaches liveResidentIDs() at all. Uses
	// its OWN registry code (ErrSeedResidentIDRangeCollides, MET-G814),
	// not ErrCitizenIDNamespaceSeam -- see that code's own doc comment
	// (errors.go) for why reusing it left "{id}"/"{base}" surviving as
	// literal, unsubstituted text (caught live by
	// internal/foundation/errs' whole-tree render gate,
	// TestRenderGate_WholeTreeHasNoLiteralTokens).
	if deps.SeedResidentIDCount > 0 {
		if deps.SeedResidentIDCount < 0 {
			return nil, errs.New(ErrSeedResidentIDRangeCollides, cid, map[string]any{
				"reason": "SeedResidentIDCount is negative", "count": deps.SeedResidentIDCount,
			})
		}
		// Manual overflow check (uint64 has no signed-style SafeAdd in
		// foundation/num today): base+count must not wrap past
		// math.MaxUint64.
		if deps.SeedResidentIDBase > math.MaxUint64-uint64(deps.SeedResidentIDCount) {
			return nil, errs.New(ErrSeedResidentIDRangeCollides, cid, map[string]any{
				"reason": "SeedResidentIDBase+SeedResidentIDCount overflows uint64",
				"base":   deps.SeedResidentIDBase, "count": deps.SeedResidentIDCount,
			})
		}
		maxSeededID := deps.SeedResidentIDBase + uint64(deps.SeedResidentIDCount)
		if deps.SeedResidentIDBase+1 <= seedCitizenCount {
			return nil, errs.New(ErrSeedResidentIDRangeCollides, cid, map[string]any{
				"reason": "SeedResidentIDBase+1 collides with compose's own [1,seedCitizenCount] founder range",
				"base":   deps.SeedResidentIDBase, "seedCitizenCount": seedCitizenCount,
			})
		}
		if maxSeededID >= attract.MigrantIDBase {
			return nil, errs.New(ErrSeedResidentIDRangeCollides, cid, map[string]any{
				"reason":      "SeedResidentIDBase+SeedResidentIDCount reaches attract.MigrantIDBase",
				"maxSeededID": maxSeededID, "migrantIDBase": attract.MigrantIDBase,
			})
		}
		if maxSeededID >= citizens.FertilityChildIDBase {
			return nil, errs.New(ErrSeedResidentIDRangeCollides, cid, map[string]any{
				"reason":      "SeedResidentIDBase+SeedResidentIDCount reaches citizens.FertilityChildIDBase",
				"maxSeededID": maxSeededID, "fertilityChildIDBase": citizens.FertilityChildIDBase,
			})
		}
	}

	// AC-3: compose is the only real hook registrar, so any pre-existing
	// hook means this engine was already composed (or tampered with).
	// Reject rather than silently append duplicates.
	if e.HookCount() > 0 {
		return nil, errs.New(ErrAlreadyComposed, cid, nil)
	}

	// FEAT-169 id-namespace-seam Wire-time assertion (destructive-review
	// REJECT finding): the ORIGINAL FEAT-169 build only guarded compose's
	// own counter against citizens.FertilityChildIDBase (spawnCitizens'
	// per-mint check below) — that defends compose's [1, 2^62) range but
	// says nothing about the boundary between engine.attract's migrant
	// range [2^62, 2^63) and citizens' fertility range [2^63, ...), which
	// independently collided (both started life at 1<<62). Both sides are
	// compile-time constants today, so this can never actually fail unless
	// a future edit to either package's base breaks the convention — but a
	// silent overlap there is exactly the class of bug that shipped once
	// already, so this checks it explicitly, every Wire call, rather than
	// leaving it to a comment nobody re-reads. See citizens/doc.go and
	// this package's doc.go for the full three-range id map.
	if !idNamespaceRangesDisjoint(citizens.FertilityChildIDBase, attract.MigrantIDBase) {
		return nil, errs.New(ErrIDNamespaceRangesOverlap, cid, map[string]any{
			"fertilityChildIDBase": citizens.FertilityChildIDBase,
			"migrantIDBase":        attract.MigrantIDBase,
		})
	}

	// Resolve every required dependency BEFORE the first registration, so
	// a construction failure never leaves a partially-wired engine (AC-4).
	w := deps.World
	if w == nil {
		w = world.NewWorldAPI(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY})
	}

	c := deps.Citizens
	if c == nil {
		var err error
		c, err = citizens.NewCitizensAPI(e.WorldSeed(), cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens"})
		}
	}

	m := deps.Market
	if m == nil {
		loader := deps.LoadMarket
		if loader == nil {
			loader = market.LoadDefault
		}
		var err error
		m, err = loader(cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "market"})
		}
	}

	// FEAT-083: construct the real baseline-one modules that replace the
	// three original stub slots (consumption/build/attract), plus the
	// finance/households APIs attract's HousingAffordability term consumes.
	// Each is resolved BEFORE the first hook registers, so a construction
	// failure never leaves a partially-wired engine (AC-4).
	consumptionAPI, err := consumption.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	waterNet, err := baselineOneNetwork(consumption.UtilityWater, consumption.SourceReservoir, baselineOneWaterCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	powerNet, err := baselineOneNetwork(consumption.UtilityPower, consumption.SourceSellindgeGrid, baselineOnePowerCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}
	gasNet, err := baselineOneNetwork(consumption.UtilityGas, consumption.SourceOffMapPipeline, baselineOneGasCapacity, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "consumption"})
	}

	seasonAPI, err := season.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "season"})
	}
	// FEAT-087 (mkey feat.deathwave) inc2: wire engine.season into citizens
	// so AdvanceDayTick's once-per-month death-queue realisation can
	// declare a weather emergency (AC-6/AC-7) through the registered
	// feat.deathwave -> engine.season edge. Mirrors buildAPI.SetSeason
	// below exactly -- an unwired citizens.CitizensAPI (e.g. a bare
	// citizens.NewCitizensAPI in an older/unrelated test) simply never
	// declares an emergency, so this wiring is additive-only.
	if err := c.SetSeason(seasonAPI, cid); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens"})
	}
	logisticsAPI := deps.Logistics
	if logisticsAPI == nil {
		var err error
		logisticsAPI, err = logistics.LoadDefault(cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "logistics"})
		}
	}
	buildAPI, err := build.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetWorld(w); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetSeason(seasonAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}
	if err := buildAPI.SetLogistics(logisticsAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}

	financeAPI := finance.NewFinanceAPI(cid)

	// BUG-737 (FEAT-143 wiring): construct the locked-at-startup game
	// initialization mode and install it as finance's mode gate BEFORE
	// seeding opening balances, so Real mode's opening treasury is
	// sourced from data/gameinit.json (gi.StartingCapitalMicropounds,
	// AC-6/GR#15) rather than a bare Go literal, and Unlimited mode's
	// finance bypass (AC-2) is live from the very first tick.
	gi, err := wireGameInit(financeAPI, deps.GameMode, cid)
	if err != nil {
		return nil, err
	}
	// StartingCapitalMicropounds always returns the data-sourced REAL-mode
	// figure regardless of the locked mode (its own doc comment,
	// api.go) — Unlimited's finance bypass makes the seeded balance value
	// irrelevant to that mode's own failure-loop checks, so seeding the
	// same real-mode figure in both modes is deliberate, not an oversight:
	// it keeps genesis deterministic across modes and reproduces every
	// pre-BUG-737 test's treasury expectation byte-for-byte (the data file
	// is kept numerically equal to the old initialTreasury literal — see
	// data/gameinit.json's disclosure).
	openingTreasury, err := gi.StartingCapitalMicropounds(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "gameinit", "step": "StartingCapitalMicropounds"})
	}
	if err := seedOpeningBalances(financeAPI, openingTreasury, initialCitizenWealth); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "finance", "step": "seedOpeningBalances"})
	}
	// BUG-548: grant AcctFirms a working-capital credit line so
	// financeHook can post PRIVATE-sector wages FROM firms (see
	// firmsWageCreditLineMicropounds's doc comment) instead of the
	// treasury paying every wage regardless of sector.
	if err := financeAPI.SetCreditLine(finance.AcctFirms, finance.Money(firmsWageCreditLineMicropounds)); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "finance", "step": "SetCreditLine.AcctFirms"})
	}
	householdsAPI, err := households.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "households"})
	}
	if err := householdsAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "households"})
	}
	// FEAT-1972079927 Q1/Q2: report a coarse, unbounded stock (mirroring
	// the existing baselineOneHousingVacancy placeholder already used for
	// immigration capacity below) against every loaded typology, ONCE at
	// Wire time. Without this, every typology's built-stock reads its
	// zero-value default forever (engine.build never calls ReportStock —
	// no build->households completion bridge exists yet at baseline-one),
	// so UnhousedByPreference (households/api.go) would report EVERY
	// formed household as unhoused-by-preference unconditionally, which
	// alone saturates HousingAffordability's stressed>=total branch to a
	// permanent 0 the moment Q1 forms any households — not a housing
	// shortage signal, just an unmodelled stock floor. This placeholder
	// keeps the term real (driven by overcrowding/rent-burden, which DO
	// vary) rather than pinned to a different constant (0 instead of
	// 100). TODO(FEAT-1972079927 inc2+/build-households bridge): replace
	// with engine.build reporting real completed-dwelling counts.
	for _, typ := range householdsAPI.Typologies() {
		if err := householdsAPI.ReportStock(households.StockCommand{TypologyID: typ.ID, Count: baselineOneHousingVacancy}); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "households", "step": "ReportStock", "typology": typ.ID})
		}
	}
	attractAPI, err := attract.New(baselineOneAttractConfig(), e.WorldSeed(), cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetFinance(financeAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}
	if err := attractAPI.SetHouseholds(householdsAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract"})
	}

	// FEAT-167 (docs/planning/icd/engine.attract-terms.md): construct the
	// three real Safety/LeisureFit/Environment source modules, resolved
	// BEFORE the first hook registers like every other required module
	// above (AC-4 — no partially-wired engine on a construction failure).
	crimeAPI := deps.Crime
	if crimeAPI == nil {
		var cErr error
		crimeAPI, cErr = crime.New(e.WorldSeed(), cid)
		if cErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, cErr, map[string]any{"module": "crime"})
		}
	}

	leisureAPI := deps.Leisure
	if leisureAPI == nil {
		var lErr error
		leisureAPI, lErr = leisure.LoadDefault(cid)
		if lErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, lErr, map[string]any{"module": "leisure"})
		}
	}

	refuseAPI := deps.Refuse
	if refuseAPI == nil {
		var rErr error
		refuseAPI, rErr = refuse.LoadDefault(cid)
		if rErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, rErr, map[string]any{"module": "refuse"})
		}
	}
	// RegisterCell is an upsert (refuse/generate.go): safe to call every
	// Wire, including against an injected/pre-registered *RefuseAPI.
	if err := refuseAPI.RegisterCell(citywideRefuseCellID, refuse.LandUseResidential, "citywide"); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "refuse"})
	}

	attractTerms, err := loadAttractTermsData(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract_terms_data"})
	}

	// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
	// docs/planning/icd/engine.firms-labourmarket.md): construct the two
	// remaining §11 term source modules. Resolved BEFORE the first hook
	// registers, like every other required module above (AC-4).
	servicesAPI := deps.Services
	if servicesAPI == nil {
		var sErr error
		servicesAPI, sErr = services.LoadDefault(cid)
		if sErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, sErr, map[string]any{"module": "services"})
		}
	}
	// FEAT-build-services-bridge-2026-09-02 (edge engine.build->
	// engine.services): wire the completed-building -> service-registration
	// bridge now that servicesAPI exists (buildAPI was constructed earlier,
	// alongside world/season/logistics, before servicesAPI's own
	// construction above) -- mirrors firmsAPI.SetBuild below: additive-only,
	// an unwired *BuildAPI (e.g. a bare build.LoadDefault in an
	// older/unrelated test) simply never attempts a service registration
	// (Tick only consults b.services for an order whose catalogue entry
	// declares a serviceKind).
	if err := buildAPI.SetServices(servicesAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "build"})
	}

	firmsAPI := deps.Firms
	if firmsAPI == nil {
		var fErr error
		firmsAPI, fErr = firms.LoadDefault(e.WorldSeed(), cid)
		if fErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, fErr, map[string]any{"module": "firms"})
		}
	}
	// JobAvailability's LabourMarket() fails closed (MET-G1409) without
	// citizens wired (labourmarket.go's TotalVacancies alone needs no
	// dependency, but LabourMarket's Workforce side does). Finance/market/
	// build are wired too — cheap given all three are already constructed
	// above, and required for any future firm-lifecycle work this module
	// owns beyond the JobAvailability aggregate (out of this ICD's scope).
	if err := firmsAPI.SetCitizens(c); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetFinance(financeAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetMarket(m); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}
	if err := firmsAPI.SetBuild(buildAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms"})
	}

	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): construct the
	// real engine.traffic dependency BEFORE extcommute below, so its
	// TrafficSeam adapter (extCommuteTrafficSeamAdapter, traffic_wire.go)
	// can be built against a live instance instead of the old free-flow
	// stub. Resolved before the first hook registers, like every other
	// required module above (AC-4 — no partially-wired engine on a
	// construction failure).
	loadTraffic := deps.LoadTraffic
	if loadTraffic == nil {
		loadTraffic = loadDefaultTraffic
	}
	trafficAPI, err := loadTraffic(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "traffic"})
	}
	trafficSeam, err := newExtCommuteTrafficSeamAdapter(trafficAPI, cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "traffic"})
	}

	// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): the
	// Wire-time identity-map cross-check MUST run before extcommute's
	// citizens-seam adapter is ever exercised (§3/§11 "identity-map
	// conformance") — checked here, before extcommute is even constructed,
	// so a drift fails loudly with zero hooks left behind (AC-4's
	// discipline extended to this assertion).
	if err := extCommuteEmploymentStatesIdentical(cid); err != nil {
		return nil, err
	}
	extCommuteAPI := deps.ExtCommute
	if extCommuteAPI == nil {
		var xErr error
		extCommuteAPI, xErr = extcommute.LoadDefault(cid)
		if xErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, xErr, map[string]any{"module": "extcommute"})
		}
	}
	if err := extCommuteAPI.SetSeed(e.WorldSeed()); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	if err := extCommuteAPI.SetCitizensSeam(&extCommuteCitizensSeam{api: c, cid: cid}); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	// FEAT-206: TrafficSeam is now the real derivation off the composed
	// *traffic.TrafficAPI (traffic_wire.go's extCommuteTrafficSeamAdapter),
	// replacing the old always-0.0 extCommuteTrafficSeamStub free-flow
	// placeholder (ICD §12 open decision 2 is now closed for this seam;
	// extCommuteTrafficSeamStub itself is left in extcommute_wire.go,
	// unused by Wire, as the documented historical baseline
	// TestExtCommute_TrafficSeamStub_IsFreeFlow still pins).
	if err := extCommuteAPI.SetTrafficSeam(trafficSeam); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}
	if err := extCommuteAPI.SetFinanceSeam(&extCommuteFinanceSeam{
		api: financeAPI,
		cid: cid,
		monthFn: func() int64 {
			clock, cErr := e.Clock()
			if cErr != nil {
				return 0
			}
			return clock.Month()
		},
	}); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "extcommute"})
	}

	// FEAT-1972079941 AC-6: construct the engine.unlocks instance this
	// composition owns so its save.Participant can be assembled
	// (Participants(), save_wire.go). Resolved BEFORE the first hook
	// registers, like every other module above (AC-4 — no partially-wired
	// engine on a construction failure). unlocks has no baseline-one phase
	// hook (its state is driven by milestone/DP commands that are a later
	// item), so it is constructed purely so a composed save/load covers its
	// state — it never ticks.
	unlocksAPI, err := unlocks.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "unlocks"})
	}

	// BUG-689: construct BUG-689/MOD-083's engine.deathservices dependency
	// (the registered feat.compositionroot -> engine.deathservices edge,
	// e2927b5) — resolved BEFORE the first hook registers, like every
	// other module above (AC-4 — no partially-wired engine on a
	// construction failure).
	deathServicesAPI := deps.DeathServices
	if deathServicesAPI == nil {
		loadDeathServices := deps.LoadDeathServices
		if loadDeathServices == nil {
			loadDeathServices = deathservices.LoadDefault
		}
		var dsErr error
		deathServicesAPI, dsErr = loadDeathServices(cid)
		if dsErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, dsErr, map[string]any{"module": "deathservices"})
		}
	}
	// AC-8 (round-3, commit 6a4e210): both registered outbound edges
	// (engine.services for cremation cost posting, engine.logistics for
	// hearse-trip congestion) are wired here, mirroring buildAPI's own
	// SetServices/SetLogistics calls above — an unwired DeathServicesAPI
	// (a bare NewDeathServicesAPI/Load in an older/unrelated test) simply
	// degrades to the documented local-only accounting (api.go's struct
	// doc), so this wiring is additive-only.
	if err := deathServicesAPI.Wire(servicesAPI, logisticsAPI, cid); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "deathservices"})
	}
	// BUG-720 stopgap registration (see Deps.DeathServiceCemeteries' doc
	// comment for why this is a Wire-time seam rather than a real
	// build-completion trigger): register every pre-declared cemetery/
	// crematorium BEFORE any hook ever runs (AC-4's "no partially-wired
	// engine on a construction failure", mirrored here), and remember the
	// ids in deterministic (sorted) order — deathservices exposes no
	// enumeration accessor of its own, so this composition's own roster is
	// the only record of which ids exist to sweep daily.
	deathServiceCemeteryIDs := make([]string, 0, len(deps.DeathServiceCemeteries))
	for _, spec := range deps.DeathServiceCemeteries {
		if spec.Capacity > 0 {
			if err := deathServicesAPI.RegisterCemeteryWithCapacity(spec.ID, spec.Capacity, cid); err != nil {
				return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "deathservices", "step": "RegisterCemeteryWithCapacity", "cemeteryId": spec.ID})
			}
		} else {
			if err := deathServicesAPI.RegisterCemetery(spec.ID, cid); err != nil {
				return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "deathservices", "step": "RegisterCemetery", "cemeteryId": spec.ID})
			}
		}
		deathServiceCemeteryIDs = append(deathServiceCemeteryIDs, spec.ID)
	}
	sort.Strings(deathServiceCemeteryIDs)
	deathServiceCrematoriumIDs := make([]string, 0, len(deps.DeathServiceCrematoria))
	for _, id := range deps.DeathServiceCrematoria {
		if err := deathServicesAPI.RegisterCrematorium(id, cid); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "deathservices", "step": "RegisterCrematorium", "crematoriumId": id})
		}
		deathServiceCrematoriumIDs = append(deathServiceCrematoriumIDs, id)
	}
	sort.Strings(deathServiceCrematoriumIDs)
	// FEAT-087 inc3/BUG-689 (GR#3 SSOT fix, round follow-up G2): wire
	// citizens' FEAT-088 [citizens.DrainCapacity] (ASM-580) through
	// MOD-083's OWN registered composition-root adapter,
	// [deathservices.DeathServicesAPI.WireDrainCapacity], instead of a
	// hand-rolled second closure. WireDrainCapacity passes the module
	// itself (which implements [citizens.DrainCapacity] directly via
	// [deathservices.DeathServicesAPI.MonthlyDrainCapacity]) into the
	// already-registered citizens.CitizensAPI.SetDeathDrainCapacity seam —
	// the live, recomputed-every-call figure (plot capacity + cremation
	// headroom + remaining hearse budget) rather than the old hand-rolled
	// closure's static HearseMonthlyBudget()-only constant. Numerically
	// identical for every current fixture (0 cemeteries + 0 crematoria +
	// 300 hearse budget == 300, TestBUG689_DrainCapacityNeverBindsForDefaultData)
	// but no longer two competing SSOT implementations of one concept.
	//
	// Lock-order note (the round's explicit caveat): this closes a REAL
	// new lock-nesting edge — citizens.DeathQueue.RealiseDrained holds
	// q.mu when it calls q.drain.MonthlyDrainCapacity (deathwave.go), which
	// is now deathservices' own MonthlyDrainCapacity taking d.mu. The old
	// hand-rolled closure called HearseMonthlyBudget (no lock) so this edge
	// did not exist before. Audited: deathservices never calls back into
	// citizens while holding d.mu (Intake/IntakeFromHandoff only consume
	// citizens.RealisedDeath as plain data), so the edge is one-directional
	// (q.mu -> d.mu) and does not invert against any existing d.mu-then-
	// q.mu path — there is none. Re-hammered under -race with concurrent
	// readers during live ticks and save/restore mid-month
	// (TestAttackBUG689_ConcurrentReadersDuringTicks,
	// TestBUG689_WireDrainCapacity_RaceUnderConcurrentTicksAndReads) with no
	// reported inversion.
	if err := deathServicesAPI.WireDrainCapacity(c, cid); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "deathservices", "step": "WireDrainCapacity"})
	}

	// MOD-034 (engine.wellbeing, compose_wellbeing.go): construct and wire
	// the composed WellbeingAPI. Resolved BEFORE the first hook registers,
	// like every other required module above (AC-4 — no partially-wired
	// engine on a construction failure). See compose_wellbeing.go's package
	// doc comment for exactly which seams are live vs degraded, and for the
	// GR#25 finding that the four downstream modifiers are computed but not
	// yet applied to any consumer (the missing engine.citizens/engine.firms/
	// engine.attract -> engine.wellbeing edges).
	wellbeingAPI, wellbeingSeams, err := wireWellbeing(e.WorldSeed(), w, seasonAPI, trafficAPI, cid)
	if err != nil {
		return nil, err
	}

	invReg := invariant.NewRegistry()
	for _, inv := range []invariant.Invariant{
		invariant.NewPeopleInvariant(),
		invariant.NewMoneyInvariant(),
		invariant.NewGoodsInvariant(),
		invariant.NewVehicleInvariant(),
	} {
		if err := invReg.Register(inv); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "invariant"})
		}
	}

	st := &simState{
		e:                          e,
		cid:                        cid,
		seed:                       e.WorldSeed(),
		citizens:                   c,
		world:                      w,
		market:                     m,
		consumption:                consumptionAPI,
		waterNet:                   waterNet,
		powerNet:                   powerNet,
		gasNet:                     gasNet,
		buildAPI:                   buildAPI,
		attract:                    attractAPI,
		finance:                    financeAPI,
		gameInit:                   gi,
		crime:                      crimeAPI,
		leisure:                    leisureAPI,
		refuse:                     refuseAPI,
		services:                   servicesAPI,
		firms:                      firmsAPI,
		deathServices:              deathServicesAPI,
		deathServiceCemeteryIDs:    deathServiceCemeteryIDs,
		deathServiceCrematoriumIDs: deathServiceCrematoriumIDs,
		wellbeingAPI:               wellbeingAPI,
		wellbeingSeams:             wellbeingSeams,
		wellbeingStatus:            computeWellbeingStatus(wellbeingAPI, wellbeingSeams, 0, 0, 0),
		traffic:                    trafficAPI,
		extCommute:                 extCommuteAPI,
		unlocks:                    unlocksAPI,
		attractTerms:               attractTerms,
		leisureVenuesRegistered:    make(map[uint64]bool),
		treasury:                   ledgerBalance(financeAPI, finance.AcctTreasury),
		citizenWealth:              ledgerBalance(financeAPI, finance.AcctHouseholds),
		nextCitizenID:              1,
		seedResidentIDBase:         deps.SeedResidentIDBase,
		seedResidentIDCount:        deps.SeedResidentIDCount,
	}
	// treasury is seeded through setTreasury (never assigned directly)
	// so the BUG-324 publish mirror is correct from before the engine
	// ever ticks. BUG-355: the pot itself comes from the FinanceAPI
	// ledger (seedOpeningBalances above), so the mirror is seeded from
	// that same ledger value, not a literal — a bar that reads £0 for
	// the first frame and then jumps is the same class of wrong number
	// as one that always reads £0.
	st.setTreasury(st.treasury)

	// MOD-034 (engine.wellbeing downstream-effect application): wire the
	// three consumer seams this lane closes — engine.citizens'
	// SetMortalityModifier (no registered engine.citizens ->
	// engine.wellbeing edge, so a plain getter, never a *wellbeing.
	// WellbeingAPI reference), and engine.attract's SetWellbeingModifiers /
	// engine.firms' SetProductivityModifier (both DO have a registered
	// outbound edge to engine.wellbeing, code.json, but still take a plain
	// getter for symmetry — the value each needs is the composition root's
	// monthly cohort-mean reconstruction, wellbeingHook, which only compose
	// can supply). Every getter closes over st itself (not a copy) and
	// reads st.wellbeingStatus, which wellbeingHook.ApplyEffect (compose_
	// wellbeing.go) commits once per month — the split RunShard(read-only,
	// may run concurrently with other hooks' RunShard)/ApplyEffect(commits,
	// sequential in registrationOrder) phase discipline GR#21 already
	// requires of every hook in this file means a getter invoked from
	// another hook's RunShard/cold-pass always reads last month's already-
	// committed value, never a value ApplyEffect is concurrently writing
	// (see compose_wellbeing.go's wellbeingHook doc comment) — the same
	// "snapshotted at month start" contract SetMortalityModifier/
	// SetWellbeingModifiers/SetProductivityModifier's own doc comments
	// require of their callers.
	if err := c.SetMortalityModifier(func() float64 { return st.wellbeingStatus.MortalityModifier }, cid); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens", "step": "SetMortalityModifier"})
	}
	if err := attractAPI.SetWellbeingModifiers(func() (float64, float64) {
		return st.wellbeingStatus.SatisfactionModifier, st.wellbeingStatus.EmigrationModifier
	}); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "attract", "step": "SetWellbeingModifiers"})
	}
	if err := firmsAPI.SetProductivityModifier(func() float64 { return st.wellbeingStatus.ProductivityModifier }); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "firms", "step": "SetProductivityModifier"})
	}

	// Establish the non-zero seed population (AC-8's precondition).
	if err := st.spawnCitizens(0, seedCitizenCount); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "citizens"})
	}
	st.peopleOpening = int64(st.citizens.TotalPopulation(cid))
	st.moneyOpening = num.SatAdd(st.treasury, st.citizenWealth)

	// Route the four gameplay-intent commands (Buy/Zone/Build/Demolish)
	// onto the build/world command surfaces through core's injected seam.
	// This is the single wiring point — the same AC-1 discipline as the
	// phase hooks: no runnable path bypasses compose to reach these.
	if err := e.SetGameplayCommandHandler(st.handleGameplay); err != nil {
		return nil, wrapSeal(cid, err, "build")
	}

	// Wire the engine-owns-journal seam (Aaron's DD, FEAT-1972079852 inc4;
	// edge feat.compositionroot -> harness.replay registered 61e41e9): a
	// nil Deps.CommandJournaler defaults to a freshly constructed
	// *replay.NewRecorder() so the composed engine journals every accepted
	// command from boot — no runnable top (cmd/metropolis,
	// internal/harness/headless) is left with the documented nil-journaler
	// no-op. Deps.CommandJournaler lets tests inject their own
	// core.CommandJournaler (a spy, or one that fails) the same way
	// LoadMarket overrides market construction above.
	journaler := deps.CommandJournaler
	if journaler == nil {
		journaler = replay.NewRecorder()
	}
	// FEAT-1972079936 Phase 1 inc2: when durable persistence is enabled,
	// wrap the resolved journaler with the write-through adapter so every
	// accepted command is ALSO durably appended to the Store. Default-off:
	// deps.PersistStore == nil leaves journaler exactly as resolved above, so
	// every existing test and the default runnable path are byte-for-byte
	// unaffected. The inner journaler stays whatever it already was (the
	// injected Recorder/spy or the default one), so in-memory replay and
	// durable persist both happen.
	if deps.PersistStore != nil {
		city := deps.PersistCity
		if city == (persist.CityKey{}) {
			city = defaultPersistCity // PLACEHOLDER (see defaultPersistCity).
		}
		journaler = newPersistCommandJournaler(journaler, deps.PersistStore, city)

		// BUG-737 (FEAT-143 wiring, round findings P1-3/P1-4/P3): CHECK
		// the composition's own locked gameinit mode against whatever is
		// ALREADY durably on record -- BEFORE this call stamps anything
		// at all, including the world-seed stamp immediately below.
		// checkGameMode's own doc comment explains why the ordering
		// matters: it distinguishes "genuinely fresh city" from "was
		// Wired before but its gamemode.json sidecar is now missing" by
		// reading whether a world seed is ALREADY on record — a signal
		// that would be contaminated (always true) if read AFTER this
		// same call's own SetWorldSeedIfAbsent stamp ran first. gi is
		// guaranteed non-nil and already-validated at this point:
		// wireGameInit ran earlier in this function and would have
		// returned an error on an invalid mode string long before Wire
		// ever reached here -- an invalid mode is NEVER stamped, closing
		// P1-3's own named defect (the pre-fix code used to stamp in
		// cmd/metroserve BEFORE compose.Wire validated anything at all,
		// so a boot with "bogus" left a permanently-poisoned
		// gamemode.json). A refusal here (save.ErrGameModeMismatch,
		// naming both modes) fires on either a genuine cross-restart
		// mismatch (P1-4's "unlimited then real") or a missing sidecar
		// on an already-Wired city (P1-4's "deleted gamemode.json
		// between boots") -- never a silent overrule (P2).
		gameModeWire, err := gi.GameModeWire(cid)
		if err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "gameinit", "step": "GameModeWire"})
		}
		if err := checkGameMode(context.Background(), deps.PersistStore, city, gameModeWire, cid); err != nil {
			return nil, err
		}

		// BUG-488: stamp this composition's own world seed as city's
		// durable ORIGINATING seed the first time persistence is ever
		// wired for it. SetWorldSeedIfAbsent is a no-op once a seed is
		// already on record (e.g. a restore target's Wire call for a
		// city that already has real history) -- it never overwrites --
		// so this is safe to call unconditionally on every Wire, whether
		// this composition goes on to be the genuine originating city or
		// a foreign-seeded restore target. The actual cross-city
		// REFUSAL happens later, in RestoreLatestSnapshotOrGenesis
		// (snapshot.go), which compares e.WorldSeed() against whatever
		// this call (or an earlier one) left on record. A failure here
		// is a durable-store fault, same class as a failed
		// AppendJournal -- surfaced, never swallowed.
		if _, err := deps.PersistStore.SetWorldSeedIfAbsent(context.Background(), city, e.WorldSeed()); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "persist", "step": "stamp-world-seed"})
		}

		// BUG-737: only stamp the gameinit-mode sidecar AFTER
		// checkGameMode above has already passed -- SetGameModeIfAbsent
		// is a no-op once a mode is already on record, safe to call
		// unconditionally.
		if _, err := deps.PersistStore.SetGameModeIfAbsent(context.Background(), city, gameModeWire); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "persist", "step": "stamp-game-mode"})
		}

		// BUG-737 round-2 lead ruling (2026-09-05): mark the mode epoch
		// UNCONDITIONALLY, on every Wire call, whether checkGameMode
		// just took the genesis path (case 3), the legacy-migration path
		// (case 5), or the ordinary match path (case 1) -- idempotent
		// (SetGameModeEpoch is a no-op once already marked), and this is
		// what makes case 4 (a since-deleted gamemode.json on an
		// ALREADY-migrated or genuinely-genesis city) detectable on a
		// LATER boot: only a city whose mode was established under
		// mode-aware code ever gets this marker, so its presence alone
		// distinguishes "was migrated/genesis" from "genuinely never
		// touched" (checkGameMode's own doc comment, snapshot.go).
		if err := deps.PersistStore.SetGameModeEpoch(context.Background(), city); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "persist", "step": "stamp-game-mode-epoch"})
		}
	}
	st.journaler = journaler
	if err := e.SetCommandJournaler(journaler); err != nil {
		return nil, wrapSeal(cid, err, "journaler")
	}

	// Register in the fixed, documented order (AC-2). The slice order IS
	// the contract — nothing here ranges over a map.
	for _, reg := range registrationOrder {
		if reg.name == "invariant" {
			if err := invariant.WireDaily(e, invReg, st.snapshot, deps.InvariantOpts...); err != nil {
				return nil, wrapSeal(cid, err, "invariant")
			}
			continue
		}
		if err := e.RegisterPhaseHook(reg.phase, reg.hook(st)); err != nil {
			return nil, wrapSeal(cid, err, reg.name)
		}
	}

	// FEAT-208: register every view compose publishes, in the fixed,
	// documented viewRegistrationOrder — same "resolve every producer
	// before the first RegisterView call" discipline the phase-hook loop
	// above already applies (AC-4's "zero hooks left behind" extended to
	// "zero views left behind"): every fn(st) closure above was already
	// built when viewRegistrationOrder's literal was constructed, so a
	// RegisterView failure here (e.g. a duplicate name — cannot happen
	// today, the slice's names are all distinct literals) never leaves a
	// partially-registered view table any more than a phase-hook failure
	// leaves a partially-wired engine.
	for _, reg := range viewRegistrationOrder {
		if err := e.RegisterView(reg.name, reg.fn(st)); err != nil {
			return nil, wrapSeal(cid, err, reg.name)
		}
	}

	return &Composition{state: st}, nil
}

// wrapSeal translates a registration failure into the compose-level error.
// A sealed-engine failure (core.ErrEngineSealed, reachable through
// invariant's own ErrWiringAfterSeal wrap) becomes ErrWiringAfterSeal so
// the caller can distinguish "sealed" from any other registration failure
// (AC-6), and every other failure becomes ErrModuleFailed naming the
// module.
func wrapSeal(cid string, err error, module string) error {
	if errors.Is(err, &errs.E{Code: core.ErrEngineSealed}) {
		return errs.Wrap(ErrWiringAfterSeal, cid, err, map[string]any{"module": module})
	}
	return errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": module})
}

// idNamespaceRangesDisjoint is Wire's id-namespace-seam cross-check (FEAT-169
// destructive-review REJECT finding), extracted as a pure function so it is
// independently unit-testable against synthetic values — the real
// constants (citizens.FertilityChildIDBase, attract.MigrantIDBase) cannot
// be overridden to exercise the REJECTING branch of this check any other
// way. Reports whether the fertility child-id range starts at least twice
// as far out as the migrant-id range starts, which — given both ranges
// extend to infinity and migrantBase is itself compose's own lower-range
// boundary starting at 1 — is exactly the condition that keeps
// [migrantBase, fertilityBase) and [fertilityBase, ...) disjoint from
// [1, migrantBase) AND from each other. The historical bug (both bases
// independently at 1<<62) fails this: with migrantBase=2^62,
// fertilityBase(2^62) is NOT >= 2*migrantBase(2^63).
func idNamespaceRangesDisjoint(fertilityChildIDBase, migrantIDBase uint64) bool {
	return fertilityChildIDBase >= 2*migrantIDBase
}

// simState is the composition's shared state. The people/money ledgers
// implement the conservation accounting the invariant checks every tick:
// each ledger records the opening total at the last daily check plus the
// tracked delta accumulated since, and the invariant's SnapshotProvider
// (snapshot below) verifies Closing - Opening == TrackedDelta against the
// live store, then closes the period.
//
// # No mutex, by the same discipline as invariant.Hook
//
// simState holds no sync.Mutex. Every access to simState's OWN plain
// fields (treasury, citizenWealth, peopleDelta, moneyDelta, ...) is
// single-goroutine by construction: only shard 0 of each hook's RunShard
// touches them (the invariant's SnapshotProvider, the spawn/finance
// ApplyEffect barrier work), and the phase pipeline runs phases
// sequentially — the daily phase's det.RunPhase joins its workers before
// the monthly phases start.
//
// CORRECTION (F3, independent round r1, FEAT-208 increment 1): this
// "single-goroutine by construction" property does NOT extend to
// buildServicesCapacityDemandPatch (services_publish.go) or any future
// view-publishing method this file adds — those run on the subscription
// pump goroutine (engine/core.StartSubscriptionPump), CONCURRENTLY with
// the phase-pipeline goroutines this comment describes, not sequenced
// with them. That is safe ONLY because those methods read through the
// held module's OWN synchronization (st.services is a
// *services.ServicesAPI, and every accessor it exposes — ServiceIDs,
// Capacity, Demand — takes its own sync.RWMutex internally); they never
// touch simState's own unguarded plain fields. See
// engine/core.ViewPatchFunc's doc comment (subscribe.go) for the general
// contract this specific case satisfies. A future ViewPatchFunc-backed
// method that read one of simState's own plain fields directly (e.g.
// st.treasury) WOULD be a real, unguarded data race against the phase
// pipeline — this file's discipline (§3.3 of the design) of only ever
// reading through an already-guarded *XxxAPI accessor is load-bearing,
// not incidental.
//
// A mutex on simState itself would be a copy hazard with no copy risk to
// guard (and would make this type an astgate SEC-020 candidate for
// nothing) — the fix for the concurrency gap above is "read through the
// module's own lock," never "add a lock here."
//
// BUG-324 addendum: the top bar needs the player's MONEY, and the
// correction above is precisely why it could not simply read
// st.treasury. It also could not read engine.finance's AcctTreasury
// ledger account instead: baseline one never funds that account (Wire
// seeds st.treasury, and financeHook moves st.treasury — the finance
// ledger's own accounts stay at zero, which is why "f2.finance"'s
// balance sheet publishes zeros today too). So the money the player
// actually has lives in an unguarded plain field, and both honest
// alternatives — publish a zero, or drop money from the bar — were
// worse than making the real figure safely readable. treasuryPub below
// is that: a publish-only atomic MIRROR of st.treasury, written by
// setTreasury alongside every st.treasury write, read lock-free by the
// subscription pump. It is not a second source of truth (nothing in the
// simulation ever reads it) and it cannot drift, because st.treasury is
// never assigned outside setTreasury.
type simState struct {
	e    *core.Engine
	cid  string
	seed uint64

	// journaler is the composed engine's engine-owns-journal seam
	// (Deps.CommandJournaler, injected into e via e.SetCommandJournaler
	// below). Stored here too, mirroring finance/traffic's own doc
	// comments, so Composition.Journaler() can hand tests the same
	// instance the composed engine actually records into.
	journaler core.CommandJournaler

	citizens *citizens.CitizensAPI
	world    *world.WorldAPI
	market   *market.MarketAPI

	// real baseline-one modules (FEAT-083): consumption/build/attract
	// replace the three original stub slots. (finance/households are also
	// constructed in Wire and handed to attract via its SetFinance/
	// SetHouseholds seam — attract holds those references, so simState does
	// not re-store them.)
	consumption *consumption.UtilityAPI
	waterNet    *consumption.Network
	powerNet    *consumption.Network
	gasNet      *consumption.Network

	buildAPI *build.BuildAPI
	attract  *attract.AttractAPI

	// finance is the shared *finance.FinanceAPI instance constructed in
	// Wire and handed to attract (SetFinance) and, since FEAT-207, to
	// extcommute's FinanceSeam adapter (extCommuteFinanceSeam). Stored here
	// too so any future compose-owned poster (and tests) can reach the
	// same ledger without re-threading it through every hook constructor.
	finance *finance.FinanceAPI

	// gameInit is BUG-737's FEAT-143 wiring: the *gameinit.GameInit
	// constructed once in Wire (compose_gameinit.go) and installed as
	// finance's mode gate (financeAPI.SetModeGate). Stored here so
	// Composition.GameMode() (GR#17) and Save/Load's ctx.GameMode /
	// WithExpectedGameMode threading (save_wire.go) can reach the same
	// locked instance without re-constructing it. Never nil after a
	// successful Wire — see wireGameInit's own doc comment.
	gameInit *gameinit.GameInit

	// FEAT-167 (docs/planning/icd/engine.attract-terms.md): the three real
	// Safety/LeisureFit/Environment source modules, plus this integration's
	// one new data-driven balance file. attractTerms is read-only after
	// Wire (loadAttractTermsData runs once, at construction) — never
	// re-read per tick.
	crime        *crime.CrimeAPI
	leisure      *leisure.LeisureAPI
	refuse       *refuse.RefuseAPI
	attractTerms attractTermsData

	// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
	// docs/planning/icd/engine.firms-labourmarket.md): the two remaining
	// §11 term source modules, constructed in Wire alongside
	// crime/leisure/refuse above.
	services *services.ServicesAPI
	firms    *firms.FirmsAPI

	// deathServices is BUG-689's engine.deathservices instance (the
	// registered feat.compositionroot -> engine.deathservices edge,
	// e2927b5): constructed in Wire, wired to services/logistics, and
	// injected as citizens' [citizens.DrainCapacity] (deathServicesHook
	// below drives its monthly Intake). Stored here — mirroring every
	// other module field's own doc comment — so Participants()
	// (save_wire.go) and Composition.DeathServices() (test/inspection
	// accessor) can reach the same instance.
	deathServices *deathservices.DeathServicesAPI

	// handoffCursorCheckDone / lastCheckedHandoffCursor (BUG-725 P2
	// follow-up, refined after opus-round-bug725's re-round) together gate
	// intakeDeathServices' full-citizens-handoff-stream over-length-cursor
	// check (below) so it does NOT re-run on every subsequent caught-up
	// month once a given cursor VALUE has already been confirmed in-range
	// -- a long-lived, low-mortality city can otherwise spend one full
	// O(stream) DeathHandoff copy per caught-up month for its entire life
	// (24 caught-up months == 24 full copies, measured).
	//
	// This is deliberately keyed on the CURSOR VALUE, not a one-shot
	// "checked since Load" latch: the round's own
	// TestAttackBUG725_BoundaryLenVsLenPlusOne proves the cursor CAN move
	// again after the first check without a fresh Load -- its len+1 case
	// advances the cursor via a direct IntakeFromHandoff call (bypassing
	// intakeDeathServices entirely), the same shape any future caller
	// reaching IntakeFromHandoff outside the monthly hook would have. A
	// one-shot "ever" latch would let that second, genuinely-impossible
	// cursor value sail through unchecked for the rest of the load's
	// lifetime -- worse than the original defect, because it would look
	// fixed. Re-checking whenever the observed cursor differs from the
	// last CONFIRMED-in-range value costs nothing extra in the common
	// steady-state case (the cursor does not change while caught up) and
	// re-verifies correctly the instant it does.
	//
	// Reset by Composition.Load (save_wire.go) on every successful load,
	// so a freshly restored bundle's decoded cursor (never previously
	// seen by this *simState) is always re-checked at least once,
	// regardless of what value a PRIOR load on the same instance last
	// confirmed.
	handoffCursorCheckDone   bool
	lastCheckedHandoffCursor int64

	// MOD-034 (engine.wellbeing, compose_wellbeing.go): wellbeingAPI is the
	// composed WellbeingAPI instance, wellbeingSeams is the fixed-order
	// live/degraded report from wireWellbeing, and wellbeingStatus is the
	// last monthly reconstruction's cohort mean + computed (not applied —
	// see compose_wellbeing.go's package doc comment) downstream modifiers.
	wellbeingAPI    *wellbeing.WellbeingAPI
	wellbeingSeams  []wellbeingSeamStatus
	wellbeingStatus WellbeingStatus

	// deathServiceCemeteryIDs / deathServiceCrematoriumIDs (BUG-720) remain
	// EXACTLY the Wire-time stopgap roster the original doc described:
	// deterministic (sorted, GR#21) ids registered ONLY from
	// Deps.DeathServiceCemeteries/DeathServiceCrematoria at Wire time — the
	// only route to give a cemetery a non-default plot capacity, since
	// RegisterCemeteryWithCapacity has no build-completion-driven equivalent
	// (see Deps.DeathServiceCemeteries' own doc comment). Deliberately NEVER
	// touched by Load/BUG-743's bridge — these ids are constructor-time
	// configuration, not save-restorable STATE, so a rewind to an earlier
	// save must not affect them (mirrors how Wire's other one-shot
	// registrations, e.g. the catalogue itself, are never part of any
	// save). See deathServiceBridgeCemeteryIDs immediately below for the
	// LIVE, build-completion-driven counterpart BUG-743 adds.
	deathServiceCemeteryIDs    []string
	deathServiceCrematoriumIDs []string

	// deathServiceBridgeCemeteryIDs / deathServiceBridgeCrematoriumIDs
	// (BUG-743) are the deterministic (sorted, GR#21) roster of every
	// cemetery/crematorium id THIS composition has registered with
	// engine.deathservices via the LIVE build->deathservices bridge
	// (runDeathServiceBuildingRegistry, compose_buildregistry.go, called
	// from buildHook.ApplyEffect below) — closing BUG-720's own documented
	// gap #1 ("no ServiceKind-declaring catalogue building... can ever be
	// registered... via the live player build path") for cemetery/
	// crematorium specifically. deathservices exposes no enumeration
	// accessor of its own (RegisterCemetery/RegisterCrematorium only ever
	// insert into unexported maps), so this roster — kept SEPARATE from the
	// Wire-time stopgap roster above — is the only record of which
	// build-order-derived ids exist. deathServicesRunHook's daily
	// RunHearseTransport/Cremate sweep (runDeathServices) iterates BOTH
	// rosters; DeathServicesRunStatus's counts sum both too.
	//
	// UNLIKE the Wire-time roster, this one IS genuinely save-restorable
	// state: it is added to the moment engine.build's completion feed
	// reports a landing and removed the moment the demolition feed reports
	// a teardown, so a rewind to an earlier save correctly drops an id
	// registered after that save point (the SAME "no phantom instance
	// survives a rewind" property TestServicesBridge_RewindDropsPhantomInLiveComposition
	// already proves for engine.services). Persisted via
	// composeLedgerParticipant (compose_ledger_participant.go) alongside
	// buildRegistryCursor/buildDemolitionCursor.
	deathServiceBridgeCemeteryIDs    []string
	deathServiceBridgeCrematoriumIDs []string

	// buildRegistryCursor / buildDemolitionCursor (BUG-743) are this
	// composition's own persisted cursors over engine.build's completion/
	// demolition feeds ([build.BuildAPI.CompletedBuildings]/
	// [build.BuildAPI.DemolishedSince]) — the highest CompletionSeq/
	// DemolitionSeq runDeathServiceBuildingRegistry has ever consumed.
	// Exactly the DeathHandoffSince/handoffCursor cursor idiom (BUG-689),
	// mirrored for the build->deathservices registration bridge instead of
	// the citizens->deathservices intake bridge. Persisted via
	// composeLedgerParticipant so a Load resumes EXACTLY where the live
	// composition left off — never redelivering an already-processed
	// completion/demolition, never skipping one taken between two saves.
	buildRegistryCursor   build.BuildOrderID
	buildDemolitionCursor build.BuildOrderID

	// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): the composed
	// engine.traffic dependency (traffic_wire.go). trafficTickHook calls
	// AdvanceTick on it once per simulated day; extCommuteTrafficSeamAdapter
	// (constructed in Wire, held by extCommute's TrafficSeam field, not
	// here) reads its CommuteHours live. Stored here too — mirroring
	// finance's own doc comment above — so a future demand-generating
	// hook this package adds, and today's tests via Composition.Traffic(),
	// can reach the same instance without re-threading it.
	traffic *traffic.TrafficAPI

	// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): the
	// off-map external-commuting module, wired with its three seam
	// adapters (extcommute_wire.go). Baseline one routes no gameplay
	// command to Assign/Release/InCommute yet (ICD §12 open decision 4 —
	// out of this ICD's scope); this field exists so a future gameplay
	// seam, and today's tests, can reach it.
	extCommute *extcommute.ExtCommuteAPI

	// FEAT-1972079941 AC-6 (save live-wiring): the engine.unlocks module
	// instance this composition owns SO its save.Participant can be
	// assembled in Participants() (save_wire.go). Baseline one routes no
	// phase hook or gameplay command to unlocks yet (its state is driven by
	// milestone/DP commands that are a later item), so it is constructed
	// purely so the composed engine's save covers unlocks' state too — the
	// same "own the instance so it can be saved" reason every other module
	// above is reachable here.
	unlocks *unlocks.UnlocksAPI

	// leisureVenuesRegistered tracks which completed engine.build
	// ZoneEntertainment order IDs are CURRENTLY bridged into an open
	// engine.leisure venue (registerLeisureVenues below) — membership is
	// removed again when the underlying structure is demolished (destructive
	// round r1 F2 fix), so this is a live "still open" set, not a
	// once-true-forever registration log.
	leisureVenuesRegistered map[uint64]bool

	// people conservation ledger
	peopleOpening int64
	peopleDelta   int64

	// money conservation ledger (total money = treasury + citizen wealth)
	moneyOpening  int64
	moneyDelta    int64
	treasury      int64
	citizenWealth int64

	// treasuryPub is the BUG-324 publish-only mirror of treasury — see
	// this type's own doc comment for why it exists and why it cannot
	// drift. Write it ONLY via setTreasury; read it ONLY from a
	// ViewPatchFunc (publishedTreasury). The simulation itself must keep
	// reading treasury, so that a stale/forgotten mirror can never
	// change a simulated outcome, only a displayed one.
	treasuryPub atomic.Int64

	// cumulative gross money flow (AC-9)
	moneyFlows int64

	// housingAffordability is FEAT-1972079927 Q2's liveness evidence: the
	// HousingAffordability figure captured DURING applyMigration, in the
	// same instant SetTermInputs freshly snapshotted the current
	// household-id set — never re-queried later. AttractAPI's own
	// HousingAffordability() accessor re-reads against whatever
	// household-id snapshot it was LAST given, which can go stale within
	// the same month if that month's own emigration dissolves a household
	// formed a moment earlier (its last member departs) — capturing the
	// value here, right after SetTermInputs, avoids that race entirely,
	// and gives F2/tests a stable "this month's affordability" reading.
	housingAffordability float64

	// cumulative consumption delivered (liveness evidence) and net
	// migration applied (liveness evidence) — the "consumption draws" and
	// "migration is attractiveness-driven" observables.
	consumptionDelivered float64
	netMigration         int64

	nextCitizenID uint64

	// seedResidentIDBase/seedResidentIDCount (BUG-665) mirror
	// Deps.SeedResidentIDBase/SeedResidentIDCount verbatim — see that
	// field's doc comment. seedResidentIDCount == 0 (the default) means
	// liveResidentIDs() computes exactly what it always did.
	seedResidentIDBase  uint64
	seedResidentIDCount int64

	// liveResidentIDsCache/liveResidentIDsCacheMigrants/
	// liveResidentIDsCacheChildren/liveResidentIDsCacheNextID are
	// BUG-547's allocation-regression fix for liveResidentIDs() (see that
	// method's own doc comment for the full BUG-529 union it computes).
	// BUG-529 made liveResidentIDs() the enumeration
	// markEmploymentAndCount/formResidentHouseholds/
	// distributeWagesToResidents/employedResidentCount (moneycirc.go) all
	// call, independently, every time they run — perfci measured this as
	// +278% alloc bytes / +394% alloc count per tick (~4x the pre-BUG-529
	// baseline, one full-population slice build per caller). This cache
	// memoises the LAST computed union against the exact three live
	// counters it is a pure function of (residentIDs()'s own nextCitizenID
	// plus attract's/citizens' live migrant/fertility-child counters): a
	// cache hit is returned ONLY when all three still match what produced
	// the cached slice, so the memo is invalidated automatically and
	// EXACTLY when the true answer would differ — never on a wall-clock or
	// tick-boundary guess, which would have been wrong here (migrants get
	// admitted mid-month, between formResidentHouseholds' PhasePopulation
	// call and markEmploymentAndCount's later PhaseFinance call in the
	// SAME tick, so a naive "compute once per tick" cache would have
	// delayed a freshly-admitted migrant's employment marking by a whole
	// month — a real, if subtle, correctness regression this
	// counter-keyed design cannot introduce). Deliberately NOT part of any
	// snapshot payload (mirrors liveResidentIDs' own doc comment on why a
	// compose-tracked counter must never be persisted): a zero-value cache
	// miss-compares against the live counters after every LoadAt/restore
	// (vanishingly unlikely to coincide with a real 0/0/0 state) and
	// simply recomputes the full union on the next call, so restore
	// correctness never depends on this field surviving a restore.
	liveResidentIDsCache         []uint64
	liveResidentIDsCacheMigrants uint64
	liveResidentIDsCacheChildren uint64
	liveResidentIDsCacheNextID   uint64

	// vitalBirths/vitalDeaths are the cumulative real fertility/mortality
	// totals folded into peopleDelta so far (liveness evidence, mirrors
	// consumptionDelivered/netMigration above) — the "births/deaths are
	// real, not the old flat-8 fake" observable (FEAT-169). Folded one
	// day-tick's own totals at a time, straight from AdvanceDayTick's
	// return values — see coldPassHook.ApplyEffect's doc comment for why
	// this is NOT batched to the month boundary via VitalEvents.
	vitalBirths int64
	vitalDeaths int64

	// lastClosedTick tracks the last tick for which ledgers were closed (BUG-288).
	// snapshot() uses this to ensure ledger closing (opening/delta reset) happens
	// exactly once per tick, at the START of that tick's snapshot call.
	lastClosedTick int64

	// Previous closing values from the last snapshot, used to set opening for the
	// current tick before any deltas are recorded.
	previousClosingPop   int64
	previousClosingMoney int64

	// --- FEAT-1972079927 inc2: firms-pay-construction (moneycirc_inc2.go) ---

	// buildersMerchantFirmID is the auto-placed builders'-merchant firm's
	// ID once the Industry&Farms trigger has fired, or 0 before it fires.
	// Set exactly once (maybeAutoPlaceBuildersMerchant is idempotent) —
	// there is at most one auto-placed merchant per city at Baseline One.
	buildersMerchantFirmID firms.FirmID

	// materialsDrawnCumulative is the running total (all orders, all time)
	// of engine.build's own BuildOrder.MaterialsDrawn snapshot field, as of
	// the last accrueConstruction call — the baseline this file diffs
	// against to find THIS tick's newly-drawn tonnage (engine.build's Tick
	// exposes no per-tick delta directly, only the cumulative snapshot).
	materialsDrawnCumulative int64

	// constructionAccrualLocal/constructionAccrualExternal are the NET-90
	// (COMMERCIAL_PAYMENT_TERM_TICKS) accrued-but-not-yet-settled
	// construction cost, split by source at the moment each tonne was
	// drawn (local merchant vs imported) — settled and zeroed together at
	// every 90-tick boundary by settleConstructionAccrual.
	constructionAccrualLocal    int64
	constructionAccrualExternal int64

	// constructionSettledLocal/constructionSettledExternal are cumulative
	// liveness evidence (test/UI observable) of every settlement this run
	// has posted, split by source — never fed back into the accrual math.
	constructionSettledLocal    int64
	constructionSettledExternal int64
}

// closeLedgerForTick closes the ledger for the given tick at the START of
// snapshot, before reading state. Uses previousClosing values (set by the
// previous snapshot) as opening for the current tick. Ensures ledger closing
// happens exactly once per tick despite snapshot being called twice on the
// same tick (BUG-288).
func (st *simState) closeLedgerForTick(tick int64) {
	if st.lastClosedTick >= tick {
		return
	}
	// Set opening for this tick to the previous tick's closing.
	// Deltas are reset AFTER reading the snapshot, not here.
	st.peopleOpening = st.previousClosingPop
	st.moneyOpening = st.previousClosingMoney
	st.lastClosedTick = tick
}

// snapshot implements invariant.SnapshotProvider: it builds this tick's
// conservation Snapshot. PURE: calling it twice on same tick returns identical
// snapshot (ledger closing happens once at snapshot START via closeLedgerForTick).
// Called from the invariant hook's RunShard (shard 0 only) — single reader/writer,
// no map iteration, no wall clock (BUG-288).
func (st *simState) snapshot(tick int64) invariant.Snapshot {
	st.closeLedgerForTick(tick)
	closingPop := int64(st.citizens.TotalPopulation(st.cid))

	s := invariant.NewSnapshot(tick)
	s.Readings[invariant.StockPeople] = invariant.StockReading{
		Registered:   true,
		Opening:      st.peopleOpening,
		Closing:      closingPop,
		TrackedDelta: st.peopleDelta,
	}

	totalMoney := num.SatAdd(st.treasury, st.citizenWealth)
	s.Readings[invariant.StockMoney] = invariant.StockReading{
		Registered:   true,
		Opening:      st.moneyOpening,
		Closing:      totalMoney,
		TrackedDelta: st.moneyDelta,
	}

	// goods and vehicles are genuinely zero in baseline one (market has no
	// goods flow yet, traffic does not exist). Report them registered at
	// zero so the full suite runs and balances every tick (AC-10) rather
	// than being skipped.
	s.Readings[invariant.StockGoods] = invariant.StockReading{Registered: true}
	s.Readings[invariant.StockVehicles] = invariant.StockReading{Registered: true}

	// Store closing values for use as opening in the next tick's snapshot.
	// This must happen after reading the snapshot but before returning,
	// so that the NEXT snapshot call can use these values via closeLedgerForTick.
	st.previousClosingPop = closingPop
	st.previousClosingMoney = totalMoney

	// Reset deltas for the next tick. This happens AFTER reading the snapshot
	// so that the snapshot includes the deltas accumulated during this tick.
	// (Deltas are accumulated during the tick via life events, transactions, etc.)
	st.peopleDelta = 0
	st.moneyDelta = 0

	return s
}

// spawnCitizens births count citizens at the given sim month, deterministically
// (sequential IDs, personality drawn from the world seed via
// citizens.InitPersonality). It is the only citizen-mutation path the
// composition uses, routed through CitizensAPI's command surface (GR#20).
func (st *simState) spawnCitizens(month int64, count int) error {
	for i := 0; i < count; i++ {
		id := st.nextCitizenID
		// FEAT-169 ID-SEAM GUARD: id must stay inside compose's own range
		// of the three-way disjoint id map — [1, attract.MigrantIDBase) —
		// never reaching either engine.attract's migrant range
		// [MigrantIDBase, FertilityChildIDBase) or engine.citizens'
		// fertility range [FertilityChildIDBase, ...). Bounded against
		// MigrantIDBase (2^62), NOT FertilityChildIDBase (2^63):
		// destructive-review REJECT found the ORIGINAL guard here checked
		// only the fertility boundary, which would have let compose's
		// counter silently drift into attract's migrant range first
		// without ever tripping. The three id spaces are a documented,
		// verified-disjoint CONTRACT (ICD §12 open decision 2, amended),
		// not a shared allocator. Checked on every mint (cheap: one uint64
		// comparison), including the seed population minted at Wire time —
		// so this single check doubles as both the "startup check" and the
		// "every mint" assertion the ICD calls for, rather than two
		// separate code paths.
		if id >= attract.MigrantIDBase {
			return errs.New(ErrCitizenIDNamespaceSeam, st.cid, map[string]any{
				"id":   id,
				"base": attract.MigrantIDBase,
			})
		}
		st.nextCitizenID++
		// BUG-517: the seed population is a founding city's residents, not
		// a nursery — it must arrive with a realistic UK-like age spread,
		// not uniformly age 0. The age is drawn deterministically (never
		// math/rand or time) from citizens' age pyramid, keyed on this
		// citizen's own id and the mint month so two runs draw identical
		// ages for identical citizens (GR#21).
		age := citizens.DrawAgeAtCreationMonths(st.seed, id, month)
		cit := citizens.Citizen{
			ID:          id,
			BirthMonth:  citizens.BirthMonthForAge(month, age),
			Personality: citizens.InitPersonality(st.seed, id, month, citizens.Personality{}, citizens.Personality{}),
		}
		if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: st.cid,
			Kind:          citizens.LifeEventBirth,
			Citizen:       cit,
		}); err != nil {
			return err
		}
	}
	return nil
}

// noopHook is the PhaseHook for a real module whose tick behaviour is not
// yet built (world: terrain/ownership store; market: price registry). It
// satisfies core.PhaseHook with zero work: RunShard touches only
// shard-local scratch (nothing), ApplyEffect is a no-op, both
// deterministic. Documented in doc.go's STUB-FOR-BASELINE section.
type noopHook struct {
	name string
	st   *simState
}

func (noopHook) RunShard(shard int) ([]core.Effect, error) { return nil, nil }
func (noopHook) ApplyEffect(core.Effect)                   {}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard is a
// nil-op for every shard including 0, so it trivially only "does work"
// on shard 0 (none at all).
func (noopHook) SingleShard() bool { return true }

// coldPassEffect is the daily citizens cold-pass tick marker (FEAT-169).
// Carries no payload data of its own — AdvanceDayTick/VitalEvents derive
// everything they need from citizens' own internal state — it exists only
// to move the "run the cold pass" instruction from RunShard (shard 0) to
// ApplyEffect (the single-goroutine barrier), the same shape every other
// hook in this file uses.
type coldPassEffect struct{}

// coldPassHook is the PhaseHook for citizens' REAL cold pass — per-citizen
// mortality plus FEAT-160 fertility, via CitizensAPI.AdvanceDayTick — REPLACING
// the old spawnHook fake (a flat monthlyBirths=8 births/month with no
// connection to demographics, mortality, or eligibility). Registered
// against core.PhaseDailyTick (see registrationOrder's comment and
// doc.go's "Live-tick wiring" section): AdvanceDayTick already runs
// unconditionally once per day-tick internally to citizens (its own
// amortised 1/30-shards-per-day schedule), and the ICD's T0 update class
// requires the resulting births/deaths land in peopleDelta the SAME tick
// they are computed. Only shard 0 emits the effect; ApplyEffect drives the
// cold pass and folds THAT TICK's own births/deaths (AdvanceDayTick's
// return values, not VitalEvents' monthly-completed totals — see
// ApplyEffect's doc comment for why) into the people conservation ledger
// every day-tick — exactly the role attractHook plays for migration
// admits, just at daily rather than monthly granularity.
type coldPassHook struct {
	st *simState
}

func (h *coldPassHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: coldPassEffect{}}}, nil
}

func (h *coldPassHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(coldPassEffect); !ok {
		return
	}
	st := h.st
	births, deaths, err := st.citizens.AdvanceDayTick(st.cid)
	if err != nil {
		// AdvanceDayTick's only real failure mode is a copied-handle
		// rejection (MET-G004), which cannot happen given compose's
		// single-owner st.citizens field; log loudly rather than swallow
		// (GR#1) instead of a silent no-op.
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "cause": err.Error()})
		return
	}

	// Fold THIS TICK's own births/deaths into peopleDelta immediately — NOT
	// batched to the month boundary via VitalEvents. ICD deviation, with
	// reason (docs/planning/icd/engine.citizens-coldpass.md §4/§5): the
	// ICD's own §4 floated "pull VitalEvents at the month boundary" as one
	// option, but AdvanceDayTick's mortality/fertility mutations land on
	// the cold store incrementally, one amortised shard-slice per day-tick
	// (A2's amortised cold pass) — so batching the peopleDelta credit to
	// month-end would defer it past the tick the population actually
	// changed on, violating §5's T0 same-tick requirement. This was not
	// theoretical: the deferred-batch design was built first and the
	// invariant's daily conservation check (WithLogSink) caught real
	// violations on every day a death/birth landed outside the last day of
	// the month. AdvanceDayTick's return values (this call's own totals)
	// fix that at the source — see its doc comment.
	st.peopleDelta = num.SatAdd(st.peopleDelta, int64(births))
	st.peopleDelta = num.SatSub(st.peopleDelta, int64(deaths))
	st.vitalBirths = num.SatAdd(st.vitalBirths, int64(births))
	st.vitalDeaths = num.SatAdd(st.vitalDeaths, int64(deaths))
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0. This matches the ICD's §6 Shard
// Scope: AdvanceDayTick is single-call/opaque from compose's point of
// view even though citizens fans its own internal parallel mortality pass
// and sequential fertility pass across many cold shards INSIDE that one
// call — entirely invisible to this hook.
func (h *coldPassHook) SingleShard() bool { return true }

// financeEffect is the monthly finance stub's tick marker.
type financeEffect struct{}

// financeHook is the baseline-one finance stub: a budget-closing wage/tax
// transfer. Wages move treasury -> citizen wealth; tax moves citizen
// wealth -> treasury. The net money change is zero (the budget closes), so
// the conserved total is unchanged while the gross flow (AC-9) grows.
type financeHook struct {
	st *simState
}

func (h *financeHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: financeEffect{}}}, nil
}

func (h *financeHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(financeEffect); !ok {
		return
	}
	st := h.st

	// BUG-355: the ledger F2 reads is FinanceAPI. Open the monthly
	// finance tick FIRST (finance.BeginMonth resets the per-tick
	// transaction log that WagesPosted/TaxRevenue aggregate over) — with
	// no BeginMonth caller the log never cleared, WagesPosted read an
	// ALL-TIME cumulative sum, and attract's HousingAffordability divided
	// that ever-growing figure by households: migrant attractiveness grew
	// linearly forever and the log leaked memory unboundedly. PhaseFinance
	// is the LAST monthly phase (core.MonthlyPhaseOrder), so the tick
	// opened here holds exactly this month's posts when NEXT month's
	// population phase reads WagesPosted — always one month's wage bill.
	var flowed int64
	// preTreasury/preHouseholds snapshot the tracked money stock (see
	// invariant.StockMoney / this hook's moneyDelta line below) BEFORE any
	// of this month's legs post, so the actual delta can be measured
	// directly from the ledger rather than hand-summed leg by leg — this
	// is what FEAT-1972079927 inc1's new legs (Q4 consumption spend +
	// commercial/industrial/council tax) need: unlike the original wage/
	// tax pair (an internal treasury<->households transfer that always
	// nets to zero on the tracked stock), spend crosses OUT of the tracked
	// stock into AcctFirms (untracked by invariant.StockMoney by design —
	// see syncMoneyFromLedger's doc comment) and the new tax legs bring
	// money back IN from firms — a net non-zero delta that must be
	// measured, not assumed.
	preTreasury, preHouseholds := st.treasury, st.citizenWealth
	if st.finance != nil {
		clock, cErr := st.e.Clock()
		if cErr != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "cause": cErr.Error()})
			return
		}
		if err := st.finance.BeginMonth(clock.Month()); err != nil {
			// BeginMonth only fails on a copied handle, which compose's
			// single-owner field makes unreachable; log loudly rather
			// than swallow (GR#1) and skip this month's posting rather
			// than post into a stale tick window.
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "cause": err.Error()})
			return
		}
		// FEAT-083 de-stub: mark this month's resident employment BEFORE
		// sizing the wage bill (moneycirc.go's markEmploymentAndCount doc
		// comment) — the wage bill is employedCount x the real UK gross
		// wage, not a flat constant, so employment must be decided first.
		employed, employedPublic, empErr := st.markEmploymentAndCount(clock.Month())
		if empErr != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "op": "markEmploymentAndCount", "cause": empErr.Error()})
		}

		// BUG-745: run engine.firms' own monthly resolution and read the
		// resulting city-wide output scale (moneycirc.go's
		// resolveFirmsMonth doc comment) BEFORE sizing the consumption/tax
		// leg below — a firm's OutputScale (AC-8's market-input-
		// availability scale, now ALSO folded with MOD-034's wellbeing
		// ProductivityModifier as of 57fc437's firms/lifecycle.go —
		// applyInputScalingLocked multiplies the two) previously never
		// reached anything compose posts; this is the seam that finally
		// routes it into consumption revenue (postConsumptionAndTax). The
		// private wage bill below is deliberately NOT scaled by this — see
		// that leg's own doc comment for the destructive credit-line/
		// emigration cascade this was measured to trigger.
		outputScale := st.resolveFirmsMonth(clock.Month())

		// FEAT-1972079927 Q4 + Aaron's 2026-08-31 diversify-the-base steer
		// (BUG-391): monthly consumption spend (households -> firms) plus
		// the commercial/industrial tax legs that bring money back from
		// firms to the treasury, and the flat residential council-tax leg
		// — see moneycirc.go's postConsumptionAndTax doc comment. BUG-548
		// (2026-09-05): moved AHEAD of the wage posting below (was after)
		// so firms hold this month's household-spend revenue BEFORE
		// PostWagesFromFirms draws on AcctFirms — real payroll-after-
		// revenue ordering, and it shrinks (never eliminates — see
		// firmsWageCreditLineMicropounds's doc comment) reliance on the
		// firms credit line. Neither leg reads the other's output, so the
		// reorder changes only WHEN money moves within this same month's
		// tick, not what moves. BUG-745: the revenue this leg posts is now
		// scaled by outputScale (moneycirc.go's postConsumptionAndTax doc
		// comment) — a productivity collapse means firms have less to
		// sell, so less spend clears.
		flowed = num.SatAdd(flowed, st.postConsumptionAndTax(outputScale))

		// BUG-548: the treasury previously paid the ENTIRE wage bill
		// regardless of sector, with no business->worker flow at all, and
		// then immediately clawed back 100% of it via a fake IncomeRate:
		// 10000 (100%) "tax" — a self-cancelling round-trip that inflated
		// the gross-flow metric (AC-9) while doing nothing real, while
		// distributeWagesToResidents (below) separately credited citizens'
		// per-citizen Wealth net of a real 28% (incomeNITaxRateBp)
		// deduction that was never posted anywhere — it simply vanished.
		// Fixed flow: PRIVATE-sector wages (employedPublic's complement)
		// are paid FROM FIRMS (PostWagesFromFirms — businesses pay
		// employees from revenue/working capital, never the treasury);
		// PUBLIC-sector wages (employedPublic) are paid from the treasury
		// via PostWages, as intended. employedPublic is not always zero:
		// this package's OWN employedSectorPlaceholder never assigns
		// SectorPublic (see markEmploymentAndCount's doc comment), but
		// citizens.ColdShard's separate, pre-existing matchJob (coldpass.go)
		// independently draws SectorPublic for some cold citizens well
		// before markEmploymentAndCount ever runs — a real, if usually
		// small, public headcount this split correctly picks up and bills
		// to the treasury rather than firms. Income tax is then collected
		// at the REAL blended rate (incomeNITaxRateBp, 28%) on the ACTUAL
		// posted bill — the same rate distributeWagesToResidents already
		// deducts per-citizen, so that 28% now lands in the treasury as
		// real tax revenue instead of disappearing.
		employedPrivate := employed - employedPublic
		// BUG-745 FINDING (evaluated, deliberately NOT applied here — see
		// moneycirc.go's resolveFirmsMonth doc comment): scaling
		// privateWageBill by outputScale here, the same way
		// postConsumptionAndTax's revenue leg is scaled above, was tried
		// first. It compounds destructively: a sustained low outputScale
		// reduces the consumption-leg revenue AcctFirms receives AND the
		// wage bill firms must still cover from it, so PostWagesFromFirms
		// starts rejecting (its credit line exhausts faster), which flips
		// distributeWagesToResidents' creditPrivateSector gate off for
		// private-sector citizens — starving their per-citizen Wealth and
		// triggering the BUG-452 rent-burden emigration collapse this
		// package's own tests already guard against elsewhere
		// (TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable). Measured
		// directly (this ticket's own round): a 24-month half-output run
		// with this leg ALSO scaled collapsed population from 509 to 118
		// relative to the full-output control — a real but wildly
		// disproportionate, cascading consequence, not the contained
		// "productivity change is observable" fix this ticket scopes.
		// Left UNSCALED pending a follow-up that can bound the cascade
		// (e.g. widening firmsWageCreditLineMicropounds in proportion, or
		// decoupling the floor backstop from outputScale entirely) —
		// flagged for Aaron rather than silently shipped. Flag this same
		// finding for Aaron's balance list alongside 57fc437's own
		// slope-tuning note (data/wellbeing.json's placeholder 0.001
		// slope): now that ProductivityModifier genuinely reaches
		// OutputScale (57fc437), a bad wellbeing month can drive this same
		// cascade organically, not just via this ticket's synthetic
		// market-shortfall fixture.
		privateWageBill := int64(employedPrivate) * monthlyWageGrossPerEmployedMicropounds
		publicWageBill := int64(employedPublic) * monthlyWageGrossPerEmployedMicropounds
		// monthlyWagesFloor's doc comment: never post BELOW the
		// already-validated safe floor (avoids reproducing the BUG-452
		// rent-burden emigration collapse at baseline-one's seed scale) —
		// population-scaling only takes over once it would exceed the
		// floor. The shortfall is attributed to the PRIVATE bucket as a
		// simplification (the floor's original intent was always a wage
		// SAFETY NET, not a sector-specific subsidy) — a real, usually
		// small, public headcount (see employedPublic's doc comment above)
		// means this is not always byte-identical to the pre-fix flat
		// figure, but stays within the same documented placeholder spirit.
		if totalWageBill := privateWageBill + publicWageBill; totalWageBill < monthlyWagesFloor {
			privateWageBill += monthlyWagesFloor - totalWageBill
		}
		var wagePosted int64
		// firmsPaidPrivate tracks whether PostWagesFromFirms actually
		// landed the private-sector bill on the ledger this month (BUG-548
		// fix #4): distributeWagesToResidents below must never credit a
		// private-sector citizen's Wealth for a wage the ledger did not
		// pay — the round's finding was exactly this: firms posted ZERO
		// wages while every employed citizen was still credited off-ledger.
		firmsPaidPrivate := privateWageBill <= 0 // nothing owed => trivially satisfied
		if privateWageBill > 0 {
			if posted, err := st.finance.PostWagesFromFirms(finance.Money(privateWageBill)); err == nil {
				wagePosted = int64(posted)
				firmsPaidPrivate = true
			} else {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostWagesFromFirms", "cause": err.Error()})
				// BUG-548 fix #3 (GR#17): a payroll shortfall is a real,
				// user-visible failure, not just a debug log line — record
				// it on FinanceAPI's monitorable PayrollShortfall surface
				// AND raise the registered MET-G217 error (never a bare
				// errs.New with no registry code, GR#7).
				_ = errs.New(finance.ErrPrivateWagePayrollShortfall, st.cid, map[string]any{
					"amountMicropounds": privateWageBill,
					"employedPrivate":   employedPrivate,
					"correlationID":     st.cid,
				})
				st.finance.RecordPayrollShortfall(clock.Month(), finance.Money(privateWageBill))
			}
		}
		if publicWageBill > 0 {
			if posted, err := st.finance.PostWages(finance.Money(publicWageBill)); err == nil {
				wagePosted = num.SatAdd(wagePosted, int64(posted))
			} else {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostWages", "cause": err.Error()})
			}
		}

		// BUG-548 fix #1: the monthlyWagesFloor safety net (BUG-452) must
		// stay UNCONDITIONAL — it must never be breached just because the
		// firms leg above was rejected (the round's
		// TestBUG548Attack_ExhaustionIsPermanentAndUnrecoverable measured
		// the floor breached 12/12 months once the firms credit line was
		// exhausted, because the topped-up amount was baked into
		// privateWageBill and lost together with it). Whatever actually
		// landed on the ledger this month (wagePosted, firms + treasury)
		// is compared against the floor directly; any gap is topped up
		// from the treasury as a real, visible, separately-posted wage
		// leg — never a silent citizen-side-only credit.
		if wagePosted < monthlyWagesFloor {
			floorShortfall := monthlyWagesFloor - wagePosted
			if posted, err := st.finance.PostWages(finance.Money(floorShortfall)); err == nil {
				wagePosted = num.SatAdd(wagePosted, int64(posted))
			} else {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "PostWages.floorBackstop", "cause": err.Error()})
			}
		} else if firmsPaidPrivate {
			// firmsPaidPrivate is true whenever this month's private
			// posting did NOT reject — either nothing was owed at all
			// (privateWageBill <= 0, a genuinely clean month: the
			// MOD-034 wellbeing-driven population-trajectory shift makes
			// this reachable far more often than the original privateWageBill>0
			// guard assumed) or PostWagesFromFirms succeeded. A rejected
			// posting leaves firmsPaidPrivate false (set only in the
			// success branch above, never touched in the failure branch),
			// so this clear never fires while a real failure persists.
			// Clear the shortfall surface so PayrollShortfall() reads
			// fresh for the current month (GR#17: a monitor must see
			// recovery, not a stale failure forever — and must never
			// report a phantom failure for a month that had nothing to
			// fail).
			st.finance.RecordPayrollShortfall(clock.Month(), 0)
		}

		if wagePosted > 0 {
			flowed = num.SatAdd(flowed, wagePosted)
			if receipts, err := st.finance.CollectTax(finance.TaxRates{IncomeRate: incomeNITaxRateBp}, finance.Money(wagePosted), 0, 0); err == nil {
				flowed = num.SatAdd(flowed, int64(receipts.Income))
			} else {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "CollectTax.income", "cause": err.Error()})
			}
		}

		// FEAT-1972079927 Q5: distribute this month's wage to every
		// resident's own Wealth field (per-citizen, not the ledger — see
		// moneycirc.go's distributeWagesToResidents doc comment). A
		// failure here is logged loudly (GR#1) but never blocks the
		// ledger legs above, which have already posted. BUG-548 fix #4:
		// firmsPaidPrivate gates whether PRIVATE-sector citizens are
		// credited at all — a citizen must never receive Wealth for a
		// wage the ledger did not actually pay from firms. Public-sector
		// citizens are unaffected (PostWages/treasury is not the account
		// under attack here and is not gated).
		if st.citizens != nil {
			if err := st.distributeWagesToResidents(firmsPaidPrivate); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "citizens", "op": "distributeWagesToResidents", "cause": err.Error()})
			}
		}
	}
	// Mirror the LEDGER unconditionally — on success and on rejection
	// alike. A rejected Post leaves the ledger unchanged by contract
	// (finance.Post validates before mutating, never a partial post), so
	// syncing is honest on every path: simState can never diverge from
	// the pot F2 actually reads, and a future partial post (a leg landing
	// without its pair) would be mirrored exactly as it landed rather
	// than replayed from stale fallback deltas. The pair is all-or-nothing
	// today because CollectTax's debit is computed on the wages credited
	// moments earlier (rate <= 100% can never overdraft households).
	st.syncMoneyFromLedger()

	// gross flow (AC-9 "money moved") counts only what actually posted.
	if flowed > 0 {
		st.moneyFlows = num.SatAdd(st.moneyFlows, flowed)
	}
	// The tracked stock's actual change this month, measured directly from
	// the ledger (see preTreasury/preHouseholds's doc comment above) —
	// correct whether this month's new legs succeeded, partially
	// succeeded, or were rejected outright.
	actualDelta := num.SatSub(num.SatAdd(st.treasury, st.citizenWealth), num.SatAdd(preTreasury, preHouseholds))
	st.moneyDelta = num.SatAdd(st.moneyDelta, actualDelta)
}

// seedOpeningBalances posts the baseline-one opening grant into the
// FinanceAPI ledger so F2 is not a permanent zero sheet (BUG-355).
// External is the outside-world source; it is not part of the money stock.
func seedOpeningBalances(f *finance.FinanceAPI, treasury, households int64) error {
	if treasury > 0 {
		if _, err := f.Post(finance.Transaction{
			Description: "baseline-one opening treasury",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(treasury), Category: finance.Category("opening.capital")},
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(treasury), Category: finance.Category("opening.capital")},
			},
		}); err != nil {
			return err
		}
	}
	if households > 0 {
		if _, err := f.Post(finance.Transaction{
			Description: "baseline-one opening household wealth",
			Entries: []finance.Entry{
				{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(households), Category: finance.Category("opening.capital")},
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(households), Category: finance.Category("opening.capital")},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func ledgerBalance(f *finance.FinanceAPI, id finance.AccountID) int64 {
	if f == nil {
		return 0
	}
	bal, ok := f.AccountBalance(id)
	if !ok {
		return 0
	}
	return int64(bal)
}

func (st *simState) syncMoneyFromLedger() {
	if st.finance == nil {
		return
	}
	// BUG-324: treasury is written ONLY through setTreasury so the
	// publish-only mirror cannot drift from the simulated pot.
	st.setTreasury(ledgerBalance(st.finance, finance.AcctTreasury))
	st.citizenWealth = ledgerBalance(st.finance, finance.AcctHouseholds)
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *financeHook) SingleShard() bool { return true }

// --- consumption hook (MOD-021, real) ---

// consumptionEffect is the monthly consumption tick marker.
type consumptionEffect struct {
	month int64
}

// consumptionHook is the baseline-one consumption hook (MOD-021, real): it
// draws the whole city's residential utility demand (water/power/gas)
// against the three coarse baseline-one networks, via UtilityAPI's
// SolveDailyTick, and accumulates the delivered quantity. Only shard 0
// emits the effect; ApplyEffect is the single-goroutine barrier that runs
// the solve (which mutates each network's last-solve state).
type consumptionHook struct {
	st *simState
}

func (h *consumptionHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: consumptionEffect{month: clock.Month()}}}, nil
}

func (h *consumptionHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(consumptionEffect)
	if !ok {
		return
	}
	if err := h.st.drawConsumption(p.month); err != nil {
		// A valid baseline-one draw cannot fail; log loudly rather than
		// swallow (GR#1). ApplyEffect has no error return, so the failure
		// is surfaced through the error registry's log sink.
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "consumption", "cause": err.Error()})
		return
	}
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *consumptionHook) SingleShard() bool { return true }

// drawConsumption solves the residential demand (one entity: the whole
// city's population at the §17.1 per-person baseline) against water/power/
// gas and accumulates the delivered quantity. A monthly approximation of
// the module's per-day solve (PhaseConsumptionShortfall runs once per
// month) — the real per-day cadence is the module's own daily-tick concern.
func (st *simState) drawConsumption(month int64) error {
	pop := float64(st.citizens.TotalPopulation(st.cid))
	opts := consumption.DemandOptions{MonthIndex: month, GasNetworkPresent: true}
	entities := []consumption.DemandEntity{{EntityRef: "residential", Population: pop}}

	// Slice (not a map) so the network solve order is deterministic (GR#21).
	networks := []*consumption.Network{st.waterNet, st.powerNet, st.gasNet}
	var delivered float64
	for _, net := range networks {
		res, err := st.consumption.SolveDailyTick(net, entities, opts)
		if err != nil {
			return err
		}
		delivered += res.Delivered
	}
	st.consumptionDelivered += delivered
	return nil
}

// --- build hook (MOD-026, real) ---

// buildEffect is the daily build-queue tick marker.
type buildEffect struct {
	month int64
}

// buildHook is the baseline-one build hook (MOD-026, real; cadence fixed by
// BUG-268): it advances the build queue one simulation day per simulation
// day via BuildAPI.Tick, registered against PhaseDailyTick so its cadence
// matches BuildAPI.Tick's own one-day-per-call contract (build.go's
// daysPerTick). Passing clock.Month() every day is safe — build.go's Tick
// only uses its month parameter for >=0 validation, never as a "did the
// month change" gate. Zone/Build commands themselves arrive through the
// gameplay-command seam (handleGameplay), not this phase hook — this hook
// only elapses the queue.
type buildHook struct {
	st *simState
}

func (h *buildHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: buildEffect{month: clock.Month()}}}, nil
}

func (h *buildHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(buildEffect)
	if !ok {
		return
	}
	if err := h.st.buildAPI.Tick(p.month); err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "build", "cause": err.Error()})
		return
	}
	h.st.registerLeisureVenues()
	// BUG-743/BUG-734: piggyback the build->deathservices registration
	// bridge on this SAME daily hook, right after Tick, mirroring
	// registerLeisureVenues' own build->leisure bridge immediately above —
	// deliberately NOT a new core.PhaseHook (which would change
	// PhaseHookCount and invalidate the CI perf baseline). See
	// compose_buildregistry.go's package doc for the two-cursor recipe this
	// call implements.
	h.st.runDeathServiceBuildingRegistry()

	// FEAT-1972079927 inc2: accrue this tick's newly-drawn construction
	// materials cost (split local-merchant vs imported) and settle the
	// NET-90 accrual at every COMMERCIAL_PAYMENT_TERM_TICKS boundary. Runs
	// after Tick (which just moved MaterialsDrawn) and after
	// registerLeisureVenues, on the same daily cadence as the build queue
	// itself (BUG-268's fixed one-day-per-call contract).
	if clock, cErr := h.st.e.Clock(); cErr == nil {
		// clock.Tick() is read INSIDE this same daily phase pass, before
		// core.Engine.advanceOneDailyTick's post-phase clock.advanceOneDay()
		// increments it (engine.go) — so it is still the PREVIOUS day's
		// count here. +1 gives the day-tick that is actually completing
		// right now, so the 90th call to this hook (not the 91st) is the
		// one that reads exactly 90 and settles the net-90 boundary.
		if err := h.st.accrueAndSettleConstruction(clock.Tick() + 1); err != nil {
			_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "finance", "op": "accrueAndSettleConstruction", "cause": err.Error()})
		}
	} else {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "core", "op": "Clock", "cause": cErr.Error()})
	}
}

// SingleShard implements core.SingleShardHook (BUG-269 — this is the
// hook the regression report named directly): RunShard returns (nil,
// nil) for every shard except 0 (see above) — the only Effect ever
// emitted comes from shard 0.
func (h *buildHook) SingleShard() bool { return true }

// registerLeisureVenues bridges engine.build's completed ZoneEntertainment
// orders into engine.leisure's venue registry (FEAT-167 ICD §12 open
// decision 4's "fourth edge gap" — mediated entirely by compose; no direct
// engine.build -> engine.leisure edge is registered in code.json). Called
// once per day-tick after buildAPI.Tick (buildHook.ApplyEffect, above): a
// completed entertainment-zone build order becomes exactly one leisure
// venue, opened once (idempotent via leisureVenuesRegistered) at the
// data-driven bridge capacity (data/attract_terms.json's
// leisure.bridgeVenueCapacityUnits, GR#15) in the community category — a
// deliberately coarse composite (engine.build's zone catalogue carries one
// generic "entertainment" type today, with no venue-category sub-signal),
// never an invented per-building capacity. Iterates buildAPI.Queue()'s
// insertion-order slice, never a map (GR#21).
//
// Destructive round r1 (F2) fix: BuildAPI.Queue() keeps every order
// FOREVER, including one whose structure a later Demolish command already
// deleted (SubmitDemolishCommand clears zoneState/structures, never the
// queue entry) — an order snapshot still reporting
// complete+ZoneEntertainment is therefore NOT proof a venue should exist.
// The only live-truth signal is BuildAPI.Structure(tile, local): whether
// THIS order's ID is still the standing structure on its cell. Every
// completed entertainment order is reconciled against that truth every
// call: currently-standing-but-not-yet-registered opens a venue,
// registered-but-no-longer-standing (demolished, or replaced by a later
// order on the same cell) removes it — so demolishing an entertainment
// zone measurably lowers LeisureFit again, and a later rebuild
// re-registers cleanly (idempotent both ways).
func (st *simState) registerLeisureVenues() {
	for _, order := range st.buildAPI.Queue() {
		if order.Zone != build.ZoneEntertainment || order.Status != build.OrderComplete {
			continue
		}
		venueID := uint64(order.ID)
		structID, standing := st.buildAPI.Structure(order.Tile, order.Local)
		stillStanding := standing && structID == order.ID

		switch {
		case stillStanding && !st.leisureVenuesRegistered[venueID]:
			v := leisure.Venue{
				ID:       venueID,
				Category: leisure.CategoryCommunity,
				District: 0,
				Capacity: st.attractTerms.Leisure.BridgeVenueCapacityUnits,
			}
			if err := st.leisure.OpenVenue(v, st.cid); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "leisure", "cause": err.Error()})
				continue
			}
			st.leisureVenuesRegistered[venueID] = true
		case !stillStanding && st.leisureVenuesRegistered[venueID]:
			if err := st.leisure.RemoveVenue(venueID, st.cid); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "leisure", "cause": err.Error()})
				continue
			}
			delete(st.leisureVenuesRegistered, venueID)
		}
	}
}

// --- attract hook (MOD-029, real) ---

// attractEffect is the monthly migration tick marker.
type attractEffect struct {
	month int64
}

// attractHook is the baseline-one attract hook (MOD-029, real): it runs one
// monthly AttractAPI.ApplyMigration step. Net migration is g(A − A_world) —
// signed, reputation-momentum-amplified, capacity-capped — never a
// hardcoded +N. The applied net population change is tracked in the people
// conservation ledger so the invariant balances.
type attractHook struct {
	st *simState
}

func (h *attractHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: attractEffect{month: clock.Month()}}}, nil
}

func (h *attractHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(attractEffect)
	if !ok {
		return
	}
	res, err := h.st.applyMigration(p.month)
	if err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "attract", "cause": err.Error()})
		return
	}
	h.st.peopleDelta = num.SatAdd(h.st.peopleDelta, res.NetApplied())
	h.st.netMigration = num.SatAdd(h.st.netMigration, res.NetApplied())
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard
// returns (nil, nil) for every shard except 0 (see above) — the only
// Effect ever emitted comes from shard 0.
func (h *attractHook) SingleShard() bool { return true }

// applyMigration pushes all five compose-owned §11 terms, then runs one
// monthly migration step. Safety/LeisureFit/Environment are real, computed
// this same month from engine.crime/engine.leisure/engine.refuse (FEAT-167
// wave 1, docs/planning/icd/engine.attract-terms.md). ServiceCoverage/
// JobAvailability are ALSO now real (FEAT-167 completion,
// docs/planning/icd/engine.services-coverage.md /
// engine.firms-labourmarket.md), computed from engine.services/
// engine.firms — see serviceCoverageTerm/jobAvailabilityTerm
// (servicesfirms_wire.go) for the honest scope-limit each carries (no
// automatic build->services/firm-founding bridge is wired into compose
// yet, so both read their formula's zero-signal edge case until that
// separate integration lands). HousingVacancy/JunctionThroughput are
// unbounded placeholders until households/logistics produce real capacity
// signals.
func (st *simState) applyMigration(month int64) (attract.MigrationResult, error) {
	safety, err := st.safetyTerm(month)
	if err != nil {
		return attract.MigrationResult{}, err
	}
	leisureFit, err := st.leisureFitTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	environment, err := st.environmentTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	serviceCoverage, err := st.serviceCoverageTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}
	jobAvailability, err := st.jobAvailabilityTerm()
	if err != nil {
		return attract.MigrationResult{}, err
	}

	// FEAT-1972079927 Q1: form households monthly from resident citizens —
	// real ASM-247 wiring (moneycirc.go's formResidentHouseholds), BEFORE
	// gathering the household-id set Q2's SetTermInputs call needs.
	if err := st.formResidentHouseholds(month); err != nil {
		return attract.MigrationResult{}, err
	}
	householdIDs := st.citizens.HouseholdIDs(st.cid)

	// FEAT-1972079927 Q2: engine.attract's own snapshotTerms already calls
	// engine.households' real HousingAffordability (attract/api.go) — it
	// only ever needed a non-nil household-id set and a non-zero rent,
	// both supplied now that Q1 forms real households.
	if err := st.attract.SetTermInputs(attract.TermInputs{
		JobAvailability:        jobAvailability,
		ServiceCoverage:        serviceCoverage,
		Environment:            environment,
		LeisureFit:             leisureFit,
		Safety:                 safety,
		HouseholdIDs:           householdIDs,
		MonthlyRentMicroPounds: st.monthlyRentForHouseholds(householdIDs),
	}); err != nil {
		return attract.MigrationResult{}, err
	}
	// Capture affordability RIGHT NOW, immediately after SetTermInputs —
	// see housingAffordability's doc comment on simState for why this
	// avoids the stale-snapshot race a later re-query of
	// AttractAPI.HousingAffordability() could hit. Best-effort: a failure
	// here (defensive only — SetTermInputs just succeeded with this exact
	// household-id set) leaves the previous month's reading in place
	// rather than blocking migration on an observability read.
	if aff, err := st.attract.HousingAffordability(); err == nil {
		st.housingAffordability = aff
	}
	return st.attract.ApplyMigration(attract.MigrationCommand{
		Month:              month,
		ResidentIDs:        st.residentIDs(),
		HousingVacancy:     baselineOneHousingVacancy,
		JunctionThroughput: baselineOneJunctionThroughput,
	})
}

// deathServicesEffect carries the month deathServicesHook.RunShard reads
// from the clock, mirroring attractEffect's identical shape (a PhaseHook's
// RunShard/ApplyEffect split must never read live engine state from
// ApplyEffect directly — GR#21 determinism discipline).
type deathServicesEffect struct {
	month int64
}

// deathServicesHook drives BUG-689's monthly engine.deathservices Intake
// from the live tick, on the same monthly PhasePopulation slot as attract
// (registrationOrder above).
type deathServicesHook struct {
	st *simState
}

func (h *deathServicesHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: deathServicesEffect{month: clock.Month()}}}, nil
}

func (h *deathServicesHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(deathServicesEffect)
	if !ok {
		return
	}
	if err := h.st.intakeDeathServices(p.month); err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "deathservices", "cause": err.Error()})
	}
}

// SingleShard implements core.SingleShardHook (mirrors attractHook): the
// only Effect ever emitted comes from shard 0.
func (h *deathServicesHook) SingleShard() bool { return true }

// intakeDeathServices is BUG-689's exactly-once monthly drain of citizens'
// FEAT-087/BUG-483 handoff stream into engine.deathservices: it reads
// deathservices' OWN persisted cursor (round-trips through its
// save.Participant, participant.go), pages exactly the NEW entries since
// that cursor via [citizens.CitizensAPI.DeathHandoffSince], and applies
// them through [deathservices.DeathServicesAPI.IntakeFromHandoff] — which
// advances the cursor atomically with the intake application (see that
// method's doc for why the two never observe a save/restore boundary
// independently of each other).
//
// Nil-wiring fail-safe (AC per the BUG-689 brief): a nil st.deathServices
// or st.citizens (never true through Wire today — both are unconditionally
// constructed — but defensive against a future lower-level test harness
// that builds a *simState directly) is a documented no-op, never a panic.
func (st *simState) intakeDeathServices(month int64) error {
	_ = month // reserved for a future month-scoped policy; unused today.
	if st.deathServices == nil || st.citizens == nil {
		return nil
	}
	cursor, err := st.deathServices.HandoffCursor(st.cid)
	if err != nil {
		return err
	}
	if cursor < 0 || cursor > math.MaxInt {
		cursor = 0 // defensive clamp; DeathHandoffSince itself also clamps negatives.
	}
	deaths, err := st.citizens.DeathHandoffSince(int(cursor), st.cid)
	if err != nil {
		return err
	}
	if len(deaths) == 0 {
		// BUG-689 P2 follow-up (over-length cursor wedge --
		// attack_bug689_reround2_test.go's pinned FINDING, now the fix
		// TestAttackBUG689_RR2_OverLengthCursorSelfCorrectsNoDroppedDeaths
		// asserts): deathservices' OWN decode-time clamp (participant.go's
		// F6 fix, MET-G5452) can only ever guard a NEGATIVE cursor --
		// negative is unconditionally invalid regardless of the citizens
		// handoff stream's length, so deathservices' decode step (which
		// never holds a citizens reference, GR#20) can zero it
		// unilaterally and safely. An OVER-LENGTH cursor is a different
		// shape of invalid: "impossible" is defined only relative to the
		// REAL stream length, which only the composition root can observe
		// (it alone holds both APIs) -- clamping it inside deathservices
		// itself would mean either guessing an arbitrary ceiling (a real
		// 100M-citizen city can legitimately reach a huge cursor -- GR#15
		// bans a hand-picked magic threshold) or leaving it unguarded,
		// which is exactly the pinned FINDING: a cursor at or past the
		// stream's length makes DeathHandoffSince return empty forever, so
		// IntakeFromHandoff is never called, so the cursor never advances
		// -- permanently wedged, strictly worse than the negative case,
		// which self-corrects in one month.
		//
		// Fixed HERE, the one place with enough information to make the
		// correction principled rather than arbitrary:
		// st.citizens.DeathHandoff's full stream length is the exact,
		// non-negotiable upper bound a valid cursor can never exceed
		// (DeathHandoffSince's own documented contract), so any cursor
		// beyond it is unambiguously impossible, never a size judgement
		// call.
		//
		// BUG-725 round follow-up (P2, opus-round-bug725, refined in the
		// re-round): re-running the full-stream read on every caught-up
		// month re-verifies a cursor VALUE this composition has already
		// confirmed in-range, for no additional safety -- the earlier
		// revision's claim of "bounded cost" was true per-call but not
		// true in aggregate (e.g. 24 caught-up months in a save's life
		// meant 24 full-stream copies, not one). st.lastCheckedHandoffCursor
		// / st.handoffCursorCheckDone (reset by Composition.Load,
		// save_wire.go) skip the read only when the CURRENT cursor is the
		// exact value last confirmed in-range -- NOT a one-shot "checked
		// once, ever" latch, because the cursor CAN move again without a
		// fresh Load (a direct IntakeFromHandoff call from outside this
		// monthly hook is exactly that shape --
		// TestAttackBUG725_BoundaryLenVsLenPlusOne's len+1 case proves it),
		// and an unconditional "already checked" latch would let that
		// SECOND, genuinely-impossible cursor value sail through unchecked
		// for the rest of the load's lifetime -- worse than the original
		// defect because it would look fixed.
		if !st.handoffCursorCheckDone || st.lastCheckedHandoffCursor != cursor {
			st.handoffCursorCheckDone = true
			st.lastCheckedHandoffCursor = cursor
			full, ferr := st.citizens.DeathHandoff(st.cid)
			if ferr != nil {
				// BUG-725 P3: a failed full-stream read must never be a
				// SILENT no-op -- discarding ferr here would leave a
				// genuinely over-length cursor permanently unguarded with
				// zero signal, exactly the class of silent wedge this
				// whole fix exists to close. Not fatal (a transient
				// citizens-side fault must not abort the monthly intake
				// hook), but registry-logged so it is observable via
				// errs.Recent -- mirrors this function's own nil-wiring
				// fail-safe stance.
				_ = errs.Wrap(ErrModuleFailed, st.cid, ferr, map[string]any{
					"module":        "citizens",
					"step":          "death-handoff-read-for-cursor-check",
					"handoffCursor": cursor,
				})
			} else if cursor > int64(len(full)) {
				_ = errs.New(deathservices.ErrCorruptHandoffCursor, st.cid, map[string]any{
					"direction":     "over_length",
					"handoffCursor": cursor,
					"streamLength":  len(full),
					"clampedTo":     int64(0),
				})
				// handoffCursor's only other mutator (IntakeFromHandoff) is
				// additive-only -- re-deriving `deaths` from index 0 alone
				// would leave the PERSISTED cursor at its original impossible
				// value, and the next IntakeFromHandoff's `+= len(deaths)`
				// would add on TOP of it (for math.MaxInt64, wrapping to a
				// negative int64 -- an even worse corrupt state). Reset the
				// persisted value first via the module's own explicit escape
				// hatch (ResetHandoffCursor's doc comment).
				if rerr := st.deathServices.ResetHandoffCursor(st.cid); rerr != nil {
					return rerr
				}
				deaths, err = st.citizens.DeathHandoffSince(0, st.cid)
				if err != nil {
					return err
				}
				// Clamped to 0 (mirroring F6's own negative treatment,
				// MET-G5452) rather than to the stream's length,
				// specifically so the IntakeFromHandoff call below is
				// non-empty and therefore actually WRITES the corrected
				// cursor back through the module's only real setter --
				// clamping to len(full) instead would leave deaths empty,
				// skip IntakeFromHandoff entirely, and leave the poisoned
				// value persisted forever, self-correcting nothing. Any
				// already-consumed entries this re-read revisits are
				// safely absorbed by Intake's own per-citizenID duplicate
				// guard (H4 policy (b), ErrDuplicateDeath handled below
				// exactly as today).
			}
		}
		if len(deaths) == 0 {
			return nil
		}
	}
	_, err = st.deathServices.IntakeFromHandoff(deaths, st.cid)
	// ErrDuplicateDeath is Intake's own documented WARNING-not-abort signal
	// (H4 policy (b): every other entry in the batch was still applied) —
	// never a hook failure. Every other error IS a genuine fault (a
	// SEC-020 copy-guard trip, most plausibly) and propagates.
	if err != nil && !deathservices.IsDuplicateDeath(err) {
		return err
	}
	return nil
}

// deathServicesRunEffect carries the day/month deathServicesRunHook.RunShard
// reads from the clock, mirroring deathServicesEffect/attractEffect's
// identical shape (GR#21: a PhaseHook's RunShard/ApplyEffect split must
// never read live engine state from ApplyEffect directly).
type deathServicesRunEffect struct {
	day   int64
	month int64
}

// deathServicesRunHook is BUG-720: the daily run loop that actually drains
// the Awaiting backlog through hearse transport, burial, cremation, and
// emergency dispensation — the gap BUG-689 deliberately left open
// ("Crematoriums still do not RUN — that is BUG-720", ccf15b6's commit
// message). See registrationOrder's own comment on this hook's entry for
// why it is a DAILY (not monthly) registration.
type deathServicesRunHook struct {
	st *simState
}

func (h *deathServicesRunHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: deathServicesRunEffect{day: clock.Tick(), month: clock.Month()}}}, nil
}

func (h *deathServicesRunHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(deathServicesRunEffect)
	if !ok {
		return
	}
	if err := h.st.runDeathServices(p.day, p.month); err != nil {
		_ = errs.New(ErrModuleFailed, h.st.cid, map[string]any{"module": "deathservices", "op": "run", "cause": err.Error()})
	}
}

// SingleShard implements core.SingleShardHook (mirrors deathServicesHook):
// the only Effect ever emitted comes from shard 0.
func (h *deathServicesRunHook) SingleShard() bool { return true }

// deathServicesCremateBatchObserved is a nil-by-default test observation
// seam (BUG-720 round F1): when set, runDeathServices calls it with the
// exact batch size it is about to hand [deathservices.DeathServicesAPI.
// Cremate], once per crematorium per day, immediately before the call.
// Package-private, never touched by production code, and always nil there
// — a test sets it (and restores it to nil via defer) to prove the
// submitted batch size stays bounded by DailyThroughput regardless of
// backlog size, the count-based (never wall-clock) form of the round's
// perf finding.
var deathServicesCremateBatchObserved func(crematoriumID string, submitted int)

// runDeathServices is BUG-720's disposal run loop: every simulation day, it
// drains the CURRENT Awaiting backlog through every registered cemetery
// (hearse transport, one body per trip, bounded by the shared monthly
// hearse budget AND, when wired, engine.logistics congestion — hearse.go's
// RunHearseTransport), then every registered crematorium (bounded by that
// crematorium's own data-sourced daily throughput — crematory.go's
// Cremate), then — only while dispensation is active — the emergency
// multi-body dispensation channel (dispensation.go's Dispense). Nil-wiring
// fail-safe: a nil st.deathServices (never true through Wire today, same
// defensive posture as intakeDeathServices) is a documented no-op.
//
// day is the caller-supplied simulation day counter Cremate's own AC-19
// requires (never wall-clock) — clock.Tick() is exactly that: a monotonic
// per-simulation-day counter, unrelated to real time.
//
// Iteration order over multiple cemeteries/crematoria is the composition's
// OWN sorted id roster (deathServiceCemeteryIDs/deathServiceCrematoriumIDs,
// populated once at Wire time — GR#21: never a map range), so a fixture
// with several registered instances processes them in the same order
// regardless of pool size. AwaitingSorted is re-read before EACH disposal
// call rather than once up front: Bury/Cremate/Dispense's own admission
// gates (awaitingAheadCountLocked) are computed against the CURRENT
// backlog, and a body already claimed by an earlier call in this same day
// would otherwise be re-offered to a later call and rejected with
// ErrBodyAlreadyHandled, aborting that call's ENTIRE batch (Cremate/
// RunHearseTransport are documented all-or-nothing on an unknown/
// already-terminal id) — re-fetching keeps every call's input a genuinely
// still-Awaiting set.
func (st *simState) runDeathServices(day, month int64) error {
	if st.deathServices == nil {
		return nil
	}

	// BUG-743: the roster this sweep drives is now TWO sources,
	// concatenated (never merged into one globally-resorted slice — each
	// half is independently sorted, and a fixed concatenation order is
	// itself a deterministic order, GR#21's actual requirement): the
	// Wire-time stopgap roster (deathServiceCemeteryIDs/CrematoriumIDs,
	// unchanged from BUG-720) and the live build->deathservices bridge
	// roster (deathServiceBridgeCemeteryIDs/CrematoriumIDs, added by
	// runDeathServiceBuildingRegistry) — see simState's own field docs for
	// why these stay two separate fields rather than one persisted union.
	cemeteryIDs := append(append([]string{}, st.deathServiceCemeteryIDs...), st.deathServiceBridgeCemeteryIDs...)
	crematoriumIDs := append(append([]string{}, st.deathServiceCrematoriumIDs...), st.deathServiceBridgeCrematoriumIDs...)

	// Hearse transport + burial: one cemetery at a time, deterministic
	// order, refetching the backlog between cemeteries (see doc above).
	//
	// BUG-720 round F1 perf fix: the batch handed to RunHearseTransport is
	// truncated to the fleet's REMAINING monthly budget first (never the
	// raw, unbounded backlog) — RunHearseTransport's own admission gate
	// (buryLocked -> awaitingAheadCountLocked, O(len(bodies))) runs once
	// per body it actually TRANSPORTS, which is already capped at the
	// budget internally, but Pass 1's dedup/validate still walks the
	// WHOLE submitted slice first; truncating here keeps that walk (and
	// every allocation sized off it) bounded by the budget too, not the
	// backlog.
	for _, cemeteryID := range cemeteryIDs {
		remaining, err := st.deathServices.RemainingHearseBudget(month, st.cid)
		if err != nil {
			return err
		}
		if remaining <= 0 {
			break // shared city-wide budget exhausted -- no cemetery can transport more this month
		}
		awaiting, err := st.deathServices.AwaitingSorted(st.cid)
		if err != nil {
			return err
		}
		if len(awaiting) == 0 {
			break
		}
		if int64(len(awaiting)) > remaining {
			awaiting = awaiting[:remaining]
		}
		if _, _, err := st.deathServices.RunHearseTransport(awaiting, cemeteryID, month, st.cid); err != nil {
			return err
		}
	}

	// Cremation: one crematorium at a time, deterministic order, cost
	// posted through engine.finance's SettleOpex — the sanctioned
	// "service operating expenditure" transaction (finance/stages.go),
	// the same shape internal/engine/maintenance's SetFinance-wired
	// SettleOpex caller uses. deathservices deliberately has NO
	// engine.finance edge of its own (doc.go: "NOT engine.finance...
	// costs are plain int64 micro-pounds") — Cremate returns a plain
	// int64 cost precisely so the composition root, which DOES hold the
	// registered engine.finance edge, converts and posts it at its own
	// boundary, exactly as that return value's own doc comment
	// prescribes.
	//
	// BUG-720 round F1 perf fix (the round's headline finding): Cremate's
	// own admission loop calls awaitingAheadCountLocked (O(len(bodies)))
	// once per SUBMITTED id, not once per id it actually cremates, with
	// NO early break once the daily cap is reached — handing it the
	// entire (often much larger) Awaiting backlog every day made that
	// loop cost O(backlog x totalBodies) for a result capped at
	// DailyThroughput regardless (measured: 109ms/2.65s/17.08s per
	// SIMULATED month at backlog 500/2000/5000 to cremate the same ~360
	// bodies either way). Truncating to RemainingDailyThroughput FIRST
	// bounds it to O(throughput x totalBodies), independent of backlog
	// size — see TestBUG720_DailySweepBoundedByThroughputNotBacklog for
	// the count-based (never wall-clock) regression proof.
	for _, crematoriumID := range crematoriumIDs {
		remaining, err := st.deathServices.RemainingDailyThroughput(crematoriumID, day, st.cid)
		if err != nil {
			return err
		}
		if remaining <= 0 {
			continue // this crematorium's own daily cap already spent today
		}
		awaiting, err := st.deathServices.AwaitingSorted(st.cid)
		if err != nil {
			return err
		}
		if len(awaiting) == 0 {
			break
		}
		if int64(len(awaiting)) > remaining {
			awaiting = awaiting[:remaining]
		}
		// BUG-720 round F1 test seam: a nil-by-default observer hook so a
		// test can capture the SUBMITTED batch size Cremate actually
		// receives (the count the round's finding measured cost scaling
		// against) without instrumenting deathservices itself. Zero cost
		// in production (a single nil check).
		if deathServicesCremateBatchObserved != nil {
			deathServicesCremateBatchObserved(crematoriumID, len(awaiting))
		}
		_, cost, err := st.deathServices.Cremate(awaiting, crematoriumID, day, st.cid)
		if err != nil {
			return err
		}
		if cost > 0 && st.finance != nil {
			if _, err := st.finance.SettleOpex(finance.Money(cost)); err != nil {
				_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "SettleOpex", "cause": err.Error()})
			} else {
				st.syncMoneyFromLedger()
			}
		}
	}

	backlog, err := st.deathServices.AwaitingBacklog(st.cid)
	if err != nil {
		return err
	}

	// BUG-720 dispensation: the backlog-crisis trigger this bug's brief
	// asks for, layered ADDITIVELY on top of BUG-689's already-working
	// EmergencyFlag activation (Intake/IntakeFromHandoff's intakeLocked
	// already raises active on a weather-flagged death — nothing to add
	// there). data/deathservices.json's backlogCapacityCeiling is
	// documented "informational-only in inc1" (config.go) — this is that
	// field's first real consumer.
	//
	// Raise-only while over the ceiling, mirroring Intake's own "raise
	// only, never lower on an ordinary signal" policy (H5): a backlog at
	// or above the data-sourced ceiling is itself a crisis regardless of
	// whether a weather EmergencyFlag also happened to be present.
	//
	// Clear ONLY once backlog is fully drained to zero, deliberately NOT
	// "back under the ceiling" — this composition has no accessor for
	// the live FEAT-087 weather-event signal itself (citizens.
	// IsWeatherEmergency takes an unexported MortalityConfig with no
	// CitizensAPI-level wrapper — a real gap, filed as a follow-up, not
	// invented around here), so it cannot distinguish "activated by
	// backlog" from "activated by a still-ongoing weather emergency".
	// Backlog==0 is the one condition safe to treat as "done" regardless
	// of WHY dispensation activated: there is nothing left to disperse
	// either way, and the next EmergencyFlag-carrying Intake batch (or
	// the very next tick's backlog re-crossing the ceiling) re-raises it
	// exactly as before — AC-12's reversion guarantee is not weakened by
	// waiting for a full drain rather than a partial one.
	cfg, cfgErr := st.deathServices.Config(st.cid)
	if cfgErr == nil {
		ceiling := cfg.BacklogCapacityCeiling()
		active, actErr := st.deathServices.DispensationActive(st.cid)
		if actErr == nil {
			if ceiling > 0 && int64(backlog) >= ceiling && !active {
				if err := st.deathServices.SetDispensationActive(true, st.cid); err != nil {
					return err
				}
				active = true
			} else if backlog == 0 && active {
				if err := st.deathServices.SetDispensationActive(false, st.cid); err != nil {
					return err
				}
				active = false
			}
			if active && backlog > 0 {
				// Dispense drains via its OWN multi-body van/truck channel
				// (dispensation.go), sharing hearse.usedThisMonth only in
				// the inactive single-body branch — while active, this is
				// the module's documented emergency-throughput lift, used
				// here for the SAME reason RunHearseTransport is used
				// above: drive every declared disposal channel, not just
				// the ordinary one.
				//
				// LOOPED, unlike the single RunHearseTransport/Cremate
				// calls above: Dispense truncates its OWN input to the
				// data-sourced per-call van capacity (dispensationVanBodyCapacity,
				// spec seed 6) BEFORE consulting the remaining monthly
				// budget (DispensationMonthlyBudget, hearseMonthlyTransportBudget
				// x dispensationThroughputMultiplier) — unlike
				// RunHearseTransport, which processes many trips' worth of
				// bodies in ONE call. A single Dispense call therefore
				// moves at most one van's load; calling it once per DAY
				// would silently cap the "24x7 operation" (dispensationState's
				// own doc) emergency channel at van-capacity-per-day, an
				// order of magnitude under its own documented monthly
				// budget. Looping here (bounded: each successful call
				// strictly drains the backlog by >0, and the remaining
				// monthly budget is exactly as finite) drives up to the
				// FULL remaining monthly budget in a single crisis day,
				// which is the correct reading of "24x7" for an emergency
				// response channel.
				for {
					awaiting, err := st.deathServices.AwaitingSorted(st.cid)
					if err != nil {
						return err
					}
					if len(awaiting) == 0 {
						break
					}
					dispensed, err := st.deathServices.Dispense(awaiting, month, st.cid)
					if err != nil {
						return err
					}
					if len(dispensed) == 0 {
						break // monthly budget exhausted for today
					}
				}
			}
		}
	}

	return nil
}

// safetyTerm advances engine.crime one month against the single citywide
// district (population-driven EligiblePool half only — ICD §12 open
// decision 2: every other DistrictInput driver has no compose-owned real
// source yet, so it stays at its documented zero-neutral default) and
// returns the resulting [0,100] SafetyTerm — higher population -> larger
// EligiblePool -> more generation -> lower Safety, a real monotonic
// dependency, never a flat constant.
func (st *simState) safetyTerm(month int64) (float64, error) {
	population := int64(st.citizens.TotalPopulation(st.cid))
	in := crime.DistrictInput{
		District:     citywideCrimeDistrict,
		EligiblePool: population,
	}
	if err := st.crime.AdvanceMonth(month, []crime.DistrictInput{in}, crime.SecurityInput{}); err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "crime"})
	}
	safety, err := st.crime.SafetyTerm(citywideCrimeDistrict)
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "crime"})
	}
	return safety, nil
}

// leisureFitTerm queries engine.leisure's citywide LeisureFitAggregate
// (venue mix vs the would-be-migrant taste distribution, leisure's own
// data-loaded Config.DefaultTaste — no new data file needed, ICD §3) and
// scales its [0,1] result to attract's [0,100] term scale. Zero registered
// venues yields a low aggregate; the registerLeisureVenues bridge (above)
// is what makes this move as the player builds entertainment zones.
func (st *simState) leisureFitTerm() (float64, error) {
	taste := st.leisure.PopulationTaste(st.cid)
	fit, err := st.leisure.LeisureFitAggregate(taste, st.cid)
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "leisure"})
	}
	return 100 * fit, nil
}

// environmentTerm generates one month's waste into the single citywide
// refuse cell (population-driven, mirroring safetyTerm's EligiblePool
// half) and folds the resulting uncollected+disposal-backlog tonnage
// (summed across engine.refuse's three §25 streams, refuseStreams) through
// the data-driven half-saturation curve (data/attract_terms.json's
// environment.pollutionHalfSaturationKg, GR#15) — the same curve shape
// engine.crime's own SafetyTerm uses. Baseline one never wires a refuse
// collection round (no engine.logistics/engine.services dependency is
// injected into refuse here), so the generated waste is always starved of
// collection: uncollected tonnage — and therefore this term's degradation —
// grows monotonically with population, a real dependency never a constant.
func (st *simState) environmentTerm() (float64, error) {
	population := float64(st.citizens.TotalPopulation(st.cid))
	if err := st.refuse.Generate(citywideRefuseCellID, population); err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
	}
	var outstanding int64
	for _, s := range refuseStreams {
		uncollected, err := st.refuse.TonnesUncollected(s)
		if err != nil {
			return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
		}
		backlog, err := st.refuse.TonnesDisposalBacklog(s)
		if err != nil {
			return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "refuse"})
		}
		outstanding = num.SatAdd(outstanding, num.SatAdd(uncollected, backlog))
	}
	half := st.attractTerms.Environment.PollutionHalfSaturationKg
	total := float64(outstanding)
	pressure := total / (total + half)
	if pressure < 0 {
		pressure = 0
	} else if pressure > 1 {
		pressure = 1
	}
	return 100 * (1 - pressure), nil
}

// residentIDs returns the citizen-id set eligible for personality-weighted
// emigration: every sequentially-minted id (seed + direct seeding). This is
// deliberately left NARROW (never widened to migrants/fertility children —
// see liveResidentIDs below for the wider set the wage/employment/
// household-formation surface needs): a first cut of BUG-529/BUG-535
// widened THIS function itself, which also feeds
// attract.MigrationCommand.ResidentIDs (compose.go's applyMigration) — the
// emigration-eligible set — and empirically produced a sawtooth
// boom/bust population collapse every few months in a 48-month composed
// run (a mass-emigration event repeatedly wiping out most of the newly
// widened eligible pool), which is a materially worse regression than the
// wage-pinning defect this ticket fixes. Emigration eligibility for
// migrants/fertility children is therefore left as the SAME pre-existing,
// documented baseline-one limitation it always was — flagged for a
// follow-up ticket, not silently folded into this wiring fix.
func (st *simState) residentIDs() []uint64 {
	ids := make([]uint64, 0, st.nextCitizenID-1)
	for id := uint64(1); id < st.nextCitizenID; id++ {
		ids = append(ids, id)
	}
	return ids
}

// liveResidentIDs returns the FULL live-resident citizen-id set: residentIDs()
// (the seed population) UNION every migrant id engine.attract has ever
// admitted UNION every fertility-born child id engine.citizens has ever
// minted. Used ONLY by the wage/employment-marking surface
// (markEmploymentAndCount/distributeWagesToResidents/employedResidentCount,
// moneycirc.go) and monthly household formation (formResidentHouseholds,
// moneycirc.go) — NEVER for attract's emigration eligibility (residentIDs()
// itself stays narrow for that, see its own doc comment for why).
//
// BUG-529/BUG-535 (2026-09-02): markEmploymentAndCount/formResidentHouseholds/
// distributeWagesToResidents/employedResidentCount used to iterate
// residentIDs() alone — the CLOSED, sequentially-minted seed/direct-seed
// range compose.go's spawnCitizens mints once at Wire time, which never
// grows again. Migrant ids (engine.attract, high-bit-prefixed) and
// fertility-child ids (engine.citizens, FertilityChildIDBase-prefixed) were
// NEVER enumerated, so: (a) the wage bill never counted a migrant's
// employment regardless of its Employment.State, permanently pinning it at
// monthlyWagesFloor as organic migration grew the city while the closed
// seed cohort attrited via mortality (BUG-529's reported symptom), and (b)
// formResidentHouseholds — the ONLY caller of citizens' LifeEventPartner —
// never paired a migrant-or-fertility-child pair, so Household stayed 0 for
// any resident whose partner also came from outside the seed range and
// zero further household-linked events (e.g. births gated on a formed
// household) could ever follow (BUG-535).
//
// Migrant and fertility-child ids are minted densely and gaplessly
// (migration.go's mintMigrantID / fertility.go's nextFertilityChildID, both
// simple "id = base + counter; counter++" schemes), so
// [MigrantIDBase+1, MigrantIDBase+MigrantsAdmitted()] and
// [FertilityChildIDBase+1, FertilityChildIDBase+FertilityChildrenBorn()]
// are exactly the admitted/born sets at any point in the run. Both counts
// are LIVE reads of attract's and citizens' own already-correctly-persisted
// counters (AttractAPI.MigrantsAdmitted/CitizensAPI.FertilityChildrenBorn) —
// deliberately NOT a compose-tracked shadow counter: an earlier version of
// this fix tracked its own running total in simState and found it
// desynced from the real counters across a save/LoadAt boundary (compose's
// own fields are not part of any snapshot payload, so a monotonic counter
// living only in simState silently resets to 0 on restore while the
// continuously-running reference keeps counting) — reading the source of
// truth live avoids that whole class of bug. A departed citizen in any of
// the three ranges (death/emigration) is simply skipped by every consumer's
// existing CitizenAt !ok check, exactly as it already was for the seed
// range.
//
// BUG-665 (2026-09-04): a FOURTH, optional term -- the caller-supplied
// [SeedResidentIDBase+1, SeedResidentIDBase+SeedResidentIDCount] range
// (Deps.SeedResidentIDBase/SeedResidentIDCount) -- is unioned in too, when
// non-empty. This is for a caller that bulk-injects citizens directly into
// the cold store (citizens.CitizensAPI.SeedColdRecords) rather than through
// spawnCitizens' per-citizen command path, which is what the [1,
// nextCitizenID) seed range above actually requires to grow. Unlike the
// three ranges above, this one is STATIC for the life of a Composition (set
// once from Deps at Wire time, never grows) -- it needs no cache-
// invalidation key of its own, unlike migrants/children/nextCitizenID
// which are live, growing counters. Deliberately NOT folded into
// residentIDs() itself -- see Deps.SeedResidentIDCount's own doc comment
// for why (residentIDs() also feeds emigration eligibility, and widening
// IT to a large externally-seeded range risks reproducing the exact
// "sawtooth boom/bust population collapse" residentIDs()'s own doc comment
// already records from a past over-widening).
func (st *simState) liveResidentIDs() []uint64 {
	migrants := st.attract.MigrantsAdmitted()
	children := st.citizens.FertilityChildrenBorn(st.cid)
	// BUG-547: a cache hit requires ALL THREE of the values the union is a
	// pure function of to be unchanged since the cache was populated — see
	// this cache's own field doc comment (compose.go's simState struct)
	// for why counter-keyed invalidation is exact, not tick-boundary
	// approximate. seedResidentIDBase/seedResidentIDCount (BUG-665) need
	// no fourth cache key: they are immutable Deps values copied once at
	// construction (see their own field doc comment), so a cache hit
	// keyed on the three LIVE counters is exactly as valid with that
	// static fourth term folded in below as without it.
	if st.liveResidentIDsCache != nil &&
		st.liveResidentIDsCacheMigrants == migrants &&
		st.liveResidentIDsCacheChildren == children &&
		st.liveResidentIDsCacheNextID == st.nextCitizenID {
		return st.liveResidentIDsCache
	}
	ids := st.residentIDs()
	if st.seedResidentIDCount > 0 {
		for i := uint64(1); i <= uint64(st.seedResidentIDCount); i++ {
			ids = append(ids, st.seedResidentIDBase+i)
		}
	}
	for i := uint64(1); i <= migrants; i++ {
		ids = append(ids, attract.MigrantIDBase+i)
	}
	// BUG-541: fertility mints from [FertilityChildIDBase+0,
	// FertilityChildIDBase+children-1] (fertility.go's nextFertilityChildID
	// starts at 0 and increments AFTER minting each child — see
	// fertility.go's birthChildLocked / FertilityChildrenBorn's doc comment),
	// NOT [+1, +children] as this loop originally enumerated. The off-by-one
	// dropped the FIRST fertility-born child from every liveResidentIDs()
	// consumer (wage/employment marking, household formation) and spuriously
	// enumerated FertilityChildIDBase+children, one past the last real mint,
	// which CitizenAt's !ok check silently skips today only because births
	// were structurally zero (the coupled safeUint32 truncation bug,
	// coldshard.go). It was inert until that bug was fixed alongside it
	// (births-unblock lane, 2026-09-02) — the moment births actually happen,
	// this becomes a live per-child wage/household bug.
	for i := uint64(0); i < children; i++ {
		ids = append(ids, citizens.FertilityChildIDBase+i)
	}
	st.liveResidentIDsCache = ids
	st.liveResidentIDsCacheMigrants = migrants
	st.liveResidentIDsCacheChildren = children
	st.liveResidentIDsCacheNextID = st.nextCitizenID
	return ids
}

// currentMonth returns the engine clock's current simulation month.
func (st *simState) currentMonth() (int64, error) {
	clock, err := st.e.Clock()
	if err != nil {
		return 0, err
	}
	return clock.Month(), nil
}

// --- gameplay command seam (Buy/Zone/Build/Demolish/SetFunding -> build/world/services) ---

// handleGameplay is the injected core.GameplayCommandHandler. It maps the
// gameplay-intent protocol commands onto the owning modules' command
// surfaces: Buy -> world.PurchaseTile, Zone/Build/Demolish ->
// BuildAPI.Submit*Command, SetFunding -> ServicesAPI.SetFunding (FEAT-208
// increment 3, the pilot command promoting services.set-funding off
// protocol.KindDebug's no-op escape hatch onto this real seam — see
// ui/screens/services/doc.go's gating note, now closed). A nil return
// accepts the command (core turns it into an Accepted CommandResult); a
// non-nil registry error rejects it with that code. This is the ONE place
// gameplay intent meets the real modules (AC-1/GR#20): no runnable path
// routes these kinds around compose.
func (st *simState) handleGameplay(cmd protocol.Command) error {
	switch cmd.Kind {
	case protocol.KindBuy:
		p, ok := cmd.Payload.(protocol.BuyPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, _, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		res := st.world.PurchaseTile(world.PurchaseCommand{CorrelationID: st.cid, Tile: tile, BuyerID: playerOwnerID})
		if res.Accepted {
			return nil
		}
		if res.Error == nil {
			return errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "world", "cause": "purchase rejected without an error"})
		}
		// BUG-267: res.Error.Code/Display were already rendered against
		// engine.world's OWN registry template (e.g. MET-E404's
		// "PurchaseTile rejected for tile {tile}: {cause}"). Re-wrapping
		// under that SAME code with a ctx keyed "display" left {tile}/
		// {cause} literal in the message — the ctx key didn't match the
		// template's placeholders. ErrGameplayRejectionPassthrough's
		// template is exactly "{display}", so the already-rendered string
		// passes through intact instead of being re-rendered.
		return errs.New(ErrGameplayRejectionPassthrough, st.cid, map[string]any{"display": res.Error.Display})
	case protocol.KindZone:
		p, ok := cmd.Payload.(protocol.ZonePayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		if err := st.buildAPI.SubmitZoneCommand(build.ZoneCommand{Tile: tile, Local: local, OwnerID: playerOwnerID, Zone: build.ZoneType(p.ZoneType)}); err != nil {
			return err
		}
		// FEAT-1972079927 inc2: check the Industry&Farms auto-placement
		// trigger AFTER a successful zoning (never on a rejected command —
		// zoneState is unchanged on rejection, so the trigger would read
		// stale state for nothing). A failure here is logged loudly
		// (GR#1) but never rejects the zone command that already landed.
		if err := st.maybeAutoPlaceBuildersMerchant(); err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "firms", "op": "maybeAutoPlaceBuildersMerchant", "cause": err.Error()})
		}
		return nil
	case protocol.KindBuild:
		p, ok := cmd.Payload.(protocol.BuildPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		month, err := st.currentMonth()
		if err != nil {
			return err
		}
		// Baseline-one seam note: the protocol's BuildingType maps onto the
		// build module's zone catalogue (build builds zones, not a separate
		// building catalogue yet).
		_, err = st.buildAPI.SubmitBuildCommand(build.BuildCommand{Tile: tile, Local: local, OwnerID: playerOwnerID, Zone: build.ZoneType(p.BuildingType), Month: month})
		return err
	case protocol.KindDemolish:
		p, ok := cmd.Payload.(protocol.DemolishPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		tile, local, err := st.cellFromRef(p.Cell)
		if err != nil {
			return err
		}
		res, err := st.buildAPI.SubmitDemolishCommand(build.DemolishCommand{Tile: tile, Local: local, OwnerID: playerOwnerID})
		if err != nil {
			return err
		}
		// BUG-266: demolish returns a LandPrice-sourced Compensation
		// (build.go's SubmitDemolishCommand doc: "never a bare deletion
		// with no financial consequence"). Credit it treasury -> citizen
		// wealth. BUG-355: post the same transfer through FinanceAPI so
		// the ledger F2 reads moves with the sim. Fallback keeps the
		// simState pots consistent if the post is rejected (demolish
		// already landed in build); treasury writes go through
		// setTreasury so the BUG-324 publish mirror never drifts.
		if res.Compensation > 0 && st.finance != nil {
			if _, err := st.finance.Post(finance.Transaction{
				Description: "demolish compensation",
				Entries: []finance.Entry{
					{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(res.Compensation), Category: finance.Category("demolish.compensation")},
					{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(res.Compensation), Category: finance.Category("demolish.compensation")},
				},
			}); err == nil {
				st.syncMoneyFromLedger()
			} else {
				st.setTreasury(num.SatSub(st.treasury, res.Compensation))
				st.citizenWealth = num.SatAdd(st.citizenWealth, res.Compensation)
			}
		} else {
			st.setTreasury(num.SatSub(st.treasury, res.Compensation))
			st.citizenWealth = num.SatAdd(st.citizenWealth, res.Compensation)
		}
		st.moneyFlows = num.SatAdd(st.moneyFlows, res.Compensation)
		st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(res.Compensation, res.Compensation))
		return nil
	case protocol.KindSetFunding:
		p, ok := cmd.Payload.(protocol.SetFundingPayload)
		if !ok {
			return st.gameplayReject(cmd.Kind, "malformed payload")
		}
		// FEAT-208 increment 3, the pilot command (lead ruling): forwards
		// verbatim to ServicesAPI.SetFunding — no validation duplicated
		// here (GR#3's "the engine validates once"; api.go's SetFunding
		// already hard-rejects non-finite/out-of-[0,1] levels, an
		// unregistered ServiceID, and a not-yet-unlocked service's
		// milestone gate). SetFunding's own errors are already
		// *errs.E values built via serviceErr against this codebase's
		// registered error registry (GR#7), so returning err directly
		// (rather than re-wrapping under a compose-owned code) preserves
		// the already-rendered registry code/display verbatim on the
		// CommandResult — core/commands.go's toErrorRef type-asserts
		// *errs.E directly, exactly the same shape Zone/Build/Demolish's
		// own pass-through errors already take above.
		return st.services.SetFunding(services.ServiceID(p.ServiceID), p.Level)
	default:
		return st.gameplayReject(cmd.Kind, "unhandled gameplay kind")
	}
}

// gameplayReject builds the registry-sourced error for a gameplay command
// this composition cannot map (a defensive branch — core's HandleCommand
// only reaches here for the four gameplay kinds, and Validate already
// guarantees the payload type matches).
func (st *simState) gameplayReject(kind protocol.Kind, cause string) error {
	return errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "build", "kind": string(kind), "cause": cause})
}

// cellFromRef maps a protocol CellRef {x,y} onto a world tile+local cell.
// Baseline-one placeholder: the whole playable extent is the single start
// tile, and the {x,y} grid maps onto its 200x200 local cells. The real
// multi-tile mapping is a world/UI concern (a later sprint).
func (st *simState) cellFromRef(ref protocol.CellRef) (world.TileCoord, world.CellLocal, error) {
	if ref.X < 0 || ref.X >= world.TileSizeCells || ref.Y < 0 || ref.Y >= world.TileSizeCells {
		return world.TileCoord{}, world.CellLocal{}, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "build", "cause": "cell out of bounds",
		})
	}
	return world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}, world.CellLocal{Row: ref.Y, Col: ref.X}, nil
}

// baselineOneNetwork builds one coarse single-source utility network for
// the consumption draw (the real topology arrives once build constructs
// actual networks).
func baselineOneNetwork(kind consumption.Utility, sourceType consumption.SourceType, capacity float64, cid string) (*consumption.Network, error) {
	n := consumption.NewNetwork(kind, cid)
	if err := n.AddSource(consumption.Source{ID: string(kind), Type: sourceType, Capacity: capacity}); err != nil {
		return nil, err
	}
	return n, nil
}

// baselineOneAttractConfig builds attract's runtime Config from the
// documented baseline-one placeholders (the attract module has no data file
// yet; the S6 scenario constructs the same shape inline).
func baselineOneAttractConfig() attract.Config {
	return attract.Config{
		Weights: attract.Weights{
			JobAvailability:      0.2,
			HousingAffordability: 0.2,
			ServiceCoverage:      0.15,
			Environment:          0.1,
			LeisureFit:           0.1,
			Safety:               0.1,
			Reputation:           0.15,
		},
		World:         attract.NewStaticWorldPool(baselineOneAWorld),
		MigrationRate: baselineOneMigrationRate,
		Reputation:    attract.ReputationConfig{RiseRate: baselineOneRepRise, FallRate: baselineOneRepFall, Max: baselineOneRepMax},
	}
}
