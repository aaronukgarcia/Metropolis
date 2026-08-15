package freight

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// PortCapacity is the three-factor port throughput figure (§33, AC-2):
// berths × crane rate (t/hr) × operating hours, each a named field so a
// consumer can see (and a test can vary) the factors independently.
type PortCapacity struct {
	Berths                 int64
	CraneRateTonnesPerHour int64
	OperatingHoursPerDay   int64
	TonnesPerDay           int64
}

// PortCapacity returns the port's physical throughput capacity, computed
// as berths × craneRateTonnesPerHour × operatingHoursPerDay (§33's
// multiplicative formula — never a flat daily-tonnage constant, AC-2).
// Errors with ErrNoBerthsConfigured while the loaded port config carries
// zero berths (the port is not yet built), never a silently-returned zero
// figure (AC-12).
func (f *FreightAPI) PortCapacity() (PortCapacity, error) {
	if err := f.checkNotCopied("PortCapacity"); err != nil {
		return PortCapacity{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.cfg.Port.Berths <= 0 {
		return PortCapacity{}, errs.New(ErrNoBerthsConfigured, f.correlationID, map[string]any{
			"berths": f.cfg.Port.Berths,
		})
	}
	throughput, _ := num.SafeMul(f.cfg.Port.CraneRateTonnesPerHour, f.cfg.Port.OperatingHoursPerDay)
	throughput, _ = num.SafeMul(throughput, f.cfg.Port.Berths)
	return PortCapacity{
		Berths:                 f.cfg.Port.Berths,
		CraneRateTonnesPerHour: f.cfg.Port.CraneRateTonnesPerHour,
		OperatingHoursPerDay:   f.cfg.Port.OperatingHoursPerDay,
		TonnesPerDay:           throughput,
	}, nil
}

// CustomsCapacity is the customs throughput capacity (§33/§28, AC-3) — a
// figure SEPARATE from the port's physical berth/crane throughput, so the
// two can saturate independently.
type CustomsCapacity struct {
	TonnesPerDay int64
}

// CustomsCapacity returns the port's customs throughput capacity. Errors
// with ErrNoBerthsConfigured while zero berths are configured (AC-12) —
// the same "port not yet built" state PortCapacity reports, since customs
// exists only once the port does.
func (f *FreightAPI) CustomsCapacity() (CustomsCapacity, error) {
	if err := f.checkNotCopied("CustomsCapacity"); err != nil {
		return CustomsCapacity{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.cfg.Port.Berths <= 0 {
		return CustomsCapacity{}, errs.New(ErrNoBerthsConfigured, f.correlationID, map[string]any{
			"berths": f.cfg.Port.Berths,
		})
	}
	return CustomsCapacity{TonnesPerDay: f.cfg.Port.CustomsCapacityTonnesPerDay}, nil
}

// CustomsSaturation is the customs demand-vs-capacity reading (AC-3): the
// tonnage demanded of customs this tick, the capacity, and the resulting
// saturation ratio (demand ÷ capacity, may exceed 1 when backed up).
type CustomsSaturation struct {
	Demanded   int64
	Capacity   int64
	Saturation float64
}

// CustomsSaturation returns the current tick's customs demand against the
// customs capacity. Demand accumulates from every import and export
// movement (both pass customs, §28). Errors with ErrNoBerthsConfigured
// while zero berths are configured (AC-12).
func (f *FreightAPI) CustomsSaturation() (CustomsSaturation, error) {
	if err := f.checkNotCopied("CustomsSaturation"); err != nil {
		return CustomsSaturation{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.cfg.Port.Berths <= 0 {
		return CustomsSaturation{}, errs.New(ErrNoBerthsConfigured, f.correlationID, map[string]any{
			"berths": f.cfg.Port.Berths,
		})
	}
	capacity := f.cfg.Port.CustomsCapacityTonnesPerDay
	demanded := f.customsDemanded
	var saturation float64
	if capacity > 0 {
		saturation = float64(demanded) / float64(capacity)
	}
	return CustomsSaturation{Demanded: demanded, Capacity: capacity, Saturation: saturation}, nil
}

// SmugglingRisk is the §28 smuggling-risk indicator (AC-3): it rises as
// customs saturation increases (saturated customs is the smuggling
// opportunity §28 names). It is derived, not stored: min(1, saturation),
// so it reads 0 at no demand and 1 at or beyond full saturation — the
// exact curve is a directional placeholder (the spec states the direction,
// not the magnitude).
func (f *FreightAPI) SmugglingRisk() (float64, error) {
	sat, err := f.CustomsSaturation()
	if err != nil {
		return 0, err
	}
	if sat.Saturation < 0 {
		return 0, nil
	}
	if sat.Saturation > 1 {
		return 1, nil
	}
	return sat.Saturation, nil
}
