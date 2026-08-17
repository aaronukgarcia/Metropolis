package maintenance

import (
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSEC153AdvanceMonthDoesNotWrapClock reproduces SEC-153 (P1, bounds-
// overflow): AdvanceMonth(MaxInt64) followed by AdvanceMonth(12) must not wrap
// the month clock negative, must not drive Efficiency outside [0,1], must
// flag the ancient age for refit, and must not decrease the backlog without
// any RunCrewDay work (AC-6/AC-7 conservation). The month and accrual
// increments saturate via num.SatAdd instead of raw +=.
func TestSEC153AdvanceMonthDoesNotWrapClock(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := a.AdvanceMonth(math.MaxInt64, "test"); err != nil {
		t.Fatalf("advance MaxInt64: %v", err)
	}
	before, err := a.CityDemand("test")
	if err != nil {
		t.Fatalf("city demand before: %v", err)
	}
	if err := a.AdvanceMonth(12, "test"); err != nil {
		t.Fatalf("advance 12: %v", err)
	}
	v, err := a.View(1, "test")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if v.AgeMonths < 0 {
		t.Fatalf("age wrapped negative: %d", v.AgeMonths)
	}
	if v.Efficiency < 0 || v.Efficiency > 1.0 {
		t.Fatalf("efficiency outside [0,1]: %v", v.Efficiency)
	}
	if !v.NeedsRefit {
		t.Fatalf("an age beyond lifetime must be flagged for refit")
	}
	after, err := a.CityDemand("test")
	if err != nil {
		t.Fatalf("city demand after: %v", err)
	}
	if after.Backlog < before.Backlog {
		t.Fatalf("backlog decreased with no work applied: %d -> %d", before.Backlog, after.Backlog)
	}
}

// TestSEC154RepairDemandNeverNegative reproduces SEC-154 (P2, bounds-
// overflow): repairDemandPerYear must never return negative demand or a figure
// below base, even when lifetimeMonths is so large that the old
// (lifetime + age) addition would overflow int64.
func TestSEC154RepairDemandNeverNegative(t *testing.T) {
	half := int64(math.MaxInt64/2) + 1
	cases := []struct {
		name            string
		base, age, life int64
	}{
		{"huge lifetime age zero", 2, 0, math.MaxInt64},
		{"sum overflows", 2, half, half},
		{"large base sum overflows", 10, half, half},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repairDemandPerYear(tc.base, tc.age, tc.life)
			if got < 0 {
				t.Fatalf("repairDemandPerYear(%d,%d,%d) = %d, must never be negative", tc.base, tc.age, tc.life, got)
			}
			if got < tc.base {
				t.Fatalf("repairDemandPerYear(%d,%d,%d) = %d, must be >= base %d", tc.base, tc.age, tc.life, got, tc.base)
			}
		})
	}
}

// TestSEC155NegativeSizeFactorRejected reproduces SEC-155 (P3, input-
// validation): a negative SizePerMille is an authoring bug and Register must
// reject it with a registry-sourced error, never silently coerce it to 1.0x —
// while a positive factor still scales the base rate.
func TestSEC155NegativeSizeFactorRejected(t *testing.T) {
	a := newTestAPI(t)
	err := a.Register(1, "dwelling", RegisterOptions{SizePerMille: -2500}, "test")
	if err == nil {
		t.Fatal("Register with negative SizePerMille must be rejected")
	}
	wantCode(t, err, ErrInvalidInput)
	if _, exists := a.instances[1]; exists {
		t.Fatal("a rejected Register must not create an instance")
	}

	// A positive factor is still accepted and scales the base rate (no
	// over-rejection of the placeholder's positive domain).
	if err := a.Register(2, "dwelling", RegisterOptions{SizePerMille: 2000}, "test"); err != nil {
		t.Fatalf("register positive size factor: %v", err)
	}
	v, err := a.View(2, "test")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear
	if want := rate * 2; v.BaseEngineerDaysPerYear != want {
		t.Fatalf("positive size factor 2000: base = %d, want 2*rate = %d", v.BaseEngineerDaysPerYear, want)
	}
}

// TestSEC156TotalBacklogCopyGuard reproduces SEC-156 (P3, encapsulation-leak):
// TotalBacklog must reject a struct-copied *MaintenanceAPI with ErrCopiedValue
// instead of returning backlog data off the copied mutex, and the original
// still reads its own backlog.
func TestSEC156TotalBacklogCopyGuard(t *testing.T) {
	orig := newTestAPI(t)
	if _, err := orig.EnqueueJob("dwelling", 50, "test"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	cp := maintenanceCopy(orig)
	if _, err := cp.TotalBacklog("test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("TotalBacklog on a copied value: want ErrCopiedValue, got %v", err)
	}
	if got, err := orig.TotalBacklog("test"); err != nil || got != 50 {
		t.Fatalf("original TotalBacklog = (%d, %v), want (50, nil)", got, err)
	}
}
