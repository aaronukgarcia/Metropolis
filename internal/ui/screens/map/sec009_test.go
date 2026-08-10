package mapscreen

import (
	"encoding/json"
	"math"
	"runtime"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// SEC-009: MapScreen.applyFullLocked must never allocate a grid sized
// directly, unboundedly, from a wire-supplied Extent. These tests assert
// the PROPERTY (no giant allocation attempted; last-known-good state
// preserved; ordinary patches still apply), not an exact error message,
// per this item's brief.

// TestSEC009_HugeExtent_RejectedWithoutAllocating proves a full patch
// whose Extent would require a many-terabyte allocation is rejected
// before make() ever runs. Measured via runtime.MemStats' cumulative
// TotalAlloc rather than a mock allocator (this package has none to
// inject) -- if applyFullLocked's make([]cellData, w*h) ever ran for
// these dimensions, the process would OOM/panic long before this
// assertion could even execute, so "the test function returned at all,
// promptly, without a panic" is itself strong evidence, and the
// TotalAlloc delta bound below additionally proves no huge allocation
// merely SUCCEEDED without crashing (e.g. on a machine with enough
// overcommitted virtual memory to satisfy even a huge Go slice make).
func TestSEC009_HugeExtent_RejectedWithoutAllocating(t *testing.T) {
	m := NewMapScreen("corr-sec009-huge", widgets.DefaultPalette)

	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: 1_000_000, Height: 1_000_000}, // 1e12 cells if ever allocated
	})
	if err != nil {
		t.Fatalf("marshal huge-extent patch: %v", err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	m.ApplyPatch(raw)

	runtime.ReadMemStats(&after)

	// A genuine attempt to allocate 1e12 cellData (64 bytes each, per
	// zzsizecheck) would ask for ~64 TB -- many orders of magnitude past
	// any plausible legitimate allocation this test itself performs.
	// Bounding the delta at 64 MiB is generous headroom for the test's
	// own bookkeeping while being utterly incompatible with the attack
	// having proceeded even partway.
	const maxPlausibleTestOverheadBytes = 64 * 1024 * 1024
	if delta := after.TotalAlloc - before.TotalAlloc; delta > maxPlausibleTestOverheadBytes {
		t.Fatalf("ApplyPatch(huge extent) allocated %d bytes, want < %d -- the attack allocation was not rejected before make()", delta, maxPlausibleTestOverheadBytes)
	}

	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("state changed after a rejected huge-extent patch: %+v, want Found=false (no snapshot was ever applied)", res)
	}
}

// TestSEC009_HugeExtent_PreservesLastKnownGoodState proves requirement 2
// (reject, never clamp) the other way round: a legitimate snapshot
// already applied, then a hostile huge-extent patch arrives -- the
// PREVIOUS grid must survive untouched, not be silently truncated to
// whatever huge (but rejected) extent was requested.
func TestSEC009_HugeExtent_PreservesLastKnownGoodState(t *testing.T) {
	m := NewMapScreen("corr-sec009-preserve", widgets.DefaultPalette)

	goodRaw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: 2, Height: 2},
		Cells:         []wireCell{{X: 0, Y: 0, Terrain: "shore"}, {X: 1, Y: 1, Terrain: "shelf"}},
	})
	if err != nil {
		t.Fatalf("marshal good patch: %v", err)
	}
	m.ApplyPatch(goodRaw)

	before := m.Inspect(0, 0)
	if !before.Found || before.Terrain != "shore" {
		t.Fatalf("setup: Inspect(0,0) = %+v, want Found=true Terrain=shore", before)
	}

	hugeRaw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: 5_000_000, Height: 5_000_000},
	})
	if err != nil {
		t.Fatalf("marshal huge-extent patch: %v", err)
	}
	m.ApplyPatch(hugeRaw)

	after := m.Inspect(0, 0)
	if after != before {
		t.Fatalf("Inspect(0,0) after rejected huge-extent patch = %+v, want unchanged %+v (last-known-good state)", after, before)
	}
	// The rejected patch's own (huge) extent must not have become the
	// screen's new width/height either.
	if outOfOldRange := m.Inspect(3, 3); outOfOldRange.Found {
		t.Fatalf("Inspect(3,3) = %+v, want Found=false -- the grid must still be the OLD 2x2 extent, not the rejected 5,000,000x5,000,000 one", outOfOldRange)
	}
}

