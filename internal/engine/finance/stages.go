package finance

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BasisPoints is a fixed-point rate: 1 basis point = 0.01%, so 10000 bp
// = 100%. Rates are int64 everywhere — a tax or interest rate is never a
// float, matching the package-wide "no float money" rule (AC-2).
type BasisPoints int64

// basisPointScale is the denominator: rate / basisPointScale is the
// fraction. 10000 bp = 100%.
const basisPointScale int64 = 10000

// apply returns amount * rate / basisPointScale (truncated toward zero),
// the fixed-point "rate of amount" used by tax and interest.
func (r BasisPoints) apply(amount Money) Money {
	return Money(mulDiv(int64(amount), int64(r), basisPointScale))
}

// taxOf is a shorthand for a rate applied to a base figure.
func taxOf(base Money, rate BasisPoints) Money { return rate.apply(base) }

// PostWages posts household income (the aggregate wage bill) as a
// transfer from the city treasury to the household account, and returns
// the posted amount. It is stage (1) of the §7 money-flow chain —
// wages→spend→tax→budget→opex/imports/debt/construction (AC-3).
func (f *FinanceAPI) PostWages(total Money) (Money, error) {
	if err := f.checkNotCopied("PostWages"); err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "wages", "amount": int64(total)})
	}
	if _, err := f.Post(Transaction{
		Description: "household wages by sector/skill",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: total, Category: CatWages},
			{Account: AcctHouseholds, Side: SideCredit, Amount: total, Category: CatWages},
		},
	}); err != nil {
		return 0, err
	}
	return total, nil
}

// PostHouseholdSpend posts the household purchase of a consumed quantity
// at a unit price: households pay firms quantity × price (micro-pounds),
// and the spend amount is returned. It is stage (2) of the chain — a
// distinct, visible link between wages and tax, never folded into wages
// (AC-3's false-pass framing). quantity is in the commodity's own unit
// (data/market.json's "unit" field); price is micro-pounds per unit.
func (f *FinanceAPI) PostHouseholdSpend(quantity int64, price Money) (Money, error) {
	if err := f.checkNotCopied("PostHouseholdSpend"); err != nil {
		return 0, err
	}
	if quantity < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "spend.quantity", "amount": quantity})
	}
	if price < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "spend.price", "amount": int64(price)})
	}
	spend := Money(mulDiv(quantity, int64(price), 1))
	if _, err := f.Post(Transaction{
		Description: "household spend on consumed commodities/utilities",
		Entries: []Entry{
			{Account: AcctHouseholds, Side: SideDebit, Amount: spend, Category: CatSpend},
			{Account: AcctFirms, Side: SideCredit, Amount: spend, Category: CatSpend},
		},
	}); err != nil {
		return 0, err
	}
	return spend, nil
}

// PostHouseholdSpendAtMarket is PostHouseholdSpend with the unit price
// resolved from engine.market (a registered consumer edge, GR#20): it
// looks up commodity's static price via MarketAPI.Price and posts
// quantity × price. It is the composition-root-facing form of the spend
// stage; PostHouseholdSpend remains the price-injected form tests and
// callers with their own price source use.
func (f *FinanceAPI) PostHouseholdSpendAtMarket(mkt *market.MarketAPI, commodity market.CommodityType, quantity int64) (Money, error) {
	if err := f.checkNotCopied("PostHouseholdSpendAtMarket"); err != nil {
		return 0, err
	}
	if mkt == nil {
		return 0, errs.New(ErrUnknownAccount, f.correlationID, map[string]any{"account": "<nil market>"})
	}
	price, err := mkt.Price(commodity)
	if err != nil {
		return 0, err
	}
	return f.PostHouseholdSpend(quantity, Money(price))
}

// TaxRates is the headline income/sales/corporation rate set this item
// needs to close the M1 loop (out of scope: the full engine.tax
// instrument panel, Sprint 9).
type TaxRates struct {
	IncomeRate BasisPoints // on wages
	SalesRate  BasisPoints // on household spend
	CorpRate   BasisPoints // on firm P&L profit
}

// TaxReceipts is CollectTax's return: the three collected amounts.
type TaxReceipts struct {
	Income Money
	Sales  Money
	Corp   Money
}

// Total sums the three receipts with saturating addition (GR#16).
func (r TaxReceipts) Total() Money {
	total, _ := satAddMoney(r.Income, r.Sales)
	total, _ = satAddMoney(total, r.Corp)
	return total
}

