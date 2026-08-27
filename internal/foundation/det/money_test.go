package det

import (
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
