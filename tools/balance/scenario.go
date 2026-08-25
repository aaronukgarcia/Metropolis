package balance

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

// Known swept parameter dimensions. These names are the closed vocabulary a
// scenario may use (validated below); the VALUES the sweep runs over always
// come from the scenario JSON, never from Go literals (GR#15 / AC-6).
const (
	paramSecondsPerMonthAt1x = "secondsPerMonthAt1x"
	paramMonths              = "months"
	paramCitizenCount        = "citizenCount"
	paramSprawl              = "sprawl"
	paramNetworkShape        = "networkShape"
	paramMilestoneSpacing    = "milestoneSpacing"
	paramGrowthCurve         = "growthCurve"
)

// requiredParams are the dimensions a scenario must sweep (AC-1 / MOD-036's
// own description, plus the world-shape dimensions every headless fan-out
// needs so the grid is never defaulted in Go). GR#15: the requirement is on
// the NAME, never on a baked-in value.
var requiredParams = []string{
	paramSecondsPerMonthAt1x,
	paramMonths,
	paramCitizenCount,
	paramSprawl,
	paramNetworkShape,
}

// Target is the metric target band a sweep's proposal is judged against
// (ICD §2 "metric targets"; AC-5's 80-150 band lives in the scenario data).
type Target struct {
	Metric string     `json:"metric"`
	Band   [2]float64 `json:"band"`
}

// Scenario is the JSON scenario-definition schema (ICD §3 Inputs; AC-1):
// the swept parameter grid (name → list of values), the seed set, and the
// metric target. It is read entirely from a file — see LoadScenario.
type Scenario struct {
	Name       string                       `json:"name"`
	Parameters map[string][]json.RawMessage `json:"parameters"`
	Seeds      []uint64                     `json:"seeds"`
	Target     Target                       `json:"target"`
	Milestone  int64                        `json:"milestone,omitempty"`
	Retries    int                          `json:"retries,omitempty"`
	TimeoutMS  int64                        `json:"timeoutMillis,omitempty"`

	// raw is the exact file content, retained for the provenance hash.
	raw []byte
}

// LoadScenario reads path as a JSON scenario definition, validates it, and
// returns the parsed Scenario. Every read/parse/schema failure is reported
// as a registry-sourced error (AC-7), never a panic and never a silently
// empty grid.
func LoadScenario(path string) (*Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapErr(codeScenarioReadFailed, err, map[string]any{"path": path})
	}
	var scn Scenario
	if err := json.Unmarshal(raw, &scn); err != nil {
		return nil, wrapErr(codeScenarioReadFailed, err, map[string]any{"path": path, "cause": "scenario file must be a JSON object"})
	}
	scn.raw = raw
	if err := scn.Validate(); err != nil {
		return nil, err
	}
	return &scn, nil
}

// Validate checks the scenario's SHAPE at load time (AC-7): required
// dimensions present and non-empty, seeds non-empty, target well-formed, and
// every value the right JSON type for its dimension. It deliberately does
// NOT check value domains (a negative secondsPerMonthAt1x, an out-of-range
// citizen count) — those are per-cell rejections (AC-8), resolved during the
// sweep, not a load-time failure.
func (s *Scenario) Validate() error {
	if s.Parameters == nil {
		return newErr(codeScenarioInvalid, map[string]any{"field": "parameters", "reason": "missing"})
	}
	for _, name := range requiredParams {
		vals, ok := s.Parameters[name]
		if !ok {
			return newErr(codeScenarioInvalid, map[string]any{"field": name, "reason": "required parameter dimension missing"})
		}
		if len(vals) == 0 {
			return newErr(codeScenarioInvalid, map[string]any{"field": name, "reason": "parameter range must not be empty"})
		}
	}
	for name, vals := range s.Parameters {
		for i, raw := range vals {
			if err := validateValueShape(name, raw); err != nil {
				return wrapErr(codeScenarioInvalid, err, map[string]any{"field": name, "index": i})
			}
		}
	}
	if len(s.Seeds) == 0 {
		return newErr(codeScenarioInvalid, map[string]any{"field": "seeds", "reason": "seed set must not be empty"})
	}
	if s.Target.Metric != metricRealHours {
		return newErr(codeScenarioInvalid, map[string]any{"field": "target.metric", "reason": "unsupported metric", "value": s.Target.Metric})
	}
	if !(s.Target.Band[0] > 0) || !(s.Target.Band[1] > s.Target.Band[0]) {
		return newErr(codeScenarioInvalid, map[string]any{"field": "target.band", "reason": "band must be [low, high] with 0 < low < high"})
	}
	if s.Retries < 0 {
		return newErr(codeScenarioInvalid, map[string]any{"field": "retries", "reason": "must be >= 0"})
	}
	if s.TimeoutMS < 0 {
		return newErr(codeScenarioInvalid, map[string]any{"field": "timeoutMillis", "reason": "must be >= 0"})
	}
	return nil
}

