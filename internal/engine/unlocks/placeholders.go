package unlocks

import "github.com/aaronukgarcia/Metropolis/internal/engine/finance"

// micropoundsPerPound is M0-ENG §1.2's fixed money scale, reused from
// engine.finance's exported constant rather than re-derived (GR#3) — the
// single source of the "1 pound = 1,000,000 micro-pounds" figure.
const micropoundsPerPound = int64(finance.MicropoundsPerPound)

// The balance magnitudes below are PLACEHOLDER v1 shapes: §22 names the
// four currencies but gives no figure for any award, grant, or price, so
// these are directional stand-ins pending Aaron's balance pass (the
// balance-number regime). They are recorded as one ASM rather than
// scattered per-constant; tests assert direction and independence, never
// a pinned number.
const (
	// cashAwardPerMilestone is the flat cash award every milestone
	// crossing posts through engine.finance (§4: "a cash award").
	cashAwardPerMilestone finance.Money = 100_000 * finance.MicropoundsPerPound

	// dpGrantPerMilestone is the flat Development-Point grant every
	// milestone crossing awards (§22: milestones grant Development
	// Points).
	dpGrantPerMilestone int64 = 10

	// permitGrantPerMilestone is the flat expansion-permit allowance
	// increase every milestone crossing grants (§4: "an expansion-permit
	// allowance").
	permitGrantPerMilestone int64 = 1
)

// Ledger-entry categories this module posts under, so engine.finance's
// LinesByCategory can drill through to milestone awards and off-map
// capacity purchases (AC-11's drill-through rule). engine.finance.Category
// is a plain string tag, so these module-specific tags slot alongside the
// finance package's own without a cross-module edit.
const (
	categoryMilestoneAward finance.Category = "milestone.award"
	categoryCapacityBuy    finance.Category = "capacity.buy"
)
