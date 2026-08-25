package registry

import (
	"testing"
)

// TestRegression_Register_RejectsInvalidStatus proves Register refuses a
// misspelled/unknown Status value (MET-F108) rather than silently
// persisting a fourth, unrecognised status alongside StatusReal/
// StatusStub/StatusOff.
func TestRegression_Register_RejectsInvalidStatus(t *testing.T) {
	r := NewRegistry()
	err := r.Register("engine.bogus", nil, newStub("engine.bogus"), WithStatus(Status("not-a-real-status")))
	if err == nil {
		t.Fatal("Register with an invalid Status: want error, got nil")
	}
	if _, ok := r.Get("engine.bogus"); ok {
		t.Fatal("Register with an invalid Status: entry was persisted despite the rejection")
	}
}

// TestRegression_SetStatus_RejectsInvalidTarget proves SetStatus refuses
// to toggle a module to an unknown Status value.
func TestRegression_SetStatus_RejectsInvalidTarget(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("engine.togglebogus", newReal("engine.togglebogus"), newStub("engine.togglebogus"),
		WithStatus(StatusStub), WithCanToggle(true)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := r.SetStatus("engine.togglebogus", Status("not-a-real-status"), "engine.togglebogus")
	if err == nil {
		t.Fatal("SetStatus to an invalid target: want error, got nil")
	}
	entry, ok := r.Get("engine.togglebogus")
	if !ok {
		t.Fatal("Get: expected entry to still exist")
	}
	if entry.Status != StatusStub {
		t.Fatalf("Status = %q after a rejected SetStatus, want it unchanged at %q", entry.Status, StatusStub)
	}
}
