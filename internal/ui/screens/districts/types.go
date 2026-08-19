package districts

// District is a named district's identity (AC-2). BLOCKED today — see
// doc.go: engine.policies is not on main, so no source populates the
// screen's district roster. This type exists so wireDistrict's shape has a
// same-named domain type for future accessors/render code, mirroring
// ui.screen.services' PieSlice/wirePieSlice "forward compatibility only"
// convention (services/types.go).
type District struct {
	DistrictID string
	Name       string
}

// DistrictTaxSetting is AC-6's per-district tax-multiplier figure: one
// (district, instrument) pair's citywide rate, per-district multiplier and
// the engine-computed effective rate, sourced live from engine.tax's
// TaxAPI (merged on main). Mirrors internal/engine/tax/tax.go's
// SetDistrictMultiplier/InstrumentInfo fields exactly — Multiplier stacks
// with Rate (EffectiveRate = Rate * Multiplier); RateMax is the same
// SEC-098 cap the engine enforces, reused here rather than re-derived.
type DistrictTaxSetting struct {
	DistrictID      string
	InstrumentID    string
	InstrumentLabel string
	Multiplier      float64
	Rate            float64
	RateMax         float64
	EffectiveRate   float64
}
