package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609845 r2: the round r1 attacker tests (F1 demolish-then-save,
// F2 load-time AssignHomes x2, F3 rezone of a standing dwelling), adopted as
// regressions. State checks only: occupancy, and the population hash at Save
// versus straight after Load.

// r1Fresh is the production restore path (lead note, BUG-1201): a FRESH
// engine + Composition loaded with LoadAt(dir, saveTick), never a plain Load
// into the running composition (which does not seed the clock by design).
func r1Fresh(t *testing.T, saveTick int64, dir string) (*core.Engine, *Composition) {
	t.Helper()
	e, comp := buildComposition2(t, housingSeed)
	if err := comp.LoadAt(dir, saveTick); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	return e, comp
}

func r1SaveTick(t *testing.T, e *core.Engine) int64 {
	t.Helper()
	clk, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	return clk.Tick()
}

func r1Occ(t *testing.T, comp *Composition, ref citizens.CellRef) int {
	ids, total, _, err := comp.state.citizens.OccupantsPage(citizens.OccupantsHome, uint32(ref), 0, 100, "r1")
	if err != nil {
		t.Fatalf("OccupantsPage: %v", err)
	}
	_ = ids
	return total
}

// A1: demolish a full dwelling, save before the next daily sync, then compare
// continuation after Load against the uninterrupted run.
func TestR1_DemolishThenSaveLoadContinue(t *testing.T) {
	e, comp, refs := housingRun(t)
	cid := errs.NewCorrelationID()
	tile, local, err := comp.state.cellFromRef(protocol.CellRef{X: 5, Y: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comp.state.buildAPI.SubmitDemolishCommand(build.DemolishCommand{Tile: tile, Local: local, OwnerID: 1}); err != nil {
		t.Fatalf("demolish: %v", err)
	}
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatal(err)
	}
	saveTick := r1SaveTick(t, e)
	if err := e.AdvanceTicks(cid, 2); err != nil {
		t.Fatal(err)
	}
	ctrlHash := comp.state.citizens.PopulationHash("h")
	ctrlOcc := r1Occ(t, comp, refs[0])
	ctrlLedger := comp.state.citizens.HomeOccupancy(refs[0])
	eL, compL := r1Fresh(t, saveTick, dir)
	if err := eL.AdvanceTicks(cid, 2); err != nil {
		t.Fatal(err)
	}
	gotHash := compL.state.citizens.PopulationHash("h")
	gotOcc := r1Occ(t, compL, refs[0])
	gotLedger := compL.state.citizens.HomeOccupancy(refs[0])
	t.Logf("control: residents at demolished cell=%d ledger=%d; loaded: residents=%d ledger=%d", ctrlOcc, ctrlLedger, gotOcc, gotLedger)
	if gotOcc != 0 || ctrlOcc != 0 {
		t.Errorf("citizens still point at demolished cell: control=%d loaded=%d", ctrlOcc, gotOcc)
	}
	if gotHash != ctrlHash {
		t.Errorf("save->load->continue diverged from uninterrupted run")
	}
}

// A2: a departure frees a slot in the HIGHER dwelling, save, then a departure
// frees a slot in the LOWER dwelling before the next daily sync. Load-time
// AssignHomes houses the pending citizen early, so assignment order changes.
func TestR1_LoadTimeAssignReordersHousing(t *testing.T) {
	e, comp, refs := housingRun(t)
	cid := errs.NewCorrelationID()
	api := comp.state.citizens
	victim := func(ref citizens.CellRef) uint64 {
		ids, _, _, err := api.OccupantsPage(citizens.OccupantsHome, uint32(ref), 0, 100, "r1")
		if err != nil || len(ids) == 0 {
			t.Fatalf("no occupants at %d: %v", ref, err)
		}
		return ids[len(ids)-1]
	}
	kill := func(id uint64) {
		if err := api.ApplyLifeEventCommand(citizens.LifeEventCommand{Kind: citizens.LifeEventDeath, CitizenID: id, CorrelationID: "r1"}); err != nil {
			t.Fatalf("death %d: %v", id, err)
		}
	}
	hiVictim := victim(refs[1])
	loVictim := victim(refs[0])
	kill(hiVictim)
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatal(err)
	}
	saveTick := r1SaveTick(t, e)
	kill(loVictim)
	if err := e.AdvanceTicks(cid, 2); err != nil {
		t.Fatal(err)
	}
	ctrl := api.PopulationHash("h")
	eL, compL := r1Fresh(t, saveTick, dir)
	api = compL.state.citizens
	kill(loVictim)
	if err := eL.AdvanceTicks(cid, 2); err != nil {
		t.Fatal(err)
	}
	if api.PopulationHash("h") != ctrl {
		t.Errorf("save->load->continue diverged: Load's AssignHomes housed the pending citizen before the next daily sync")
	}
}

// A2b: Load itself mutates the population when a slot was freed after the
// last daily sync (pending + free at save time).
func TestR1_LoadMutatesPopulation(t *testing.T) {
	e, comp, refs := housingRun(t)
	api := comp.state.citizens
	ids, _, _, _ := api.OccupantsPage(citizens.OccupantsHome, uint32(refs[1]), 0, 100, "r1")
	if err := api.ApplyLifeEventCommand(citizens.LifeEventCommand{Kind: citizens.LifeEventDeath, CitizenID: ids[len(ids)-1], CorrelationID: "r1"}); err != nil {
		t.Fatal(err)
	}
	homeless := api.HomelessCount()
	before := api.PopulationHash("h")
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatal(err)
	}
	_, compL := r1Fresh(t, r1SaveTick(t, e), dir)
	after := compL.state.citizens.HomelessCount()
	t.Logf("homeless at save=%d after load=%d", homeless, after)
	if compL.state.citizens.PopulationHash("h") != before {
		t.Errorf("Load changed the population hash (load-time AssignHomes)")
	}
}

func TestR1_RezoneStandingDwelling(t *testing.T) {
	e, comp, refs := housingRun(t)
	tile, local, _ := comp.state.cellFromRef(protocol.CellRef{X: 5, Y: 5})
	var other build.ZoneType
	for _, z := range comp.state.buildAPI.ZoneCatalogue() {
		if z.ResidentCapacity == 0 {
			other = z.Zone
			break
		}
	}
	if err := comp.state.buildAPI.SubmitZoneCommand(build.ZoneCommand{Tile: tile, Local: local, OwnerID: 1, Zone: other}); err != nil {
		t.Fatalf("rezone to %v: %v", other, err)
	}
	listed := false
	for _, s := range comp.state.buildAPI.StandingCapacities() {
		if s.Tile == tile && s.Local == local && s.ResidentCapacity > 0 {
			listed = true
		}
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 60); err != nil {
		t.Fatal(err)
	}
	_, total, _, _ := comp.state.citizens.OccupantsPage(citizens.OccupantsHome, uint32(refs[0]), 0, 100, "r1")
	t.Logf("rezoned to %v; still listed as dwelling=%v; residents after 60 days=%d", other, listed, total)
	if !listed && total > 0 {
		t.Errorf("residents remain at a cell no longer a dwelling (epoch never moved)")
	}
}
