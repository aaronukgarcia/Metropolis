package finance

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// accountState is one ledger account's persistent state: its role (for
// money-conservation classification) and its running balance
// (credits - debits, maintained incrementally on every post).
type accountState struct {
	Role    AccountRole
	Balance Money
}

// FinanceAPI is code.json's "engine.finance" inbound contract
// (FinanceAPI, "double-entry ledger; every figure drill-through to
// ledger lines"). It owns the double-entry ledger, the running money
// stock, the per-tick external-flow tracker, loan facilities, the
// insolvency counter, and the v1 firm/investment registries.
//
// The zero value is not usable; construct via NewFinanceAPI. A
// *FinanceAPI is safe for concurrent use (AC-16): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class).
type FinanceAPI struct {
	mu            sync.RWMutex
	correlationID string

	accounts map[AccountID]*accountState
	role     map[AccountID]AccountRole

	// txns is the append-only transaction log; tickTxns is the current
	// tick's slice of txns, the per-tick log conservation localisation
	// (AC-10b) and drill-through (AC-11) read.
	txns     []Transaction
	tickTxns []Transaction
	nextTxID TxID

	// Running money-conservation state. moneyStock is the maintained
	// TotalMoneyInCirculation (Closing); openingStock is its value at the
	// start of the current tick; trackedDelta is the net external money
	// flow posted during the current tick.
	moneyStock   Money
	openingStock Money
	trackedDelta Money

	month int64

	// creditLines is the available (unused) credit per money account; an
	// overdraft is only allowed if the account's line covers it (AC-13).
	// totalCreditLine is the maintained running sum, so AvailableCredit
	// never iterates the map in map order (AC-14).
	creditLines     map[AccountID]Money
	totalCreditLine Money

	// Loan/credit state. totalDebt is the maintained outstanding-principal
	// running total (updated on Borrow/RepayLoan), so no monetary sum ever
	// iterates the loans map in map order (AC-14).
	loans          map[LoanID]*Loan
	nextLoanID     LoanID
	totalDebt      Money
	missedPayments int
	gate           MilestoneGate

	// Insolvency state.
	insolvencyMonths int
	gameOver         bool

	// v1 registries.
	firms        map[FirmID]*SimpleFirm
	nextFirmID   FirmID
	investments  []*InvestmentProgramme
	nextInvestID InvestmentID

	// self is the SEC-020 copy guard, stored exactly once in
	// NewFinanceAPI before the value is returned to any caller.
	self atomic.Pointer[FinanceAPI]
}

// MilestoneGate reports whether a milestone tier has been reached. It is
// the shape this package consumes engine.unlocks's milestone state
// through (AC-5): engine.finance does not hardcode a tier table, and the
// composition root wires the real engine.unlocks gate (or a test fake)
// here.
type MilestoneGate interface {
	// MilestoneReached reports whether tier has been reached.
	MilestoneReached(tier int) bool
}

// NewFinanceAPI constructs an empty, ready-to-post FinanceAPI with the
// six well-known accounts opened and a nil milestone gate (nil gate
// rejects every Borrow until SetMilestoneGate is called, AC-5).
func NewFinanceAPI(correlationID string) *FinanceAPI {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	f := &FinanceAPI{
		correlationID: correlationID,
		accounts:      make(map[AccountID]*accountState),
		role:          make(map[AccountID]AccountRole),
		nextTxID:      1,
		creditLines:   make(map[AccountID]Money),
		loans:         make(map[LoanID]*Loan),
		nextLoanID:    1,
		firms:         make(map[FirmID]*SimpleFirm),
		nextFirmID:    1,
		nextInvestID:  1,
	}
	// Open the well-known accounts directly (not via OpenAccount): the
	// copy-guard's self pointer is stored below, after construction, so
	// OpenAccount would reject these first writes. wellKnownAccounts is a
	// slice (not a map) so setup order is deterministic (GR#21).
	for _, spec := range wellKnownAccounts {
		f.accounts[spec.id] = &accountState{Role: spec.role}
		f.role[spec.id] = spec.role
	}
	// Stored exactly once, before f is returned to any caller.
	f.self.Store(f)
	return f
}

// accountSpec pairs an account ID with its role, for the deterministic
// well-known-account setup in NewFinanceAPI.
type accountSpec struct {
	id   AccountID
	role AccountRole
}

// wellKnownAccounts is the ordered set of accounts NewFinanceAPI always
// opens, opened directly (before the copy guard is armed) rather than
// through OpenAccount.
var wellKnownAccounts = []accountSpec{
	{AcctTreasury, RoleMoney},
	{AcctHouseholds, RoleMoney},
	{AcctFirms, RoleMoney},
	{AcctReserves, RoleMoney},
	{AcctDebt, RoleLiability},
	{AcctExternal, RoleExternal},
}

