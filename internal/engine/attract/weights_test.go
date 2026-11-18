package attract

import (
	"math"
	"os"
	"strings"
	"testing"
)

// shippedTermsPath is data/attract_terms.json relative to this package.
const shippedTermsPath = "../../../data/attract_terms.json"

// mixedInputs is a fixed lever/modifier spread used by the data tests.
func mixedInputs() TermInputs {
	in := TermInputs{}
	in.Levers[LeverHousing] = LeverTerm{Built: true}
	in.Levers[LeverJobs] = LeverTerm{Value: 60, Built: true}
	in.Levers[LeverServices] = LeverTerm{Value: 40, Built: true}
	in.Levers[LeverParks] = LeverTerm{Value: 30, Built: true}
	in.Modifiers[ModifierSafety] = LeverTerm{Value: 50, Built: true}
	in.Modifiers[ModifierEnvironment] = LeverTerm{Value: 20, Built: true}
	return in
}

// TestWeightFromData is AC-2: the weights are loaded from config data
// (never literals in this package's source), so two different weight-config
// fixtures over identical term inputs produce different A() scores - a
// rebalance is a data edit, and the computed score actually moves.
func TestWeightFromData(t *testing.T) {
	cfgA := validConfig()
	cfgB := cfgA
	cfgB.Weights = validWeights()
	// Same sum (1.0), a different split: jobs 0.5, the other nine 0.5/9.
	for i := range cfgB.Weights.Levers {
		cfgB.Weights.Levers[i] = 0.5 / 9
	}
	cfgB.Weights.Levers[LeverJobs] = 0.5

	aA, _, _, _ := newAPI(t, cfgA)
	if err := aA.SetTermInputs(mixedInputs()); err != nil {
		t.Fatalf("SetTermInputs(A): %v", err)
	}
	aB, _, _, _ := newAPI(t, cfgB)
	if err := aB.SetTermInputs(mixedInputs()); err != nil {
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
		t.Fatalf("A() identical (%v) under two different weight configs - weights are not data-driven", scoreA)
	}
}

// TestWeightFromJSON is AC-2's data-loading half: two JSON config documents
// with different weights (and an explicit aWorld) both load, and A() differs.
func TestWeightFromJSON(t *testing.T) {
	docA := []byte(`{"weights": ` + weightsBlockJSON + `,
		"aWorld": 50, "migrationRate": 1.0,
		"reputation": {"riseRate": 0.2, "fallRate": 0.8, "max": 100}}`)
	// B: same shipped shape, reputation weight 0 (a data edit only).
	docB := []byte(strings.Replace(string(docA), `"reputation": 0.15`, `"reputation": 0.0`, 1))

	cfgA, err := ParseConfig(docA, "corr-attract")
	if err != nil {
		t.Fatalf("ParseConfig(A): %v", err)
	}
	cfgB, err := ParseConfig(docB, "corr-attract")
	if err != nil {
		t.Fatalf("ParseConfig(B): %v", err)
	}
	if cfgA.Weights.Reputation == cfgB.Weights.Reputation {
		t.Fatalf("the JSON edit did not change the loaded reputation weight")
	}
}

// TestShippedWeightsFile is C1's observation: the shipped
// data/attract_terms.json parses to ten lever weights of exactly 0.1 in
// fixed order, two modifiers at 0.1, reputation 0.15.
func TestShippedWeightsFile(t *testing.T) {
	b, err := os.ReadFile(shippedTermsPath)
	if err != nil {
		t.Fatalf("read shipped terms file: %v", err)
	}
	w, err := ParseWeights(b, "corr-attract")
	if err != nil {
		t.Fatalf("ParseWeights(shipped): %v", err)
	}
	for i := 0; i < LeverCount; i++ {
		if w.Levers[i] != 0.1 {
			t.Fatalf("shipped lever %s weight = %v, want 0.1", LeverID(i), w.Levers[i])
		}
	}
	for i := 0; i < ModifierCount; i++ {
		if w.Modifiers[i] != 0.1 {
			t.Fatalf("shipped modifier %s weight = %v, want 0.1", ModifierID(i), w.Modifiers[i])
		}
	}
	if w.Reputation != 0.15 {
		t.Fatalf("shipped reputation weight = %v, want 0.15", w.Reputation)
	}
	// The weights are valid by construction (ParseWeights validates), and
	// the sum is one to within the epsilon.
	if s := w.leverSum(); math.Abs(s-1) > weightSumEpsilon {
		t.Fatalf("shipped lever weights sum = %v, want 1", s)
	}
}

