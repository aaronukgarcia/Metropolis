package det

import (
	"math"
	"testing"
)

// TestRegression_MulRat_MinInt64DividedByNegOne is the exact boundary
// BUG-310 names: a=MinInt64, num=1, den=-1. checkedMul64(MinInt64, 1)
// does not overflow (product == MinInt64 exactly), so the pre-existing
// multiply-overflow guard never fires -- but MinInt64 / -1 is ITSELF an
// overflow in Go's two's-complement division (wraps back to MinInt64,
// silently, with no panic and no runtime error). MulRat must catch this
// second overflow surface explicitly.
func TestRegression_MulRat_MinInt64DividedByNegOne(t *testing.T) {
	_, err := MulRat("corr-bug310-1", Micropounds(math.MinInt64), 1, -1)
	if err == nil {
		t.Fatal("MulRat(MinInt64, 1, -1): want division-overflow error, got nil")
	}
}
