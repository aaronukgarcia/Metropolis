package metricsdash

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

// KnownPerfPresets are the two named scale points M0-ENG's own spec
// fixes (synth.OneMillionCitizens/synth.TenMillionCitizens, exposed as
// the "1M"/"10M" preset labels synth.Preset1M/synth.Preset10M and
// cmd/perfci's own `-preset` flag use) — a closed set read from the
// existing spec/tooling, not an invented list (GR#15).
var KnownPerfPresets = []string{"1M", "10M"}

// PerfPresetStatus is one preset's current perf-CI state (AC-5).
type PerfPresetStatus struct {
	Preset         string
	HasHistory     bool
	PerMonthTick   time.Duration
	Verdict        string // "no-history" | "passed" | "regressed" | "could-not-evaluate" | "accepted" | "unreadable"
	Accepted       bool
	AcceptedReason string
}

// PerfReport is the parsed/derived form of H-SYNTH's perf-CI history
// for every known preset (AC-5/AC-6/AC-11).
type PerfReport struct {
	Presets                 []PerfPresetStatus
	ResultsFileMissing      bool
	AcceptedRegistryMissing bool
	// Warnings holds one human-readable line per corrupt/unreadable
	// source encountered (AC-11) — a warning here means "this section's
	// data may be incomplete", never "the dashboard crashed".
	Warnings []string
}

