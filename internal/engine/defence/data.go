package defence

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §55 leaves unquantified lives here, never as a Go literal. The
// code.json inbound pattern text names "external_world.json era script" as
// the mandate source; that file is §21's external-commuting dataset
// (FEAT-047) and carries no mandate events, so this module's mandate/grants
// data lives in its own defence.json (see doc.go).
const fileName = "defence.json"

// Config is engine.defence's runtime balance configuration: the competitive
// grant pots, the low-tax-capacity formula-support line, the
// population-threshold mandates with their choices and compensation, the
// per-facility payroll/personnel/family/procurement figures, and the refusal
// reputation penalty. Every field is data-sourced from data/defence.json
// (GR#15): values are placeholders pending the M2 balance pass, so
// rebalancing is a data edit, never a code change.
type Config struct {
	Version        int                       `json:"version"`
	GrantPots      []GrantPotConfig          `json:"grantPots"`
	FormulaSupport FormulaSupportConfig      `json:"formulaSupport"`
	Mandates       []MandateConfig           `json:"mandates"`
	Facilities     map[string]FacilityConfig `json:"facilities"`
	Reputation     ReputationConfig          `json:"reputation"`
	// MoneyConvention is the file's units-documentation prose (micropound
	// convention). Declared explicitly — never consumed — because the
	// BUG-281 r2 strict loader rejects undeclared fields and only strips
	// $-prefixed top-level keys.
	MoneyConvention string `json:"moneyConvention,omitempty"`
}

// GrantPotConfig is one competitive grant pot (AC-2): the win-probability
// anchors. Win probability = base + matchFundingWeight × (matchFunding /
// maxMatch) + planningQualityWeight × planningQuality, clamped to [0,1], so
// it rises with match funding and planning quality independently.
type GrantPotConfig struct {
	ID                    string  `json:"id"`
	Name                  string  `json:"name"`
	BaseWinProbability    float64 `json:"baseWinProbability"`
	MatchFundingWeight    float64 `json:"matchFundingWeight"`
	PlanningQualityWeight float64 `json:"planningQualityWeight"`
	MaxMatchMicropounds   int64   `json:"maxMatchMicropounds"`
	AwardMicropounds      int64   `json:"awardMicropounds"`
}

// FormulaSupportConfig is the low-tax-capacity formula-support line (AC-3):
// unconditional, non-competitive funding available below a tax-capacity
// threshold, distinct from AC-2's competitive pots.
type FormulaSupportConfig struct {
	TaxCapacityThresholdMicropounds int64 `json:"taxCapacityThresholdMicropounds"`
	FormulaAmountMicropounds        int64 `json:"formulaAmountMicropounds"`
}

// MandateConfig is one population-threshold mandate (AC-4): the threshold it
// fires at, the default facility type, the compensation grant for
// compliance, and the ≥2 compliant choices (AC-5).
type MandateConfig struct {
	ID                      string                `json:"id"`
	PopulationThreshold     int64                 `json:"populationThreshold"`
	FacilityType            string                `json:"facilityType"`
	CompensationMicropounds int64                 `json:"compensationMicropounds"`
	Choices                 []MandateChoiceConfig `json:"choices"`
}

// MandateChoiceConfig is one compliant choice within a mandate (AC-5).
type MandateChoiceConfig struct {
	ID           string `json:"id"`
	FacilityType string `json:"facilityType"`
	Description  string `json:"description"`
}

// FacilityConfig is one facility type's integration figures (AC-7/AC-8/
// AC-9): the §34 zone it sits on, the nominal payroll and its anti-cyclical
// floor, the personnel/family counts, and the procurement contract value.
type FacilityConfig struct {
	BuildZone               string `json:"buildZone"`
	PayrollMicropounds      int64  `json:"payrollMicropounds"`
	PayrollFloorMicropounds int64  `json:"payrollFloorMicropounds"`
	PersonnelCount          int64  `json:"personnelCount"`
	MarriedQuarters         int64  `json:"marriedQuarters"`
	ChildrenPerQuarter      int64  `json:"childrenPerQuarter"`
	ProcurementMicropounds  int64  `json:"procurementMicropounds"`
}

// ReputationConfig is the refusal cost (AC-6): the reputation penalty points
// applied when a mandate is refused.
type ReputationConfig struct {
	RefusalPenaltyPoints int64 `json:"refusalPenaltyPoints"`
}

