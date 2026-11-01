package compose

// BUG-1142 destructive round r2 (opus-round-r2-bug1142): OLD-SAVE policy
// probes for the compose ledger's construction-accrual counters. An "old
// shape" bundle is produced by saving through Participants() with the
// compose.ledger records stripped of the five BUG-1142 r2 keys (exactly what
// a bundle written before the r2 rework carries).

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

var b1142r2StrippedKeys = []string{
	"materialsDrawnCumulative",
	"constructionAccrualLocal",
	"constructionAccrualExternal",
	"constructionSettledLocal",
	"constructionSettledExternal",
}

// b1142r2StripLedger wraps the compose ledger participant so its Source emits
// the pre-r2 wire shape.
type b1142r2StripLedger struct{ save.Participant }

func (p b1142r2StripLedger) Source() serialize.RecordSource {
	src := p.Participant.Source()
	return func() (serialize.Record, bool, error) {
		rec, ok, err := src()
		if err != nil || !ok || rec.Kind != recComposeLedger {
			return rec, ok, err
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return rec, ok, err
		}
		for _, k := range b1142r2StrippedKeys {
			delete(m, k)
		}
		b, err := json.Marshal(m)
		if err != nil {
			return rec, ok, err
		}
		rec.Data = b
		return rec, ok, nil
	}
}

// b1142r2SaveOldShape mirrors Composition.Save but strips the ledger keys and
// (optionally) drops the logistics shard (a bundle written before the
// logistics participant existed).
func b1142r2SaveOldShape(t *testing.T, c *Composition, root string, dropLogistics bool) {
	t.Helper()
	clock, err := c.state.e.Clock()
	if err != nil {
		t.Fatal(err)
	}
	gameMode, err := c.state.gameInit.GameModeWire(c.state.cid)
	if err != nil {
		t.Fatal(err)
	}
	var parts []save.Participant
	for _, p := range c.Participants() {
		switch p.Kind() {
		case kindComposeLedger:
			parts = append(parts, b1142r2StripLedger{p})
		case logistics.KindLogistics:
			if !dropLogistics {
				parts = append(parts, p)
			}
		default:
			parts = append(parts, p)
		}
	}
	ctx := save.Context{WorldSeed: int64(c.state.seed), CreatedAtTick: clock.Tick(), GameMonth: clock.Month(), AppVersion: compositionSaveAppVersion, GameMode: gameMode}
	if err := save.NewManager(root, parts, c.state.cid).SaveManual(ctx, compositionSaveName); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
}

// TestBUG1142R2_Attack_OldShapeSaveMidCycle: an old-shape save at tick 45
// (draws made, accrual pending, settlement at 90 still ahead). After LoadAt
// and 100 continued ticks the loaded engine may differ from the never-stopped
// one ONLY by the documented under-charge: the accrual pending at the save
// point (never a double charge, never anything else).
func TestBUG1142R2_Attack_OldShapeSaveMidCycle(t *testing.T) {
	for _, drop := range []bool{false, true} {
		drop := drop
		name := "withLogisticsShard"
		if drop {
			name = "noLogisticsShard"
		}
		t.Run(name, func(t *testing.T) {
			const saveTick, cont = 45, 100
			eA, compA, _, vA := b1142r1Wire(t)
			b1142r1BuyAndBuild(t, eA, []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}})
			if err := eA.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
				t.Fatal(err)
			}
			stA := compA.state
			pending := stA.constructionAccrualLocal + stA.constructionAccrualExternal
			drawnAtSave := stA.totalMaterialsDrawn()
			if pending <= 0 || drawnAtSave <= 0 {
				t.Fatalf("precondition: pending accrual %d drawn %d", pending, drawnAtSave)
			}
			dir := t.TempDir()
			b1142r2SaveOldShape(t, compA, dir, drop)
			eB, compB, _, vB := b1142r1Wire(t)
			if err := compB.LoadAt(dir, saveTick); err != nil {
				t.Fatalf("LoadAt: %v", err)
			}
			stB := compB.state
			if stB.materialsDrawnCumulative != drawnAtSave {
				t.Errorf("re-base landed %d, want restored queue drawn total %d", stB.materialsDrawnCumulative, drawnAtSave)
			}
			cid := errs.NewCorrelationID()
			if err := eA.AdvanceTicks(cid, cont); err != nil {
				t.Fatal(err)
			}
			if err := eB.AdvanceTicks(cid, cont); err != nil {
				t.Fatal(err)
			}
			diff := compB.Treasury() - compA.Treasury()
			t.Logf("pending accrual at save %d; treasury B-A after %d ticks = %d; settled A ext %d B ext %d", pending, cont, diff, compA.state.constructionSettledExternal, compB.state.constructionSettledExternal)
			if diff != pending {
				t.Errorf("treasury B-A = %d, want exactly the documented under-charge %d (negative = double charge)", diff, pending)
			}
			if got := compA.state.constructionSettledExternal - compB.state.constructionSettledExternal; got != pending {
				t.Errorf("settled delta A-B = %d, want %d", got, pending)
			}
			if len(*vA) != 0 || len(*vB) != 0 {
				t.Errorf("conservation violations A=%v B=%v", *vA, *vB)
			}
		})
	}
}

// TestBUG1142R2_Attack_ReorderedOldShapeHeader: the re-base reads the build
// queue at the moment the ledger shard loads, i.e. the bundle header's shard
// order. Probe: an old-shape bundle whose header lists compose.ledger BEFORE
// build. Logged (not asserted) - documents the order dependency for the lead.
func TestBUG1142R2_Attack_ReorderedOldShapeHeader(t *testing.T) {
	const saveTick = 45
	eA, compA, _, _ := b1142r1Wire(t)
	b1142r1BuyAndBuild(t, eA, []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}})
	if err := eA.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
		t.Fatal(err)
	}
	clock, _ := compA.state.e.Clock()
	gameMode, _ := compA.state.gameInit.GameModeWire(compA.state.cid)
	var ledger save.Participant
	var rest []save.Participant
	for _, p := range compA.Participants() {
		if p.Kind() == kindComposeLedger {
			ledger = b1142r2StripLedger{p}
			continue
		}
		rest = append(rest, p)
	}
	parts := append([]save.Participant{ledger}, rest...)
	dir := t.TempDir()
	ctx := save.Context{WorldSeed: int64(compA.state.seed), CreatedAtTick: clock.Tick(), GameMonth: clock.Month(), AppVersion: compositionSaveAppVersion, GameMode: gameMode}
	if err := save.NewManager(dir, parts, compA.state.cid).SaveManual(ctx, compositionSaveName); err != nil {
		t.Fatal(err)
	}
	_, compB, _, _ := b1142r1Wire(t)
	if err := compB.LoadAt(dir, saveTick); err != nil {
		t.Logf("LoadAt refused reordered bundle: %v", err)
		return
	}
	t.Logf("reordered old-shape header: re-base landed %d, true drawn %d (0 => the next settlement re-charges every prior draw)", compB.state.materialsDrawnCumulative, compA.state.totalMaterialsDrawn())
}
