package converge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// servicesJournalFile is the on-disk shape of
// converge-services-actions.json: {"entries": [...]}, decoded straight
// into []JournalEntry since this fixture uses domain.go's generic Journal
// shape (unlike converge-finance-actions.json's bespoke actionEntry —
// see that file's own schema note). Extra top-level fields
// (schema/description/goCapacityNote/placementNote/populationNote) are
// TS-authoring documentation this Go reader ignores (encoding/json drops
// unrecognised fields).
type servicesJournalFile struct {
	Entries []JournalEntry `json:"entries"`
}

const servicesActionsPath = "../../webconsole/test/converge-fixtures/converge-services-actions.json"
const servicesWebconsoleFixture = "testdata/services-webconsole-v1.json"

// loadServicesJournal reads and decodes the canonical services journal
// shared with webconsole/test/converge-fixture-emit-services.mjs.
func loadServicesJournal(t *testing.T) Journal {
	t.Helper()
	b, err := os.ReadFile(servicesActionsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", servicesActionsPath, err)
	}
	var f servicesJournalFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("decoding %s: %v", servicesActionsPath, err)
	}
	if len(f.Entries) == 0 {
		t.Fatalf("%s: no entries decoded", servicesActionsPath)
	}
	return Journal(f)
}

// --- AC-6: determinism, Go side --------------------------------------------

// TestServicesDomain_DeterministicTrajectory proves GR#21 for the adapter
// itself: the SAME journal run against two freshly constructed
// *services.ServicesAPI instances (via two separate ServicesDomain.Run
// calls) produces a byte-for-byte (reflect.DeepEqual) identical Trajectory
// both times.
func TestServicesDomain_DeterministicTrajectory(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}

	first, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 sampled ticks (30/60/90), got %d: %+v", len(first), first)
	}

	for i := 0; i < 10; i++ {
		got, err := domain.Run(journal)
		if err != nil {
			t.Fatalf("Run iteration %d: %v", i, err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("iteration %d: trajectory differs.\nfirst=%+v\ngot=%+v", i, first, got)
		}
	}
}

// --- Domain-adapter-only fixture round trip (mirrors finance_domain_test.go) ---

// TestServicesDomain_MatchingFixture_Passes proves the harness end to end
// on the services domain: a fixture saved from a live ServicesDomain run
// compares as a pass against a fresh reference run of the same journal,
// under the domain's own Contract.
func TestServicesDomain_MatchingFixture_Passes(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}

	ref, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "services-fixture.json")
	if err := SaveFixture(path, domain.Name(), ref); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}
	fixtureDomain, candidate, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if fixtureDomain != domain.Name() {
		t.Fatalf("expected fixture domain %q, got %q", domain.Name(), fixtureDomain)
	}

	report := Compare(domain.Name(), ref, candidate, domain.Contract())
	if !report.Pass {
		t.Fatalf("expected matching fixture to pass parity, got diffs: %v", report.Diffs)
	}
}

// TestServicesDomain_DivergentFixture_Fails proves the gate has teeth on
// the services domain specifically: a fixture with one deliberately
// mutated value fails Compare under the domain's TierExact/TierBounded
// bars, naming the mutated field (AC-7's "perturbing either side").
func TestServicesDomain_DivergentFixture_Fails(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}

	ref, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ref) < 2 {
		t.Fatalf("expected at least 2 samples, got %d", len(ref))
	}

	divergent := make(Trajectory, len(ref))
	copy(divergent, ref)
	mutatedValues := make(map[string]int64, len(ref[1].Values))
	for k, v := range ref[1].Values {
		mutatedValues[k] = v
	}
	mutatedValues["fire_capacity"] = mutatedValues["fire_capacity"] + 1
	divergent[1] = Sample{Tick: ref[1].Tick, Values: mutatedValues}

	dir := t.TempDir()
	path := filepath.Join(dir, "services-fixture-divergent.json")
	if err := SaveFixture(path, domain.Name(), divergent); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}
	_, candidate, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	report := Compare(domain.Name(), ref, candidate, domain.Contract())
	if report.Pass {
		t.Fatalf("expected the divergent fixture to fail parity, got Pass=true")
	}
	found := false
	for _, d := range report.Diffs {
		if d.Field == "fire_capacity" && d.Tick == ref[1].Tick {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a diff naming fire_capacity at tick %d, got: %v", ref[1].Tick, report.Diffs)
	}
}

