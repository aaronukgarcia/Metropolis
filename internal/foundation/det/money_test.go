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

// AC-11: the MinInt64/-1 division itself overflows even when the multiply
// does not — MulRat(MinInt64, 1, -1) is mathematically +2^63, which must
// error rather than silently wrap back to MinInt64.
func TestMulRat_DivisionOverflow(t *testing.T) {
	_, err := MulRat("corr-money-9", Micropounds(math.MinInt64), 1, -1)
	if err == nil {
		t.Fatal("MulRat(MinInt64, 1, -1): want overflow error, got nil")
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
