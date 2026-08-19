package finance

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type SendCommandFunc func(protocol.Command) error

const (
	opBorrowLoan = "finance.borrow-loan"
	opRepayLoan  = "finance.repay-loan"
	opSetTaxRate = "finance.set-tax-rate"
)

func opCommand(correlationID string, op string, args map[string]string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: op, Args: args},
	}
}

type Screen struct {
	mu sync.Mutex

	self atomic.Pointer[Screen]

	correlationID      string
	subs               map[protocol.SubscriptionID]string
	stale              bool
	haveData           bool
	loanRejectedReason string

	pl                  *PLView
	havePL              bool
	balanceSheet        *BalanceSheetView
	haveBalance         bool
	loans               []LoanState
	haveLoans           bool
	creditRating        int
	haveCredit          bool
	creditRatingHistory []float64
	haveCreditHistory   bool
	taxSliders          []TaxSliderState
	haveSliders         bool
	payroll             *PublicPayrollView
	havePayroll         bool
	sankey              *FiscalCircuitView
	haveSankey          bool
}

func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
	}
	s.self.Store(s)
	return s
}

func (s *Screen) Subscribe(send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewSubscriptionName},
	}
	return send(cmd)
}

func (s *Screen) BindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.subs[id] = ViewSubscriptionName
}

func (s *Screen) UnbindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	delete(s.subs, id)
}

func (s *Screen) ApplyResult(res protocol.CommandResult) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}

	if string(res.CorrelationID) == s.correlationID {
		if !res.Accepted && res.Error != nil {
			s.loanRejectedReason = res.Error.Display
		} else {
			s.loanRejectedReason = ""
		}
	}
}

func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}
	if view != ViewSubscriptionName {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}

	p, err := decodeWirePatch(delta.Patch)
	if err != nil {
		s.logMalformed(err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.haveData = true

	if p.PL != nil {
		s.pl = &PLView{
			Period: p.PL.Period,
		}
		s.pl.Revenues = make([]PLItem, len(p.PL.Revenues))
		for i, r := range p.PL.Revenues {
			s.pl.Revenues[i] = PLItem{Label: r.Label, ValueMicropounds: r.ValueMicropounds}
		}
		s.pl.Expenses = make([]PLItem, len(p.PL.Expenses))
		for i, e := range p.PL.Expenses {
			s.pl.Expenses[i] = PLItem{Label: e.Label, ValueMicropounds: e.ValueMicropounds}
		}
		s.havePL = true
	} else {
		s.havePL = false
	}

	if p.BalanceSheet != nil {
		s.balanceSheet = &BalanceSheetView{
			NetWorth: p.BalanceSheet.NetWorth,
		}
		s.balanceSheet.Assets = make([]BalanceItem, len(p.BalanceSheet.Assets))
		for i, a := range p.BalanceSheet.Assets {
			s.balanceSheet.Assets[i] = BalanceItem{Label: a.Label, ValueMicropounds: a.ValueMicropounds}
		}
		s.balanceSheet.Liabilities = make([]BalanceItem, len(p.BalanceSheet.Liabilities))
		for i, l := range p.BalanceSheet.Liabilities {
			s.balanceSheet.Liabilities[i] = BalanceItem{Label: l.Label, ValueMicropounds: l.ValueMicropounds}
		}
		s.haveBalance = true
	} else {
		s.haveBalance = false
	}

	if p.Loans != nil {
		s.loans = make([]LoanState, len(*p.Loans))
		for i, l := range *p.Loans {
			s.loans[i] = LoanState{
				ID:                     l.ID,
				PrincipalMicropounds:   l.PrincipalMicropounds,
				RatePercent:            l.RatePercent,
				TermMonths:             l.TermMonths,
				NextPaymentMicropounds: l.NextPaymentMicropounds,
			}
		}
		s.haveLoans = true
	} else {
		s.haveLoans = false
	}

	if p.CreditRating != nil {
		s.creditRating = *p.CreditRating
		s.haveCredit = true
	} else {
		s.haveCredit = false
	}

	if p.CreditRatingHistory != nil {
		s.creditRatingHistory = append([]float64(nil), *p.CreditRatingHistory...)
		s.haveCreditHistory = true
	} else {
		s.creditRatingHistory = nil
		s.haveCreditHistory = false
	}

	if p.TaxSliders != nil {
		s.taxSliders = make([]TaxSliderState, len(*p.TaxSliders))
		for i, t := range *p.TaxSliders {
			s.taxSliders[i] = TaxSliderState{
				ID:                    t.ID,
				Label:                 t.Label,
				Value:                 t.Value,
				Min:                   t.Min,
				Max:                   t.Max,
				Step:                  t.Step,
				ElasticityCurvePoints: append([]float64(nil), t.ElasticityCurvePoints...),
				IncidenceDescription:  t.IncidenceDescription,
			}
		}
		s.haveSliders = true
	} else {
		s.haveSliders = false
	}

	if p.PublicPayroll != nil {
		s.payroll = &PublicPayrollView{
			WageCostMicropounds:    p.PublicPayroll.WageCostMicropounds,
			TaxClawbackMicropounds: p.PublicPayroll.TaxClawbackMicropounds,
		}
		s.havePayroll = true
	} else {
		s.havePayroll = false
	}

	if p.Sankey != nil {
		s.sankey = &FiscalCircuitView{}
		s.sankey.Bands = make([]SankeyBand, len(p.Sankey.Bands))
		for i, b := range p.Sankey.Bands {
			s.sankey.Bands[i] = SankeyBand{Source: b.Source, Target: b.Target, Amount: b.Amount}
		}
		s.haveSankey = true
	} else {
		s.haveSankey = false
	}
}

func (s *Screen) HaveData() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haveData
}

