package dash_test

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
)

func TestNewDrillTarget_Valid(t *testing.T) {
	d, err := dash.NewDrillTarget("f2.ledger", "line-42")
	if err != nil {
		t.Fatalf("NewDrillTarget: %v", err)
	}
	if d.ViewName != "f2.ledger" || d.EntityID != "line-42" {
		t.Fatalf("got %+v, want f2.ledger / line-42", d)
	}
	if !d.Valid() {
		t.Fatal("Valid() = false, want true")
	}
}

func TestNewDrillTarget_WholeView(t *testing.T) {
	d, err := dash.NewDrillTarget("junction.14.approaches", "")
	if err != nil {
		t.Fatalf("NewDrillTarget: %v", err)
	}
	if d.EntityID != "" {
		t.Fatalf("EntityID = %q, want empty", d.EntityID)
	}
}

func TestNewDrillTarget_RejectsEmptyViewName(t *testing.T) {
	if _, err := dash.NewDrillTarget("", "x"); err == nil {
		t.Fatal("NewDrillTarget(\"\") returned nil error, want rejection")
	}
}

func TestNewDrillTarget_RejectsInvalidGrammar(t *testing.T) {
	for _, bad := range []string{"UPPERCASE", "has space", "trailing.", "no-dots"} {
		if _, err := dash.NewDrillTarget(bad, ""); err == nil {
			t.Fatalf("NewDrillTarget(%q) returned nil error, want rejection", bad)
		}
	}
}

func TestDrillTarget_ZeroValueIsInvalid(t *testing.T) {
	var d dash.DrillTarget
	if d.Valid() {
		t.Fatal("zero DrillTarget reported Valid, want false")
	}
}

// TestNewDrillTarget_RejectsHostileEntityID closes the FEAT-042 round's P3
// finding: NewDrillTarget validated ViewName but not EntityID, so a
// malformed/hostile EntityID (whitespace, a control character, or a
// leading separator the int.protocol EntityID grammar rejects) was
// silently carried through into a DrillTarget instead of being caught at
// construction time. protocol.ValidateEntityID is now wired into
// NewDrillTarget (drill.go) for exactly this reason.
func TestNewDrillTarget_RejectsHostileEntityID(t *testing.T) {
	for _, bad := range []string{
		" leading-space",
		"trailing-space ",
		"has space",
		"\tcontrol-tab",
		"\x00null-byte",
		".leading-dot",
		"-leading-dash",
	} {
		if _, err := dash.NewDrillTarget("f2.ledger", bad); err == nil {
			t.Fatalf("NewDrillTarget(%q) returned nil error, want rejection of the hostile EntityID", bad)
		}
	}
}
