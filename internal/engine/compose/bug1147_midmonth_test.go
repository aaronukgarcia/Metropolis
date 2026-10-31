package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG1147_MidMonthSaveLoadStaysTickContinuous: save at tick 3 (mid-month),
// LoadAt(3) into a fresh composition, step both one tick at a time to tick 33
// and require the digest AND every participant stream to match at every tick.
func TestBUG1147_MidMonthSaveLoadStaysTickContinuous(t *testing.T) {
	for _, pool := range []int{1, 4} {
		for _, play := range []bool{false, true} {
			runBUG1147(t, pool, play)
		}
	}
}

func playCommands(t *testing.T, e *core.Engine) {
	t.Helper()
	cells := []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}}
	mk := func(k protocol.Kind, p protocol.CommandPayload) protocol.Command {
		return protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "b1147", Kind: k, Payload: p}
	}
	if res := e.HandleCommand(mk(protocol.KindBuy, protocol.BuyPayload{Cell: cells[0]})); !res.Accepted {
		t.Fatalf("Buy: %+v", res.Error)
	}
	for _, c := range cells {
		if res := e.HandleCommand(mk(protocol.KindZone, protocol.ZonePayload{Cell: c, ZoneType: "dwelling"})); !res.Accepted {
			t.Fatalf("Zone: %+v", res.Error)
		}
		if res := e.HandleCommand(mk(protocol.KindBuild, protocol.BuildPayload{Cell: c, BuildingType: "dwelling"})); !res.Accepted {
			t.Fatalf("Build: %+v", res.Error)
		}
	}
}

func runBUG1147(t *testing.T, pool int, play bool) {
	const saveTick, endTick = int64(3), int64(33)
	eRef, cRef := bugComposition(t, pool)
	if play {
		playCommands(t, eRef)
	}
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := cRef.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	eB, cB := bugComposition(t, pool)
	if err := cB.LoadAt(dir, saveTick); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if d := diffParticipants(t, cRef, cB); len(d) != 0 {
		t.Fatalf("diverged immediately after load: %v", d)
	}
	for tk := saveTick + 1; tk <= endTick; tk++ {
		if err := eRef.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatal(err)
		}
		if err := eB.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatal(err)
		}
		d := diffParticipants(t, cRef, cB)
		eq := cRef.StateDigest() == cB.StateDigest()
		if len(d) != 0 || !eq {
			t.Fatalf("pool=%d play=%v tick %d: diverging participants=%v digestEqual=%v", pool, play, tk, d, eq)
		}
	}
}

// TestBUG1147_ProbePlainLoadDivergence is a diagnostic probe (kept as the
// documented-contrast control): the SAME scenario via plain Load (clock not
// seeded) names the first diverging tick/participants.
func TestBUG1147_ProbePlainLoadDivergence(t *testing.T) {
	const saveTick = int64(3)
	eRef, cRef := bugComposition(t, 1)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := cRef.Save(dir); err != nil {
		t.Fatal(err)
	}
	eB, cB := bugComposition(t, 1)
	if err := cB.Load(dir); err != nil {
		t.Fatal(err)
	}
	for tk := saveTick + 1; tk <= saveTick+40; tk++ {
		_ = eRef.AdvanceTicks(errs.NewCorrelationID(), 1)
		_ = eB.AdvanceTicks(errs.NewCorrelationID(), 1)
		d := diffParticipants(t, cRef, cB)
		if len(d) != 0 || cRef.StateDigest() != cB.StateDigest() {
			t.Logf("plain Load: first divergence at ref tick %d: %v", tk, d)
			return
		}
	}
	t.Logf("plain Load: no divergence in 40 ticks")
}
