package metricsdash

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// LintFinding is one drift finding from `node claude-bow.js lint`'s
// output — the BOW code that owns the finding, the code it names
// (AC-4), which of the three drift classes it belongs to, and the
// finding's own free text for display.
type LintFinding struct {
	Class      int
	OwnerCode  string
	TargetCode string
	Text       string
}

// LintReport is the parsed form of cmdLint's stdout (AC-4).
type LintReport struct {
	Total    int
	Findings []LintFinding
}

// lintClassHeaderPattern matches cmdLint's own class-header lines,
// e.g. "Class 1 — prose names a gate, no bow_dependencies row (3):".
var lintClassHeaderPattern = regexp.MustCompile(`^Class (\d+) — .*\((\d+)\):$`)

// bowCodePattern matches a BOW-code-shaped token using the same prefix
// set claude-bow.js's own TYPE_PREFIX map declares (MOD/FEAT/BUG/INT/
// ASM/SEC) — every finding line cmdLint prints names the owning item's
// own code first and the cited/target code somewhere later in the same
// line (runLint, claude-bow.js).
var bowCodePattern = regexp.MustCompile(`\b(?:MOD|FEAT|BUG|INT|ASM|SEC)-\d+\b`)

// ParseLintText parses `node claude-bow.js lint`'s real stdout
// (cmdLint) into a LintReport. "No drift found" is a legitimate,
// empty (not erroneous) result.
func ParseLintText(text string) (LintReport, error) {
	if strings.Contains(text, "No drift found:") {
		return LintReport{}, nil
	}

	var findings []LintFinding
	currentClass := 0
	for _, line := range strings.Split(text, "\n") {
		if m := lintClassHeaderPattern.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				currentClass = n
			}
			continue
		}
		if currentClass == 0 {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			// A blank line or the trailing "N finding(s) total ..."
			// summary line both end the current class block.
			currentClass = 0
			continue
		}
		codes := bowCodePattern.FindAllString(line, -1)
		if len(codes) == 0 {
			continue
		}
		findings = append(findings, LintFinding{
			Class:      currentClass,
			OwnerCode:  codes[0],
			TargetCode: codes[len(codes)-1],
			Text:       strings.TrimSpace(line),
		})
	}

	if len(findings) == 0 {
		// Neither the "no drift" sentinel nor any parseable finding —
		// an unrecognised output shape (AC-1: never silently render an
		// empty report for a source that actually returned something
		// unreadable).
		return LintReport{}, errs.New(codeLintSourceUnavailable, errs.NewCorrelationID(), map[string]any{
			"reason": "claude-bow.js lint output did not match the expected findings shape or the known-clean sentinel",
		})
	}
	return LintReport{Total: len(findings), Findings: findings}, nil
}

// RunLint execs `node claude-bow.js lint` from repoRoot and parses its
// output (AC-1).
func RunLint(ctx context.Context, repoRoot string) (LintReport, error) {
	correlationID := errs.NewCorrelationID()
	out, err := runBowCommand(ctx, repoRoot, "lint")
	if err != nil {
		return LintReport{}, errs.Wrap(codeLintSourceUnavailable, correlationID, err, map[string]any{"command": "lint"})
	}
	return ParseLintText(out)
}
