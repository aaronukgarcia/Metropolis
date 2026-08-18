package wellbeing

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestJobLevelOffMapScoresAsEmployed (FEAT-198,
// docs/planning/icd/engine.citizens-offmap.md §11 — "REAL SILENT-
// MISCLASSIFICATION RISK"): jobLevel must return the employed-equivalent
// baseline (0.5, the same unknown/off-map-sector value the EmploymentEmployed
// switch's own default/SectorPrimary case uses) for an off-map-employed
// citizen, not the pre-fix default of 0.0.
//
// Proof the pre-fix behaviour really was 0.0 (not merely asserting the
// post-fix number): jobLevel's switch has a `default: return 0.0` arm.
// Before this extension added an explicit `case citizens.EmploymentOffMap`,
// EmploymentOffMap did not exist as a citizens.EmploymentState constant at
// all, so any caller passing the raw value 5 fell through every named case
// straight to that default and scored 0.0 — WORSE than EmploymentUnemployed's
// own 0.1 despite the citizen holding a real job. This test locks in that it
// no longer does.
func TestJobLevelOffMapScoresAsEmployed(t *testing.T) {
	got := jobLevel(citizens.EmploymentOffMap, citizens.SectorNone)
	const want = 0.5
	if got != want {
		t.Fatalf("jobLevel(EmploymentOffMap, SectorNone) = %v, want %v (the employed-equivalent baseline)", got, want)
	}

	// Must not fall through to the generic default's 0.0 — that would be
	// the exact silent-misclassification the ICD flags, and it would also
	// score WORSE than an unemployed citizen's 0.1, which is nonsensical
	// for someone who holds a real (off-map) job.
	unemployedLevel := jobLevel(citizens.EmploymentUnemployed, citizens.SectorNone)
	if got <= unemployedLevel {
		t.Fatalf("jobLevel(EmploymentOffMap) = %v must score ABOVE EmploymentUnemployed's %v — an off-map job is still a real job", got, unemployedLevel)
	}
	if got == 0.0 {
		t.Fatal("jobLevel(EmploymentOffMap) fell through to the generic default's 0.0 — the pre-fix silent-misclassification regressed")
	}
}

// TestJobAmbitionMismatchDeltaOffMapNotWorstCase: an off-map-employed
// citizen with mid-range ambition must not be scored as the worst possible
// mismatch (which is what a jobLevel of 0.0 combined with high ambition
// would produce) — end-to-end sanity check one level up from jobLevel
// itself, using the actual driver consumers call.
func TestJobAmbitionMismatchDeltaOffMapNotWorstCase(t *testing.T) {
	m := MentalFile{JobAmbitionMismatchWeight: 1.0}
	// High ambition (80/100) against the pre-fix 0.0 would produce a gap of
	// 0.8 (near-maximal mismatch). Against the fixed 0.5 baseline the gap
	// is only 0.3.
	delta := jobAmbitionMismatchDelta(m, 80, citizens.EmploymentOffMap, citizens.SectorNone)
	worstCaseDelta := -m.JobAmbitionMismatchWeight * 0.8
	if delta <= worstCaseDelta {
		t.Fatalf("jobAmbitionMismatchDelta(ambition=80, OffMap) = %v, at or below the pre-fix worst-case %v — jobLevel regressed to (near) 0.0", delta, worstCaseDelta)
	}
}
