package compose

// BUG-1142 destructive round r1 (opus-round-r1-bug1142): attack pins for the
// default-composition ConstructionMaterials starting stock and the LoadAt
// BUG-288 tracker re-establishment change in save_wire.go.

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// b1142r1Wire builds a dev-mode composition whose logistics is EXACTLY what
// Wire's default path builds (LoadDefault + ProvisionStartingStocks), but
// handed in so the test can read the shelf. Violations are collected.
func b1142r1Wire(t *testing.T) (*core.Engine, *Composition, *logistics.LogisticsAPI, *[]string) {
	t.Helper()
	cid := errs.NewCorrelationID()
	lg, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := lg.ProvisionStartingStocks(); err != nil {
		t.Fatalf("ProvisionStartingStocks: %v", err)
	}
	var violations []string
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		CorrelationID: cid,
		Coastal:       quietCoastal(t),
		Logistics:     lg,
		InvariantOpts: []invariant.HookOption{
			invariant.WithDevMode(true),
			invariant.WithPanicFunc(func(msg string) { violations = append(violations, msg) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp, lg, &violations
}

func b1142r1BuyAndBuild(t *testing.T, e *core.Engine, cells []protocol.CellRef) {
	t.Helper()
	if res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1-buy", Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cells[0]}}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	for _, cell := range cells {
		if res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1-zone", Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}}); !res.Accepted {
			t.Fatalf("Zone rejected: %+v", res.Error)
		}
		if res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1-build", Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}}); !res.Accepted {
			t.Fatalf("Build rejected: %+v", res.Error)
		}
	}
}

func b1142r1Level(t *testing.T, lg *logistics.LogisticsAPI) int64 {
	t.Helper()
	s, err := lg.Stock(build.DefaultDistrict, market.ConstructionMaterials)
	if err != nil {
		t.Fatalf("Stock: %v", err)
	}
	return s.Level
}

// TestBUG1142R1_LoadAt_MaterialsShelfIsNotReprovisioned: a loaded city must
// never be re-provisioned. City A draws materials, saves; B is a fresh boot
// (Wire provisions the full starting stock) then LoadAt. The shelf level B
// resumes with must be A's drawn-down level, not a refilled shelf.
func TestBUG1142R1_LoadAt_MaterialsShelfIsNotReprovisioned(t *testing.T) {
	eA, compA, lgA, _ := b1142r1Wire(t)
	full := b1142r1Level(t, lgA)
	b1142r1BuyAndBuild(t, eA, []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}})
	if err := eA.AdvanceTicks(errs.NewCorrelationID(), 45); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	levelA := b1142r1Level(t, lgA)
	if levelA >= full {
		t.Fatalf("precondition: no materials drawn (level %d, full %d)", levelA, full)
	}
	clockA, _ := eA.Clock()
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, compB, lgB, _ := b1142r1Wire(t)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if levelB := b1142r1Level(t, lgB); levelB != levelA {
		t.Fatalf("loaded city's ConstructionMaterials shelf = %d, saved city's = %d (full %d): the load path re-provisioned the starting stock - %d t of materials minted from nothing per save/load", levelB, levelA, full, levelB-levelA)
	}
}

