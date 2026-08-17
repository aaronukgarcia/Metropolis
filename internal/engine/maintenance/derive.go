package maintenance

import "github.com/aaronukgarcia/Metropolis/internal/foundation/num"

// monthsPerYear is the fixed simulation-time conversion between the module's
// month-index clock and the data file's per-year rates/lifetimes. It is a
// time-step convention, not a balance value (the same class as
// engine.build's daysPerTick and engine.finance's MicropoundsPerPound scale).
const monthsPerYear = int64(12)

// sizePerMilleUnit is the fixed-point denominator for RegisterOptions.SizePerMille:
// 1000 == 1.0×. A structural scale constant, not a balance value.
const sizePerMilleUnit = int64(1000)

// efficiency returns the instance's efficiency at the given age, a
// non-increasing function of age: 1.0 at age zero, declining linearly to 0
// at the class lifetime and holding at 0 beyond it. The linear SHAPE is a
// documented placeholder (balance-number regime); monotonicity is the tested
// invariant, never the slope (AC-3).
func efficiency(ageMonths, lifetimeMonths int64) float64 {
	if lifetimeMonths <= 0 {
		return 0
	}
	if ageMonths >= lifetimeMonths {
		return 0
	}
	return 1.0 - float64(ageMonths)/float64(lifetimeMonths)
}

// repairDemandPerYear returns the age-scaled engineer-days/year demand for a
// base rate: base × (1 + min(age, lifetime)/lifetime), so the figure is
// monotonic non-decreasing with age, doubling from base at age zero to
// 2×base at the lifetime and holding beyond it (AC-3: new needs less repair
// than old). Integer arithmetic throughout, via num.SafeMul/SatAdd — never a
// float rounding a backlog ledger in the crew's favour (AC-6).
func repairDemandPerYear(base, ageMonths, lifetimeMonths int64) int64 {
	if lifetimeMonths <= 0 {
		return base
	}
	age := ageMonths
	if age > lifetimeMonths {
		age = lifetimeMonths
	}
	// base × (lifetime + age) / lifetime ≡ base + base×age/lifetime. Split into
	// two terms so the (lifetime + age) addition can never overflow int64
	// (SEC-154): base×age is a num.SafeMul (saturating), the term divide is
	// exact integer arithmetic, and the final add is num.SatAdd. Monotonic
	// non-decreasing and non-negative even at absurd lifetime values.
	scaled, _ := num.SafeMul(base, age)
	return num.SatAdd(base, scaled/lifetimeMonths)
}

// effectiveSizePerMille maps a registered size factor to its effective
// per-mille scale: 0 is the documented 1.0× default. A negative factor is
// rejected at the Register boundary (SEC-155), so the `<= 0` guard here is
// the defensive remainder that keeps the pure helper from ever emitting a
// negative base. Centralising the 0→1.0× mapping in one place keeps Register's
// saturation check (SEC-163) and baseEngineerDaysPerYear from drifting (GR#3).
func effectiveSizePerMille(sizePerMille int64) int64 {
	if sizePerMille <= 0 {
		return sizePerMilleUnit
	}
	return sizePerMille
}

// baseEngineerDaysPerYear applies the per-instance size factor to the class
// rate, in integer fixed-point arithmetic: rate × sizePerMille / 1000. A zero
// size factor is the documented 1.0× default. The Register boundary rejects
// both a negative factor (SEC-155) and a positive factor whose rate×size
// product would overflow int64 (SEC-163), so the SafeMul here cannot silently
// saturate a registerable instance's base — the `<= 0` guard is the defensive
// remainder that keeps the pure helper from ever emitting a negative base.
func baseEngineerDaysPerYear(rate, sizePerMille int64) int64 {
	prod, _ := num.SafeMul(rate, effectiveSizePerMille(sizePerMille))
	return prod / sizePerMilleUnit
}
