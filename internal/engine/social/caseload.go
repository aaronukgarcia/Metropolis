package social

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file holds the decomposed per-category caseload generation (AC-2):
// five categories, each a documented function of ITS OWN driver subset —
// never one blended "social need" score split into five labels. The coupling
// follows §40:
//
//	Family support & child protection ← deprivation + crowding + financial
//	    stress + (discrete crisis events, injected separately — AC-5).
//	Homelessness ← deprivation + unemployment duration + financial stress.
//	Disability & carers ← deprivation only (NOT unemployment duration, the
//	    AC-2 isolation anchor: §40 does not tie disability to unemployment).
//	Fostering ← crowding + financial stress (plus escalations from family
//	    support via EscalateCase).
//	Addiction ← the nightlife/deprivation coupling (Deprivation ×
//	    NightlifeDensity, §40).
//
// Every rate is data-sourced (GR#15); the only arithmetic here is the
// deterministic sum-and-round of a driver value against its data rate.

// generateCaseload is the pure, deterministic steady-state generator: a
// function of (Config, DriverInputs) only (AC-15). It returns the per-category
// case counts for one month. No wall clock, no shared state, no map
// iteration.
func generateCaseload(cfg Config, in DriverInputs) [numCategories]int64 {
	c := cfg.Caseload
	var out [numCategories]int64

	unemp := float64(in.UnemploymentMonths)
	if unemp < 0 {
		unemp = 0
	}
	if unemp > c.UnemploymentCapMonths {
		unemp = c.UnemploymentCapMonths
	}

	out[CategoryFamilySupport] = caseloadCount(
		in.Deprivation*c.FamilyPerDeprivation +
			in.CrowdingStress*c.FamilyPerCrowdingStress +
			in.FinancialStress*c.FamilyPerFinancialStress)
	out[CategoryHomelessness] = caseloadCount(
		in.Deprivation*c.HomelessnessPerDeprivation +
			unemp*c.HomelessnessPerUnemploymentMonth +
			in.FinancialStress*c.HomelessnessPerFinancialStress)
	out[CategoryDisabilityCarers] = caseloadCount(in.Deprivation * c.DisabilityPerDeprivation)
	out[CategoryFostering] = caseloadCount(
		in.CrowdingStress*c.FosteringPerCrowdingStress +
			in.FinancialStress*c.FosteringPerFinancialStress)
	out[CategoryAddiction] = caseloadCount(in.Deprivation * in.NightlifeDensity * c.AddictionPerPressure)
	return out
}

// maxCaseloadProposalsPerMonth bounds the number of caseload case-proposals a
// single month (steady-state via generateCaseload, or one crisis event via
// InjectCrisis) may generate and allocate. It is a resource ceiling — the same
// shape as engine.core's MaxAdvanceTicksPerCall and engine.fdi's
// maxSupplyChainFirms — NOT a balance number: the balance magnitudes live in
// data/social.json, and this ceiling exists only so a hostile or malformed
// driver/rate combination cannot turn one GenerateCaseload/AdvanceMonth/
// InjectCrisis call into a hang or OOM, or permanently poison the conserved
// ledger with a month's worth of unbounded cases (SEC-195). The magnitude
// drivers (CrowdingStress/FinancialStress) are documented "finite and >= 0"
// and the config rates "finite and non-negative" — each individually in-domain
// — yet their product can still be pathological, so the bound sits on the
// COUNT at the allocation site, never on the driver magnitude (which would be
// an invented balance number). The shipped data rates (a few cases per unit)
// and realistic driver magnitudes sit several orders of magnitude below this
// ceiling, so a legitimate balance pass never reaches it.
const maxCaseloadProposalsPerMonth int64 = 100_000

// totalCaseload sums the per-category proposal counts with saturation (GR#16),
// so a pathological driver that saturates several categories to MaxInt64
// saturates the total rather than wrapping negative and defeating the bound.
func totalCaseload(counts [numCategories]int64) int64 {
	var total int64
	for _, cat := range categoryOrder {
		total = num.SatAdd(total, counts[cat])
	}
	return total
}