// TestServicesDomain_UnknownOp_FailsClosed proves a malformed/unknown
// journal op fails loudly (MET-H503) rather than being silently skipped.
func TestServicesDomain_UnknownOp_FailsClosed(t *testing.T) {
	domain := ServicesDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 1, Op: "not_a_real_op"},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for an unrecognised journal op, got nil")
	}
}

// TestServicesDomain_MalformedArgs_FailsClosed proves malformed op args
// fail loudly too, distinct from an unknown op name.
func TestServicesDomain_MalformedArgs_FailsClosed(t *testing.T) {
	domain := ServicesDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 1, Op: "set_population", Args: json.RawMessage(`{"n": "not-a-number"}`)},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for malformed op args, got nil")
	}
}

// TestServicesDomain_UnknownServiceKind_FailsClosed proves a journal that
// names a goKind ServicesAPI has no registered definition for fails
// through RegisterService's own ErrUnknownServiceKind, wrapped as
// codeJournalOpFailed — never silently dropped.
func TestServicesDomain_UnknownServiceKind_FailsClosed(t *testing.T) {
	domain := ServicesDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 0, Op: "place_service", Args: json.RawMessage(`{"buildingID":"fire_station","goKind":"not-a-real-kind","goServiceID":"x-1"}`)},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for an unregistered service kind, got nil")
	}
}

// TestServicesDomain_UnknownBuildingID_FailsClosed proves remediation point
// 3's catalogue lookup fails loudly on a buildingID with no
// data/buildings.json entry, rather than silently registering a
// zero-capacity service.
func TestServicesDomain_UnknownBuildingID_FailsClosed(t *testing.T) {
	domain := ServicesDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 0, Op: "place_service", Args: json.RawMessage(`{"buildingID":"not-a-real-building","goKind":"fire","goServiceID":"x-1"}`)},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for an unknown Go catalogue building id, got nil")
	}
}

// TestServicesDomain_ContractCoversEverySampledField proves the domain's
// own Contract never leaves a sampled field uncovered — otherwise
// Compare's fail-closed codeUnknownTolerance path would fire on every real
// run of this domain.
func TestServicesDomain_ContractCoversEverySampledField(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}
	traj, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	contract := domain.Contract()
	for _, s := range traj {
		for field := range s.Values {
			if _, ok := contract[field]; !ok {
				t.Fatalf("field %q is sampled but has no Contract tolerance entry", field)
			}
		}
	}
}

// projectFields returns a copy of traj whose each Sample's Values map is
// restricted to the keys named in fields — used by a narrower parity
// comparison that would otherwise trip Compare's fail-closed
// codeUnknownTolerance on every OTHER field the full trajectory reports
// (in particular the "citywide_*" fields below, which are a Go-only
// self-consistency concern — see servicesGroupContract's doc comment —
// and are never reported by the TS fixture at all).
func projectFields(traj Trajectory, fields Contract) Trajectory {
	out := make(Trajectory, len(traj))
	for i, s := range traj {
		values := make(map[string]int64, len(fields))
		for field := range fields {
			if v, ok := s.Values[field]; ok {
				values[field] = v
			}
		}
		out[i] = Sample{Tick: s.Tick, Values: values}
	}
	return out
}

// servicesGroupContract is ServicesDomain's Contract restricted to the
// three compared groups' own capacity/need/coverage fields (9 total),
// excluding the "citywide_*" fields — those are a Go-only cross-check
// (TestServicesDomain_CoverageSummary_MatchesPerGroupSums below), never
// reported by the TS fixture, so feeding them into a Go-vs-TS Compare call
// would only ever report "candidate sample does not report this field"
// noise, obscuring the REAL per-row divergence this mapping cares about.
func servicesGroupContract() Contract {
	full := ServicesDomain{}.Contract()
	c := make(Contract, len(servicesGroups)*3)
	for _, g := range servicesGroups {
		c[g+"_capacity"] = full[g+"_capacity"]
		c[g+"_need"] = full[g+"_need"]
		c[g+coverageFieldSuffix] = full[g+coverageFieldSuffix]
	}
	return c
}

