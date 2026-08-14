package attract

import (
	"math"
	"testing"
)

// TestWeightFromData is AC-2: the seven weights are loaded from config data
// (never literals in this package's source), so two different weight-config
// fixtures over identical term inputs produce different A() scores — a
// rebalance is a data edit, and the computed score actually moves.
func TestWeightFromData(t *testing.T) {
	cfgA := validConfig()
	// cfgB weights the same sum (1.0) but a different split.
	cfgB := cfgA
	cfgB.Weights = Weights{
		JobAvailability:      0.5,
		HousingAffordability: 0.1,
		ServiceCoverage:      0.1,
		Environment:          0.1,
		LeisureFit:           0.1,
		Safety:               0.1,
		Reputation:           0.0,
	}

	inputs := TermInputs{
		JobAvailability:        60,
		ServiceCoverage:        40,
		Environment:            20,
		LeisureFit:             30,
		Safety:                 50,
		MonthlyRentMicroPounds: 0,
	}

	aA, _, _, _ := newAPI(t, cfgA)
	if err := aA.SetTermInputs(inputs); err != nil {
		t.Fatalf("SetTermInputs(A): %v", err)
	}
	aB, _, _, _ := newAPI(t, cfgB)
	if err := aB.SetTermInputs(inputs); err != nil {
		t.Fatalf("SetTermInputs(B): %v", err)
	}

	scoreA, err := aA.A()
	if err != nil {
		t.Fatalf("A(A): %v", err)
	}
	scoreB, err := aB.A()
	if err != nil {
		t.Fatalf("A(B): %v", err)
	}
	if scoreA == scoreB {
		t.Fatalf("A() identical (%v) under two different weight configs — weights are not data-driven", scoreA)
	}
}

// TestWeightFromJSON is AC-2's data-loading half: two JSON config documents
// with different weights (and an explicit aWorld) both load, and A() differs.
func TestWeightFromJSON(t *testing.T) {
	docA := []byte(`{
		"weights": {"jobAvailability": 0.2, "housingAffordability": 0.2, "serviceCoverage": 0.15,
		            "environment": 0.1, "leisureFit": 0.1, "safety": 0.1, "reputation": 0.15},
		"aWorld": 50, "migrationRate": 1.0,
		"reputation": {"riseRate": 0.2, "fallRate": 0.8, "max": 100}
	}`)
	docB := []byte(`{
		"weights": {"jobAvailability": 0.5, "housingAffordability": 0.1, "serviceCoverage": 0.1,
		            "environment": 0.1, "leisureFit": 0.1, "safety": 0.1, "reputation": 0.0},
		"aWorld": 50, "migrationRate": 1.0,
		"reputation": {"riseRate": 0.2, "fallRate": 0.8, "max": 100}
	}`)

	cfgA, err := ParseConfig(docA, "corr-attract")
	if err != nil {
		t.Fatalf("ParseConfig(A): %v", err)
	}
	cfgB, err := ParseConfig(docB, "corr-attract")
	if err != nil {
		t.Fatalf("ParseConfig(B): %v", err)
	}

	inputs := TermInputs{JobAvailability: 60, ServiceCoverage: 40, Environment: 20, LeisureFit: 30, Safety: 50}
	aA, _, _, _ := newAPI(t, cfgA)
	_ = aA.SetTermInputs(inputs)
	aB, _, _, _ := newAPI(t, cfgB)
	_ = aB.SetTermInputs(inputs)

	scoreA, _ := aA.A()
	scoreB, _ := aB.A()
	if scoreA == scoreB {
		t.Fatalf("A() identical (%v) across two JSON weight fixtures", scoreA)
	}
}

// TestInvalidWeightRejected is AC-10: an out-of-range weight returns a
// registry-sourced error (ErrInvalidWeights) and — the GR#7 assertion —
// never silently substitutes an unweighted/zero-weighted term. The
// constructor refuses, so no live config ever carries the bad weight.
func TestInvalidWeightRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Weights)
	}{
		{"negative", func(w *Weights) { w.JobAvailability = -0.1 }},
		{"aboveOne", func(w *Weights) { w.Safety = 1.5 }},
		{"nan", func(w *Weights) { w.Reputation = math.NaN() }},
		{"inf", func(w *Weights) { w.Environment = math.Inf(1) }},
		{"sumImbalance", func(w *Weights) { w.JobAvailability = 0.5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg.Weights)
			_, err := New(cfg, 7, "corr-attract")
			if err == nil {
				t.Fatalf("New accepted an invalid weight config (%s)", tc.name)
			}
			isErr(t, err, ErrInvalidWeights)
		})
	}
}

// TestInvalidConfigRejected is AC-10's sibling: a symmetric (or reversed)
// reputation rate pair is rejected — the asymmetry is structural (US-2).
func TestInvalidConfigRejected(t *testing.T) {
	cfg := validConfig()
	cfg.Reputation.FallRate = 0.1 // now fallRate < riseRate (0.2)
	if _, err := New(cfg, 7, "corr-attract"); err == nil {
		t.Fatal("New accepted a symmetric reputation config (fallRate <= riseRate)")
	} else {
		isErr(t, err, ErrConfigInvalid)
	}
}
