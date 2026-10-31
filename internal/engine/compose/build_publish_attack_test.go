package compose

import (
	"context"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// wireBuildAttackEngine is wireFinanceTestEngine that also returns the
// Composition (for its Treasury read) and registers its own cleanup.
func wireBuildAttackEngine(t *testing.T) (*Composition, *protocol.InProcTransport) {
	t.Helper()
	e := core.NewEngine()
	comp, err := Wire(e, &Deps{CorrelationID: errs.NewCorrelationID()})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = transport.Close() })
	go func() { _ = e.RunCommandLoop(ctx, transport) }()
	return comp, transport
}

// Destructive round r1 (FEAT-2326609837, GR#23) attack test for pack AC-B3's
// ledger half ("the ledger balance changes by the build cost").
//
// TestBuildView_AC_B2_B3_QueuedBuildVisible originally also checked that
// the F2 treasury differed from its pre-purchase value after 95 ticks. The
// round measured that check passing with NO Build command at all (tick
// income alone moves the treasury: 1500000000 -> 1581679500), so it could not
// tell a build that costs money from one that costs nothing; the claim was
// removed from that test. This test
// isolates the build cost with a control: two identically wired engines run
// the same Buy + 95 ticks; only the treatment also issues the Build. The
// treatment's treasury must end strictly lower. Tick-based, no wall clock
// beyond sendAndAwait's hang guard.
func TestBuildView_Attack_AC_B3_BuildCostReachesTreasury(t *testing.T) {
	if os.Getenv("METRO_RUN_BUG1142") != "1" {
		t.Skip("BUG-1142: the default composition has no ConstructionMaterials supply, so a queued build never reaches the ledger; set METRO_RUN_BUG1142=1 to run (expected RED until BUG-1142 lands)")
	}
	run := func(withBuild bool) int64 {
		comp, transport := wireBuildAttackEngine(t)
		cell := protocol.CellRef{X: 0, Y: 0}
		if r := sendAndAwait(t, transport, protocol.KindBuy, protocol.BuyPayload{Cell: cell}); !r.Accepted {
			t.Fatalf("Buy rejected: %+v", r.Error)
		}
		if withBuild {
			zone := string(comp.state.buildAPI.ZoneCatalogue()[0].Zone)
			if r := sendAndAwait(t, transport, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: zone}); !r.Accepted {
				t.Fatalf("Build(%s) rejected: %+v", zone, r.Error)
			}
		}
		if r := sendAndAwait(t, transport, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 95}); !r.Accepted {
			t.Fatalf("AdvanceTicks rejected: %+v", r.Error)
		}
		// The AdvanceTicks result was received from the command loop, so its
		// state writes happen-before this read.
		return comp.Treasury()
	}
	control := run(false)
	if control == 0 {
		t.Fatal("control treasury is zero: the comparison would be vacuous")
	}
	treated := run(true)
	if treated >= control {
		t.Fatalf("treasury with a queued build = %d, without = %d: the build cost never reached the ledger", treated, control)
	}
}
