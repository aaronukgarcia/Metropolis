package invariant

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SnapshotProvider builds this tick's Snapshot for the invariant suite
// to check. Called from Hook.RunShard, always for shard 0 only (see
// Hook's doc comment) — implementations are free to read whatever
// already-committed world state they need (module registries, a
// citizens/finance ledger once those exist), but must be a pure,
// deterministic function of tick: same tick, same returned Snapshot,
// regardless of goroutine scheduling or POOL-SIM worker count (AC-14,
// AC-15 — never the wall clock).
type SnapshotProvider func(tick int64) Snapshot

// HookOption customizes a Hook. Unset options take the defaults
// documented on each With* function.
type HookOption func(*Hook)

// WithDevMode sets whether a Detected Violation additionally triggers a
// hard assert (AC-8), on top of always being logged (AC-9). Defaults to
// false (release: log only, never crash the process) — deny-by-default,
// matching engine.core's WithSpeed8xGate/foundation.registry's
// CanToggle convention of never defaulting to the more dangerous
// behaviour. M0-ENG §3 treats "debug" as a runtime feature switch, not
// a build flavour, so this is a runtime Option rather than a build tag
// (ASM-* logged against this file — see the dispatch report).
func WithDevMode(enabled bool) HookOption {
	return func(h *Hook) { h.devMode = enabled }
}

// WithPanicFunc overrides the function Hook calls for AC-8's dev-mode
// hard assert, instead of a real panic(). Primarily for tests (AC-8's
// check explicitly calls for "a test-only override, not an actual
// process-killing panic in the test binary") — production callers
// should leave this unset.
func WithPanicFunc(fn func(msg string)) HookOption {
	return func(h *Hook) { h.panicFn = fn }
}

// WithLogSink installs a callback invoked with every registry-sourced
// *errs.E this Hook constructs for a Detected Violation (AC-9's
// release-mode logging path — errs.New already writes to
// foundation.errors' log sink internally; this is an ADDITIONAL,
// optional observation point for tests and for a caller that wants its
// own copy of what was logged, e.g. to correlate with a headless run's
// report). nil (the default) means no additional callback — logging via
// errs.New still happens either way.
func WithLogSink(sink func(*errs.E)) HookOption {
	return func(h *Hook) { h.logSink = sink }
}

// Hook is engine.core.PhaseHook (AC-7): the wired form of RunSuite that
// runs once per tick on whichever core.PhaseKind it is registered
// against (see wire.go).
//
// # Why only shard 0 does real work
//
// Conservation checking is a single, whole-tick verdict (RunSuite over
// one Snapshot), not shard-parallel work — there is no meaningful way
// to split "does this tick's total balance" across 256 shards. Rather
// than not implementing PhaseHook's contract, RunShard does its real
// work only for shard == 0 and returns (nil, nil) immediately for every
// other shard (255 cheap branch checks, no allocation) — this still
// honours "RunShard must touch only shard-local scratch" (it touches
// nothing shared) and is deterministic under det.RunPhase regardless of
// POOL-SIM's worker count, because det.RunPhase always calls RunShard
// for every shard in [0, NumShards) exactly once, and shard 0's
// identity never depends on scheduling (foundation/det/phase.go).
// ApplyEffect then runs, single-goroutine, at the phase barrier —
// exactly once, since only one Effect was ever emitted.
type Hook struct {
	engine   *core.Engine
	registry *Registry
	provider SnapshotProvider

	devMode bool
	panicFn func(msg string)
	logSink func(*errs.E)
}

// RunShard implements core.PhaseHook. See Hook's doc comment for why
// only shard 0 performs work.
func (h *Hook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}

	clock, err := h.engine.Clock()
	if err != nil {
		// Already a registry-sourced *errs.E (e.g. ErrEngineCopied) —
		// propagate unchanged, per this package's own error-handling
		// convention.
		return nil, err
	}

	state := h.provider(clock.Tick())
	result := RunSuite(h.registry, state)
	return []core.Effect{{Sequence: 0, Payload: result}}, nil
}

// SingleShard implements core.SingleShardHook (BUG-269): as documented
// above, RunShard does real work only for shard 0 and returns (nil,
// nil) immediately for every other shard, so this hook qualifies for
// the SingleShardHook fast path — det.RunPhase's 256-shard
// goroutine-pool dispatch is provably unnecessary for a whole-tick
// conservation verdict that was never shard-parallel work to begin
// with. This hook is one of the two BUG-269's regression report named
// directly (the other is compose.go's buildHook).
func (h *Hook) SingleShard() bool { return true }

// ApplyEffect implements core.PhaseHook. Called single-goroutine, at
// the phase barrier, exactly once per tick (see Hook's doc comment).
//
// BUG-277: this consumer now reads BOTH verdicts RunSuite reports —
// AnyViolation (an imbalance was actually detected) and AllRan (every
// registered invariant actually ran this tick). Before the fix only
// AnyViolation was consumed, so a provider returning an empty snapshot
// (or a StockReading left at its Registered:false zero value) silently
// starved a registered invariant every tick with no error, no log, no
// dev-mode assert — a gate that could not evaluate reporting success,
// which is the exact silent-failure class this package exists to prevent
// (AC-1b's consumer obligation: never conflate "some invariants were
// skipped" with "all invariants ran clean").
func (h *Hook) ApplyEffect(eff core.Effect) {
	result, ok := eff.Payload.(SuiteResult)
	if !ok {
		h.handleMalformedPayload(eff)
		return
	}
	if result.AnyViolation {
		h.handleViolations(result)
	}
	if !result.AllRan {
		h.handleSkipped(result)
	}
}