// checkProposalLimit rejects a proposed per-month (or per-event) case count
// that exceeds the resource ceiling (SEC-195). It is the single choke point
// every allocation-proportional-to-a-driver/rate path funnels through, so the
// bound is on the COUNT (a resource concern) rather than on any driver
// magnitude or rate (a balance concern — ASM-1366's magnitude drivers stay
// "finite and >= 0" with no invented upper bound).
func checkProposalLimit(total int64, correlationID string) error {
	if total > maxCaseloadProposalsPerMonth {
		return errs.New(ErrCaseloadExceedsLimit, correlationID, map[string]any{
			"proposals": total,
			"max":       maxCaseloadProposalsPerMonth,
		})
	}
	return nil
}

// GenerateCaseload exposes the steady-state generator as a decomposed case
// proposal list (AC-1/AC-2): one NewCase per case the given month's driver
// set would open, each carrying its category and a "steady-state" source.
// It is a pure function of (month, DriverInputs) — the same inputs always
// yield the same output (AC-15). The returned cases are proposals; opening
// them into the ledger is AdvanceMonth's job.
//
// The DriverInputs domain is enforced at this boundary (SEC-181): an
// out-of-domain driver value — a fraction driver outside [0,1], a magnitude
// driver that is negative or non-finite, or a negative unemployment duration
// — is rejected with ErrInvalidDriverInput rather than silently producing an
// unbounded proposal count (a large finite Deprivation would otherwise yield
// ~900k proposals and a larger one would exhaust memory). The two magnitude
// drivers are in-domain while finite and non-negative, so their unbounded
// product with the rates is instead bounded at the allocation site: a month
// whose total proposed count exceeds maxCaseloadProposalsPerMonth is rejected
// with ErrCaseloadExceedsLimit before any allocation (SEC-195).
func (a *SocialAPI) GenerateCaseload(month int64, in DriverInputs) ([]NewCase, error) {
	if err := a.checkNotCopied("GenerateCaseload"); err != nil {
		return nil, err
	}
	if err := validateDriverInputs(in, a.correlationID); err != nil {
		return nil, err
	}
	counts := generateCaseload(a.cfg, in)
	if err := checkProposalLimit(totalCaseload(counts), a.correlationID); err != nil {
		return nil, err
	}
	var out []NewCase
	for _, cat := range categoryOrder {
		for i := int64(0); i < counts[cat]; i++ {
			out = append(out, NewCase{Category: cat, Source: "steady-state"})
		}
	}
	return out, nil
}

// validateDriverInputs enforces the documented DriverInputs domain at the
// write boundary (SEC-181/GR#16): the two fraction drivers are in [0,1], the
// two magnitude drivers are finite and ≥ 0, and UnemploymentMonths is ≥ 0.
// It rejects (never sanitizes) an out-of-domain value with a registry-sourced
// error, because a large finite driver value is a resource-exhaustion vector
// (proposal count is proportional to it) — weakness pattern #1: an invariant
// stated in prose is not enforced.
func validateDriverInputs(in DriverInputs, correlationID string) error {
	if !numFinite(in.Deprivation) || in.Deprivation < 0 || in.Deprivation > 1 {
		return errs.New(ErrInvalidDriverInput, correlationID, map[string]any{
			"field": "deprivation", "value": in.Deprivation, "domain": "[0,1]",
		})
	}
	if in.UnemploymentMonths < 0 {
		return errs.New(ErrInvalidDriverInput, correlationID, map[string]any{
			"field": "unemploymentMonths", "value": in.UnemploymentMonths, "domain": ">= 0",
		})
	}
	if !numFinite(in.CrowdingStress) || in.CrowdingStress < 0 {
		return errs.New(ErrInvalidDriverInput, correlationID, map[string]any{
			"field": "crowdingStress", "value": in.CrowdingStress, "domain": ">= 0 (finite)",
		})
	}
	if !numFinite(in.FinancialStress) || in.FinancialStress < 0 {
		return errs.New(ErrInvalidDriverInput, correlationID, map[string]any{
			"field": "financialStress", "value": in.FinancialStress, "domain": ">= 0 (finite)",
		})
	}
	if !numFinite(in.NightlifeDensity) || in.NightlifeDensity < 0 || in.NightlifeDensity > 1 {
		return errs.New(ErrInvalidDriverInput, correlationID, map[string]any{
			"field": "nightlifeDensity", "value": in.NightlifeDensity, "domain": "[0,1]",
		})
	}
	return nil
}
