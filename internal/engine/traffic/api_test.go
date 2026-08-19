package traffic

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/education"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/roads"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestTraffic_AC2_DataSourcedWages(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "traffic-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	configData := `{"baseCommuteHours": 7.5, "baseAccessMinutes": 22.0, "baseCommuteMinutes": 45.0, "baseActiveTravelShare": 0.25, "bprAlpha": 0.15, "bprBeta": 4.0, "capacityPerLanePerHour": 1200.0}`
	_ = os.WriteFile(filepath.Join(tempDir, "traffic.json"), []byte(configData), 0644)

	err = api.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if api.cfg.BaseCommuteHours != 7.5 || api.cfg.BaseAccessMinutes != 22.0 {
		t.Errorf("expected data-sourced config 7.5/22.0, got %f/%f", api.cfg.BaseCommuteHours, api.cfg.BaseAccessMinutes)
	}
}

func TestTraffic_AC11_CommuteAccounting(t *testing.T) {
	api := New()

	h, err := api.CommuteHours(1234, "test-commute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != 5.0 {
		t.Errorf("expected default hours 5.0, got %f", h)
	}

	// Verify un-registered citizen ID 0 returns error code MET-G4501 (AC-9)
	_, err = api.CommuteHours(0, "test-error")
	if err == nil {
		t.Error("expected error for citizen ID 0")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrUnknownCitizen {
		t.Errorf("expected unknown citizen error MET-G4501, got: %v", err)
	}
}

func TestTraffic_AC11_LeisureAccessMinutes(t *testing.T) {
	api := New()

	// Direct access query
	m, err := api.AccessMinutes(5678, leisure.Category(1), "test-access")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != 15.0 {
		t.Errorf("expected default minutes 15.0, got %f", m)
	}

	// Verify un-registered citizen ID 0 returns error code MET-G4501 (AC-9)
	_, err = api.AccessMinutes(0, leisure.Category(1), "test-error")
	if err == nil {
		t.Error("expected error for citizen ID 0")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrUnknownCitizen {
		t.Errorf("expected unknown citizen error MET-G4501, got: %v", err)
	}
}

func TestTraffic_TripFiling(t *testing.T) {
	api := New()

	// Verify leisure trip filing
	err := api.AddTripDemand(leisure.TripDemand{
		District: 12,
		Count:    150,
	})
	if err != nil {
		t.Fatalf("unexpected leisure filing error: %v", err)
	}

	// Verify education trip filing
	err = api.RegisterTrip(education.TripDemand{
		SchoolID: 301,
		Count:    50,
	})
	if err != nil {
		t.Fatalf("unexpected education filing error: %v", err)
	}

	// Verify direct school demand filing
	err = api.AddDemand(301, 25)
	if err != nil {
		t.Fatalf("unexpected direct demand error: %v", err)
	}

	api.mu.RLock()
	d12 := api.demands[12]
	d301 := api.demands[301]
	api.mu.RUnlock()

	if d12 != 150 {
		t.Errorf("expected demand for district 12 to be 150, got %d", d12)
	}
	if d301 != 75 {
		t.Errorf("expected aggregate demand for school 301 to be 75, got %d", d301)
	}
}

func TestTraffic_AC9_InvalidInputValidation(t *testing.T) {
	api := New()

	// Register negative count leisure trip should fail with MET-G4502
	err := api.AddTripDemand(leisure.TripDemand{
		District: 12,
		Count:    -50,
	})
	if err == nil {
		t.Error("expected error for negative trip count")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidInput {
		t.Errorf("expected invalid input error MET-G4502, got: %v", err)
	}
}

func TestTraffic_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.AddDemand(301, 100)
	_ = api2.AddDemand(301, 100)

	api1.mu.RLock()
	d1 := api1.demands[301]
	api2.mu.RLock()
	d2 := api2.demands[301]
	api1.mu.RUnlock()
	api2.mu.RUnlock()

	if d1 != d2 {
		t.Errorf("expected demand to be equal, got %d and %d", d1, d2)
	}
}

func TestTraffic_AC13_Concurrency(t *testing.T) {
	api := New()

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(schoolID uint64) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = api.AddDemand(schoolID, 2)
				_, _ = api.CommuteHours(1234, "test-concurrency")
			}
		}(uint64(i * 100))
	}

	wg.Wait()
}

func TestTraffic_AC15_AdvanceTickReset(t *testing.T) {
	api := New()

	_ = api.AddDemand(301, 1000)
	c1, _ := api.CommuteHours(1234, "test-reset")

	_ = api.AdvanceTick("test-reset")
	c2, _ := api.CommuteHours(1234, "test-reset")

	if c1 <= c2 {
		t.Errorf("expected commute hours to be higher before tick reset (%f) than after (%f)", c1, c2)
	}
	if c2 != 5.0 {
		t.Errorf("expected commute hours to return to base 5.0 after reset, got %f", c2)
	}
}

