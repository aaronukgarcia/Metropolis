package leisure

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestLifeStageFor_OffMapBucketsAsEmployed is the coordinator-requested
// regression for the FEAT-198 gap: citizens.EmploymentOffMap (=5, "a real
// job, just outside the map") had no case in lifeStageFor's switch, so it
// fell through to the post-switch "EmploymentNone" age check and wrongly
// bucketed an off-map-employed adult as StageUnemployed — the same bucket
// as someone who has never worked at all. An off-map worker must get
// exactly the same life stage (and therefore the same weekly-budget
// hours) as an on-map EmploymentEmployed citizen of the same age.
//
// PROOF THIS CAN FAIL: temporarily removing the `citizens.EmploymentOffMap`
// arm from lifeStageFor's EmploymentEmployed case (reverting to the
// pre-fix switch, which had no EmploymentOffMap case at all) makes the
// off-map citizen fall through to the child/adult age check and return
// StageUnemployed instead of StageEmployed — verified by hand via a scratch
// copy of types.go (edit, run this test, confirm it fails, restore from the
// copy — never via git), then reverted.
func TestLifeStageFor_OffMapBucketsAsEmployed(t *testing.T) {
	const adultBirthMonth = 0
	const currentMonth = 40 * 12 // well past 18 years old either way

	employed := citizens.Citizen{
		BirthMonth: adultBirthMonth,
		Month:      currentMonth,
		Employment: citizens.Employment{State: citizens.EmploymentEmployed},
	}
	offMap := citizens.Citizen{
		BirthMonth: adultBirthMonth,
		Month:      currentMonth,
		Employment: citizens.Employment{State: citizens.EmploymentOffMap},
	}

	gotEmployed := lifeStageFor(employed)
	gotOffMap := lifeStageFor(offMap)

	if gotEmployed != StageEmployed {
		t.Fatalf("lifeStageFor(EmploymentEmployed) = %v, want StageEmployed (test's own baseline is wrong)", gotEmployed)
	}
	if gotOffMap != gotEmployed {
		t.Fatalf("lifeStageFor(EmploymentOffMap) = %v, want %v (the same stage as an on-map employed adult of the same age) — an off-map worker has a real job, it's just outside the map (FEAT-198)", gotOffMap, gotEmployed)
	}
	if gotOffMap == StageUnemployed {
		t.Fatalf("lifeStageFor(EmploymentOffMap) = StageUnemployed — the off-map-employed adult fell through to the EmploymentNone age-check branch (the exact FEAT-198 regression)")
	}
}

// TestLifeStageFor_OffMapDiffersFromNeverWorkedAdult double-checks the
// contrast case explicitly: an adult who has genuinely never worked
// (EmploymentNone) still buckets as StageUnemployed (unchanged behaviour) —
// proving the fix did not accidentally widen StageEmployed to cover
// EmploymentNone too.
func TestLifeStageFor_OffMapDiffersFromNeverWorkedAdult(t *testing.T) {
	const currentMonth = 40 * 12

	neverWorked := citizens.Citizen{
		BirthMonth: 0,
		Month:      currentMonth,
		Employment: citizens.Employment{State: citizens.EmploymentNone},
	}
	offMap := citizens.Citizen{
		BirthMonth: 0,
		Month:      currentMonth,
		Employment: citizens.Employment{State: citizens.EmploymentOffMap},
	}

	if got := lifeStageFor(neverWorked); got != StageUnemployed {
		t.Fatalf("lifeStageFor(EmploymentNone, adult) = %v, want StageUnemployed (unchanged baseline behaviour)", got)
	}
	if got := lifeStageFor(offMap); got == StageUnemployed {
		t.Fatalf("lifeStageFor(EmploymentOffMap) == StageUnemployed, want StageEmployed — off-map must not collapse into the never-worked bucket")
	}
}
