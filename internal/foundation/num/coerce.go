package num

import (
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Reject-form coercion error codes — reserved sub-range F800-F899 in
// data/errors.json's "ranges.reserved" table (module foundation.num).
// Every code below IS registered there with real severity/module/message/
// remedy fields (GR#7); the errs source-scan test guards against drift
// (BUG-008).
const (
	// codeNonFinite: a NaN or ±Inf value reached a numeric boundary and
	// must be rejected, not clamped or propagated (SEC-093).
	codeNonFinite = "MET-F800"
	// codeInt64Overflow: a finite float64 is at/outside the int64 range
	// (2^63 = float64(math.MaxInt64) would wrap negative — SEC-080).
	codeInt64Overflow = "MET-F801"
	// codeOutOfRange: a finite float64 is outside the caller-supplied
	// [lo, hi] bounds (GR#15).
	codeOutOfRange = "MET-F802"
	// codeTypeMismatch: an any-typed stored value is not a numeric type
	// (GR#16 — never trust the stored type, never type-assert blindly).
	codeTypeMismatch = "MET-F803"
)

// SafeInt64 converts a float64 to int64 and rejects — never clamps, never
// wraps — a non-finite input (NaN/±Inf) with codeNonFinite and any value
// at or outside the int64 range with codeInt64Overflow. It is the
// boundary-validation path (feat.securehelpers): module entry points,
// command handlers, and data loaders call this where a bad input must fail
// closed with a registry-sourced error (GR#7). The conserved-arithmetic
// path — where saturation is the documented invariant — remains
// [ClampInt64FromFloat].
//
// It is a pure, deterministic function of its input (GR#21): the value and
// error code returned depend only on f. The correlation ID attached to the
// returned error is freshly minted per call (GR#1) and is an audit field,
// not part of the coercion result.
func SafeInt64(f float64) (int64, error) {
	if !IsFinite(f) {
		return 0, errs.New(codeNonFinite, errs.NewCorrelationID(), map[string]any{"value": f})
	}
	// float64(math.MaxInt64) == 2^63 (MaxInt64 itself is not representable,
	// it rounds up), and float64(math.MinInt64) == -2^63 (MinInt64 IS
	// exactly representable). So the valid range is [-2^63, 2^63): f ==
	// -2^63 is MinInt64 and is accepted, while f >= 2^63 would wrap
	// negative (SEC-080) and f < -2^63 is below MinInt64.
	if f >= float64(math.MaxInt64) || f < float64(math.MinInt64) {
		return 0, errs.New(codeInt64Overflow, errs.NewCorrelationID(), map[string]any{"value": f})
	}
	return int64(f), nil
}

// BoundedFloat rejects a non-finite v (NaN/±Inf) with codeNonFinite BEFORE
// the ordered lo <= v <= hi check, then rejects a finite v outside [lo, hi]
// with codeOutOfRange. It also rejects a non-finite lo or hi with
// codeNonFinite before the range check — a NaN bound would otherwise
// silently disable the gate, because an ordered comparison against NaN is
// always false (SEC-093): BoundedFloat(5, NaN, NaN) must fail, not pass.
// The ordering is the whole point (SEC-093): checking non-finiteness FIRST
// is what keeps NaN from slipping through a range gate. lo and hi are
// caller-supplied parameters (GR#15) — this helper enforces no value
// bound of its own, only that the bounds themselves are finite.
func BoundedFloat(v, lo, hi float64) (float64, error) {
	if !IsFinite(v) {
		return 0, errs.New(codeNonFinite, errs.NewCorrelationID(), map[string]any{"value": v})
	}
	if !IsFinite(lo) || !IsFinite(hi) {
		// Report the offending bound as {value} so the non-finite message
		// names the thing that is actually non-finite, not the finite v.
		bad := lo
		if IsFinite(lo) {
			bad = hi
		}
		return 0, errs.New(codeNonFinite, errs.NewCorrelationID(), map[string]any{
			"value": bad,
			"lo":    lo,
			"hi":    hi,
		})
	}
	if v < lo || v > hi {
		return 0, errs.New(codeOutOfRange, errs.NewCorrelationID(), map[string]any{
			"value": v,
			"lo":    lo,
			"hi":    hi,
		})
	}
	return v, nil
}

// SafeInt64FromAny coerces an any-typed value (a stored field read back
// from a map/JSON/fixture) to int64 and returns a registry-sourced error
// on a wrong type, a non-finite float, or overflow — never a bare
// v.(int64) assertion that panics on nil or mis-trusts a NaN float64
// (GR#16). Accepted: the signed integer types (int64/int/int32/int16/int8),
// the unsigned integer types with a uint64/uint→int64 range check, and
// float64/float32 (routed through [SafeInt64], so NaN/±Inf/overflow are
// rejected with the same codes).
func SafeInt64FromAny(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return 0, errs.New(codeInt64Overflow, errs.NewCorrelationID(), map[string]any{"value": x})
		}
		return int64(x), nil
	case uint:
		if uint64(x) > uint64(math.MaxInt64) {
			return 0, errs.New(codeInt64Overflow, errs.NewCorrelationID(), map[string]any{"value": x})
		}
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case float64:
		return SafeInt64(x)
	case float32:
		return SafeInt64(float64(x))
	default:
		return 0, errs.New(codeTypeMismatch, errs.NewCorrelationID(), map[string]any{"type": fmt.Sprintf("%T", v)})
	}
}

// SafeFloat64FromAny coerces an any-typed value to float64, rejecting a
// wrong type and a non-finite float (NaN/±Inf) with registry-sourced
// errors (GR#16/GR#7). Integer inputs convert exactly; float64/float32
// inputs are checked for finiteness. (Overflow is not a float64 concept: a
// finite float is representable as float64 by definition. Note that a
// large int64 above 2^53 is converted exactly-in-float64-terms but may
// lose integer precision — callers needing exact integer transport should
// use [SafeInt64FromAny] instead.)
func SafeFloat64FromAny(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		if !IsFinite(x) {
			return 0, errs.New(codeNonFinite, errs.NewCorrelationID(), map[string]any{"value": x})
		}
		return x, nil
	case float32:
		f := float64(x)
		if !IsFinite(f) {
			return 0, errs.New(codeNonFinite, errs.NewCorrelationID(), map[string]any{"value": x})
		}
		return f, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int8:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint8:
		return float64(x), nil
	default:
		return 0, errs.New(codeTypeMismatch, errs.NewCorrelationID(), map[string]any{"type": fmt.Sprintf("%T", v)})
	}
}
