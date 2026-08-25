// Command sweep is the balance.harness SweepRunner CLI (ICD §1 inbound
// contract: "CLI + JSON scenario defs"). It loads a JSON scenario definition,
// fans the parameter grid out through harness.synth + harness.headless, and
// writes the deterministic result set as NDJSON to -out (or stdout).
//
//	balance-sweep -scenario tools/balance/scenarios/example.json -out results.ndjson
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/tools/balance"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("balance-sweep", flag.ContinueOnError)
	fs.SetOutput(stderr)

	scenarioPath := fs.String("scenario", "", "path to the JSON scenario-definition file (required)")
	seedsFlag := fs.String("seeds", "", "optional comma-separated seed set override (default: the scenario's seeds)")
	workers := fs.Int("workers", 0, "fan-out worker count (0 = default)")
	outPath := fs.String("out", "", "path to write the NDJSON results to (default: stdout)")
	commitHash := fs.String("commit", "", "provenance commit hash override (default: buildinfo.Commit)")
	version := fs.String("version", "", "provenance version override (default: buildinfo.Version)")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scenarioPath == "" {
		fmt.Fprintln(stderr, "balance-sweep: -scenario is required")
		return 2
	}

	scn, err := balance.LoadScenario(*scenarioPath)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	if *seedsFlag != "" {
		seeds, parseErr := parseSeeds(*seedsFlag)
		if parseErr != nil {
			fmt.Fprintf(stderr, "balance-sweep: -seeds: %v\n", parseErr)
			return 2
		}
		scn.Seeds = seeds
	}

	var out io.Writer = stdout
	var outFile *os.File
	if *outPath != "" {
		outFile, err = os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(stderr, "balance-sweep: creating -out file %q: %v\n", *outPath, err)
			return 1
		}
		defer func() { _ = outFile.Close() }()
		out = outFile
	}

	runner := balance.NewSweepRunner(balance.HeadlessRunner(), balance.Options{
		WorkerCount: *workers,
		CommitHash:  *commitHash,
		Version:     *version,
	})
	result, err := runner.Run(context.Background(), scn, out)
	if err != nil {
		printErr(stderr, err)
		return 1
	}

	proposal := balance.Proposal(scn.Target, result.Records)
	fmt.Fprintf(stdout, "balance-sweep: %d cells, %d records, %d in band [%.0f, %.0f]\n",
		result.TotalCells, len(result.Records), len(proposal), scn.Target.Band[0], scn.Target.Band[1])
	return 0
}

func parseSeeds(s string) ([]uint64, error) {
	parts := strings.Split(s, ",")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid seed %q: %w", p, err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no seeds parsed from %q", s)
	}
	return out, nil
}

func printErr(w io.Writer, err error) {
	if e, ok := err.(*errs.E); ok {
		fmt.Fprintln(w, e.Display())
		return
	}
	fmt.Fprintln(w, err)
}
