package attract

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// The ten-lever migration formula (FEAT-9995385616, Aaron's rulings of
// 2026-11-18). Attractiveness is one term per lever, each lever on a [0,100]
// scale and each carrying the weight data/attract_terms.json gives it (0.1
// today, ten equal weights). A lever that is not built contributes EXACTLY
// neutral (A_world) and reports itself as not built; its value is ignored.
//
//	score_pre = A_world
//	          + sum over the ten levers  w_i * (value_i - A_world)   [built only]
//	          + w_safety * (safety - A_world)                         [modifier]
//	          + w_environment * (environment - A_world)               [modifier]
//	          + w_reputation * reputation                             [momentum]
//	score     = score_pre * wellbeingFactor
//	net       = migrationRate * (score - A_world)
//
// Identity (the panel prints every term, they must add up):
//
//	sum(lever points) + sum(modifier points) + reputation points
//	  + wellbeing points = net / migrationRate
//
// where wellbeing points = score - score_pre. Units: lever values and
// points are "attractiveness points" on the 0..100 scale.

// LeverID names one of the ten levers. The numeric value is the index into
// every [LeverCount] array in this package and the position on the panel;
// the order is fixed here and nowhere else (GR#21: no map is ever ranged).
type LeverID uint8

// The ten levers, in the fixed panel order.
const (
	LeverHousing LeverID = iota
	LeverJobs
	LeverServices
	LeverHealthCare
	LeverPolicy
	LeverShops
	LeverParks
	LeverTransport
	LeverCongestion
	LeverTourism

	// LeverCount is the number of levers. Not a LeverID value.
	LeverCount = int(iota)
)

// leverNames are the stable data/wire names, indexed by LeverID.
var leverNames = [LeverCount]string{
	"housing", "jobs", "services", "healthcare", "policy",
	"shops", "parks", "transport", "congestion", "tourism",
}

// String returns the lever's stable name ("" for an out-of-range id).
func (id LeverID) String() string {
	if int(id) >= LeverCount {
		return ""
	}
	return leverNames[id]
}

// ParseLeverID resolves a stable name to its LeverID by a linear scan of the
// fixed table (no map).
func ParseLeverID(name string) (LeverID, bool) {
	for i := 0; i < LeverCount; i++ {
		if leverNames[i] == name {
			return LeverID(i), true
		}
	}
	return 0, false
}

// ModifierID names one of the two extra modifiers that stay outside the ten
// levers at full strength (Aaron, 2026-11-18, ruling 1): safety and
// environment. Each has its own weight and its own line on the panel.
type ModifierID uint8

// The two modifiers, in fixed order.
const (
	ModifierSafety ModifierID = iota
	ModifierEnvironment

	// ModifierCount is the number of modifiers. Not a ModifierID value.
	ModifierCount = int(iota)
)

var modifierNames = [ModifierCount]string{"safety", "environment"}

// String returns the modifier's stable name.
func (id ModifierID) String() string {
	if int(id) >= ModifierCount {
		return ""
	}
	return modifierNames[id]
}

// ParseModifierID resolves a stable name to its ModifierID.
func ParseModifierID(name string) (ModifierID, bool) {
	for i := 0; i < ModifierCount; i++ {
		if modifierNames[i] == name {
			return ModifierID(i), true
		}
	}
	return 0, false
}

// ReinforcerID names one named input slot of the reputation momentum (Aaron,
// 2026-11-18, ruling 2: "momentum, pride, have big inertia; things which
// reinforce this are sports, welfare, education, prospects, well paying
// jobs, hospitals, parks"). None is built yet: every slot reads not built
// and is exactly neutral. A later lever fills its slot by pushing a built
// LeverTerm; attract needs no change.
type ReinforcerID uint8

// The seven reputation reinforcers, in fixed order.
const (
	ReinforcerSports ReinforcerID = iota
	ReinforcerWelfare
	ReinforcerEducation
	ReinforcerProspects
	ReinforcerWellPaidJobs
	ReinforcerHospitals
	ReinforcerParks

	// ReinforcerCount is the number of reinforcers. Not a ReinforcerID value.
	ReinforcerCount = int(iota)
)

var reinforcerNames = [ReinforcerCount]string{
	"sports", "welfare", "education", "prospects", "wellpaidjobs", "hospitals", "parks",
}

// String returns the reinforcer's stable name.
func (id ReinforcerID) String() string {
	if int(id) >= ReinforcerCount {
		return ""
	}
	return reinforcerNames[id]
}

