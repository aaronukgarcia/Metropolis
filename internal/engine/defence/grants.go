package defence

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// grantCategory is the ledger category this package tags central-grant money
// with. finance.Category is an open string type; the predefined set
// (wages/spend/tax.*/opex/...) has no grants category, and §54 names
// "central grants (§55)" as a money-in producer, so this package introduces
// the "grants" category value. Documented here because it is a new category
// name in finance's namespace.
const grantCategory finance.Category = "grants"

// bidPurpose is the det.Stream purpose tag for grant-bid outcome draws
// (AC-13): a fixed literal, never string-built from input.
const bidPurpose = "grant-bid"

// BidForGrant evaluates a competitive grant bid (AC-2). The win probability
// rises with match funding and, independently, with the pushed planning
// quality ([DefenceAPI.SetPlanningQuality]); the win/lose roll is drawn from
// the counter-based hash stream det.NewStream(worldSeed, bidID, month,
// "grant-bid") — no shared/global RNG (AC-13). On a win the award is
// credited through engine.finance's double-entry ledger (grant money in).
//
// A bid is rejected with the refusal-specific [ErrGrantRefused] when a
// mandate refusal is in effect (AC-6), and with [ErrUndeclaredPot] for a pot
// id that is not one of the data/defence.json pots (AC-11).
func (d *DefenceAPI) BidForGrant(req GrantBid) (GrantResult, error) {
	if err := d.checkNotCopied("BidForGrant"); err != nil {
		return GrantResult{}, err
	}
	if req.Month < 0 {
		return GrantResult{}, errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "month", "value": req.Month})
	}
	if req.MatchFunding < 0 {
		return GrantResult{}, errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "matchFunding", "value": int64(req.MatchFunding)})
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// AC-6: a refusal in effect gates every subsequent bid with a distinct,
	// refusal-specific code — not a generic funding-shortage rejection.
	if len(d.refusedMandates) > 0 {
		return GrantResult{}, errs.New(ErrGrantRefused, d.correlationID, map[string]any{"pot": req.Pot})
	}

	pot, ok := d.potByID(req.Pot)
	if !ok {
		return GrantResult{}, errs.New(ErrUndeclaredPot, d.correlationID, map[string]any{"pot": req.Pot})
	}

	p := d.bidWinProbability(pot, req.MatchFunding)
	d.bidSeq++
	stream := det.NewStream(d.worldSeed, d.bidSeq, req.Month, bidPurpose)
	won := stream.Float64() < p

	res := GrantResult{Pot: req.Pot, Won: won, WinProbability: p}
	if !won {
		return res, nil
	}

	award := finance.Money(pot.AwardMicropounds)
	res.Award = award
	// The award is credited through the ledger only on a win; a lost bid
	// posts nothing (the match funding is committed, not spent, and the
	// speculative bid cost is out of scope at Baseline One).
	if d.finance == nil {
		return GrantResult{}, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"operation": "BidForGrant", "dependency": "engine.finance"})
	}
	if err := postGrant(d.finance, req.Month, award, "grant award: "+pot.Name); err != nil {
		return GrantResult{}, err
	}
	return res, nil
}

// bidWinProbability computes the AC-2 win probability for a pot and a
// match-funding amount: base + matchFundingWeight × (matchFunding / maxMatch)
// + planningQualityWeight × planningQuality, clamped to [0,1]. Both the
// match-funding term and the planning-quality term are strictly increasing,
// so a test that varies exactly one of them (holding the other fixed) must
// observe the probability move in the documented direction.
func (d *DefenceAPI) bidWinProbability(pot GrantPotConfig, matchFunding finance.Money) float64 {
	matchNorm := 0.0
	if matchFunding > 0 && pot.MaxMatchMicropounds > 0 {
		matchNorm = float64(matchFunding) / float64(pot.MaxMatchMicropounds)
		if matchNorm > 1 {
			matchNorm = 1 // a bid above the max match is treated as full match
		}
	}
	p := pot.BaseWinProbability +
		pot.MatchFundingWeight*matchNorm +
		pot.PlanningQualityWeight*d.planningQuality
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// FormulaSupport returns the low-tax-capacity formula-support amount (AC-3):
// unconditional, non-competitive funding available without a grant bid when
// tax capacity is below the data-sourced threshold, and zero at/above it. It
// is a query, not a command — there is no bid and no win/lose draw.
func (d *DefenceAPI) FormulaSupport(taxCapacity finance.Money) finance.Money {
	if err := d.checkNotCopied("FormulaSupport"); err != nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if taxCapacity < finance.Money(d.cfg.FormulaSupport.TaxCapacityThresholdMicropounds) {
		return finance.Money(d.cfg.FormulaSupport.FormulaAmountMicropounds)
	}
	return 0
}

// potByID returns the grant-pot config for a pot id (caller holds d.mu).
func (d *DefenceAPI) potByID(id string) (GrantPotConfig, bool) {
	for _, p := range d.cfg.GrantPots {
		if p.ID == id {
			return p, true
		}
	}
	return GrantPotConfig{}, false
}

// postGrant posts a balanced central-grant inflow through engine.finance:
// debit the outside world (RoleExternal — the source) and credit the city
// treasury (RoleMoney — money in). The transaction is balanced by
// construction (equal debit and credit), and finance.Post's own validation
// re-checks it. The caller already holds d.mu; finance acquires its own lock
// and never calls back into this package, so there is no lock-order cycle.
func postGrant(f *finance.FinanceAPI, month int64, amount finance.Money, description string) error {
	_, err := f.Post(finance.Transaction{
		Month:       month,
		Description: description,
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: grantCategory},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: grantCategory},
		},
	})
	return err
}