// Validate implements the foundation/data.Validator contract so the generic
// data.Load runs schema validation immediately after JSON decoding. Every
// failure is a *data.FieldError naming the offending field and rule.
func (c *Config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(c.GrantPots) == 0 {
		return &data.FieldError{Field: "grantPots", Rule: "must contain at least one competitive pot"}
	}
	for i, p := range c.GrantPots {
		prefix := "grantPots"
		if p.ID == "" {
			return &data.FieldError{Field: fieldIdx(prefix, i, "id"), Rule: "required, must be non-empty"}
		}
		if p.Name == "" {
			return &data.FieldError{Field: fieldIdx(prefix, i, "name"), Rule: "required, must be non-empty"}
		}
		if !num.IsFinite(p.BaseWinProbability) || p.BaseWinProbability < 0 || p.BaseWinProbability > 1 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "baseWinProbability"), Rule: "must be finite and in [0,1]"}
		}
		if !num.IsFinite(p.MatchFundingWeight) || p.MatchFundingWeight < 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "matchFundingWeight"), Rule: "must be finite and non-negative"}
		}
		if !num.IsFinite(p.PlanningQualityWeight) || p.PlanningQualityWeight < 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "planningQualityWeight"), Rule: "must be finite and non-negative"}
		}
		if p.MaxMatchMicropounds <= 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "maxMatchMicropounds"), Rule: "must be positive"}
		}
		if p.AwardMicropounds <= 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "awardMicropounds"), Rule: "must be positive"}
		}
	}

	if c.FormulaSupport.TaxCapacityThresholdMicropounds <= 0 {
		return &data.FieldError{Field: "formulaSupport.taxCapacityThresholdMicropounds", Rule: "must be positive"}
	}
	if c.FormulaSupport.FormulaAmountMicropounds <= 0 {
		return &data.FieldError{Field: "formulaSupport.formulaAmountMicropounds", Rule: "must be positive"}
	}

	if len(c.Mandates) == 0 {
		return &data.FieldError{Field: "mandates", Rule: "must contain at least one mandate"}
	}
	for i, m := range c.Mandates {
		prefix := "mandates"
		if m.ID == "" {
			return &data.FieldError{Field: fieldIdx(prefix, i, "id"), Rule: "required, must be non-empty"}
		}
		if m.PopulationThreshold <= 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "populationThreshold"), Rule: "must be positive"}
		}
		if m.FacilityType == "" {
			return &data.FieldError{Field: fieldIdx(prefix, i, "facilityType"), Rule: "required, must be non-empty"}
		}
		if m.CompensationMicropounds < 0 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "compensationMicropounds"), Rule: "must be non-negative"}
		}
		if len(m.Choices) < 2 {
			return &data.FieldError{Field: fieldIdx(prefix, i, "choices"), Rule: "must offer at least two compliant choices (AC-5)"}
		}
		for j, ch := range m.Choices {
			if ch.ID == "" {
				return &data.FieldError{Field: fieldIdx(prefix, i, "choices["+itoa(j)+"].id"), Rule: "required, must be non-empty"}
			}
			if ch.FacilityType == "" {
				return &data.FieldError{Field: fieldIdx(prefix, i, "choices["+itoa(j)+"].facilityType"), Rule: "required, must be non-empty"}
			}
			if ch.Description == "" {
				return &data.FieldError{Field: fieldIdx(prefix, i, "choices["+itoa(j)+"].description"), Rule: "required, must be non-empty"}
			}
		}
	}

	// Every facility type a mandate or choice references must exist in the
	// facility table, so a build never reaches a missing facility config.
	for _, m := range c.Mandates {
		if _, ok := c.Facilities[m.FacilityType]; !ok {
			return &data.FieldError{Field: "facilities", Rule: "missing facility type " + m.FacilityType + " referenced by mandate " + m.ID}
		}
		for _, ch := range m.Choices {
			if _, ok := c.Facilities[ch.FacilityType]; !ok {
				return &data.FieldError{Field: "facilities", Rule: "missing facility type " + ch.FacilityType + " referenced by choice " + ch.ID}
			}
		}
	}
	for id, f := range c.Facilities {
		if f.BuildZone == "" {
			return &data.FieldError{Field: "facilities." + id + ".buildZone", Rule: "required, must be non-empty"}
		}
		if f.PayrollMicropounds <= 0 {
			return &data.FieldError{Field: "facilities." + id + ".payrollMicropounds", Rule: "must be positive"}
		}
		if f.PayrollFloorMicropounds < 0 || f.PayrollFloorMicropounds > f.PayrollMicropounds {
			return &data.FieldError{Field: "facilities." + id + ".payrollFloorMicropounds", Rule: "must be in [0, payrollMicropounds]"}
		}
		if f.PersonnelCount <= 0 {
			return &data.FieldError{Field: "facilities." + id + ".personnelCount", Rule: "must be positive"}
		}
		if f.MarriedQuarters < 0 {
			return &data.FieldError{Field: "facilities." + id + ".marriedQuarters", Rule: "must be non-negative"}
		}
		// Married quarters pair two personnel, so the quarter count cannot
		// exceed half the personnel count (2 × marriedQuarters ≤ personnel).
		if f.MarriedQuarters > f.PersonnelCount/2 {
			return &data.FieldError{Field: "facilities." + id + ".marriedQuarters", Rule: "must be <= personnelCount/2 (a quarter is a pair)"}
		}
		if f.ChildrenPerQuarter < 0 {
			return &data.FieldError{Field: "facilities." + id + ".childrenPerQuarter", Rule: "must be non-negative"}
		}
		if f.ProcurementMicropounds < 0 {
			return &data.FieldError{Field: "facilities." + id + ".procurementMicropounds", Rule: "must be non-negative"}
		}
	}

	if c.Reputation.RefusalPenaltyPoints <= 0 {
		return &data.FieldError{Field: "reputation.refusalPenaltyPoints", Rule: "must be positive"}
	}
	return nil
}

// fieldIdx builds a data.FieldError field path for a slice element.
func fieldIdx(prefix string, i int, sub string) string {
	return prefix + "[" + itoa(i) + "]." + sub
}

// itoa renders a small non-negative int as a decimal string without pulling
// in strconv for this one call site (deterministic, no locale).
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

// Load reads and schema-validates data/defence.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready-to-wire
// *DefenceAPI with its balance Config populated. worldSeed keys the
// deterministic grant-draw hash stream (AC-13). correlationID is attached to
// every error this call (and the returned API's methods) construct (GR#1).
func Load(dir string, worldSeed uint64, correlationID string) (*DefenceAPI, error) {
	path := filepath.Join(dir, fileName)
	cfg, err := data.Load[Config, *Config](path, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrDefenceDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return New(cfg, worldSeed, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then Loads it — the convenience entry point for callers
// (boot wiring, tests) that don't already have a resolved data directory in
// hand.
func LoadDefault(worldSeed uint64, correlationID string) (*DefenceAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, worldSeed, correlationID)
}
