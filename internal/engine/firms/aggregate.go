package firms

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// AggregateOutputScale is BUG-745's fix. Prior to this, Financial.
// OutputScale (AC-8's market-input-availability scale, set only by
// [FirmsAPI.ResolveMonth]'s applyInputScalingLocked step) had exactly one
// consumer: ResolveMonth's own credit-failure check
// (effective := MonthlyCashFlow*OutputScale/1000, compared against the
// month's borrowing cost). It never reached anything compose posts to the
// ledger — a firm whose output was forced to 0 for 24 straight months left
// the money loop (treasury, tracked household wealth) byte-identical to a
// firm running at full output, because nothing outside ResolveMonth ever
// read the field.
//
// AggregateOutputScale gives compose (engine.compose already depends on
// engine.firms — code.json's feat.compositionroot -> engine.firms edge,
// this is a new METHOD on that existing edge, not a new edge) a single
// city-wide per-mille figure it can multiply the money leg that funds from
// firm output by (moneycirc.go's postConsumptionAndTax revenue leg — see
// that call site's doc comment for exactly how the multiply is applied,
// and for why the private-sector wage bill is deliberately NOT also
// scaled by this today).
//
// It is a workforce-weighted average (Σ OutputScale×staffCount / Σ
// staffCount) rather than a flat per-firm average, so a handful of tiny or
// unstaffed firms cannot swing the citywide figure as much as the firms
// that actually employ people — the same "weight by what actually matters"
// principle LabourMarket's Workforce side already applies. A firm with zero
// staff contributes weight 1 (never 0 — a divide-by-zero-free floor that
// also keeps a genuinely-unstaffed firm from being silently excluded rather
// than just lightly weighted).
//
// Firms are folded in ascending FirmID order (GR#21 — never a Go map
// range): the result cannot depend on map iteration order. No firms (a
// pre-firms-module city, or simply no firm registered yet) — or,
// degenerately, a firm set that sums to zero total weight — reads the
// documented NEUTRAL value 1000 (full output), never 0: an empty or
// unstaffed city must never read as a total productivity collapse it never
// actually suffered (GR#17 — a status figure must not fabricate severity
// from missing data, the same rule compose_wellbeing.go's
// WellbeingStatus.NoData branch already follows).
//
// Read-only: this method never mutates any firm's stored OutputScale and
// never calls ResolveMonth — it only reports the LAST value ResolveMonth (or
// RegisterFirm/founding's initial 1000) left on each firm. A firm whose
// OutputScale has never been recomputed by ResolveMonth simply reads the
// documented 1000 it was founded with.
func (f *FirmsAPI) AggregateOutputScale() (int64, error) {
	if err := f.checkNotCopied("AggregateOutputScale"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.aggregateOutputScaleLocked(), nil
}

// aggregateOutputScaleLocked computes AggregateOutputScale; the caller
// holds f.mu (read or write).
func (f *FirmsAPI) aggregateOutputScaleLocked() int64 {
	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var weightedSum, totalWeight int64
	for _, id := range ids {
		fs := f.firms[id]
		weight := int64(len(fs.firm.Staff))
		if weight <= 0 {
			weight = 1
		}
		weightedSum = num.SatAdd(weightedSum, satMul(fs.firm.Financial.OutputScale, weight))
		totalWeight = num.SatAdd(totalWeight, weight)
	}
	if totalWeight <= 0 {
		return 1000
	}
	return weightedSum / totalWeight
}
