package wellbeing

import (
	"fmt"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file pins mental.commuteMinutesClampMax's OPTIONAL-but-validated
// contract, which nothing else in the tree covered (round-5 finding).
//
// The field is consumed only by the webconsole (see MentalFile's doc
// comment); this package declares it so the shared data/wellbeing.json
// round-trips. It is therefore optional here — the same zero-value
// tolerance the three webconsole-only weights already have (BUG-910
// symmetry) — because every fixture predating FEAT-2326609798 omits it
// (engine/social's caseload fixture is the one that went red when the
// rule was hard "strictly positive"). But a value that IS written must
// still be a usable cap: finite and strictly positive.
//
// Both directions are pinned because either half can rot silently:
// tightening the rule back to mandatory reds unrelated packages, and
// dropping the rule lets a hand-edited -1 or NaN reach the webconsole,
// whose own loader would then be the only thing between a broken cap and
// a NaN commute penalty.

// TestCommuteMinutesClampMaxOptionalWhenAbsent proves the zero value (the
// JSON field omitted entirely) is ACCEPTED — the regression that broke
// engine/social's caseload fixture.
func TestCommuteMinutesClampMaxOptionalWhenAbsent(t *testing.T) {
	cfg := testCfg()
	cfg.Mental.CommuteMinutesClampMax = 0 // as if the JSON key were absent
	if _, err := New(cfg, 1, errs.NewCorrelationID()); err != nil {
		t.Fatalf("New rejected a config omitting mental.commuteMinutesClampMax: %v", err)
	}
}

// TestCommuteMinutesClampMaxValidatedWhenPresent proves a written value is
// still checked: negative, NaN and ±Inf are rejected with the
// registry-sourced ErrDataInvalid, while a real cap is accepted.
func TestCommuteMinutesClampMaxValidatedWhenPresent(t *testing.T) {
	rejected := []struct {
		name string
		v    float64
	}{
		{"negative", -1},
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Mental.CommuteMinutesClampMax = tc.v
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err == nil {
				t.Fatalf("New accepted mental.commuteMinutesClampMax = %v", tc.v)
			} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
				t.Errorf("err = %v, want code %s", err, ErrDataInvalid)
			}
		})
	}
	for _, v := range []float64{1440, 0.5} {
		t.Run(fmt.Sprintf("accepts %v", v), func(t *testing.T) {
			cfg := testCfg()
			cfg.Mental.CommuteMinutesClampMax = v
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err != nil {
				t.Fatalf("New rejected mental.commuteMinutesClampMax = %v: %v", v, err)
			}
		})
	}
}

// TestLoadWellbeingClampMaxOptionalOnDisk is the same contract through the
// real JSON loader: a file with the key omitted loads, the same file with
// the key set to -1 is refused.
func TestLoadWellbeingClampMaxOptionalOnDisk(t *testing.T) {
	const tmpl = `{
		"version": 1,
		"baseline": {"physical": 62, "mental": 62},
		"headline": {"physicalWeight": 0.4, "mentalWeight": 0.4, "satisfactionWeight": 0.2},
		"physical": {
			"ageCurve": [{"ageYears": 0, "delta": 0}, {"ageYears": 100, "delta": -35}],
			"healthcareAccessWeight": 15, "dietWeight": 10, "activeTravelWeight": 8,
			"pollutionWeight": 12, "sportParticipationWeight": 10
		},
		"mental": {
			"commuteWeight": 10, "commuteThresholdMinutes": 45,
			"commuteStressAtThreshold": 0.5, "commuteStressAt100Minutes": 2.0,
			"jobAmbitionMismatchWeight": 10, "greenSpaceWeight": 8, "leisureFitWeight": 10,
			"crowdingWeight": 8, "isolationWeight": 10, "noiseWeight": 8,
			"financialStressWeight": 12, "rentBurdenThreshold": 0.35,
			"unemploymentWeight": 10, "unemploymentCapMonths": 60%s
		},
		"modifiers": {"mortalitySlope": 0.01, "productivitySlope": 0.01, "satisfactionSlope": 0.01, "emigrationSlope": 0.01}
	}`

	absent := writeWellbeingFixture(t, fmt.Sprintf(tmpl, ""))
	if _, err := LoadWellbeing(absent, errs.NewCorrelationID()); err != nil {
		t.Fatalf("LoadWellbeing refused a file omitting mental.commuteMinutesClampMax: %v", err)
	}

	negative := writeWellbeingFixture(t, fmt.Sprintf(tmpl, `, "commuteMinutesClampMax": -1`))
	if _, err := LoadWellbeing(negative, errs.NewCorrelationID()); err == nil {
		t.Fatalf("LoadWellbeing accepted mental.commuteMinutesClampMax = -1")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
		t.Errorf("err = %v, want code %s", err, ErrDataInvalid)
	}

	good := writeWellbeingFixture(t, fmt.Sprintf(tmpl, `, "commuteMinutesClampMax": 1440`))
	if _, err := LoadWellbeing(good, errs.NewCorrelationID()); err != nil {
		t.Fatalf("LoadWellbeing refused mental.commuteMinutesClampMax = 1440: %v", err)
	}
}
