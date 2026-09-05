package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// MOD-034 (engine.wellbeing) composition-root wiring. Kept in its own file
// (rather than folded into compose.go's already-large Wire body) per
// BUG-720's collision note: another lane is landing changes to compose.go
// at the same time, so this file carries the construction, the monthly
// phase hook, and the read-only status surface, and compose.go itself is
// touched only for (a) the new *wellbeing.WellbeingAPI field on simState,
// (b) the wireWellbeing(...) call site inside Wire, and (c) one
// registrationOrder entry.
//
// ============================================================================
// GR#25 finding — the four downstream modifiers are COMPUTED but NOT APPLIED
// ============================================================================
//
// engine.wellbeing's four downstream-effect modifiers (MortalityModifier,
// ProductivityModifier, SatisfactionModifier, EmigrationModifier) are
// reconstructed every month by wellbeingHook below and exposed read-only via
// Composition.WellbeingStatus() — but this wiring does NOT apply any of them
// to a real consumer (citizens' mortality hazard, firms'/moneycirc's wage
// bill, attract's satisfaction/migration arithmetic). This is a deliberate
// STOP, not an oversight:
//
//   - MortalityModifier's only consumer would be citizens.MortalityHazard's
//     per-citizen hazard draw (internal/engine/citizens/mortality.go),
//     invoked from WITHIN engine.citizens' own cold pass — compose never
//     calls MortalityHazard directly, and CitizensAPI exposes exactly two
//     Set* seams (SetSeason, SetDeathDrainCapacity), neither generic. Feeding
//     a wellbeing-derived multiplier in would require a NEW inbound seam on
//     engine.citizens (e.g. SetMortalityModifier), which is itself a new
//     engine.citizens -> engine.wellbeing outbound edge.
//   - ProductivityModifier's only plausible consumer is compose's own
//     moneycirc.go wage-bill arithmetic (employedResidentCount() x the real
//     UK gross wage) OR engine.firms' labour-market/production accounting —
//     neither currently exposes a productivity-scaling input.
//   - SatisfactionModifier's and EmigrationModifier's only plausible consumer
//     is engine.attract's migration arithmetic (TermInputs / MigrationCommand,
//     see applyMigration in compose.go) — attract.TermInputs/MigrationCommand
//     carry no wellbeing-modifier field today.
//
// Mechanically verified (2026-09-05, this lane): code.json's outbound edges
// for the three candidate consumer modules do NOT include engine.wellbeing —
//
//	engine.citizens outbound: engine.core, int.serializer, engine.invariant,
//	  foundation.det, foundation.errors, foundation.num
//	engine.firms    outbound: engine.citizens, engine.finance, engine.market,
//	  engine.build, engine.freight, foundation.data, foundation.det,
//	  foundation.errors, foundation.num
//	engine.attract  outbound: engine.citizens, engine.finance,
//	  engine.households, foundation.det, foundation.errors, foundation.num,
//	  engine.build, engine.logistics, engine.market, engine.season,
//	  engine.world, int.serializer
//
// GR#25 is explicit: "If a specification requires a new dependency, the BA
// must first coordinate with the Architect to register the new outbound
// edge/contract in code.json before writing the prose." Wiring any of the
// four modifiers into a real consumer needs one of:
//
//	engine.citizens -> engine.wellbeing   (MortalityModifier)
//	engine.firms    -> engine.wellbeing   (ProductivityModifier, if firms
//	                                       owns the consuming arithmetic) OR
//	                                       a compose-owned moneycirc.go hook
//	                                       (no new edge, but a moneycirc.go
//	                                       change outside this file's remit)
//	engine.attract  -> engine.wellbeing   (SatisfactionModifier,
//	                                       EmigrationModifier)
//
// None of these are registered. Per this brief's own instruction ("if
// applying a modifier needs an edge compose lacks... STOP that modifier and
// report the exact edge"), all four modifiers are computed for observability
// only (Composition.WellbeingStatus()) and applied nowhere. Escalated to the
// architect (Bev) alongside this report; MOD-034 remains built-but-only-
// partially-wired until the missing edge(s) land and a follow-on ticket
// threads the multiplier through.

