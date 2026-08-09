package core

import "testing"

func TestClock_TwoLayerCadence(t *testing.T) {
	c := NewClock(DefaultSecondsPerMonthAt1x)
	for i := 0; i < 65; i++ {
		c.advanceOneDay()
	}
	if got := c.Month(); got != 2 {
		t.Fatalf("Month() after 65 ticks = %d, want 2", got)
	}
	if got := c.DayInMonth(); got != 5 {
		t.Fatalf("DayInMonth() after 65 ticks = %d, want 5", got)
	}
	if got := c.Tick(); got != 65 {
		t.Fatalf("Tick() = %d, want 65", got)
	}
}

func TestClock_MonthRolloverBoundary(t *testing.T) {
	c := NewClock(DefaultSecondsPerMonthAt1x)
	var rolledOverAt []int64
	for i := int64(1); i <= 90; i++ {
		if c.advanceOneDay() {
			rolledOverAt = append(rolledOverAt, i)
		}
	}
	want := []int64{30, 60, 90}
	if len(rolledOverAt) != len(want) {
		t.Fatalf("rollovers = %v, want %v", rolledOverAt, want)
	}
	for i, w := range want {
		if rolledOverAt[i] != w {
			t.Fatalf("rollovers = %v, want %v", rolledOverAt, want)
		}
	}
}

func TestClock_TicksPerRealSecond_ScalesWithSpeed(t *testing.T) {
	base := NewClock(480)
	base.setPaused(false)

	rates := map[Speed]float64{}
	for _, s := range []Speed{Speed1x, Speed2x, Speed4x, Speed8xDebug} {
		c := base
		c.setSpeed(s)
		rates[s] = c.TicksPerRealSecond()
	}

	if rates[Speed1x] <= 0 {
		t.Fatalf("Speed1x rate = %v, want > 0", rates[Speed1x])
	}
	if got, want := rates[Speed2x], rates[Speed1x]*2; !almostEqual(got, want) {
		t.Errorf("Speed2x rate = %v, want %v (2x Speed1x)", got, want)
	}
	if got, want := rates[Speed4x], rates[Speed1x]*4; !almostEqual(got, want) {
		t.Errorf("Speed4x rate = %v, want %v (4x Speed1x)", got, want)
	}
	if got, want := rates[Speed8xDebug], rates[Speed1x]*8; !almostEqual(got, want) {
		t.Errorf("Speed8xDebug rate = %v, want %v (8x Speed1x)", got, want)
	}
}

func TestClock_TicksPerRealSecond_ZeroWhilePaused(t *testing.T) {
	c := NewClock(480) // NewClock starts paused
	if got := c.TicksPerRealSecond(); got != 0 {
		t.Fatalf("TicksPerRealSecond() while paused = %v, want 0", got)
	}
	if got := c.SecondsPerMonth(); got != 0 {
		t.Fatalf("SecondsPerMonth() while paused = %v, want 0", got)
	}
}

func TestValidSpeed(t *testing.T) {
	valid := []Speed{Speed1x, Speed2x, Speed4x, Speed8xDebug}
	for _, s := range valid {
		if !ValidSpeed(s) {
			t.Errorf("ValidSpeed(%d) = false, want true", s)
		}
	}
	invalid := []Speed{0, -1, 3, 5, 16}
	for _, s := range invalid {
		if ValidSpeed(s) {
			t.Errorf("ValidSpeed(%d) = true, want false", s)
		}
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
