package attract

import (
	"testing"
)

// TestMissingWorldPool is AC-11: a missing/unloadable A_world is a named,
// registry-sourced error (the engine refuses to construct), distinguishable
// from a genuine zero baseline — it is never silently defaulted to
// A_world = 0.
func TestMissingWorldPool(t *testing.T) {
	cfg := validConfig()
	cfg.World = nil
	if _, err := New(cfg, 7, "corr-attract"); err == nil {
		t.Fatal("New accepted a nil WorldPool")
	} else {
		isErr(t, err, ErrWorldPoolMissing)
	}

	// A JSON config that omits aWorld entirely is the same missing case.
	doc := []byte(`{
		"weights": {"jobAvailability": 0.2, "housingAffordability": 0.2, "serviceCoverage": 0.15,
		            "environment": 0.1, "leisureFit": 0.1, "safety": 0.1, "reputation": 0.15},
		"migrationRate": 1.0,
		"reputation": {"riseRate": 0.2, "fallRate": 0.8, "max": 100}
	}`)
	if _, err := ParseConfig(doc, "corr-attract"); err == nil {
		t.Fatal("ParseConfig accepted a missing aWorld")
	} else {
		isErr(t, err, ErrWorldPoolMissing)
	}

	// A genuine zero baseline is expressible and loads — distinguishable from
	// "missing".
	cfg.World = NewStaticWorldPool(0)
	a, err := New(cfg, 7, "corr-attract")
	if err != nil {
		t.Fatalf("New(StaticWorldPool{0}) should succeed: %v", err)
	}
	if got := a.AWorld(); got != 0 {
		t.Fatalf("AWorld = %v, want 0 (genuine zero)", got)
	}
}

// TestWorldPoolSeam is AC-8's interface half: A_world is read through the
// WorldPool accessor, so a custom (future dynamic) pool is an interface
// change, not a rewrite of the migration math.
func TestWorldPoolSeam(t *testing.T) {
	cfg := validConfig()
	cfg.World = dynamicWorldPool{value: 72}
	a, err := New(cfg, 7, "corr-attract")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := a.AWorld(); got != 72 {
		t.Fatalf("AWorld = %v, want 72 (custom pool not honoured)", got)
	}
}

// dynamicWorldPool is a test WorldPool standing in for the future
// finite/dynamic §4 pool.
type dynamicWorldPool struct{ value float64 }

func (d dynamicWorldPool) AWorld() float64 { return d.value }