// wellbeingSeamStatus records, for one wired WellbeingAPI input seam,
// whether the real module was available at Wire time ("live") or the seam
// was left unwired and AttributeCitizen degrades that driver to a neutral
// delta + low confidence per AC-14 ("degraded"). Exposed on WellbeingStatus
// so a degraded seam is inspectable (GR#17), never silent.
type wellbeingSeamStatus struct {
	Name  string
	State string // "live" or "degraded"
}

// WellbeingStatus is the GR#17 read-only status surface for MOD-034's
// composition-root wiring: the reconstructed cohort's mean tracks, the four
// downstream-effect modifier values computed from that mean (see this
// file's package doc comment for why they are NOT applied to any consumer
// yet), which input seams are live vs degraded, and how many citizens fed
// the reconstruction.
type WellbeingStatus struct {
	// SampleSize is the number of resident citizens whose attribution fed
	// this month's mean (0 before the hook has run once, or if the LIVE
	// resident population is empty — see NoData).
	SampleSize int
	// NoData is true iff SampleSize == 0 (round-2 P1-b fix): when true,
	// MeanPhysical/MeanMental are 0 (no meaningful mean exists) and all four
	// modifiers are pinned to their documented NEUTRAL value 1.0 — NEVER
	// computed from (0, 0), which would silently read as a maximally
	// catastrophic cohort (MortalityModifier/EmigrationModifier == 2.0,
	// ProductivityModifier/SatisfactionModifier == 0.0) byte-identical to a
	// real worst-case city (GR#17: a status surface must never fabricate a
	// severity reading from missing data).
	NoData bool
	// MeanPhysical/MeanMental are the reconstructed cohort's mean track
	// totals (PhysicalAttribution.Total / MentalAttribution.Total averaged
	// over SampleSize citizens). Both are 0 when NoData is true.
	MeanPhysical float64
	MeanMental   float64

	// The four §18 downstream-effect modifiers, computed from
	// (MeanPhysical, MeanMental) via WellbeingAPI's own accessors when
	// NoData is false, or pinned to the neutral 1.0 when NoData is true —
	// COMPUTED ONLY, not applied to any consumer (see package doc comment).
	MortalityModifier    float64
	ProductivityModifier float64
	SatisfactionModifier float64
	EmigrationModifier   float64

	// Seams is a fixed-order (never a map range, GR#21) report of every
	// WellbeingAPI input seam's live/degraded state at Wire time. Composition.
	// WellbeingStatus() returns a defensive clone of this slice on every call
	// (round-2 P2 fix) — a caller mutating a returned WellbeingStatus's Seams
	// can never perturb the composition's own report or a different call's
	// result.
	Seams []wellbeingSeamStatus
}

// wellbeingSampleCap bounds the number of LIVE resident citizens (round-2
// P1-a fix: liveResidentIDs(), not the closed seed-only residentIDs()) the
// monthly reconstruction attributes, so the hook's cost is fixed regardless
// of city size (AC-18's "reconstruction is a CPU-time feature, not
// unbounded" spirit — this is compose's own scale knob, not a wellbeing
// package concern). Documented placeholder (GR#15/balance-perf regime):
// large enough to be a meaningful cohort sample at baseline-one/dogfood
// scales, small enough to keep the monthly hook's cost flat as population
// grows toward the 100M northstar. NOT a spec-transcribed figure — a future
// balance/perf pass may retune it.
const wellbeingSampleCap = 500

// trafficWellbeingAdapter narrows *traffic.TrafficAPI to
// wellbeing.TrafficSource's two methods. traffic.TrafficAPI already
// implements CommuteMinutes/ActiveTravelShare with the exact matching
// signatures (structural typing), so this adapter only documents the
// intent; Go's structural interfaces mean *traffic.TrafficAPI already
// satisfies wellbeing.TrafficSource directly — this named type exists so a
// nil *traffic.TrafficAPI can be told apart from "no traffic wired" without
// an untyped-nil-in-interface footgun (a nil *TrafficAPI stored in a
// TrafficSource interface variable is a non-nil interface whose methods
// would panic on the receiver's own nil checks; traffic.TrafficAPI's
// checkNotCopied-style guards make this safe today, but the wrapper keeps
// the intent explicit and cheap to change later).
type trafficWellbeingAdapter struct{ api *traffic.TrafficAPI }

