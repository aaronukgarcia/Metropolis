// Command metricsdash is feat.metricsdash's out-of-band CLI report
// (see internal/harness/metricsdash's package doc comment,
// "Escalation A" section, for why this dispatch built it out-of-band
// rather than as an in-game screen).
//
// Two modes, selected by which flags are set:
//
//   - Dashboard (default): prints the aggregated weakness/gate-status/
//     lint/perf-CI report (US-1..US-4).
//     Usage: go run ./internal/harness/metricsdash/cmd/metricsdash \
//     [-sprint N] [-repo <path>] [-results <path>] [-accepted-regressions <path>]
//
//   - Log a note (AC-7/AC-9 — "easy", reachable in one command, no
//     pause/inspect flow required):
//     Usage: go run ./internal/harness/metricsdash/cmd/metricsdash \
//     -log "the note text" [-kind bug|finding|assumption] [-context "..."]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/harness/metricsdash"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("metricsdash", flag.ContinueOnError)
	fs.SetOutput(stderr)

	sprint := fs.String("sprint", "3", "sprint number to report gate status for (AC-3)")
	repo := fs.String("repo", ".", "repository root claude-bow.js is invoked from")
	results := fs.String("results", "perf-results.ndjson", "path to H-SYNTH's persisted perf results NDJSON file (AC-5/AC-6)")
	accepted := fs.String("accepted-regressions", "perf-accepted-regressions.json", "path to H-SYNTH's accepted-regression registry file (AC-5/AC-6)")

	logNote := fs.String("log", "", "log a defect/query note (AC-7/AC-9) instead of printing the dashboard")
	kind := fs.String("kind", "", "note kind: bug|finding|assumption (default bug, AC-7)")
	noteContext := fs.String("context", "", "current screen/module/file context for the note (AC-7); defaults to \"unspecified\"")
	inbox := fs.String("inbox", "", "override the feedback inbox directory (defaults to metricsdash.DefaultFeedbackInbox)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *logNote != "" {
		if err := metricsdash.LogNote(*inbox, metricsdash.NoteKind(*kind), *logNote, *noteContext, nil); err != nil {
			_, _ = fmt.Fprintf(stderr, "metricsdash: failed to log note: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Note logged — will be filed as a real BOW item on the next claude-devfeedback-import.js run.")
		return 0
	}

	d := metricsdash.BuildDashboard(context.Background(), *repo, *sprint, *results, *accepted)
	_, _ = fmt.Fprint(stdout, d.Render())
	return 0
}