// checkNotCopied rejects a method call on a struct-copied *FinanceAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load, safe to run
// before mu is ever touched.
func (f *FinanceAPI) checkNotCopied(method string) error {
	if f.self.Load() != f {
		return errs.New(ErrCopiedValue, f.correlationID, map[string]any{"method": method})
	}
	return nil
}

// OpenAccount opens an account with the given role. Rejects a duplicate
// AccountID (ErrDuplicateAccount).
func (f *FinanceAPI) OpenAccount(id AccountID, role AccountRole) error {
	if err := f.checkNotCopied("OpenAccount"); err != nil {
		return err
	}
	if id == "" {
		return errs.New(ErrUnknownAccount, f.correlationID, map[string]any{"account": id})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("OpenAccount"); err != nil {
		return err
	}
	if _, ok := f.accounts[id]; ok {
		return errs.New(ErrDuplicateAccount, f.correlationID, map[string]any{"account": string(id)})
	}
	f.accounts[id] = &accountState{Role: role}
	f.role[id] = role
	return nil
}

// SetMilestoneGate installs the milestone gate used by Borrow. A nil
// gate means no facility is available.
func (f *FinanceAPI) SetMilestoneGate(g MilestoneGate) error {
	if err := f.checkNotCopied("SetMilestoneGate"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate = g
	return nil
}

// SetCreditLine sets the available credit line for a money account. A
// debit that would take the account below zero is only allowed while the
// line covers the shortfall (AC-13).
func (f *FinanceAPI) SetCreditLine(id AccountID, amount Money) error {
	if err := f.checkNotCopied("SetCreditLine"); err != nil {
		return err
	}
	if amount < 0 {
		return errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "creditLine", "amount": int64(amount)})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("SetCreditLine"); err != nil {
		return err
	}
	if _, ok := f.accounts[id]; !ok {
		return errs.New(ErrUnknownAccount, f.correlationID, map[string]any{"account": string(id)})
	}
	f.totalCreditLine = satSubMoney(f.totalCreditLine, f.creditLines[id])
	f.creditLines[id] = amount
	f.totalCreditLine, _ = satAddMoney(f.totalCreditLine, amount)
	return nil
}

// BeginMonth opens a new monthly finance tick: it records the current
// money stock as Opening, resets the tracked external-flow delta and the
// per-tick transaction log, and advances the simulation month. Call this
// once at the start of each monthly finance phase, before posting.
func (f *FinanceAPI) BeginMonth(month int64) error {
	if err := f.checkNotCopied("BeginMonth"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("BeginMonth"); err != nil {
		return err
	}
	f.month = month
	f.openingStock = f.moneyStock
	f.trackedDelta = 0
	f.tickTxns = f.tickTxns[:0]
	return nil
}

// Month returns the current simulation month (the last BeginMonth
// argument).
func (f *FinanceAPI) Month() int64 {
	if err := f.checkNotCopied("Month"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.month
}

// Post validates tx and, on success, records it in the ledger: it
// assigns the transaction an ID, appends it to the log, updates account
// balances and the running money stock, and adds the transaction's money
// delta to the current tick's TrackedDelta. On any validation failure
// (unbalanced, negative amount, unknown account, overdraft without
// credit) it returns a registry-sourced error and the ledger is left
// unchanged — never a plug entry, never a partial post (AC-12, AC-13).
func (f *FinanceAPI) Post(tx Transaction) (TxID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("Post"); err != nil {
		return 0, err
	}
	if err := f.validateLocked(tx); err != nil {
		return 0, err
	}
	return f.post(tx, true), nil
}

// validateLocked runs Post's balance and overdraft validation without
// locking (the caller holds f.mu) and without mutating the ledger. Used
// by Post and by ServiceDebt, which needs the same checks atomically with
// its loan-book reduction.
func (f *FinanceAPI) validateLocked(tx Transaction) error {
	if err := f.checkNotCopied("validateLocked"); err != nil {
		return err
	}
	if len(tx.Entries) == 0 {
		return errs.New(ErrUnbalancedTransaction, f.correlationID, map[string]any{
			"txid": 0, "debits": 0, "credits": 0,
		})
	}
	for _, e := range tx.Entries {
		if e.Amount < 0 {
			return errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "entry.amount", "amount": int64(e.Amount)})
		}
		if _, ok := f.accounts[e.Account]; !ok {
			return errs.New(ErrUnknownAccount, f.correlationID, map[string]any{"account": string(e.Account)})
		}
	}
	if !tx.balanced() {
		d, _ := tx.debits()
		c, _ := tx.credits()
		return errs.New(ErrUnbalancedTransaction, f.correlationID, map[string]any{
			"txid": 0, "debits": int64(d), "credits": int64(c),
		})
	}

	// Overdraft check: no RoleMoney account may go below zero unless its
	// credit line covers the shortfall (AC-13). The check accumulates the
	// transaction's own effect per account in a working map, so multiple
	// debits to one account see each other (GR#16 — a second debit must
	// not be validated against the untouched pre-transaction balance).
	projected := make(map[AccountID]Money, len(tx.Entries))
	for _, e := range tx.Entries {
		acct := f.accounts[e.Account]
		if acct.Role != RoleMoney {
			continue
		}
		bal, ok := projected[e.Account]
		if !ok {
			bal = acct.Balance
		}
		if e.Side == SideCredit {
			bal, _ = satAddMoney(bal, e.Amount)
		} else {
			bal = satSubMoney(bal, e.Amount)
		}
		projected[e.Account] = bal
		afterCredit, _ := satAddMoney(bal, f.creditLines[e.Account])
		if bal < 0 && afterCredit < 0 {
			return errs.New(ErrInsufficientFunds, f.correlationID, map[string]any{
				"account": string(e.Account), "balance": int64(acct.Balance), "amount": int64(e.Amount),
			})
		}
	}
	return nil
}

// post is the shared append path behind Post (tracked=true) and postRaw
// (tracked=false). It assumes validation has already run for the caller
// that needs it. tracked controls whether the transaction's money delta
// is added to trackedDelta — postRaw deliberately does not, which is
// exactly the money-created/destroyed-without-a-flow corruption the
// conservation invariant and FindConservationViolations exist to catch
// (AC-10b).
func (f *FinanceAPI) post(tx Transaction, tracked bool) TxID {
	if err := f.checkNotCopied("post"); err != nil {
		return 0
	}
	tx.ID = f.nextTxID
	f.nextTxID++
	tx.Month = f.month

	f.txns = append(f.txns, tx)
	f.tickTxns = append(f.tickTxns, tx)

	delta := tx.moneyDelta(f.role)
	for _, e := range tx.Entries {
		acct := f.accounts[e.Account]
		if e.Side == SideCredit {
			acct.Balance, _ = satAddMoney(acct.Balance, e.Amount)
		} else {
			acct.Balance = satSubMoney(acct.Balance, e.Amount)
		}
	}
	f.moneyStock, _ = satAddMoney(f.moneyStock, delta)
	if tracked {
		f.trackedDelta, _ = satAddMoney(f.trackedDelta, delta)
	}
	return tx.ID
}

// postRaw appends a transaction without balance/overdraft validation or
// tracked-delta accounting. It exists so a test can simulate the exact
// "unbalanced transaction reached the ledger" bug the conservation
// invariant exists to catch (AC-10b), and is unexported for that reason
// — production callers must use Post.
func (f *FinanceAPI) postRaw(tx Transaction) TxID {
	if err := f.checkNotCopied("postRaw"); err != nil {
		return 0
	}
	return f.post(tx, false)
}

// TotalMoneyInCirculation returns the maintained running total of all
// RoleMoney account balances (AC-10). O(1): it reads the running total
// rather than walking the ledger.
func (f *FinanceAPI) TotalMoneyInCirculation() Money {
	if err := f.checkNotCopied("TotalMoneyInCirculation"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.moneyStock
}

// RecomputeMoneyStock recomputes the money stock from scratch by walking
// the ledger entries (credits - debits per RoleMoney account), in sorted
// account order (GR#21). It exists so the AC-10 invariant "running total
// matches a from-scratch sum" is independently checkable, and so a drift
// between the running total and the ledger is detectable.
func (f *FinanceAPI) RecomputeMoneyStock() Money {
	if err := f.checkNotCopied("RecomputeMoneyStock"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	var total Money
	for _, id := range f.sortedMoneyAccounts() {
		total, _ = satAddMoney(total, f.balanceFromScratch(id))
	}
	return total
}

// sortedMoneyAccounts returns the RoleMoney account IDs in ascending
// order (deterministic — never map-iteration order, GR#21).
func (f *FinanceAPI) sortedMoneyAccounts() []AccountID {
	if err := f.checkNotCopied("sortedMoneyAccounts"); err != nil {
		return nil
	}
	ids := make([]AccountID, 0, len(f.role))
	for id, r := range f.role {
		if r == RoleMoney {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// balanceFromScratch recomputes one account's balance by summing its
// entries' (credit - debit) over the whole ledger — independent of the
// incrementally-maintained Balance field, so a balance-maintenance bug
// surfaces here.
func (f *FinanceAPI) balanceFromScratch(id AccountID) Money {
	if err := f.checkNotCopied("balanceFromScratch"); err != nil {
		return 0
	}
	var balance Money
	for _, tx := range f.txns {
		for _, e := range tx.Entries {
			if e.Account != id {
				continue
			}
			if e.Side == SideCredit {
				balance, _ = satAddMoney(balance, e.Amount)
			} else {
				balance = satSubMoney(balance, e.Amount)
			}
		}
	}
	return balance
}

// AccountBalance returns an account's current running balance, and
// whether the account exists.
func (f *FinanceAPI) AccountBalance(id AccountID) (Money, bool) {
	if err := f.checkNotCopied("AccountBalance"); err != nil {
		return 0, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	acct, ok := f.accounts[id]
	if !ok {
		return 0, false
	}
	return acct.Balance, true
}

// MoneyStock is the per-tick money-conservation triple the invariant
// hook needs: Opening (money at the start of the tick), Closing (money
// now), and TrackedDelta (net external flow this tick).
type MoneyStock struct {
	Opening      Money
	Closing      Money
	TrackedDelta Money
}

// MoneyStock returns the current tick's Opening/Closing/TrackedDelta
// triple (US-3). Opening and TrackedDelta are meaningful only after
// BeginMonth; before the first BeginMonth they read as the zero value.
func (f *FinanceAPI) MoneyStock() MoneyStock {
	if err := f.checkNotCopied("MoneyStock"); err != nil {
		return MoneyStock{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return MoneyStock{
		Opening:      f.openingStock,
		Closing:      f.moneyStock,
		TrackedDelta: f.trackedDelta,
	}
}

// MoneyStockReading returns the current tick's money reading in
// engine.invariant's StockReading shape, ready to slot into a
// SnapshotProvider's Readings[invariant.StockMoney] entry so
// engine.invariant's MoneyInvariant has real data to check (US-3,
// AC-10). Registered is always true — a *FinanceAPI that exists always
// reports its money stock.
func (f *FinanceAPI) MoneyStockReading() invariant.StockReading {
	if err := f.checkNotCopied("MoneyStockReading"); err != nil {
		return invariant.StockReading{}
	}
	s := f.MoneyStock()
	return invariant.StockReading{
		Registered:   true,
		Opening:      int64(s.Opening),
		Closing:      int64(s.Closing),
		TrackedDelta: int64(s.TrackedDelta),
	}
}

// Lines returns the current tick's ledger entries touching account, in
// post order (AC-11's drill-through path). Aggregates are documented as
// sums over these lines, so a caller can always open a figure to its
// underlying transactions.
func (f *FinanceAPI) Lines(account AccountID) []Entry {
	if err := f.checkNotCopied("Lines"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.linesLocked(account)
}

// linesLocked is Lines without the lock — the caller holds f.mu (RLock).
// Split out so the aggregate accessors can compute several figures over
// one consistent snapshot without recursive read-locking (a recursive
// RLock can deadlock once a writer is waiting).
func (f *FinanceAPI) linesLocked(account AccountID) []Entry {
	if err := f.checkNotCopied("linesLocked"); err != nil {
		return nil
	}
	var out []Entry
	for _, tx := range f.tickTxns {
		for _, e := range tx.Entries {
			if e.Account == account {
				out = append(out, e)
			}
		}
	}
	return out
}

// LinesByCategory returns the current tick's ledger entries carrying the
// given category, in post order.
func (f *FinanceAPI) LinesByCategory(cat Category) []Entry {
	if err := f.checkNotCopied("LinesByCategory"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []Entry
	for _, tx := range f.tickTxns {
		for _, e := range tx.Entries {
			if e.Category == cat {
				out = append(out, e)
			}
		}
	}
	return out
}

// ConservationViolation names a transaction that created or destroyed
// money without a matching tracked flow (AC-10b): either the transaction
// is unbalanced (debits != credits, so money changed hands without a
// counterpart), or its money delta was not tracked. The localisation
// query FindConservationViolations returns these, so engine.invariant's
// Violation.EntityIDs can be populated with a real transaction/account
// ID instead of a bare total.
type ConservationViolation struct {
	TransactionID TxID
	AccountIDs    []AccountID
	Debits        Money
	Credits       Money
	NetMoneyDelta Money
	Unbalanced    bool
}

// FindConservationViolations scans the current tick's transaction log
// for transactions that broke conservation: any transaction whose total
// debits do not equal total credits (money created or destroyed without
// a balancing counterpart) is reported with its transaction ID and the
// RoleMoney accounts it touched (AC-10b). A balanced external flow is
// not a violation — its delta is tracked.
func (f *FinanceAPI) FindConservationViolations() []ConservationViolation {
	if err := f.checkNotCopied("FindConservationViolations"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	var out []ConservationViolation
	for _, tx := range f.tickTxns {
		if tx.balanced() {
			continue
		}
		d, _ := tx.debits()
		c, _ := tx.credits()
		out = append(out, ConservationViolation{
			TransactionID: tx.ID,
			AccountIDs:    tx.moneyAccounts(f.role),
			Debits:        d,
			Credits:       c,
			NetMoneyDelta: tx.moneyDelta(f.role),
			Unbalanced:    true,
		})
	}
	return out
}