func (a trafficWellbeingAdapter) CommuteMinutes(citizenID uint64, correlationID string) (float64, bool, error) {
	return a.api.CommuteMinutes(citizenID, correlationID)
}

func (a trafficWellbeingAdapter) ActiveTravelShare(citizenID uint64, correlationID string) (float64, bool, error) {
	return a.api.ActiveTravelShare(citizenID, correlationID)
}

// wireWellbeing constructs MOD-034's WellbeingAPI (data/wellbeing.json +
// e.WorldSeed()) and wires every input seam registered on the
// engine.wellbeing outbound edge (code.json) that compose can actually
// satisfy today:
//
//   - engine.season (SetSeason)      -> LIVE (seasonAPI is always constructed
//     in Wire before this call; AC-10's HealthWaveModifier).
//   - engine.traffic (SetTraffic)    -> LIVE (trafficAPI's CommuteMinutes/
//     ActiveTravelShare already match wellbeing.TrafficSource's signatures
//     exactly, FEAT-206).
//   - engine.world (SetPollution)    -> LIVE (wellbeing.WorldPollution
//     adapter over the composed *world.WorldAPI, AC-12b).
//   - engine.shopping (SetShopping)  -> DEGRADED: engine.shopping is not
//     constructed anywhere in compose today (it does not appear in
//     feat.compositionroot's own registered outbound-call list in
//     code.json) — the Diet driver degrades to neutral+low-confidence
//     (AC-14) until engine.shopping lands and is wired here.
//   - engine.services (SetHealthcare) -> DEGRADED: servicesAPI (constructed
//     in Wire for the FEAT-167 ServiceCoverage term) exposes no
//     citizen-keyed HealthcareAccess method today — the HealthcareAccess
//     driver degrades (AC-14) until engine.services grows that surface.
//   - engine.world neighbourhood (SetNeighbourhood) -> DEGRADED:
//     GreenSpace400m/Noise overlays are not carried on *world.WorldAPI yet
//     (ASM-1109) — both drivers degrade (AC-14).
//
// A construction/validation failure returns ErrModuleFailed naming
// "wellbeing", mirroring every other required-module resolution in Wire
// (AC-4 — zero hooks left behind on failure). Never returns a nil API on a
// nil error.
func wireWellbeing(worldSeed uint64, w *world.WorldAPI, seasonAPI *season.SeasonAPI, trafficAPI *traffic.TrafficAPI, correlationID string) (*wellbeing.WellbeingAPI, []wellbeingSeamStatus, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing"})
	}
	cfg, err := wellbeing.LoadWellbeing(dir, correlationID)
	if err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing"})
	}
	api, err := wellbeing.New(cfg, worldSeed, correlationID)
	if err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing"})
	}

	// AC-10: season is a hard wiring requirement (SetSeason's own doc);
	// seasonAPI is always non-nil by the time Wire reaches this call.
	if err := api.SetSeason(seasonAPI); err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing", "step": "SetSeason"})
	}
	if err := api.SetTraffic(trafficWellbeingAdapter{api: trafficAPI}); err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing", "step": "SetTraffic"})
	}
	if err := api.SetPollution(wellbeing.WorldPollution{World: w}); err != nil {
		return nil, nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "wellbeing", "step": "SetPollution"})
	}
	// SetShopping/SetHealthcare/SetNeighbourhood are deliberately left
	// unwired (nil sources) — see the doc comment above for why each is a
	// documented degradation, not an oversight. AttributeCitizen's
	// sourceValue helper treats a nil source exactly like ok=false (AC-14).

	seams := []wellbeingSeamStatus{
		{Name: "engine.season", State: "live"},
		{Name: "engine.traffic", State: "live"},
		{Name: "engine.world.pollution", State: "live"},
		{Name: "engine.shopping", State: "degraded"},
		{Name: "engine.services.healthcare", State: "degraded"},
		{Name: "engine.world.neighbourhood", State: "degraded"},
	}
	return api, seams, nil
}

// --- wellbeing hook (MOD-034 monthly reconstruction) ---

