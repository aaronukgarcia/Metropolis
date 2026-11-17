package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609845 -- end to end in the composed engine: a built dwelling gives
// the seed population real Home cells, deterministically, and a save/load
// rebuilds the (derived) occupancy ledger.

const housingSeed = uint64(20261101)

func buildDwellings(t *testing.T, e *core.Engine, comp *Composition, cells []protocol.CellRef) {
	t.Helper()
	bought := map[string]bool{}
	for _, cell := range cells {
		tile, local, err := comp.state.cellFromRef(cell)
		if err != nil {
			t.Fatalf("cellFromRef: %v", err)
		}
		// A Buy purchases the whole tile, so buy each tile once.
		if key := fmt.Sprint(tile); !bought[key] {
			bought[key] = true
			buy := protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.CorrelationID("housing-buy"),
				Kind:            protocol.KindBuy,
				Payload:         protocol.BuyPayload{Cell: cell},
			}
			if res := e.HandleCommand(buy); !res.Accepted {
				t.Fatalf("Buy %v rejected: %+v", cell, res.Error)
			}
		}
		month, err := comp.state.currentMonth()
		if err != nil {
			t.Fatalf("currentMonth: %v", err)
		}
		if _, err := comp.state.buildAPI.SubmitBuildCommand(build.BuildCommand{
			Tile: tile, Local: local, OwnerID: 1, Zone: build.ZoneDwelling, Month: month,
		}); err != nil {
			t.Fatalf("SubmitBuildCommand: %v", err)
		}
	}
}

func housingRun(t *testing.T) (*core.Engine, *Composition, []citizens.CellRef) {
	t.Helper()
	e, comp := buildComposition2(t, housingSeed)
	cid := errs.NewCorrelationID()
	cells := []protocol.CellRef{{X: 5, Y: 5}, {X: 6, Y: 5}}
	if got := comp.state.citizens.HomelessCount(); got < 20 {
		t.Fatalf("test setup: want a seed population of at least 20, got %d", got)
	}
	buildDwellings(t, e, comp, cells)
	if err := e.AdvanceTicks(cid, 3*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	refs := make([]citizens.CellRef, 0, len(cells))
	for _, c := range cells {
		tile, local, err := comp.state.cellFromRef(c)
		if err != nil {
			t.Fatalf("cellFromRef: %v", err)
		}
		ref, ok := housingCellRef(tile, local)
		if !ok {
			t.Fatalf("housingCellRef(%v,%v) out of grid", tile, local)
		}
		refs = append(refs, ref)
	}
	return e, comp, refs
}

func TestHousing_ComposedEngine_BuiltDwellingsHouseCitizens(t *testing.T) {
	_, comp, refs := housingRun(t)
	api := comp.state.citizens
	if !api.HousingEnabled() {
		t.Fatal("housing ledger never switched on in the composed engine")
	}
	for _, ref := range refs {
		if got := api.HomeOccupancy(ref); got != 8 {
			t.Errorf("occupancy(%d)=%d, want 8", ref, got)
		}
	}
	// Passport: the lowest-id live citizen lives at the lowest built cell and
	// the occupants list for that cell is non-empty.
	ids, total, _, err := api.OccupantsPage(citizens.OccupantsHome, uint32(refs[0]), 0, 50, "housing-test")
	// One dwelling's placeholder residentCapacity (data/buildings.json).
	const dwellingResidents = 8
	if err != nil || len(ids) != dwellingResidents || total != dwellingResidents {
		t.Fatalf("occupants of %d: ids=%v total=%d err=%v, want %d", refs[0], ids, total, err, dwellingResidents)
	}
	rec, _, ok := api.PassportRecord(ids[0], "housing-test")
	if !ok || rec.Home != refs[0] {
		t.Fatalf("passport of %d: ok=%v home=%d, want %d", ids[0], ok, rec.Home, refs[0])
	}
}

func TestHousing_ComposedEngine_Deterministic(t *testing.T) {
	_, a, _ := housingRun(t)
	_, b, _ := housingRun(t)
	if a.state.citizens.PopulationHash("h") != b.state.citizens.PopulationHash("h") {
		t.Fatal("two identical runs produced different populations")
	}
}

func TestHousing_ComposedEngine_SaveLoadRebuildsLedger(t *testing.T) {
	_, comp, refs := housingRun(t)
	api := comp.state.citizens
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	hashBefore := api.PopulationHash("h")
	if err := comp.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !comp.state.citizens.HousingEnabled() {
		t.Fatal("housing ledger not rebuilt after Load")
	}
	for _, ref := range refs {
		if got := comp.state.citizens.HomeOccupancy(ref); got != 8 {
			t.Errorf("after load occupancy(%d)=%d, want 8", ref, got)
		}
	}
	if comp.state.citizens.PopulationHash("h") != hashBefore {
		t.Error("population hash changed across save/load")
	}
}
