package refuse

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func newTestAPI(t *testing.T) *RefuseAPI {
	t.Helper()
	api, err := LoadDefault("refuse-test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return api
}

// recordingWellbeing is the test fake for the engine.wellbeing seam: it
// records every ReportPollutionExposure call so tests can assert the
// consequence crossed the registered interface rather than a refuse-owned
// health number.
type recordingWellbeing struct {
	mu    sync.Mutex
	calls map[string][]float64
}

func (w *recordingWellbeing) ReportPollutionExposure(cellID string, exposure float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.calls == nil {
		w.calls = map[string][]float64{}
	}
	w.calls[cellID] = append(w.calls[cellID], exposure)
	return nil
}

func (w *recordingWellbeing) exposures(cellID string) []float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]float64(nil), w.calls[cellID]...)
}

func newWiredAPI(t *testing.T) (*RefuseAPI, *recordingWellbeing) {
	t.Helper()
	api := newTestAPI(t)
	lg, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg, sv, w); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatalf("SetTrucks: %v", err)
	}
	return api, w
}

func assertErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s error, got nil", code)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected a registry-sourced *errs.E, got %T: %v", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected code %s, got %s", code, e.Code)
	}
}

// AC-2: three typed bin stocks with distinct documented capacities, and
// identical-driver generation into different-capacity stocks.
func TestBinTypeCapacityByLandUse(t *testing.T) {
	api := newTestAPI(t)
	if err := api.RegisterCell("res", LandUseResidential, "High Street"); err != nil {
		t.Fatalf("RegisterCell(residential): %v", err)
	}
	if err := api.RegisterCell("ind", LandUseIndustrial, "Mill Road"); err != nil {
		t.Fatalf("RegisterCell(industrial): %v", err)
	}

	res, err := api.BinStock("res")
	if err != nil {
		t.Fatalf("BinStock(res): %v", err)
	}
	ind, err := api.BinStock("ind")
	if err != nil {
		t.Fatalf("BinStock(ind): %v", err)
	}

	if res.Capacity <= 0 || ind.Capacity <= 0 {
		t.Fatalf("capacities must be positive, got res=%d ind=%d", res.Capacity, ind.Capacity)
	}
	if res.Capacity == ind.Capacity {
		t.Fatalf("residential wheelie and industrial skip must have distinct capacities, both %d", res.Capacity)
	}
	if res.Capacity >= ind.Capacity {
		t.Fatalf("residential wheelie (%d) must be smaller than industrial skip (%d)", res.Capacity, ind.Capacity)
	}

	// Identical driver: both cells generate, but into different capacities.
	if err := api.Generate("res", 100); err != nil {
		t.Fatalf("Generate(res): %v", err)
	}
	if err := api.Generate("ind", 100); err != nil {
		t.Fatalf("Generate(ind): %v", err)
	}
	resAfter, _ := api.BinStock("res")
	indAfter, _ := api.BinStock("ind")
	if resAfter.Capacity >= indAfter.Capacity {
		t.Fatalf("capacities still not distinct after generation: res=%d ind=%d", resAfter.Capacity, indAfter.Capacity)
	}
}

// AC-3: three named stream accessors, and contamination lowers only the
// recycling stream's resale value.
func TestStreamsAndContamination(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterCell("c1", LandUseResidential, "Stream Street"); err != nil {
		t.Fatalf("RegisterCell: %v", err)
	}
	if err := api.Generate("c1", 1000); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	g, _ := api.GeneralLevel("c1")
	rc, _ := api.RecyclingLevel("c1")
	fd, _ := api.FoodLevel("c1")
	if g <= 0 || rc <= 0 || fd <= 0 {
		t.Fatalf("all three streams should be non-empty, got general=%d recycling=%d food=%d", g, rc, fd)
	}
	// The three named accessors are per-stream views of the bin stock's
	// in-bin levels.
	bs, _ := api.BinStock("c1")
	if g != bs.General || rc != bs.Recycling || fd != bs.Food {
		t.Fatalf("stream accessors must match the bin stock: general=%d/%d recycling=%d/%d food=%d/%d",
			g, bs.General, rc, bs.Recycling, fd, bs.Food)
	}
	// With nothing collected yet, generated == uncollected (in-bin + overflow)
	// for every stream.
	for _, s := range streamOrder {
		gen := must(api.TonnesGenerated(s))
		uncol := must(api.TonnesUncollected(s))
		if gen != uncol {
			t.Fatalf("stream %s: generated %d != uncollected %d before any collection", s, gen, uncol)
		}
	}

	// Collect the recycling stream via a round so resale value is nonzero,
	// and process the general/food streams so their collected tonnage is
	// nonzero too (so the "unaffected" assertion is not trivially 0==0).
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if _, err := api.ProcessDisposal("L1"); err != nil {
		t.Fatalf("ProcessDisposal(L1): %v", err)
	}
	if _, err := api.ProcessDisposal("C1"); err != nil {
		t.Fatalf("ProcessDisposal(C1): %v", err)
	}

	colGenBefore, _ := api.TonnesCollected(StreamGeneral)
	colFoodBefore, _ := api.TonnesCollected(StreamFood)
	if colGenBefore == 0 || colFoodBefore == 0 {
		t.Fatalf("precondition: general/food collected must be nonzero, got %d/%d", colGenBefore, colFoodBefore)
	}

	if err := api.SetContamination(0.0); err != nil {
		t.Fatal(err)
	}
	v0 := api.RecyclingResaleValue()
	if err := api.SetContamination(0.5); err != nil {
		t.Fatal(err)
	}
	v1 := api.RecyclingResaleValue()
	if v1 >= v0 {
		t.Fatalf("raising contamination must lower recycling resale, got %d -> %d", v0, v1)
	}

	// General and food streams are structurally unaffected by contamination:
	// their collected tonnage is identical before/after the contamination
	// change (only the recycling stream's value depends on contamination).
	if err := api.SetContamination(0.9); err != nil {
		t.Fatal(err)
	}
	colGenAfter, _ := api.TonnesCollected(StreamGeneral)
	colFoodAfter, _ := api.TonnesCollected(StreamFood)
	if colGenAfter != colGenBefore || colFoodAfter != colFoodBefore {
		t.Fatalf("contamination must not move general/food collected: general %d->%d, food %d->%d",
			colGenBefore, colGenAfter, colFoodBefore, colFoodAfter)
	}
}