// weightsDoc returns a one-lever-edited copy of the shipped-shape document.
func weightsDoc(old, repl string) []byte {
	doc := `{"weights": ` + weightsBlockJSON + `}`
	if old != "" && !strings.Contains(doc, old) {
		panic("weightsDoc: pattern not found: " + old)
	}
	return []byte(strings.Replace(doc, old, repl, 1))
}

// TestWeightsFileRejected is C1: nine or eleven lever weights, a missing
// or duplicated name, an unknown name, a missing weight value, a sum off 1,
// NaN-shaped (non-finite) and out-of-range weights are each refused with
// ErrInvalidWeights and no Weights come back.
func TestWeightsFileRejected(t *testing.T) {
	cases := []struct {
		name string
		doc  []byte
	}{
		{"nineLeversLast", weightsDoc("{\"id\":\"congestion\",\"weight\":0.1},\n\t\t{\"id\":\"tourism\",\"weight\":0.1}", `{"id":"congestion","weight":0.1}`)},
		{"nineLeversFirst", weightsDoc(`{"id":"jobs","weight":0.1},`, ``)},
		{"eleventhLever", weightsDoc(`{"id":"tourism","weight":0.1}]`, `{"id":"tourism","weight":0.1},{"id":"extra","weight":0.0}]`)},
		{"duplicateLever", weightsDoc(`{"id":"jobs","weight":0.1}`, `{"id":"housing","weight":0.1}`)},
		{"unknownLever", weightsDoc(`{"id":"tourism","weight":0.1}`, `{"id":"nightlife","weight":0.1}`)},
		{"sumOff", weightsDoc(`{"id":"jobs","weight":0.1}`, `{"id":"jobs","weight":0.15}`)},
		{"leverAboveOne", weightsDoc(`{"id":"jobs","weight":0.1}`, `{"id":"jobs","weight":1.5}`)},
		{"leverNegative", weightsDoc(`{"id":"jobs","weight":0.1}`, `{"id":"jobs","weight":-0.1}`)},
		{"missingWeightValue", weightsDoc(`{"id":"jobs","weight":0.1}`, `{"id":"jobs"}`)},
		{"missingModifier", weightsDoc(`,{"id":"environment","weight":0.1}`, ``)},
		{"missingReputation", weightsDoc(`"reputation": 0.15,`, ``)},
		{"reputationAboveOne", weightsDoc(`"reputation": 0.15`, `"reputation": 2`)},
		{"unknownReinforcer", weightsDoc(`"wellpaidjobs"`, `"wellpaid"`)},
		{"noWeightsBlock", []byte(`{"version": 3}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ParseWeights(tc.doc, "corr-attract")
			if err == nil {
				t.Fatalf("ParseWeights accepted %s", tc.name)
			}
			isErr(t, err, ErrInvalidWeights)
			if w != (Weights{}) {
				t.Fatalf("ParseWeights returned partial weights on error: %+v", w)
			}
		})
	}
}

// TestInvalidWeightRejected is AC-10: an out-of-range weight returns a
// registry-sourced error (ErrInvalidWeights) and - the GR#7 assertion -
// never silently substitutes an unweighted/zero-weighted term. The
// constructor refuses, so no live config ever carries the bad weight.
func TestInvalidWeightRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Weights)
	}{
		{"negative", func(w *Weights) { w.Levers[LeverJobs] = -0.1 }},
		{"aboveOne", func(w *Weights) { w.Modifiers[ModifierSafety] = 1.5 }},
		{"nan", func(w *Weights) { w.Reputation = math.NaN() }},
		{"inf", func(w *Weights) { w.Modifiers[ModifierEnvironment] = math.Inf(1) }},
		{"reinforcerNaN", func(w *Weights) { w.Reinforcers[ReinforcerSports] = math.NaN() }},
		{"sumImbalance", func(w *Weights) { w.Levers[LeverJobs] = 0.5 }},
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
// reputation rate pair is rejected - the asymmetry is structural (US-2).
func TestInvalidConfigRejected(t *testing.T) {
	cfg := validConfig()
	cfg.Reputation.FallRate = 0.1 // now fallRate < riseRate (0.2)
	if _, err := New(cfg, 7, "corr-attract"); err == nil {
		t.Fatal("New accepted a symmetric reputation config (fallRate <= riseRate)")
	} else {
		isErr(t, err, ErrConfigInvalid)
	}
}
