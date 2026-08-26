package metricsdash

import (
	"bufio"
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// WeaknessRecurrenceThreshold mirrors claude-bow.js's cmdWeakness
// function (`const recurring = sorted.filter(([, e]) => e.total >= 3)`)
// exactly (AC-2/GR#15: derive the boundary from the tool's own real
// behaviour, never re-pick a number). If cmdWeakness's threshold ever
// changes, this constant must change with it — TestWeaknessRecurrence_
// BoundaryMatchesThreshold below fails loudly if the two drift apart in
// a way this parser can detect via the fixture text.
const WeaknessRecurrenceThreshold = 3

// WeaknessClass is one finding-class row from `node claude-bow.js
// weakness`'s histogram output.
type WeaknessClass struct {
	Name      string
	Total     int
	Open      int
	Recurring bool
}

// WeaknessReport is the parsed form of cmdWeakness's stdout (AC-2).
type WeaknessReport struct {
	TotalFindings int
	OpenFindings  int
	Classes       []WeaknessClass
}

// weaknessRowPattern matches one histogram row exactly as cmdWeakness
// prints it:
//
//	`  ${cls.padEnd(width)}  ${total.padStart(3)} total  ${open.padStart(3)} open  ${bar}`
//
// — two leading spaces, the class name, at least two spaces (the
// padEnd fill plus the literal separator), the total count, " total",
// then the open count, " open".
var weaknessRowPattern = regexp.MustCompile(`^  (\S(?:.*\S)?)\s{2,}(\d+) total\s+(\d+) open\b`)

// ParseWeaknessText parses `node claude-bow.js weakness`'s real stdout
// (cmdWeakness) into a WeaknessReport. A report of "no findings
// recorded yet" is a legitimate, empty (not erroneous) result — this
// project's own BOW can genuinely have zero recorded security
// findings.
func ParseWeaknessText(text string) (WeaknessReport, error) {
	if strings.Contains(text, "No security findings recorded yet.") {
		return WeaknessReport{}, nil
	}

	var rep WeaknessReport
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		m := weaknessRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		open, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		rep.Classes = append(rep.Classes, WeaknessClass{
			Name:      strings.TrimSpace(m[1]),
			Total:     total,
			Open:      open,
			Recurring: total >= WeaknessRecurrenceThreshold,
		})
		rep.TotalFindings += total
		rep.OpenFindings += open
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return WeaknessReport{}, weaknessDataFailed(errs.NewCorrelationID(), "scanner error: "+scanErr.Error())
	}
	if len(rep.Classes) == 0 {
		// Neither the "no findings" sentinel nor any parseable row —
		// this is an unrecognised output shape, not a legitimate empty
		// state (AC-1: never silently render an empty dashboard for a
		// source that actually returned something we failed to read).
		return WeaknessReport{}, errs.New(codeWeaknessSourceUnavailable, errs.NewCorrelationID(), map[string]any{
			"reason": "claude-bow.js weakness output did not match the expected histogram shape or the known-empty sentinel",
		})
	}
	return rep, nil
}

// RunWeakness execs `node claude-bow.js weakness` from repoRoot and
// parses its output (AC-1).
func RunWeakness(ctx context.Context, repoRoot string) (WeaknessReport, error) {
	correlationID := errs.NewCorrelationID()
	out, err := runBowCommand(ctx, repoRoot, "weakness")
	if err != nil {
		return WeaknessReport{}, weaknessDataFailed(correlationID, err.Error())
	}
	return ParseWeaknessText(out)
}
