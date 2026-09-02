package converge

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ServicesDomain is FEAT-2326609747 (services.convergence) inc1's [Domain]
// adapter for engine.services' coverage half.
//
// # r1 remediation (this file's second draft — the independent round's
// REJECT)
//
// The round's finding: the first draft never actually READ
// engine.services' own coverage arithmetic — it re-derived a
// capacity/demand ratio locally with hand-authored capacity literals, which
// CONCEALED a real, live divergence (coverageRatio() — coverage.go:68-73 —
// clamps to [0,1]; TS's serviceCoverageOf row() does not) and made a
// coverageRatio()×0.5 source mutation invisible to every test. Three fixes,
// all present below:
//
//  1. Every compared coverage figure now comes from a REAL ServicesAPI
//     read — [services.ServicesAPI.CoverageForDistrict] (one district per
//     compared group, Section 3's mapping groups) and
//     [services.ServicesAPI.CoverageSummary] (the whole-city cross-check) —
//     never a hand-rolled re-implementation of coverageRatio's arithmetic.
//     The clamp asymmetry itself is now an EXPLICIT, named divergence
//     (Section 3 addendum of the acceptance doc): this adapter's
//     "*_coverage_x10000" field is the CLAMPED value (matching the TS
//     emitter's own min(1, ...) representation of the same field, so the
//     two SHOULD agree once capacity/demand genuinely align — they do not
//     yet, see point 3), while the TS-only "*_ts_unclamped_coverage_x10000"
//     field (converge-fixture-emit-services.mjs) carries the raw,
//     un-clamped TS ratio for visibility, never fed into this domain's
//     Contract (Compare only checks fields the Go reference reports).
//  2. UpdateDemand/UpdateDistrictDemand are now load-bearing: this
//     adapter's "*_need" field is [DistrictCoverage.TotalDemand] read back
//     FROM the engine (via CoverageForDistrict), not the locally-computed
//     needOf() value echoed straight into the Sample — a mutation that
//     guts either push call now changes the reported value.
//  3. Capacity is now sourced via [services.ServiceSpecFromBuilding] from
//     the REAL data/buildings.json catalogue (mirroring
//     internal/engine/build/build.go's registerServiceLocked, the actual
//     build→services bridge), never a hand-authored journal literal — see
//     the catalogue note on the "place_service" op below. This is
//     DELIBERATELY expected to diverge hugely from TS's SPECS capacity
//     (different buildings, different units — fire_station's "4
//     appliances" vs TS fire_post's "served=4000 people" is not a bug to
//     paper over, it is the honest pre-flip state), exactly the class
//     finance's own TestFinanceAB_KnownDivergence_NonEmpty documents for
//     treasury. See services_domain_test.go's
//     TestServicesParity_KnownDivergence_NonEmpty.
//
// # AC-8 (interim sampling source)
//
// engine.services has no [save.Participant] yet (BOW FEAT-2326609743 is the
// separate, in-flight item that will give it one) — so this adapter reads
// *services.ServicesAPI directly (pre-serialization), per AC-8's documented
// interim.
//
// # Wellbeing is OUT of this file's scope (Section 6.2's open question)
//
// See this type's original doc history: WellbeingAPI is a per-citizen
// driver-decomposed attribution engine with no coverage-consuming
// composite comparable to TS's wellbeingOf — deferred to a follow-up
// increment pending Aaron's ruling, per Section 2's explicit "NOT in
// inc1: any new... wellbeing sub-score."
//
// # Field mapping (Section 3 of the spec, as amended by this file's
// addendum — see the acceptance doc's "Addendum" section)
//
// Three compared groups: "fire" (1:1 with TS's 'fire' row), "education"
// (Go's single kind vs the SUM of TS's 'nursery'+'primary'+'college'),
// "healthcare" (Go's single kind vs the SUM of TS's 'gp'+'hosp'). Each
// reports "<group>_capacity"/"<group>_need" (engine reads, TierExact) and
// "<group>_coverage_x10000" (engine's own clamped ratio, TierBounded at
// [ServicesCoverageEpsilon]). Plus three "citywide_*" fields from
// [services.ServicesAPI.CoverageSummary] over every registered instance —
// a Go-only cross-check (see services_domain_test.go), not compared
// against any single TS row (the spec's mapping table names no
// city-wide-combined TS figure).
//
// Police and power/electricity are NOT compared rows in this increment:
// data/buildings.json currently carries no serviceKind-tagged entry for
// either ("police_desk"/"police_station" and every power-plant entry lack
// the serviceKind/coverageRadius/staffingNeed fields
// registerServiceLocked's bridge requires — only clinic/small_hospital/
// general_hospital/teaching_hospital/one_room_school/primary_school/
// secondary_school/fire_station carry them, confirmed by `grep -n
// '"serviceKind"' data/buildings.json` returning exactly those eight
// entries), so sourcing them via ServiceSpecFromBuilding (point 3 above)
// is not possible without inventing a capacity — exactly the
// hand-authored-literal anti-pattern this remediation removes. This
// corrects the original mapping table's implicit "police is ready to
// compare today" framing; see the acceptance doc's addendum.
type ServicesDomain struct{}

