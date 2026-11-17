package compose

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Round r2 attacker (FEAT-2326609845): save at many days, perturbations applied
// immediately before the save, fresh Composition + LoadAt, continue, compare
// every day against the uninterrupted run.

type r2Day struct {
	hash     [32]byte
	homeless int
	ledger   []int
	column   []int
}

func r2Cells() []protocol.CellRef {
	return []protocol.CellRef{{X: 5, Y: 5}, {X: 6, Y: 5}, {X: 7, Y: 5}, {X: 8, Y: 5}, {X: 9, Y: 5}}
}

func r2Snap(t *testing.T, comp *Composition, refs []citizens.CellRef) r2Day {
	d := r2Day{hash: comp.state.citizens.PopulationHash("h"), homeless: comp.state.citizens.HomelessCount()}
	for _, r := range refs {
		d.ledger = append(d.ledger, comp.state.citizens.HomeOccupancy(r))
		_, total, _, err := comp.state.citizens.OccupantsPage(citizens.OccupantsHome, uint32(r), 0, 1000, "r2")
		if err != nil {
			t.Fatal(err)
		}
		d.column = append(d.column, total)
	}
	return d
}

// r2Perturb applies the day's scripted perturbation (identical in both runs).
func r2Perturb(t *testing.T, e *core.Engine, comp *Composition, day int) {
	switch day {
	case 1: // two more dwellings started (complete ~45 days later)
		r2Build(t, comp, protocol.CellRef{X: 7, Y: 5}, protocol.CellRef{X: 8, Y: 5})
	case 10: // demolish a full dwelling
		tile, local, _ := comp.state.cellFromRef(protocol.CellRef{X: 5, Y: 5})
		if _, err := comp.state.buildAPI.SubmitDemolishCommand(build.DemolishCommand{Tile: tile, Local: local, OwnerID: 1}); err != nil {
			t.Fatalf("demolish: %v", err)
		}
	case 20: // rezone a standing dwelling to a non-residential zone
		tile, local, _ := comp.state.cellFromRef(protocol.CellRef{X: 6, Y: 5})
		var other build.ZoneType
		for _, z := range comp.state.buildAPI.ZoneCatalogue() {
			if z.ResidentCapacity == 0 {
				other = z.Zone
				break
			}
		}
		if err := comp.state.buildAPI.SubmitZoneCommand(build.ZoneCommand{Tile: tile, Local: local, OwnerID: 1, Zone: other}); err != nil {
			t.Fatalf("rezone: %v", err)
		}
	case 30:
		r2Build(t, comp, protocol.CellRef{X: 9, Y: 5})
	}
}

func r2Refs(t *testing.T, comp *Composition) []citizens.CellRef {
	var refs []citizens.CellRef
	for _, c := range r2Cells() {
		tile, local, err := comp.state.cellFromRef(c)
		if err != nil {
			t.Fatal(err)
		}
		ref, _ := housingCellRef(tile, local)
		refs = append(refs, ref)
	}
	return refs
}