// TestSEC009_OverflowExtent_RejectedNotPanicked proves requirement 3
// (guard the multiplication itself, not only the product): a w,h pair
// whose product overflows a 64-bit int if ever multiplied together
// before being bounds-checked. maxGridSide (limits.go) must reject on
// the per-dimension check alone, before w*h is ever computed against the
// attacker's numbers.
func TestSEC009_OverflowExtent_RejectedNotPanicked(t *testing.T) {
	m := NewMapScreen("corr-sec009-overflow", widgets.DefaultPalette)

	// Individually each dimension already exceeds int32 range and their
	// product vastly exceeds int64 range (math.MaxInt64 ~= 9.2e18;
	// 3e9*3e9 = 9e18, close to overflowing on its own, and would
	// definitely overflow int on a 32-bit build).
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: 3_000_000_000, Height: 3_000_000_000},
	})
	if err != nil {
		t.Fatalf("marshal overflow-extent patch: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ApplyPatch panicked on an overflow-shaped extent: %v", r)
		}
	}()
	m.ApplyPatch(raw)

	if res := m.Inspect(0, 0); res.Found {
		t.Fatalf("state changed after a rejected overflow-extent patch: %+v", res)
	}
}

// TestSEC009_MaxGridSide_IsWellBelowOverflow is a sanity check on
// limits.go's derivation itself: maxGridSide*maxGridSide (the largest
// product applyFullLocked's per-dimension check can ever let through to
// the multiplication) must not itself be anywhere near overflowing int,
// and must stay comfortably inside maxGridCells (its own definition).
func TestSEC009_MaxGridSide_IsWellBelowOverflow(t *testing.T) {
	if maxGridSide <= 0 {
		t.Fatalf("maxGridSide = %d, want > 0", maxGridSide)
	}
	product := maxGridSide * maxGridSide
	if product > maxGridCells {
		t.Fatalf("maxGridSide^2 = %d exceeds maxGridCells = %d -- limits.go's derivation is inconsistent", product, maxGridCells)
	}
	// Comfortably below math.MaxInt64/2 -- nowhere near the point where
	// multiplying two maxGridSide-bounded factors together could overflow.
	if float64(product) > math.MaxInt64/2 {
		t.Fatalf("maxGridSide^2 = %d is uncomfortably close to overflow, want well below math.MaxInt64/2", product)
	}
}

// TestSEC009_OrdinaryFolkestone64Patch_StillApplies proves the fix does
// not regress the legitimate case: Folkestone-64's real 64x64 extent
// (this package's actual Sprint 1 fixture size) is far under
// maxGridSide/maxGridCells and must apply exactly as before.
func TestSEC009_OrdinaryFolkestone64Patch_StillApplies(t *testing.T) {
	m := NewMapScreen("corr-sec009-ordinary", widgets.DefaultPalette)

	const fixtureSide = 64 // internal/engine/stub.FixtureWidth/FixtureHeight -- mirrored literal, see map_test.go's stub-backed fixture tests for the load-bearing full-fixture coverage
	cells := make([]wireCell, 0, fixtureSide*fixtureSide)
	for y := 0; y < fixtureSide; y++ {
		for x := 0; x < fixtureSide; x++ {
			cells = append(cells, wireCell{X: x, Y: y, Terrain: "shore"})
		}
	}
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Extent:        wireExtent{Width: fixtureSide, Height: fixtureSide},
		Cells:         cells,
	})
	if err != nil {
		t.Fatalf("marshal ordinary fixture-shaped patch: %v", err)
	}

	m.ApplyPatch(raw)

	res := m.Inspect(10, 10)
	if !res.Found || res.Terrain != "shore" {
		t.Fatalf("Inspect(10,10) after an ordinary 64x64 patch = %+v, want Found=true Terrain=shore", res)
	}
	// A second, larger-but-still-ordinary cell check: the corner
	// diagonally opposite (10,10), still well inside the 64x64 fixture
	// extent, proving the whole grid was allocated and populated, not
	// just a partial/truncated one.
	if corner := m.Inspect(fixtureSide-1, fixtureSide-1); !corner.Found {
		t.Fatalf("Inspect(%d,%d) (fixture corner) after an ordinary 64x64 patch: Found=false, want true", fixtureSide-1, fixtureSide-1)
	}
}
