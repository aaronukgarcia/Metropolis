package metricsdash

import (
	"errors"
	"strings"
	"testing"
)

// TestDashboard_Render_SourceFailureDoesNotBlockOtherSections proves
// AC-6/AC-11's "not a dashboard error/crash" requirement at the
// aggregation layer: one failed source still lets the rest of the
// report render, and the failure itself is visible rather than hidden.
func TestDashboard_Render_SourceFailureDoesNotBlockOtherSections(t *testing.T) {
	d := Dashboard{
		Sprint:      "3",
		WeaknessErr: errors.New("boom: weakness source unavailable"),
		Lint:        LintReport{}, // clean state
		GateStatus:  GateStatusReport{Sprint: "3", Checks: []GateCheck{{Number: 1, Name: "data-files", Verdict: "PASS"}}, Overall: "PASS"},
		Perf:        PerfReport{ResultsFileMissing: true, AcceptedRegistryMissing: true, Presets: []PerfPresetStatus{{Preset: "1M", Verdict: "no-history"}}},
	}

	out := d.Render()

	if !strings.Contains(out, "boom: weakness source unavailable") {
		t.Errorf("rendered report does not surface the weakness failure: %q", out)
	}
	if !strings.Contains(out, "No drift found") {
		t.Errorf("rendered report missing the clean lint section: %q", out)
	}
	if !strings.Contains(out, "data-files") || !strings.Contains(out, "PASS") {
		t.Errorf("rendered report missing gate-status detail: %q", out)
	}
	if !strings.Contains(out, "no history yet") {
		t.Errorf("rendered report missing perf no-history state: %q", out)
	}
}

func TestDashboard_Render_RecurringWeaknessIsFlagged(t *testing.T) {
	d := Dashboard{
		Sprint:   "3",
		Weakness: WeaknessReport{TotalFindings: 3, OpenFindings: 3, Classes: []WeaknessClass{{Name: "input-validation", Total: 3, Open: 3, Recurring: true}}},
	}
	out := d.Render()
	if !strings.Contains(out, "RECURRING") {
		t.Errorf("rendered report does not flag a recurring weakness class: %q", out)
	}
}
