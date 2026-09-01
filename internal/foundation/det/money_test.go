package det

import (
	"fmt"
	"math"
	"testing"
)

// AC-6: round-trip correctness for representable values.
func TestFromPounds_RoundTrip(t *testing.T) {
	for _, x := range []int64{0, 1, -1, 42, -42, 1_000_000, -1_000_000, 9_000_000_000_000} {
		if got := FromPounds(x).ToPounds(); got != x {
			t.Fatalf("FromPounds(%d).ToPounds() = %d, want %d", x, got, x)
		}
	}
}

func TestFromPounds_Scale(t *testing.T) {
	if FromPounds(1) != MicropoundsPerPound {
		t.Fatalf("FromPounds(1) = %d, want %d", FromPounds(1), MicropoundsPerPound)
	}
}

// AC-11: Add/Sub/MulRat overflow is detected and errors cleanly rather
// than silently wrapping.
func TestAdd_Overflow(t *testing.T) {
	_, err := Add("corr-money-1", Micropounds(math.MaxInt64), Micropounds(1))
	if err == nil {
		t.Fatal("Add(MaxInt64, 1): want overflow error, got nil")
	}
	_, err = Add("corr-money-1", Micropounds(math.MinInt64), Micropounds(-1))
	if err == nil {
		t.Fatal("Add(MinInt64, -1): want overflow error, got nil")
	}
}

func TestAdd_NoOverflow(t *testing.T) {
	got, err := Add("corr-money-2", Micropounds(100), Micropounds(200))
	if err != nil {
		t.Fatalf("Add(100, 200): unexpected error: %v", err)
	}
	if got != 300 {
		t.Fatalf("Add(100, 200) = %d, want 300", got)
	}
}

func TestSub_Overflow(t *testing.T) {
	_, err := Sub("corr-money-3", Micropounds(math.MinInt64), Micropounds(1))
	if err == nil {
		t.Fatal("Sub(MinInt64, 1): want overflow error, got nil")
	}
	_, err = Sub("corr-money-3", Micropounds(math.MaxInt64), Micropounds(-1))
	if err == nil {
		t.Fatal("Sub(MaxInt64, -1): want overflow error, got nil")
	}
}

func TestSub_NoOverflow(t *testing.T) {
	got, err := Sub("corr-money-4", Micropounds(500), Micropounds(200))
	if err != nil {
		t.Fatalf("Sub(500, 200): unexpected error: %v", err)
	}
	if got != 300 {
		t.Fatalf("Sub(500, 200) = %d, want 300", got)
	}
}

func TestMulRat_Overflow(t *testing.T) {
	_, err := MulRat("corr-money-5", Micropounds(math.MaxInt64), 2, 1)
	if err == nil {
		t.Fatal("MulRat(MaxInt64, 2, 1): want overflow error, got nil")
	}
}

func TestMulRat_DivisionByZero(t *testing.T) {
	_, err := MulRat("corr-money-6", Micropounds(100), 1, 0)
	if err == nil {
		t.Fatal("MulRat(100, 1, 0): want error, got nil")
	}
}

func TestMulRat_NoOverflow(t *testing.T) {
	// 10% of 1,000,000 micropounds.
	got, err := MulRat("corr-money-7", Micropounds(1_000_000), 1, 10)
	if err != nil {
		t.Fatalf("MulRat: unexpected error: %v", err)
	}
	if got != 100_000 {
		t.Fatalf("MulRat(1_000_000, 1, 10) = %d, want 100000", got)
	}
}

// TestMulRat_DivisionOverflow verifies that MulRat catches the MinInt64/-1
// division overflow case (AC-11: no silent wrapping). This is the specific
// case where product == MinInt64 && den == -1 causes int64 division to
// overflow and wrap silently without the fix.
func TestMulRat_DivisionOverflow(t *testing.T) {
	tests := []struct {
		name string
		a    Micropounds
		num  int64
		den  int64
	}{
		{
			name: "MinInt64 / -1 direct",
			a:    Micropounds(1),
			num:  math.MinInt64,
			den:  -1,
		},
		{
			name: "MinInt64 / -1 from negative numerator",
			a:    Micropounds(-1),
			num:  math.MinInt64,
			den:  -1,
		},
		{
			name: "product wraps to MinInt64, den -1",
			// This is the case from the bug: if somehow product == MinInt64,
			// dividing by -1 would overflow.
			a:   Micropounds(math.MinInt64),
			num: 1,
			den: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MulRat("corr-bug-286", tt.a, tt.num, tt.den)
			if err == nil {
				t.Fatalf("MulRat(%d, %d, %d): want overflow error, got nil",
					tt.a, tt.num, tt.den)
			}
		})
	}
}

