package main

import (
	"context"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1211 — after a restart the restored city must report its ABSOLUTE
// tick on /health. Before the fix /health read core.Engine.TicksCompleted(),
// a per-process counter that restore never seeds, so a city rehydrated at
// tick 65 reported ticks-since-process-start (0, then small numbers) while
// the engine clock and state digest were correct.
func TestBug1211_HealthTickContinuesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1211"}

	h1, _ := newFinalHost(t, dir, 20*time.Second, time.Hour, nil)
	rc1, err := h1.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc1.Engine(), []protocol.Command{advance("b1211-a", 65)})
	preDigest := rc1.Composition().StateDigest()
	if err := h1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, _ := newFinalHost(t, dir, 20*time.Second, time.Hour, nil)
	rc2, err := h2.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate after restart: %v", err)
	}
	clk, err := rc2.Engine().Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if clk.Tick() != 65 {
		t.Fatalf("restored engine clock tick = %d, want 65", clk.Tick())
	}
	if rc2.Composition().StateDigest() != preDigest {
		t.Fatalf("restored digest differs from pre-stop digest")
	}
	healthTick := func() uint64 {
		for _, c := range h2.HealthRegistry().snapshot() {
			if c.CityID == key.CityID {
				return c.Tick
			}
		}
		t.Fatalf("city not in /health registry")
		return 0
	}
	if got := healthTick(); got != 65 {
		t.Fatalf("/health tick right after restore = %d, want 65 (absolute engine tick)", got)
	}
	submitAll(t, rc2.Engine(), []protocol.Command{advance("b1211-b", 7)})
	if got := healthTick(); got != 72 {
		t.Fatalf("/health tick after 7 more ticks = %d, want 72", got)
	}
	clk, _ = rc2.Engine().Clock()
	if clk.Tick() != 72 {
		t.Fatalf("engine clock = %d, want 72", clk.Tick())
	}
}