func TestRoundR2_FEAT845_SaveLoadEquivalenceSweep(t *testing.T) {
	const days = 150
	saveDays := map[int]bool{0: true, 1: true, 10: true, 11: true, 20: true, 30: true, 44: true, 45: true, 46: true, 47: true, 60: true, 90: true, 120: true}
	cid := errs.NewCorrelationID()
	e, comp, _ := housingRun(t)
	refs := r2Refs(t, comp)
	ctrl := make([]r2Day, days+1)
	dirs := map[int]string{}
	ticks := map[int]int64{}
	for d := 0; d <= days; d++ {
		r2Perturb(t, e, comp, d)
		if saveDays[d] {
			before := comp.state.citizens.PopulationHash("h")
			dir := t.TempDir()
			if err := comp.Save(dir); err != nil {
				t.Fatal(err)
			}
			if after := comp.state.citizens.PopulationHash("h"); after != before {
				t.Fatalf("day %d: Save mutated the population", d)
			}
			dirs[d] = dir
			ticks[d] = r1SaveTick(t, e)
		}
		if err := e.AdvanceTicks(cid, 1); err != nil {
			t.Fatal(err)
		}
		ctrl[d] = r2Snap(t, comp, refs)
		for i := range refs {
			if ctrl[d].ledger[i] != ctrl[d].column[i] {
				t.Errorf("control day %d cell %d: ledger occ %d != column residents %d", d, refs[i], ctrl[d].ledger[i], ctrl[d].column[i])
			}
		}
	}
	t.Logf("control day 0 %+v", ctrl[0].column)
	t.Logf("control day 11 %+v homeless=%d", ctrl[11].column, ctrl[11].homeless)
	t.Logf("control day 21 %+v homeless=%d", ctrl[21].column, ctrl[21].homeless)
	t.Logf("control day 60 %+v homeless=%d", ctrl[60].column, ctrl[60].homeless)
	t.Logf("control day %d %+v homeless=%d", days, ctrl[days].column, ctrl[days].homeless)
	for sd := 0; sd <= days; sd++ {
		dir, ok := dirs[sd]
		if !ok {
			continue
		}
		t.Run(fmt.Sprintf("save-day-%d", sd), func(t *testing.T) {
			eL, compL := r1Fresh(t, ticks[sd], dir)
			for d := sd; d <= days; d++ {
				if d > sd {
					r2Perturb(t, eL, compL, d)
				}
				if err := eL.AdvanceTicks(cid, 1); err != nil {
					t.Fatal(err)
				}
				got := r2Snap(t, compL, refs)
				if got.hash != ctrl[d].hash || got.homeless != ctrl[d].homeless || fmt.Sprint(got.ledger) != fmt.Sprint(ctrl[d].ledger) || fmt.Sprint(got.column) != fmt.Sprint(ctrl[d].column) {
					t.Fatalf("day %d diverged: loaded col=%v ledger=%v homeless=%d; control col=%v ledger=%v homeless=%d; hashEq=%v",
						d, got.column, got.ledger, got.homeless, ctrl[d].column, ctrl[d].ledger, ctrl[d].homeless, got.hash == ctrl[d].hash)
				}
			}
		})
	}
}

func r2Build(t *testing.T, comp *Composition, cells ...protocol.CellRef) {
	for _, cell := range cells {
		tile, local, err := comp.state.cellFromRef(cell)
		if err != nil {
			t.Fatal(err)
		}
		month, err := comp.state.currentMonth()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := comp.state.buildAPI.SubmitBuildCommand(build.BuildCommand{Tile: tile, Local: local, OwnerID: 1, Zone: build.ZoneDwelling, Month: month}); err != nil {
			t.Fatalf("build: %v", err)
		}
	}
}

