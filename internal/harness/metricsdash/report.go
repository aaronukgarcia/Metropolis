package metricsdash

import (
	"context"
	"fmt"
	"strings"
)

// Dashboard is the aggregated, one-look view US-1 asks for: the
// weakness histogram, sprint gate verdicts, BOW lint drift, and perf-CI
// trend, gathered without a human running four separate `claude-bow.js`
// commands by hand. Any one source failing does not prevent the other
// three from rendering (AC-6/AC-11) — a failed source's *Err field is
// set and Render shows it as a visible failure for that section only.
type Dashboard struct {
	Sprint string

	Weakness    WeaknessReport
	WeaknessErr error

	Lint    LintReport
	LintErr error

	GateStatus    GateStatusReport
	GateStatusErr error

	Perf PerfReport
}

// BuildDashboard gathers every source (AC-1) for the given sprint and
// perf-CI file paths. repoRoot is the working directory `node
// claude-bow.js` is invoked from.
func BuildDashboard(ctx context.Context, repoRoot, sprint, perfResultsPath, perfAcceptedPath string) Dashboard {
	var d Dashboard
	d.Sprint = sprint

	if w, err := RunWeakness(ctx, repoRoot); err != nil {
		d.WeaknessErr = err
	} else {
		d.Weakness = w
	}

	if l, err := RunLint(ctx, repoRoot); err != nil {
		d.LintErr = err
	} else {
		d.Lint = l
	}

	if g, err := RunGateStatus(ctx, repoRoot, sprint); err != nil {
		d.GateStatusErr = err
	} else {
		d.GateStatus = g
	}

	d.Perf = RunPerf(perfResultsPath, perfAcceptedPath)

	return d
}

// Render formats the dashboard as plain text for the out-of-band CLI
// report (Escalation A — see doc.go). Every section renders
// independently: a failed source shows its error instead of blocking
// the rest of the report (AC-6/AC-11).
func (d Dashboard) Render() string {
	var b strings.Builder

	fmt.Fprintln(&b, "=== Metropolis backend metrics dashboard (feat.metricsdash) ===")
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "--- Weakness patterns (claude-bow.js weakness) ---")
	if d.WeaknessErr != nil {
		fmt.Fprintf(&b, "  UNAVAILABLE: %v\n", d.WeaknessErr)
	} else if len(d.Weakness.Classes) == 0 {
		fmt.Fprintln(&b, "  No security findings recorded yet.")
	} else {
		fmt.Fprintf(&b, "  %d finding(s), %d open\n", d.Weakness.TotalFindings, d.Weakness.OpenFindings)
		for _, c := range d.Weakness.Classes {
			tag := ""
			if c.Recurring {
				tag = "  [RECURRING]"
			}
			fmt.Fprintf(&b, "    %-30s %3d total  %3d open%s\n", c.Name, c.Total, c.Open, tag)
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "--- Sprint %s gate status (claude-bow.js gate-status) ---\n", d.Sprint)
	if d.GateStatusErr != nil {
		fmt.Fprintf(&b, "  UNAVAILABLE: %v\n", d.GateStatusErr)
	} else if d.GateStatus.NoVerdictsRecorded {
		fmt.Fprintln(&b, "  NO GATE VERDICTS RECORDED.")
	} else {
		for _, c := range d.GateStatus.Checks {
			manual := ""
			if c.ManualOverride {
				manual = "  [MANUAL-OVERRIDE]"
			}
			fmt.Fprintf(&b, "    check %d (%-16s): %s%s\n", c.Number, c.Name, c.Verdict, manual)
		}
		fmt.Fprintf(&b, "  Overall: %s\n", d.GateStatus.Overall)
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "--- BOW lint drift (claude-bow.js lint) ---")
	if d.LintErr != nil {
		fmt.Fprintf(&b, "  UNAVAILABLE: %v\n", d.LintErr)
	} else if len(d.Lint.Findings) == 0 {
		fmt.Fprintln(&b, "  No drift found.")
	} else {
		fmt.Fprintf(&b, "  %d finding(s):\n", d.Lint.Total)
		for _, f := range d.Lint.Findings {
			fmt.Fprintf(&b, "    [class %d] %s\n", f.Class, f.Text)
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "--- Perf-CI trend (H-SYNTH) ---")
	if d.Perf.ResultsFileMissing {
		fmt.Fprintln(&b, "  No perf-results file present yet (no data yet).")
	}
	if d.Perf.AcceptedRegistryMissing {
		fmt.Fprintln(&b, "  No accepted-regressions registry present yet (nothing accepted).")
	}
	for _, p := range d.Perf.Presets {
		if !p.HasHistory {
			fmt.Fprintf(&b, "    %-4s no history yet\n", p.Preset)
			continue
		}
		acceptedNote := ""
		if p.Accepted {
			acceptedNote = fmt.Sprintf("  (accepted: %s)", p.AcceptedReason)
		}
		fmt.Fprintf(&b, "    %-4s %-20s %v/month%s\n", p.Preset, p.Verdict, p.PerMonthTick, acceptedNote)
	}
	for _, w := range d.Perf.Warnings {
		fmt.Fprintf(&b, "  WARNING: %s\n", w)
	}

	return b.String()
}
