package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-1214: the housing vacancy is derived state (seed homes + built capacity
// - population), recomputed every month. A save/load in the middle of a run
// must not change the population trajectory: run to month 4, save, load into
// a fresh composition (LoadAt), continue to month 8, and every month's
// population equals the uninterrupted run's.
func TestBUG1214_SaveLoadKeepsPopulationTrajectory(t *testing.T) {
	const saveMonth = 4
	const endMonth = 8
	month := int64(core.DailyTicksPerMonth)

	eRef, compRef := newTestEngine(t, b380Seed)
	ref := make([]int, 0, endMonth)
	for m := 1; m <= endMonth; m++ {
		advanceInChunks(t, eRef, month)
		ref = append(ref, compRef.Population())
	}

	eA, compA := newTestEngine(t, b380Seed)
	advanceInChunks(t, eA, saveMonth*month)
	if got := compA.Population(); got != ref[saveMonth-1] {
		t.Fatalf("pre-save month %d population %d != reference %d", saveMonth, got, ref[saveMonth-1])
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}

	eB, compB := newTestEngine(t, b380Seed)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if got := compB.Population(); got != ref[saveMonth-1] {
		t.Fatalf("loaded population %d != saved %d", got, ref[saveMonth-1])
	}
	moved := false
	for m := saveMonth + 1; m <= endMonth; m++ {
		advanceInChunks(t, eB, month)
		got := compB.Population()
		if got != ref[m-1] {
			t.Fatalf("month %d: restored population %d != uninterrupted %d", m, got, ref[m-1])
		}
		if got != ref[saveMonth-1] {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("fixture vacuous: population never moved after the save (%v)", ref)
	}
	t.Logf("population by month (uninterrupted == restored from month %d): %v", saveMonth, ref)
}
