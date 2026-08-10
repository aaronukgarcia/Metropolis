package uitest

import (
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestTruncatedFixtureReportsExhausted is AC-3b: a deliberately truncated
// harness.replay fixture — one whose Delta stream ends before the
// scripted sequence's stated expected effects were all observed —
// reports the distinct MET-H101 "fixture exhausted" condition, rather
// than the harness treating "no more deltas" as "scenario complete" and
// silently snapshotting whatever partial state happened to accumulate.
func TestTruncatedFixtureReportsExhausted(t *testing.T) {
	full := buildFixture(t, 5)
	truncated := truncateFixture(full, 2) // keep only the first 2 of 5 recorded Deltas

	h := NewHarness(errs.NewCorrelationID(), nil, countDraw)
	defer h.Stop()

	if err := h.AttachFixture(truncated); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}

	// The script "expects" all 5 deltas' effects (as a real regression
	// test authored against the FULL session would), but the attached
	// fixture only carries 2 — RunScript must report the mismatch, not
	// silently proceed.
	err := h.RunScript("b", 5, 2*time.Second)
	if err == nil {
		t.Fatal("RunScript against a truncated fixture: got nil error, want a distinct fixture-exhausted rejection")
	}
	if !strings.Contains(err.Error(), codeFixtureExhausted) {
		t.Errorf("error %q does not carry %s", err.Error(), codeFixtureExhausted)
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("error %q does not report how many deltas WERE observed before exhaustion (want 2)", err.Error())
	}
}

// TestFullFixtureDoesNotReportExhausted is the control case: an
// UN-truncated fixture, awaited for exactly the number of deltas it
// carries, never reports exhaustion — proving TestTruncatedFixture...
// fails specifically because of truncation, not because AwaitDeltas
// always errors.
func TestFullFixtureDoesNotReportExhausted(t *testing.T) {
	fx := buildFixture(t, 4)
	h := NewHarness(errs.NewCorrelationID(), nil, countDraw)
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}
	if err := h.RunScript("b", 4, 2*time.Second); err != nil {
		t.Fatalf("RunScript against a complete fixture: unexpected error: %v", err)
	}
}
