package maintenance

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG263_NearMaxInt64ConfigRejectedAtLoad is the BUG-263 regression: the
// maintenance config validator left EngineerDaysPerYear, LifetimeYears and the
// two cost figures positive-UNBOUNDED, so a near-MaxInt64 authoring value was
// silently accepted and then saturated downstream (the SEC-117 load-time
// saturation shape). Each sub-case builds an otherwise-valid config with one
// field pinned near math.MaxInt64 and asserts New/validate now REJECTS it with
// the MET-G3200 registry error. Before the fix New accepted every case (RED);
// after the fix each is rejected (GREEN). The rejection must be a
// registry-sourced *errs.E (GR#7), never a bare non-nil error.
func TestBUG263_NearMaxInt64ConfigRejectedAtLoad(t *testing.T) {
	base := func() Config {
		return Config{
			Classes: map[Class]ClassConfig{
				"dwelling": {EngineerDaysPerYear: 10, LifetimeYears: 50},
				"shop":     {EngineerDaysPerYear: 8, LifetimeYears: 40},
			},
			CrewCostPerEngineerDay:       100,
			ContractorCostPerEngineerDay: 300,
		}
	}

	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"engineerDaysPerYear near MaxInt64", func(c *Config) {
			cc := c.Classes["dwelling"]
			cc.EngineerDaysPerYear = math.MaxInt64 - 1
			c.Classes["dwelling"] = cc
		}},
		{"engineerDaysPerYear just over the ×2 headroom cap", func(c *Config) {
			cc := c.Classes["dwelling"]
			cc.EngineerDaysPerYear = maxEngineerDaysPerYear + 1
			c.Classes["dwelling"] = cc
		}},
		{"lifetimeYears near MaxInt64", func(c *Config) {
			cc := c.Classes["shop"]
			cc.LifetimeYears = math.MaxInt64 - 1
			c.Classes["shop"] = cc
		}},
		{"lifetimeYears just over the ×monthsPerYear cap", func(c *Config) {
			cc := c.Classes["shop"]
			cc.LifetimeYears = maxLifetimeYears + 1
			c.Classes["shop"] = cc
		}},
		{"crewCostPerEngineerDay near MaxInt64", func(c *Config) {
			c.CrewCostPerEngineerDay = math.MaxInt64 - 1
		}},
		{"contractorCostPerEngineerDay near MaxInt64", func(c *Config) {
			c.ContractorCostPerEngineerDay = math.MaxInt64 - 1
		}},
		{"cost just over the money-scale cap", func(c *Config) {
			c.CrewCostPerEngineerDay = maxCostPerEngineerDay + 1
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(&c)
			if _, err := New(c, "test"); err == nil {
				t.Fatalf("New accepted a near-MaxInt64 config (silent load-time saturation, SEC-117 shape); want rejection")
			} else {
				wantCode(t, err, ErrMaintenanceDataInvalid)
			}
		})
	}
}

// TestBUG263_BoundaryValuesAccepted proves the caps admit the largest safe
// value (a cap that rejected everything would be a gate that cannot pass): the
// exact cap values must load, so the bound is a real ceiling, not an
// off-by-one that also blocks legitimate data.
func TestBUG263_BoundaryValuesAccepted(t *testing.T) {
	c := Config{
		Classes: map[Class]ClassConfig{
			"dwelling": {EngineerDaysPerYear: maxEngineerDaysPerYear, LifetimeYears: maxLifetimeYears},
			"shop":     {EngineerDaysPerYear: 8, LifetimeYears: 40},
		},
		CrewCostPerEngineerDay:       maxCostPerEngineerDay,
		ContractorCostPerEngineerDay: maxCostPerEngineerDay,
	}
	if _, err := New(c, "test"); err != nil {
		t.Fatalf("New rejected a config at the exact caps: %v", err)
	}
	// Cross-check the money-scale cap is the documented derivation.
	if maxCostPerEngineerDay != math.MaxInt64/int64(det.MicropoundsPerPound) {
		t.Fatalf("maxCostPerEngineerDay drifted from its money-scale derivation")
	}
}

// TestBUG263_FirstErrorDeterministic proves validate reports a STABLE
// first-error field when several classes are out of bounds — the BUG-098/280
// class: iterating a map for the first error makes the reported field depend on
// Go's randomised map order. With sorted-key iteration the first offending
// class ("a_first" sorts before "m_mid" and "z_last") is always the one
// reported.
//
// Falsifiability (r1 REJECT fix): the assertion is on the REPORTED FIELD, read
// from the error's Ctx map (Ctx["field"] names which class was reported), NOT
// on err.Error()/Display() — those render only "[code] msg (correlation: id)"
// and DISCARD the Ctx map, and MET-G3200's message is static/token-free, so
// every offending class yields an identical error string and a map-order revert
// stays green (SEC-233 gate-cannot-fail). Asserting the reported class is
// specifically the sorted-first offender ("a_first") is what a revert to plain
// map-range iteration breaks: a different class then surfaces first across runs,
// turning this test RED. Proven RED by scratch-copy reverting the sort in
// config.go to `for class := range c.Classes`.
func TestBUG263_FirstErrorDeterministic(t *testing.T) {
	build := func() Config {
		return Config{
			Classes: map[Class]ClassConfig{
				"z_last":  {EngineerDaysPerYear: math.MaxInt64 - 1, LifetimeYears: 40},
				"a_first": {EngineerDaysPerYear: math.MaxInt64 - 1, LifetimeYears: 40},
				"m_mid":   {EngineerDaysPerYear: math.MaxInt64 - 1, LifetimeYears: 40},
			},
			CrewCostPerEngineerDay:       100,
			ContractorCostPerEngineerDay: 300,
		}
	}

	// reportedField type-asserts the returned error to the concrete *errs.E and
	// reads the "field" Ctx key that names the offending class. This is the
	// observable determinism signal the r1 attacker required: it depends on
	// WHICH class validate reported first, not just on the rendered string.
	reportedField := func(t *testing.T, err error) string {
		t.Helper()
		if err == nil {
			t.Fatal("expected a rejection")
		}
		e, ok := err.(*errs.E)
		if !ok {
			t.Fatalf("error is %T, want *errs.E (registry-sourced, GR#7)", err)
		}
		field, ok := e.Ctx["field"].(string)
		if !ok {
			t.Fatalf("error Ctx has no string \"field\" key naming the offending class; Ctx=%v", e.Ctx)
		}
		return field
	}

	const wantField = "classes.a_first.engineerDaysPerYear"

	first := reportedField(t, build().validate("test"))
	if first != wantField {
		t.Fatalf("first error names %q, want the sorted-first offender %q", first, wantField)
	}
	for i := 0; i < 200; i++ {
		got := reportedField(t, build().validate("test"))
		if got != wantField {
			t.Fatalf("run %d reported %q, want the deterministic sorted-first offender %q (map-order leak)", i, got, wantField)
		}
	}
}