func must(v int64, _ error) int64 { return v }

// AC-4: refuse rounds are engine.logistics movements — a saturated
// throughput produces a next-tick (in-transit) queue, not same-tick
// teleportation.
func TestRoundMovementThroughLogistics(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Queue Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(100); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	// Generate far more than engine.logistics' waste throughput can move in
	// one tick, so the movement is saturated (gridlock).
	if err := api.Generate("c1", 100_000); err != nil {
		t.Fatal(err)
	}
	res, err := api.RunRound("r1")
	if err != nil {
		t.Fatalf("RunRound: %v", err)
	}
	if res.DeliveredGeneral <= 0 {
		t.Fatalf("expected some delivery, got %d", res.DeliveredGeneral)
	}
	if res.ShortfallGeneral <= 0 {
		t.Fatalf("expected a throughput shortfall (saturated movement), got %d", res.ShortfallGeneral)
	}
	if res.DeliveredGeneral+res.ShortfallGeneral != res.CollectedGeneral {
		t.Fatalf("delivered+shortfall must equal collected: %d+%d != %d",
			res.DeliveredGeneral, res.ShortfallGeneral, res.CollectedGeneral)
	}
	// The shortfall queues for the next tick — it is in-transit, not lost.
	inTransit, _ := api.TonnesInTransit(StreamGeneral)
	if inTransit != res.ShortfallGeneral {
		t.Fatalf("shortfall should queue as in-transit tonnage: transit=%d shortfall=%d", inTransit, res.ShortfallGeneral)
	}
}

// AC-5: auto-optimisation is the default and a player override takes
// precedence until cleared.
func TestRoundOverridePersists(t *testing.T) {
	api := newTestAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}

	auto, err := api.AutoOptimise("r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(auto) != 3 || auto[0] != "a" || auto[1] != "b" || auto[2] != "c" {
		t.Fatalf("auto-optimiser should sort the route ascending, got %v", auto)
	}

	override := []string{"c", "a", "b"}
	if err := api.OverrideRound("r1", override); err != nil {
		t.Fatal(err)
	}
	rd, _ := api.Round("r1")
	if !rd.Overridden {
		t.Fatal("round should be marked overridden")
	}
	if !sameRoute(rd.Route, override) {
		t.Fatalf("override route should be %v, got %v", override, rd.Route)
	}

	// The override persists across the next optimisation pass.
	if _, err := api.AutoOptimise("r1"); err != nil {
		t.Fatal(err)
	}
	rd, _ = api.Round("r1")
	if !sameRoute(rd.Route, override) {
		t.Fatalf("override must persist across AutoOptimise, got %v", rd.Route)
	}

	// Clearing restores the auto-optimised route.
	if err := api.ClearOverride("r1"); err != nil {
		t.Fatal(err)
	}
	rd, _ = api.Round("r1")
	if rd.Overridden {
		t.Fatal("override should be cleared")
	}
	if !sameRoute(rd.Route, []string{"a", "b", "c"}) {
		t.Fatalf("cleared route should be auto-optimised, got %v", rd.Route)
	}
}

func sameRoute(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AC-6: the four documented miss causes are each independently
// constructible and the overflow state records which cause applied.
func TestMissedCollectionCauses(t *testing.T) {
	t.Run("strike", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Strike Street"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 10_000); err != nil {
			t.Fatal(err)
		}
		if err := api.SetStrike("d1", true); err != nil {
			t.Fatal(err)
		}
		if err := api.SetTrucks(100); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Missed || res.Cause == nil || *res.Cause != MissStrike {
			t.Fatalf("expected a strike miss, got %+v", res)
		}
		bs, _ := api.BinStock("c1")
		if bs.MissCause == nil || *bs.MissCause != MissStrike {
			t.Fatalf("overflow state should record the strike cause, got %v", bs.MissCause)
		}
	})

	t.Run("depot-underfunding", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Underfunded Way"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 10_000); err != nil {
			t.Fatal(err)
		}
		if err := api.SetTrucks(100); err != nil {
			t.Fatal(err)
		}
		if err := api.SetFunding(0.2); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Missed || res.Cause == nil || *res.Cause != MissDepotUnderfunding {
			t.Fatalf("expected a depot-underfunding miss, got %+v", res)
		}
		bs, _ := api.BinStock("c1")
		if bs.MissCause == nil || *bs.MissCause != MissDepotUnderfunding {
			t.Fatalf("overflow state should record the underfunding cause, got %v", bs.MissCause)
		}
	})

	t.Run("truck-shortage", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "No Trucks Lane"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 10_000); err != nil {
			t.Fatal(err)
		}
		if err := api.SetTrucks(0); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Missed || res.Cause == nil || *res.Cause != MissTruckShortage {
			t.Fatalf("expected a truck-shortage miss, got %+v", res)
		}
		bs, _ := api.BinStock("c1")
		if bs.MissCause == nil || *bs.MissCause != MissTruckShortage {
			t.Fatalf("overflow state should record the truck-shortage cause, got %v", bs.MissCause)
		}
	})

	t.Run("gridlock-delay", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
			t.Fatal(err)
		}
		if err := api.SetGeneralSite("L1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Gridlock Ave"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 100_000); err != nil {
			t.Fatal(err)
		}
		if err := api.SetTrucks(100); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Missed || res.Cause == nil || *res.Cause != MissGridlockDelay {
			t.Fatalf("expected a gridlock-delay miss, got %+v", res)
		}
		if res.ShortfallGeneral <= 0 {
			t.Fatalf("gridlock should leave a nonzero shortfall, got %d", res.ShortfallGeneral)
		}
	})
}

