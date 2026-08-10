package synth

// PhaseHookCountInHeadlessPath is the specific defence BUG-034 (this
// item's driving BOW record) asked for by name: "nothing currently stops
// someone citing a '10M-citizen tick cost' from today's runs, which
// would be pure walking-skeleton overhead wearing a simulation label."
// Every PerfResult now carries this figure (perf.go's
// PerfResult.PhaseHookCount) alongside its timing, so the context
// travels WITH the number rather than living only in a doc comment a
// reader of a graph or a quoted figure would never see.
//
// # What this actually is (read before trusting the number)
//
// This is NOT a live introspection of a running *core.Engine — neither
// internal/harness/headless (which builds the engine RunPerf's
// measurement actually drives, run.go's core.NewEngine call) nor
// internal/engine/core exposes a hook-count accessor, and both packages
// are outside this dispatch's file ownership (BUG-034's dispatch brief:
// "FILES YOU OWN: internal/harness/synth/**, .github/workflows/ci.yml").
// This is a MANUALLY ASSERTED fact about harness.headless's engine-
// construction path, true as of the revision this comment was written
// against (2026-08-10, internal/harness/headless/run.go's Run function):
//
//	opts := []core.Option{
//	    core.WithWorldSeed(cfg.Seed),
//	    core.WithPhaseObserver(rw.phaseObserver()),
//	}
//	if cfg.PoolSize > 0 { opts = append(opts, core.WithPoolSize(cfg.PoolSize)) }
//	e := core.NewEngine(opts...)
//
// No RegisterPhaseHook call appears anywhere in headless.Run's call
// graph — confirmed both by reading run.go directly and by
// `grep -rln "RegisterPhaseHook" --include=*.go .` across the whole
// repo, which finds call sites only in internal/engine/core (the
// interface itself), internal/engine/stub, internal/engine/invariant,
// internal/engine/debug, internal/harness/replay, and
// internal/ui/screens/debug — every one of them builds and drives ITS
// OWN *core.Engine for its own purpose, never the one harness.headless
// constructs for this package's RunPerf. So the true count, today, is 0.
//
// # This constant WILL go stale, silently, the day that changes
//
// The moment any module starts registering a real PhaseHook against the
// engine harness.headless.Run constructs (the whole point of Sprint 3's
// citizens work landing), this hardcoded 0 becomes a lie unless someone
// remembers to update it by hand — there is no compiler or test that can
// catch the drift from inside this package, because this package cannot
// see into headless.Run's engine construction. Logged as an assumption
// against this item's BOW record (see the ASM this dispatch adds/
// supersedes) with an explicit "breaks if" clause and a recommended real
// fix once someone can touch internal/engine/core or
// internal/harness/headless: an exported accessor (e.g.
// core.Engine.HookCount() or headless.Result.PhaseHookCount) that this
// package reads instead of asserting by hand. Until that lands,
// TestPhaseHookCountAssertionStillTrue (phasehooks_test.go) re-runs the
// same grep this comment describes on every `go test` invocation, so a
// new RegisterPhaseHook call site anywhere in the repo fails the build
// loudly rather than letting this constant drift unnoticed — it cannot
// prove headless.Run's OWN construction is still hook-free (that would
// require touching engine.core/harness.headless), but it does prove no
// NEW call site has appeared anywhere for a human to have missed.
func PhaseHookCountInHeadlessPath() int {
	return 0
}