// Long run, everybody housed: births to housed parents overcrowd, deaths free
// slots at both release sites, and a demolition cuts an overcrowded dwelling.
func TestRoundR2_FEAT845_LongRunHousedSaveLoad(t *testing.T) {
	const days = 600
	cid := errs.NewCorrelationID()
	e, comp, _ := housingRun(t)
	var cells []protocol.CellRef
	for y := 6; y <= 7; y++ {
		for x := 5; x <= 14; x++ {
			cells = append(cells, protocol.CellRef{X: x, Y: y})
		}
	}
	r2Build(t, comp, cells...)
	var refs []citizens.CellRef
	for _, c := range cells {
		tile, local, _ := comp.state.cellFromRef(c)
		ref, _ := housingCellRef(tile, local)
		refs = append(refs, ref)
	}
	perturb := func(comp *Composition, d int) {
		if d == 300 || d == 450 {
			c := cells[0]
			if d == 450 {
				c = cells[1]
			}
			tile, local, _ := comp.state.cellFromRef(c)
			if _, err := comp.state.buildAPI.SubmitDemolishCommand(build.DemolishCommand{Tile: tile, Local: local, OwnerID: 1}); err != nil {
				t.Fatalf("demolish: %v", err)
			}
		}
	}
	ctrl := make([]r2Day, days+1)
	dirs := map[int]string{}
	ticks := map[int]int64{}
	pop0 := comp.state.citizens.HomelessCount()
	maxOver := 0
	for d := 0; d <= days; d++ {
		perturb(comp, d)
		if d%73 == 0 || d == 300 || d == 450 {
			dir := t.TempDir()
			if err := comp.Save(dir); err != nil {
				t.Fatal(err)
			}
			dirs[d] = dir
			ticks[d] = r1SaveTick(t, e)
		}
		if err := e.AdvanceTicks(cid, 1); err != nil {
			t.Fatal(err)
		}
		ctrl[d] = r2Snap(t, comp, refs)
		for i := range refs {
			if ctrl[d].ledger[i] != ctrl[d].column[i] {
				t.Errorf("control day %d cell %d: ledger %d != column %d", d, refs[i], ctrl[d].ledger[i], ctrl[d].column[i])
			}
			if ctrl[d].column[i] > maxOver {
				maxOver = ctrl[d].column[i]
			}
		}
	}
	t.Logf("homeless at start(before 20 builds)=%d; day 100 col=%v homeless=%d; day 299 col=%v; day 301 col=%v; day %d col=%v homeless=%d; max residents in one cell=%d",
		pop0, ctrl[100].column, ctrl[100].homeless, ctrl[299].column, ctrl[301].column, days, ctrl[days].column, ctrl[days].homeless, maxOver)
	for sd := 0; sd <= days; sd++ {
		dir, ok := dirs[sd]
		if !ok {
			continue
		}
		t.Run(fmt.Sprintf("save-day-%d", sd), func(t *testing.T) {
			eL, compL := r1Fresh(t, ticks[sd], dir)
			for d := sd; d <= days; d++ {
				if d > sd {
					perturb(compL, d)
				}
				if err := eL.AdvanceTicks(cid, 1); err != nil {
					t.Fatal(err)
				}
				got := r2Snap(t, compL, refs)
				if got.hash != ctrl[d].hash || got.homeless != ctrl[d].homeless || fmt.Sprint(got.ledger) != fmt.Sprint(ctrl[d].ledger) {
					t.Fatalf("day %d diverged: loaded col=%v homeless=%d; control col=%v homeless=%d; hashEq=%v", d, got.column, got.homeless, ctrl[d].column, ctrl[d].homeless, got.hash == ctrl[d].hash)
				}
			}
		})
	}
}

func TestRoundR2_FEAT845_OldSaveLoadsAndContinues(t *testing.T) {
	dir := os.Getenv("R2_OLDSAVE_DIR")
	if dir == "" {
		t.Skip("no old save")
	}
	tick, _ := strconv.ParseInt(os.Getenv("R2_OLDSAVE_TICK"), 10, 64)
	eL, compL := r1Fresh(t, tick, dir)
	api := compL.state.citizens
	var refs []citizens.CellRef
	for x := 5; x <= 9; x++ {
		tile, local, _ := compL.state.cellFromRef(protocol.CellRef{X: x, Y: 5})
		ref, _ := housingCellRef(tile, local)
		refs = append(refs, ref)
	}
	h0 := api.PopulationHash("h")
	t.Logf("after old-save load: housingEnabled=%v homeless=%d", api.HousingEnabled(), api.HomelessCount())
	if api.HousingEnabled() {
		t.Error("old save restored with the ledger ON")
	}
	before := r2Snap(t, compL, refs)
	t.Logf("column residents at dwellings before first sync: %v", before.column)
	for d := 0; d < 120; d++ {
		if err := eL.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
		s := r2Snap(t, compL, refs)
		if fmt.Sprint(s.ledger) != fmt.Sprint(s.column) {
			t.Fatalf("day %d ledger %v != column %v", d, s.ledger, s.column)
		}
		if d == 0 || d == 119 {
			t.Logf("day %d: col=%v homeless=%d enabled=%v hashChanged=%v", d, s.column, s.homeless, api.HousingEnabled(), s.hash != h0)
		}
	}
	// save the continued old game and reload it: now a new-format save
	dir2 := t.TempDir()
	if err := compL.Save(dir2); err != nil {
		t.Fatal(err)
	}
	clk, _ := eL.Clock()
	_, c2 := r1Fresh(t, clk.Tick(), dir2)
	if c2.state.citizens.PopulationHash("h") != api.PopulationHash("h") || !c2.state.citizens.HousingEnabled() {
		t.Fatal("re-save of a continued old game did not round-trip")
	}
}