// Name implements Domain.
func (ServicesDomain) Name() string { return "services" }

// servicesGroups is the fixed set of compared groups (Section 3), in a
// deterministic order (GR#21).
var servicesGroups = []string{"fire", "education", "healthcare"}

// coverageFieldSuffix names the fixed-point coverage field suffix both
// this file and converge-fixture-emit-services.mjs use — computed once
// rather than repeating fmt.Sprintf at every call site.
var coverageFieldSuffix = fmt.Sprintf("_coverage_x%d", ServicesCoverageScale)

// Contract implements Domain. Every group's capacity/need are TierExact
// (both are real engine reads — an int64 sum of catalogue-sourced
// capacities and an int64 sum of pushed demand, GR#21-deterministic given
// a fixed journal); coverage is TierBounded at ServicesCoverageEpsilon
// (AC-4). The three citywide_* fields are also declared here so a real run
// never trips Compare's fail-closed codeUnknownTolerance — they exist for
// services_domain_test.go's Go-only cross-check, not for cross-engine
// comparison (no TS row names a whole-city combined figure).
func (ServicesDomain) Contract() Contract {
	c := make(Contract, len(servicesGroups)*3+3)
	for _, g := range servicesGroups {
		c[g+"_capacity"] = Tolerance{Tier: TierExact}
		c[g+"_need"] = Tolerance{Tier: TierExact}
		c[g+coverageFieldSuffix] = Tolerance{Tier: TierBounded, Epsilon: ServicesCoverageEpsilon}
	}
	c["citywide_capacity"] = Tolerance{Tier: TierExact}
	c["citywide_demand"] = Tolerance{Tier: TierExact}
	c["citywide"+coverageFieldSuffix] = Tolerance{Tier: TierBounded, Epsilon: ServicesCoverageEpsilon}
	return c
}

