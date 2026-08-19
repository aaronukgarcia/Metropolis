package fiscal

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// SetChildcarePlaces sets the number of subsidised childcare places, the
// input to [ChildcareNetLine] (AC-6). A negative count is rejected (GR#16).
func (f *FiscalAPI) SetChildcarePlaces(places int64) error {
	if err := f.checkNotCopied("SetChildcarePlaces"); err != nil {
		return err
	}
	if places < 0 {
		return errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "childcarePlaces", "value": places})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.childcarePlaces = places
	return nil
}

// ChildcarePlaces returns the current number of subsidised childcare places.
func (f *FiscalAPI) ChildcarePlaces() int64 {
	if err := f.checkNotCopied("ChildcarePlaces"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.childcarePlaces
}

// ChildcareNetLine returns the §54 childcare subsidy as a net line (AC-6):
// three distinct, simultaneously-queryable values —
//
//   - GrossSpend: the gross subsidy cost (places × per-place subsidy);
//   - TaxYield: the income-tax revenue the subsidy unlocks via higher
//     second-earner participation (uplift × average second-earner wage × the
//     income-tax instrument's rate, queried live from engine.tax);
//   - Net: gross spend minus that tax yield.
//
// The arithmetic is exact int64 fixed-point (GR#16): money figures route
// through num.SafeMul / moneyTimesRate, never a bare int64 add that could
// wrap. Net is GrossSpend − TaxYield, clamped at 0 (SEC-149, GR#16
// money-is-never-negative): when TaxYield exceeds GrossSpend — e.g. the
// income-tax instrument at its own max rate — Net is exactly 0, not a
// negative figure. That surplus is NOT redistributed anywhere by this
// function; a caller must not assume Net == GrossSpend − TaxYield or try to
// reconstruct GrossSpend from Net + TaxYield once the clamp has engaged.
func (f *FiscalAPI) ChildcareNetLine() (ChildcareNetLine, error) {
	if err := f.checkNotCopied("ChildcareNetLine"); err != nil {
		return ChildcareNetLine{}, err
	}
	t, err := f.requireTax("ChildcareNetLine")
	if err != nil {
		return ChildcareNetLine{}, err
	}
	rate, err := f.incomeRatePercent(t)
	if err != nil {
		return ChildcareNetLine{}, err
	}
	f.mu.RLock()
	places := f.childcarePlaces
	f.mu.RUnlock()

	cc := f.cfg.Childcare

	var line ChildcareNetLine
	if places == 0 {
		return line, nil
	}

	gross, overflow := num.SafeMul(places, cc.SubsidyPerPlacePerMonthMicroPounds)
	if overflow {
		return ChildcareNetLine{}, errs.New(ErrFiscalOverflow, f.correlationID, map[string]any{
			"field": "childcare.grossSpend", "places": places,
		})
	}
	line.GrossSpend = finance.Money(gross)

	// second earners drawn into work = floor(places × uplift-per-place).
	uplift := int64(math.Floor(float64(places) * cc.SecondEarnerUpliftPerPlace))
	if uplift < 0 {
		uplift = 0
	}
	extraWages, overflow := num.SafeMul(uplift, cc.SecondEarnerAvgWagePerMonthMicroPounds)
	if overflow {
		return ChildcareNetLine{}, errs.New(ErrFiscalOverflow, f.correlationID, map[string]any{
			"field": "childcare.extraWages", "uplift": uplift,
		})
	}
	line.TaxYield, err = f.moneyTimesRate(finance.Money(extraWages), rate)
	if err != nil {
		return ChildcareNetLine{}, err
	}

	// Net is clamped at 0 (GR#16 money-is-never-negative): the subsidy is
	// documented as only partially self-funding, never a net revenue
	// generator, so a TaxYield that would exceed GrossSpend (e.g. the
	// income-tax instrument at its own max 60% rate) must not flow out as a
	// negative money figure (SEC-149).
	line.Net = satSub(line.GrossSpend, line.TaxYield)
	if line.Net < 0 {
		line.Net = 0
	}
	return line, nil
}
