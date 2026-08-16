// Package tax implements the Metropolis economy's taxation module
// (BOW MOD-052; module key `engine.tax`; GUID
// 0b7ec47b-a0bc-4ead-b39e-296c9fb39142; spec §39 "Taxation — fine-grain
// controls" and §49 fuel-duty erosion).
//
// It owns the full instrument panel: six data-defined instruments loaded
// from data/tax_instruments.json (via foundation/data's LoadTaxInstruments —
// GR#15, no instrument figure is a Go literal), each carrying a headline
// rate, a real rate-elasticity response (the taxed base shrinks as the rate
// climbs), a rate-dependent incidence split (who bears the cost at the
// current rate), an optional per-district rate multiplier, and an optional
// external base-erosion input (the fuel-duty EV-share shape, §49). Revenue
// is computed from the current rate multiplied by the current,
// rate-responsive base — never a cached pre-change value — so the revenue
// curve is genuinely Laffer-shaped rather than a straight rate × fixed-base
// line.
//
// # Money model
//
// Every monetary value this package produces or accepts reuses
// engine.finance's [finance.Money] (int64 micro-pounds, M0-ENG §1.2) —
// never a locally-declared money type and never a float32/float64 monetary
// field. Rate percentages, elasticity coefficients and incidence shares are
// legitimately fractional and stay float64; the money result of multiplying
// an int64 base by a fractional factor is rounded back to [finance.Money]
// via a saturating conversion (moneyFromFloat).
//
// # Determinism
//
// Nothing in this package reads the wall clock. Every cross-instrument or
// cross-district summation iterates instrument/district keys in sorted
// order (GR#21) — never Go map-iteration order.
//
// # Incidence display (AC-5)
//
// [TaxAPI.IncidenceDisplay] and [TaxAPI.Revenue] are both recomputed from
// the instrument's current rate and current base at call time — never from a
// cached or once-computed split. Incidence shares are interpolated between
// the instrument's data-loaded bearer-weight rate points and renormalised so
// they sum to 1.0.
package tax
