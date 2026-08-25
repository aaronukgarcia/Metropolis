package firms

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// LabourMarket is the vacancy-vs-workforce aggregate (FEAT-167
// JobAvailability; AC-21..AC-27): the city-wide count of open jobs
// (TotalVacancies), the labour-supply side (Workforce, read live from
// CitizensAPI.TotalPopulation — a labour-supply PROXY, see doc.go's AC-22
// honesty note), and the per-mille ratio (VacancyRatePerMille) that the
// migration wiring maps onto JobAvailability. It is a pure, dimensionless
// query result — no conserved stock is fed (ICD §4).
type LabourMarket struct {
	TotalVacancies      int64
	Workforce           int64
	VacancyRatePerMille int64
}

// bandCeiling returns the staff-count ceiling of the stage's band, derived
// from data/firms.json (GR#15), never a Go-literal headcount (AC-21). For
// Startup/Small/Medium the ceiling is the next stage's minStaff floor − 1
// (so Startup 1..5, Small 6..25, Medium 26..250 track the loaded floors).
// Enterprise has no §45 upper bound ("250+"), so its ceiling is the
// data-declared labourMarket.enterpriseCeiling field.
func (f *FirmsAPI) bandCeiling(st Stage) int64 {
	if st == StageEnterprise {
		return f.cfg.LabourMarket.EnterpriseCeiling
	}
	if next, ok := nextStage(st); ok {
		return f.stageConfigFor(next).MinStaff - 1
	}
	return 0
}

// TotalVacancies returns the city-wide vacancy count: Σ over every firm of
// max(0, bandCeiling(stage) − len(Staff)) (AC-21). It reads only this
// module's own firm registry — the real Staff rosters (AC-4) and the
// data-derived band ceilings — so it does not need citizens wired. Firms
// are iterated in ascending FirmID order (GR#21), never Go map order, and
// the headroom sum saturates (GR#16).
func (f *FirmsAPI) TotalVacancies() int64 {
	if err := f.checkNotCopied("TotalVacancies"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.totalVacanciesLocked()
}

// totalVacanciesLocked computes TotalVacancies; the caller holds f.mu.
func (f *FirmsAPI) totalVacanciesLocked() int64 {
	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var total int64
	for _, id := range ids {
		headroom := f.bandCeiling(f.firms[id].firm.Stage) - int64(len(f.firms[id].firm.Staff))
		if headroom < 0 {
			headroom = 0 // a firm over its band ceiling has no vacancy
		}
		total = num.SatAdd(total, headroom)
	}
	return total
}

// LabourMarket returns the vacancy-vs-workforce aggregate (AC-21..AC-24).
// The workforce side is read LIVE from CitizensAPI.TotalPopulation over the
// already-registered engine.firms → engine.citizens edge (AC-22) — a live
// query at the moment of the call, never a value baked in at SetCitizens
// time. Called before SetCitizens it fails closed with ErrDependencyMissing
// (MET-G1409), never a zero Workforce that would silently make the ratio
// read "no jobs" (AC-24, GR#17).
func (f *FirmsAPI) LabourMarket() (LabourMarket, error) {
	if err := f.checkNotCopied("LabourMarket"); err != nil {
		return LabourMarket{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.citizens == nil {
		return LabourMarket{}, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
			"operation":  "LabourMarket",
			"dependency": "engine.citizens",
		})
	}

	workforce := int64(f.citizens.TotalPopulation(f.correlationID))
	vacancies := f.totalVacanciesLocked()
	return LabourMarket{
		TotalVacancies:      vacancies,
		Workforce:           workforce,
		VacancyRatePerMille: vacancyRatePerMille(vacancies, workforce),
	}, nil
}

// vacancyRatePerMille returns the integer per-mille ratio, division-by-zero
// guarded (AC-23): 0 when workforce <= 0, else vacancies×1000/workforce.
// The numerator multiply saturates (GR#16) and the quotient is truncated
// integer division — never NaN/Inf. There is NO upper clamp: a rate above
// 1000‰ (vacancies exceed workforce) is legal and must grow strictly as
// vacancies rise (AC-23's directionality check), so clamping to [0,1000]
// would be wrong here.
func vacancyRatePerMille(vacancies, workforce int64) int64 {
	if workforce <= 0 {
		return 0
	}
	return satMul(vacancies, 1000) / workforce
}
