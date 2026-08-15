package unlocks

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// XP accrues continuously from the four sources §22 names — construction,
// population, service performance, and milestone progress — each through
// its own documented, per-source award function below, never a single
// opaque gainXP(amount) called ad hoc from unrelated code (AC-2, US-1).
//
// The rate/formula of each source is a documented PLACEHOLDER v1 shape
// (no §4/§22 figure exists for XP rates — the balance-number regime; see
// the logged ASM), so the tests assert direction (XP increases when the
// source fires) rather than a pinned number.

// AwardConstructionXP awards XP for construction spend: one XP per whole
// pound of construction money spent (constructionMoneyMicropounds ÷
// finance.MicropoundsPerPound). Placeholder rate.
func (u *UnlocksAPI) AwardConstructionXP(constructionMoneyMicropounds int64, correlationID string) error {
	if constructionMoneyMicropounds < 0 {
		return errs.New(ErrNegativeAmount, u.correlationID, map[string]any{
			"field": "constructionMoneyMicropounds", "value": constructionMoneyMicropounds,
		})
	}
	return u.awardXP(constructionMoneyMicropounds/micropoundsPerPound, "AwardConstructionXP")
}

// AwardPopulationXP awards XP for population growth: one XP per citizen
// added. Placeholder rate.
func (u *UnlocksAPI) AwardPopulationXP(citizens int64, correlationID string) error {
	if citizens < 0 {
		return errs.New(ErrNegativeAmount, u.correlationID, map[string]any{
			"field": "citizens", "value": citizens,
		})
	}
	return u.awardXP(citizens, "AwardPopulationXP")
}

// AwardServiceXP awards XP for service performance: one XP per
// service-performance point (the 0-100 service score). Placeholder rate.
func (u *UnlocksAPI) AwardServiceXP(performance int64, correlationID string) error {
	if performance < 0 {
		return errs.New(ErrNegativeAmount, u.correlationID, map[string]any{
			"field": "performance", "value": performance,
		})
	}
	return u.awardXP(performance, "AwardServiceXP")
}

// AwardMilestoneProgressXP awards XP for milestone progress: one XP per
// progress-bar point toward the next milestone. This is §22's
// "milestones' progress bar" source — the progress indicator feeding the
// next crossing's bar, distinct from the population-threshold crossing
// itself (which is [UnlocksAPI.AdvancePopulation]).
func (u *UnlocksAPI) AwardMilestoneProgressXP(progress int64, correlationID string) error {
	if progress < 0 {
		return errs.New(ErrNegativeAmount, u.correlationID, map[string]any{
			"field": "progress", "value": progress,
		})
	}
	return u.awardXP(progress, "AwardMilestoneProgressXP")
}

// awardXP adds a non-negative amount to the XP counter. The callers have
// already validated non-negativity; this is the single shared mutation
// point. The addition is saturating (num.SatAdd) so a sequence of huge
// awards can never wrap int64 into a negative XP total (SEC-080/GR#16).
func (u *UnlocksAPI) awardXP(amount int64, method string) error {
	if err := u.checkNotCopied(method); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.xp = num.SatAdd(u.xp, amount)
	return nil
}