func TestTraffic_Int64Overflow(t *testing.T) {
	api := New()

	maxInt64 := int64(^uint64(0) >> 1)

	_ = api.AddDemand(301, maxInt64)
	_ = api.AddDemand(301, 10) // attempt to overflow

	api.mu.RLock()
	d := api.demands[301]
	api.mu.RUnlock()

	if d < 0 {
		t.Errorf("expected saturating add to prevent negative overflow, got %d", d)
	}
	if d != maxInt64 {
		t.Errorf("expected saturating add to cap at MaxInt64, got %d", d)
	}
}

func TestTraffic_Stage1_NetworkLoading(t *testing.T) {
	api := New()
	_ = api.AddNode(1)
	_ = api.AddNode(2)
	_ = api.AddLink(10, 1, 2, 10.0)

	api.mu.RLock()
	linkCount := len(api.links)
	api.mu.RUnlock()

	if linkCount != 1 {
		t.Errorf("expected 1 link, got %d", linkCount)
	}

	err := api.AddLinkVolume(10, -5.0)
	if err == nil {
		t.Error("expected error for negative volume")
	}

	_ = api.AddLinkVolume(10, 2400.0) // 2 lanes worth of capacity at 1200/hr

	// Default lanes=1, speed=50.0, capacity=1200
	// T0 = 10.0 / 50.0 = 0.2
	// V/C = 2400 / 1200 = 2.0
	// T = 0.2 * (1 + 0.15 * 2.0^4) = 0.2 * (1 + 0.15 * 16) = 0.2 * (1 + 2.4) = 0.2 * 3.4 = 0.68
	travelTime, err := api.LinkTravelTime(10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := 0.68
	if math.Abs(travelTime-expected) > 1e-9 {
		t.Errorf("expected travel time %f, got %f", expected, travelTime)
	}
}

func TestTraffic_Stage1_RoadsDependency(t *testing.T) {
	api := New()

	// Create real roads API to satisfy the network dependency
	roadsAPI, err := roads.LoadDefault(42, "test-traffic")
	if err != nil {
		t.Fatalf("failed to load roads api: %v", err)
	}
	_ = api.SetRoads(roadsAPI)

	// Mock link
	_ = api.AddLink(10, 1, 2, 10.0)

	// Will query roads API and use defaults if the road doesn't exist
	travelTime, _ := api.LinkTravelTime(10, 0)
	if travelTime <= 0 {
		t.Error("expected non-zero travel time when backed by roads API")
	}
}

// --- MOD-023 r2/Stage-1-r1 destructive-verdict fixes: proof-of-failure tests ---
// Each test below is written so it demonstrably FAILS against the
// pre-fix code (the lane/ben snapshot this delivery assembled from),
// per the verification-standards rule that a check must be able to fail.

// TestTraffic_BoundedAcrossManyDays proves the ORIGINAL unbounded-growth
// defect (commute hours climbing monotonically, base 5.0 -> 995.0 over
// 3960 simulated days, per the 2026-08-18 20:09 destructive verdict) is
// fixed when AdvanceTick is called once per day: a single day's demand
// bounds the coarse multiplier regardless of how many days elapse.
func TestTraffic_BoundedAcrossManyDays(t *testing.T) {
	api := New()
	const days = 3960
	const dailyDemand = int64(500)

	maxSeen := 0.0
	for day := 0; day < days; day++ {
		if err := api.AddDemand(301, dailyDemand); err != nil {
			t.Fatalf("day %d: unexpected AddDemand error: %v", day, err)
		}
		h, err := api.CommuteHours(1234, "bounded-test")
		if err != nil {
			t.Fatalf("day %d: unexpected CommuteHours error: %v", day, err)
		}
		if h > maxSeen {
			maxSeen = h
		}
		if err := api.AdvanceTick("bounded-test"); err != nil {
			t.Fatalf("day %d: unexpected AdvanceTick error: %v", day, err)
		}
	}

	// One day's worth of demand (500) bounds the multiplier to
	// 1 + 500/1000*0.1 = 1.05, so commute hours never exceed base*1.06
	// across 3960 simulated days -- versus the unbounded-growth repro's
	// 995.0 without the per-day AdvanceTick call.
	bound := api.cfg.BaseCommuteHours * 1.06
	if maxSeen > bound {
		t.Errorf("commute hours grew unbounded across %d days: max seen %v, expected <= %v", days, maxSeen, bound)
	}
}

// TestTraffic_DayBoundaryOrdering proves the documented AdvanceTick
// contract (doc.go's "Day-boundary contract" section): the PRIOR day's
// demand is wiped by the call, and demand added AFTER the call (i.e.
// during the new day) is immediately visible and survives until the NEXT
// call.
func TestTraffic_DayBoundaryOrdering(t *testing.T) {
	api := New()

	// Day 1 demand.
	_ = api.AddDemand(301, 1000)

	// Day 1->2 boundary: wipes day 1's demand (the "prior day").
	if err := api.AdvanceTick("boundary-1"); err != nil {
		t.Fatalf("unexpected AdvanceTick error: %v", err)
	}

	// Right after the tick, with no new demand added yet, the multiplier
	// must be back to base -- day 1's demand did not leak past its own
	// boundary.
	hImmediatelyAfterTick, _ := api.CommuteHours(1234, "day2-pre")
	if hImmediatelyAfterTick != api.cfg.BaseCommuteHours {
		t.Errorf("expected prior day's demand wiped immediately after AdvanceTick, got %v want base %v", hImmediatelyAfterTick, api.cfg.BaseCommuteHours)
	}

	// Demand added AFTER the tick belongs to day 2 and must survive,
	// visible immediately -- it is NOT wiped by the tick that just ran.
	_ = api.AddDemand(301, 400)
	h2, _ := api.CommuteHours(1234, "day2-post")
	if h2 <= api.cfg.BaseCommuteHours {
		t.Errorf("expected day-2 demand added after the tick to be visible (h2=%v > base=%v)", h2, api.cfg.BaseCommuteHours)
	}

	// Day 2->3 boundary: wipes day 2's demand in turn.
	if err := api.AdvanceTick("boundary-2"); err != nil {
		t.Fatalf("unexpected AdvanceTick error: %v", err)
	}
	h3, _ := api.CommuteHours(1234, "day3")
	if h3 != api.cfg.BaseCommuteHours {
		t.Errorf("expected day-2's demand wiped at the next boundary, got %v want base %v", h3, api.cfg.BaseCommuteHours)
	}
}

// TestTraffic_ValidateConfig_RejectsBadBPRParams proves LoadConfig's
// validation rule directly against hand-built Config values, including a
// NaN injection JSON itself cannot express (MOD-023 r2 destructive
// verdict: bprAlpha/bprBeta previously loaded -999/-999 without error).
func TestTraffic_ValidateConfig_RejectsBadBPRParams(t *testing.T) {
	base := Config{
		BaseCommuteHours:       5,
		BaseAccessMinutes:      15,
		BaseCommuteMinutes:     30,
		BaseActiveTravelShare:  0.1,
		BPRAlpha:               0.15,
		BPRBeta:                4.0,
		CapacityPerLanePerHour: 1200,
	}

	if _, ok := validateConfig(base); !ok {
		t.Fatal("expected the base config to validate cleanly")
	}

	cases := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"negativeAlpha", func(c *Config) { c.BPRAlpha = -999 }},
		{"negativeBeta", func(c *Config) { c.BPRBeta = -999 }},
		{"zeroAlpha", func(c *Config) { c.BPRAlpha = 0 }},
		{"zeroBeta", func(c *Config) { c.BPRBeta = 0 }},
		{"nanAlpha", func(c *Config) { c.BPRAlpha = math.NaN() }},
		{"nanBeta", func(c *Config) { c.BPRBeta = math.NaN() }},
		{"infAlpha", func(c *Config) { c.BPRAlpha = math.Inf(1) }},
		{"zeroCapacity", func(c *Config) { c.CapacityPerLanePerHour = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			tc.mutate(&bad)
			if reason, ok := validateConfig(bad); ok {
				t.Errorf("expected %s to be rejected, got ok with reason %q", tc.name, reason)
			}
		})
	}
}