func (s *Screen) Stale() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stale
}

func (s *Screen) LoanRejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LoanRejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loanRejectedReason
}

func (s *Screen) PL() (PLView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "PL"}); err != nil {
		return PLView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.havePL {
		return PLView{}, false
	}
	res := PLView{
		Period:   s.pl.Period,
		Revenues: make([]PLItem, len(s.pl.Revenues)),
		Expenses: make([]PLItem, len(s.pl.Expenses)),
	}
	copy(res.Revenues, s.pl.Revenues)
	copy(res.Expenses, s.pl.Expenses)
	return res, true
}

func (s *Screen) BalanceSheet() (BalanceSheetView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BalanceSheet"}); err != nil {
		return BalanceSheetView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveBalance {
		return BalanceSheetView{}, false
	}
	res := BalanceSheetView{
		NetWorth:    s.balanceSheet.NetWorth,
		Assets:      make([]BalanceItem, len(s.balanceSheet.Assets)),
		Liabilities: make([]BalanceItem, len(s.balanceSheet.Liabilities)),
	}
	copy(res.Assets, s.balanceSheet.Assets)
	copy(res.Liabilities, s.balanceSheet.Liabilities)
	return res, true
}

func (s *Screen) Loans() ([]LoanState, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Loans"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveLoans {
		return nil, false
	}
	res := make([]LoanState, len(s.loans))
	copy(res, s.loans)
	return res, true
}

func (s *Screen) CreditRating() (int, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CreditRating"}); err != nil {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creditRating, s.haveCredit
}

func (s *Screen) CreditRatingHistory() ([]float64, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CreditRatingHistory"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveCreditHistory {
		return nil, false
	}
	res := make([]float64, len(s.creditRatingHistory))
	copy(res, s.creditRatingHistory)
	return res, true
}

func (s *Screen) TaxSliders() ([]TaxSliderState, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "TaxSliders"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveSliders {
		return nil, false
	}
	res := make([]TaxSliderState, len(s.taxSliders))
	for i, t := range s.taxSliders {
		res[i] = TaxSliderState{
			ID:                    t.ID,
			Label:                 t.Label,
			Value:                 t.Value,
			Min:                   t.Min,
			Max:                   t.Max,
			Step:                  t.Step,
			ElasticityCurvePoints: append([]float64(nil), t.ElasticityCurvePoints...),
			IncidenceDescription:  t.IncidenceDescription,
		}
	}
	return res, true
}

func (s *Screen) PublicPayroll() (PublicPayrollView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "PublicPayroll"}); err != nil {
		return PublicPayrollView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.havePayroll {
		return PublicPayrollView{}, false
	}
	return *s.payroll, true
}

func (s *Screen) Sankey() (FiscalCircuitView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Sankey"}); err != nil {
		return FiscalCircuitView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveSankey {
		return FiscalCircuitView{}, false
	}
	res := FiscalCircuitView{
		Bands: make([]SankeyBand, len(s.sankey.Bands)),
	}
	copy(res.Bands, s.sankey.Bands)
	return res, true
}

func (s *Screen) BorrowLoan(send SendCommandFunc, amountMicropounds int64, termMonths int) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BorrowLoan"}); err != nil {
		return err
	}
	if amountMicropounds <= 0 {
		return errs.New(ErrInvalidLoanRequest, s.correlationID, map[string]any{"reason": "non-positive borrow amount"})
	}
	if termMonths <= 0 || termMonths > 360 {
		return errs.New(ErrInvalidLoanRequest, s.correlationID, map[string]any{"reason": "invalid termMonths (must be between 1 and 360)"})
	}
	args := map[string]string{
		"amountMicropounds": strconv.FormatInt(amountMicropounds, 10),
		"termMonths":        strconv.Itoa(termMonths),
	}
	return send(opCommand(s.correlationID, opBorrowLoan, args))
}

func (s *Screen) RepayLoan(send SendCommandFunc, loanID string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RepayLoan"}); err != nil {
		return err
	}
	args := map[string]string{
		"loanId": loanID,
	}
	return send(opCommand(s.correlationID, opRepayLoan, args))
}

func (s *Screen) SetTaxRate(send SendCommandFunc, id string, value float64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetTaxRate"}); err != nil {
		return err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return errs.New(ErrInvalidLoanRequest, s.correlationID, map[string]any{"reason": "invalid tax rate (must be non-negative, non-NaN and non-Inf)"})
	}
	args := map[string]string{
		"id":    id,
		"value": strconv.FormatFloat(value, 'f', -1, 64),
	}
	return send(opCommand(s.correlationID, opSetTaxRate, args))
}

func (s *Screen) logMalformed(cause error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logMalformed"}); err != nil {
		return
	}
	_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
		"view":  ViewSubscriptionName,
		"cause": cause.Error(),
	})
}

func (s *Screen) logUnknownSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logUnknownSubscription"}); err != nil {
		return
	}
	_ = errs.New(ErrStaleSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
