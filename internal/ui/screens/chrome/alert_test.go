package chrome

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// exampleTarget is a convenience for tests that don't care about the
// target's value, only that it is non-empty.
var exampleTarget = drill("f2.finance")

// TestNewAlertRequiresTarget is AC-3/AC-11's construction-path check: an
// Alert cannot be constructed without a non-empty jump target. A lazy build
// that lets Target default to a zero value ("" = "no target registered")
// fails this test, because the constructor is the thing AC-3's discipline
// makes the guarantee at.
func TestNewAlertRequiresTarget(t *testing.T) {
	_, err := NewAlert("a", "text", TierInfo, false, drill(""), protocol.Tick(0))
	if err == nil {
		t.Fatal("NewAlert with empty target returned nil error, want ErrAlertMissingTarget")
	}
	if !errors.Is(err, &errs.E{Code: ErrAlertMissingTarget}) {
		t.Fatalf("NewAlert error = %v, want registry code %s", err, ErrAlertMissingTarget)
	}
}

// TestNewAlertRequiresID is AC-8/AC-12's keying requirement: an alert with
// no identifier cannot be resolved (AC-12) and cannot serve as a crisis
// dedupe key (AC-8), so it is rejected at construction rather than silently
// keyed on the empty string.
func TestNewAlertRequiresID(t *testing.T) {
	_, err := NewAlert("", "text", TierInfo, false, exampleTarget, protocol.Tick(0))
	if err == nil {
		t.Fatal("NewAlert with empty ID returned nil error, want ErrAlertMissingID")
	}
	if !errors.Is(err, &errs.E{Code: ErrAlertMissingID}) {
		t.Fatalf("NewAlert error = %v, want registry code %s", err, ErrAlertMissingID)
	}
}

// TestNewAlertWhitespaceTarget is the same construction-path rejection for a
// target that is only whitespace — "valid" is a non-empty target after
// trimming, not merely a non-zero-length string.
func TestNewAlertWhitespaceTarget(t *testing.T) {
	_, err := NewAlert("a", "text", TierInfo, false, drill("   "), protocol.Tick(0))
	if !errors.Is(err, &errs.E{Code: ErrAlertMissingTarget}) {
		t.Fatalf("NewAlert with whitespace-only target error = %v, want %s", err, ErrAlertMissingTarget)
	}
}

// TestAddAlertRejectsTargetless is AC-11's BUG-100 assertion, stated
// explicitly: a hand-built Alert (bypassing NewAlert, as a white-box caller
// or future refactor might) with an empty target is rejected at the stack
// boundary with the registry code, AND nothing is silently entered onto the
// stack. A build that only validated in NewAlert but trusted AddAlert would
// fail the second half (the targetless alert would be on the stack).
func TestAddAlertRejectsTargetless(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	err := c.AddAlert(Alert{ID: "x", Text: "targetless", Tier: TierCritical, Crisis: true, Tick: protocol.Tick(1)})
	if err == nil {
		t.Fatal("AddAlert of a targetless alert returned nil error, want ErrAlertMissingTarget")
	}
	if !errors.Is(err, &errs.E{Code: ErrAlertMissingTarget}) {
		t.Fatalf("AddAlert error = %v, want registry code %s", err, ErrAlertMissingTarget)
	}

	if got := c.Alerts(); len(got) != 0 {
		t.Fatalf("stack has %d alerts after a rejected targetless add, want 0 (nothing silently entered)", len(got))
	}
}

// TestAddAlertRejectsMissingID is the same stack-boundary gate for the ID —
// a crisis-tagged alert with no ID must not enter the stack, since AC-8's
// dedupe would be keyed on the empty string and every subsequent crisis
// would collide.
func TestAddAlertRejectsMissingID(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	err := c.AddAlert(Alert{Text: "no id", Tier: TierWarning, Crisis: true, Tick: protocol.Tick(1), target: exampleTarget})
	if !errors.Is(err, &errs.E{Code: ErrAlertMissingID}) {
		t.Fatalf("AddAlert error = %v, want registry code %s", err, ErrAlertMissingID)
	}
	if got := c.Alerts(); len(got) != 0 {
		t.Fatalf("stack has %d alerts after a rejected ID-less add, want 0", len(got))
	}
}
