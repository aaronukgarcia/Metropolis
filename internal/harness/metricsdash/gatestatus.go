package metricsdash

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// GateCheck is one of the five named sprint-gate checks
// (data-files/call-edges/tripwires/boundary-rulings/ready-queue) and
// its individual verdict (AC-3).
type GateCheck struct {
	Number         int
	Name           string
	Verdict        string
	ManualOverride bool
}

// GateStatusReport is the parsed form of cmdGateStatus's stdout.
type GateStatusReport struct {
	Sprint             string
	NoVerdictsRecorded bool
	Checks             []GateCheck
	Overall            string
}

// gateCheckLinePattern matches cmdGateStatus's own per-check line:
//
//	`  check ${check_number} (${check_name}): ${VERDICT}${manualTag}  [${runner}, ${ts}]`
var gateCheckLinePattern = regexp.MustCompile(`^  check (\d+) \(([a-zA-Z0-9_-]+)\): ([A-Za-z]+)`)

// gateOverallLinePattern matches cmdGateStatus's derived-overall
// summary line.
var gateOverallLinePattern = regexp.MustCompile(`^Overall \(derived.*\): (\S+)\s*$`)

// ParseGateStatusText parses `node claude-bow.js gate-status
// <sprint>`'s real stdout (cmdGateStatus) into a GateStatusReport. "No
// gate verdicts recorded" for a sprint is a legitimate state
// (AC-28's own text, cited in cmdGateStatus) — not a parse error.
func ParseGateStatusText(sprint, text string) (GateStatusReport, error) {
	if strings.Contains(text, "NO GATE VERDICTS RECORDED") {
		return GateStatusReport{Sprint: sprint, NoVerdictsRecorded: true}, nil
	}

	rep := GateStatusReport{Sprint: sprint}
	for _, line := range strings.Split(text, "\n") {
		if m := gateCheckLinePattern.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			rep.Checks = append(rep.Checks, GateCheck{
				Number:         n,
				Name:           m[2],
				Verdict:        strings.ToUpper(m[3]),
				ManualOverride: strings.Contains(line, "MANUAL-OVERRIDE"),
			})
			continue
		}
		if m := gateOverallLinePattern.FindStringSubmatch(line); m != nil {
			rep.Overall = m[1]
		}
	}

	if len(rep.Checks) == 0 || rep.Overall == "" {
		return GateStatusReport{}, errs.New(codeGateStatusSourceUnavailable, errs.NewCorrelationID(), map[string]any{
			"reason": "claude-bow.js gate-status output did not match the expected per-check/overall shape or the known-empty sentinel",
			"sprint": sprint,
		})
	}
	return rep, nil
}

// RunGateStatus execs `node claude-bow.js gate-status <sprint>` from
// repoRoot and parses its output (AC-1/AC-3).
func RunGateStatus(ctx context.Context, repoRoot, sprint string) (GateStatusReport, error) {
	correlationID := errs.NewCorrelationID()
	out, err := runBowCommand(ctx, repoRoot, "gate-status", sprint)
	if err != nil {
		return GateStatusReport{}, gateStatusFailed(correlationID, sprint, err.Error())
	}
	return ParseGateStatusText(sprint, out)
}
