package converge

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// sampleFinanceJournal returns a small, deterministic journal exercising
// several finance stages across two simulated months, sampling the
// trajectory at the end of each month.
func sampleFinanceJournal(t *testing.T) Journal {
	t.Helper()
	arg := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal journal arg: %v", err)
		}
		return b
	}
	seedTreasury := arg(map[string]any{
		"description": "opening treasury balance (test seed)",
		"entries": []map[string]any{
			{"account": string(finance.AcctTreasury), "side": "credit", "amount": 50_000_000, "category": "imports"},
			{"account": string(finance.AcctExternal), "side": "debit", "amount": 50_000_000, "category": "imports"},
		},
	})
	return Journal{Entries: []JournalEntry{
		{Tick: 1, Op: "begin_month", Args: arg(map[string]int64{"month": 1})},
		{Tick: 1, Op: "post", Args: seedTreasury},
		{Tick: 1, Op: "post_wages", Args: arg(map[string]int64{"total": 10_000_000})},
		{Tick: 1, Op: "post_household_spend", Args: arg(map[string]int64{"quantity": 100, "price": 5_000})},
		{Tick: 1, Op: "settle_opex", Args: arg(map[string]int64{"opex": 2_000_000})},
		{Tick: 1, Op: "sample"},
		{Tick: 2, Op: "begin_month", Args: arg(map[string]int64{"month": 2})},
		{Tick: 2, Op: "post_wages", Args: arg(map[string]int64{"total": 11_000_000})},
		{Tick: 2, Op: "settle_construction", Args: arg(map[string]int64{"cost": 3_000_000})},
		{Tick: 2, Op: "sample"},
	}}
}

// TestFinanceDomain_DeterministicTrajectory proves GR#21 for the
// adapter itself: running the SAME Journal against two freshly
// constructed FinanceAPI instances (via two separate FinanceDomain.Run
// calls) produces a byte-for-byte (reflect.DeepEqual) identical
// Trajectory both times.
func TestFinanceDomain_DeterministicTrajectory(t *testing.T) {
	journal := sampleFinanceJournal(t)
	domain := FinanceDomain{}

	first, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 sampled ticks, got %d: %+v", len(first), first)
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

// TestFinanceDomain_MatchingFixture_Passes proves the harness end to
// end on the finance domain: a fixture saved from a live FinanceDomain
// run compares as a pass against a fresh reference run of the same
// journal, under the domain's own Contract.
func TestFinanceDomain_MatchingFixture_Passes(t *testing.T) {
	journal := sampleFinanceJournal(t)
	domain := FinanceDomain{}

	ref, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "finance-fixture.json")
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

// TestFinanceDomain_DivergentFixture_Fails proves the A/B determinism
// gate has teeth on the finance domain specifically: a fixture with one
// deliberately mutated value (as if a candidate/TS-side implementation
// diverged) fails Compare under the finance Contract's TierExact bars,
// naming the mutated field.
func TestFinanceDomain_DivergentFixture_Fails(t *testing.T) {
	journal := sampleFinanceJournal(t)
	domain := FinanceDomain{}

	ref, err := domain.Run(journal)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ref) < 2 {
		t.Fatalf("expected at least 2 samples, got %d", len(ref))
	}

	divergent := make(Trajectory, len(ref))
	copy(divergent, ref)
	// Deep-copy the mutated sample's Values map so we don't also mutate ref.
	mutatedValues := make(map[string]int64, len(ref[1].Values))
	for k, v := range ref[1].Values {
		mutatedValues[k] = v
	}
	mutatedValues["netWorth"] = mutatedValues["netWorth"] + 1 // one micropound off
	divergent[1] = Sample{Tick: ref[1].Tick, Values: mutatedValues}

	dir := t.TempDir()
	path := filepath.Join(dir, "finance-fixture-divergent.json")
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
		if d.Field == "netWorth" && d.Tick == ref[1].Tick {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a diff naming netWorth at tick %d, got: %v", ref[1].Tick, report.Diffs)
	}
}

// TestFinanceDomain_UnknownOp_FailsClosed proves a malformed/unknown
// journal op fails loudly (MET-H503) rather than being silently
// skipped.
func TestFinanceDomain_UnknownOp_FailsClosed(t *testing.T) {
	domain := FinanceDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 1, Op: "not_a_real_op"},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for an unrecognised journal op, got nil")
	}
}

// TestFinanceDomain_MalformedArgs_FailsClosed proves malformed op args
// (wrong JSON shape) fail loudly too, distinct from an unknown op name.
func TestFinanceDomain_MalformedArgs_FailsClosed(t *testing.T) {
	domain := FinanceDomain{}
	journal := Journal{Entries: []JournalEntry{
		{Tick: 1, Op: "post_wages", Args: json.RawMessage(`{"total": "not-a-number"}`)},
	}}
	_, err := domain.Run(journal)
	if err == nil {
		t.Fatal("expected an error for malformed op args, got nil")
	}
}

// TestFinanceDomain_ContractCoversEverySampledField proves the domain's
// own Contract never leaves a field it reports uncovered — otherwise
// Compare's fail-closed codeUnknownTolerance path would fire on every
// single real run of this domain, which would make the gate useless
// rather than strict.
func TestFinanceDomain_ContractCoversEverySampledField(t *testing.T) {
	journal := sampleFinanceJournal(t)
	domain := FinanceDomain{}
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
