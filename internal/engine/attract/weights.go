package attract

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Weights holds the ten-lever attractiveness weights and the three extra
// weights outside the ten (FEAT-9995385616):
//
//   - Levers: one weight per lever, indexed by LeverID. They sum to 1
//     (within weightSumEpsilon); today all ten are 0.1 (Aaron's ruling), but
//     equal is the data, not a code rule — a balance pass is a data edit.
//   - Modifiers: the safety and environment weights, each at full strength
//     (0.1 today), outside the ten and outside the sum-to-1 rule.
//   - Reputation: the weight of the signed momentum kick (0.15 today).
//   - Reinforcers: the weight of each named momentum reinforcer (sports,
//     welfare, ...). Inert today: no reinforcer is built.
//
// All are loaded from data (ParseWeights over data/attract_terms.json's
// "weights" block) — never literal constants in this package (GR#15, AC-2).
type Weights struct {
	Levers      [LeverCount]float64
	Modifiers   [ModifierCount]float64
	Reputation  float64
	Reinforcers [ReinforcerCount]float64
}

// weightSumEpsilon is the tolerance for the "ten lever weights sum to 1"
// check — a data authoring near-miss (0.9999999) is accepted, a real
// imbalance is not.
const weightSumEpsilon = 1e-6

// leverSum returns the ten lever weights' total.
func (w Weights) leverSum() float64 {
	var s float64
	for i := 0; i < LeverCount; i++ {
		s += w.Levers[i]
	}
	return s
}

// validate rejects any weight that is non-finite or outside [0,1], and a
// lever-weight set whose total departs from 1 by more than weightSumEpsilon
// — with a registry-sourced error and no partial application (AC-10).
func (w Weights) validate(correlationID string) error {
	bad := func(field string, v float64) error {
		return errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": field, "value": v})
	}
	ok := func(v float64) bool { return num.IsFinite(v) && v >= 0 && v <= 1 }
	for i := 0; i < LeverCount; i++ {
		if !ok(w.Levers[i]) {
			return bad("levers."+LeverID(i).String(), w.Levers[i])
		}
	}
	for i := 0; i < ModifierCount; i++ {
		if !ok(w.Modifiers[i]) {
			return bad("modifiers."+ModifierID(i).String(), w.Modifiers[i])
		}
	}
	if !ok(w.Reputation) {
		return bad("reputation", w.Reputation)
	}
	for i := 0; i < ReinforcerCount; i++ {
		if !ok(w.Reinforcers[i]) {
			return bad("reinforcers."+ReinforcerID(i).String(), w.Reinforcers[i])
		}
	}
	if s := w.leverSum(); !num.IsFinite(s) || s < 1-weightSumEpsilon || s > 1+weightSumEpsilon {
		return bad("sum", s)
	}
	return nil
}

// weightEntryJSON is one named weight in the data file. Weight is a pointer
// so an entry that omits "weight" is a rejected entry, not a silent zero.
type weightEntryJSON struct {
	ID     string   `json:"id"`
	Weight *float64 `json:"weight"`
}

// weightsJSON is the wire shape of data/attract_terms.json's "weights"
// block. Each group is a list of named entries (not an object) so a
// duplicated name is detectable: a JSON object with a repeated key would be
// silently last-wins. Reputation is a pointer so a missing value is
// distinguishable from a genuine zero.
type weightsJSON struct {
	Levers      []weightEntryJSON `json:"levers"`
	Modifiers   []weightEntryJSON `json:"modifiers"`
	Reputation  *float64          `json:"reputation"`
	Reinforcers []weightEntryJSON `json:"reinforcers"`
}

// fillGroup places each entry at its named index and rejects an unknown
// name, a duplicate, a missing weight, or a group with a missing member.
func fillGroup(group string, entries []weightEntryJSON, n int, find func(string) (int, bool), dst []float64, correlationID string) error {
	var seen [16]bool
	for _, e := range entries {
		idx, found := find(e.ID)
		if !found {
			return errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": group + "." + e.ID, "value": "unknown name"})
		}
		if seen[idx] {
			return errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": group + "." + e.ID, "value": "duplicate"})
		}
		if e.Weight == nil {
			return errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": group + "." + e.ID, "value": "missing weight"})
		}
		seen[idx] = true
		dst[idx] = *e.Weight
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			return errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": group, "value": "missing entry"})
		}
	}
	return nil
}

// toWeights converts the wire block into Weights and validates it.
func (raw weightsJSON) toWeights(correlationID string) (Weights, error) {
	var w Weights
	if err := fillGroup("levers", raw.Levers, LeverCount,
		func(s string) (int, bool) { id, ok := ParseLeverID(s); return int(id), ok },
		w.Levers[:], correlationID); err != nil {
		return Weights{}, err
	}
	if err := fillGroup("modifiers", raw.Modifiers, ModifierCount,
		func(s string) (int, bool) { id, ok := ParseModifierID(s); return int(id), ok },
		w.Modifiers[:], correlationID); err != nil {
		return Weights{}, err
	}
	if err := fillGroup("reinforcers", raw.Reinforcers, ReinforcerCount,
		func(s string) (int, bool) { id, ok := ParseReinforcerID(s); return int(id), ok },
		w.Reinforcers[:], correlationID); err != nil {
		return Weights{}, err
	}
	if raw.Reputation == nil {
		return Weights{}, errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": "reputation", "value": "missing"})
	}
	w.Reputation = *raw.Reputation
	if err := w.validate(correlationID); err != nil {
		return Weights{}, err
	}
	return w, nil
}

// ParseWeights decodes and validates the attract weights from the bytes of
// data/attract_terms.json (its "weights" block; every other key in the file
// is ignored here — compose owns them). Nine or eleven lever weights, a
// missing or duplicated name, a non-finite or out-of-[0,1] weight, or a
// lever sum off 1 is rejected with ErrInvalidWeights (a JSON syntax error
// with ErrConfigInvalid) — never a silent default.
func ParseWeights(data []byte, correlationID string) (Weights, error) {
	var doc struct {
		Weights *weightsJSON `json:"weights"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Weights{}, errs.Wrap(ErrConfigInvalid, correlationID, err, map[string]any{
			"field": "JSON",
			"value": err.Error(),
			"cause": err.Error(),
		})
	}
	if doc.Weights == nil {
		return Weights{}, errs.New(ErrInvalidWeights, correlationID, map[string]any{"field": "weights", "value": "missing block"})
	}
	return doc.Weights.toWeights(correlationID)
}
