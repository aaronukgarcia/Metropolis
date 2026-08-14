package households

import (
	"math"
	"testing"
)

// This file proves the saturating-arithmetic core (FEAT-086 / GR#16) does
// what the Destructive round fuzzes for: a ±MaxInt64 / mixed-sign input can
// never wrap negative, produce +Inf/NaN, or invent/destroy units. These are
// the negative controls that CAN fail if a helper regresses to a bare + - *.

func TestSatAddSaturatesAtExtremes(t *testing.T) {
	if got := satAdd(math.MaxInt64, 1); got != math.MaxInt64 {
		t.Fatalf("satAdd(MaxInt64,1) = %d, want MaxInt64 (no wrap)", got)
	}
	if got := satAdd(math.MinInt64, -1); got != math.MinInt64 {
		t.Fatalf("satAdd(MinInt64,-1) = %d, want MinInt64 (no wrap)", got)
	}
	if got := satAdd(1, -1); got != 0 {
		t.Fatalf("satAdd(1,-1) = %d, want 0", got)
	}
	if got := satAdd(7, 8); got != 15 {
		t.Fatalf("satAdd(7,8) = %d, want 15", got)
	}
}

func TestSatSubSaturatesAtExtremes(t *testing.T) {
	if got := satSub(math.MinInt64, 1); got != math.MinInt64 {
		t.Fatalf("satSub(MinInt64,1) = %d, want MinInt64 (no wrap)", got)
	}
	if got := satSub(math.MaxInt64, -1); got != math.MaxInt64 {
		t.Fatalf("satSub(MaxInt64,-1) = %d, want MaxInt64 (no wrap)", got)
	}
	if got := satSub(15, 8); got != 7 {
		t.Fatalf("satSub(15,8) = %d, want 7", got)
	}
}

func TestSafeMulMixedSigns(t *testing.T) {
	if v, over := safeMul(math.MaxInt64, 2); !over || v != math.MaxInt64 {
		t.Fatalf("safeMul(MaxInt64,2) = (%d,%v), want (MaxInt64,true)", v, over)
	}
	if v, over := safeMul(math.MaxInt64, -2); !over || v != math.MinInt64 {
		t.Fatalf("safeMul(MaxInt64,-2) = (%d,%v), want (MinInt64,true)", v, over)
	}
	if v, over := safeMul(math.MinInt64, 2); !over || v != math.MinInt64 {
		t.Fatalf("safeMul(MinInt64,2) = (%d,%v), want (MinInt64,true)", v, over)
	}
	if v, over := safeMul(math.MinInt64, -1); !over || v != math.MaxInt64 {
		t.Fatalf("safeMul(MinInt64,-1) = (%d,%v), want (MaxInt64,true)", v, over)
	}
	if v, over := safeMul(6, 7); over || v != 42 {
		t.Fatalf("safeMul(6,7) = (%d,%v), want (42,false)", v, over)
	}
	if v, over := safeMul(0, math.MaxInt64); over || v != 0 {
		t.Fatalf("safeMul(0,MaxInt64) = (%d,%v), want (0,false)", v, over)
	}
}

func TestClampInt64FromFloat(t *testing.T) {
	if got := clampInt64FromFloat(math.NaN()); got != 0 {
		t.Fatalf("clampInt64FromFloat(NaN) = %d, want 0", got)
	}
	if got := clampInt64FromFloat(math.Inf(1)); got != math.MaxInt64 {
		t.Fatalf("clampInt64FromFloat(+Inf) = %d, want MaxInt64", got)
	}
	if got := clampInt64FromFloat(math.Inf(-1)); got != math.MinInt64 {
		t.Fatalf("clampInt64FromFloat(-Inf) = %d, want MinInt64", got)
	}
	if got := clampInt64FromFloat(float64(math.MaxInt64)); got != math.MaxInt64 {
		t.Fatalf("clampInt64FromFloat(float64(MaxInt64)) = %d, want MaxInt64 (not a wrapped negative)", got)
	}
	if got := clampInt64FromFloat(12.5); got != 12 {
		t.Fatalf("clampInt64FromFloat(12.5) = %d, want 12", got)
	}
}
