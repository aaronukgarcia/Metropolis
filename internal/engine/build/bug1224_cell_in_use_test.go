package build

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// BUG-1224 as rule version 1 (rules.RefuseOccupiedCell, FEAT-9995385612
// lane 4, criterion C24). At version 1 a build order for a cell that already
// holds a queued (not yet complete) order or a standing structure is refused
// at submit with ErrCellInUse; no order id is minted and nothing is ever drawn
// for it. At version 0 (replay of history recorded before the rule) the same
// order is accepted, mints an id and is built, exactly as recorded. These
// tests are also the proof that the registry's Old and New text is true (C25).

func submitDwelling(b *BuildAPI, col int) (BuildOrderID, error) {
	return b.SubmitBuildCommand(BuildCommand{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: col},
		OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	})
}

func runTicks(t *testing.T, b *BuildAPI, n int64) {
	t.Helper()
	for i := int64(0); i < n; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}
}

// fixtureAt builds a provisioned fixture whose rule-version getter answers v.
func fixtureAt(t *testing.T, v rules.Version) *BuildAPI {
	t.Helper()
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	b.SetRuleVersionSource(func() rules.Version { return v })
	if !b.HasRuleVersionSource() {
		t.Fatal("HasRuleVersionSource = false after SetRuleVersionSource")
	}
	return b
}

func nextOrderOf(b *BuildAPI) BuildOrderID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.nextOrder
}

func TestBUG1224_V1_SecondOrderOnQueuedCellRefusedNoExtraDraw(t *testing.T) {
	run := func(orders int) (queueLen int, drawn int64, structs int) {
		b := fixtureAt(t, rules.RefuseOccupiedCell)
		for i := 0; i < orders; i++ {
			before := nextOrderOf(b)
			_, err := submitDwelling(b, 0)
			if i == 0 && err != nil {
				t.Fatalf("first order: %v", err)
			}
			if i > 0 {
				assertCode(t, err, ErrCellInUse)
				if got := nextOrderOf(b); got != before {
					t.Errorf("nextOrder moved %d -> %d on a refused order", before, got)
				}
			}
		}
		runTicks(t, b, 200)
		q := b.Queue()
		for _, o := range q {
			drawn += o.MaterialsDrawn
		}
		if _, ok := b.Structure(tile00(), local00()); ok {
			structs = 1
		}
		return len(q), drawn, structs
	}
	q1, d1, s1 := run(1)
	q5, d5, s5 := run(5)
	if q1 != 1 || q5 != 1 {
		t.Errorf("queue lengths = %d, %d; want 1, 1", q1, q5)
	}
	if d1 != d5 || d1 == 0 {
		t.Errorf("materials drawn single=%d five=%d; want equal and non-zero", d1, d5)
	}
	if s1 != 1 || s5 != 1 {
		t.Errorf("structures = %d, %d; want 1, 1", s1, s5)
	}
}

func TestBUG1224_V1_OrderOnStandingStructureRefused(t *testing.T) {
	b := fixtureAt(t, rules.RefuseOccupiedCell)
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("first order: %v", err)
	}
	runTicks(t, b, 200)
	if _, ok := b.Structure(tile00(), local00()); !ok {
		t.Fatalf("precondition: structure did not land")
	}
	before := nextOrderOf(b)
	_, err := submitDwelling(b, 0)
	assertCode(t, err, ErrCellInUse)
	if n := len(b.Queue()); n != 1 {
		t.Errorf("queue length = %d, want 1 (refused order must not be queued)", n)
	}
	if got := nextOrderOf(b); got != before {
		t.Errorf("nextOrder moved %d -> %d on a refused order", before, got)
	}
}

func TestBUG1224_V1_DemolishFreesTheCell(t *testing.T) {
	b := fixtureAt(t, rules.RefuseOccupiedCell)
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("first order: %v", err)
	}
	runTicks(t, b, 200)
	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile00(), Local: local00(), OwnerID: testOwner}); err != nil {
		t.Fatalf("demolish: %v", err)
	}
	if _, err := submitDwelling(b, 0); err != nil {
		t.Errorf("order after demolition refused: %v", err)
	}
}