// Run implements Domain: constructs a fresh *services.ServicesAPI and a
// fresh catalogue load, applies j's entries in order, and returns the
// resulting Trajectory. Deterministic (GR#21): no wall clock, a fixed
// servicesGroups iteration order, and every ServicesAPI read is a single
// keyed lookup or the API's own internally-sorted aggregate — never a raw
// map range in this file.
func (ServicesDomain) Run(j Journal) (Trajectory, error) {
	api := services.New("")
	catalogue, err := loadServicesCatalogue()
	if err != nil {
		return nil, err
	}
	registered := map[string][]services.ServiceID{}
	var population int64
	var traj Trajectory

	for _, entry := range j.Entries {
		switch entry.Op {
		case "place_service":
			var args struct {
				BuildingID  string `json:"buildingID"`
				GoKind      string `json:"goKind"`
				GoServiceID string `json:"goServiceID"`
			}
			if err := json.Unmarshal(entry.Args, &args); err != nil {
				return nil, journalOpFailed(entry, fmt.Sprintf("malformed args: %v", err))
			}
			be, ok := buildingByID(catalogue, args.BuildingID)
			if !ok {
				return nil, journalOpFailed(entry, fmt.Sprintf("unknown Go catalogue building id %q (data/buildings.json)", args.BuildingID))
			}
			// Mirrors internal/engine/build/build.go's registerServiceLocked
			// EXACTLY: ServiceSpecFromBuilding sources CapacityRaw/Milestone
			// from the catalogue entry (AC-10 — capacity is never a
			// hand-authored duplicate), then CoverageRadius/StaffingNeed are
			// copied across the same way the real build->services bridge
			// does (X/Y are left zero — this fixture computes no spatial
			// distance term, DemandDistance is always 0 below).
			spec := services.ServiceSpecFromBuilding(services.ServiceID(args.GoServiceID), services.ServiceKind(args.GoKind), be)
			spec.CoverageRadius = be.CoverageRadius
			spec.StaffingNeed = be.StaffingNeed
			if err := api.RegisterService(spec); err != nil {
				return nil, journalOpFailed(entry, err.Error())
			}
			registered[args.GoKind] = append(registered[args.GoKind], spec.ID)

		case "set_population":
			var args struct {
				N int64 `json:"n"`
			}
			if err := json.Unmarshal(entry.Args, &args); err != nil {
				return nil, journalOpFailed(entry, fmt.Sprintf("malformed args: %v", err))
			}
			if args.N < 0 {
				return nil, journalOpFailed(entry, "population must be non-negative")
			}
			population = args.N
			if err := pushGroupDemand(api, registered, population); err != nil {
				return nil, journalOpFailed(entry, err.Error())
			}

		case "sample":
			s, err := snapshotServices(api, registered, entry.Tick)
			if err != nil {
				return nil, journalOpFailed(entry, err.Error())
			}
			traj = append(traj, s)

		default:
			return nil, journalOpFailed(entry, "unrecognised op name")
		}
	}
	return traj, nil
}

// journalOpFailed wraps a services-domain journal-application failure as
// codeJournalOpFailed (MET-H503) — the SAME code applyFinanceJournalOp uses
// (errors.go's doc comment: "used by every in-package Domain adapter"), so
// this increment claims zero new registry codes.
func journalOpFailed(entry JournalEntry, reason string) error {
	return errs.New(codeJournalOpFailed, errs.NewCorrelationID(), map[string]any{
		"tick": entry.Tick, "op": entry.Op, "domain": "services", "reason": reason,
	})
}

// loadServicesCatalogue resolves data/'s directory and loads
// data/buildings.json fresh — remediation point 3: capacity must come from
// the LIVE catalogue at Run() time (so a scratch-edit to a building's
// capacityRaw is visible to a fresh Run, the round's own RED-proof
// requirement), never a value baked into the journal.
func loadServicesCatalogue() (data.Buildings, error) {
	cid := errs.NewCorrelationID()
	dir, err := data.ResolveDataDir(cid)
	if err != nil {
		return data.Buildings{}, err
	}
	return data.LoadBuildings(dir, cid)
}

// buildingByID finds catalogue entry id, mirroring the lookup
// internal/engine/build's own order-resolution path performs (that
// package does it via its own order/entry bookkeeping this harness has no
// access to — this is the equivalent minimal lookup for a package that
// only needs it a handful of times per Run).
func buildingByID(b data.Buildings, id string) (data.BuildingEntry, bool) {
	for _, e := range b.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return data.BuildingEntry{}, false
}

// pushGroupDemand distributes each group's population-derived need evenly
// across that group's registered instances via BOTH UpdateDemand (feeds
// CoverageSummary's TotalDemand) and UpdateDistrictDemand under a district
// named for the group (feeds CoverageForDistrict's TotalDemand) — the same
// per-instance value through both engine paths, remediation point 2: both
// pushes are now read back into compared/cross-checked fields
// (snapshotServices), so neither is decorative.
func pushGroupDemand(api *services.ServicesAPI, registered map[string][]services.ServiceID, population int64) error {
	for _, group := range servicesGroups {
		ids := registered[group]
		if len(ids) == 0 {
			continue
		}
		perInstance := needOf(group, population) / float64(len(ids))
		for _, id := range ids {
			if err := api.UpdateDemand(id, perInstance, 0); err != nil {
				return err
			}
			if err := api.UpdateDistrictDemand(services.DistrictID(group), id, perInstance, 0); err != nil {
				return err
			}
		}
	}
	return nil
}