// TestTraffic_LoadConfig_RejectsInvalidBPRParamsFromDisk proves the
// disk-loading path (not just the unit-level validateConfig helper)
// rejects the exact -999/-999 values the r2 destructive verdict found
// loading silently.
func TestTraffic_LoadConfig_RejectsInvalidBPRParamsFromDisk(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "traffic-badconfig-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	configData := `{"baseCommuteHours":5,"baseAccessMinutes":15,"baseCommuteMinutes":30,"baseActiveTravelShare":0.1,"bprAlpha":-999,"bprBeta":-999,"capacityPerLanePerHour":1200}`
	_ = os.WriteFile(filepath.Join(tempDir, "traffic.json"), []byte(configData), 0644)

	if err := api.LoadConfig(tempDir); err == nil {
		t.Fatal("expected LoadConfig to reject -999 bprAlpha/bprBeta, got nil")
	}

	// The rejected load must not have clobbered the safe default config.
	if api.cfg.BPRAlpha != 0.15 || api.cfg.BPRBeta != 4.0 {
		t.Errorf("expected default config preserved after rejected load, got alpha=%v beta=%v", api.cfg.BPRAlpha, api.cfg.BPRBeta)
	}
}

// TestTraffic_BPRGuard_CapacityZero attacks LinkTravelTime with a
// (directly-forced) zero capacity -- reproducing the r2 verdict's
// "capacity=0 returns +Inf nil-error" finding -- and proves it now errors.
func TestTraffic_BPRGuard_CapacityZero(t *testing.T) {
	api := New()
	_ = api.AddLink(10, 1, 2, 10.0)
	_ = api.AddLinkVolume(10, 100.0)

	api.mu.Lock()
	api.cfg.CapacityPerLanePerHour = 0
	api.mu.Unlock()

	tt, err := api.LinkTravelTime(10, 0)
	if err == nil {
		t.Fatalf("expected error for zero capacity, got nil (travelTime=%v)", tt)
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrNonFiniteTravelTime {
		t.Errorf("expected ErrNonFiniteTravelTime MET-G4503, got: %v", err)
	}
}

// TestTraffic_BPRGuard_HugeVolumeOverflow attacks LinkTravelTime with an
// astronomically large volume -- reproducing the r2 verdict's
// "vol=1e300 overflows to +Inf" finding -- and proves it now errors.
func TestTraffic_BPRGuard_HugeVolumeOverflow(t *testing.T) {
	api := New()
	_ = api.AddLink(10, 1, 2, 10.0)
	_ = api.AddLinkVolume(10, 1e300)

	tt, err := api.LinkTravelTime(10, 0)
	if err == nil {
		t.Fatalf("expected error for overflowing V/C^beta term, got nil (travelTime=%v)", tt)
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrNonFiniteTravelTime {
		t.Errorf("expected ErrNonFiniteTravelTime MET-G4503, got: %v", err)
	}
}

// TestTraffic_BPRGuard_NegativeVolume attacks LinkTravelTime with a
// (directly-forced) negative link volume -- reproducing the r2 verdict's
// "negative V/C with non-integer beta = NaN" finding -- and proves it now
// errors before the pow term is ever computed.
func TestTraffic_BPRGuard_NegativeVolume(t *testing.T) {
	api := New()
	_ = api.AddLink(10, 1, 2, 10.0)

	api.mu.Lock()
	api.links[10].Volume = -50.0
	api.cfg.BPRBeta = 4.5 // non-integer exponent: negative base would be NaN
	api.mu.Unlock()

	tt, err := api.LinkTravelTime(10, 0)
	if err == nil {
		t.Fatalf("expected error for negative volume, got nil (travelTime=%v)", tt)
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrNonFiniteTravelTime {
		t.Errorf("expected ErrNonFiniteTravelTime MET-G4503, got: %v", err)
	}
}

// TestTraffic_Determinism_SortedKeysByteIdentical proves demandMultiplier's
// sorted-key reduction (AC-18) produces byte-identical results regardless
// of the order demand was inserted in, repeated multiple times.
func TestTraffic_Determinism_SortedKeysByteIdentical(t *testing.T) {
	buildAndRead := func(order []uint64) float64 {
		api := New()
		for _, id := range order {
			_ = api.AddDemand(id, int64(id)*7+3)
		}
		h, _ := api.CommuteHours(1, "det-test")
		return h
	}

	ascending := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	descending := []uint64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	want := buildAndRead(ascending)
	if want == 0 {
		t.Fatal("test fixture produced zero demand -- check the fixture, not the assertion below")
	}
	for i := 0; i < 5; i++ {
		got := buildAndRead(descending)
		if got != want {
			t.Fatalf("non-deterministic across insertion order/repeat %d: want %v got %v", i, want, got)
		}
	}
}

// TestTraffic_CoarseFallback_NoNetworkLoaded proves the coarse layer's
// query surfaces still answer deterministically from the base config
// alone when no Stage 1 network (no AddNode/AddLink/AddLinkVolume) has
// been loaded at all.
func TestTraffic_CoarseFallback_NoNetworkLoaded(t *testing.T) {
	api := New()

	h1, err := api.CommuteHours(42, "fallback-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h2, err := api.CommuteHours(42, "fallback-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h1 != h2 || h1 != api.cfg.BaseCommuteHours {
		t.Errorf("expected byte-identical coarse fallback %v, got %v and %v", api.cfg.BaseCommuteHours, h1, h2)
	}

	m, err := api.AccessMinutes(42, leisure.Category(1), "fallback-access")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != api.cfg.BaseAccessMinutes {
		t.Errorf("expected coarse fallback access minutes %v, got %v", api.cfg.BaseAccessMinutes, m)
	}
}
