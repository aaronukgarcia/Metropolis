package compose

import (
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ev83 is the BUG-1214 default-composition harness (the same Wire the real
// boot uses), driven only through protocol commands.
type ev83 struct {
	t         *testing.T
	comp      *Composition
	transport *protocol.InProcTransport
}

func ev83New(t *testing.T) *ev83 {
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
	return &ev83{t: t, comp: comp, transport: transport}
}

func (r *ev83) advance(n int64) {
	if res := sendAndAwait(r.t, r.transport, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: n}); !res.Accepted {
		r.t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}
}

func (r *ev83) buildDwellings(n int) {
	if res := sendAndAwait(r.t, r.transport, protocol.KindBuy, protocol.BuyPayload{Cell: protocol.CellRef{X: 0, Y: 0}}); !res.Accepted {
		r.t.Fatalf("Buy rejected: %+v", res.Error)
	}
	for i := 0; i < n; i++ {
		c := protocol.CellRef{X: i, Y: 0}
		if res := sendAndAwait(r.t, r.transport, protocol.KindZone, protocol.ZonePayload{Cell: c, ZoneType: "dwelling"}); !res.Accepted {
			r.t.Fatalf("Zone %d rejected: %+v", i, res.Error)
		}
		if res := sendAndAwait(r.t, r.transport, protocol.KindBuild, protocol.BuildPayload{Cell: c, BuildingType: "dwelling"}); !res.Accepted {
			r.t.Fatalf("Build %d rejected: %+v", i, res.Error)
		}
	}
}

// newUncappedTestEngine is newTestEngine with the BUG-1214 housing cap lifted
// (a huge seed home stock), for fixtures that are about migration, emigration
// or growth rather than housing.
func newUncappedTestEngine(t *testing.T, seed uint64, opts ...invariant.HookOption) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{InvariantOpts: opts, Coastal: quietCoastal(t), SocialQuiet: true, SeedHomeCapacityPeople: 1 << 40})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}