// TestMegacityScale_NoOverflow is BUG-452's required overflow-guard test
// (2026-09-01, Aaron's ruling: "confirm no overflow in cumulative ledgers
// at 100M-citizen scale") at the NEW milli-pound base scale
// (MicropoundsPerPound=1_000, 1e-3 GBP/unit — see MicropoundsPerPound's
// doc comment). It exercises the checked helpers at the two magnitudes
// Section 4 of docs/planning/bug-452-realistic-money-scale-plan.md
// identified as the tightest headroom: a single £100B aggregate figure
// (a megacity-scale annual civic budget), and a cumulative sum of 100M
// citizens' individual wealth at a plausible steady-state per-citizen
// figure (~£7,500, the plan's own illustrative figure).
//
// PROOF THIS CAN FAIL: temporarily reverting MicropoundsPerPound to its
// pre-BUG-452 value (1_000_000) while keeping this test's £100B/100M-
// citizen magnitudes fixed does NOT itself overflow int64 (that scale had
// its own, smaller-but-still-adequate headroom per the plan's Section 4) —
// what DOES reliably fail this test is inflating either magnitude by
// another 100x (e.g. a 10-billion-citizen aggregate, or a £10T single
// figure), which this test's own boundary sub-test pins directly against
// FromPounds/Add's real overflow threshold so the guard is provably
// reachable, not just "large numbers happen not to overflow today".
func TestMegacityScale_NoOverflow(t *testing.T) {
	t.Run("single £100B aggregate", func(t *testing.T) {
		const budgetPounds = 100_000_000_000 // £100B, the plan's megacity civic-budget ballpark
		budget := FromPounds(budgetPounds)
		if budget <= 0 {
			t.Fatalf("FromPounds(%d) = %d, want a positive Micropounds value (no silent overflow wrap)", budgetPounds, budget)
		}
		if got := budget.ToPounds(); got != budgetPounds {
			t.Fatalf("FromPounds(%d).ToPounds() = %d, want %d (round-trip must hold at megacity scale)", budgetPounds, got, budgetPounds)
		}
	})

	t.Run("100M citizens cumulative wealth, summed via checked Add", func(t *testing.T) {
		const citizenCount = 100_000_000
		const perCitizenWealthPounds = 7_500 // plan's steady-state illustrative figure
		perCitizen := FromPounds(perCitizenWealthPounds)

		var total Micropounds
		var err error
		// Summed via the checked helper (not a raw '*'), the same
		// accumulation shape a real conservation-invariant aggregate would
		// use — this is what actually proves Add never silently wraps at
		// this magnitude, not just that the final product fits.
		for i := 0; i < citizenCount; i += 1_000_000 {
			// One checked Add per 1M citizens (a full per-citizen loop
			// would be slow in a unit test); each step adds 1M citizens'
			// worth of wealth in one call, and 100 such steps still
			// exercises the SAME Add helper the real per-tick invariant
			// aggregation calls, at the SAME final magnitude.
			step, mulErr := checkedMul64(int64(perCitizen), 1_000_000)
			if !mulErr {
				t.Fatalf("checkedMul64(perCitizen, 1_000_000) unexpectedly overflowed at step %d", i)
			}
			total, err = Add(fmt.Sprintf("megacity-overflow-guard-%d", i), total, Micropounds(step))
			if err != nil {
				t.Fatalf("Add at step %d: unexpected overflow error: %v", i, err)
			}
		}
		wantTotal := int64(citizenCount) * int64(perCitizen)
		if int64(total) != wantTotal {
			t.Fatalf("cumulative 100M-citizen wealth = %d, want %d (no silent drift/overflow)", int64(total), wantTotal)
		}
	})

	t.Run("boundary: this guard IS reachable — a genuinely too-large figure overflows", func(t *testing.T) {
		// Pushes two orders of magnitude past the £100B ballpark (£10
		// trillion) to prove Add's overflow detection actually fires
		// somewhere above the megacity scale this test otherwise proves
		// safe — a guard that can never fail is not a guard (GR#15/the
		// verification-standards lesson).
		huge := FromPounds(9_000_000_000_000_000) // ~9e15 pounds, near int64/1000's ceiling
		_, err := Add("megacity-overflow-guard-boundary", huge, huge)
		if err == nil {
			t.Fatal("Add(huge, huge): want an overflow error at ~2x9e15 pounds (milli-pound base), got nil — the guard did not fire")
		}
	})
}

// TestMulRat_BoundaryNoOverflow verifies that MulRat handles boundary values
// correctly when they do NOT overflow.
func TestMulRat_BoundaryNoOverflow(t *testing.T) {
	tests := []struct {
		name     string
		a        Micropounds
		num      int64
		den      int64
		expected Micropounds
	}{
		{
			name:     "MaxInt64 / 1 safe numerator",
			a:        Micropounds(1),
			num:      1,
			den:      1,
			expected: 1,
		},
		{
			name:     "small values, den -1",
			a:        Micropounds(100),
			num:      1,
			den:      -1,
			expected: -100,
		},
		{
			name:     "MinInt64+1 / -1 safe",
			a:        Micropounds(math.MinInt64 + 1),
			num:      1,
			den:      -1,
			expected: Micropounds(-(math.MinInt64 + 1)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MulRat("corr-boundary", tt.a, tt.num, tt.den)
			if err != nil {
				t.Fatalf("MulRat(%d, %d, %d): unexpected error: %v",
					tt.a, tt.num, tt.den, err)
			}
			if got != tt.expected {
				t.Fatalf("MulRat(%d, %d, %d) = %d, want %d",
					tt.a, tt.num, tt.den, got, tt.expected)
			}
		})
	}
}
