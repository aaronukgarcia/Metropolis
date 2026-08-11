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
// # BUG-083: the baseline no longer ratchets on a regressed run, and a
// # genuine slowdown needs an explicit acceptance to become the new one
//
// synth.LoadLatestBaseline (results.go) now reconstructs the stored
// baseline by REPLAYING history rather than trusting whatever was
// appended last — it freezes at the last record that did NOT regress,
// so a regressed run's number (even one that reddens this very job)
// never becomes the reference point a future run compares against. See
// that function's doc comment for the live-verified ratchet this closes
// (30 consecutive sub-threshold commits compounding 13.27x with zero
// signal) and why a purely relative, moving-reference gate cannot see
// that on its own — synth.CumulativeRegressionThreshold (limits.go) is
// the second, anchor-based check this command also gates on for
// exactly that reason.
//
// Because the baseline can now only ever advance on a pass, a genuine,
// INTENDED slowdown (a real feature doing real new work) would block
// every future run forever without a deliberate way out.
//
// # BUG-095: that way out is a git-committed file, not a CLI flag
//
// This command USED TO accept a regression via a bare -accept-regression/
// -accept-reason flag pair, persisted as synth.PerfRecord.AcceptedRegression/
// AcceptedReason. Destructive-9 live-verified that this was a full bypass
// of BUG-083's fix, not merely of the step check: those two fields are
// just data in the SAME results file a corrupted-cache restore, a hand
// edit, or a re-uploaded artifact can already write (the exact
// second-writer threat model BUG-073/085/094 all name) — a hand-injected
// "accepted" record reset both the baseline AND the cumulative anchor to
// any attacker-chosen value with zero friction, turning a genuine,
// unregressed run into a reported 216% regression (or the inverse: masking
// a real future regression behind a forged fast anchor).
//
// The fix moves the acceptance decision to evidence a results-file writer
// cannot reach: -accepted-regressions names a JSON file (default
// perf-accepted-regressions.json), checked into THIS repository, listing
// {preset, commitHash, reason} entries a human added via an ordinary,
// reviewed commit — see synth/accepted.go's AcceptedRegistry doc comment
// for the full mechanism. This command loads that file fresh from the
// checked-out working tree every run and honours an entry only when it
// names the EXACT commit currently under test. There is no longer a CLI
// flag that flips acceptance on directly — the flag is gone precisely
// because a flag is exactly as forgeable as the field it used to set.
//
// This also folds in BUG-094's endorsed recommendation: a registry-
// corroborated acceptance is now checked BEFORE the could-not-evaluate
// branch below, not only after a regression — so a run that is
// permanently stuck (a below-noise-floor or scale-mismatched baseline
// that can never itself become a passing comparison) also has a human
// override reachable, rather than being unrecoverable except by hand-
// editing the results file.
//
// Practical cost, stated plainly rather than glossed over: accepting a
// regression now requires an extra reviewed commit naming the exact
// commit hash being accepted, which must be known before that commit is
// finalised (typically: make the change, commit, read the hash, add/amend
// a registry entry naming it, push) — slower than a one-line CLI flag by
// design, since the whole point is that the acceptance can no longer be
// produced by anything less than a real commit. It also does not survive
// a rebase of the accepted commit: a rebase changes the hash, so the
// registry entry silently stops matching and the "accepted" state is
// lost — this fails CLOSED (the next run simply sees an unaccepted
// regression again, not a wrongly-carried-forward acceptance), which is
// the correct failure direction for a security control, but it is a real
// operational cost a human accepting a regression across a later rebase
// needs to know about.
//
// # BUG-071: three outcomes, three exit codes
//
// A gate has at least three outcomes — passed, failed, and could not be
// evaluated — and this command exits a DISTINCT code for each, because a
// Destructive attack live-verified that folding the third into the
// first (both exiting 0) let a 100%-regressed, scale-mismatched run
// report success AND get written as the next baseline, laundering a bad
// measurement into ground truth with an outward CI signal identical to
// a working gate:
//
//	exit 0 — genuine pass: either no prior baseline exists yet (AC-8,
//	         this run becomes the new baseline) or the comparison ran
//	         and found no regression over synth.RegressionThreshold.
//	exit 1 — genuine regression: the comparison ran and found one
//	         (AC-10). This run's measurement is still recorded, so the
//	         next run compares against the branch's actual history
//	         rather than silently re-comparing against the same old
//	         baseline forever.
//	exit exitCouldNotEvaluate (3) — the comparison was SKIPPED
//	         (synth.BaselineComparison.CouldNotEvaluate(): ScaleMismatch
//	         or BelowNoiseFloor) even though a baseline existed to judge
//	         against. This is never a pass and never treated as one —
//	         printed loudly (a synth.CouldNotEvaluateWarning on stderr,
//	         MET-H309) and, per this item's dispatch brief ("a result
//	         that was never compared should not be allowed to become the
//	         baseline"), this run's measurement is explicitly NOT
//	         appended to the results file. An unevaluated run must never
//	         become the number every future comparison is judged
//	         against.
//	exit 2 — usage/system error: bad flags, the measurement run itself
//	         failed, the results file could not be read/written. Already
//	         distinct from the other three before this fix; unchanged.
//
// See internal/harness/synth/baseline.go's CompareToBaseline doc
// comment for how the comparison itself is hardened against BUG-031 (a
// hardcoded absolute wall-clock ceiling that broke a correct build on a
// busy shared runner).
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