// CollectTax posts income tax on wages, sales tax on household spend,
// and corporation tax on firm P&L profit, each as a transfer from the
// payer to the city treasury, and returns the receipts. It is stage (3)
// of the chain. Zero-amount legs are skipped (no empty transactions).
func (f *FinanceAPI) CollectTax(rates TaxRates, wages, spend, firmProfit Money) (TaxReceipts, error) {
	if err := f.checkNotCopied("CollectTax"); err != nil {
		return TaxReceipts{}, err
	}
	income := taxOf(wages, rates.IncomeRate)
	sales := taxOf(spend, rates.SalesRate)
	corp := taxOf(firmProfit, rates.CorpRate)

	if income > 0 {
		if _, err := f.Post(Transaction{
			Description: "income tax on household wages",
			Entries: []Entry{
				{Account: AcctHouseholds, Side: SideDebit, Amount: income, Category: CatTaxIncome},
				{Account: AcctTreasury, Side: SideCredit, Amount: income, Category: CatTaxIncome},
			},
		}); err != nil {
			return TaxReceipts{}, err
		}
	}
	if sales > 0 {
		if _, err := f.Post(Transaction{
			Description: "sales tax on household spend",
			Entries: []Entry{
				{Account: AcctFirms, Side: SideDebit, Amount: sales, Category: CatTaxSales},
				{Account: AcctTreasury, Side: SideCredit, Amount: sales, Category: CatTaxSales},
			},
		}); err != nil {
			return TaxReceipts{}, err
		}
	}
	if corp > 0 {
		if _, err := f.Post(Transaction{
			Description: "corporation tax on firm P&L",
			Entries: []Entry{
				{Account: AcctFirms, Side: SideDebit, Amount: corp, Category: CatTaxCorp},
				{Account: AcctTreasury, Side: SideCredit, Amount: corp, Category: CatTaxCorp},
			},
		}); err != nil {
			return TaxReceipts{}, err
		}
	}

	return TaxReceipts{Income: income, Sales: sales, Corp: corp}, nil
}

// PostCouncilTax posts a flat residential council-tax charge: households
// pay the city treasury directly, a transfer distinct from CollectTax's
// wage-rate-based income tax (FEAT-1972079927, Aaron's 2026-08-31
// diversify-the-base steer following BUG-391 — the residential side of the
// tax base is council tax + income tax together, not income tax alone).
// total is a pre-computed money amount (the caller derives it from a
// per-capita rate × population — council tax is levied per-dwelling/
// resident, never as a rate on an amount, so there is no BasisPoints leg
// here).
func (f *FinanceAPI) PostCouncilTax(total Money) (Money, error) {
	if err := f.checkNotCopied("PostCouncilTax"); err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "councilTax", "amount": int64(total)})
	}
	if total == 0 {
		return 0, nil
	}
	if _, err := f.Post(Transaction{
		Description: "residential council tax",
		Entries: []Entry{
			{Account: AcctHouseholds, Side: SideDebit, Amount: total, Category: CatTaxCouncil},
			{Account: AcctTreasury, Side: SideCredit, Amount: total, Category: CatTaxCouncil},
		},
	}); err != nil {
		return 0, err
	}
	return total, nil
}

// SettleOpex posts the service-operating-expenditure outflow: money
// leaves the treasury for the outside world. Stage (5) outflow.
func (f *FinanceAPI) SettleOpex(opex Money) (Money, error) {
	if err := f.checkNotCopied("SettleOpex"); err != nil {
		return 0, err
	}
	if opex < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "opex", "amount": int64(opex)})
	}
	if _, err := f.Post(Transaction{
		Description: "service opex",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: opex, Category: CatOpex},
			{Account: AcctExternal, Side: SideCredit, Amount: opex, Category: CatOpex},
		},
	}); err != nil {
		return 0, err
	}
	return opex, nil
}

// ServiceDebt posts a debt-service payment in one balanced transaction:
// the treasury pays interest to the outside world and principal back
// into the debt liability. interest and principal may be zero
// independently (a zero leg is skipped). Stage (5) outflow.
//
// Principal repayment also retires the same amount from the outstanding
// loan book (reduceLoanBookLocked), so OutstandingDebt() and
// CreditRatingNow() stay consistent with the ledger — the same
// principal-repayment effect RepayLoan produces, just not tied to a
// specific loan id.
func (f *FinanceAPI) ServiceDebt(interest, principal Money) error {
	if interest < 0 {
		return errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "interest", "amount": int64(interest)})
	}
	if principal < 0 {
		return errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "principal", "amount": int64(principal)})
	}
	var entries []Entry
	if interest > 0 {
		entries = append(entries,
			Entry{Account: AcctTreasury, Side: SideDebit, Amount: interest, Category: CatDebtService},
			Entry{Account: AcctExternal, Side: SideCredit, Amount: interest, Category: CatDebtService},
		)
	}
	if principal > 0 {
		entries = append(entries,
			Entry{Account: AcctTreasury, Side: SideDebit, Amount: principal, Category: CatDebtPrincipal},
			Entry{Account: AcctDebt, Side: SideCredit, Amount: principal, Category: CatDebtPrincipal},
		)
	}
	if len(entries) == 0 {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("ServiceDebt"); err != nil {
		return err
	}
	if err := f.validateLocked(Transaction{Description: "debt service (interest + principal)", Entries: entries}); err != nil {
		return err
	}
	f.post(Transaction{Description: "debt service (interest + principal)", Entries: entries}, true)
	f.reduceLoanBookLocked(principal)
	return nil
}