// ParseReinforcerID resolves a stable name to its ReinforcerID.
func ParseReinforcerID(name string) (ReinforcerID, bool) {
	for i := 0; i < ReinforcerCount; i++ {
		if reinforcerNames[i] == name {
			return ReinforcerID(i), true
		}
	}
	return 0, false
}

// LeverTerm is one lever's pushed input: a 0..100 Value and whether the
// lever is Built. When Built is false the Value is ignored (it is still
// validated) and the lever contributes exactly neutral.
type LeverTerm struct {
	Value float64
	Built bool
}

// LeverResult is one lever's (or modifier's) outcome for a month. Value is
// the lever's own 0..100 reading, reported as 0 when not built. Points is
// weight * (Value - A_world) when built and exactly 0 when not.
type LeverResult struct {
	Built  bool
	Value  float64
	Points float64
}

// What bounded a month's arrivals (MigrationResult.LimitedBy).
const (
	// LimitHomes: free homes (vacancy x household size) was the cap that
	// bound, or tied with transport.
	LimitHomes = "homes"
	// LimitTransport: junction throughput was strictly the tighter cap.
	LimitTransport = "transport"
)

// validateLeverTerm rejects a non-finite or out-of-[0,100] value, built or
// not (FEAT-086: every numeric input validated at every entry point).
func validateLeverTerm(group, name string, t LeverTerm, correlationID string) error {
	if !num.IsFinite(t.Value) || t.Value < 0 || t.Value > 100 {
		return errs.New(ErrInvalidTermInput, correlationID, map[string]any{
			"field": group + "." + name,
			"value": t.Value,
		})
	}
	return nil
}

// breakdown is the full term-by-term decomposition of one month's score.
type breakdown struct {
	aWorld          float64
	levers          [LeverCount]LeverResult
	modifiers       [ModifierCount]LeverResult
	fundamentals    float64 // the momentum's input: mean of the ten effective values + reinforcement
	reputation      float64
	reputationPts   float64
	wellbeingFactor float64
	wellbeingPts    float64
	score           float64
}

// effective returns the lever's value for averaging: its value when built,
// A_world when not (neutral).
func effective(r LeverResult, aWorld float64) float64 {
	if r.Built {
		return r.Value
	}
	return aWorld
}

// leverResults folds the ten lever inputs and the two modifier inputs into
// result lines and returns the momentum input (fundamentals). housing is
// the computed housing value (used when the housing gate is built).
func leverResults(w Weights, in TermInputs, housing, aWorld float64) (levers [LeverCount]LeverResult, mods [ModifierCount]LeverResult, fundamentals float64) {
	var sum float64
	for i := 0; i < LeverCount; i++ {
		t := in.Levers[i]
		if LeverID(i) == LeverHousing {
			t.Value = housing // attract computes housing itself; only the gate is the caller's
		}
		r := LeverResult{}
		if t.Built {
			r = LeverResult{Built: true, Value: t.Value, Points: w.Levers[i] * (t.Value - aWorld)}
		}
		levers[i] = r
		sum += effective(r, aWorld)
	}
	fundamentals = sum / float64(LeverCount)
	for i := 0; i < ModifierCount; i++ {
		t := in.Modifiers[i]
		if t.Built {
			mods[i] = LeverResult{Built: true, Value: t.Value, Points: w.Modifiers[i] * (t.Value - aWorld)}
		}
	}
	// Reinforcement of the momentum: neutral today (no slot is built).
	for i := 0; i < ReinforcerCount; i++ {
		t := in.Reinforcers[i]
		if t.Built {
			fundamentals += w.Reinforcers[i] * (t.Value - aWorld)
		}
	}
	return levers, mods, fundamentals
}

// composeBreakdown builds the score lines from the lever results, the
// reputation value and the wellbeing factor.
func composeBreakdown(w Weights, levers [LeverCount]LeverResult, mods [ModifierCount]LeverResult,
	fundamentals, reputation, factor, aWorld float64) breakdown {
	pre := aWorld
	for i := 0; i < LeverCount; i++ {
		pre += levers[i].Points
	}
	for i := 0; i < ModifierCount; i++ {
		pre += mods[i].Points
	}
	repPts := w.Reputation * reputation
	pre += repPts
	score := pre * factor
	return breakdown{
		aWorld:          aWorld,
		levers:          levers,
		modifiers:       mods,
		fundamentals:    fundamentals,
		reputation:      reputation,
		reputationPts:   repPts,
		wellbeingFactor: factor,
		wellbeingPts:    score - pre,
		score:           score,
	}
}
