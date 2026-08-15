package unlocks

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AdvancePopulation updates the city's population and crosses every
// milestone tier whose §4 population threshold the new population has
// reached, in ascending tier order (deterministic — never map order,
// GR#21/AC-14). It returns the milestones crossed in that order.
//
// Crossing a tier grants, in the same call (US-2's "the instant the
// threshold is crossed, not on a delayed check"):
//
//   - its signature unlocks becoming gate-passable (dynamic — the
//     milestone gate [UnlocksAPI.MilestoneReached] and the node-tier
//     prerequisites now resolve true; no delayed re-check);
//   - an expansion-permit allowance increase;
//   - a cash award posted through the wired engine.finance (US-7);
//   - a loan-facility uplift ([UnlocksAPI.MilestoneReached] now true for
//     the tier, which engine.finance.Borrow consumes); and
//   - a Development-Point grant (§22).
//
// A population value below the current one (a Detroit-spiral emigration
// dip, §12) is recorded but never revokes a crossed tier — the tier is a
// higher-water mark (AC-5). A negative population is rejected
// (GR#16). If a tier crossing needs to post a cash award but no
// engine.finance is wired, the call fails with ErrFinanceNotWired before
// any state is mutated (never a silent no-op, GR#17).
func (u *UnlocksAPI) AdvancePopulation(population int64, correlationID string) ([]Milestone, error) {
	if err := u.checkNotCopied("AdvancePopulation"); err != nil {
		return nil, err
	}
	if population < 0 {
		return nil, errs.New(ErrNegativeAmount, u.correlationID, map[string]any{
			"field": "population", "value": population,
		})
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	tier := int(u.tier.Load())

	// SEC-081: if a crossing will occur, engine.finance must be wired
	// BEFORE we mutate population — a rejected crossing must leave
	// population (and every other counter) untouched, never
	// "population updated but the cash award could not post".
	if tier < len(milestoneLadder) && milestoneLadder[tier].Population <= population && u.finance == nil {
		return nil, errs.New(ErrFinanceNotWired, u.correlationID, map[string]any{
			"operation": "AdvancePopulation",
		})
	}

	u.population = population

	var crossed []Milestone
	// tier is the current tier number (0 = none); the next tier to cross
	// is tier+1, whose ladder entry is milestoneLadder[tier].
	for tier < len(milestoneLadder) {
		m := milestoneLadder[tier] // index = (next tier number) - 1
		if m.Population > population {
			break
		}
		if err := u.applyMilestoneLocked(m, correlationID); err != nil {
			return nil, err
		}
		tier++
		u.tier.Store(int32(tier))
		crossed = append(crossed, m)
	}
	return crossed, nil
}

// applyMilestoneLocked applies one milestone crossing's grants (u.mu is
// held). The signature-unlock and loan-uplift halves are dynamic (they
// follow from u.tier advancing in the caller), so this only mutates the
// concrete counters: the finance cash award, the DP grant, and the
// expansion-permit allowance.
func (u *UnlocksAPI) applyMilestoneLocked(m Milestone, correlationID string) error {
	award := cashAwardPerMilestone
	if _, err := u.finance.Post(finance.Transaction{
		Description: fmt.Sprintf("milestone %d (%s) cash award", m.Tier, m.Name),
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: award, Category: categoryMilestoneAward},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: award, Category: categoryMilestoneAward},
		},
	}); err != nil {
		// A cash award is an external inflow (external.world debited,
		// treasury credited), always balanced and never overdraft-limited,
		// so the only realistic failure is a copied/foreign FinanceAPI —
		// surface that registry-sourced error rather than swallowing it.
		return err
	}
	u.dp += dpGrantPerMilestone
	u.expansionPermits += permitGrantPerMilestone
	return nil
}
