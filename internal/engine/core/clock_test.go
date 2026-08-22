package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestClock_TwoLayerCadence(t *testing.T) {
	c, err := NewClock(DefaultSecondsPerMonthAt1x)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	for i := 0; i < 65; i++ {
		c.advanceOneDay()
	}
	if got := c.Month(); got != 2 {
		t.Fatalf("Month() after 65 ticks = %d, want 2", got)
	}
	if got := c.DayInMonth(); got != 5 {
		t.Fatalf("DayInMonth() after 65 ticks = %d, want 5", got)
	}
	if got := c.Tick(); got != 65 {
		t.Fatalf("Tick() = %d, want 65", got)
	}
}

func TestClock_MonthRolloverBoundary(t *testing.T) {
	c, err := NewClock(DefaultSecondsPerMonthAt1x)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	var rolledOverAt []int64
	for i := int64(1); i <= 90; i++ {
		if c.advanceOneDay() {
			rolledOverAt = append(rolledOverAt, i)
		}
	}
	want := []int64{30, 60, 90}
	if len(rolledOverAt) != len(want) {
		t.Fatalf("rollovers = %v, want %v", rolledOverAt, want)
	}
	for i, w := range want {
		if rolledOverAt[i] != w {
			t.Fatalf("rollovers = %v, want %v", rolledOverAt, want)
		}
	}
}

func TestClock_TicksPerRealSecond_ScalesWithSpeed(t *testing.T) {
	base, err := NewClock(480)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	base.setPaused(false)

	rates := map[Speed]float64{}
	for _, s := range []Speed{Speed1x, Speed2x, Speed4x, Speed8xDebug} {
		c := base
		c.setSpeed(s)
		rates[s] = c.TicksPerRealSecond()
	}

	if rates[Speed1x] <= 0 {
		t.Fatalf("Speed1x rate = %v, want > 0", rates[Speed1x])
	}
	if got, want := rates[Speed2x], rates[Speed1x]*2; !almostEqual(got, want) {
		t.Errorf("Speed2x rate = %v, want %v (2x Speed1x)", got, want)
	}
	if got, want := rates[Speed4x], rates[Speed1x]*4; !almostEqual(got, want) {
		t.Errorf("Speed4x rate = %v, want %v (4x Speed1x)", got, want)
	}
	if got, want := rates[Speed8xDebug], rates[Speed1x]*8; !almostEqual(got, want) {
		t.Errorf("Speed8xDebug rate = %v, want %v (8x Speed1x)", got, want)
	}
}

func TestClock_TicksPerRealSecond_ZeroWhilePaused(t *testing.T) {
	c, err := NewClock(480) // NewClock starts paused
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	if got := c.TicksPerRealSecond(); got != 0 {
		t.Fatalf("TicksPerRealSecond() while paused = %v, want 0", got)
	}
	if got := c.SecondsPerMonth(); got != 0 {
		t.Fatalf("SecondsPerMonth() while paused = %v, want 0", got)
	}
}

// TestNewClock_RejectsNonPositiveSecondsPerMonthAt1x reproduces BUG-303
// (Bro audit, 2026-08-20): NewClock previously accepted any int64
// unvalidated, so a zero or negative secondsPerMonthAt1x silently produced
// a Clock whose SecondsPerMonth/TicksPerRealSecond queries report garbage
// pacing figures instead of a loud rejection at construction, where the bad
// value actually originated (GR#16 boundary discipline).
func TestNewClock_RejectsNonPositiveSecondsPerMonthAt1x(t *testing.T) {
	for _, bad := range []int64{0, -1, -480} {
		_, err := NewClock(bad)
		if err == nil {
			t.Fatalf("NewClock(%d): got nil error, want ErrInvalidPacingConstant (BUG-303)", bad)
		}
		e, ok := err.(*errs.E)
		if !ok {
			t.Fatalf("NewClock(%d): error type = %T, want *errs.E (registry-sourced, GR#7)", bad, err)
		}
		if e.Code != ErrInvalidPacingConstant {
			t.Fatalf("NewClock(%d): error code = %q, want %q", bad, e.Code, ErrInvalidPacingConstant)
		}
		assertPacingErrorMessageWellFormed(t, e.Msg, bad)
	}
}

// assertPacingErrorMessageWellFormed catches the double-brace registry
// regression the independent round found in this batch's first pass
// (data/errors.json's MET-E020 template used "{{seconds}}" — errs'
// renderTemplate parses single-brace "{key}" placeholders only, so the
// unmatched outer braces rendered as LITERAL "{" / "}" characters in
// every logged/returned message instead of the actual rejected value).
// Fails on any stray brace and requires the real seconds value to appear
// in cleartext.
func assertPacingErrorMessageWellFormed(t *testing.T, msg string, seconds int64) {
	t.Helper()
	if strings.ContainsAny(msg, "{}") {
		t.Fatalf("error message contains an unrendered/malformed template placeholder: %q "+
			"(MET-E020 double-brace regression — data/errors.json must use single-brace {seconds})", msg)
	}
	want := fmt.Sprintf("secondsPerMonthAt1x=%d", seconds)
	if !strings.Contains(msg, want) {
		t.Fatalf("error message = %q, want it to contain %q", msg, want)
	}
}

