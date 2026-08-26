package synth

import "github.com/aaronukgarcia/Metropolis/internal/engine/compose"

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
// remembers to update it by hand — there is no compiler that can catch
// the drift from inside this package, because this package cannot see
// into headless.Run's engine construction. Logged as an assumption
// against this item's BOW record (see the ASM this dispatch adds/
// supersedes) with an explicit "breaks if" clause and a recommended real
// fix once someone can touch internal/engine/core or
// internal/harness/headless: an exported accessor (e.g.
// core.Engine.HookCount() or headless.Result.PhaseHookCount) that this
// package reads instead of asserting by hand — see phasehooks_test.go's
// TestPhaseHookCountAssertionStillTrue doc comment for the full verdict
// on why that runtime accessor, not any source-level scan, is the only
// thing that can ever fully close this gap.
//
// Until that lands, TestPhaseHookCountAssertionStillTrue
// (phasehooks_test.go) re-runs an AST-level scan for the identifier
// RegisterPhaseHook on every `go test` invocation (BUG-053 upgraded this
// from a plain text/regexp grep after a live-verified attack — a
// one-line Go method value, `register := e.RegisterPhaseHook;
// register(kind, hook)` — defeated the grep entirely by never containing
// the literal substring "RegisterPhaseHook(" with an immediately
// following open paren, even though the real call site landed inside
// internal/harness/headless itself). The AST scan closes that specific
// bypass and the wider class of ordinary syntactic indirection (method
// values, wrapper functions, field assignment) because it matches on
// what an identifier IS in the parsed syntax tree, not on how it is laid
// out as text — but it still cannot catch a call built through
// reflect.MethodByName using a runtime-constructed (not literal) string,
// and it still cannot prove headless.Run's OWN construction is hook-free
// today (that requires touching engine.core/harness.headless). It does
// prove no NEW identifier reference to RegisterPhaseHook, in any
// syntactic shape, has appeared anywhere in the scanned tree for a human
// to have missed.
//
// # FEAT-082 (2026-08-15): the hand-asserted 0 is gone
//
// The composition root (internal/engine/compose) landed and now wires the
// baseline-one hook set into the engine harness.headless.Run constructs,
// so the old "the true count, today, is 0" is stale. This function now
// returns the composition root's DECLARED baseline-one hook count
// (compose.BaselineOneHookCount()) rather than a hand-asserted literal —
// so the day a module is added to (or removed from) the composition order,
// this figure moves with it automatically, the exact drift the file's own
// doc comment always warned about. The runtime ground truth is
// core.Engine.HookCount(), surfaced on headless.Result.PhaseHookCount for
// every real run; this declared figure is what PerfResult carries before a
// run exists to read the engine from.
//
// # BUG-237 (2026-08-26): runtime derivation + validation
//
// The declared count (compose.BaselineOneHookCount()) can drift from the
// runtime engine count if registrationOrder is ever modified without
// synchronizing the actual hook registrations (GR#3 single-source-of-truth,
// GR#15 validators derive from data). This is prevented by:
//
//  1. Composition root: compose.BaselineOneHookCount() at
//     internal/engine/compose/compose.go returns len(registrationOrder)
//  2. Runtime: core.Engine.HookCount() at internal/engine/core/engine.go
//     reads the actual registered hooks
//  3. Validation: TestPhaseHookCountDerivedFromRuntime in phasehooks_test.go
//     runs a real compose.Wire() and asserts runtime == declared, failing
//     loudly if they ever diverge
//
// This function returns the composition root's count (which is the SINGLE
// SOURCE of truth for the expected count), and the validation test ensures
// it always matches what the engine actually registers.
func PhaseHookCountInHeadlessPath() int {
	return compose.BaselineOneHookCount()
}
