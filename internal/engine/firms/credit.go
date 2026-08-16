package firms

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CreditRequest is ApproveCredit's input (AC-13).
type CreditRequest struct {
	FirmID    FirmID
	Principal int64 // micro-pounds
	Month     int64
}

// CreditDecision is ApproveCredit's result: whether the request was
// approved, the amount granted, the borrowing rate (basis points), and the
// deposit-backed lending capacity at decision time. A denial is carried as
// ErrCreditDenied (never a silent Approved=false the caller could mistake
// for a normal empty result).
type CreditDecision struct {
	Approved bool
	Amount   int64
	RateBp   int64
	Capacity int64
}

// DepositPool returns the bank's deposit pool (AC-13): household wealth
// deposits plus city reserves, sourced from engine.finance's ledger
// (engine.finance is the registered outbound edge). Money is int64
// micro-pounds (never a float). With no finance wired the pool reads 0
// (never a panic).
func (f *FirmsAPI) DepositPool() int64 {
	if err := f.checkNotCopied("DepositPool"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.depositPoolLocked()
}

func (f *FirmsAPI) depositPoolLocked() int64 {
	if f.finance == nil {
		return 0
	}
	hh, _ := f.finance.AccountBalance(finance.AcctHouseholds)
	res, _ := f.finance.AccountBalance(finance.AcctReserves)
	return num.SatAdd(int64(hh), int64(res))
}

// LendingCapacity returns the deposit-backed lending capacity (AC-13): the
// deposit pool scaled by the deposit-to-lending ratio (data/firms.json).
// A credit request larger than this is denied.
func (f *FirmsAPI) LendingCapacity() int64 {
	if err := f.checkNotCopied("LendingCapacity"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	pool := f.depositPoolLocked()
	return satMul(pool, f.cfg.Credit.DepositToLendingRatioPerMille) / 1000
}

// BaseRate returns the off-map central-bank base rate for a month, in
// basis points (AC-14): the last cycle step whose Month <= month. The
// cycle is data (data/firms.json), never a Go literal (GR#15).
func (f *FirmsAPI) BaseRate(month int64) int64 {
	if err := f.checkNotCopied("BaseRate"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.baseRateLocked(month)
}

func (f *FirmsAPI) baseRateLocked(month int64) int64 {
	cycle := f.cfg.Credit.BaseRateCycle
	rate := cycle[0].BaseRateBp
	for _, rp := range cycle {
		if rp.Month <= month {
			rate = rp.BaseRateBp
			continue
		}
		break
	}
	return rate
}

// BorrowingRate returns a firm's borrowing cost for a month and stage
// (AC-14): the off-map base rate plus the stage spread. A rate-cycle spike
// raises this for every credit-dependent Startup/Small firm.
func (f *FirmsAPI) BorrowingRate(month int64, st Stage) int64 {
	if err := f.checkNotCopied("BorrowingRate"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.borrowingRateLocked(month, st)
}

func (f *FirmsAPI) borrowingRateLocked(month int64, st Stage) int64 {
	return num.SatAdd(f.baseRateLocked(month), f.cfg.Credit.StageSpreadBp[st])
}

// ApproveCredit approves a firm credit request bounded by the bank's
// deposit pool (AC-13): a request larger than the deposit-backed lending
// capacity is denied (ErrCreditDenied, never a silent partial approval).
// On approval the principal is recorded against the firm's outstanding
// credit and the borrowing rate is fixed from the current base-rate cycle
// (AC-14). A non-positive principal is rejected (ErrInvalidCreditTerms).
func (f *FirmsAPI) ApproveCredit(req CreditRequest) (CreditDecision, error) {
	if err := f.checkNotCopied("ApproveCredit"); err != nil {
		return CreditDecision{}, err
	}
	if req.Principal <= 0 {
		return CreditDecision{}, errs.New(ErrInvalidCreditTerms, f.correlationID, map[string]any{
			"amount": req.Principal, "rule": "must be positive",
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fs, ok := f.firms[req.FirmID]
	if !ok {
		return CreditDecision{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(req.FirmID)})
	}

	capacity := satMul(f.depositPoolLocked(), f.cfg.Credit.DepositToLendingRatioPerMille) / 1000
	rateBp := f.borrowingRateLocked(req.Month, fs.firm.Stage)

	// Cumulative bound (SEC-100): the request plus everything already
	// outstanding must fit within the deposit-backed capacity, so a firm (or
	// many firms) cannot borrow the full capacity repeatedly.
	newTotal := num.SatAdd(f.totalCreditOutstanding, req.Principal)
	if newTotal > capacity {
		return CreditDecision{Approved: false, Amount: 0, RateBp: rateBp, Capacity: capacity},
			errs.New(ErrCreditDenied, f.correlationID, map[string]any{
				"amount": req.Principal, "capacity": capacity, "outstanding": f.totalCreditOutstanding,
			})
	}

	f.totalCreditOutstanding = newTotal
	fs.firm.Financial.CreditOutstanding = num.SatAdd(fs.firm.Financial.CreditOutstanding, req.Principal)
	return CreditDecision{Approved: true, Amount: req.Principal, RateBp: rateBp, Capacity: capacity}, nil
}
