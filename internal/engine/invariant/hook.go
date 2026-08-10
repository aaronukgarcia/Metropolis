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

// ApplyEffect implements core.PhaseHook. Called single-goroutine, at
// the phase barrier, exactly once per tick (see Hook's doc comment).
func (h *Hook) ApplyEffect(eff core.Effect) {
	result, ok := eff.Payload.(SuiteResult)
	if !ok || !result.AnyViolation {
		return
	}
	h.handleViolations(result)
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

// hardFail is AC-8's dev-mode assert: panic, unless a test has
// overridden panicFn (WithPanicFunc).
func (h *Hook) hardFail(msg string) {
	if h.panicFn != nil {
		h.panicFn(msg)
		return
	}
	panic(msg)
}
