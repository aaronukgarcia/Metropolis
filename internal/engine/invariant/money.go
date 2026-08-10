package invariant

// MoneyInvariant checks the money-conservation balance (§14, US-2): the
// total money in circulation — citizen wealth + firm/institution
// balances + city treasury + in-flight transactions — at the end of the
// tick must equal the total at the start of the tick plus every tracked
// income/expenditure event for the tick.
//
// Balance identity: Closing - Opening == TrackedDelta, where
// TrackedDelta = income - expenditure for the tick, summed across every
// participant (citizens, firms, treasury, in-flight transfers). Values
// are int64 (the same fixed-point unit foundation.det.Micropounds uses
// elsewhere in this codebase — float64 must never touch a money
// computation, per §1.2 point 4). This is the invariant the sprint
// plan's S4 exit gate later relies on running "green over 120 headless
// months" once engine.finance's real ledger registers against it — the
// generic stockCheck balance logic (conservation.go) this type wraps
// needs no rewrite for that: engine.finance only needs to populate
// StockMoney's StockReading each tick.
type MoneyInvariant struct {
	stockCheck
}

// NewMoneyInvariant constructs the money-conservation invariant.
func NewMoneyInvariant() MoneyInvariant {
	return MoneyInvariant{stockCheck{name: "money", stock: StockMoney}}
}