// validateValueShape checks one raw parameter value against the JSON type
// (not the value domain) its dimension requires. The split between "shape"
// (here, load-time — AC-7) and "domain" (per-cell — AC-8) is what lets a
// negative secondsPerMonthAt1x survive to a rejected cell record instead of
// aborting the whole load.
func validateValueShape(name string, raw json.RawMessage) error {
	switch name {
	case paramSecondsPerMonthAt1x, paramSprawl, paramMilestoneSpacing:
		if !isJSONNumber(raw) {
			return &shapeError{name: name, want: "a JSON number"}
		}
	case paramMonths, paramCitizenCount:
		var i int64
		if err := json.Unmarshal(raw, &i); err != nil {
			return &shapeError{name: name, want: "a JSON integer"}
		}
	case paramNetworkShape:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return &shapeError{name: name, want: "a JSON string"}
		}
	case paramGrowthCurve:
		var m map[string]float64
		if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
			return &shapeError{name: name, want: "a non-empty JSON object of numeric coefficients"}
		}
	default:
		// Unknown dimensions are carried and swept (data-driven extensibility)
		// but not interpreted; any JSON value shape is acceptable for them.
	}
	return nil
}

// shapeError is the parse detail wrapErr carries into codeScenarioInvalid.
type shapeError struct {
	name string
	want string
}

func (e *shapeError) Error() string {
	return "parameter " + e.name + " value must be " + e.want
}

// isJSONNumber reports whether raw is a JSON number (any of int/float forms).
func isJSONNumber(raw json.RawMessage) bool {
	var f float64
	return json.Unmarshal(raw, &f) == nil
}

// specs expands the parameter grid into the cartesian product of parameter
// dimensions (names iterated in sorted order, values in declared order) as a
// list of raw assignments. Deterministic by construction (GR#21 / ICD §7):
// the same scenario always yields the same spec order.
func (s *Scenario) specs() []cellSpec {
	names := make([]string, 0, len(s.Parameters))
	for n := range s.Parameters {
		names = append(names, n)
	}
	sort.Strings(names)

	var specs []cellSpec
	indexes := make([]int, len(names))
	for {
		values := make(map[string]json.RawMessage, len(names))
		for i, n := range names {
			values[n] = s.Parameters[n][indexes[i]]
		}
		specs = append(specs, cellSpec{values: values})

		i := len(names) - 1
		for i >= 0 {
			indexes[i]++
			if indexes[i] < len(s.Parameters[names[i]]) {
				break
			}
			indexes[i] = 0
			i--
		}
		if i < 0 {
			break
		}
	}
	return specs
}

// cells returns the full (configuration, seed) sweep grid in deterministic
// order: configs in specs() order, then seeds in declared order.
func (s *Scenario) cells() ([]cell, error) {
	var out []cell
	for _, spec := range s.specs() {
		cfg, err := resolveConfig(spec.values)
		if err != nil {
			return nil, err
		}
		for _, seed := range s.Seeds {
			out = append(out, cell{Config: cfg, Seed: seed})
		}
	}
	return out, nil
}

// cellSpec is one raw parameter assignment before typed resolution.
type cellSpec struct {
	values map[string]json.RawMessage
}

// cell is one (configuration, seed) pair — the unit of one headless run.
type cell struct {
	Config CellConfig
	Seed   uint64
}

// CellConfig is one sweep point's typed parameter assignment. Config carries
// the canonical string rendering of every raw value for provenance/recording
// (so a rejected cell preserves its requested value verbatim — AC-8).
type CellConfig struct {
	SecondsPerMonthAt1x float64
	Months              int64
	CitizenCount        int64
	Sprawl              float64
	NetworkShape        string
	MilestoneSpacing    float64
	GrowthCurve         map[string]float64

	Config map[string]string
}

