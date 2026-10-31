package compose

// Destructive round r1 (FEAT-2326609836) finding F1, adopted as the r2 pin:
// the attacker's 200-bid spam through the REAL command path. Bids are free, so
// without a per-pot monthly cap and an affordability check they mint unlimited
// grant money. Pinned: unaffordable match -> zero credit; affordable match ->
// at most one accepted bid per pot per calendar month.

import (
	"strconv"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func r1SpamRig(t *testing.T) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(defenceTestSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{})
	if err != nil {
		t.Fatal(err)
	}
	advanceInChunks(t, e, 3)
	return e, comp
}

func r1Spam(e *core.Engine, pot string, match int64, n int) (accepted int) {
	for i := 0; i < n; i++ {
		res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID: protocol.CorrelationID("spam" + pot + strconv.Itoa(i)),
			Kind:          protocol.KindBidForGrant,
			Payload:       protocol.BidForGrantPayload{Pot: pot, MatchFundingMicropounds: match}})
		if res.Accepted {
			accepted++
		}
	}
	return accepted
}

// Unaffordable match (far above any treasury): nothing accepted, zero credit.
func TestR1Pin_SpamUnaffordableMintsNothing(t *testing.T) {
	e, comp := r1SpamRig(t)
	fin := comp.state.finance
	tre0, m0 := int64(comp.Treasury()), int64(fin.TotalMoneyInCirculation())
	match := tre0 + 1_000_000_000
	acc := r1Spam(e, "transport", match, 200)
	if acc != 0 || int64(comp.Treasury()) != tre0 || int64(fin.TotalMoneyInCirculation()) != m0 {
		t.Fatalf("unaffordable spam: accepted=%d treasury %d->%d money %d->%d", acc, tre0, comp.Treasury(), m0, fin.TotalMoneyInCirculation())
	}
	if comp.state.defenceW.api.BidSeq() != 0 {
		t.Fatalf("refused bids advanced the draw counter: %d", comp.state.defenceW.api.BidSeq())
	}
}

// Affordable match: across every real pot, 200 same-month bids each, at most
// one per pot is accepted, so credit <= sum of pot awards.
func TestR1Pin_SpamAffordableCappedOnePerPotPerMonth(t *testing.T) {
	e, comp := r1SpamRig(t)
	fin := comp.state.finance
	tre0 := int64(comp.Treasury())
	pots := comp.state.defenceW.api.Pots()
	var maxCredit int64
	totalAccepted := 0
	for _, p := range pots {
		maxCredit += p.AwardMicropounds
		acc := r1Spam(e, p.ID, 1, 200)
		if acc > 1 {
			t.Fatalf("pot %s accepted %d bids in one month (cap is 1)", p.ID, acc)
		}
		totalAccepted += acc
	}
	gain := int64(comp.Treasury()) - tre0
	t.Logf("capped spam: %d pots, %d accepted of %d, credit %d (max %d)", len(pots), totalAccepted, 200*len(pots), gain, maxCredit)
	if gain > maxCredit || gain < 0 {
		t.Fatalf("credit %d exceeds one-win-per-pot bound %d", gain, maxCredit)
	}
	if totalAccepted != len(pots) {
		t.Fatalf("expected exactly one accepted bid per pot, got %d for %d pots", totalAccepted, len(pots))
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation: %v", v)
	}
}
