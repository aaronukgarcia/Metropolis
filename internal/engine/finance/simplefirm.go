package finance

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FirmID identifies a v1 SimpleFirm in a FinanceAPI's firm registry.
type FirmID uint64

// SimpleFirm is the v1 firm P&L stand-in (AC-9): revenue from local
// demand ± export, costs from wages + inputs + rent. A firm opens when
// its projected monthly profit is positive and closes after a sustained
// (consecutive-month) negative P&L.
//
// # Superseded by engine.firms (Sprint 10)
//
// This type is explicitly temporary and is superseded by engine.firms
// (MOD-058, Sprint 10). When that module lands it replaces this stand-in
// with the full entrepreneur-to-enterprise lifecycle and this type
// should be removed: the full model adds sector/product differentiation,
// professional services, banking relationships, an entrepreneur
// lifecycle, and firm-level export/import balance-of-trade accounting —
// none of which this v1 model represents. Do not mistake SimpleFirm for
// the permanent firm model (AC-18).
type SimpleFirm struct {
	ID         FirmID
	Name       string
	Revenue    Money // projected monthly revenue (local demand ± export)
	WageCost   Money
	InputCost  Money
	Rent       Money
	Open       bool
	lossStreak int
}

// firmCloseAfterLossMonths is the consecutive loss-month streak that
// closes a firm. Placeholder magnitude — directional tests only.
const firmCloseAfterLossMonths = 3

// NewSimpleFirm validates and constructs a firm. A firm opens iff its
// projected monthly profit is positive (AC-9). Rejects a negative
// revenue or cost input (ErrInvalidFirm) — never silently defaults.
func NewSimpleFirm(name string, revenue, wageCost, inputCost, rent Money) (*SimpleFirm, error) {
	cid := errs.NewCorrelationID()
	if revenue < 0 {
		return nil, errs.New(ErrInvalidFirm, cid, map[string]any{
			"field": "revenue", "value": int64(revenue), "rule": "must be non-negative",
		})
	}
	for _, c := range []struct {
		field string
		value Money
	}{{"wageCost", wageCost}, {"inputCost", inputCost}, {"rent", rent}} {
		if c.value < 0 {
			return nil, errs.New(ErrInvalidFirm, cid, map[string]any{
				"field": c.field, "value": int64(c.value), "rule": "must be non-negative",
			})
		}
	}
	f := &SimpleFirm{
		Name:      name,
		Revenue:   revenue,
		WageCost:  wageCost,
		InputCost: inputCost,
		Rent:      rent,
	}
	f.Open = f.MonthlyProfit() > 0
	return f, nil
}

// MonthlyProfit is the firm's projected monthly P&L:
// revenue − wages − inputs − rent, computed with saturating subtraction
// so extreme inputs saturate rather than wrap (GR#16).
func (s *SimpleFirm) MonthlyProfit() Money {
	p := satSubMoney(s.Revenue, s.WageCost)
	p = satSubMoney(p, s.InputCost)
	p = satSubMoney(p, s.Rent)
	return p
}

// AdvanceMonth advances the firm one month: a profitable month resets
// (and re-opens) the firm; a loss month increments the loss streak and
// closes the firm once the streak reaches firmCloseAfterLossMonths.
func (s *SimpleFirm) AdvanceMonth() {
	if s.MonthlyProfit() < 0 {
		s.lossStreak++
		if s.lossStreak >= firmCloseAfterLossMonths {
			s.Open = false
		}
		return
	}
	s.lossStreak = 0
	s.Open = true
}

// RegisterFirm adds a firm to the FinanceAPI's firm registry and assigns
// it an ID, so CollectTax's corporation-tax input can be summed from
// registered firms via TotalFirmProfit.
func (f *FinanceAPI) RegisterFirm(firm *SimpleFirm) (FirmID, error) {
	if err := f.checkNotCopied("RegisterFirm"); err != nil {
		return 0, err
	}
	if firm == nil {
		return 0, errs.New(ErrInvalidFirm, f.correlationID, map[string]any{
			"field": "firm", "value": "nil", "rule": "must not be nil",
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("RegisterFirm"); err != nil {
		return 0, err
	}
	id := f.nextFirmID
	f.nextFirmID++
	firm.ID = id
	f.firms[id] = firm
	return id, nil
}

// TotalFirmProfit returns the sum of open firms' positive monthly
// profits — the corporation-tax base (AC-3). Loss-making firms
// contribute zero (a v1 simplification; loss carry-forward is
// engine.tax's job).
func (f *FinanceAPI) TotalFirmProfit() Money {
	if err := f.checkNotCopied("TotalFirmProfit"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	// Sum over sorted firm IDs, never map-iteration order (AC-14).
	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var total Money
	for _, id := range ids {
		firm := f.firms[id]
		if !firm.Open {
			continue
		}
		if p := firm.MonthlyProfit(); p > 0 {
			total, _ = satAddMoney(total, p)
		}
	}
	return total
}

// CloseFirm forces a firm closed.
func (f *FinanceAPI) CloseFirm(id FirmID) error {
	if err := f.checkNotCopied("CloseFirm"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("CloseFirm"); err != nil {
		return err
	}
	if firm, ok := f.firms[id]; ok {
		firm.Open = false
	}
	return nil
}