// TestSnapshotMissCauseDefensiveCopy is the SEC-138 regression. The failure
// CLASS is: an exported snapshot (BinStock.MissCause, TickerEvent.MissCause)
// copies the internal cellState.missCause POINTER verbatim, so a caller holding
// a snapshot can write *snap.MissCause = ... and mutate the cell's internal
// status field without holding r.mu — contradicting AC-1 (consumers can never
// write a bin field directly) and racing a second holder of the aliased
// pointer (AC-17). Post-fix, every snapshot defensive-copies the value, so the
// write-through mutates only the snapshot and the internal state is untouched.
func TestSnapshotMissCauseDefensiveCopy(t *testing.T) {
	t.Run("BinStock", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Alias Road"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 10); err != nil {
			t.Fatal(err)
		}
		if err := api.SetStrike("d1", true); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := api.RunRound("r1"); err != nil {
			t.Fatal(err)
		}

		bs, err := api.BinStock("c1")
		if err != nil {
			t.Fatal(err)
		}
		if bs.MissCause == nil {
			t.Fatalf("precondition: expected a strike miss cause, got nil")
		}
		if *bs.MissCause != MissStrike {
			t.Fatalf("precondition: expected strike miss cause, got %q", *bs.MissCause)
		}

		// Write through the snapshot's pointer. Post-fix this mutates ONLY the
		// snapshot's private copy, never the cell's internal status field.
		*bs.MissCause = MissCause("TAMPERED")

		again, err := api.BinStock("c1")
		if err != nil {
			t.Fatal(err)
		}
		if again.MissCause == nil {
			t.Fatalf("writing through the BinStock snapshot mutated internal state: got nil, want %q", MissStrike)
		}
		if *again.MissCause != MissStrike {
			t.Fatalf("writing through the BinStock snapshot mutated internal state: got %q, want %q", *again.MissCause, MissStrike)
		}
	})

	t.Run("TickerEvent", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Alias Road"); err != nil {
			t.Fatal(err)
		}
		// Overflow the bin so the cell appears in OverflowTickerEvents.
		if err := api.Generate("c1", 10_000); err != nil {
			t.Fatal(err)
		}
		if err := api.SetStrike("d1", true); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := api.RunRound("r1"); err != nil {
			t.Fatal(err)
		}

		events := api.OverflowTickerEvents()
		if len(events) != 1 {
			t.Fatalf("precondition: expected exactly one ticker event, got %d", len(events))
		}
		if events[0].MissCause == nil {
			t.Fatalf("precondition: expected a strike miss cause, got nil")
		}
		if *events[0].MissCause != MissStrike {
			t.Fatalf("precondition: expected strike miss cause, got %q", *events[0].MissCause)
		}

		*events[0].MissCause = MissCause("TAMPERED")

		again := api.OverflowTickerEvents()
		if len(again) != 1 {
			t.Fatalf("expected exactly one ticker event on re-read, got %d", len(again))
		}
		if again[0].MissCause == nil {
			t.Fatalf("writing through the TickerEvent snapshot mutated internal state: got nil, want %q", MissStrike)
		}
		if *again[0].MissCause != MissStrike {
			t.Fatalf("writing through the TickerEvent snapshot mutated internal state: got %q, want %q", *again[0].MissCause, MissStrike)
		}
	})

	t.Run("nil cause stays nil", func(t *testing.T) {
		api := newTestAPI(t)
		if err := api.RegisterCell("c1", LandUseResidential, "Fresh Road"); err != nil {
			t.Fatal(err)
		}
		bs, err := api.BinStock("c1")
		if err != nil {
			t.Fatal(err)
		}
		if bs.MissCause != nil {
			t.Fatalf("a never-missed cell must report a nil miss cause, got %v", bs.MissCause)
		}
	})
}

// AC-6: a funding read that errors must surface, never be silently skipped
// as "funded". With the refuse service unregistered, FundingLevel errors and
// RunRound propagates the registry-sourced error instead of proceeding.
func TestRunRoundFundingErrorNotSilentlySkipped(t *testing.T) {
	api := newTestAPI(t)
	lg, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}

	// Wire the dependencies directly, deliberately skipping Wire's
	// registerService() so RefuseServiceID is NOT registered — FundingLevel
	// will error on the run.
	api.mu.Lock()
	api.logistics = lg
	api.services = sv
	api.mu.Unlock()

	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Funding Street"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 10); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(100); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	_, err = api.RunRound("r1")
	assertErrCode(t, err, services.ErrServiceNotRegistered)
}

// AC-7: the overflow chain — vermin rises, then land-value and fire-risk
// penalties rise, the wellbeing consequence crosses the registered seam,
// and the ticker names the street.
func TestOverflowChainAndVerminIndex(t *testing.T) {
	api, w := newWiredAPI(t)
	if err := api.RegisterCell("c1", LandUseResidential, "Vermin Street"); err != nil {
		t.Fatal(err)
	}

	if err := api.Generate("c1", 10_000); err != nil {
		t.Fatal(err)
	}
	v1, _ := api.VerminIndex("c1")
	lv1, _ := api.LandValuePenalty("c1")
	fr1, _ := api.FireRiskIncrease("c1")
	exposures1 := w.exposures("c1")
	if len(exposures1) == 0 {
		t.Fatalf("expected a wellbeing PollutionExposure report through the seam")
	}

	// Sustained overflow: a second generate compounds the consequences.
	if err := api.Generate("c1", 10_000); err != nil {
		t.Fatal(err)
	}
	v2, _ := api.VerminIndex("c1")
	lv2, _ := api.LandValuePenalty("c1")
	fr2, _ := api.FireRiskIncrease("c1")
	exposures2 := w.exposures("c1")

	if v2 <= v1 {
		t.Fatalf("vermin index must rise with sustained overflow: %v -> %v", v1, v2)
	}
	if lv2 <= lv1 {
		t.Fatalf("land-value penalty must rise with vermin: %v -> %v", lv1, lv2)
	}
	if fr2 <= fr1 {
		t.Fatalf("fire-risk increase must rise with vermin: %v -> %v", fr1, fr2)
	}
	if exposures2[len(exposures2)-1] <= exposures1[len(exposures1)-1] {
		t.Fatalf("wellbeing pollution exposure must rise: %v -> %v", exposures1, exposures2)
	}

	events := api.OverflowTickerEvents()
	if len(events) == 0 {
		t.Fatalf("expected a street-naming ticker event for the overflowing cell")
	}
	if events[0].Street != "Vermin Street" {
		t.Fatalf("ticker event must name the street, got %q", events[0].Street)
	}
}

