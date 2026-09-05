package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug689_g1_drain_binding_test.go — BUG-689 round follow-up G1
// (non-vacuity): a BEHAVIORAL test that reds if compose's drain injection
// (deathServicesAPI.WireDrainCapacity, compose.go) is ever removed or
// silently no-op'd.
//
// FINDING F4/G2's own numeric example is reused directly: a
// deathservices.DeathServicesAPI with exactly one registered crematorium
// and no cemetery reports MonthlyDrainCapacity == 660 (360 cremation
// headroom @ 12/day*30 + 300 remaining hearse budget, both from
// data/deathservices.json's defaults) — strictly MORE than the OLD
// hand-rolled compose closure's static 300, which is exactly why F4 called
// the two implementations "numerically identical today only because F2
// keeps the composed engine at zero crematoria" and diverging the instant
// a crematorium exists.
//
// This test proves the *citizens death queue itself* — not just the two
// standalone numbers — actually obeys the module's LIVE capacity once
// wired the way compose.go now wires it (WireDrainCapacity), and that
// tearing the wiring back out (SetDrainCapacity(nil, ...), the same
// no-op every pre-BUG-689 DeathQueue defaults to) changes the OBSERVABLE
// release count. If a future edit reverts compose to a static constant,
// or drops the wiring call entirely, this test reds because the "wired"
// run stops differing from the "no-op" run.
func TestBUG689_G1_WiredDrainCapacityBindsDeathQueueRelease(t *testing.T) {
	cid := errs.NewCorrelationID()

	// One crematorium, zero cemeteries: MonthlyDrainCapacity == 660 per
	// F4's own pinned arithmetic (cremationCapacity 360 + hearseRemaining
	// 300 + plotCapacity 0). Confirmed directly rather than hardcoded, so a
	// data-file edit changes this test's own expectation instead of
	// silently invalidating it.
	ds, err := deathservices.LoadDefault(cid)
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if err := ds.RegisterCrematorium("crem-1", cid); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	moduleCapacity := ds.MonthlyDrainCapacity(0)
	if moduleCapacity <= 0 {
		t.Fatalf("test setup invalid: module capacity with one crematorium is %d, want > 0", moduleCapacity)
	}

	// A queue with far more pending deaths than moduleCapacity, and an
	// ordinary budget far larger still, so moduleCapacity — not the queue
	// size or the ordinary budget — is the ONLY thing that can bind.
	const queued = 2000
	const hugeBudget = 100000
	if queued <= moduleCapacity {
		t.Fatalf("test setup invalid: queued=%d must exceed moduleCapacity=%d for the bound to be observable", queued, moduleCapacity)
	}
	buildQueue := func(t *testing.T) *citizens.DeathQueue {
		t.Helper()
		q := citizens.NewDeathQueue()
		for id := uint64(1); id <= uint64(queued); id++ {
			if err := q.Enqueue(id, 1, cid); err != nil {
				t.Fatalf("Enqueue(%d): %v", id, err)
			}
		}
		return q
	}
	cfg := citizens.MortalityConfig{
		Params: citizens.MortalityParams{
			MonthlyDeathBudget: citizens.MortalityNumber{Value: hugeBudget, Unit: "deaths/month", Disclosure: "test"},
		},
	}

	// --- WIRED: exactly compose's own call shape (WireDrainCapacity's
	// underlying mechanism — DeathServicesAPI implements citizens.
	// DrainCapacity directly, and SetDrainCapacity is DeathQueue's half of
	// CitizensAPI.SetDeathDrainCapacity, the seam WireDrainCapacity calls
	// through). Release must be bounded at exactly moduleCapacity.
	qWired := buildQueue(t)
	if err := qWired.SetDrainCapacity(ds, cid); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	releasedWired := qWired.RealiseDrained(cfg, false, 2, cid)
	if len(releasedWired) != moduleCapacity {
		t.Fatalf("wired release = %d, want exactly the module's live capacity %d — the drain injection is not binding (compose's WireDrainCapacity wiring may have been removed or no-op'd)", len(releasedWired), moduleCapacity)
	}

	// --- NO-OP: the pre-BUG-689 default (no drain consumer at all, the
	// exact state a reverted/no-op'd wiring would leave the queue in).
	// Release must be bounded ONLY by the huge ordinary budget and the
	// queue size, i.e. strictly greater than moduleCapacity.
	qNoDrain := buildQueue(t)
	releasedNoDrain := qNoDrain.RealiseDrained(cfg, false, 2, cid)
	if len(releasedNoDrain) != queued {
		t.Fatalf("no-drain release = %d, want exactly queued=%d (unbounded by any drain)", len(releasedNoDrain), queued)
	}

	// The load-bearing comparison: wiring the module's live capacity in
	// must produce a STRICTLY SMALLER release than leaving it unwired. A
	// future change that makes these equal (a no-op wiring, or a drain
	// that always reports "unlimited") is exactly what this test exists to
	// catch.
	if len(releasedWired) >= len(releasedNoDrain) {
		t.Fatalf("drain injection made no observable difference: wired=%d no-drain=%d — WireDrainCapacity is a no-op", len(releasedWired), len(releasedNoDrain))
	}
}
