package num

import (
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// wantCode asserts err is a registry-sourced *errs.E carrying the exact
// code, via errors.Is (GR#7 — not merely a non-nil error).
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want code %s", code)
	}
	if !errors.Is(err, &errs.E{Code: code}) {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

// AC-2: SafeInt64 rejects NaN/±Inf/out-of-range with a registry-sourced
// error and never wraps or clamps (SEC-080's generalization).
func TestSafeInt64_RejectsNonFiniteAndOverflow(t *testing.T) {
	nonFinite := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, f := range nonFinite {
		v, err := SafeInt64(f)
		if v != 0 {
			t.Errorf("SafeInt64(%v) = %d, want 0 zero value on rejection", f, v)
		}
		wantCode(t, err, codeNonFinite)
	}

	overflow := []float64{
		float64(math.MaxInt64),     // 2^63: the wrap-to-negative case SEC-080 lives on
		float64(math.MaxInt64) + 1, // still 2^63 (ULP at 2^63 is 1024)
		float64(math.MinInt64) * 2, // -2^64, clearly below MinInt64
	}
	for _, f := range overflow {
		v, err := SafeInt64(f)
		if v != 0 {
			t.Errorf("SafeInt64(%v) = %d, want 0 zero value on rejection", f, v)
		}
		wantCode(t, err, codeInt64Overflow)
	}

	inRange := []struct {
		f    float64
		want int64
	}{
		{0, 0},
		{123, 123},
		{-123, -123},
		{float64(math.MinInt64), math.MinInt64}, // -2^63 is exactly representable and valid
		{float64(1 << 62), 1 << 62},             // exactly representable power of two
	}
	for _, c := range inRange {
		v, err := SafeInt64(c.f)
		if err != nil {
			t.Errorf("SafeInt64(%v) = err %v, want %d", c.f, err, c.want)
		}
		if v != c.want {
			t.Errorf("SafeInt64(%v) = %d, want %d", c.f, v, c.want)
		}
	}
}

// AC-3: BoundedFloat rejects non-finite BEFORE the ordered range check and
// returns the non-finite code for NaN/±Inf, the range code for out-of-range
// finite values (SEC-093's exact shape).
func TestBoundedFloat_RejectsNonFiniteBeforeRange(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		v, err := BoundedFloat(f, 0, 1)
		if v != 0 {
			t.Errorf("BoundedFloat(%v, 0, 1) = %v, want 0", f, v)
		}
		wantCode(t, err, codeNonFinite)
	}
	for _, f := range []float64{1.1, -0.1} {
		v, err := BoundedFloat(f, 0, 1)
		if v != 0 {
			t.Errorf("BoundedFloat(%v, 0, 1) = %v, want 0", f, v)
		}
		wantCode(t, err, codeOutOfRange)
	}
	if v, err := BoundedFloat(0.5, 0, 1); err != nil || v != 0.5 {
		t.Errorf("BoundedFloat(0.5, 0, 1) = (%v, %v), want (0.5, nil)", v, err)
	}
}

// A non-finite bound is just as dangerous as a non-finite value (SEC-093):
// the ordered range comparisons are always false against NaN, so a NaN lo or
// hi silently disables the gate and lets any value through.
func TestBoundedFloatRejectsNonFiniteBounds(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		name string
		v    float64
		lo   float64
		hi   float64
	}{
		{"both bounds NaN", 5, nan, nan},
		{"lo NaN", 5, nan, 10},
		{"hi NaN", 5, 0, nan},
	}
	for _, c := range cases {
		v, err := BoundedFloat(c.v, c.lo, c.hi)
		if v != 0 {
			t.Errorf("BoundedFloat(%v, %v, %v) = %v, want 0 zero value on rejection", c.v, c.lo, c.hi, v)
		}
		wantCode(t, err, codeNonFinite)
	}

	// ±Inf bounds are non-finite too and must reject the same way.
	for _, c := range []struct{ lo, hi float64 }{
		{math.Inf(-1), 1},
		{0, math.Inf(1)},
	} {
		v, err := BoundedFloat(0.5, c.lo, c.hi)
		if v != 0 {
			t.Errorf("BoundedFloat(0.5, %v, %v) = %v, want 0 zero value on rejection", c.lo, c.hi, v)
		}
		wantCode(t, err, codeNonFinite)
	}

	// Finite bounds still work, including a value at each inclusive edge.
	for _, c := range []struct{ v, lo, hi, want float64 }{
		{0.5, 0, 1, 0.5},
		{0, 0, 1, 0},
		{1, 0, 1, 1},
	} {
		v, err := BoundedFloat(c.v, c.lo, c.hi)
		if err != nil {
			t.Errorf("BoundedFloat(%v, %v, %v) = err %v, want %v", c.v, c.lo, c.hi, err, c.want)
		}
		if v != c.want {
			t.Errorf("BoundedFloat(%v, %v, %v) = %v, want %v", c.v, c.lo, c.hi, v, c.want)
		}
	}
}

