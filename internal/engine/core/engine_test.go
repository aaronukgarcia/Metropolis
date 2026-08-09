package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngine_BootsWithZeroModules(t *testing.T) {
	e := NewEngine()
	if e == nil {
		t.Fatal("NewEngine() returned nil")
	}
	if err := e.AdvanceTicks("corr-zero", 5); err != nil {
		t.Fatalf("AdvanceTicks with zero registered hooks: %v", err)
	}
	if got := e.Clock().Tick(); got != 5 {
		t.Fatalf("Tick() = %d, want 5", got)
	}
}

func TestEngine_AdvanceTicks65_MonthRollovers(t *testing.T) {
	e := NewEngine()
	if err := e.AdvanceTicks("corr-65", 65); err != nil {
		t.Fatalf("AdvanceTicks(65): %v", err)
	}
	c := e.Clock()
	if got := c.Month(); got != 2 {
		t.Errorf("Month() = %d, want 2 (2 months + 5 days)", got)
	}
	if got := c.DayInMonth(); got != 5 {
		t.Errorf("DayInMonth() = %d, want 5", got)
	}
	if got := c.Tick(); got != 65 {
		t.Errorf("Tick() = %d, want 65", got)
	}
	if got := e.TicksCompleted(); got != 65 {
		t.Errorf("TicksCompleted() = %d, want 65", got)
	}
}

func TestEngine_AdvanceTicks_RejectsInvalidN(t *testing.T) {
	e := NewEngine()
	cases := []int64{0, -1, -100, MaxAdvanceTicksPerCall + 1}
	for _, n := range cases {
		err := e.AdvanceTicks("corr-invalid", n)
		if err == nil {
			t.Errorf("AdvanceTicks(%d): want error, got nil", n)
		}
	}
	// No partial advance on rejection.
	if got := e.Clock().Tick(); got != 0 {
		t.Errorf("Tick() after rejected AdvanceTicks calls = %d, want 0", got)
	}
}

func TestEngine_RegisterPhaseHook_Rejections(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPhaseHook(PhaseProduction, nil); err == nil {
		t.Error("RegisterPhaseHook(nil hook): want error, got nil")
	}
	if err := e.RegisterPhaseHook(PhaseKind("not-a-real-phase"), failingPhaseHook{}); err == nil {
		t.Error("RegisterPhaseHook(unknown phase): want error, got nil")
	}
}

func TestEngine_WorldSeedCarriedFromConstruction(t *testing.T) {
	e := NewEngine(WithWorldSeed(424242))
	if got := e.WorldSeed(); got != 424242 {
		t.Errorf("WorldSeed() = %d, want 424242", got)
	}
}

func TestEngine_PoolSizeOption(t *testing.T) {
	e := NewEngine(WithPoolSize(3))
	if got := e.PoolSize(); got != 3 {
		t.Errorf("PoolSize() = %d, want 3", got)
	}
	// Floored at 1, never 0 or negative.
	e2 := NewEngine(WithPoolSize(0))
	if got := e2.PoolSize(); got != 1 {
		t.Errorf("PoolSize() with WithPoolSize(0) = %d, want 1 (floor)", got)
	}
}

// TestNoWallClock mechanically enforces AC-12: no non-test .go file in
// this package may call time.Now anywhere. This is a belt-and-braces
// runtime check alongside the literal `grep -rn "time.Now"
// internal/engine/core/*.go` (excluding _test.go) recorded in the
// dispatch report, which returns no matches.
func TestNoWallClock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if strings.Contains(string(data), "time.Now") {
			t.Errorf("%s calls time.Now — forbidden on the tick path (AC-12, M0-ENG §1.1)", name)
		}
	}
}
