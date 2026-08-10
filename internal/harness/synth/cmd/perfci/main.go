// Command perfci is H-SYNTH's CI-runnable perf gate (AC-6, AC-10): it
// generates the named scale preset (1M-citizen by default — M0-ENG §6
// point 5 names this preset specifically for the regression gate), runs
// it -months simulated months through the real engine.core command path
// (synth.RunPerf), compares the resulting monthly-tick time against the
// stored baseline for -results' most recent record at that preset, and
// exits non-zero (os.Exit(1), never merely a logged warning — AC-10) if
// it regressed more than synth.RegressionThreshold (10%, M0-ENG §6 point
// 5's own figure). See internal/harness/synth/baseline.go's
// CompareToBaseline doc comment for how this comparison is hardened
// against BUG-031 (a hardcoded absolute wall-clock ceiling that broke a
// correct build on a busy shared runner).
//
// A missing baseline (first run on a new preset, a fresh CI cache) is
// NOT a failure (AC-8): it records this run as the new baseline and
// reports "no prior baseline to compare", exiting 0.
//
// Usage:
//
//	go run ./internal/harness/synth/cmd/perfci -preset 1M -months 12 -results perf-results.ndjson
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is split out from main so a test can drive the whole CLI without
// calling os.Exit (which would kill the test binary) — the same pattern
// this codebase's other cmd/ entry points use. stdout/stderr are
// io.Writer (not *os.File) precisely so a test can pass an in-memory
// buffer rather than juggling real file handles.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("perfci", flag.ContinueOnError)
	preset := fs.String("preset", "1M", `scale preset: "1M" or "10M" (AC-3)`)
	months := fs.Int("months", 12, "simulated months to run")
	seed := fs.Uint64("seed", 0, "world seed (deterministic — AC-9)")
	results := fs.String("results", "perf-results.ndjson", "path to the persisted per-commit results file (AC-5)")
	commit := fs.String("commit", "", "commit hash this run is filed under (defaults to buildinfo.Commit)")
	citizens := fs.Int64("citizens", 0, "override the preset's citizen count (0 = use the preset's real 1M/10M figure); intended for fast local/CI-smoke and test runs, never for the actual regression gate a merge is judged against")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	correlationID := errs.NewCorrelationID()
	commitHash := *commit
	if commitHash == "" {
		commitHash = buildinfo.Commit
	}

	var params synth.Params
	switch *preset {
	case "1M":
		params = synth.Preset1M(*seed)
	case "10M":
		params = synth.Preset10M(*seed)
	default:
		_, _ = fmt.Fprintf(stderr, "perfci: unknown -preset %q, want \"1M\" or \"10M\"\n", *preset)
		return 2
	}
	if *citizens > 0 {
		params.CitizenCount = *citizens
	}

	result, err := synth.RunPerf(correlationID, params, *preset, *months)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: run failed: %v\n", err)
		return 2
	}

	baseline, err := synth.LoadLatestBaseline(*results, *preset)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: reading baseline: %v\n", err)
		return 2
	}

	cmp := synth.CompareToBaseline(baseline, result)
	_, _ = fmt.Fprintln(stdout, cmp.Message)

	if err := synth.AppendResult(*results, synth.PerfRecord{CommitHash: commitHash, Preset: *preset, Result: result}); err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: recording result: %v\n", err)
		return 2
	}

	if cmp.Regressed {
		_, _ = fmt.Fprintln(stderr, synth.RegressionError(correlationID, cmp))
		return 1
	}
	return 0
}
