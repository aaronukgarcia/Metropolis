package finance

// AccountID names a ledger account. Accounts are the addressable units
// of the double-entry ledger: every Entry references exactly one, and
// every aggregate figure this package exposes drill-throughs back to the
// entries that touched a specific account (AC-11).
type AccountID string

// AccountRole classifies an account for money-conservation purposes
// (see doc.go's "Money model" section).
type AccountRole uint8

const (
	// RoleMoney is a cash-like account that holds real money and is
	// counted in TotalMoneyInCirculation. Balances are non-negative
	// (an overdraft is rejected unless a credit line covers it, AC-13).
	RoleMoney AccountRole = iota

	// RoleExternal is the outside-world source/sink: imports, opex paid
	// away, reserve interest, and loan disbursements/repayments all
	// settle against it. Its balance is not part of the money stock.
	RoleExternal

	// RoleLiability is debt owed (loan principal outstanding). It carries
	// a negative-or-zero balance (a debit on disbursement) and is not
	// part of the money stock.
	RoleLiability
)

// Well-known accounts opened by NewFinanceAPI. The fiscal aggregates the
// acceptance criteria name (tax revenue, opex, debt service,
// construction) are computed as the net flow through AcctTreasury tagged
// with the matching Category, and are drill-through-able via Lines.
const (
	// AcctTreasury is the city's consolidated cash account (RoleMoney).
	AcctTreasury AccountID = "city.treasury"

	// AcctHouseholds is the aggregate citizen-wealth account (RoleMoney).
	AcctHouseholds AccountID = "households.wealth"

	// AcctFirms is the aggregate firm-cash account (RoleMoney).
	AcctFirms AccountID = "firms.cash"

	// AcctReserves is the interest-bearing reserve account (RoleMoney).
	AcctReserves AccountID = "city.reserves"

	// AcctDebt is the outstanding-loan-principal liability (RoleLiability).
	AcctDebt AccountID = "city.debt"

	// AcctExternal is the outside-world source/sink (RoleExternal).
	AcctExternal AccountID = "external.world"
)

// Category tags a ledger entry with the flow it belongs to (wages, tax,
// opex, …). Aggregates are computed by summing the entries carrying a
// given category; LinesByCategory is the drill-through path for that
// computation (AC-11).
type Category string

// The fiscal-flow categories (AC-3's chain plus the external flows §7
// names).
const (
	CatWages           Category = "wages"
	CatSpend           Category = "spend"
	CatTaxIncome       Category = "tax.income"
	CatTaxSales        Category = "tax.sales"
	CatTaxCorp         Category = "tax.corp"
	CatOpex            Category = "opex"
	CatDebtService     Category = "debt.service"
	CatDebtPrincipal   Category = "debt.principal"
	CatConstruction    Category = "construction"
	CatImports         Category = "imports"
	CatLoan            Category = "loan"
	CatReserveInterest Category = "reserve.interest"
	CatReserveDeposit  Category = "reserve.deposit"
	CatInvestment      Category = "investment"
)

// taxCategories is the ordered set of categories that compose the tax
// revenue aggregate. Ordered (a slice, not a map) so summation over it
// is deterministic (GR#21).
var taxCategories = []Category{CatTaxIncome, CatTaxSales, CatTaxCorp}