// The population-need multipliers below are a DELIBERATE literal
// duplication of webconsole/src/sim/data.ts's serviceCoverageOf row
// formulas (data.ts:2295-2302: `pop*0.06` nursery, `pop*0.12` primary,
// `pop*0.05` college, `pop` gp, `pop` hosp, `pop` fire) — this Go harness
// package may not import TS source, mirroring
// converge-fixture-emit-services.mjs's own COVERAGE_SCALE duplication in
// the reverse direction. Any future re-balance of those TS multipliers
// must update these constants too, or the fixture silently compares
// against a stale formula (the units-lint/BUG-355 class of drift).
const (
	eduNurseryNeedFrac = 0.06
	eduPrimaryNeedFrac = 0.12
	eduCollegeNeedFrac = 0.05
)

// needOf computes group's population-derived demand — the input this
// adapter PUSHES into the engine (pushGroupDemand); the compared "_need"
// field itself is read back FROM the engine (DistrictCoverage.TotalDemand,
// snapshotServices), not this function's return value directly, per
// remediation point 2.
func needOf(group string, population int64) float64 {
	pop := float64(population)
	switch group {
	case "fire":
		return pop
	case "education":
		return pop*eduNurseryNeedFrac + pop*eduPrimaryNeedFrac + pop*eduCollegeNeedFrac
	case "healthcare":
		return pop + pop // gp + hosp, each need=population (data.ts:2301-2302)
	default:
		return 0
	}
}

// snapshotServices reads every compared field FROM the real ServicesAPI
// aggregates — [ServicesAPI.CoverageForDistrict] per group (remediation
// points 1/2: capacity, demand, and the ENGINE's own clamped coverage
// ratio, never a locally re-implemented formula) plus
// [ServicesAPI.CoverageSummary] for the citywide cross-check fields.
func snapshotServices(api *services.ServicesAPI, registered map[string][]services.ServiceID, tick int64) (Sample, error) {
	values := make(map[string]int64, len(servicesGroups)*3+3)

	for _, group := range servicesGroups {
		if len(registered[group]) == 0 {
			// No instance registered for this group at all — CoverageForDistrict
			// would return ErrUnknownDistrict since UpdateDistrictDemand is
			// only ever called for a group with >=1 registered id
			// (pushGroupDemand). Report the same all-zero/fully-covered shape
			// serviceCoverageOf's row() uses for a genuinely absent service
			// (need<=0 branch — coverage:=1), never a fabricated non-zero
			// number.
			values[group+"_capacity"] = 0
			values[group+"_need"] = 0
			values[group+coverageFieldSuffix] = ServicesCoverageScale
			continue
		}
		dc, err := api.CoverageForDistrict(services.DistrictID(group))
		if err != nil {
			return Sample{}, err
		}
		values[group+"_capacity"] = roundInt(dc.TotalCapacity)
		values[group+"_need"] = roundInt(dc.TotalDemand)
		values[group+coverageFieldSuffix] = roundInt(dc.CoverageRatio * ServicesCoverageScale)
	}

	cs, err := api.CoverageSummary()
	if err != nil {
		return Sample{}, err
	}
	values["citywide_capacity"] = roundInt(cs.TotalCapacity)
	values["citywide_demand"] = roundInt(cs.TotalDemand)
	values["citywide"+coverageFieldSuffix] = roundInt(cs.CoverageRatio * ServicesCoverageScale)

	return Sample{Tick: tick, Values: values}, nil
}

// roundInt rounds v to the nearest integer (half away from zero, matching
// Go's math.Round and JS's Math.round for the non-negative values this
// fixture ever produces), returned as int64.
func roundInt(v float64) int64 {
	return int64(math.Round(v))
}
