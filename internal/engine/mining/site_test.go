package mining

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// This test file is the engine.mining (MOD-046) extraction-siting regression
// suite: geology-gated siting (AC-2), deep-coal spoil tip + subsidence (AC-3),
// exhaustion + reclamation (AC-9), closure workforce (AC-10), and the
// registry-sourced rejections (AC-11).

// --- AC-2: geology-gated siting of the six extraction types -----------------

func TestSiteExtractionGeologyGate(t *testing.T) {
	w := newWorld(t)
	fx := fixtures(t)
	coal := fx.coalTiles[0]
	nonCoal := fx.chalkTiles[0]
	prospectTiles(t, w, coal, nonCoal)
	b := blightAPI(t, w)
	cell := world.CellLocal{Row: 50, Col: 50}

	// Deep coal on a revealed coal-measures (eastern) tile succeeds.
	if err := b.SiteExtraction(SiteCommand{
		Key: "mine-coal", TypeKey: "deep_coal", Tile: coal, Local: cell, CorrelationID: cid(),
	}); err != nil {
		t.Fatalf("deep_coal on a coal tile rejected: %v", err)
	}
	// Deep coal on a non-coal tile is rejected (the geology gate).
	if err := b.SiteExtraction(SiteCommand{
		Key: "mine-west", TypeKey: "deep_coal", Tile: nonCoal, Local: cell, CorrelationID: cid(),
	}); err != nil {
		assertErrCode(t, err, ErrSitingNotPermitted)
	} else {
		t.Fatal("deep_coal on a non-coal tile was accepted — the geology gate did not reject")
	}

	// The six types carry distinct output accessors: the sited deep coal
	// resolves to its own commodity + non-zero rate, not a shared default.
	info, err := b.SiteInfo("mine-coal", cid())
	if err != nil {
		t.Fatal(err)
	}
	if info.OutputCommodity != "coal" {
		t.Errorf("deep coal output commodity = %q, want coal (distinct output accessor)", info.OutputCommodity)
	}
	if info.OutputRate <= 0 {
		t.Errorf("deep coal output rate = %v, want > 0", info.OutputRate)
	}
}

// --- AC-3: deep-coal spoil tip footprint + subsidence-risk radius ----------

func TestSubsidenceRiskFlag(t *testing.T) {
	w := newWorld(t)
	fx := fixtures(t)
	coal := fx.coalTiles[0]
	prospectTiles(t, w, coal)
	b := blightAPI(t, w)
	cell := world.CellLocal{Row: 50, Col: 50}
	if err := b.SiteExtraction(SiteCommand{
		Key: "mine", TypeKey: "deep_coal", Tile: coal, Local: cell, CorrelationID: cid(),
	}); err != nil {
		t.Fatal(err)
	}

	info, err := b.SiteInfo("mine", cid())
	if err != nil {
		t.Fatal(err)
	}
	if info.SubsidenceRadiusM <= 0 {
		t.Fatalf("deep coal subsidence radius = %v, want > 0 (distinct risk accessor)", info.SubsidenceRadiusM)
	}
	if info.SpoilTipFootprint <= 0 {
		t.Fatalf("deep coal spoil tip footprint = %d, want > 0", info.SpoilTipFootprint)
	}

	// A cell within the radius carries the risk flag; one just outside does not.
	inside, err := b.SubsidenceRiskAt(coal, cell, cid())
	if err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatal("a cell at the mine (inside the radius) does not carry the subsidence flag")
	}
	outside, err := b.SubsidenceRiskAt(coal, world.CellLocal{Row: cell.Row + 31, Col: cell.Col}, cid())
	if err != nil {
		t.Fatal(err)
	}
	if outside {
		t.Fatal("a cell 310m away (outside the 300m radius) carries the subsidence flag")
	}
}

// --- AC-9: exhaustion and reclamation ---------------------------------------

