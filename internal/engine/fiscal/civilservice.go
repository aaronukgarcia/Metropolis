package fiscal

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SetCivilServiceWageBill sets the gross civil-service wage bill (the
// aggregate gross public-employee payroll for the month) reported by
// [CivilServiceGross]. The composition root sources this by summing
// engine.services' per-service GrossWageCost; this package holds it as a
// documented state field, not a re-derived figure. A negative bill is
// rejected (GR#16 — money is never negative).
func (f *FiscalAPI) SetCivilServiceWageBill(gross finance.Money) error {
	if err := f.checkNotCopied("SetCivilServiceWageBill"); err != nil {
		return err
	}
	if gross < 0 {
		return errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "civilServiceWageBill", "value": int64(gross)})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.civilServiceWageBill = gross
	return nil
}

// CivilServiceGross returns the gross civil-service wage bill (the §54
// "public wages shown gross" half of the honest pair).
func (f *FiscalAPI) CivilServiceGross() finance.Money {
	if err := f.checkNotCopied("CivilServiceGross"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.civilServiceWageBill
}

// incomeRatePercent returns the wired engine.tax's income-tax instrument's
// current headline rate (a percentage), looked up by the instrument's data
// category ("income") — never a hardcoded instrument name (GR#15). The
// caller supplies the already-acquired *tax.TaxAPI.
func (f *FiscalAPI) incomeRatePercent(t *tax.TaxAPI) (float64, error) {
	for _, info := range t.Instruments() {
		if info.Category == dataCatIncome {
			return info.Rate, nil
		}
	}
	return 0, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
		"operation":    "civil-service clawback",
		"dependency":   "engine.tax income-tax instrument",
		"dataCategory": dataCatIncome,
	})
}

// CivilServiceClawback returns the income tax the public employees pay back
// on their own wage (gross × the income-tax instrument's rate), the clawback
// that makes public workers a net fiscal cost. Queried live from
// engine.tax's income-tax instrument (AC-4's "modelled honestly" claim).
func (f *FiscalAPI) CivilServiceClawback() (finance.Money, error) {
	if err := f.checkNotCopied("CivilServiceClawback"); err != nil {
		return 0, err
	}
	t, err := f.requireTax("CivilServiceClawback")
	if err != nil {
		return 0, err
	}
	rate, err := f.incomeRatePercent(t)
	if err != nil {
		return 0, err
	}
	f.mu.RLock()
	gross := f.civilServiceWageBill
	f.mu.RUnlock()
	return f.moneyTimesRate(gross, rate)
}

// CivilServiceNet returns the net civil-service cost: gross wage bill minus
// the income-tax clawback those employees pay back (AC-4). At a zero
// income-tax rate this equals gross; at any positive rate it is less than
// gross — the two figures are always distinct and simultaneously queryable,
// never collapsed into a single "staff cost" number.
func (f *FiscalAPI) CivilServiceNet() (finance.Money, error) {
	if err := f.checkNotCopied("CivilServiceNet"); err != nil {
		return 0, err
	}
	clawback, err := f.CivilServiceClawback()
	if err != nil {
		return 0, err
	}
	return satSub(f.CivilServiceGross(), clawback), nil
}
