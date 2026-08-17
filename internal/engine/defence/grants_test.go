package defence

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestGrantWinRate_IncreasesWithMatchFunding holds planning quality fixed and
// varies match funding, asserting the win probability strictly increases
// (AC-2a): the match-funding term is not decorative.
func TestGrantWinRate_IncreasesWithMatchFunding(t *testing.T) {
	d, _ := newGrantDefence(t, 1)
	if err := d.SetPlanningQuality(0.5); err != nil {
		t.Fatalf("SetPlanningQuality: %v", err)
	}
	// BidForGrant's probability is inspectable via the returned result, but
	// the draw consumes a stream position — compare probabilities for two
	// bids at the same planning quality and differing match funding.
	low, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 0, Month: 0})
	if err != nil {
		t.Fatalf("BidForGrant(low): %v", err)
	}
	high, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 500_000_000, Month: 0})
	if err != nil {
		t.Fatalf("BidForGrant(high): %v", err)
	}
	if high.WinProbability <= low.WinProbability {
		t.Fatalf("win probability did not increase with match funding: low=%v high=%v", low.WinProbability, high.WinProbability)
	}
}

// TestPlanningQualityInput_MovesWinRate holds match funding fixed and varies
// the pushed planning-quality input, asserting the win probability moves in
// the documented direction (AC-2b): the input is not decorative — it
// independently drives the probability.
func TestPlanningQualityInput_MovesWinRate(t *testing.T) {
	d, _ := newGrantDefence(t, 1)
	if err := d.SetPlanningQuality(0.0); err != nil {
		t.Fatalf("SetPlanningQuality(0): %v", err)
	}
	low, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 0, Month: 0})
	if err != nil {
		t.Fatalf("BidForGrant(low q): %v", err)
	}
	if err := d.SetPlanningQuality(1.0); err != nil {
		t.Fatalf("SetPlanningQuality(1): %v", err)
	}
	high, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 0, Month: 0})
	if err != nil {
		t.Fatalf("BidForGrant(high q): %v", err)
	}
	if high.WinProbability <= low.WinProbability {
		t.Fatalf("win probability did not increase with planning quality: low=%v high=%v", low.WinProbability, high.WinProbability)
	}
}

// TestFormulaSupport_LowCapacityReceivesWithoutBid asserts a low-tax-capacity
// city receives formula support with NO bid submitted, and a high-tax-capacity
// city above the threshold receives none (AC-3) — formula support is distinct
// from the competitive pots.
func TestFormulaSupport_LowCapacityReceivesWithoutBid(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	cfg := validConfig()

	low := d.FormulaSupport(finance.Money(cfg.FormulaSupport.TaxCapacityThresholdMicropounds - 1))
	if low != finance.Money(cfg.FormulaSupport.FormulaAmountMicropounds) {
		t.Fatalf("FormulaSupport(below threshold) = %d, want %d", int64(low), cfg.FormulaSupport.FormulaAmountMicropounds)
	}

	high := d.FormulaSupport(finance.Money(cfg.FormulaSupport.TaxCapacityThresholdMicropounds))
	if high != 0 {
		t.Fatalf("FormulaSupport(at threshold) = %d, want 0", int64(high))
	}
}

// TestFormulaSupport_ZeroAtHighCapacity asserts a city well above the
// threshold receives no formula support (AC-3's other half).
func TestFormulaSupport_ZeroAtHighCapacity(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	if got := d.FormulaSupport(finance.Money(1_000_000_000_000)); got != 0 {
		t.Fatalf("FormulaSupport(high) = %d, want 0", int64(got))
	}
}

// TestUndeclaredPot_Rejected asserts a grant bid against an undeclared pot
// returns the registry-sourced ErrUndeclaredPot (GR#7, AC-11) — and that no
// bid is silently accepted as a no-op.
func TestUndeclaredPot_Rejected(t *testing.T) {
	d, f, _ := newWiredDefence(t, 1)
	before := f.TotalMoneyInCirculation()
	_, err := d.BidForGrant(GrantBid{Pot: "not-a-real-pot", MatchFunding: 1_000, Month: 0})
	isErr(t, err, ErrUndeclaredPot)
	if after := f.TotalMoneyInCirculation(); after != before {
		t.Fatalf("undeclared-pot bid changed money stock: before=%d after=%d", int64(before), int64(after))
	}
}

// TestGrantWinRate_DrawIsDeterministic asserts the same seed + same bid
// sequence produces the same win/lose outcome (AC-13's counter-based hash
// stream, no shared RNG).
func TestGrantWinRate_DrawIsDeterministic(t *testing.T) {
	run := func() []bool {
		d, _ := newGrantDefence(t, 42)
		var out []bool
		for i := 0; i < 20; i++ {
			r, err := d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 100_000_000, Month: int64(i)})
			if err != nil {
				t.Fatalf("BidForGrant: %v", err)
			}
			out = append(out, r.Won)
		}
		return out
	}
	a := run()
	b := run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("bid %d diverged across identical runs: %v vs %v", i, a[i], b[i])
		}
	}
}