func TestReclaimLakePark(t *testing.T) {
	w := newWorld(t)
	fx := fixtures(t)
	chalk := fx.chalkTiles[0]
	prospectTiles(t, w, chalk)
	b := blightAPI(t, w)
	cell := world.CellLocal{Row: 50, Col: 50}
	if err := b.SiteExtraction(SiteCommand{
		Key: "quarry", TypeKey: "chalk", Tile: chalk, Local: cell, CorrelationID: cid(),
	}); err != nil {
		t.Fatal(err)
	}

	info, err := b.SiteInfo("quarry", cid())
	if err != nil {
		t.Fatal(err)
	}
	if info.Capacity <= 0 {
		t.Fatalf("chalk capacity = %v, want > 0 (data-sourced)", info.Capacity)
	}
	// Exhaust the site: one big extraction reaches capacity.
	got, err := b.Extract("quarry", info.Capacity, cid())
	if err != nil {
		t.Fatal(err)
	}
	if got != info.Capacity {
		t.Fatalf("extraction returned %v, want full capacity %v", got, info.Capacity)
	}
	// Further extraction is rejected.
	if _, err := b.Extract("quarry", 1, cid()); err != nil {
		assertErrCode(t, err, ErrSiteExhausted)
	} else {
		t.Fatal("extraction on an exhausted site was accepted")
	}

	// Reclaim to a country park: success, and the site is no longer a
	// registered blighting object.
	if err := b.Reclaim("quarry", ReclaimPark, cid()); err != nil {
		t.Fatal(err)
	}
	info, err = b.SiteInfo("quarry", cid())
	if err != nil {
		t.Fatal(err)
	}
	if info.Reclaimed == nil || *info.Reclaimed != ReclaimPark {
		t.Fatalf("site reclaimed option = %v, want park", info.Reclaimed)
	}
	// A reclaimed lake/park is no longer a blighting object: EffectAt at the
	// site's own cell reports zero heard and seen.
	eff := effectAt(t, b, cell, 0)
	if eff.Heard != 0 || eff.Seen != 0 {
		t.Fatalf("reclaimed park still blights its cell (heard=%v seen=%v) — must be deregistered", eff.Heard, eff.Seen)
	}
}

