package citizens

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
)

// weatheremergency.go implements FEAT-087 (mkey feat.deathwave) inc2: the
// declared weather-emergency suspension of the death-queue smoothing
// budget (AC-6/AC-7/AC-8). It CONSUMES the registered
// feat.deathwave -> engine.season outbound edge (code.json) via
// *season.SeasonAPI — it never adds a mortality-local 12-entry seasonal
// calendar (GR#3) and never imports feat.disasters (ASM-579's tripwire:
// that edge is deliberately unregistered).
//
// # The mechanism (AC-6/AC-8 — suspension, not a second mortality model)
//
// [IsWeatherEmergency] is a PURE function of (SeasonAPI, monthIndex, the
// data-file thresholds) — no rand, no wall clock (GR#21) — that declares
// whether monthIndex is a weather emergency. [EmergencyRealise] is the
// caller-side wrapper deathwave.go's file doc anticipated: during a
// declared emergency it REPLACES DeathQueue.Realise's ordinary
// data-file budget with the emergency throughput (data/mortality.json's
// monthlyEmergencyBudget; 0 is the documented "unbounded" sentinel,
// releasing the queue's ENTIRE contents that month) — a major,
// non-smoothed death event. Outside an emergency, the ordinary budget is
// unchanged.
//
// Neither function ever touches [ColdShard.applyMonthly]'s hazard draw or
// [DeathQueue.Enqueue] — the emergency is consulted ONLY at realisation
// time, in AdvanceDayTick's once-per-completed-month release step
// (registry.go). This is what makes AC-8 structurally true rather than a
// documentation promise: a fixed set of hazard SELECTIONS realises
// differently (more of them, sooner) under an emergency, but the
// selections themselves — which citizens the Gompertz-Makeham hazard
// picked — are computed entirely independently of this file and of the
// season package. Any weather-driven ELEVATION of the underlying hazard
// is engine.season's HealthWaveModifier consumed separately by
// mortality.go's hazard path (already the case before this file existed);
// this file never multiplies the hazard a second time.

// IsWeatherEmergency declares AC-6/AC-7's weather emergency for monthIndex
// (engine.core's Clock.Month() convention — matching CitizensAPI.month and
// SeasonAPI's own month-index convention, so no re-mapping is needed
// between the two). Per ASM-579, the flag is derived LOCALLY by comparing
// two of engine.season's existing curves against data/mortality.json's
// thresholds (GR#15 — the thresholds are data, never a Go literal):
//
//   - winter-shaped: |SeasonAPI.HealthWaveModifier(monthIndex)| >=
//     cfg.WinterHealthWaveThreshold()
//   - drought-shaped: SeasonAPI.WaterDemandMultiplier(monthIndex) >=
//     cfg.DroughtWaterDemandThreshold()
//
// Either condition alone declares the month a weather emergency (an "or",
// not an "and" — a genuine adverse-weather month is major regardless of
// which curve signals it). seasonAPI must be non-nil; a nil SeasonAPI
// (the composition root has not wired engine.season for this CitizensAPI)
// returns (false, nil) — never an emergency, never a panic — mirroring
// engine.build/engine.cafe/engine.education's existing nil-season no-op
// convention for an optional injected dependency.
func IsWeatherEmergency(seasonAPI *season.SeasonAPI, monthIndex int64, cfg MortalityConfig, correlationID string) (bool, error) {
	if seasonAPI == nil {
		return false, nil
	}

	healthWave, err := seasonAPI.HealthWaveModifier(monthIndex)
	if err != nil {
		return false, err
	}
	if math.Abs(healthWave) >= cfg.WinterHealthWaveThreshold() {
		return true, nil
	}

	waterDemand, err := seasonAPI.WaterDemandMultiplier(monthIndex)
	if err != nil {
		return false, err
	}
	if waterDemand >= cfg.DroughtWaterDemandThreshold() {
		return true, nil
	}

	return false, nil
}

// EmergencyRealise is AC-6's suspension mechanism: the caller-side wrapper
// over [DeathQueue.Realise] deathwave.go's file doc named as inc2's shape.
// When emergency is false, the ordinary data-file budget
// (cfg.MonthlyDeathBudget()) applies unchanged — smoothing behaves exactly
// as inc1/inc1.5 built it. When emergency is true, the budget is SUSPENDED
// and replaced by cfg.MonthlyEmergencyBudget(): a positive value is the
// documented emergency throughput for that month; the 0 sentinel means
// "unbounded" — release the queue's entire current length (q.Len), the
// major non-smoothed death event AC-6 requires. This function makes no
// hazard draw and calls no Enqueue — it only decides how much of the FIFO
// head Realise releases (AC-8's boundary).
//
// The budget rule itself lives in [budgetFor] (deathwave.go, BUG-483 F1) —
// this function and [DeathQueue.RealiseDrained] both call that ONE helper
// rather than each keeping their own copy of the same three lines, so the
// two functions' documented byte-identical-budget behaviour (see
// RealiseDrained's doc, and attack_feat087_inc3_handoff_test.go's
// differential regression) is guaranteed by sharing code, not merely by a
// test proving two independent implementations happen to agree today.
func EmergencyRealise(q *DeathQueue, cfg MortalityConfig, emergency bool, month int64, correlationID string) []uint64 {
	// SEC-020 copy-guard (astgate): q.Realise below already rejects a
	// struct-copy call and returns nil, but this function itself takes the
	// candidate *DeathQueue as a parameter, so it checks first too (belt
	// and suspenders, matching every other DeathQueue-consuming call site
	// in this package) rather than relying solely on the delegated check.
	if err := q.checkNotCopied(correlationID, "EmergencyRealise"); err != nil {
		return nil
	}
	budget := budgetFor(q, cfg, emergency, correlationID)
	return q.Realise(budget, month, correlationID)
}
