package education

import (
	"fmt"
	"testing"
)

// AC-14: stage transitions, attainment computation, and personality drift
// are deterministic functions of (worldSeed, citizen id, month, realised
// funding-quality) — identical results across repeated runs.
func TestDeterminism(t *testing.T) {
	f1 := runDeterminismScenario(t, 42, testConfig())
	f2 := runDeterminismScenario(t, 42, testConfig())
	if f1 != f2 {
		t.Fatalf("identical seed produced different results:\nrun1=%s\nrun2=%s", f1, f2)
	}
}

// AC-14: the dropout draw is a deterministic function of the counter-based
// hash stream — the same seed drops the same pupils every run.
func TestDeterminismDropout(t *testing.T) {
	cfg := testConfig()
	cfg.DropoutRate = 0.5

	d1 := runDropoutScenario(t, 7, cfg)
	d2 := runDropoutScenario(t, 7, cfg)
	if len(d1) != len(d2) {
		t.Fatalf("dropout sets differ in size: %v vs %v", d1, d2)
	}
	for id := range d1 {
		if !d2[id] {
			t.Fatalf("dropout decision not deterministic: pupil %d differed", id)
		}
	}
}

// runDeterminismScenario drives three citizens through nursery→primary→
// secondary under fixed funding and returns a fingerprint of every pupil's
// stage, attainment and last drift plus the research-point total.
func runDeterminismScenario(t *testing.T, seed uint64, cfg Config) string {
	a, c, _, _ := newWiredAPIWithConfig(t, cfg, seed)
	for id := uint64(1); id <= 3; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 8)
	for id := uint64(1); id <= 3; id++ {
		if err := a.Enrol(id, 8); err != nil {
			t.Fatalf("enrol: %v", err)
		}
	}
	fund := func(s Stage, lvl float64) {
		if err := a.SetStageFunding(FundingCommand{
			Stage: s, Level: lvl, Month: 8, FuseYears: 20,
			Projection: ProjectedConsequence{Description: "funding", Series: []float64{0, 10}},
		}); err != nil {
			t.Fatalf("fund %s: %v", s, err)
		}
	}
	fund(StageNursery, 0.9)
	fund(StagePrimary, 0.1)

	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(20); err != nil {
		t.Fatalf("advance 20: %v", err)
	}
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(32); err != nil {
		t.Fatalf("advance 32: %v", err)
	}

	out := fmt.Sprintf("research=%d;", a.ResearchPoints())
	for id := uint64(1); id <= 3; id++ {
		p, ok := a.Pupil(id)
		if !ok {
			out += "missing;"
			continue
		}
		out += fmt.Sprintf("%d:%s:%d:%d;", id, p.Stage.String(), p.Attainment, p.LastDrift)
	}
	return out
}

// runDropoutScenario enrols four citizens and advances one stage under a
// nonzero dropout rate, returning the set of pupil ids that dropped out.
func runDropoutScenario(t *testing.T, seed uint64, cfg Config) map[uint64]bool {
	a, c, _, _ := newWiredAPIWithConfig(t, cfg, seed)
	for id := uint64(1); id <= 4; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 8)
	for id := uint64(1); id <= 4; id++ {
		if err := a.Enrol(id, 8); err != nil {
			t.Fatalf("enrol: %v", err)
		}
	}
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(20); err != nil {
		t.Fatalf("advance: %v", err)
	}

	dropped := map[uint64]bool{}
	for id := uint64(1); id <= 4; id++ {
		if _, ok := a.Pupil(id); !ok {
			dropped[id] = true
		}
	}
	return dropped
}