// wellbeingEffect carries the month wellbeingHook.RunShard reads from the
// clock, mirroring attractEffect/deathServicesEffect's identical
// RunShard/ApplyEffect split (GR#21: ApplyEffect must never read live
// engine state directly).
type wellbeingEffect struct {
	month int64
}

// wellbeingHook is MOD-034's monthly reconstruction: it reconstructs a
// bounded sample of resident citizens' wellbeing attribution (AC-18's
// reconstruct-on-demand pattern — nothing durable is stored per citizen),
// averages the two tracks, and computes the four downstream-effect
// modifiers from that mean for the read-only WellbeingStatus() surface. It
// applies no modifier to any consumer (see this file's package doc
// comment).
type wellbeingHook struct {
	st *simState
}

func (h *wellbeingHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: wellbeingEffect{month: clock.Month()}}}, nil
}

func (h *wellbeingHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(wellbeingEffect)
	if !ok {
		return
	}
	h.st.reconstructWellbeing(p.month)
}

// SingleShard implements core.SingleShardHook (mirrors attractHook/
// deathServicesHook's identical shape): the only Effect ever emitted comes
// from shard 0.
func (h *wellbeingHook) SingleShard() bool { return true }

// reconstructWellbeing is the AC-18 reconstruct-on-demand pass: it never
// persists per-citizen attribution, only the cohort MEAN into
// st.wellbeingStatus (a derived, recomputed-every-call summary — never a
// save/restore participant field, since it holds nothing that cannot be
// rebuilt from the live citizen population plus this month's index).
//
// Nil-wiring fail-safe (mirrors intakeDeathServices' identical discipline):
// a nil st.wellbeingAPI or st.citizens is a documented no-op — Wire always
// wires both today, but a test composing a bare simState should never panic
// here.
func (st *simState) reconstructWellbeing(month int64) {
	if st.wellbeingAPI == nil || st.citizens == nil {
		return
	}

	// Round-2 P1-a fix: liveResidentIDs() (residents UNION migrants UNION
	// fertility-born children), NOT residentIDs() alone. residentIDs() is
	// the CLOSED seed-only range minted once at Wire time — sampling it
	// permanently pins SampleSize at the seed population (measured live:
	// pop 46 -> 595 over 36 months, SampleSize stuck at 46) and never
	// reflects the grown city. (The original doc comment here cited
	// moneycirc.go's resident-scoped passes as precedent for residentIDs()
	// — wrong: BUG-529/BUG-535 moved exactly those passes to
	// liveResidentIDs() for this identical reason, see that function's own
	// doc comment.) liveResidentIDs() is disallowed as attract's emigration
	// eligibility set (its own doc comment explains why), but this hook
	// only READS citizen records for observability — never feeds eligibility
	// or any mutating path — so that restriction does not apply here.
	//
	// Capped to wellbeingSampleCap so this hook's cost is flat regardless of
	// city size: the first wellbeingSampleCap ids in ASCENDING id order.
	// liveResidentIDs()'s own concatenation (residents, then the optional
	// static seed-resident range, then migrants, then fertility children) is
	// already produced in strictly increasing base order — Wire's own
	// id-namespace-seam check enforces seedResidentIDBase's range sits below
	// attract.MigrantIDBase, which sits below citizens.FertilityChildIDBase
	// — so taking the slice's first N elements IS taking the first N ids in
	// ascending numeric order, with no separate O(n log n) sort needed every
	// month as population grows toward the 100M northstar.
	ids := st.liveResidentIDs()
	if len(ids) > wellbeingSampleCap {
		ids = ids[:wellbeingSampleCap]
	}

	var (
		sumPhysical float64
		sumMental   float64
		count       int
	)
	for _, id := range ids {
		cit, ok := st.citizens.CitizenAt(id, st.cid)
		if !ok {
			continue
		}
		// ContextInputs is left at its documented zero-value placeholder
		// (mirrors baselineOneHousingVacancy's own "no real bridge yet"
		// pattern elsewhere in this package, GR#15): compose has no
		// per-citizen household-crowding/venue-access/leisure-fit bridge
		// today. AC-14's graceful-degradation contract means a zero
		// PersonsPerRoom/CommunityVenueAccess/SportVenueAccess/LeisureFit
		// still produces a well-defined (if generously-optimistic)
		// attribution rather than a NaN or an error.
		attr, err := st.wellbeingAPI.AttributeCitizen(cit, month, wellbeing.ContextInputs{})
		if err != nil {
			continue
		}
		sumPhysical += attr.Physical.Total
		sumMental += attr.Mental.Total
		count++
	}

	st.wellbeingStatus = computeWellbeingStatus(st.wellbeingAPI, st.wellbeingSeams, sumPhysical, sumMental, count)
}