// TestBUG1142R1_LoadAt_SweepAcrossSettlementAndMonthBoundaries saves at many
// ticks (0, around the NET-90 settlement and month boundaries) with builds in
// flight, LoadAts, and continues both engines: the trackers must match the
// never-stopped engine, dev-mode conservation must stay clean on both, and
// treasury/settled-cost/digest must be identical after continuing (the settled
// cost lands exactly once across the boundary).
func TestBUG1142R1_LoadAt_SweepAcrossSettlementAndMonthBoundaries(t *testing.T) {
	for _, saveTick := range []int64{0, 1, 2, 29, 30, 31, 59, 60, 61, 88, 89, 90, 91, 92, 119, 120, 121} {
		saveTick := saveTick
		t.Run(fmt.Sprintf("save@%d", saveTick), func(t *testing.T) {
			const cont = 100
			eA, compA, _, vA := b1142r1Wire(t)
			b1142r1BuyAndBuild(t, eA, []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}})
			if saveTick > 0 {
				if err := eA.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
					t.Fatalf("AdvanceTicks A: %v", err)
				}
			}
			stA := compA.state
			pendM, pendP := stA.moneyDelta, stA.peopleDelta
			wantLCT, wantPCP, wantPCM := stA.lastClosedTick, stA.previousClosingPop, stA.previousClosingMoney
			dir := t.TempDir()
			if err := compA.Save(dir); err != nil {
				t.Fatalf("Save: %v", err)
			}
			eB, compB, _, vB := b1142r1Wire(t)
			if err := compB.LoadAt(dir, saveTick); err != nil {
				t.Fatalf("LoadAt: %v", err)
			}
			stB := compB.state
			// saveTick 0: a never-ticked engine's previousClosing trackers are the
			// fresh-Wire zeros; LoadAt's lastClosedTick=-1 instead fires
			// closeLedgerForTick(0), rolling the opening to the restored totals
			// (== Wire's own opening). Benign: the violation + digest checks
			// below are the real tick-0 pins.
			if saveTick > 0 && (stB.previousClosingPop != wantPCP || stB.previousClosingMoney != wantPCM) {
				t.Errorf("trackers after LoadAt: prevClosing pop/money = %d/%d, never-stopped engine = %d/%d (pending deltas money=%d people=%d)", stB.previousClosingPop, stB.previousClosingMoney, wantPCP, wantPCM, pendM, pendP)
			}
			if saveTick > 0 && stB.lastClosedTick != wantLCT {
				t.Errorf("lastClosedTick after LoadAt = %d, never-stopped engine = %d", stB.lastClosedTick, wantLCT)
			}
			if compB.StateDigest() != compA.StateDigest() {
				t.Errorf("load-point digest mismatch")
			}
			cid := errs.NewCorrelationID()
			firstDiv := int64(-1)
			for i := int64(0); i < cont; i++ {
				if err := eA.AdvanceTicks(cid, 1); err != nil {
					t.Fatalf("AdvanceTicks A cont: %v", err)
				}
				if err := eB.AdvanceTicks(cid, 1); err != nil {
					t.Fatalf("AdvanceTicks B cont: %v", err)
				}
				if firstDiv < 0 && (compA.Treasury() != compB.Treasury() || compA.state.citizenWealth != compB.state.citizenWealth) {
					firstDiv = saveTick + i + 1
					t.Errorf("first money divergence after resumed tick count %d (abs tick %d): treasury A %d B %d, citizenWealth A %d B %d", i+1, firstDiv, compA.Treasury(), compB.Treasury(), compA.state.citizenWealth, compB.state.citizenWealth)
				}
			}
			if len(*vA) != 0 {
				t.Errorf("reference engine conservation violations: %v", *vA)
			}
			if len(*vB) != 0 {
				t.Errorf("LoadAt'd engine conservation violations: %v", *vB)
			}
			if compA.Treasury() != compB.Treasury() {
				t.Errorf("treasury diverged: never-stopped %d, loaded %d (delta %d)", compA.Treasury(), compB.Treasury(), compB.Treasury()-compA.Treasury())
			}
			sA, sB := compA.state, compB.state
			if sA.constructionSettledLocal != sB.constructionSettledLocal || sA.constructionSettledExternal != sB.constructionSettledExternal {
				t.Errorf("settled construction diverged: A local/ext %d/%d, B %d/%d", sA.constructionSettledLocal, sA.constructionSettledExternal, sB.constructionSettledLocal, sB.constructionSettledExternal)
			}
			if sA.constructionSettledLocal+sA.constructionSettledExternal == 0 && saveTick < 90 {
				t.Errorf("precondition: no construction ever settled - the sweep would be vacuous for the exactly-once claim")
			}
			if compA.StateDigest() != compB.StateDigest() {
				t.Errorf("digest diverged after %d continued ticks", cont)
			}
		})
	}
}