// AC-5: the any-typed stored-value boundary — wrong type, non-finite
// float, and overflow all reject with registry codes; only a correct value
// succeeds; nil never panics.
func TestSafeInt64FromAny_RejectsWrongTypeNonFiniteOverflow(t *testing.T) {
	v, err := SafeInt64FromAny("not a number")
	if v != 0 {
		t.Errorf("SafeInt64FromAny(string) = %d, want 0", v)
	}
	wantCode(t, err, codeTypeMismatch)

	v, err = SafeInt64FromAny(float64(math.NaN()))
	if v != 0 {
		t.Errorf("SafeInt64FromAny(NaN) = %d, want 0", v)
	}
	wantCode(t, err, codeNonFinite)

	v, err = SafeInt64FromAny(float64(math.MaxInt64) + 1)
	if v != 0 {
		t.Errorf("SafeInt64FromAny(MaxInt64+1) = %d, want 0", v)
	}
	wantCode(t, err, codeInt64Overflow)

	if v, err = SafeInt64FromAny(int64(42)); err != nil || v != 42 {
		t.Errorf("SafeInt64FromAny(int64(42)) = (%d, %v), want (42, nil)", v, err)
	}

	if v, err = SafeInt64FromAny(nil); v != 0 {
		t.Errorf("SafeInt64FromAny(nil) = %d, want 0", v)
	} else {
		wantCode(t, err, codeTypeMismatch)
	}

	// uint64 above MaxInt64 must be rejected, not wrapped.
	v, err = SafeInt64FromAny(uint64(math.MaxInt64) + 1)
	if v != 0 {
		t.Errorf("SafeInt64FromAny(uint64 over MaxInt64) = %d, want 0", v)
	}
	wantCode(t, err, codeInt64Overflow)
}

func TestSafeFloat64FromAny_RejectsWrongTypeAndNonFinite(t *testing.T) {
	_, err := SafeFloat64FromAny("x")
	wantCode(t, err, codeTypeMismatch)

	_, err = SafeFloat64FromAny(math.NaN())
	wantCode(t, err, codeNonFinite)

	_, err = SafeFloat64FromAny(math.Inf(-1))
	wantCode(t, err, codeNonFinite)

	if v, err := SafeFloat64FromAny(float64(1.5)); err != nil || v != 1.5 {
		t.Errorf("SafeFloat64FromAny(1.5) = (%v, %v), want (1.5, nil)", v, err)
	}
	if v, err := SafeFloat64FromAny(int64(7)); err != nil || v != 7 {
		t.Errorf("SafeFloat64FromAny(int64(7)) = (%v, %v), want (7, nil)", v, err)
	}
}

// AC-7: every coercion helper is a pure, deterministic function of its
// inputs — identical inputs yield identical values and identical error
// codes on every call. (The correlation ID inside a returned error is an
// audit field, GR#1, and is intentionally not compared.)
func TestCoercionHelpers_Deterministic(t *testing.T) {
	i1, e1 := SafeInt64(123)
	i2, e2 := SafeInt64(123)
	if i1 != i2 {
		t.Errorf("SafeInt64(123) non-deterministic: %d vs %d", i1, i2)
	}
	assertSameCode(t, e1, e2)

	f1, e1 := BoundedFloat(0.5, 0, 1)
	f2, e2 := BoundedFloat(0.5, 0, 1)
	if f1 != f2 {
		t.Errorf("BoundedFloat(0.5,0,1) non-deterministic: %v vs %v", f1, f2)
	}
	assertSameCode(t, e1, e2)

	// Rejections must be deterministic in their code too.
	_, e1 = SafeInt64(math.NaN())
	_, e2 = SafeInt64(math.NaN())
	assertSameCode(t, e1, e2)

	_, e1 = BoundedFloat(math.Inf(1), 0, 1)
	_, e2 = BoundedFloat(math.Inf(1), 0, 1)
	assertSameCode(t, e1, e2)

	_, e1 = SafeInt64FromAny("x")
	_, e2 = SafeInt64FromAny("x")
	assertSameCode(t, e1, e2)
}

// assertSameCode asserts two errors have identical presence and code.
func assertSameCode(t *testing.T, a, b error) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("error presence mismatch: %v vs %v", a, b)
	}
	if a != nil {
		var ea, eb *errs.E
		if !errors.As(a, &ea) || !errors.As(b, &eb) {
			t.Fatalf("errors not *errs.E: %v / %v", a, b)
		}
		if ea.Code != eb.Code {
			t.Fatalf("error code mismatch: %s vs %s", ea.Code, eb.Code)
		}
	}
}

// AC-8: the reject form and the saturating form coexist — ClampInt64FromFloat
// still saturates (NaN→0, +Inf→MaxInt64) while SafeInt64 rejects.
func TestRejectForm_CoexistsWithSaturatingForm(t *testing.T) {
	if got := ClampInt64FromFloat(math.NaN()); got != 0 {
		t.Errorf("ClampInt64FromFloat(NaN) = %d, want 0 (saturating)", got)
	}
	if got := ClampInt64FromFloat(math.Inf(1)); got != math.MaxInt64 {
		t.Errorf("ClampInt64FromFloat(+Inf) = %d, want MaxInt64 (saturating)", got)
	}
	if _, err := SafeInt64(math.NaN()); err == nil {
		t.Error("SafeInt64(NaN) = nil error, want rejection")
	}
	if _, err := SafeInt64(math.Inf(1)); err == nil {
		t.Error("SafeInt64(+Inf) = nil error, want rejection")
	}
}
