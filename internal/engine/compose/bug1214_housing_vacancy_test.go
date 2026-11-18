package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-1214 / FEAT-083 leg 4: building dwellings changes migration against a
// same-seed control on the default composition. Directional, no exact pins.

func bug1214Run(t *testing.T, dwellings, months int) []int {
	t.Helper()
	r := ev83New(t)
	if dwellings > 0 {
		r.buildDwellings(dwellings)
	}
	pops := make([]int, 0, months)
	for m := 1; m <= months; m++ {
		r.advance(30)
		pops = append(pops, r.comp.Population())
	}
	return pops
}

func TestBUG1214_BuildingDwellingsRaisesMigration(t *testing.T) {
	control := bug1214Run(t, 0, 12)
	treat := bug1214Run(t, 6, 12)
	var sb strings.Builder
	fmt.Fprintf(&sb, "month: control vs build-6-dwellings population\n")
	for i := range control {
		fmt.Fprintf(&sb, "%2d: %4d vs %4d\n", i+1, control[i], treat[i])
	}
	t.Log("\n" + sb.String())
	if dir := os.Getenv("METRO_EVIDENCE_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "leg4-housing-response.txt"), []byte(sb.String()), 0o644)
	}
	if treat[5] <= control[5] {
		t.Fatalf("month 6: treatment pop %d must exceed control %d", treat[5], control[5])
	}
	for i := 0; i < 3; i++ {
		if control[i] < 50 {
			t.Fatalf("month %d control pop %d: default city collapsed", i+1, control[i])
		}
	}
}

func TestBUG1214_ZeroSpareHomesAdmitsNobody(t *testing.T) {
	if got := housingVacancyUnits(120, 0, 120); got != 0 {
		t.Fatalf("full city vacancy = %d, want 0", got)
	}
	if got := housingVacancyUnits(120, 0, 500); got != 0 {
		t.Fatalf("over-full city vacancy = %d, want 0", got)
	}
	if a, b := housingVacancyUnits(120, 0, 100), housingVacancyUnits(120, 48, 100); b <= a {
		t.Fatalf("building must raise vacancy: %d vs %d", a, b)
	}
}

func TestBUG1214_VacancyZeroSignalSafe(t *testing.T) {
	if got := housingVacancyUnits(-5, -5, -5); got != 0 {
		t.Fatalf("negative inputs vacancy = %d, want 0", got)
	}
	if got := housingVacancyUnits(0, 0, 0); got != 0 {
		t.Fatalf("empty vacancy = %d, want 0", got)
	}
}