// SettleConstruction posts a construction outflow. Stage (5) outflow.
func (f *FinanceAPI) SettleConstruction(cost Money) (Money, error) {
	if err := f.checkNotCopied("SettleConstruction"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "construction", "amount": int64(cost)})
	}
	if _, err := f.Post(Transaction{
		Description: "construction spend",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatConstruction},
			{Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatConstruction},
		},
	}); err != nil {
		return 0, err
	}
	return cost, nil
}

// SettleImports posts an import-contract outflow. Stage (5) outflow.
func (f *FinanceAPI) SettleImports(cost Money) (Money, error) {
	if err := f.checkNotCopied("SettleImports"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "imports", "amount": int64(cost)})
	}
	if _, err := f.Post(Transaction{
		Description: "import contracts",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatImports},
			{Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatImports},
		},
	}); err != nil {
		return 0, err
	}
	return cost, nil
}

// isTaxCategory reports whether cat is one of the three tax categories.
func isTaxCategory(cat Category) bool {
	for _, c := range taxCategories {
		if cat == c {
			return true
		}
	}
	return false
}

// TaxRevenue returns the tick's collected tax revenue — the sum of the
// treasury's credit entries tagged with a tax category (AC-11: the
// aggregate is a sum over Lines(AcctTreasury)).
func (f *FinanceAPI) TaxRevenue() Money {
	if err := f.checkNotCopied("TaxRevenue"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var total Money
	for _, e := range f.linesLocked(AcctTreasury) {
		if e.Side == SideCredit && isTaxCategory(e.Category) {
			total, _ = satAddMoney(total, e.Amount)
		}
	}
	return total
}

// treasuryDebit returns the tick's total treasury debit for one category
// (the caller holds f.mu RLock).
func (f *FinanceAPI) treasuryDebit(cat Category) Money {
	if err := f.checkNotCopied("treasuryDebit"); err != nil {
		return 0
	}
	var total Money
	for _, e := range f.linesLocked(AcctTreasury) {
		if e.Side == SideDebit && e.Category == cat {
			total, _ = satAddMoney(total, e.Amount)
		}
	}
	return total
}

// OpexTotal returns the tick's posted opex (treasury debit, CatOpex).
func (f *FinanceAPI) OpexTotal() Money {
	if err := f.checkNotCopied("OpexTotal"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.treasuryDebit(CatOpex)
}

// DebtServiceTotal returns the tick's posted debt interest (treasury
// debit, CatDebtService — principal is tracked separately via
// PrincipalRepaid).
func (f *FinanceAPI) DebtServiceTotal() Money {
	if err := f.checkNotCopied("DebtServiceTotal"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.treasuryDebit(CatDebtService)
}

// ConstructionTotal returns the tick's posted construction spend.
func (f *FinanceAPI) ConstructionTotal() Money {
	if err := f.checkNotCopied("ConstructionTotal"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.treasuryDebit(CatConstruction)
}

// ImportsTotal returns the tick's posted import spend.
func (f *FinanceAPI) ImportsTotal() Money {
	if err := f.checkNotCopied("ImportsTotal"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.treasuryDebit(CatImports)
}

// WagesPosted returns the tick's posted wage bill (household credit,
// CatWages).
func (f *FinanceAPI) WagesPosted() Money {
	if err := f.checkNotCopied("WagesPosted"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var total Money
	for _, e := range f.linesLocked(AcctHouseholds) {
		if e.Side == SideCredit && e.Category == CatWages {
			total, _ = satAddMoney(total, e.Amount)
		}
	}
	return total
}

// SpendPosted returns the tick's posted household spend (firm credit,
// CatSpend).
func (f *FinanceAPI) SpendPosted() Money {
	if err := f.checkNotCopied("SpendPosted"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var total Money
	for _, e := range f.linesLocked(AcctFirms) {
		if e.Side == SideCredit && e.Category == CatSpend {
			total, _ = satAddMoney(total, e.Amount)
		}
	}
	return total
}

// BudgetBalance returns the tick's city budget balance: tax revenue
// minus opex, debt interest, construction, and imports (AC-3's
// "budget = tax − opex − debt − construction", extended by §7's imports
// outflow — with imports zero this is exactly the AC's formula).
// Computed with saturating subtraction so the net never wraps (GR#16).
func (f *FinanceAPI) BudgetBalance() Money {
	if err := f.checkNotCopied("BudgetBalance"); err != nil {
		return 0
	}
	b := f.TaxRevenue()
	b = satSubMoney(b, f.OpexTotal())
	b = satSubMoney(b, f.DebtServiceTotal())
	b = satSubMoney(b, f.ConstructionTotal())
	b = satSubMoney(b, f.ImportsTotal())
	return b
}