// exitCouldNotEvaluate is BUG-071's third exit code — see run()'s
// package-level doc comment for the full three-outcome design. Named as
// a constant (rather than an inline magic 3) so a caller reading either
// this file or a test can find the single definition, and so
// TestRun_ScaleMismatch... below asserts against the same symbol run()
// returns, not a duplicated literal that could silently drift.
const exitCouldNotEvaluate = 3

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
	acceptedRegistryPath := fs.String("accepted-regressions", "perf-accepted-regressions.json", "BUG-095: path to the git-committed registry of {preset, commitHash, reason} acceptance entries -- the ONLY way a regression (or a permanently could-not-evaluate baseline) can become the new reference point. Missing file = nothing accepted yet, which is the ordinary state. There is deliberately no CLI flag to accept a regression directly any more; see this file's package doc comment (BUG-095).")
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

	// BUG-095: loaded fresh from the checked-out working tree every run —
	// see synth/accepted.go's AcceptedRegistry doc comment. A malformed
	// registry is a hard error (fail closed): this file is the sole
	// evidence the accept path now trusts, so a corrupt copy of it must
	// never be silently treated as "nothing accepted".
	acceptedRegistry, err := synth.LoadAcceptedRegistry(*acceptedRegistryPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: reading accepted-regressions registry: %v\n", err)
		return 2
	}

	baseline, anchor, corruptLines, err := synth.LoadLatestBaseline(*results, *preset, acceptedRegistry)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: reading baseline: %v\n", err)
		return 2
	}
	// BUG-054: a recovered baseline (err == nil) can still carry skipped
	// lines — e.g. a torn write from a previous run that got cancelled
	// mid-write. That is recoverable, not fatal, but it must never be
	// silent (GR#1/GR#17): report every skipped line so a human watching
	// this job's log sees the file has some corruption in its history,
	// even though the gate itself is still able to proceed on the good,
	// later record.
	for _, cl := range corruptLines {
		_, _ = fmt.Fprintf(stderr, "perfci: warning: results file %q line %d is corrupt and was skipped (%v); baseline recovery continued past it\n",
			*results, cl.LineNo, cl.Err)
	}
	// BUG-097: a missing results file and a genuinely lost/evicted cache
	// are byte-for-byte indistinguishable from inside this function --
	// os.IsNotExist cannot tell "this preset has never been measured"
	// from "it was measured, and the cache that remembered that is gone".
	// Said plainly, on every such run, rather than folded silently into
	// the ordinary AC-8 "no prior baseline" message: this is genuinely
	// the best this tool can do without a signal from outside the cache
	// it itself reads (see BUG-097's filed follow-up for what such a
	// signal would need to look like).
	if baseline == nil && len(corruptLines) == 0 {
		_, _ = fmt.Fprintf(stderr, "perfci: NOTE: no prior baseline found for preset %q at %q (BUG-097). This is either a genuine first-ever run for this preset, OR a lost/evicted CI cache -- this tool cannot currently distinguish the two, so this run is being recorded as a fresh baseline either way. If perf gating for this preset has silently gone quiet, check whether an earlier cache entry for this preset still exists.\n", *preset, *results)
	}

	cmp := synth.CompareToBaseline(baseline, anchor, result)
	_, _ = fmt.Fprintln(stdout, cmp.Message)

	rec := synth.PerfRecord{CommitHash: commitHash, Preset: *preset, Result: result}

	// BUG-095/BUG-094: a registry-corroborated acceptance for THIS EXACT
	// commit is checked before either the could-not-evaluate branch or
	// the ordinary regressed/pass branches below -- see this file's
	// package doc comment for the full BUG-095 rationale (why this is no
	// longer a CLI flag) and BUG-094's endorsed extension (why this now
	// also covers a could-not-evaluate verdict, not only a regressed
	// one): a permanently stuck below-noise-floor or scale-mismatched
	// baseline is exactly the kind of "future unrecoverable state" that
	// should still have a human override reachable, rather than being
	// recoverable only by hand-editing the results file.
	if acceptReason, accepted := acceptedRegistry.Reason(*preset, commitHash); accepted && (cmp.Regressed || cmp.CouldNotEvaluate()) {
		rec.AcceptedRegression = true
		rec.AcceptedReason = acceptReason
		banner := fmt.Sprintf("perfci: ACCEPTING this measurement as the new baseline -- commit %q is listed in %q. Reason: %q. %s", commitHash, *acceptedRegistryPath, acceptReason, cmp.Message)
		_, _ = fmt.Fprintln(stdout, banner)
		_, _ = fmt.Fprintln(stderr, banner)
		if err := synth.AppendResult(*results, rec); err != nil {
			_, _ = fmt.Fprintf(stderr, "perfci: recording accepted result: %v\n", err)
			return 2
		}
		return 0
	}

	// BUG-071: a skipped comparison is its own outcome, not a pass. Check
	// this BEFORE ever touching AppendResult — the dispatch brief's own
	// question ("should a result that was never compared become the
	// baseline?") is answered here: no. An unevaluated run's measurement
	// is reported loudly and then discarded, not filed as history.
	if cmp.CouldNotEvaluate() {
		_, _ = fmt.Fprintln(stderr, synth.CouldNotEvaluateWarning(correlationID, cmp))
		_, _ = fmt.Fprintln(stderr, "perfci: GATE COULD NOT EVALUATE this run — this is NOT a pass, the regression check was skipped, and this measurement was NOT recorded as the new baseline (BUG-071). If this state is permanent (e.g. BUG-094), a human can unblock it by adding this commit to the accepted-regressions registry (BUG-095).")
		return exitCouldNotEvaluate
	}

	if err := synth.AppendResult(*results, rec); err != nil {
		_, _ = fmt.Fprintf(stderr, "perfci: recording result: %v\n", err)
		return 2
	}

	if cmp.Regressed {
		_, _ = fmt.Fprintln(stderr, synth.RegressionError(correlationID, cmp))
		return 1
	}
	return 0
}
