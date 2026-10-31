package converge

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// populationKnownDivergentFields is the EXACT set of Contract fields on which
// the Go reference and the TS candidate genuinely diverge today (AC-6). It is
// pinned both ways: a field silently converging OR a new field silently
// diverging fails TestPopulationAB_KnownDivergence_NonEmpty and forces a
// human to update docs/planning/phase3-population-ab-report-2026-10-30.md.
var populationKnownDivergentFields = []string{"births", "deaths", "population"}

func divergedFields(r *Report) []string {
	seen := map[string]bool{}
	for _, d := range r.Diffs {
		seen[d.Field] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func populationGoRef(t *testing.T) Trajectory {
	t.Helper()
	return mustRunPopulation(t, loadPopulationJournal(t))
}

// TestPopulationAB_KnownDivergence_NonEmpty (AC-6a): the Go reference vs the
// real emitter-generated TS fixture diverge on exactly the pinned fields.
// Report-only: the divergence is logged, never fatal in itself.
func TestPopulationAB_KnownDivergence_NonEmpty(t *testing.T) {
	ref := populationGoRef(t)
	dom, cand, err := LoadFixture(populationWebconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if dom != "population" {
		t.Fatalf("fixture domain = %q, want population", dom)
	}
	report := Compare("population", ref, cand, PopulationDomain{}.Contract())
	if report.Pass {
		t.Fatal("HONESTY VIOLATION: Go ref and TS candidate report parity on population natural change; " +
			"the models differ by construction (per-citizen draws vs population-proportional rates). " +
			"Investigate before trusting this gate, then update the A/B report.")
	}
	got := divergedFields(report)
	if !reflect.DeepEqual(got, populationKnownDivergentFields) {
		t.Fatalf("divergent field set changed: got %v want %v — update the A/B report and this pin together", got, populationKnownDivergentFields)
	}
	t.Logf("population A/B report: pass=%v diffs=%d fields=%v", report.Pass, len(report.Diffs), got)
	for _, d := range report.Diffs {
		t.Logf("  %s", d.String())
	}
}

// TestPopulationAB_GreenIfFixturesMatch (AC-6b): a Go trajectory saved as a
// fixture and re-loaded compares as a pass — the pin above is capable of
// being green.
func TestPopulationAB_GreenIfFixturesMatch(t *testing.T) {
	ref := populationGoRef(t)
	p := filepath.Join(t.TempDir(), "self.json")
	if err := SaveFixture(p, "population", ref); err != nil {
		t.Fatal(err)
	}
	_, self, err := LoadFixture(p)
	if err != nil {
		t.Fatal(err)
	}
	if r := Compare("population", ref, self, PopulationDomain{}.Contract()); !r.Pass {
		t.Fatalf("Compare(go, go) must pass, got %v", r.Diffs)
	}
}

// TestPopulationAB_DivergentFixture_Fails (AC-6c): the gate has teeth — a
// deliberately perturbed candidate fails on the perturbed field.
func TestPopulationAB_DivergentFixture_Fails(t *testing.T) {
	ref := populationGoRef(t)
	cand := cloneTrajectory(ref)
	for i := range cand {
		cand[i].Values["population"] = cand[i].Values["population"] * 2
	}
	r := Compare("population", ref, cand, PopulationDomain{}.Contract())
	if r.Pass || !reflect.DeepEqual(divergedFields(r), []string{"population"}) {
		t.Fatalf("expected a population-only divergence, got pass=%v fields=%v", r.Pass, divergedFields(r))
	}
}

// TestPopulationAB_SeedAsymmetry_Fails: a candidate seeded with a different
// headcount fails the TierExact seed_headcount row rather than averaging away.
func TestPopulationAB_SeedAsymmetry_Fails(t *testing.T) {
	ref := populationGoRef(t)
	cand := cloneTrajectory(ref)
	for i := range cand {
		cand[i].Values["seed_headcount"]++
	}
	r := Compare("population", ref, cand, PopulationDomain{}.Contract())
	if r.Pass || !reflect.DeepEqual(divergedFields(r), []string{"seed_headcount"}) {
		t.Fatalf("expected seed_headcount-only divergence, got pass=%v fields=%v", r.Pass, divergedFields(r))
	}
}

func cloneTrajectory(t Trajectory) Trajectory {
	out := make(Trajectory, len(t))
	for i, s := range t {
		vals := make(map[string]int64, len(s.Values))
		for k, v := range s.Values {
			vals[k] = v
		}
		out[i] = Sample{Tick: s.Tick, Values: vals}
	}
	return out
}