// handleViolations turns every Detected Violation in result into a
// registry-sourced error (AC-9, always) and, in dev mode, additionally
// hard-fails (AC-8). One correlation ID is minted per tick's batch of
// violations, so multiple simultaneous violations in the same tick
// trace back to one root cause investigation rather than N unrelated
// ones.
func (h *Hook) handleViolations(result SuiteResult) {
	correlationID := errs.NewCorrelationID()

	for _, outcome := range result.Outcomes {
		if !outcome.Violation.Detected {
			continue
		}

		e := errs.New(ErrConservationViolation, correlationID, map[string]any{
			"invariant": outcome.Name,
			"tick":      result.Tick,
			"expected":  outcome.Violation.Expected,
			"actual":    outcome.Violation.Actual,
		})
		if h.logSink != nil {
			h.logSink(e)
		}

		if h.devMode {
			h.hardFail(fmt.Sprintf(
				"%s (invariant=%s tick=%d expected=%d actual=%d)",
				e.Display(), outcome.Name, result.Tick, outcome.Violation.Expected, outcome.Violation.Actual,
			))
		}
	}
}

// skippedDelta is what handleSkipped reports for MET-E300's
// expected/actual delta placeholders: an invariant that never ran has no
// delta to report, so the rendered message reads "expected delta n/a,
// actual delta n/a" rather than leaving the literal {expected}/{actual}
// placeholders unrendered (errs.renderTemplate leaves a missing template
// key as its visible literal "{key}" — never a silent drop).
const skippedDelta = "n/a"

// skippedReason is the shared diagnostic for a registered invariant that
// did not run this tick: its stock was not reported/registered in the
// Snapshot (AC-12's skip). Carried both in the registry-sourced error's
// Ctx (so it lands in the structured log entry) and in the dev-mode hard
// assert's message, so a human can tell "starved invariant" apart from a
// genuine imbalance, which shares the same reused MET-E300 code.
const skippedReason = "registered invariant did not run this tick: stock not reported/registered in Snapshot"

// handleSkipped turns every Outcome whose Ran is false (its stock was
// not reported/registered in this tick's Snapshot — AC-12's skip) into a
// registry-sourced error (always logged) and, in dev mode, additionally
// a hard assert (AC-8), mirroring handleViolations one-for-one. One
// correlation ID is minted per tick's batch of skipped invariants, as
// handleViolations does for its batch.
//
// BUG-277: a registered invariant that silently never runs is a gate
// that cannot evaluate, and a gate that cannot evaluate must never
// report success. ErrConservationViolation (MET-E300) is REUSED here —
// data/errors.json is deliberately untouched, per this fix's scope — a
// skipped invariant is the conservation checker failing to verify
// conservation, the same failure class MET-E300 already names. The
// skippedReason field (carried in Ctx, surfaced in the structured log
// entry) is what distinguishes "did not run" from a genuine imbalance.
func (h *Hook) handleSkipped(result SuiteResult) {
	correlationID := errs.NewCorrelationID()

	for _, outcome := range result.Outcomes {
		if outcome.Ran {
			continue
		}

		e := errs.New(ErrConservationViolation, correlationID, map[string]any{
			"invariant": outcome.Name,
			"tick":      result.Tick,
			"expected":  skippedDelta,
			"actual":    skippedDelta,
			"reason":    skippedReason,
		})
		if h.logSink != nil {
			h.logSink(e)
		}

		if h.devMode {
			h.hardFail(fmt.Sprintf(
				"%s (invariant=%s tick=%d reason=%s)",
				e.Display(), outcome.Name, result.Tick, skippedReason,
			))
		}
	}
}

// malformedPayloadReason is the shared diagnostic for an Effect whose
// payload is not a SuiteResult. The Hook only ever emits SuiteResult
// payloads itself, so this is defensive against a mis-wired or corrupted
// Effect — but a gate that cannot evaluate must never report success, so
// the drop is a logged registry error (and dev-mode hard assert), never a
// silent return (BUG-301, the same class as BUG-277's starved-invariant
// fix — MET-E300 is reused, with the reason carried in Ctx).
const malformedPayloadReason = "Effect payload was not a SuiteResult — the conservation verdict could not be evaluated"

// handleMalformedPayload turns a non-SuiteResult Effect payload into a
// registry-sourced error (always logged) and, in dev mode, additionally a
// hard assert, mirroring handleSkipped. It carries the reason in Ctx (which
// lands in the structured log entry) and "n/a" in the template's expected/
// actual delta placeholders, since a malformed payload has no delta to
// report.
func (h *Hook) handleMalformedPayload(eff core.Effect) {
	correlationID := errs.NewCorrelationID()

	e := errs.New(ErrConservationViolation, correlationID, map[string]any{
		"invariant": skippedDelta,
		"tick":      skippedDelta,
		"expected":  skippedDelta,
		"actual":    skippedDelta,
		"reason":    malformedPayloadReason,
	})
	if h.logSink != nil {
		h.logSink(e)
	}

	if h.devMode {
		h.hardFail(fmt.Sprintf(
			"%s (reason=%s sequence=%d)",
			e.Display(), malformedPayloadReason, eff.Sequence,
		))
	}
}

// hardFail is AC-8's dev-mode assert: panic, unless a test has
// overridden panicFn (WithPanicFunc).
func (h *Hook) hardFail(msg string) {
	if h.panicFn != nil {
		h.panicFn(msg)
		return
	}
	panic(msg)
}
