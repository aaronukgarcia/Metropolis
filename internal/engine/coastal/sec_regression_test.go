package coastal

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// rawTestConfig returns a rawCoastalData that passes buildConfig's validation
// (the same magnitudes as testConfig), so a single field can be mutated to
// drive a specific rejection through the data-file path (buildConfig).
func rawTestConfig() rawCoastalData {
	return rawCoastalData{
		Version: 1,
		Frequency: rawFrequency{
			BasePerMonth:         1.0,
			MaxBoatSize:          1,
			MaxArrivalsPerMonth:  50,
			EraMultipliers:       []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			SeasonMultipliers:    []float64{1, 1, 1, 1},
			WorldConditionsScale: 0,
		},
		Rescue: rawRescue{CoastguardServiceID: "coastguard", LifeboatServiceID: "lifeboat"},
		Reception: rawReception{
			CaseworkerThroughputPerMonth: 10,
			HotelCostPerCase:             1000,
			SatisfactionFrictionPerCase:  0.1,
		},
		Pipeline: rawPipeline{
			MinMonths:            1,
			MaxMonths:            1,
			GrantRate:            0.5,
			DepartureCostPerCase: 500,
			MaxReductionMonths:   0,
		},
		Policy: rawPolicy{
			ProcessingFundingDefault:                 0.5,
			ProcessingFundingThroughputGainPerUnit:   1.0,
			ProcessingFundingOpexPerUnitPerMonth:     1000,
			HousingApproachDefault:                   0.5,
			HousingApproachCostPerUnitPerMonth:       -100,
			HousingApproachFrictionIncreasePerUnit:   0.5,
			HousingApproachIntegrationPenaltyPerUnit: 0.3,
			IntegrationInvestmentDefault:             0.5,
			IntegrationInvestmentGainPerUnit:         0.6,
			IntegrationInvestmentOpexPerUnitPerMonth: 1000,
		},
		WorldProfile: rawWorldProfile{Skills: rawSkills{AttainmentMean: 30, AttainmentSpread: 0}},
	}
}