// --- r1 remediation: the cross-engine comparison is an HONESTY proof, not
// a must-pass gate (mirrors finance_ab_test.go's
// TestFinanceAB_KnownDivergence_NonEmpty exactly) --------------------------
//
// The independent round's finding: v1 hand-picked a Go capacity literal
// that numerically matched the TS building it was "standing in for",
// which made the comparison pass while concealing that the two engines'
// catalogues use genuinely different units for the same service kind
// (Go's fire_station is "4 appliances", TS's fire_post is "served=4000
// people" — see the acceptance doc's Addendum). Now that capacity is
// sourced from the REAL data/buildings.json catalogue
// (services_domain.go's remediation point 3), every compared field
// genuinely diverges — exactly finance's own pre-flip honesty state for
// "treasury". These two tests prove that state is real (non-vacuous) and
// that the comparison mechanism itself is capable of reporting a pass
// (not a tautology that can never go green).

// TestServicesParity_KnownDivergence_NonEmpty is this increment's HONESTY
// REQUIREMENT proof (mirrors TestFinanceAB_KnownDivergence_NonEmpty): the
// REAL Go-vs-TS comparison, run for real under servicesGroupContract, is
// asserted NON-EMPTY — the two catalogues genuinely diverge today (capacity
// units, and therefore the derived coverage ratios, do not match). Report-
// only (t.Log, never t.Fatal on the diffs themselves) so `go test -v`
// surfaces every divergent field without failing the build on them.
//
// This test is EXPECTED TO GO RED the day the Go and TS catalogues are
// deliberately reconciled (a domain-flip decision, Section 6's open
// question, not this increment's call) — that is the intended trip-wire:
// a maintainer who makes the two capacities agree and accidentally makes
// this test fail is being told "the services parity contract needs
// revisiting now", rather than the reconciliation silently going
// unnoticed.
func TestServicesParity_KnownDivergence_NonEmpty(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}
	goTraj, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("ServicesDomain.Run: %v", err)
	}
	_, tsTraj, err := LoadFixture(servicesWebconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	contract := servicesGroupContract()
	report := Compare(domain.Name(), projectFields(goTraj, contract), projectFields(tsTraj, contract), contract)
	if report.Pass {
		t.Fatal("HONESTY VIOLATION: the services A/B gate reports Pass=true against the real TS fixture. " +
			"Per the acceptance doc's Addendum, Go's catalogue-sourced capacity (data/buildings.json) and " +
			"TS's SPECS-sourced capacity use different units for the same service kind and CANNOT genuinely " +
			"match under this journal — a passing report here means either the contract was quietly weakened " +
			"to something vacuous, or the fixture/adapter stopped exercising a real divergence. Investigate " +
			"before trusting this gate.")
	}
	t.Logf("services A/B report: domain=%s pass=%v diffs=%d", report.Domain, report.Pass, len(report.Diffs))
	for _, d := range report.Diffs {
		t.Logf("  %s", d.String())
	}
}

// TestServicesParity_KnownDivergence_GreenIfFixturesMatch is the flip side
// of the honesty proof (mirrors TestFinanceAB_KnownDivergence_GreenIfFixturesMatch):
// pointed at a Go trajectory saved AS a fixture and reloaded (ref and
// candidate IDENTICAL), Compare reports Pass=true — proving the "non-empty"
// assertion above is a REAL check, not a tautology that can never pass.
func TestServicesParity_KnownDivergence_GreenIfFixturesMatch(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}
	goTraj, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("ServicesDomain.Run: %v", err)
	}
	contract := servicesGroupContract()
	projected := projectFields(goTraj, contract)

	dir := t.TempDir()
	path := filepath.Join(dir, "services-self-fixture.json")
	if err := SaveFixture(path, domain.Name(), projected); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}
	_, selfCandidate, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	report := Compare(domain.Name(), projected, selfCandidate, contract)
	if !report.Pass {
		t.Fatalf("expected Compare(go, go) to pass (identical trajectories), got diffs: %v", report.Diffs)
	}
}