// resolveConfig interprets one raw assignment into a typed CellConfig,
// stringifying every value into Config along the way. Type errors here are
// defensive (Validate already rejected bad shapes); a genuine error still
// returns a registry-sourced codeScenarioInvalid rather than panicking.
func resolveConfig(values map[string]json.RawMessage) (CellConfig, error) {
	cfg := CellConfig{Config: make(map[string]string, len(values))}
	for name, raw := range values {
		cfg.Config[name] = stringifyRaw(raw)
	}

	var ok bool
	cfg.SecondsPerMonthAt1x, ok = decodeFloat(values[paramSecondsPerMonthAt1x])
	if !ok {
		return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramSecondsPerMonthAt1x})
	}
	cfg.Months, ok = decodeInt(values[paramMonths])
	if !ok {
		return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramMonths})
	}
	cfg.CitizenCount, ok = decodeInt(values[paramCitizenCount])
	if !ok {
		return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramCitizenCount})
	}
	cfg.Sprawl, ok = decodeFloat(values[paramSprawl])
	if !ok {
		return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramSprawl})
	}
	cfg.NetworkShape, ok = decodeString(values[paramNetworkShape])
	if !ok {
		return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramNetworkShape})
	}
	if raw, present := values[paramMilestoneSpacing]; present {
		cfg.MilestoneSpacing, ok = decodeFloat(raw)
		if !ok {
			return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramMilestoneSpacing})
		}
	}
	if raw, present := values[paramGrowthCurve]; present {
		cfg.GrowthCurve, ok = decodeObject(raw)
		if !ok {
			return CellConfig{}, newErr(codeScenarioInvalid, map[string]any{"field": paramGrowthCurve})
		}
	}
	return cfg, nil
}

// validateCellDomain checks one resolved config against the positive domains
// documented below (AC-8), returning a registry-sourced codeCellOutOfDomain
// for the first violation. It never clamps — the requested value is preserved
// in the rejected record by the caller.
func validateCellDomain(c CellConfig) error {
	if !(c.SecondsPerMonthAt1x > 0) || isNonFinite(c.SecondsPerMonthAt1x) {
		return newErr(codeCellOutOfDomain, map[string]any{"param": paramSecondsPerMonthAt1x, "value": c.SecondsPerMonthAt1x})
	}
	if c.Months <= 0 {
		return newErr(codeCellOutOfDomain, map[string]any{"param": paramMonths, "value": c.Months})
	}
	if c.CitizenCount < synth.MinSyntheticCitizens || c.CitizenCount > synth.MaxSyntheticCitizens {
		return newErr(codeCellOutOfDomain, map[string]any{
			"param": paramCitizenCount, "value": c.CitizenCount,
			"min": synth.MinSyntheticCitizens, "max": synth.MaxSyntheticCitizens,
		})
	}
	if c.Sprawl < synth.MinSprawl || c.Sprawl > synth.MaxSprawl || isNonFinite(c.Sprawl) {
		return newErr(codeCellOutOfDomain, map[string]any{"param": paramSprawl, "value": c.Sprawl})
	}
	if !isValidNetworkShape(c.NetworkShape) {
		return newErr(codeCellOutOfDomain, map[string]any{"param": paramNetworkShape, "value": c.NetworkShape})
	}
	if c.MilestoneSpacing < 0 {
		// <= 0 would place two milestones at the same (or a reversed)
		// population tier — rejected, not clamped (AC-8). A zero value (the
		// sentinel for "not swept") is allowed.
		return newErr(codeCellOutOfDomain, map[string]any{"param": paramMilestoneSpacing, "value": c.MilestoneSpacing})
	}
	for k, v := range c.GrowthCurve {
		if isNonFinite(v) {
			return newErr(codeCellOutOfDomain, map[string]any{"param": paramGrowthCurve, "coefficient": k, "value": v})
		}
	}
	return nil
}

func isNonFinite(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0)
}

func isValidNetworkShape(s string) bool {
	switch synth.NetworkShape(s) {
	case synth.NetworkGrid, synth.NetworkRadial, synth.NetworkOrganic:
		return true
	default:
		return false
	}
}

func decodeFloat(raw json.RawMessage) (float64, bool) {
	var f float64
	return f, json.Unmarshal(raw, &f) == nil
}

func decodeInt(raw json.RawMessage) (int64, bool) {
	var i int64
	return i, json.Unmarshal(raw, &i) == nil
}

func decodeString(raw json.RawMessage) (string, bool) {
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func decodeObject(raw json.RawMessage) (map[string]float64, bool) {
	var m map[string]float64
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil, false
	}
	return m, true
}

// stringifyRaw renders one raw JSON value to a canonical string for the
// Config map (recording + sort key). Numbers render as the shortest
// round-trip form, strings bare, objects as sorted-key JSON (encoding/json
// sorts map keys). Deterministic by construction.
func stringifyRaw(raw json.RawMessage) string {
	if i, err := decodeInt(raw); err {
		return strconv.FormatInt(i, 10)
	}
	if f, err := decodeFloat(raw); err {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	if s, err := decodeString(raw); err {
		return s
	}
	if m, err := decodeObject(raw); err {
		b, _ := json.Marshal(m)
		return string(b)
	}
	return string(raw)
}

// hash returns the SHA-256 hex digest of the scenario's exact file content —
// the provenance fingerprint (AC-12) that ties a result set to the scenario
// definition it was produced from.
func (s *Scenario) hash() string {
	return sha256Sum(s.raw)
}