// TestSEC210RejectsUnboundedFrequencyCaps (SEC-210): an unbounded MaxBoatSize
// or MaxArrivalsPerMonth — the MaxInt64 inputs that let Advance's totalSize
// accumulation wrap negative into a makeslice panic — must be rejected by
// Validate (the New entry point) and by buildConfig (the data-file path),
// never silently accepted.
func TestSEC210RejectsUnboundedFrequencyCaps(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"maxBoatSize MaxInt64", func(c *Config) { c.MaxBoatSize = math.MaxInt64 }},
		{"maxArrivalsPerMonth MaxInt64", func(c *Config) { c.MaxArrivalsPerMonth = math.MaxInt64 }},
		{"maxBoatSize above ceiling", func(c *Config) { c.MaxBoatSize = maxFrequencyCap + 1 }},
		{"maxArrivalsPerMonth above ceiling", func(c *Config) { c.MaxArrivalsPerMonth = maxFrequencyCap + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted an unbounded frequency cap")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
			if _, err := New(42, cfg, "corr-sec210"); err == nil {
				t.Fatalf("New accepted an unbounded frequency cap")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}

	rawMut := map[string]func(r *rawCoastalData){
		"buildConfig maxBoatSize MaxInt64":         func(r *rawCoastalData) { r.Frequency.MaxBoatSize = math.MaxInt64 },
		"buildConfig maxArrivalsPerMonth MaxInt64": func(r *rawCoastalData) { r.Frequency.MaxArrivalsPerMonth = math.MaxInt64 },
	}
	for name, mutate := range rawMut {
		t.Run(name, func(t *testing.T) {
			raw := rawTestConfig()
			mutate(&raw)
			if _, err := buildConfig(raw, "corr", "corr-sec210"); err == nil {
				t.Fatalf("buildConfig accepted an unbounded frequency cap")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}
}

// TestSEC210ArrivalSizeAccumulationSaturates (SEC-210): the arrival-size
// accumulation saturates at math.MaxInt64 rather than wrapping negative — a
// wrapped-negative total handed to make([]Case, 0, total) is the makeslice
// panic. The raw `+=` this replaces wrapped exactly like this.
func TestSEC210ArrivalSizeAccumulationSaturates(t *testing.T) {
	events := []ArrivalEvent{
		{Size: math.MaxInt64},
		{Size: math.MaxInt64},
	}
	total := satArrivalSize(events)
	if total < 0 {
		t.Fatalf("saturated arrival-size total wrapped negative: %d", total)
	}
	if total != math.MaxInt64 {
		t.Fatalf("saturated arrival-size total = %d, want math.MaxInt64", total)
	}
	// A single in-range size sums exactly — no spurious saturation.
	if got := satArrivalSize([]ArrivalEvent{{Size: 3}, {Size: 4}}); got != 7 {
		t.Fatalf("exact arrival-size total = %d, want 7", got)
	}
}

// TestSEC211RejectsPositiveHousingApproachCost (SEC-211): a positive
// HousingApproachCostPerUnitPerMonth inverts the documented centres-cheaper
// trade-off (hotel cost would rise with centres) and must be rejected by
// Validate (New) and buildConfig (data file), never accepted.
func TestSEC211RejectsPositiveHousingApproachCost(t *testing.T) {
	cfg := testConfig()
	cfg.Policy.HousingApproachCostPerUnitPerMonth = 1 // positive: centres cost MORE
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate accepted a positive housing-approach cost (inverts centres-cheaper)")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}
	if _, err := New(42, cfg, "corr-sec211"); err == nil {
		t.Fatalf("New accepted a positive housing-approach cost")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}

	raw := rawTestConfig()
	raw.Policy.HousingApproachCostPerUnitPerMonth = 1
	if _, err := buildConfig(raw, "corr", "corr-sec211"); err == nil {
		t.Fatalf("buildConfig accepted a positive housing-approach cost")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}
}

// TestSEC211CentresCheaperTradeOffPreserved (SEC-211): with a non-positive
// (documented) cost coefficient, the effective hotel cost falls as the
// housing approach moves toward concentrated centres — the AC-11 trade-off the
// positive-value inversion destroyed. Zero (the flat boundary) remains valid.
func TestSEC211CentresCheaperTradeOffPreserved(t *testing.T) {
	cfg := testConfig()
	cfg.Reception.HotelCostPerCase = 1000
	cfg.Policy.HousingApproachCostPerUnitPerMonth = -400 // centres are cheaper

	dispersal := effectiveHotelCost(cfg, 0.0)
	centres := effectiveHotelCost(cfg, 1.0)
	if centres >= dispersal {
		t.Fatalf("centres-cheaper trade-off inverted: dispersal=%d centres=%d", dispersal, centres)
	}

	cfg.Policy.HousingApproachCostPerUnitPerMonth = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero (non-positive) housing-approach cost rejected: %v", err)
	}
}

// TestSEC220RejectsUnboundedCaseAllocation (SEC-220): the per-month case
// allocation is driven by MaxArrivalsPerMonth × MaxBoatSize. Each factor is
// individually capped at maxFrequencyCap, but the product was not, so a
// Validate-passing config {BaseArrivalRate:10000, MaxBoatSize:10000,
// MaxArrivalsPerMonth:10000} would drive Advance's make([]Case, 0, 1e8) — a
// ~5.6 GB single-call backing array (OOM). The product must be bounded at
// maxCasesPerMonth, and BaseArrivalRate must be bounded at maxFrequencyCap.
func TestSEC220RejectsUnboundedCaseAllocation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"finding's 1e8 config (base 10000, factors at cap)", func(c *Config) {
			c.BaseArrivalRate = 10000
			c.MaxBoatSize = maxFrequencyCap
			c.MaxArrivalsPerMonth = maxFrequencyCap
		}},
		{"product just above ceiling", func(c *Config) {
			c.MaxBoatSize = 1001
			c.MaxArrivalsPerMonth = 1000 // 1,001,000 > maxCasesPerMonth
		}},
		{"baseArrivalRate above cap", func(c *Config) { c.BaseArrivalRate = float64(maxFrequencyCap + 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted an allocation-driving config")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
			if _, err := New(42, cfg, "corr-sec220"); err == nil {
				t.Fatalf("New accepted an allocation-driving config")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}

	// The data-file path (buildConfig) rejects the 1e8-case product too.
	raw := rawTestConfig()
	raw.Frequency.MaxBoatSize = maxFrequencyCap
	raw.Frequency.MaxArrivalsPerMonth = maxFrequencyCap
	if _, err := buildConfig(raw, "corr", "corr-sec220"); err == nil {
		t.Fatalf("buildConfig accepted a config driving a 1e8-case allocation")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}

	// The product bound rejects the driver but accepts a legitimate config:
	// the shipped magnitude (20×50) and the exact ceiling (1000×1000) pass.
	for _, tc := range []struct {
		name  string
		boat  int64
		month int64
	}{
		{"shipped magnitude", 20, 50},
		{"product at ceiling", 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.MaxBoatSize = tc.boat
			cfg.MaxArrivalsPerMonth = tc.month
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate rejected a legitimate config (%dx%d): %v", tc.boat, tc.month, err)
			}
		})
	}
}

// TestSEC221RejectsUnboundedFrictionPerCase (SEC-221): a
// SatisfactionFrictionPerCase of 1e308 is finite ≥ 0 and passed the old check,
// yet drives the cumulative friction to +Inf. The magnitude must be bounded at
// maxSatisfactionFrictionPerCase and rejected at the boundary, never accepted.
func TestSEC221RejectsUnboundedFrictionPerCase(t *testing.T) {
	cfg := testConfig()
	cfg.Reception.SatisfactionFrictionPerCase = 1e308
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate accepted SatisfactionFrictionPerCase = 1e308")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}
	if _, err := New(42, cfg, "corr-sec221"); err == nil {
		t.Fatalf("New accepted SatisfactionFrictionPerCase = 1e308")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}

	raw := rawTestConfig()
	raw.Reception.SatisfactionFrictionPerCase = 1e308
	if _, err := buildConfig(raw, "corr", "corr-sec221"); err == nil {
		t.Fatalf("buildConfig accepted SatisfactionFrictionPerCase = 1e308")
	} else {
		assertRegistryCode(t, err, ErrDataInvalid)
	}
}

// TestSEC221FrictionAccumulationStaysFinite (SEC-221): even a VALIDATED config
// drives a non-finite per-month friction delta through the (unbounded)
// HousingApproachFrictionIncreasePerUnit coefficient, the accumulated friction
// and the per-month result must saturate at the finite maxSatisfactionFriction
// ceiling — never +Inf leaking into SatisfactionFriction() or the AdvanceResult.
func TestSEC221FrictionAccumulationStaysFinite(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 3.0
	cfg.MaxBoatSize = 10
	cfg.Reception.CaseworkerThroughputPerMonth = 1 // overflow guaranteed
	cfg.Reception.SatisfactionFrictionPerCase = maxSatisfactionFrictionPerCase
	cfg.Policy.HousingApproachFrictionIncreasePerUnit = 1e308 // finite >= 0, still valid
	cfg.Policy.HousingApproachDefault = 1.0                   // approach = 1: the coefficient applies

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	res, err := api.Advance(0)
	if err != nil {
		t.Fatalf("Advance(0): %v", err)
	}
	if !num.IsFinite(res.SatisfactionFriction) {
		t.Fatalf("AdvanceResult.SatisfactionFriction leaked a non-finite value: %v", res.SatisfactionFriction)
	}
	if _, err := api.Advance(1); err != nil {
		t.Fatalf("Advance(1): %v", err)
	}
	if got := api.SatisfactionFriction(); !num.IsFinite(got) {
		t.Fatalf("SatisfactionFriction leaked a non-finite value: %v", got)
	} else if got > maxSatisfactionFriction {
		t.Fatalf("SatisfactionFriction exceeded the finite ceiling: %v > %v", got, maxSatisfactionFriction)
	}
}

// TestSEC228RejectsUnboundedFrequencyMultipliers (SEC-228): the era/season
// frequency multipliers and WorldConditionsScale were each validated only
// "finite and >= 0", so {EraMultipliers[0]=1e308, SeasonMultipliers[0]=1e308,
// WorldConditionsScale=1e308} passed Validate while rateForMonth's product
// overflowed to +Inf and arrivalCount collapsed it to zero arrivals. Each must
// now be bounded at maxFrequencyMultiplier and rejected at the boundary.
func TestSEC228RejectsUnboundedFrequencyMultipliers(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"era multiplier 1e308", func(c *Config) { c.EraMultipliers[0] = 1e308 }},
		{"season multiplier 1e308", func(c *Config) { c.SeasonMultipliers[0] = 1e308 }},
		{"worldConditionsScale 1e308", func(c *Config) { c.WorldConditionsScale = 1e308 }},
		{"era multiplier above ceiling", func(c *Config) { c.EraMultipliers[0] = maxFrequencyMultiplier + 1 }},
		{"season multiplier above ceiling", func(c *Config) { c.SeasonMultipliers[0] = maxFrequencyMultiplier + 1 }},
		{"worldConditionsScale above ceiling", func(c *Config) { c.WorldConditionsScale = maxFrequencyMultiplier + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted an unbounded frequency multiplier")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
			if _, err := New(42, cfg, "corr-sec228"); err == nil {
				t.Fatalf("New accepted an unbounded frequency multiplier")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}

	rawMut := map[string]func(r *rawCoastalData){
		"buildConfig era multiplier 1e308":       func(r *rawCoastalData) { r.Frequency.EraMultipliers[0] = 1e308 },
		"buildConfig season multiplier 1e308":    func(r *rawCoastalData) { r.Frequency.SeasonMultipliers[0] = 1e308 },
		"buildConfig worldConditionsScale 1e308": func(r *rawCoastalData) { r.Frequency.WorldConditionsScale = 1e308 },
	}
	for name, mutate := range rawMut {
		t.Run(name, func(t *testing.T) {
			raw := rawTestConfig()
			mutate(&raw)
			if _, err := buildConfig(raw, "corr", "corr-sec228"); err == nil {
				t.Fatalf("buildConfig accepted an unbounded frequency multiplier")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}
}

// TestSEC228ArrivalCountNonFiniteRateSaturatesToCeiling (SEC-228, defence in
// depth): even if a non-finite rate ever reaches arrivalCount, a +Inf rate
// semantically means "every boat arrives" and must saturate at the maxArrivals
// ceiling — never collapse to zero the way the old non-finite-first order did.
// NaN and -Inf still mean "no arrivals".
func TestSEC228ArrivalCountNonFiniteRateSaturatesToCeiling(t *testing.T) {
	stream := det.NewStream(42, 0, 0, "coastal.arrival")
	if got := arrivalCount(stream, math.Inf(1), 50); got != 50 {
		t.Fatalf("arrivalCount(+Inf, 50) = %d, want 50 (the ceiling)", got)
	}
	if got := arrivalCount(stream, math.NaN(), 50); got != 0 {
		t.Fatalf("arrivalCount(NaN, 50) = %d, want 0", got)
	}
	if got := arrivalCount(stream, math.Inf(-1), 50); got != 0 {
		t.Fatalf("arrivalCount(-Inf, 50) = %d, want 0", got)
	}
}

// TestSEC229RejectsUnboundedPipelineMonths (SEC-229): Pipeline.MinMonths and
// MaxMonths were validated only "> 0"/">= minMonths", so Config{MinMonths=1,
// MaxMonths=math.MaxInt64} passed Validate while durationFor returned up to
// MaxInt64 and ResolveMonth = month + dm wrapped negative. The range must now
// be bounded at maxPipelineMonths and rejected at the boundary.
func TestSEC229RejectsUnboundedPipelineMonths(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"maxMonths MaxInt64", func(c *Config) { c.Pipeline.MaxMonths = math.MaxInt64 }},
		{"maxMonths above ceiling", func(c *Config) { c.Pipeline.MaxMonths = maxPipelineMonths + 1 }},
		{"minMonths above ceiling", func(c *Config) {
			c.Pipeline.MinMonths = maxPipelineMonths + 1
			c.Pipeline.MaxMonths = maxPipelineMonths + 1
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted an unbounded pipeline duration")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
			if _, err := New(42, cfg, "corr-sec229"); err == nil {
				t.Fatalf("New accepted an unbounded pipeline duration")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}

	rawMut := map[string]func(r *rawCoastalData){
		"buildConfig maxMonths MaxInt64": func(r *rawCoastalData) { r.Pipeline.MaxMonths = math.MaxInt64 },
	}
	for name, mutate := range rawMut {
		t.Run(name, func(t *testing.T) {
			raw := rawTestConfig()
			mutate(&raw)
			if _, err := buildConfig(raw, "corr", "corr-sec229"); err == nil {
				t.Fatalf("buildConfig accepted an unbounded pipeline duration")
			} else {
				assertRegistryCode(t, err, ErrDataInvalid)
			}
		})
	}
}

// TestSEC229ResolveMonthSaturates (SEC-229, defence in depth): even a VALIDATED
// config advanced at a month near math.MaxInt64 must not let ResolveMonth =
// month + dm wrap negative — a wrapped-negative resolve month reads as
// "immediately due" and grants the case on the very next Advance. The addition
// saturates at math.MaxInt64 instead.
func TestSEC229ResolveMonthSaturates(t *testing.T) {
	cfg := testConfig()
	cfg.Pipeline.MinMonths = maxPipelineMonths
	cfg.Pipeline.MaxMonths = maxPipelineMonths
	cfg.Pipeline.MaxReductionMonths = 0

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	res, err := api.Advance(math.MaxInt64)
	if err != nil {
		t.Fatalf("Advance(math.MaxInt64): %v", err)
	}
	if res.NewCases == 0 {
		t.Fatalf("Advance(math.MaxInt64) minted no cases; cannot observe ResolveMonth")
	}

	api.mu.RLock()
	defer api.mu.RUnlock()
	for _, id := range api.caseOrder {
		k := api.cases[id]
		if k.ResolveMonth != 0 && k.ResolveMonth < 0 {
			t.Fatalf("ResolveMonth wrapped negative: case %d resolveMonth=%d", uint64(id), k.ResolveMonth)
		}
	}
}