// TestNewClock_And_WithSecondsPerMonthAt1x_BothPathsRejectAndLog is the
// independent round's promoted Option-path probe (BUG-303 REJECT
// verdict, 2026-08-20): MET-E020 must fire for every non-positive value
// through BOTH the direct NewClock(...) constructor AND the
// WithSecondsPerMonthAt1x(...) Option path, each logging a genuine,
// well-formed (non-double-braced) registry entry recoverable via
// errs.Recent() -- the ring buffer coalesces repeat pushes of the SAME
// code into one slot (ringBuffer.push's doc comment, log.go), so this
// checks errs.Recent() immediately after each individual trigger rather
// than batching triggers first. It also proves WithSecondsPerMonthAt1x's
// failure mode: NewEngine's Engine.clock genuinely keeps whatever it was
// already set to (NewEngine's own valid DefaultSecondsPerMonthAt1x
// construction, since WithSecondsPerMonthAt1x runs strictly after that
// in the Option loop) rather than being replaced by a zero-value Clock
// that would silently report zero pacing everywhere.
func TestNewClock_And_WithSecondsPerMonthAt1x_BothPathsRejectAndLog(t *testing.T) {
	for _, bad := range []int64{0, -480} {
		t.Run(fmt.Sprintf("NewClock(%d)", bad), func(t *testing.T) {
			_, err := NewClock(bad)
			e, ok := err.(*errs.E)
			if !ok || e.Code != ErrInvalidPacingConstant {
				t.Fatalf("NewClock(%d): error = %v, want *errs.E with code %q", bad, err, ErrInvalidPacingConstant)
			}
			assertPacingErrorMessageWellFormed(t, e.Msg, bad)

			found := false
			for _, entry := range errs.Recent() {
				if entry.Code == ErrInvalidPacingConstant {
					found = true
					assertPacingErrorMessageWellFormed(t, entry.Msg, bad)
				}
			}
			if !found {
				t.Fatalf("NewClock(%d): no %s entry found via errs.Recent()", bad, ErrInvalidPacingConstant)
			}
		})

		t.Run(fmt.Sprintf("WithSecondsPerMonthAt1x(%d)", bad), func(t *testing.T) {
			e := NewEngine(WithSecondsPerMonthAt1x(bad))

			// The rejected Option must never replace a working clock with
			// a broken one: NewEngine's own default construction runs
			// BEFORE the Option loop, so a rejected override must leave
			// exactly that default in place.
			if got := e.clock.secondsPerMonthAt1x; got != DefaultSecondsPerMonthAt1x {
				t.Fatalf("WithSecondsPerMonthAt1x(%d): engine's clock.secondsPerMonthAt1x = %d, want the untouched default %d",
					bad, got, DefaultSecondsPerMonthAt1x)
			}

			found := false
			for _, entry := range errs.Recent() {
				if entry.Code == ErrInvalidPacingConstant {
					found = true
					assertPacingErrorMessageWellFormed(t, entry.Msg, bad)
				}
			}
			if !found {
				t.Fatalf("WithSecondsPerMonthAt1x(%d): no %s entry found via errs.Recent() -- the rejection must be logged, never silent (GR#1)", bad, ErrInvalidPacingConstant)
			}
		})
	}
}

// TestNewClock_AcceptsPositiveSecondsPerMonthAt1x is the GREEN
// counterpart: a genuinely valid pacing constant must still construct a
// working Clock, exactly as before BUG-303's fix.
func TestNewClock_AcceptsPositiveSecondsPerMonthAt1x(t *testing.T) {
	c, err := NewClock(480)
	if err != nil {
		t.Fatalf("NewClock(480): unexpected error %v", err)
	}
	c.setPaused(false)
	if got := c.SecondsPerMonth(); got != 480 {
		t.Fatalf("SecondsPerMonth() = %d, want 480", got)
	}
}

func TestValidSpeed(t *testing.T) {
	valid := []Speed{Speed1x, Speed2x, Speed4x, Speed8xDebug}
	for _, s := range valid {
		if !ValidSpeed(s) {
			t.Errorf("ValidSpeed(%d) = false, want true", s)
		}
	}
	invalid := []Speed{0, -1, 3, 5, 16}
	for _, s := range invalid {
		if ValidSpeed(s) {
			t.Errorf("ValidSpeed(%d) = true, want false", s)
		}
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
