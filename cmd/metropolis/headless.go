package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/headless"
)

// headlessFlags are the -headless-only flags, registered unconditionally
// on run()'s FlagSet (AC-12: `-headless -h`/`--help` must document every
// flag, including these, without needing -headless itself set first).
type headlessFlags struct {
	seed     *uint64
	months   *int64
	out      *string
	scenario *string
	report   *string
	poolSize *int

	// debug and in are FEAT-035's wiring: -debug enables feat.debugmode
	// (debug.SourceFlag) for this run, sticky-flagging the header this
	// run writes; -in names a prior bundle directory to carry that
	// prior run's DebugTouched flag forward from (ASM-403's reload
	// mechanism, used by the mandatory two-hop end-to-end test).
	debug *bool
	in    *string
}

// registerHeadlessFlags wires every -headless flag into fs with a
// one-line description each (AC-12).
func registerHeadlessFlags(fs *flag.FlagSet) headlessFlags {
	return headlessFlags{
		seed:     fs.Uint64("seed", 0, "headless: deterministic world seed (required with -headless)"),
		months:   fs.Int64("months", 0, "headless: number of in-game months to advance, must be > 0 (required with -headless)"),
		out:      fs.String("out", "", "headless: bundle directory to write the -out snapshot to (required with -headless; must not already exist)"),
		scenario: fs.String("scenario", "", "headless: path to a JSON scenario script (a JSON array of protocol.Command envelopes) run before tick advancement"),
		report:   fs.String("report", "", "headless: path to write per-tick phase-timing and invariant NDJSON reports to (default: not written)"),
		poolSize: fs.Int("pool-size", 0, "headless: override POOL-SIM worker count (0 = default, runtime.NumCPU()-2)"),
		debug:    fs.Bool("debug", false, "headless: enable feat.debugmode (FEAT-035) for this run -- sticky-flags the written bundle's header debug-touched"),
		in:       fs.String("in", "", "headless: prior bundle directory to resume from -- carries that run's DebugTouched flag forward into this run's header (FEAT-035 AC-M1)"),
	}
}

// runHeadless implements `metropolis -headless -seed N -months M -out
// snap.json [-scenario path.json] [-report path.ndjson] [-pool-size N]`
// (MOD-015's own acceptance file, harness.headless.md AC-1). fs must
// already have had Parse called on it (run() does this) — runHeadless
// only reads the parsed flag values and uses fs.Visit to detect which
// flags were actually passed on the command line (AC-2: "omitting"
// -seed/-months/-out is a usage error, distinct from explicitly passing
// a zero/empty value for one of them).
func runHeadless(fs *flag.FlagSet, hf headlessFlags, stdout, stderr io.Writer) int {
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })

	var missing []string
	if !seen["seed"] {
		missing = append(missing, "-seed")
	}
	if !seen["months"] {
		missing = append(missing, "-months")
	}
	if !seen["out"] {
		missing = append(missing, "-out")
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "metropolis -headless: missing required flag(s): %s\n", strings.Join(missing, ", "))
		return 2
	}
	if *hf.months <= 0 {
		_, _ = fmt.Fprintf(stderr, "metropolis -headless: -months must be > 0 (got %d)\n", *hf.months)
		return 2
	}

	correlationID := errs.NewCorrelationID()
	cfg := headless.Config{
		Seed:          *hf.seed,
		Months:        *hf.months,
		OutDir:        *hf.out,
		ScenarioPath:  *hf.scenario,
		PoolSize:      *hf.poolSize,
		CorrelationID: string(correlationID),
		Debug:         *hf.debug,
		InDir:         *hf.in,
	}

	if *hf.report != "" {
		f, err := os.Create(*hf.report)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "metropolis -headless: creating -report file %q: %v\n", *hf.report, err)
			return 1
		}
		defer func() { _ = f.Close() }()
		cfg.Report = f
	}

	result, err := headless.Run(context.Background(), cfg)
	if err != nil {
		printBootError(stderr, err)
		return 1
	}
	if result.ReportWriteErr != nil {
		_, _ = fmt.Fprintf(stderr, "metropolis -headless: warning: -report stream write failed: %v\n", result.ReportWriteErr)
	}

	_, _ = fmt.Fprintf(stdout, "metropolis -headless: wrote %s (worldSeed=%d, ticksAdvanced=%d, scenarioCommands=%d, formatVersion=%s)\n",
		*hf.out, result.Header.WorldSeed, result.TicksAdvanced, result.ScenarioCommands, result.Header.FormatVersion)
	return 0
}