func TestDoubleReclaim(t *testing.T) {
	w := newWorld(t)
	fx := fixtures(t)
	chalk := fx.chalkTiles[0]
	prospectTiles(t, w, chalk)
	b := blightAPI(t, w)
	if err := b.SiteExtraction(SiteCommand{
		Key: "quarry", TypeKey: "chalk", Tile: chalk, Local: world.CellLocal{Row: 50, Col: 50}, CorrelationID: cid(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Reclaim("quarry", ReclaimLake, cid()); err != nil {
		t.Fatal(err)
	}
	// A second reclaim of the same site is rejected, and the registry code is
	// the claimed one — not merely a matching-named test function (AC-11 GR#7).
	if err := b.Reclaim("quarry", ReclaimPark, cid()); err != nil {
		assertErrCode(t, err, ErrAlreadyReclaimed)
	} else {
		t.Fatal("double reclaim was accepted")
	}
	// And the site's reclaimed option was not mutated by the rejected second
	// reclaim (still lake, not park).
	info, err := b.SiteInfo("quarry", cid())
	if err != nil {
		t.Fatal(err)
	}
	if info.Reclaimed == nil || *info.Reclaimed != ReclaimLake {
		t.Fatalf("rejected double-reclaim mutated the site (reclaimed=%v, want lake)", info.Reclaimed)
	}
}

// --- AC-10: deep-mine closure exposes the workforce-at-risk figure ---------

func TestDeepMineClosureWorkforceAtRisk(t *testing.T) {
	w := newWorld(t)
	fx := fixtures(t)
	coal := fx.coalTiles[0]
	prospectTiles(t, w, coal)
	b := blightAPI(t, w)
	if err := b.SiteExtraction(SiteCommand{
		Key: "mine", TypeKey: "deep_coal", Tile: coal, Local: world.CellLocal{Row: 50, Col: 50}, CorrelationID: cid(),
	}); err != nil {
		t.Fatal(err)
	}

	// Before closure: a nonzero figure tied to THIS mine (the mining-jobs
	// headcount), not a generic layoff constant.
	atRisk, err := b.WorkforceAtRisk("mine", cid())
	if err != nil {
		t.Fatal(err)
	}
	info, err := b.SiteInfo("mine", cid())
	if err != nil {
		t.Fatal(err)
	}
	if atRisk <= 0 {
		t.Fatalf("workforce at risk = %d, want > 0 (the §32 mining-jobs headcount)", atRisk)
	}
	if atRisk != info.Jobs {
		t.Fatalf("workforce at risk %d != the site's jobs %d — must be tied to the specific mine", atRisk, info.Jobs)
	}

	// Closure triggers, and the figure stays queryable afterward.
	if err := b.CloseSite("mine", cid()); err != nil {
		t.Fatal(err)
	}
	after, err := b.WorkforceAtRisk("mine", cid())
	if err != nil {
		t.Fatal(err)
	}
	if after != atRisk {
		t.Fatalf("workforce at risk changed across closure (%d -> %d)", atRisk, after)
	}
	// A closed site no longer produces.
	if _, err := b.Extract("mine", 1, cid()); err != nil {
		assertErrCode(t, err, ErrSiteExhausted)
	} else {
		t.Fatal("extraction on a closed deep mine was accepted")
	}
}

// --- AC-11: registry-sourced rejections, no side-effect mutation -----------

func TestUngeologyGatedSiting(t *testing.T) {
	w := newWorld(t)
	b := blightAPI(t, w)
	// An unprospected tile is rejected (revealed geology is the siting gate).
	tile := world.TileCoord{X: 10, Y: 10}
	err := b.SiteExtraction(SiteCommand{
		Key: "q", TypeKey: "chalk", Tile: tile, Local: world.CellLocal{Row: 5, Col: 5}, CorrelationID: cid(),
	})
	assertErrCode(t, err, ErrSitingNotPermitted)

	// And no site / blighting-object record was created as a side effect.
	if _, err := b.SiteInfo("q", cid()); err != nil {
		assertErrCode(t, err, ErrUnknownBlightKey)
	} else {
		t.Fatal("a rejected siting left a site record behind")
	}
	if _, err := b.WorkforceAtRisk("q", cid()); err != nil {
		assertErrCode(t, err, ErrUnknownBlightKey)
	} else {
		t.Fatal("a rejected siting left a blighting-object record behind")
	}
}

func TestBlightProfileInvalid(t *testing.T) {
	w := newWorld(t)
	b := blightAPI(t, w)

	// Negative noise radius.
	if err := b.RegisterBlightingObject("neg", BlightLow, -5); err != nil {
		assertErrCode(t, err, ErrBlightProfileInvalid)
	} else {
		t.Fatal("negative contour radius was accepted")
	}
	// Out-of-extent location.
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "oob", Class: BlightLow, Tile: world.TileCoord{X: -1, Y: 5},
		Local: world.CellLocal{Row: 0, Col: 0}, NoiseRadiusM: 100, VisualHeightM: 5, VisualMagnitude: 0.2,
	}); err != nil {
		assertErrCode(t, err, ErrBlightProfileInvalid)
	} else {
		t.Fatal("out-of-extent location was accepted")
	}
	// Negative visual height.
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "negheight", Class: BlightLow, Tile: world.TileCoord{X: 5, Y: 5},
		Local: world.CellLocal{Row: 0, Col: 0}, NoiseRadiusM: 100, VisualHeightM: -1, VisualMagnitude: 0.2,
	}); err != nil {
		assertErrCode(t, err, ErrBlightProfileInvalid)
	} else {
		t.Fatal("negative visual height was accepted")
	}

	// None of the rejected registrations created an object: a home cell far
	// from everything reports zero effect.
	eff := effectAt(t, b, world.CellLocal{Row: 100, Col: 100}, 0)
	if eff.Heard != 0 || eff.Seen != 0 {
		t.Fatalf("rejected registrations still contribute blight (heard=%v seen=%v)", eff.Heard, eff.Seen)
	}
}
