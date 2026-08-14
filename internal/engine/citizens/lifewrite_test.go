package citizens

import (
	"reflect"
	"testing"
)

// TestLifeWriteIdempotent (AC-10): inspecting the same cold citizen at the
// same month returns byte-identical reconstructed detail — life-writing is
// binding, not a fresh random guess each inspection.
func TestLifeWriteIdempotent(t *testing.T) {
	rec := mkRecord(1234, 7)
	ds := DistrictStats{LeisureRate: 0.6, HealthRate: 0.7, EmploymentRate: 0.8}
	d1 := LifeWrite(5, rec.ID, 40, rec, ds)
	d2 := LifeWrite(5, rec.ID, 40, rec, ds)
	if !reflect.DeepEqual(d1, d2) {
		t.Fatalf("life-writing not idempotent at the same month: %+v vs %+v", d1, d2)
	}
}

// TestLifeWriteVariesByMonth: the reconstruction is keyed to the month, so
// a different month (with overwhelming probability) reconstructs a
// different detail — proving it is a real per-(id, month) stream, not a
// constant.
func TestLifeWriteVariesByMonth(t *testing.T) {
	rec := mkRecord(1234, 7)
	ds := DistrictStats{LeisureRate: 0.6, HealthRate: 0.7, EmploymentRate: 0.8}
	d1 := LifeWrite(5, rec.ID, 40, rec, ds)
	d2 := LifeWrite(5, rec.ID, 41, rec, ds)
	if reflect.DeepEqual(d1, d2) {
		t.Fatal("life-writing did not vary with month — stream is not month-keyed")
	}
}
