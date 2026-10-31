package converge

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const populationActionsPath = "../../webconsole/test/converge-fixtures/converge-population-actions.json"
const populationWebconsoleFixture = "testdata/population-webconsole-v1.json"

// populationJournalFile is the on-disk shape of converge-population-actions.json
// ({"entries": [...]}, generic Journal shape; extra fields are documentation).
type populationJournalFile struct {
	Entries []JournalEntry `json:"entries"`
}

func loadPopulationJournal(t *testing.T) Journal {
	t.Helper()
	b, err := os.ReadFile(populationActionsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", populationActionsPath, err)
	}
	var f populationJournalFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("decoding %s: %v", populationActionsPath, err)
	}
	if len(f.Entries) == 0 {
		t.Fatalf("%s: no entries decoded", populationActionsPath)
	}
	return Journal(f)
}

func mustRunPopulation(t *testing.T, j Journal) Trajectory {
	t.Helper()
	traj, err := PopulationDomain{}.Run(j)
	if err != nil {
		t.Fatalf("PopulationDomain.Run: %v", err)
	}
	return traj
}

func sumField(traj Trajectory, field string) int64 {
	var s int64
	for _, smp := range traj {
		s += smp.Values[field]
	}
	return s
}

// TestPopulationDomain_EngineReadsPinned (AC-1): the trajectory is read from
// the REAL citizens engine. Built-but-not-wired guard: births AND deaths are
// non-zero, population actually moves, and the conservation identity
// pop_end = seed + sum(births) - sum(deaths) holds against TotalPopulation.
// Prints the per-tick table (tester evidence).
func TestPopulationDomain_EngineReadsPinned(t *testing.T) {
	traj := mustRunPopulation(t, loadPopulationJournal(t))
	if len(traj) != 36 {
		t.Fatalf("expected 36 monthly samples, got %d", len(traj))
	}
	t.Logf("%-6s %-10s %-7s %-7s %-8s", "tick", "population", "births", "deaths", "seeded")
	for _, s := range traj {
		t.Logf("%-6d %-10d %-7d %-7d %-8d", s.Tick, s.Values["population"], s.Values["births"], s.Values["deaths"], s.Values["seed_headcount"])
	}
	births, deaths := sumField(traj, "births"), sumField(traj, "deaths")
	if births == 0 || deaths == 0 {
		t.Fatalf("engine produced births=%d deaths=%d over 36 months; both must be non-zero (a constant trajectory is a built-but-not-wired FAIL)", births, deaths)
	}
	seeded := traj[0].Values["seed_headcount"]
	if seeded != 2200 {
		t.Fatalf("seed_headcount = %d, want 2200", seeded)
	}
	last := traj[len(traj)-1].Values["population"]
	if want := seeded + births - deaths; last != want {
		t.Fatalf("population conservation broken: TotalPopulation=%d, seed+births-deaths=%d", last, want)
	}
	if last == seeded {
		t.Fatalf("population never moved from the seed (%d)", seeded)
	}
	// A locally computed linear trajectory (seed + k*ticks) must not equal
	// the engine's: pin that the series is not arithmetic.
	d1 := traj[1].Values["population"] - traj[0].Values["population"]
	linear := true
	for i := 1; i < len(traj); i++ {
		if traj[i].Values["population"]-traj[i-1].Values["population"] != d1 {
			linear = false
			break
		}
	}
	if linear {
		t.Fatalf("population series is perfectly linear (step %d): not an engine read", d1)
	}
}

// TestPopulationDomain_DeterministicTrajectory (AC-2): two fresh runs of the
// same journal are reflect.DeepEqual.
func TestPopulationDomain_DeterministicTrajectory(t *testing.T) {
	j := loadPopulationJournal(t)
	first := mustRunPopulation(t, j)
	for i := 0; i < 3; i++ {
		got := mustRunPopulation(t, j)
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("iteration %d: trajectory differs", i)
		}
	}
}

