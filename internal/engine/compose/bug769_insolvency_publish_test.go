package compose

import (
	"context"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uifinance "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// bug769_insolvency_publish_test.go — BUG-769 (P1, built-but-not-wired
// layer 2): BUG-759 made FinanceAPI.InsolvencyMonths()/IsInsolvent() run
// for real every month, but nothing READ them. Two increments landed here:
//
//  1. The "f2.finance" wire patch publishes InsolvencyMonths/Insolvent
//     directly from FinanceAPI, live, through the real tick loop
//     (InsolvencyStatus(), one RLock, per round finding F1/F4 below).
//  2. Once the Architect registered feat.compositionroot -> engine.spiral
//     (docs/planning/master-plan-v2.1.json / code.json), the SAME publish
//     function (finance_publish.go's buildFinanceBalanceSheetPatch) also
//     calls engine.spiral's DecayAPI.EvaluateInsolvency(st.finance) LIVE,
//     every publish tick, and surfaces its DeathVerdict as
//     insolvencyVerdict beside Insolvent. EvaluateInsolvency itself is a
//     pure reader (spiral/death.go) with no enforcement of its own; there
//     is no game-over/halt state anywhere in the composed engine yet for a
//     DeathInsolvency verdict to trigger — that remains a SEPARATE policy
//     item, deliberately not invented here.
//
// Round REJECT (opus-round-bug769, F1): the first landing mirrored the
// verdict into an atomic (insolvencyVerdictPub), written only at a month
// boundary, while Insolvent/InsolvencyMonths were read live on every
// publish — a Save(solvent)->starve->Load sequence (finance's own
// resetForLoad participant zeroes insolvencyMonths/gameOver on Load;
// nothing reset the mirror) published a STALE insolvencyVerdict=
// "insolvency" alongside a fresh insolvent=false/months=0 on the SAME
// patch. Fixed per the round's own recommendation: the mirror is DELETED;
// EvaluateInsolvency is called live inside buildFinanceBalanceSheetPatch
// instead, so it can never go stale relative to the other two fields.
// attack_bug769_round_test.go (this package) pins both this fix
// (TestAttackBUG769_StaleVerdictAfterFinanceStateCleared) and the honest,
// still-latching post-game-over shape (F2/F3,
// TestAttackBUG769_RecoveryAfterGameOver) as permanent regressions.
//
// Reuses bug759_recordmonth_test.go's own starve/fund fixture helpers
// (wireBUG759/starveBUG759) rather than re-deriving a duplicate.

// bug769Seed is this file's own dedicated seed, distinct from every other
// test file's in this package (per this package's own convention).
const bug769Seed = uint64(769001)

// TestBUG769_ThreeStarvedMonthsPublishInsolventOnTheWire is the primary
// rule proof: driving a starved city (starveBUG759's own real-posting
// fixture) through three real consecutive months via AdvanceTicks commands
// results in a "f2.finance" delta whose InsolvencyMonths/Insolvent fields
// — decoded through ui.screen.finance's OWN real ApplyDelta/Insolvency()
// path, never a hand-rolled re-implementation — read >=3 / true. Before
// this ticket's fix, FinanceAPI.InsolvencyMonths()/IsInsolvent() advanced
// internally (BUG-759) but the wire patch never carried either field, so
// this decode would have reported have=false forever regardless of how
// bankrupt the city became.
func TestBUG769_ThreeStarvedMonthsPublishInsolventOnTheWire(t *testing.T) {
	cid := errs.NewCorrelationID()
	api, err := citizens.NewCitizensAPI(bug769Seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(bug769Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api, DeathServiceCrematoria: []string{"crem-769"}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	f := comp.state.finance
	starveBUG759(t, f)

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)
	scr := uifinance.New(errs.NewCorrelationID())
	subID := delta.SubscriptionID
	scr.BindSubscription(subID)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: subID, Patch: delta.Patch})

	// Precondition: a fresh, just-starved city has not yet been evaluated
	// for a single month — no insolvency signal at all yet is acceptable
	// (InsolvencyMonths() itself starts at 0), so no assertion on the very
	// first delta's insolvency fields beyond "the fixture is sane" is made
	// here; the real proof is after three real months below.
	if f.IsInsolvent() {
		t.Fatal("fixture error: must not already be insolvent before any month elapses")
	}

	for i := 0; i < 3; i++ {
		if err := transport.SendCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: core.DailyTicksPerMonth},
		}); err != nil {
			t.Fatalf("SendCommand(AdvanceTicks month %d): %v", i, err)
		}
		select {
		case r := <-transport.Results():
			if !r.Accepted {
				t.Fatalf("AdvanceTicks month %d rejected: %+v", i, r.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for AdvanceTicks month %d result", i)
		}

		// Drain every delta produced by this month's ticks, applying each
		// in order so the Screen ends up with the LATEST published state
		// (the pump may coalesce or emit more than one delta per command).
		draining := true
		for draining {
			select {
			case d := <-transport.Deltas():
				scr.ApplyDelta(protocol.Delta{SubscriptionID: subID, Patch: d.Patch})
			case <-time.After(500 * time.Millisecond):
				draining = false
			}
		}
	}

	if got := f.InsolvencyMonths(); got < 3 {
		t.Fatalf("BUG-769 fixture error: FinanceAPI.InsolvencyMonths() = %d after three starved months, want >= 3 (BUG-759's own wiring)", got)
	}
	if !f.IsInsolvent() {
		t.Fatal("BUG-769 fixture error: FinanceAPI.IsInsolvent() = false after three starved months, want true (BUG-759's own wiring)")
	}

	insolvency, have := scr.Insolvency()
	if !have {
		t.Fatal("BUG-769: ui.screen.finance Screen.Insolvency() reported have=false after three starved months' worth of deltas — the wire patch never published the field")
	}
	if insolvency.Months < 3 {
		t.Fatalf("BUG-769: decoded Insolvency().Months = %d, want >= 3", insolvency.Months)
	}
	if !insolvency.Insolvent {
		t.Fatal("BUG-769: decoded Insolvency().Insolvent = false, want true after three starved months")
	}

	// Second increment: engine.spiral's OWN read of the same signal, via
	// the now-registered feat.compositionroot -> engine.spiral edge.
	// EvaluateInsolvency returns DeathInsolvency (String() "insolvency")
	// once FinanceAPI.IsInsolvent() is true — proven here through the real
	// wire round trip, not a direct spiral.EvaluateInsolvency unit call.
	//
	// RED-PROOF (confirmed by hand this round, per the coordinator's
	// instruction — not left in the tree): disabling
	// finance_publish.go's `if st.spiral != nil { ... }` block inside
	// buildFinanceBalanceSheetPatch (the block that calls
	// EvaluateInsolvency and sets insolvencyVerdict) makes this exact
	// assertion FAIL — insolvencyVerdict stays nil (omitted from the wire
	// via its `omitempty` tag), so the decoded Verdict is the empty
	// string, not "insolvency" — while
	// TestBUG769_ThreeStarvedMonthsPublishInsolventOnTheWire's other
	// assertions (Insolvent, Months) stay green, proving this assertion
	// uniquely pins the live EvaluateInsolvency call.
	if insolvency.Verdict != "insolvency" {
		t.Fatalf("BUG-769: decoded Insolvency().Verdict = %q, want %q (engine.spiral.EvaluateInsolvency's own verdict, via the registered feat.compositionroot -> engine.spiral edge)", insolvency.Verdict, "insolvency")
	}
}