// AC-8: landfill fill is permanent, full triggers blight, and cap-and-
// reclaim removes it as a valid disposal target.
func TestLandfillBlightAndReclaim(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterLandfill("L1", 1000, []string{"n1"}); err != nil {
		t.Fatal(err)
	}

	rem, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if rem != 1000 {
		t.Fatalf("initial remaining capacity = %d, want 1000", rem)
	}

	accepted, err := api.RouteGeneralToSite("L1", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if accepted != 1000 {
		t.Fatalf("accepted = %d, want 1000", accepted)
	}
	remAfter, _ := api.RemainingCapacity("L1")
	if remAfter != 0 {
		t.Fatalf("remaining capacity should be 0 after filling, got %d", remAfter)
	}

	// A full landfill blights its surrounding cells.
	blighted, err := api.BlightedCells("L1")
	if err != nil {
		t.Fatal(err)
	}
	if len(blighted) != 1 || blighted[0] != "n1" {
		t.Fatalf("full landfill should blight its neighbour, got %v", blighted)
	}

	// Routing further general waste to a full landfill is rejected.
	if _, err := api.RouteGeneralToSite("L1", 10); err == nil {
		t.Fatal("routing to a full landfill must be rejected")
	} else {
		assertErrCode(t, err, ErrDisposalSiteUnavailable)
	}

	// Cap and reclaim: no longer a valid disposal target, blight clears.
	if err := api.CapAndReclaim("L1"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("L1", 10); err == nil {
		t.Fatal("routing to a reclaimed landfill must be rejected")
	} else {
		assertErrCode(t, err, ErrDisposalSiteUnavailable)
	}
	blighted, _ = api.BlightedCells("L1")
	if len(blighted) != 0 {
		t.Fatalf("reclaimed landfill must not blight, got %v", blighted)
	}
}

// AC-8: Wire re-invocation is idempotent for disposal-site state — a
// re-wire with the SAME logistics instance must not reset an already
// provisioned landfill's fill, so RemainingCapacity is monotone
// non-increasing across re-wires.
func TestWireIdempotentDisposalFill(t *testing.T) {
	api := newTestAPI(t)
	lg, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg, sv, w); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}

	if err := api.RegisterLandfill("L1", 1000, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	rem, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if rem != 0 {
		t.Fatalf("after fill remaining capacity = %d, want 0", rem)
	}

	// Re-Wire with the SAME logistics instance (documented idempotent).
	if err := api.Wire(lg, sv, w); err != nil {
		t.Fatal(err)
	}

	remAfter, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if remAfter != 0 {
		t.Fatalf("re-wire reset the landfill fill: remaining capacity = %d, want 0", remAfter)
	}
	blighted, err := api.BlightedCells("L1")
	if err != nil {
		t.Fatal(err)
	}
	if len(blighted) != 1 || blighted[0] != "n1" {
		t.Fatalf("re-wire cleared the full landfill's blight, got %v", blighted)
	}
}

// AC-9: incineration produces energy at the cost of airshed pollution —
// a different pollution profile from landfill (which has no airshed term).
func TestIncinerationTradeOff(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterIncinerator("I1"); err != nil {
		t.Fatal(err)
	}

	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("I1", 1000); err != nil {
		t.Fatal(err)
	}

	// The landfill path has no airshed term (it is not an incinerator).
	if _, err := api.AirshedPollution("L1"); err == nil {
		t.Fatal("landfill must expose no airshed-pollution term")
	} else {
		assertErrCode(t, err, ErrDisposalSiteUnavailable)
	}
	if _, err := api.EnergyOutput("L1"); err == nil {
		t.Fatal("landfill must expose no energy output")
	} else {
		assertErrCode(t, err, ErrDisposalSiteUnavailable)
	}

	// The incinerator path produces both energy and airshed pollution.
	energy, err := api.EnergyOutput("I1")
	if err != nil {
		t.Fatal(err)
	}
	airshed, err := api.AirshedPollution("I1")
	if err != nil {
		t.Fatal(err)
	}
	if energy <= 0 {
		t.Fatalf("incineration should produce energy, got %d", energy)
	}
	if airshed <= 0 {
		t.Fatalf("incineration should produce airshed pollution, got %v", airshed)
	}
}

// AC-10: food waste converts to a queryable compost output at the
// data-sourced ratio (GR#15).
func TestCompostOutput(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}

	dir, err := data.ResolveDataDir("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	rf, err := data.LoadRefuseFile(dir, "refuse-test")
	if err != nil {
		t.Fatal(err)
	}

	const food = int64(1000)
	accepted, err := api.RouteFoodToCompost("C1", food)
	if err != nil {
		t.Fatal(err)
	}
	if accepted != food {
		t.Fatalf("accepted = %d, want %d", accepted, food)
	}
	got, err := api.CompostOutput("C1")
	if err != nil {
		t.Fatal(err)
	}
	want := int64(math.Floor(float64(food) * rf.Compost.ConversionRatio))
	if got != want {
		t.Fatalf("compost output = %d, want %d (ratio %v)", got, want, rf.Compost.ConversionRatio)
	}
}

// AC-11: the mass-conservation identity holds for every stream after a
// synthetic city with a mix of hits, misses, and processing.
func TestMassConservationIdentity(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("a", LandUseResidential, "A Street"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("b", LandUseResidential, "B Street"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(100); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}

	// Tick 1: generate into both cells, collect successfully, process.
	if err := api.Generate("a", 100); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("b", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}
	assertIdentity(t, api)

	if _, err := api.ProcessDisposal("L1"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ProcessDisposal("C1"); err != nil {
		t.Fatal(err)
	}
	assertIdentity(t, api)

	// Tick 2: generate a large amount, then a missed round (strike) leaves
	// uncollected overflow.
	if err := api.SetStrike("d1", true); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("a", 10_000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r2", "d1", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r2"); err != nil {
		t.Fatal(err)
	}
	assertIdentity(t, api)
}

func assertIdentity(t *testing.T, api *RefuseAPI) {
	t.Helper()
	for _, s := range streamOrder {
		generated, err := api.TonnesGenerated(s)
		if err != nil {
			t.Fatal(err)
		}
		collected, _ := api.TonnesCollected(s)
		uncollected, _ := api.TonnesUncollected(s)
		inTransit, _ := api.TonnesInTransit(s)
		backlog, _ := api.TonnesDisposalBacklog(s)
		rhs := collected + uncollected + inTransit + backlog
		if generated != rhs {
			t.Fatalf("mass-conservation identity broken for stream %s: generated=%d, collected+uncollected+inTransit+backlog=%d+%d+%d+%d=%d",
				s, generated, collected, uncollected, inTransit, backlog, rhs)
		}
	}
}

// AC-11: the direct-route surface (RouteGeneralToSite / RouteFoodToCompost)
// introduces externally-sourced waste, so it must credit `generated` as well
// as `collected` — otherwise zero Generate calls still produce collected
// tonnage and the four-term identity breaks.
func TestDirectRouteMassConservation(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterLandfill("L1", 100_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterIncinerator("I1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}

	// No cells registered, zero Generate calls — the direct-route primitives
	// are the only source of tonnage in the accounting period.
	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("I1", 500); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteFoodToCompost("C1", 300); err != nil {
		t.Fatal(err)
	}

	assertIdentity(t, api)

	gen, _ := api.TonnesGenerated(StreamGeneral)
	col, _ := api.TonnesCollected(StreamGeneral)
	if gen != 1500 || col != 1500 {
		t.Fatalf("general: generated=%d collected=%d, want 1500/1500", gen, col)
	}
	fgen, _ := api.TonnesGenerated(StreamFood)
	fcol, _ := api.TonnesCollected(StreamFood)
	if fgen != 300 || fcol != 300 {
		t.Fatalf("food: generated=%d collected=%d, want 300/300", fgen, fcol)
	}
}

// AC-13: unregistered depot and unknown cell return registry-sourced
// errors and create no zero-value entries.
func TestUnregisteredDepotRejected(t *testing.T) {
	api := newTestAPI(t)
	err := api.ScheduleRound("r1", "ghost-depot", []string{"c1"})
	assertErrCode(t, err, ErrUnknownDepot)

	// No zero-value round was created.
	if _, err := api.Round("r1"); err == nil {
		t.Fatal("a round for an unregistered depot must not be created")
	} else {
		assertErrCode(t, err, ErrInvalidOverride)
	}
}

func TestUnknownCellRejected(t *testing.T) {
	api := newTestAPI(t)
	_, err := api.BinStock("ghost-cell")
	assertErrCode(t, err, ErrUnknownLandUse)
	// No zero-value stock entry was created: a second query still errors.
	if _, err := api.BinStock("ghost-cell"); err == nil {
		t.Fatal("a zero-value bin-stock entry must not be created for an unknown cell")
	} else {
		assertErrCode(t, err, ErrUnknownLandUse)
	}
	if err := api.Generate("ghost-cell", 10); err == nil {
		t.Fatal("generating into an unknown cell must be rejected")
	} else {
		assertErrCode(t, err, ErrUnknownLandUse)
	}
}

// AC-14: invalid override and out-of-range inputs are rejected, never
// silently clamped or ignored.
func TestInvalidOverrideRejected(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Done Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 10); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	// Overriding a completed round is rejected.
	err := api.OverrideRound("r1", []string{"c1"})
	assertErrCode(t, err, ErrInvalidOverride)

	// Overriding an unknown round is rejected.
	err = api.OverrideRound("nope", []string{"c1"})
	assertErrCode(t, err, ErrInvalidOverride)
}

// AC-14: AutoOptimise rejects a completed round, exactly like OverrideRound
// does — a finished round's route is not silently rewritten.
func TestAutoOptimiseCompletedRoundRejected(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Done Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 10); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	_, err := api.AutoOptimise("r1")
	assertErrCode(t, err, ErrInvalidOverride)
}

func TestOutOfRangeInputsRejected(t *testing.T) {
	api, _ := newWiredAPI(t)

	if err := api.SetContamination(1.5); err == nil {
		t.Fatal("over-100% contamination must be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidContamination)
	}
	if err := api.SetContamination(-0.1); err == nil {
		t.Fatal("negative contamination must be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidContamination)
	}
	// No silent clamp: the value is unchanged.
	if api.Contamination() != 0 {
		t.Fatalf("contamination must not be silently clamped, got %v", api.Contamination())
	}

	if err := api.SetFunding(1.5); err == nil {
		t.Fatal("over-100% funding must be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidFunding)
	}
	if err := api.SetFunding(-0.1); err == nil {
		t.Fatal("negative funding must be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidFunding)
	}
}

// AC-15: the module is deterministic — identical command sequences produce
// byte-identical state.
func TestDeterminism(t *testing.T) {
	run := func() *RefuseAPI {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterLandfill("L1", 1_000_000, []string{"n1"}); err != nil {
			t.Fatal(err)
		}
		if err := api.SetGeneralSite("L1"); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"c", "a", "b"} {
			if err := api.RegisterCell(id, LandUseResidential, "Street "+id); err != nil {
				t.Fatal(err)
			}
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c", "a", "b"}); err != nil {
			t.Fatal(err)
		}
		if _, err := api.AutoOptimise("r1"); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c"} {
			if err := api.Generate(id, 500); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := api.RunRound("r1"); err != nil {
			t.Fatal(err)
		}
		if _, err := api.ProcessDisposal("L1"); err != nil {
			t.Fatal(err)
		}
		return api
	}

	a := run()
	b := run()
	assertSameState(t, a, b)
}

func assertSameState(t *testing.T, a, b *RefuseAPI) {
	t.Helper()
	for _, s := range streamOrder {
		ag, _ := a.TonnesGenerated(s)
		bg, _ := b.TonnesGenerated(s)
		ac, _ := a.TonnesCollected(s)
		bc, _ := b.TonnesCollected(s)
		au, _ := a.TonnesUncollected(s)
		bu, _ := b.TonnesUncollected(s)
		at, _ := a.TonnesInTransit(s)
		bt, _ := b.TonnesInTransit(s)
		abk, _ := a.TonnesDisposalBacklog(s)
		bbk, _ := b.TonnesDisposalBacklog(s)
		if ag != bg || ac != bc || au != bu || at != bt || abk != bbk {
			t.Fatalf("stream %s state diverged: gen %d/%d col %d/%d uncol %d/%d transit %d/%d backlog %d/%d",
				s, ag, bg, ac, bc, au, bu, at, bt, abk, bbk)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		as, _ := a.BinStock(id)
		bs, _ := b.BinStock(id)
		if as.General != bs.General || as.Recycling != bs.Recycling || as.Food != bs.Food ||
			as.OverflowGeneral != bs.OverflowGeneral || as.VerminIndex != bs.VerminIndex {
			t.Fatalf("cell %s state diverged", id)
		}
	}
	are, _ := a.RemainingCapacity("L1")
	bre, _ := b.RemainingCapacity("L1")
	if are != bre {
		t.Fatalf("landfill remaining diverged: %d vs %d", are, bre)
	}
	ar, _ := a.Round("r1")
	br, _ := b.Round("r1")
	if !sameRoute(ar.Route, br.Route) || ar.Overridden != br.Overridden {
		t.Fatalf("round state diverged")
	}
}

// AC-17: concurrent round processing across depots is race-free.
func TestConcurrentRounds(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}

	const depots = 4
	const cellsPerDepot = 4
	for d := 0; d < depots; d++ {
		depotID := "d" + string(rune('0'+d))
		if err := api.RegisterDepot(depotID); err != nil {
			t.Fatal(err)
		}
		for c := 0; c < cellsPerDepot; c++ {
			cellID := depotID + "-c" + string(rune('0'+c))
			if err := api.RegisterCell(cellID, LandUseResidential, "Concurrent "+cellID); err != nil {
				t.Fatal(err)
			}
			if err := api.Generate(cellID, 50); err != nil {
				t.Fatal(err)
			}
		}
		var cellIDs []string
		for c := 0; c < cellsPerDepot; c++ {
			cellIDs = append(cellIDs, depotID+"-c"+string(rune('0'+c)))
		}
		if err := api.ScheduleRound("round-"+depotID, depotID, cellIDs); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errsCh := make(chan error, depots)
	for d := 0; d < depots; d++ {
		wg.Add(1)
		depotID := "d" + string(rune('0'+d))
		go func() {
			defer wg.Done()
			if _, err := api.RunRound("round-" + depotID); err != nil {
				errsCh <- err
			}
		}()
	}
	wg.Wait()
	close(errsCh)
	for err := range errsCh {
		t.Fatalf("concurrent RunRound: %v", err)
	}

	// Every cell was collected (no uncollected tonnage remains).
	for _, s := range streamOrder {
		if u, _ := api.TonnesUncollected(s); u != 0 {
			t.Fatalf("expected no uncollected tonnage after concurrent rounds, stream %s=%d", s, u)
		}
	}
}

// AC-8: Wire re-invocation with a DIFFERENT logistics instance must not
// reset a landfill's fill — the fill is persisted locally on the
// disposalSite and re-seeds the new instance's shelf, so RemainingCapacity
// stays monotone non-increasing across ANY re-wire.
func TestWireDifferentInstanceKeepsLandfillFill(t *testing.T) {
	api := newTestAPI(t)
	lg1, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg1, sv, w); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}

	if err := api.RegisterLandfill("L1", 1000, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	rem, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if rem != 0 {
		t.Fatalf("after fill remaining capacity = %d, want 0", rem)
	}

	// Re-wire with a DIFFERENT logistics instance.
	lg2, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Wire(lg2, sv, w); err != nil {
		t.Fatal(err)
	}

	remAfter, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if remAfter != 0 {
		t.Fatalf("different-instance re-wire reset the landfill fill: remaining capacity = %d, want 0", remAfter)
	}
	blighted, err := api.BlightedCells("L1")
	if err != nil {
		t.Fatal(err)
	}
	if len(blighted) != 1 || blighted[0] != "n1" {
		t.Fatalf("different-instance re-wire cleared the full landfill's blight, got %v", blighted)
	}
}

// AC-4/AC-6: an in-transit gridlock shortfall must be deliverable on a
// later round, not permanently stranded — a round with remaining shortfall
// stays open (not completed) so a re-run drains it through the same
// throughput-bounded machinery.
func TestGridlockShortfallDrainsOnLaterRun(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Drain Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 100_000); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(100); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}

	res, err := api.RunRound("r1")
	if err != nil {
		t.Fatal(err)
	}
	if res.ShortfallGeneral <= 0 {
		t.Fatalf("precondition: expected a gridlock shortfall, got %d", res.ShortfallGeneral)
	}
	rd, err := api.Round("r1")
	if err != nil {
		t.Fatal(err)
	}
	if rd.Completed {
		t.Fatal("a round with remaining in-transit shortfall must NOT be completed")
	}

	// Re-run (possibly several times) until the shortfall drains.
	total := res.CollectedGeneral
	delivered := res.DeliveredGeneral
	for i := 0; i < 50 && rd.InTransitGeneral > 0; i++ {
		res, err = api.RunRound("r1")
		if err != nil {
			t.Fatalf("re-run %d: %v", i, err)
		}
		delivered += res.DeliveredGeneral
		rd, err = api.Round("r1")
		if err != nil {
			t.Fatal(err)
		}
	}
	if rd.InTransitGeneral != 0 {
		t.Fatalf("in-transit shortfall never drained: %d", rd.InTransitGeneral)
	}
	if !rd.Completed {
		t.Fatal("round should complete once the shortfall is fully drained")
	}
	if delivered != total {
		t.Fatalf("delivered %d != collected %d after drain", delivered, total)
	}
}

// AC-3: general waste is the exact remainder of the recycling/food split,
// not a separately-configured data fraction — the data file must not carry
// a redundant "streamMix.general" key that generate.go never reads.
func TestStreamMixGeneralIsRemainderNotData(t *testing.T) {
	dir, err := data.ResolveDataDir("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "refuse.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		StreamMix map[string]float64 `json:"streamMix"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top.StreamMix["general"]; ok {
		t.Fatal("streamMix.general is dead data: general is computed as the exact remainder, so the data file must not carry a redundant general fraction")
	}
	if top.StreamMix["recycling"] <= 0 || top.StreamMix["food"] <= 0 {
		t.Fatalf("streamMix must still carry recycling and food fractions, got %v", top.StreamMix)
	}
}

// AC-8/AC-9/AC-10: the incinerator and compost DIRECT routes are
// throughput-bounded by engine.logistics' Deliverable, exactly like the
// round path — they must not accept an unbounded tonnage instantly.
func TestDirectRoutesThroughputBounded(t *testing.T) {
	api := newTestAPI(t)
	lg, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg, sv, w); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterIncinerator("I1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}

	const huge = int64(1) << 40
	// Independent oracle: engine.logistics' own waste throughput.
	want, err := lg.Deliverable("I1", market.Waste, huge)
	if err != nil {
		t.Fatal(err)
	}

	accepted, err := api.RouteGeneralToSite("I1", huge)
	if err != nil {
		t.Fatal(err)
	}
	if accepted != want.Delivered {
		t.Fatalf("incinerator direct route accepted %d, want throughput-bounded %d", accepted, want.Delivered)
	}

	acceptedFood, err := api.RouteFoodToCompost("C1", huge)
	if err != nil {
		t.Fatal(err)
	}
	if acceptedFood != want.Delivered {
		t.Fatalf("compost direct route accepted %d, want throughput-bounded %d", acceptedFood, want.Delivered)
	}
}

// AC-8: ProcessDisposal against a full landfill must surface a
// registry-sourced error rather than silently dropping the backlog
// (added=0 with no error left the tonnage permanently stuck).
func TestProcessDisposalFullLandfillErrors(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterLandfill("L1", 1000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	// Fill the landfill to capacity.
	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Full Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 100); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	// The round delivered general waste into the full landfill's backlog.
	added, err := api.ProcessDisposal("L1")
	assertErrCode(t, err, ErrDisposalSiteUnavailable)
	if added != 0 {
		t.Fatalf("full landfill should accept nothing, got added=%d", added)
	}
	// The shortfall is not silently dropped: it remains in the backlog.
	backlog, err := api.TonnesDisposalBacklog(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	if backlog == 0 {
		t.Fatal("backlog was silently dropped instead of surfaced")
	}
}

// AC-14: ClearOverride rejects a completed round, exactly like OverrideRound
// and AutoOptimise — a finished round's override is not silently cleared.
func TestClearOverrideCompletedRoundRejected(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Done Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 10); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	if err := api.ClearOverride("r1"); err == nil {
		t.Fatal("ClearOverride on a completed round must be rejected")
	} else {
		assertErrCode(t, err, ErrInvalidOverride)
	}
}

// AC-11/AC-14: ScheduleRound must reject re-scheduling an existing round
// (in flight or completed) with ErrInvalidOverride, never silently
// overwriting the roundState and zeroing its in-transit tonnage — the one
// public call that could otherwise break the mass-conservation identity.
func TestScheduleRoundRejectsExistingRound(t *testing.T) {
	t.Run("completed-round-with-stranded-transit", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Strand Street"); err != nil {
			t.Fatal(err)
		}
		// No disposal site configured: the round strands its collected
		// general and food tonnage in transit and completes.
		if err := api.Generate("c1", 4445); err != nil { // 4000kg -> 2200 general + 600 food
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if res.ShortfallGeneral <= 0 || res.ShortfallFood <= 0 {
			t.Fatalf("precondition: no-site round must strand general+food in transit, got %+v", res)
		}

		// Re-scheduling the same round ID must be rejected, preserving the
		// stranded tonnage and keeping AC-11's identity intact.
		err = api.ScheduleRound("r1", "d1", []string{"c1"})
		assertErrCode(t, err, ErrInvalidOverride)
		assertIdentity(t, api)
	})

	t.Run("gridlocked-round-with-shortfall", func(t *testing.T) {
		api, _ := newWiredAPI(t)
		if err := api.RegisterDepot("d1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
			t.Fatal(err)
		}
		if err := api.SetGeneralSite("L1"); err != nil {
			t.Fatal(err)
		}
		if err := api.RegisterCell("c1", LandUseResidential, "Gridlock Road"); err != nil {
			t.Fatal(err)
		}
		if err := api.Generate("c1", 100_000); err != nil {
			t.Fatal(err)
		}
		if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
			t.Fatal(err)
		}
		res, err := api.RunRound("r1")
		if err != nil {
			t.Fatal(err)
		}
		if res.ShortfallGeneral <= 0 {
			t.Fatalf("precondition: expected a gridlock shortfall, got %+v", res)
		}

		err = api.ScheduleRound("r1", "d1", []string{"c1"})
		assertErrCode(t, err, ErrInvalidOverride)
		assertIdentity(t, api)
	})
}

// AC-8: a reclaimed landfill must not accept round deliveries — RunRound's
// deliverToSite rejects a reclaimed site just like RouteGeneralToSite, so the
// tonnage stays in transit (routable elsewhere) instead of silently
// backlogging into a closed site where ProcessDisposal would refuse it.
func TestReclaimedSiteRejectsRoundDelivery(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Reclaimed Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if err := api.CapAndReclaim("L1"); err != nil {
		t.Fatal(err)
	}

	res, err := api.RunRound("r1")
	if err != nil {
		t.Fatal(err)
	}

	// The reclaimed site must accept nothing: no delivery, no backlog.
	if res.DeliveredGeneral != 0 {
		t.Fatalf("reclaimed landfill must accept no round delivery, got delivered=%d", res.DeliveredGeneral)
	}
	backlog, err := api.TonnesDisposalBacklog(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	if backlog != 0 {
		t.Fatalf("reclaimed landfill must not backlog round deliveries, got %d", backlog)
	}
	// The tonnage is preserved in transit, not destroyed — identity holds.
	assertIdentity(t, api)
}

// AC-2/AC-7 regression (Destructive-MOD039 r5): re-registering a cell to a
// SMALLER land use must clamp each in-bin stream level to the new capacity
// and spill the excess into overflow exactly once. The pre-fix behaviour
// kept the old in-bin levels while shrinking the capacity (industrial 6000kg
// -> residential 240kg leaves 2200/1200/600 in a 240kg bin), so the next
// Generate's negative-headroom branch re-spilled the pre-existing in-bin
// tonnage into overflow — phantom overflow that inflated
// vermin/land-value/fire-risk with waste that was never actually spilled.
func TestRegisterCellSmallerCapacityClampsLevels(t *testing.T) {
	api := newTestAPI(t)
	dir, err := data.ResolveDataDir("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	rf, err := data.LoadRefuseFile(dir, "refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	indCap := rf.BinCapacities[string(LandUseIndustrial)].CapacityKg
	resCap := rf.BinCapacities[string(LandUseResidential)].CapacityKg
	if resCap >= indCap {
		t.Fatalf("precondition: residential capacity (%d) must be smaller than industrial (%d)", resCap, indCap)
	}

	if err := api.RegisterCell("c1", LandUseIndustrial, "Mill Road"); err != nil {
		t.Fatal(err)
	}
	const driver = 1000.0
	gen := int64(math.Floor(driver * rf.WasteRates[string(LandUseIndustrial)].PerDriverPerTickKg))
	recycling := int64(math.Floor(float64(gen) * rf.StreamMix.Recycling))
	food := int64(math.Floor(float64(gen) * rf.StreamMix.Food))
	general := gen - recycling - food
	if err := api.Generate("c1", driver); err != nil {
		t.Fatal(err)
	}
	before, err := api.BinStock("c1")
	if err != nil {
		t.Fatal(err)
	}
	if before.General != general || before.Recycling != recycling || before.Food != food {
		t.Fatalf("precondition: expected generated levels g=%d r=%d f=%d, got g=%d r=%d f=%d",
			general, recycling, food, before.General, before.Recycling, before.Food)
	}
	// The shrink must genuinely force a spill on every stream, or the clamp
	// branch is never exercised.
	if general <= resCap || recycling <= resCap || food <= resCap {
		t.Fatalf("precondition: every stream must exceed the residential capacity (%d) to exercise the clamp: g=%d r=%d f=%d",
			resCap, general, recycling, food)
	}

	// Re-register to the smaller residential land use.
	if err := api.RegisterCell("c1", LandUseResidential, "High Street"); err != nil {
		t.Fatal(err)
	}
	after, err := api.BinStock("c1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Capacity != resCap {
		t.Fatalf("capacity = %d, want %d", after.Capacity, resCap)
	}
	// No in-bin stream may exceed the new capacity: each is clamped to it.
	if after.General != resCap || after.Recycling != resCap || after.Food != resCap {
		t.Fatalf("shrink must clamp every in-bin level to the new capacity %d: g=%d r=%d f=%d",
			resCap, after.General, after.Recycling, after.Food)
	}
	// The excess is spilled into overflow exactly once.
	wantOG := general - resCap
	wantOR := recycling - resCap
	wantOF := food - resCap
	if after.OverflowGeneral != wantOG || after.OverflowRecycling != wantOR || after.OverflowFood != wantOF {
		t.Fatalf("shrink must spill the excess exactly once: overflow g=%d r=%d f=%d, want g=%d r=%d f=%d",
			after.OverflowGeneral, after.OverflowRecycling, after.OverflowFood, wantOG, wantOR, wantOF)
	}
	// Mass conservation: levels+overflow is unchanged by the shrink.
	if after.General+after.OverflowGeneral != general ||
		after.Recycling+after.OverflowRecycling != recycling ||
		after.Food+after.OverflowFood != food {
		t.Fatalf("shrink must conserve mass: g=%d+%d r=%d+%d f=%d+%d, want g=%d r=%d f=%d",
			after.General, after.OverflowGeneral, after.Recycling, after.OverflowRecycling, after.Food, after.OverflowFood,
			general, recycling, food)
	}

	// The next Generate must add ONLY the newly-generated tonnage to overflow,
	// never re-spill the already-spilled excess (the phantom-overflow defect).
	if err := api.Generate("c1", 10); err != nil {
		t.Fatal(err)
	}
	afterGen, err := api.BinStock("c1")
	if err != nil {
		t.Fatal(err)
	}
	gen2 := int64(math.Floor(10 * rf.WasteRates[string(LandUseResidential)].PerDriverPerTickKg))
	recycling2 := int64(math.Floor(float64(gen2) * rf.StreamMix.Recycling))
	food2 := int64(math.Floor(float64(gen2) * rf.StreamMix.Food))
	general2 := gen2 - recycling2 - food2
	// Every stream is already at capacity, so the whole new generation spills.
	if afterGen.OverflowGeneral != wantOG+general2 ||
		afterGen.OverflowRecycling != wantOR+recycling2 ||
		afterGen.OverflowFood != wantOF+food2 {
		t.Fatalf("phantom overflow: after re-generate expected g=%d r=%d f=%d, got g=%d r=%d f=%d",
			wantOG+general2, wantOR+recycling2, wantOF+food2,
			afterGen.OverflowGeneral, afterGen.OverflowRecycling, afterGen.OverflowFood)
	}
}

// AC-8/AC-17 regression (Destructive-MOD039 r5): ensureSiteShelf's
// provisioned-flag check was a TOCTOU — concurrent RouteGeneralToSite calls
// on a fresh landfill could both pass the check and double-Provision,
// resetting the shelf so the landfill accepts over capacity and site.used
// diverges from shelf.Level (RemainingCapacity over-reports).
func TestConcurrentRouteGeneralToSiteNoOverAcceptance(t *testing.T) {
	api, _ := newWiredAPI(t)
	const capacity = int64(100_000)
	if err := api.RegisterLandfill("L1", capacity, nil); err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perCall = int64(20_000) // 8 * 20k = 160k > 100k capacity

	// Release every goroutine simultaneously to maximise the double-provision
	// window on the fresh shelf.
	var ready sync.WaitGroup
	var begin sync.WaitGroup
	ready.Add(goroutines)
	begin.Add(1)
	var mu sync.Mutex
	var acceptedSum int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			begin.Wait()
			accepted, err := api.RouteGeneralToSite("L1", perCall)
			if err != nil {
				var e *errs.E
				if !errors.As(err, &e) || e.Code != ErrDisposalSiteUnavailable {
					t.Errorf("unexpected error from RouteGeneralToSite: %v", err)
				}
				return
			}
			mu.Lock()
			acceptedSum += accepted
			mu.Unlock()
		}()
	}
	ready.Wait()
	begin.Done()
	wg.Wait()

	// The landfill must never accept more than its capacity.
	if acceptedSum > capacity {
		t.Fatalf("landfill accepted %d kg over capacity %d kg", acceptedSum, capacity)
	}
	rem, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if rem < 0 {
		t.Fatalf("remaining capacity must never be negative, got %d", rem)
	}
	// site.used must equal the shelf level: remaining == capacity - accepted.
	if want := capacity - acceptedSum; rem != want {
		t.Fatalf("RemainingCapacity diverged from accepted total (site.used != shelf.Level): rem=%d, accepted=%d, want rem=%d", rem, acceptedSum, want)
	}
}