// fileMissing reports whether path does not exist — used only to
// annotate "no data yet" (AC-6), never to distinguish that from a real
// I/O error (os.Stat's other error classes are not treated as
// "missing").
func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// readLastRecordForPreset scans path (an AppendResult-written NDJSON
// file, synth/results.go) for the LAST record matching preset (AC-1).
//
// BUG-305 hardened this to mirror synth.LoadLatestBaseline exactly on the
// two axes an independent audit found it had drifted from:
//
//  1. Bounded reads (ASM-355): this used to read lines via a bare
//     bufio.Reader.ReadString('\n'), which has no per-line size ceiling —
//     a single oversized line (corrupt, hostile, or an operator mistake)
//     could OOM the reader, the exact failure ASM-355 closed for
//     LoadLatestBaseline itself but never re-applied here. It now scans
//     via synth.ReadResultsLine, the same finite-ceiling reader
//     LoadLatestBaseline uses, so an oversized line becomes a
//     synth.CorruptLine and is skipped, never read unbounded.
//  2. Provenance screening: this used to trust ANY well-formed
//     PerfRecord for preset as "the last record", including one with
//     Measured=false or an implausible measurement — a hand-edited or
//     corrupted record LoadLatestBaseline would reject as a baseline
//     candidate would still display on the dashboard as if it were
//     genuine. It now applies synth.ScreenRecordProvenance, the same
//     Measured/implausible screen LoadLatestBaseline's replay applies,
//     so a record LoadLatestBaseline would reject is likewise skipped
//     here (reported as a CorruptLine, not silently substituted for a
//     genuine last measurement).
//
// A missing file is not an error (mirrors LoadLatestBaseline).
func readLastRecordForPreset(path, preset string) (*synth.PerfRecord, []synth.CorruptLine, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("metricsdash: opening perf results file %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	var last *synth.PerfRecord
	var corrupt []synth.CorruptLine
	lineNo := 0
	for {
		line, oversized, readErr := synth.ReadResultsLine(reader)
		if readErr != nil && readErr != io.EOF {
			return last, corrupt, fmt.Errorf("metricsdash: reading perf results file %q: %w", path, readErr)
		}
		if oversized {
			lineNo++
			// ASM-355 (mirrored from synth.LoadLatestBaseline): a line
			// over synth.MaxResultsLineBytes is recorded as a
			// CorruptLine (unattributable — its preset was never read)
			// and skipped, exactly like a torn line, so a good later
			// record is still recovered rather than the scan reading an
			// unbounded line or aborting outright.
			corrupt = append(corrupt, synth.CorruptLine{LineNo: lineNo, Err: fmt.Errorf("record at line %d exceeds maxResultsLineBytes (%d bytes) -- refusing to read an unbounded line (ASM-355)", lineNo, synth.MaxResultsLineBytes)})
			if readErr == io.EOF {
				break
			}
			continue
		}
		if len(line) != 0 {
			lineNo++
			trimmed := strings.TrimSpace(string(line))
			if trimmed != "" {
				var rec synth.PerfRecord
				if jerr := json.Unmarshal([]byte(trimmed), &rec); jerr != nil {
					corrupt = append(corrupt, synth.CorruptLine{LineNo: lineNo, Err: jerr})
				} else if rec.Preset == preset {
					if provErr := synth.ScreenRecordProvenance(rec, lineNo); provErr != nil {
						corrupt = append(corrupt, synth.CorruptLine{LineNo: lineNo, Err: provErr})
					} else {
						r := rec
						last = &r
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	return last, corrupt, nil
}

// RunPerf builds the perf-CI section of the dashboard for every known
// preset from resultsPath/acceptedPath (AC-1/AC-5/AC-6/AC-11). It never
// returns a hard error — a missing or corrupt source degrades that
// preset's status/Warnings rather than aborting the whole report
// (AC-6/AC-11: "not a dashboard error/crash").
func RunPerf(resultsPath, acceptedPath string) PerfReport {
	correlationID := errs.NewCorrelationID()
	var report PerfReport
	report.ResultsFileMissing = fileMissing(resultsPath)
	report.AcceptedRegistryMissing = fileMissing(acceptedPath)

	acceptedRegistry, aErr := synth.LoadAcceptedRegistry(acceptedPath)
	if aErr != nil {
		e := errs.Wrap(codePerfSourceCorrupt, correlationID, aErr, map[string]any{"path": acceptedPath})
		report.Warnings = append(report.Warnings, e.Display())
		acceptedRegistry = synth.AcceptedRegistry{}
	}

	for _, preset := range KnownPerfPresets {
		baseline, anchor, corrupt, err := synth.LoadLatestBaseline(resultsPath, preset, acceptedRegistry)
		if err != nil {
			e := errs.Wrap(codePerfSourceCorrupt, correlationID, err, map[string]any{"path": resultsPath, "preset": preset})
			report.Warnings = append(report.Warnings, e.Display())
			report.Presets = append(report.Presets, PerfPresetStatus{Preset: preset, Verdict: "unreadable"})
			continue
		}
		for _, c := range corrupt {
			report.Warnings = append(report.Warnings, fmt.Sprintf("perf results %s line %d unreadable: %v", resultsPath, c.LineNo, c.Err))
		}
		if baseline == nil {
			report.Presets = append(report.Presets, PerfPresetStatus{Preset: preset, HasHistory: false, Verdict: "no-history"})
			continue
		}

		last, lastCorrupt, lastErr := readLastRecordForPreset(resultsPath, preset)
		for _, c := range lastCorrupt {
			report.Warnings = append(report.Warnings, fmt.Sprintf("perf results %s line %d unreadable: %v", resultsPath, c.LineNo, c.Err))
		}
		if lastErr != nil {
			e := errs.Wrap(codePerfSourceCorrupt, correlationID, lastErr, map[string]any{"path": resultsPath, "preset": preset})
			report.Warnings = append(report.Warnings, e.Display())
			report.Presets = append(report.Presets, PerfPresetStatus{Preset: preset, HasHistory: true, PerMonthTick: baseline.PerMonthTick, Verdict: "unreadable"})
			continue
		}
		if last == nil {
			// LoadLatestBaseline found history for this preset but the
			// raw scan above found no matching line — should not
			// normally happen (both read the same file), degrade
			// gracefully rather than panic/guess.
			report.Presets = append(report.Presets, PerfPresetStatus{Preset: preset, HasHistory: true, PerMonthTick: baseline.PerMonthTick, Verdict: "unreadable"})
			continue
		}

		cmp := synth.CompareToBaseline(baseline, anchor, last.Result)
		reason, accepted := acceptedRegistry.Reason(preset, last.CommitHash)

		verdict := "passed"
		switch {
		case cmp.CouldNotEvaluate():
			verdict = "could-not-evaluate"
		case cmp.Regressed:
			verdict = "regressed"
		}
		if accepted && (cmp.Regressed || cmp.CouldNotEvaluate()) {
			// AC-5: the post-acceptance state must read as "accepted",
			// not "still regressed" — mirrors cmd/perfci's own ordering
			// (registry-corroborated acceptance checked before the
			// could-not-evaluate/regressed branches, BUG-095/BUG-094).
			verdict = "accepted"
		}

		report.Presets = append(report.Presets, PerfPresetStatus{
			Preset:         preset,
			HasHistory:     true,
			PerMonthTick:   baseline.PerMonthTick,
			Verdict:        verdict,
			Accepted:       accepted,
			AcceptedReason: reason,
		})
	}
	return report
}