// wellbeingNeutralModifier is the documented NEUTRAL value every one of the
// four §18 downstream modifiers takes at perfect (100, 100) health, AND the
// value computeWellbeingStatus pins them to when there is no cohort data at
// all (round-2 P1-b fix) — never (0, 0), which reads as the SAME modifiers a
// genuinely catastrophic (0, 0) cohort would produce (mortality/emigration
// == 2.0, productivity/satisfaction == 0.0), a false maximum-severity report
// GR#17 forbids for a status surface backed by missing data.
const wellbeingNeutralModifier = 1.0

// computeWellbeingStatus builds one month's WellbeingStatus from the summed
// cohort tracks: count == 0 (no live citizen attributed successfully this
// month) yields the documented neutral report (NoData=true, zero means, all
// four modifiers pinned to wellbeingNeutralModifier) rather than deriving
// the four modifiers from a fabricated (0, 0) mean (round-2 P1-b). A pure
// function, independent of simState, so it is directly unit-testable at
// count == 0 without composing a full engine.
func computeWellbeingStatus(w *wellbeing.WellbeingAPI, seams []wellbeingSeamStatus, sumPhysical, sumMental float64, count int) WellbeingStatus {
	status := WellbeingStatus{SampleSize: count, Seams: seams}
	if count == 0 {
		status.NoData = true
		status.MortalityModifier = wellbeingNeutralModifier
		status.ProductivityModifier = wellbeingNeutralModifier
		status.SatisfactionModifier = wellbeingNeutralModifier
		status.EmigrationModifier = wellbeingNeutralModifier
		return status
	}
	status.MeanPhysical = sumPhysical / float64(count)
	status.MeanMental = sumMental / float64(count)
	status.MortalityModifier = w.MortalityModifier(status.MeanPhysical, status.MeanMental)
	status.ProductivityModifier = w.ProductivityModifier(status.MeanPhysical, status.MeanMental)
	status.SatisfactionModifier = w.SatisfactionModifier(status.MeanPhysical, status.MeanMental)
	status.EmigrationModifier = w.EmigrationModifier(status.MeanPhysical, status.MeanMental)
	return status
}

// Wellbeing returns the composed engine's MOD-034 WellbeingAPI instance —
// the same one wellbeingHook's monthly reconstruction drives. Read-only
// accessor for tests and inspection tooling, mirroring Composition.Traffic()/
// Composition.DeathServices()'s identical shape.
func (c *Composition) Wellbeing() *wellbeing.WellbeingAPI {
	return c.state.wellbeingAPI
}

// WellbeingStatus returns the composed engine's MOD-034 read-only status
// surface (GR#17): the last-reconstructed cohort mean tracks, the four
// downstream-effect modifiers computed from that mean (NOT applied to any
// consumer — see compose_wellbeing.go's package doc comment for the exact
// missing edges), and each input seam's live/degraded state. Before the
// monthly hook has run once (or whenever the live resident population is
// empty), NoData is true, SampleSize is 0, MeanPhysical/MeanMental are 0,
// and all four modifiers are pinned to the documented NEUTRAL value 1.0 —
// NEVER derived from the (0, 0) mean, which would read as a maximally
// catastrophic cohort (round-2 P1-b fix). Safe to call any time after Wire
// returns, including mid-run or after a run completes (single-goroutine,
// see simState's doc comment, mirrors Treasury()/MoneyFlows()). Every call
// returns a fresh, independently-owned copy — round-2 P2 fix: Seams is
// defensively cloned on every call, so mutating one call's returned
// WellbeingStatus can never perturb the composition's own report or a
// different call's result.
func (c *Composition) WellbeingStatus() WellbeingStatus {
	status := c.state.wellbeingStatus
	status.Seams = append([]wellbeingSeamStatus(nil), status.Seams...)
	return status
}
