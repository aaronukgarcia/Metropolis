package core

import (
	"errors"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// failingInitScreen wraps a real SimulationScreen but reports Init
// failure, so newScreenWith's error path can be exercised headlessly
// (AC-8: "no compatible terminal" without needing to actually withhold
// one from the test environment).
type failingInitScreen struct {
	tcell.SimulationScreen
}

func (failingInitScreen) Init() error { return errors.New("simulated: no compatible terminal") }

func TestNewScreen_ConstructionFailure_ReturnsRegistryError(t *testing.T) {
	ctor := func() (tcell.Screen, error) {
		return nil, errors.New("simulated construction failure")
	}
	_, err := newScreenWith("corr-1", ctor)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != "MET-U001" {
		t.Fatalf("Code = %q, want MET-U001", e.Code)
	}
	if e.CorrelationID != "corr-1" {
		t.Fatalf("CorrelationID = %q, want corr-1", e.CorrelationID)
	}
}

func TestNewScreen_InitFailure_ReturnsRegistryError_NotPanic(t *testing.T) {
	ctor := func() (tcell.Screen, error) {
		return failingInitScreen{tcell.NewSimulationScreen("")}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("newScreenWith must not panic on Init failure, got panic: %v", r)
		}
	}()

	scr, err := newScreenWith("corr-2", ctor)
	if scr != nil {
		t.Fatalf("expected nil Screen on Init failure, got %v", scr)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != "MET-U001" {
		t.Fatalf("Code = %q, want MET-U001", e.Code)
	}
}

func TestNewScreen_Success(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	sim.SetSize(120, 30)
	ctor := func() (tcell.Screen, error) { return sim, nil }

	scr, err := newScreenWith("corr-3", ctor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scr == nil {
		t.Fatal("expected a non-nil Screen")
	}
	scr.Fini()
}
