package data

import "testing"

// BUG-280 regressions: Consumption.Validate and Seasonal.Validate used to
// range over their maps directly, so a malformed file with 2+ invalid
// entries reported a different first violation on every run (GR#21 - would
// trip the red determinism gate). Both now iterate sorted keys; these tests
// pin the stable first-violation order the BUG-098-class fix guarantees.

func TestLoadConsumption_MultipleViolationsDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {
			"school": {"unit": "", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2},
			"clinic": {"unit": "bed", "waterL": -1, "elecKWh": 2.0, "gasKWh": 1.0, "wasteKg": 0.3}
		}
	}`)
	// Both classes violate ("clinic" negative waterL, "school" empty unit);
	// the reported first violation must always be clinic (sorted order),
	// never school, regardless of map iteration order.
	for i := 0; i < 5; i++ {
		_, err := LoadConsumption(dir, testCorrelationID())
		assertPlaceholderCode(t, err, CodeSchemaInvalid, "classes[clinic].waterL")
	}
}

func TestLoadSeasonal_MultipleViolationsDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1,-1,1,1,1,1,1]},
			"electricityWinter": {"multipliers": [2,2,1,1,1,1,1,1,1,1,2,2]}
		}
	}`)
	// waterSummerPeak has a negative multiplier AND electricityWinter has a
	// wrong curve length; sorted iteration must blame waterSummerPeak first
	// every run.
	for i := 0; i < 5; i++ {
		_, err := LoadSeasonal(dir, testCorrelationID())
		assertPlaceholderCode(t, err, CodeSchemaInvalid, "curves[waterSummerPeak].multipliers")
	}
}
