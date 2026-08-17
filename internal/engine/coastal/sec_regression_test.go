package coastal

import (
	"math"
	"testing"
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
