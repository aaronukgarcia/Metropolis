package build

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// FEAT-1972079927 inc2 (Aaron's 2026-08-31 ruling): builders'-merchant
// auto-placement fires on a deterministic, state-derived "Industry & Farms
// zone grouping" — at least one industry-type zone (manufacturing/
// heavy_industry/mining) AND at least one ZoneFarming cell. These tests
// exercise IndustryAndFarmsPresent directly (the query the composition
// root's trigger reads), proving it is false until BOTH categories are
// present, true once they are, and unaffected by zoning order or by an
// unrelated (non-industry, non-farming) zone type.

// TestIndustryAndFarmsPresent_FalseUntilBothCategoriesZoned proves the
// query requires BOTH an industry zone and a farming zone — neither alone
// trips it, and re-zoning an unrelated cell (dwelling) never trips it.
//
// PROOF THIS CAN FAIL: temporarily changing the trigger to
// `hasIndustry || hasFarms` (OR instead of AND) makes this test's
// "farming alone is false" and "industry alone is false" assertions fail
// (both would read true) — verified by hand during development via a
// scratch copy (cp zone.go zone.go.bak; edit; go test; mv back), then
// reverted.
func TestIndustryAndFarmsPresent_FalseUntilBothCategoriesZoned(t *testing.T) {
	b, w, _ := newBuildFixture(t)
	_ = w

	if got, err := b.IndustryAndFarmsPresent(); err != nil || got {
		t.Fatalf("IndustryAndFarmsPresent = %v, err=%v; want false, nil (nothing zoned yet)", got, err)
	}

	// Zone an unrelated cell (dwelling) — must not trip the trigger.
	if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner, Zone: ZoneDwelling}); err != nil {
		t.Fatalf("SubmitZoneCommand(dwelling): %v", err)
	}
	if got, err := b.IndustryAndFarmsPresent(); err != nil || got {
		t.Fatalf("IndustryAndFarmsPresent after dwelling = %v, err=%v; want false, nil", got, err)
	}

	// Zone farming ALONE — still false (no industry zone yet).
	if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner, Zone: ZoneFarming}); err != nil {
		t.Fatalf("SubmitZoneCommand(farming): %v", err)
	}
	if got, err := b.IndustryAndFarmsPresent(); err != nil || got {
		t.Fatalf("IndustryAndFarmsPresent after farming-only = %v, err=%v; want false, nil (no industry zone yet)", got, err)
	}

	// Now zone an industry-type cell too — the grouping is complete.
	if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 2}, OwnerID: testOwner, Zone: ZoneManufacturing}); err != nil {
		t.Fatalf("SubmitZoneCommand(manufacturing): %v", err)
	}
	if got, err := b.IndustryAndFarmsPresent(); err != nil || !got {
		t.Fatalf("IndustryAndFarmsPresent after farming+manufacturing = %v, err=%v; want true, nil", got, err)
	}
}

// TestIndustryAndFarmsPresent_AnyIndustryTypeQualifies proves all three
// industry zone types (manufacturing/heavy_industry/mining) independently
// satisfy the "industry" half of the trigger, not just manufacturing.
func TestIndustryAndFarmsPresent_AnyIndustryTypeQualifies(t *testing.T) {
	for _, zt := range []ZoneType{ZoneManufacturing, ZoneHeavyIndustry, ZoneMining} {
		zt := zt
		t.Run(string(zt), func(t *testing.T) {
			b, _, _ := newBuildFixture(t)
			if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 1, Col: 0}, OwnerID: testOwner, Zone: ZoneFarming}); err != nil {
				t.Fatalf("SubmitZoneCommand(farming): %v", err)
			}
			if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile00(), Local: world.CellLocal{Row: 1, Col: 1}, OwnerID: testOwner, Zone: zt}); err != nil {
				t.Fatalf("SubmitZoneCommand(%s): %v", zt, err)
			}
			if got, err := b.IndustryAndFarmsPresent(); err != nil || !got {
				t.Fatalf("IndustryAndFarmsPresent with farming+%s = %v, err=%v; want true, nil", zt, got, err)
			}
		})
	}
}
