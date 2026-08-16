package tax

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Data-category values (§39's five instrument categories, as authored in
// data/tax_instruments.json). These are the data file's own enum values,
// referenced here only to map a loaded instrument onto its ledger posting —
// never an instrument-NAME literal (GR#15: instrument identity comes from
// the loaded data, not a Go switch on a name).
const (
	categoryConsumption     = "consumption"
	categoryImport          = "import"
	categoryCorporateProfit = "corporateProfit"
	categoryIncome          = "income"
	categoryProperty        = "property"
)

// zoneClassEnum is §34's closed 8-way zone-class key set, the only valid
// ZoneCell.ZoneClass values BusinessRateRevenue accepts. It mirrors
// foundation/data's own zoneClassEnum (which validates the data file's
// zoneOverrides keys) — the duplication across the module boundary is the
// weakness-pattern-#2 shape, kept honest by TestZoneClassEnumCoversData.
var zoneClassEnum = map[string]bool{
	"dwelling": true, "shop": true, "office": true, "entertainment": true,
	"farming": true, "manufacturing": true, "heavyIndustry": true, "mining": true,
}

// residentialBearerCategories are the incidence bearer categories that mark
// a property-category instrument as residential (paid by households) rather
// than commercial (paid by firms). Data values, not instrument names.
var residentialBearerCategories = map[string]bool{
	"ownerOccupier": true,
	"landlord":      true,
	"tenant":        true,
}

// postingSpec is the ledger posting for one instrument: which money account
// remits the tax, and which finance tax category the flow is tagged with.
type postingSpec struct {
	payer finance.AccountID
	cat   finance.Category
}

// postingFor maps a loaded instrument onto its double-entry posting, keyed
// off the instrument's data category (and, for the two property-category
// instruments, its incidence bearer set) — never an instrument-name literal.
func postingFor(def data.TaxInstrument) postingSpec {
	switch def.Category {
	case categoryIncome:
		return postingSpec{payer: finance.AcctHouseholds, cat: finance.CatTaxIncome}
	case categoryConsumption, categoryImport:
		return postingSpec{payer: finance.AcctFirms, cat: finance.CatTaxSales}
	case categoryCorporateProfit:
		return postingSpec{payer: finance.AcctFirms, cat: finance.CatTaxCorp}
	case categoryProperty:
		if isResidential(def) {
			return postingSpec{payer: finance.AcctHouseholds, cat: finance.CatTaxIncome}
		}
		return postingSpec{payer: finance.AcctFirms, cat: finance.CatTaxCorp}
	default:
		// Unreachable: the loader validates category against the closed enum.
		return postingSpec{payer: finance.AcctFirms, cat: finance.CatTaxCorp}
	}
}

// isResidential reports whether a property-category instrument's incidence
// bearer set marks it residential (owner/landlord/tenant) rather than
// commercial (firm/consumer).
func isResidential(def data.TaxInstrument) bool {
	if def.BearerWeights == nil {
		return false
	}
	for _, rp := range def.BearerWeights.RatePoints {
		for _, b := range rp.Bearers {
			if residentialBearerCategories[b.Category] {
				return true
			}
		}
	}
	return false
}

// referenceRate returns def's elasticity/incidence reference rate: the
// lowest bearer-weight rate point (the data-authored baseline point). It is
// the rate at which the base is "full" (no elasticity shrinkage) and the
// anchor of the incidence interpolation.
func referenceRate(def data.TaxInstrument) float64 {
	if def.BearerWeights == nil || len(def.BearerWeights.RatePoints) == 0 {
		return 0
	}
	r := def.BearerWeights.RatePoints[0].RatePercent
	for _, rp := range def.BearerWeights.RatePoints[1:] {
		if rp.RatePercent < r {
			r = rp.RatePercent
		}
	}
	return r
}

// elasticityCoeff returns def's elasticity coefficient (0 if the pointer is
// somehow absent — the loader guarantees presence, but a guarded read keeps
// a nil deref impossible, GR#1).
func elasticityCoeff(def data.TaxInstrument) float64 {
	if def.Elasticity == nil {
		return 0
	}
	return def.Elasticity.Coefficient
}

// moneyFromFloat converts a non-negative floating-point money figure back to
// finance.Money (int64 micro-pounds). It is the one place a float
// intermediate (an int64 base times a fractional elasticity/EV factor or a
// percentage rate) becomes stored money — the stored value is always int64
// (AC-10/GR#16). The clamp and the NaN→0 guard are foundation/num's
// ClampInt64FromFloat (GR#3 reuse-first, SEC-099); the only addition here is
// the money-specific non-negativity guard so a -Inf/negative input can never
// become MinInt64 (money is never negative, GR#16).
func moneyFromFloat(f float64) finance.Money {
	if f <= 0 {
		return 0
	}
	return finance.Money(num.ClampInt64FromFloat(f))
}

// elasticatedBase returns the float64 taxed base at rate r — full base ×
// (1 − EV share) × elasticity factor — WITHOUT rounding to int64. It is the
// single shared core (GR#3) behind both the int64 base query [taxedBaseAt]
// and [revenueAt], which multiplies by r/100 before its one rounding so a
// sub-micro-pound base is never rounded to zero ahead of the rate multiply
// (SEC-098: revenue must stay monotonic in rate).
func elasticatedBase(fullBase finance.Money, evShare, rRef, e, r float64) float64 {
	if fullBase <= 0 || evShare >= 1 {
		return 0
	}
	f := float64(fullBase) * (1 - evShare)
	if r > rRef && e > 0 {
		f *= math.Pow(rRef/r, e)
	}
	return f
}

// taxedBaseAt returns the rate-responsive, EV-eroded taxed base:
//
//	base = fullBase × (1 − evShare) × clamp((rRef/r)^e, max 1)
//
// The (1 − evShare) term is the external base-erosion input (the fuel-duty
// EV-share shape, AC-9); the power-law term is the rate elasticity (base
// shrinks as the rate climbs above the reference rate, never expands below
// it). Both are fractional factors on an int64 base; the result is rounded
// back to finance.Money by moneyFromFloat.
func taxedBaseAt(fullBase finance.Money, evShare, rRef, e, r float64) finance.Money {
	return moneyFromFloat(elasticatedBase(fullBase, evShare, rRef, e, r))
}

// revenueAt returns the revenue at rate r (a percentage): (r/100) × the
// rate-responsive base. Because the base itself shrinks with r, revenue is
// concave in r — the Laffer shape AC-4 requires, not a straight rate ×
// fixedBase line. The revenue is computed in float and rounded once at the
// end (SEC-098): the base is never rounded to an int64 micro-pound before
// the rate multiply, so revenue stays monotonic in r.
func revenueAt(fullBase finance.Money, evShare, rRef, e, r float64) finance.Money {
	return moneyFromFloat(elasticatedBase(fullBase, evShare, rRef, e, r) * r / 100)
}

// satAddMoney adds two money values with int64 saturation (GR#16) so a
// cross-instrument sum can never wrap. It is a thin adapter over
// foundation/num's canonical SatAddChecked.
func satAddMoney(a, b finance.Money) finance.Money {
	v, _ := num.SatAddChecked(int64(a), int64(b))
	return finance.Money(v)
}

// BearerShare is one incidence-holder category and its fractional share of
// the burden at the queried rate. Shares across all categories sum to 1.0.
type BearerShare struct {
	Category string
	Share    float64
}

// incidenceSharesAt returns def's bearer split at rate r, linearly
// interpolated between the instrument's data-loaded bearer-weight rate
// points (clamped at the extremes) and renormalised so the shares sum to
// exactly 1.0. Category order is deterministic (the union of the bracketing
// points' categories, low point first).
func incidenceSharesAt(def data.TaxInstrument, r float64) []BearerShare {
	if def.BearerWeights == nil || len(def.BearerWeights.RatePoints) == 0 {
		return nil
	}
	pts := make([]data.RatePoint, len(def.BearerWeights.RatePoints))
	copy(pts, def.BearerWeights.RatePoints)
	sort.Slice(pts, func(i, j int) bool { return pts[i].RatePercent < pts[j].RatePercent })

	var low, high data.RatePoint
	switch {
	case r <= pts[0].RatePercent:
		low, high = pts[0], pts[0]
	case r >= pts[len(pts)-1].RatePercent:
		low, high = pts[len(pts)-1], pts[len(pts)-1]
	default:
		for i := 0; i < len(pts)-1; i++ {
			if r >= pts[i].RatePercent && r <= pts[i+1].RatePercent {
				low, high = pts[i], pts[i+1]
				break
			}
		}
	}

	lowShares := map[string]float64{}
	for _, b := range low.Bearers {
		lowShares[b.Category] = b.Share
	}
	highShares := map[string]float64{}
	for _, b := range high.Bearers {
		highShares[b.Category] = b.Share
	}

	var categories []string
	seen := map[string]bool{}
	for _, b := range low.Bearers {
		if !seen[b.Category] {
			seen[b.Category] = true
			categories = append(categories, b.Category)
		}
	}
	for _, b := range high.Bearers {
		if !seen[b.Category] {
			seen[b.Category] = true
			categories = append(categories, b.Category)
		}
	}

	t := 0.0
	if high.RatePercent != low.RatePercent {
		t = (r - low.RatePercent) / (high.RatePercent - low.RatePercent)
	}
	shares := make([]BearerShare, 0, len(categories))
	sum := 0.0
	for _, cat := range categories {
		s := lowShares[cat] + t*(highShares[cat]-lowShares[cat])
		shares = append(shares, BearerShare{Category: cat, Share: s})
		sum += s
	}
	if sum > 0 {
		for i := range shares {
			shares[i].Share /= sum
		}
	}
	return shares
}
