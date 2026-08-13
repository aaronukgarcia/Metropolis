package core

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

func writePacingFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, data.FilePacing), []byte(content), 0o644); err != nil {
		t.Fatalf("write pacing fixture: %v", err)
	}
}

func validPacingJSON(seconds int) string {
	return `{"version": 1, "secondsPerMonthAt1x": ` + strconv.Itoa(seconds) + `}`
}

// --- FEAT-030: secondsPerMonthAt1x is genuinely loaded from data ---------

// TestLoadSecondsPerMonthAt1x_ReadsRealDataFile proves LoadDefaultSecondsPerMonthAt1x
// resolves this repository's own data/pacing.json and returns the value
// currently checked in there (480 — the unchanged placeholder carried
// over from the old Go var, per the balance-number regime: this fix is
// about SOURCING, not repicking the number).
func TestLoadSecondsPerMonthAt1x_ReadsRealDataFile(t *testing.T) {
	got, err := LoadDefaultSecondsPerMonthAt1x(testCorrelationID())
	if err != nil {
		t.Fatalf("LoadDefaultSecondsPerMonthAt1x: %v", err)
	}
	if got != DefaultSecondsPerMonthAt1x {
		t.Fatalf("LoadDefaultSecondsPerMonthAt1x() = %d, want %d (data/pacing.json vs the fallback var)", got, DefaultSecondsPerMonthAt1x)
	}
}

// TestLoadSecondsPerMonthAt1x_FixtureMutationFlowsThrough is the
// fixture-mutation regression test: it proves the loaded value genuinely
// tracks data/pacing.json's content — not just that Load succeeds — by
// loading once, editing the fixture file on disk, loading again, and
// asserting the SECOND load reflects the NEW number. A test that only
// asserted Load() doesn't error would pass even if the loader silently
// ignored the file and always returned a hardcoded 480.
func TestLoadSecondsPerMonthAt1x_FixtureMutationFlowsThrough(t *testing.T) {
	dir := t.TempDir()
	writePacingFixture(t, dir, validPacingJSON(480))

	got1, err := LoadSecondsPerMonthAt1x(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadSecondsPerMonthAt1x (first load): %v", err)
	}
	if got1 != 480 {
		t.Fatalf("first load = %d, want 480 (fixture value)", got1)
	}

	// Mutate the fixture to a different value and reload — the SAME code
	// path must reflect the new number.
	changed := strings.Replace(validPacingJSON(480), "480", "999", 1)
	writePacingFixture(t, dir, changed)

	got2, err := LoadSecondsPerMonthAt1x(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadSecondsPerMonthAt1x (second load): %v", err)
	}
	if got2 != 999 {
		t.Fatalf("second load after editing fixture = %d, want 999 — value did not flow from data/pacing.json", got2)
	}

	// The loaded value must actually drive the clock — not just be
	// computed and discarded — so exercise NewClock with it too.
	c := NewClock(got2)
	c.setPaused(false)
	c.setSpeed(Speed1x)
	if got := c.SecondsPerMonth(); got != 999 {
		t.Fatalf("NewClock(999).SecondsPerMonth() = %d, want 999", got)
	}
}

// TestLoadSecondsPerMonthAt1x_RejectsNonPositive proves Load-time
// validation (AC-10's field-error convention) rejects a zero or
// negative secondsPerMonthAt1x rather than accepting a value that would
// make Clock.SecondsPerMonth divide by zero or go negative.
func TestLoadSecondsPerMonthAt1x_RejectsNonPositive(t *testing.T) {
	for _, bad := range []int{0, -1, -480} {
		dir := t.TempDir()
		writePacingFixture(t, dir, validPacingJSON(bad))

		if _, err := LoadSecondsPerMonthAt1x(dir, testCorrelationID()); err == nil {
			t.Errorf("LoadSecondsPerMonthAt1x with secondsPerMonthAt1x=%d: got nil error, want a rejection", bad)
		} else {
			var e *errs.E
			if !errors.As(err, &e) {
				t.Fatalf("error is not a registry-sourced *errs.E: %v (%T)", err, err)
			}
			if e.Code != ErrPacingDataInvalid {
				t.Errorf("error code = %s, want %s", e.Code, ErrPacingDataInvalid)
			}
		}
	}
}

// TestLoadSecondsPerMonthAt1x_MissingFile proves a missing pacing.json
// is a registry-sourced error, never a silent fallback.
func TestLoadSecondsPerMonthAt1x_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSecondsPerMonthAt1x(dir, testCorrelationID()); err == nil {
		t.Fatal("LoadSecondsPerMonthAt1x with no pacing.json present: got nil error, want a rejection")
	}
}
