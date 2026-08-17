package census

// Finding is the regulator/watchdog's queryable finding (AC-7): a condition
// name, the scope it applies to, the offending value, and the data-defined
// threshold. A finding is a report — the regulator never mutates any
// module's state to "fix" the condition (AC-2, AC-7).
type Finding struct {
	Condition string
	Scope     string
	Value     float64
	Threshold float64
}

// Regulator condition names (AC-7).
const (
	FindingCrimeTooHigh = "crime-too-high"
	FindingUnfed        = "unfed"
	FindingUneducated   = "uneducated"
)

// regulate runs the regulator/watchdog thread: it evaluates the current
// aggregates against the data-defined thresholds and emits queryable
// findings, never interventions (US-5). A condition fires strictly greater
// than its threshold — the documented inclusive/exclusive rule — so a value
// at exactly the threshold does not fire (AC-7).
func (c *CensusAPI) regulate(agg Aggregates) []Finding {
	var out []Finding
	if agg.CrimeRate > c.cfg.Thresholds.CrimeRate.Value {
		out = append(out, Finding{
			Condition: FindingCrimeTooHigh,
			Scope:     "city",
			Value:     agg.CrimeRate,
			Threshold: c.cfg.Thresholds.CrimeRate.Value,
		})
	}
	if agg.UnfedFraction > c.cfg.Thresholds.UnfedFraction.Value {
		out = append(out, Finding{
			Condition: FindingUnfed,
			Scope:     "city",
			Value:     agg.UnfedFraction,
			Threshold: c.cfg.Thresholds.UnfedFraction.Value,
		})
	}
	if agg.UneducatedFraction > c.cfg.Thresholds.UneducatedFraction.Value {
		out = append(out, Finding{
			Condition: FindingUneducated,
			Scope:     "city",
			Value:     agg.UneducatedFraction,
			Threshold: c.cfg.Thresholds.UneducatedFraction.Value,
		})
	}
	return out
}

// Findings returns the regulator's most recent findings (the regulator's
// query surface, AC-1d). Findings are reports, never interventions.
func (c *CensusAPI) Findings() []Finding {
	if err := c.checkNotCopied("Findings"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Finding(nil), c.findings...)
}
