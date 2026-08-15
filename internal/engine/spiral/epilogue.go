package spiral

import (
	"fmt"
	"strings"
)

// This file implements AC-8's ghost-city epilogue: generated from the
// actual logged history of THIS save — the shock date, the key
// stage-transition dates, and the historic peak value and date — never a
// static templated string with a single substituted number.

// Epilogue is the ghost-city ending's generated epilogue (AC-8).
type Epilogue struct {
	Title      string
	Lines      []string
	ShockMonth int64
	PeakMonth  int64
	PeakValue  int64
	FinalMonth int64
	FinalValue int64
}

// GenerateEpilogue builds an epilogue from the logged history (AC-8): it
// takes the history log as an explicit input and references real logged
// events — the shock date, each key stage-transition date, and the historic
// peak value and date — so two saves with different histories produce
// different epilogues, not just cosmetic wording differences.
func (d *DecayAPI) GenerateEpilogue(history []HistoryEntry, events []Event) Epilogue {
	if err := d.checkNotCopied("GenerateEpilogue"); err != nil {
		return Epilogue{}
	}
	var ep Epilogue
	ep.Title = "The City Falls Silent"

	// Shock date: the first shock event in the log.
	for _, ev := range events {
		if ev.Kind == EventShock {
			ep.ShockMonth = ev.Month
			break
		}
	}

	// Historic peak: the highest logged population and the month it was
	// recorded.
	ep.PeakValue = 0
	ep.PeakMonth = 0
	for _, h := range history {
		if h.Population > ep.PeakValue {
			ep.PeakValue = h.Population
			ep.PeakMonth = h.Month
		}
	}

	// Final population.
	if len(history) > 0 {
		last := history[len(history)-1]
		ep.FinalMonth = last.Month
		ep.FinalValue = last.Population
	}

	// Stage-transition dates: every rising transition, in order, referenced
	// by the real month it happened and the stage it reached.
	var stages []string
	lastStage := StageStable
	for _, ev := range events {
		if ev.Kind != EventStageTransition {
			continue
		}
		if ev.Stage == lastStage {
			continue
		}
		lastStage = ev.Stage
		stages = append(stages, fmt.Sprintf("month %d: %s", ev.Month, ev.Stage))
	}

	ep.Lines = []string{
		"This city was abandoned not by a single catastrophe, but by a spiral.",
	}
	if ep.ShockMonth > 0 {
		ep.Lines = append(ep.Lines, fmt.Sprintf("In month %d the shock came, and the first door closed.", ep.ShockMonth))
	}
	if len(stages) > 0 {
		ep.Lines = append(ep.Lines, "Then, in order:")
		ep.Lines = append(ep.Lines, stages...)
	}
	if ep.PeakValue > 0 {
		ep.Lines = append(ep.Lines, fmt.Sprintf("At its height, in month %d, %d people called it home.", ep.PeakMonth, ep.PeakValue))
	}
	if ep.FinalValue > 0 {
		ep.Lines = append(ep.Lines, fmt.Sprintf("By month %d, only %d remained.", ep.FinalMonth, ep.FinalValue))
	}
	ep.Lines = append(ep.Lines, "The city fell silent.")

	return ep
}

// Render returns the epilogue as a single newline-joined string (the
// player-facing text).
func (e Epilogue) Render() string {
	return strings.Join(e.Lines, "\n")
}
