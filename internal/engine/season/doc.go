// Package season is the seasonality module (MOD-027): month-index-driven
// curve lookups for every seasonal system §9 names, exposed as a pure
// SeasonAPI loaded from data/seasonal.json.
//
// Module key: engine.season (see code.json)
// Spec ref:   §9 (seasonality: "month index drives: power demand
// (winter peak), water stress (summer), harvest calendar..., construction
// speed (winter slowdown), school year (September intake gates)...,
// leisure mix..., minor health wave (winter). All seasonal curves
// visible in projections."); §17 (Resource Consumption Model modifiers —
// electricity "winter +15%", gas "x2.2 Jan, x0.2 Jul", water "+25%
// summer peak", all "before seasonal modifiers (§9)").
//
// # The eight curves
//
// Every curve below is a [SeasonAPI] query method, backed by a named
// entry in data/seasonal.json's "curves" map (never a Go literal —
// GR#15):
//
//  1. Power demand    — [SeasonAPI.PowerDemandMultiplier]: §17.1 winter
//     electricity peak ("electricityWinterPeak").
//  2. Water demand     — [SeasonAPI.WaterDemandMultiplier]: §17.1 summer
//     water-stress peak ("waterSummerPeak").
//  3. Gas demand       — [SeasonAPI.GasDemandMultiplier]: §17.1 gas
//     seasonal curve, x2.2 Jan / x0.2 Jul ("gasSeasonal").
//  4. Harvest calendar — [SeasonAPI.HarvestCalendar]: §9 lumped
//     staples-arrival timing ("harvestCalendar").
//  5. Construction     — [SeasonAPI.ConstructionSpeedMultiplier]: §9
//     winter build-queue slowdown ("constructionSpeedMultiplier").
//  6. School intake    — [SeasonAPI.IsSchoolIntakeMonth]: §9 September
//     intake gate ("schoolIntakeGate").
//  7. Leisure mix      — [SeasonAPI.LeisureMix]: §9/§41 beach/indoor
//     weighting pair ("leisureBeachWeight"/"leisureIndoorWeight").
//  8. Health wave      — [SeasonAPI.HealthWaveModifier]: §9/§18 minor
//     winter physical-health drift adjustment ("healthWaveModifier").
//
// # Pure functions of month index (AC-1, AC-11, AC-14)
//
// Every query method takes an absolute month index (int64, matching
// engine.core's Clock.Month(): 0 = world genesis, monotonically
// increasing) and returns the same value every time it is called with
// the same index. There is no "current month" state anywhere in this
// package — a caller can query 240 months into the future exactly as
// cheaply and exactly as validly as it can query month 0, which is what
// makes this package usable by engine.projections' anti-ambush
// requirement (§13, US-7) without any special "future" mode.
//
// # Month-index convention (AC-18)
//
// data/seasonal.json's own "meta" block is the authoritative statement:
// index 0 = January in every 12-entry curve array, and an absolute
// month index maps to its calendar month via monthIndex mod 12 (world
// genesis, month 0, is treated as January). See calendarMonth's doc
// comment (season.go) for the code-side half of this.
//
// # Loading and errors (GR#7, GR#15)
//
// [Load] reads data/seasonal.json via foundation/data.LoadSeasonal (schema
// validation: version present, every curve exactly 12 points, no
// negative multiplier) and additionally checks that all eight curves
// this package requires are present — a completeness check
// foundation.data's generic per-file schema cannot perform, since it has
// no notion of which curve names a particular consumer needs. Every
// failure returns a registry-sourced *errs.E (MET-E5xx, this package's
// claimed sub-range — see errors.go), never a silent default-to-1.0
// substitution and never a panic.
//
// # Shape invariants beyond generic schema (BUG-059)
//
// foundation/data.Seasonal.Validate checks every curve the same way
// (length, non-negativity) with no notion of what a particular curve
// name means. Where a curve's documented contract is a genuine
// structural invariant rather than a balance number, this package
// enforces it itself at Load time rather than leaving it as prose a
// hand-edit can silently violate (process weakness pattern #1 — an
// invariant stated only in a comment is not a control). The one curve
// that qualifies is schoolIntakeGate: it is consumed as a once-per-year
// boolean state-machine trigger (§9/US-4 — education's stage-transition
// gate), so Load rejects any curve without exactly one qualifying month
// (MET-E504). The other seven curves are continuous multipliers/weights
// a consumer reads and applies directly every month; their doc comments
// describe an intended *shape* ("winter +15% peak", "lumped, not
// smooth") as guidance for M2 Batch tuning, not a discrete pass/fail
// contract with a downstream boolean-trigger consumer, so Load does not
// shape-validate them — see ASM-231 (BOW) for the reasoning this
// distinction rests on.
//
// # Determinism (GR#21)
//
// This package never reads the wall clock (grep -rn "time\.Now\|time\.
// Since" internal/engine/season/*.go, excluding _test.go, returns no
// matches) and never ranges over a Go map in a way that affects an
// observable result — the one map here (SeasonAPI.curves) is only ever
// looked up by a fixed key, never iterated, and is populated once at
// Load time and never mutated afterward, so concurrent reads from
// multiple consumer modules/shards need no lock (AC-16).
//
// # What this package does NOT do
//
// It has no state of its own beyond the loaded curve data, and it does
// not decide how any consumer uses a curve value — the utility network's
// actual demand calculation, farming's harvest logistics, education's
// stage-transition mechanics, and the projections engine's rendering are
// each that module's own job (see the acceptance doc's Out of scope
// section).
package season