// A completed order whose structure was later demolished stays in the queue
// but is complete, so it must not hold the cell (it is the queue walk's
// "not complete" test, distinct from the structure lookup).
func TestBUG1224_V1_CompletedOrderWithDemolishedStructureDoesNotHoldCell(t *testing.T) {
	b := fixtureAt(t, rules.RefuseOccupiedCell)
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("first order: %v", err)
	}
	runTicks(t, b, 200)
	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile00(), Local: local00(), OwnerID: testOwner}); err != nil {
		t.Fatalf("demolish: %v", err)
	}
	q := b.Queue()
	if len(q) != 1 || q[0].Status != OrderComplete {
		t.Fatalf("precondition: want one complete order still in the queue, got %+v", q)
	}
	if _, ok := b.Structure(tile00(), local00()); ok {
		t.Fatal("precondition: structure still standing after demolition")
	}
	if _, err := submitDwelling(b, 0); err != nil {
		t.Errorf("order refused although the only prior order is complete and demolished: %v", err)
	}
}

func TestBUG1224_V1_DistinctCellsAllAccepted(t *testing.T) {
	b := fixtureAt(t, rules.RefuseOccupiedCell)
	for col := 0; col < 5; col++ {
		if _, err := submitDwelling(b, col); err != nil {
			t.Fatalf("order on cell col %d refused: %v", col, err)
		}
	}
	if n := len(b.Queue()); n != 5 {
		t.Errorf("queue length = %d, want 5", n)
	}
}

// Version 0 mirrors: the SAME inputs, the opposite expectation. This is the
// old behaviour a journal recorded before version 1 must replay under.

func TestBUG1224_V0_DuplicateOrdersAcceptedMintedAndBuilt(t *testing.T) {
	b := fixtureAt(t, 0)
	var ids []BuildOrderID
	for i := 0; i < 5; i++ {
		before := nextOrderOf(b)
		id, err := submitDwelling(b, 0)
		if err != nil {
			t.Fatalf("version 0 order %d refused: %v", i, err)
		}
		if id != before+1 || nextOrderOf(b) != before+1 {
			t.Errorf("order %d: id %d, nextOrder %d; want id and nextOrder %d (minted)", i, id, nextOrderOf(b), before+1)
		}
		ids = append(ids, id)
	}
	if n := len(b.Queue()); n != 5 {
		t.Errorf("queue length = %d, want 5 (all duplicates accepted)", n)
	}
	runTicks(t, b, 400)
	var drawn int64
	for _, o := range b.Queue() {
		drawn += o.MaterialsDrawn
	}
	b1 := fixtureAt(t, 0)
	if _, err := submitDwelling(b1, 0); err != nil {
		t.Fatalf("single order: %v", err)
	}
	runTicks(t, b1, 400)
	var drawn1 int64
	for _, o := range b1.Queue() {
		drawn1 += o.MaterialsDrawn
	}
	if drawn <= drawn1 {
		t.Errorf("five accepted duplicates drew %d, one order drew %d: the old rule charges every duplicate, want more", drawn, drawn1)
	}
	_ = ids
}

func TestBUG1224_V0_OrderOnStandingStructureAccepted(t *testing.T) {
	b := fixtureAt(t, 0)
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("first order: %v", err)
	}
	runTicks(t, b, 200)
	if _, ok := b.Structure(tile00(), local00()); !ok {
		t.Fatalf("precondition: structure did not land")
	}
	before := nextOrderOf(b)
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("version 0 order on a standing structure refused: %v", err)
	}
	if got := nextOrderOf(b); got != before+1 {
		t.Errorf("nextOrder = %d, want %d", got, before+1)
	}
}

// A bare module (no getter) judges by rules.Effective(), which is Current:
// live play and unit tests of the bare module run the refusal (C5).
func TestBUG1224_BareModuleRunsCurrentRules(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	if b.HasRuleVersionSource() {
		t.Fatal("bare fixture unexpectedly holds a getter")
	}
	if _, err := submitDwelling(b, 0); err != nil {
		t.Fatalf("first order: %v", err)
	}
	_, err := submitDwelling(b, 0)
	assertCode(t, err, ErrCellInUse)
}
