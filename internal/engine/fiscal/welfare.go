package fiscal

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// The benefits ledger categories engine.fiscal posts through. These are
// fiscal-owned finance.Category values: engine.finance's Category type is
// open (a string), and these two flows are §54/§40's benefits — no
// engine.finance category exists for them, so this package defines them
// rather than mislabelling a benefit as wages/spend/opex (GR#3: a benefit is
// not a wage).
const (
	catBenefitUnemployment finance.Category = "benefits.unemployment"
	catBenefitHousing      finance.Category = "benefits.housing"
)

// PostUnemploymentSupport posts §40/§54's unemployment support through
// engine.finance's double-entry ledger (the registered engine.fiscal →
// engine.finance edge, AC-7): a treasury → households transfer, a balanced
// internal transfer (money delta zero — conserved). Returns the posted
// transaction ID.
func (f *FiscalAPI) PostUnemploymentSupport(amount finance.Money) (finance.TxID, error) {
	return f.postBenefit("PostUnemploymentSupport", amount, catBenefitUnemployment, "unemployment support")
}

// PostHousingBenefit posts §40/§54's housing benefit through engine.finance's
// double-entry ledger (AC-7): a treasury → households transfer, a balanced
// internal transfer (money delta zero — conserved).
func (f *FiscalAPI) PostHousingBenefit(amount finance.Money) (finance.TxID, error) {
	return f.postBenefit("PostHousingBenefit", amount, catBenefitHousing, "housing benefit")
}

// postBenefit is the shared benefit-posting path: it validates the amount,
// requires the finance dependency, and posts one balanced treasury→households
// transfer tagged with the benefit category.
func (f *FiscalAPI) postBenefit(method string, amount finance.Money, cat finance.Category, desc string) (finance.TxID, error) {
	if err := f.checkNotCopied(method); err != nil {
		return 0, err
	}
	if amount < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": desc, "value": int64(amount)})
	}
	fin, err := f.requireFinance(method)
	if err != nil {
		return 0, err
	}
	if amount == 0 {
		return 0, nil
	}
	return fin.Post(finance.Transaction{
		Description: desc,
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: amount, Category: cat},
			{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: amount, Category: cat},
		},
	})
}

// PostSocialHousingBuild posts the social-housing build programme's
// construction cost (AC-7): it reuses engine.finance's construction-settlement
// stage — FinanceAPI.SettleConstruction, the same stage §7's ordinary
// construction posts through — rather than a parallel fiscal-only construction
// cost model (GR#3 reuse-first). Returns the settled cost.
func (f *FiscalAPI) PostSocialHousingBuild(cost finance.Money) (finance.Money, error) {
	if err := f.checkNotCopied("PostSocialHousingBuild"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "socialHousingBuildCost", "value": int64(cost)})
	}
	fin, err := f.requireFinance("PostSocialHousingBuild")
	if err != nil {
		return 0, err
	}
	return fin.SettleConstruction(cost)
}
