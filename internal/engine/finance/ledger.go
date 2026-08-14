package finance

// Side is the debit/credit side of a double-entry ledger entry (AC-1).
// Every Transaction's entries carry a Side and a positive Amount; the
// transaction is balanced when the sum of debit amounts equals the sum
// of credit amounts.
type Side uint8

const (
	// SideDebit decreases a RoleMoney account's balance (money out).
	SideDebit Side = iota
	// SideCredit increases a RoleMoney account's balance (money in).
	SideCredit
)

// Entry is one line of a Transaction: an account, the side (debit or
// credit), a non-negative amount, and a Category tagging the flow. Every
// figure this package exposes can be traced back to the Entries that
// compose it (AC-11's drill-through rule).
type Entry struct {
	Account  AccountID
	Side     Side
	Amount   Money
	Category Category
}

// TxID identifies a posted transaction, assigned monotonically by the
// FinanceAPI (starting at 1; 0 is the "no transaction" sentinel).
type TxID uint64

// Transaction is a double-entry posting: one or more Entries whose total
// debits equal total credits (enforced by FinanceAPI.Post, AC-1/AC-12).
type Transaction struct {
	ID          TxID
	Month       int64 // simulation month this transaction was posted in
	Description string
	Entries     []Entry
}

// debits returns the sum of the transaction's debit entries, and whether
// that sum overflowed int64 (saturated at MaxInt64 — never wrapped).
func (t Transaction) debits() (Money, bool) {
	var total Money
	var overflowed bool
	for _, e := range t.Entries {
		if e.Side == SideDebit {
			var o bool
			total, o = satAddMoney(total, e.Amount)
			overflowed = overflowed || o
		}
	}
	return total, overflowed
}

// credits returns the sum of the transaction's credit entries, and
// whether that sum overflowed int64 (saturated at MaxInt64 — never
// wrapped).
func (t Transaction) credits() (Money, bool) {
	var total Money
	var overflowed bool
	for _, e := range t.Entries {
		if e.Side == SideCredit {
			var o bool
			total, o = satAddMoney(total, e.Amount)
			overflowed = overflowed || o
		}
	}
	return total, overflowed
}

// balanced reports whether the transaction's debits equal its credits.
// An overflowing sum on either side is never treated as balanced — a
// wrapped comparison could otherwise accept a corrupt transaction
// (GR#16).
func (t Transaction) balanced() bool {
	d, do := t.debits()
	c, co := t.credits()
	if do || co {
		return false
	}
	return d == c
}

// moneyDelta returns the transaction's net change to RoleMoney accounts:
// the sum over RoleMoney-account entries of (credit ? +amount :
// -amount). An internal transfer has delta zero; an external flow or a
// loan/reserve-interest accrual has a non-zero delta (doc.go).
func (t Transaction) moneyDelta(role map[AccountID]AccountRole) Money {
	var delta Money
	for _, e := range t.Entries {
		if role[e.Account] != RoleMoney {
			continue
		}
		if e.Side == SideCredit {
			delta, _ = satAddMoney(delta, e.Amount)
		} else {
			delta = satSubMoney(delta, e.Amount)
		}
	}
	return delta
}

// moneyAccounts returns the set of AccountIDs (among the transaction's
// entries) that are RoleMoney accounts — used by conservation-violation
// localisation to name the account a discrepant transaction touched
// (AC-10b).
func (t Transaction) moneyAccounts(role map[AccountID]AccountRole) []AccountID {
	seen := make(map[AccountID]bool)
	var out []AccountID
	for _, e := range t.Entries {
		if role[e.Account] != RoleMoney || seen[e.Account] {
			continue
		}
		seen[e.Account] = true
		out = append(out, e.Account)
	}
	return out
}
