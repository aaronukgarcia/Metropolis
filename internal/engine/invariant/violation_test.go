package invariant

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestNewViolation_UnexplainedSaturatesNotWraps is SEC-060's regression: once
// the SEC-055 fix saturates evalTerms and stockCheck, a Detected Violation can
// carry expected/actual that are themselves saturated at the int64 extremes.
// newViolation's Message must then compute the imbalance with overflow-safe
// arithmetic — plain `actual - expected` wraps when the two extremes have
// opposite signs, reporting "unexplained 1" (or -1) where the true imbalance is
// on the order of 1.8e19.
func TestNewViolation_UnexplainedSaturatesNotWraps(t *testing.T) {
	cases := []struct {
		name      string
		expected  int64
		actual    int64
		wrapped   string // the value plain int64 subtraction would report
		saturated int64  // the value overflow-safe subtraction must report
	}{
		{
			name:      "actual below expected above",
			expected:  math.MaxInt64,
			actual:    math.MinInt64,
			wrapped:   "unexplained 1",
			saturated: math.MinInt64,
		},
		{
			name:      "actual above expected below",
			expected:  math.MinInt64,
			actual:    math.MaxInt64,
			wrapped:   "unexplained -1",
			saturated: math.MaxInt64,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newViolation("stock", 7, tc.expected, tc.actual, nil)

			if strings.Contains(v.Message, tc.wrapped) {
				t.Fatalf("Message = %q: unexplained wrapped under plain int64 subtraction, want saturated at %d (SEC-060)", v.Message, tc.saturated)
			}
			if !strings.Contains(v.Message, fmt.Sprintf("unexplained %d", tc.saturated)) {
				t.Fatalf("Message = %q: want unexplained saturated at %d", v.Message, tc.saturated)
			}
		})
	}
}