// TestPopulationDomain_ContractCoversEverySampledField (AC-3): every field the
// domain samples has a Contract entry, and a field with none fails closed.
func TestPopulationDomain_ContractCoversEverySampledField(t *testing.T) {
	traj := mustRunPopulation(t, loadPopulationJournal(t))
	contract := PopulationDomain{}.Contract()
	var fields []string
	for k := range traj[0].Values {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	for _, f := range fields {
		if _, ok := contract[f]; !ok {
			t.Errorf("sampled field %q has no Contract entry", f)
		}
	}
	for _, f := range []string{"population", "births", "deaths"} {
		tol := contract[f]
		if tol.Tier != TierDistribution || tol.Window != PopulationDistWindow || tol.BandPct != PopulationBandPct {
			t.Errorf("%s tolerance = %+v, want TierDistribution window=%d band=%v", f, tol, PopulationDistWindow, PopulationBandPct)
		}
	}
	if contract["seed_headcount"].Tier != TierExact {
		t.Errorf("seed_headcount must be TierExact, got %v", contract["seed_headcount"].Tier)
	}

	// Fail closed: a sampled field with no entry is reported, not passed.
	ref := cloneTrajectory(traj)
	ref[0].Values["mystery_field"] = 1
	r := Compare("population", ref, traj, contract)
	if r.Pass {
		t.Fatal("a sampled field with no Contract entry must fail closed")
	}
	found := false
	for _, d := range r.Diffs {
		if d.Field == "mystery_field" && strings.Contains(d.Reason, codeUnknownTolerance) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %s diff naming mystery_field, got %v", codeUnknownTolerance, r.Diffs)
	}
}

// TestPopulationDomain_DirectionalBand (AC-3): a candidate with 2x the birth
// rate must fail, one equal to the reference must pass. Catches a band
// widened until everything passes.
func TestPopulationDomain_DirectionalBand(t *testing.T) {
	ref := mustRunPopulation(t, loadPopulationJournal(t))
	contract := PopulationDomain{}.Contract()

	if r := Compare("population", ref, cloneTrajectory(ref), contract); !r.Pass {
		t.Fatalf("candidate == ref must pass, got %v", r.Diffs)
	}
	twice := cloneTrajectory(ref)
	for i := range twice {
		twice[i].Values["births"] *= 2
	}
	r := Compare("population", ref, twice, contract)
	if r.Pass || !reflect.DeepEqual(divergedFields(r), []string{"births"}) {
		t.Fatalf("2x births must fail on births only, got pass=%v fields=%v", r.Pass, divergedFields(r))
	}
}

// TestPopulationDomain_DistributionWarmupAndEvaluated (AC-4): no divergence is
// reported before Window samples have accumulated, and at least one tick past
// warm-up is genuinely evaluated (a perturbed candidate IS caught) — a gate
// that cannot evaluate must not report success.
func TestPopulationDomain_DistributionWarmupAndEvaluated(t *testing.T) {
	ref := mustRunPopulation(t, loadPopulationJournal(t))
	if len(ref) <= PopulationDistWindow {
		t.Fatalf("journal has %d samples, need more than Window=%d to evaluate anything", len(ref), PopulationDistWindow)
	}
	bad := cloneTrajectory(ref)
	for i := range bad {
		bad[i].Values["births"] = bad[i].Values["births"]*2 + 10
	}
	r := Compare("population", ref, bad, PopulationDomain{}.Contract())
	evaluated := 0
	firstEvaluatedTick := int64(-1)
	for _, d := range r.Diffs {
		if d.Field != "births" {
			continue
		}
		evaluated++
		if firstEvaluatedTick < 0 || d.Tick < firstEvaluatedTick {
			firstEvaluatedTick = d.Tick
		}
	}
	if evaluated == 0 {
		t.Fatal("vacuous pass: zero ticks were evaluated for births under the distribution contract")
	}
	warmupEnds := ref[PopulationDistWindow-1].Tick
	if firstEvaluatedTick < warmupEnds {
		t.Fatalf("a divergence was reported at tick %d, before the Window=%d warm-up completed at tick %d", firstEvaluatedTick, PopulationDistWindow, warmupEnds)
	}
	if firstEvaluatedTick != warmupEnds {
		t.Fatalf("first evaluated tick = %d, want exactly the end of warm-up %d", firstEvaluatedTick, warmupEnds)
	}
}

// TestPopulationDomain_FailsClosed (AC-7): bad input returns a coded error
// (MET-H503), never a zero-valued sample.
func TestPopulationDomain_FailsClosed(t *testing.T) {
	cases := map[string]Journal{
		"unknown op":          {Entries: []JournalEntry{{Op: "bogus"}}},
		"malformed seed":      {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":"x"}`)}}},
		"negative count":      {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":-5,"ageYears":30}`)}}},
		"zero count":          {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":0,"ageYears":30}`)}}},
		"age out of range":    {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":10,"ageYears":200}`)}}},
		"negative age":        {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":10,"ageYears":-1}`)}}},
		"zero days":           {Entries: []JournalEntry{{Op: "advance", Args: json.RawMessage(`{"days":0}`)}}},
		"malformed advance":   {Entries: []JournalEntry{{Op: "advance", Args: json.RawMessage(`{"days":"x"}`)}}},
		"valid seed then bad": {Entries: []JournalEntry{{Op: "seed", Args: json.RawMessage(`{"count":10,"ageYears":30}`)}, {Op: "nope"}}},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		traj, err := PopulationDomain{}.Run(cases[n])
		if err == nil {
			t.Errorf("%s: expected an error, got nil (traj=%v)", n, traj)
			continue
		}
		if traj != nil {
			t.Errorf("%s: a failed Run must not return a trajectory, got %v", n, traj)
		}
		if !strings.Contains(err.Error(), codeJournalOpFailed) {
			t.Errorf("%s: error %q does not carry code %s", n, err.Error(), codeJournalOpFailed)
		}
	}
}