// TestServicesParity_TSUnclampedField_ExceedsGoClampedField proves the
// clamp01-vs-unclamped asymmetry the round's remediation point 1 records
// as an explicit divergence: at the low-population checkpoint (tick 30,
// every group over-provisioned), the TS fixture's own unclamped ratio for
// "fire" genuinely exceeds 1.0 (10000 on the ×10000 scale) while its
// CLAMPED sibling field caps at exactly 10000 — the two TS-side fields
// this increment's addendum says must both be visible are, in fact, both
// present and behave as documented. This does not touch the Go side at
// all (the point is the TS fixture's own two-field shape), but pins the
// specific behaviour Section 3's Addendum describes so a future emitter
// change that collapses the two fields back into one is caught.
func TestServicesParity_TSUnclampedField_ExceedsGoClampedField(t *testing.T) {
	_, tsTraj, err := LoadFixture(servicesWebconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(tsTraj) == 0 {
		t.Fatal("expected at least one TS sample")
	}
	first := tsTraj[0].Values
	clamped, ok := first["fire"+coverageFieldSuffix]
	if !ok {
		t.Fatalf("TS fixture's first sample has no fire%s field", coverageFieldSuffix)
	}
	unclamped, ok := first["fire_ts_unclamped"+coverageFieldSuffix]
	if !ok {
		t.Fatalf("TS fixture's first sample has no fire_ts_unclamped%s field", coverageFieldSuffix)
	}
	if clamped != ServicesCoverageScale {
		t.Fatalf("expected the clamped fire coverage at the over-provisioned checkpoint to read exactly %d, got %d", ServicesCoverageScale, clamped)
	}
	if unclamped <= ServicesCoverageScale {
		t.Fatalf("expected the UNCLAMPED fire coverage to exceed the clamp ceiling %d at an over-provisioned checkpoint, got %d — the fixture may have collapsed the two fields", ServicesCoverageScale, unclamped)
	}
}

// --- r1 remediation points 1-3: Go self-consistency, pinned against
// TODAY's data/buildings.json + the engine's own coverageRatio()/
// CoverageForDistrict/CoverageSummary — the REAL red-proof anchors -------
//
// These do NOT depend on the TS fixture at all. Each pinned literal below
// is DERIVED, not invented: capacity from data.CapacityFromRaw applied to
// the CURRENT data/buildings.json entry (fire_station="4 appliances"->4,
// primary_school="240"->240 x3 registered instances=720, clinic="150
// visits/d"->150); demand from needOf() at population=12000 (tick 90, the
// checkpoint where demand genuinely binds capacity for every group —
// see converge-services-actions.json's tick-90 note); coverage from the
// engine's OWN coverageRatio() (coverage.go:68-73) applied to those two
// numbers. Mirrors finance_ab_test.go's own "mirrored here as a literal
// since it is package-private" precedent for pinning a cross-package
// constant.
//
// Sensitivity (the round's three RED-proofs):
//   - A hypothetical coverageRatio()×0.5 mutation would corrupt EVERY
//     "*_coverage_x10000"/"citywide_coverage_x10000" value pinned below
//     (remediation point 1).
//   - A hypothetical UpdateDemand/UpdateDistrictDemand-gutting mutation
//     would zero every "*_need"/"citywide_demand" value AND collapse
//     every coverage ratio to 1.0 (the demand==0 branch) — both pinned
//     below diverge from that (remediation point 2).
//   - A hypothetical capacityCeiling()×0.9 mutation changes every
//     "*_capacity"/"citywide_capacity" value pinned below EXCEPT fire's
//     (4×0.9=3.6 rounds back to 4 — noted honestly rather than silently
//     assumed sensitive); education (720->648) and healthcare (150->135)
//     both catch it regardless (remediation point 3, first half).
//   - A scratch-edit to data/buildings.json's fire_station/primary_school/
//     clinic capacityRaw changes the LIVE catalogue loadServicesCatalogue()
//     reads at Run() time, changing every "*_capacity" value pinned below
//     away from its literal (remediation point 3, second half).
func TestServicesDomain_EngineReadsPinnedAtTick90(t *testing.T) {
	journal := loadServicesJournal(t)
	domain := ServicesDomain{}
	traj, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var s90 *Sample
	for i := range traj {
		if traj[i].Tick == 90 {
			s90 = &traj[i]
		}
	}
	if s90 == nil {
		t.Fatalf("expected a tick=90 sample, got ticks: %+v", traj)
	}

	want := map[string]int64{
		"fire_capacity":                    4,
		"fire_need":                        12000,
		"fire" + coverageFieldSuffix:       3,
		"education_capacity":               720,
		"education_need":                   2760,
		"education" + coverageFieldSuffix:  2609,
		"healthcare_capacity":              150,
		"healthcare_need":                  24000,
		"healthcare" + coverageFieldSuffix: 63,
		"citywide_capacity":                874,
		"citywide_demand":                  38760,
		"citywide" + coverageFieldSuffix:   225,
	}
	for field, expect := range want {
		got, ok := s90.Values[field]
		if !ok {
			t.Fatalf("tick=90 sample has no field %q", field)
		}
		if got != expect {
			t.Errorf("tick=90 field %q: got %d, want %d (pinned against today's data/buildings.json + engine.services' own coverageRatio()) — an engine or catalogue mutation changed this", field, got, expect)
		}
	}
}

// TestServicesDomain_CapacitySourcedFromCatalogue_NotHandAuthored proves
// remediation point 3 directly: the SAME buildingID registered under
// different ServiceIDs sums correctly (education = 3 x primary_school =
// 720, not a single hand-picked literal), and the capacity is IDENTICAL
// to CapacityFromRaw applied to the current catalogue entry — i.e. this
// value could ONLY have come from data/buildings.json, not a journal
// literal (the journal carries no capacity field at all any more, see
// converge-services-actions.json's r1RemediationNote).
func TestServicesDomain_CapacitySourcedFromCatalogue_NotHandAuthored(t *testing.T) {
	catalogue, err := loadServicesCatalogue()
	if err != nil {
		t.Fatalf("loadServicesCatalogue: %v", err)
	}
	fireStation, ok := buildingByID(catalogue, "fire_station")
	if !ok {
		t.Fatal("data/buildings.json has no fire_station entry")
	}
	primarySchool, ok := buildingByID(catalogue, "primary_school")
	if !ok {
		t.Fatal("data/buildings.json has no primary_school entry")
	}
	clinic, ok := buildingByID(catalogue, "clinic")
	if !ok {
		t.Fatal("data/buildings.json has no clinic entry")
	}

	journal := loadServicesJournal(t)
	traj, err := ServicesDomain{}.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	first := traj[0].Values

	wantFire := roundInt(capacityFromRawForTest(fireStation.CapacityRaw))
	wantEducation := roundInt(3 * capacityFromRawForTest(primarySchool.CapacityRaw))
	wantHealthcare := roundInt(capacityFromRawForTest(clinic.CapacityRaw))

	if first["fire_capacity"] != wantFire {
		t.Errorf("fire_capacity = %d, want %d (fire_station.capacityRaw=%q)", first["fire_capacity"], wantFire, fireStation.CapacityRaw)
	}
	if first["education_capacity"] != wantEducation {
		t.Errorf("education_capacity = %d, want %d (3x primary_school.capacityRaw=%q)", first["education_capacity"], wantEducation, primarySchool.CapacityRaw)
	}
	if first["healthcare_capacity"] != wantHealthcare {
		t.Errorf("healthcare_capacity = %d, want %d (clinic.capacityRaw=%q)", first["healthcare_capacity"], wantHealthcare, clinic.CapacityRaw)
	}
}

// TestServicesDomain_CoverageSummary_MatchesPerGroupSums exercises
// [services.ServicesAPI.CoverageSummary] as a first-class read
// (remediation point 1's "the reads the spec names"): the citywide
// capacity/demand this method reports equals the sum of the three
// per-group CoverageForDistrict figures, proving CoverageSummary is a
// real, load-bearing cross-check of the SAME underlying instance/demand
// state — not a decorative call whose return value is discarded.
func TestServicesDomain_CoverageSummary_MatchesPerGroupSums(t *testing.T) {
	journal := loadServicesJournal(t)
	traj, err := ServicesDomain{}.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, s := range traj {
		var wantCapacity, wantDemand int64
		for _, g := range servicesGroups {
			wantCapacity += s.Values[g+"_capacity"]
			wantDemand += s.Values[g+"_need"]
		}
		if s.Values["citywide_capacity"] != wantCapacity {
			t.Errorf("tick=%d citywide_capacity=%d, want sum-of-groups %d", s.Tick, s.Values["citywide_capacity"], wantCapacity)
		}
		if s.Values["citywide_demand"] != wantDemand {
			t.Errorf("tick=%d citywide_demand=%d, want sum-of-groups %d", s.Tick, s.Values["citywide_demand"], wantDemand)
		}
	}
}

// capacityFromRawForTest wraps services.CapacityFromRaw for readability at
// this file's call sites.
func capacityFromRawForTest(raw string) float64 {
	return services.CapacityFromRaw(raw)
}
