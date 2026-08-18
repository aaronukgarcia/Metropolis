package core

import (
	"fmt"
	"sort"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PhaseKind names one stop in the fixed, documented phase pipeline (§3,
// AC-3, AC-16). It is a closed set — see DailyPhaseOrder and
// MonthlyPhaseOrder below, which are the only valid values
// RegisterPhaseHook accepts.
type PhaseKind string

// The fixed phase set. Monthly phases run in this exact order, every
// month, after the 30th daily tick of that month completes (§3):
// production -> logistics settlement -> consumption & shortfall ->
// population -> land value & decay -> finance. PhaseDailyTick is the
// single daily-tick phase (§8's logistics resolution) that runs once
// per AdvanceTicks-driven day, before the monthly check.
//
// Real module content behind every one of these phases is explicitly
// out of scope for engine.core (see the acceptance doc's "Out of
// scope") — this package only guarantees the barrier and the order;
// citizens/finance/etc. modules register PhaseHooks against these
// names as they go real, one at a time (M0-ENG §2).
const (
	PhaseDailyTick            PhaseKind = "daily-tick"
	PhaseProduction           PhaseKind = "production"
	PhaseLogisticsSettlement  PhaseKind = "logistics-settlement"
	PhaseConsumptionShortfall PhaseKind = "consumption-shortfall"
	PhasePopulation           PhaseKind = "population"
	PhaseLandValueDecay       PhaseKind = "land-value-decay"
	PhaseFinance              PhaseKind = "finance"
)

// dailyPhaseOrder is the fixed phase set executed on every daily
// logistics tick (§8). v1 (draft-ahead, MOD-012) exposes exactly one
// named daily phase; this is the barrier hook the citizens/logistics
// modules (MOD-017/018) fill in later, including A2's amortised cold
// pass (1/30 of shards per daily tick on a fixed schedule) — see
// SCHEDULE below.
//
// UNEXPORTED AND FIXED-SIZE (SEC-005 fix). This used to be an exported
// []PhaseKind — a package-level mutable slice any importer could
// reorder/truncate/append to with a plain assignment, defeating the
// "never reordered at runtime" contract at the type level (GR#21). It
// is now a private array: nothing outside this file can name it, let
// alone mutate it, and internal readers (advanceOneDailyTick, validPhase
// below) range over it directly with zero allocation — the same
// zero-cost access the old exported slice had on the hot path, just not
// exported. External callers get DailyPhaseOrder(), a function that
// returns a fresh copy (see below) — allocates, but is never called from
// the per-tick path, only at boot/test time.
var dailyPhaseOrder = [...]PhaseKind{
	PhaseDailyTick,
}

// monthlyPhaseOrder is the fixed, documented order the monthly tick
// executes phases in (§3, AC-3, AC-16). This array's order IS the
// contract — never re-sliced, sorted, or otherwise reordered at
// runtime. See dailyPhaseOrder's doc comment above for why this is an
// unexported array rather than an exported slice (SEC-005).
//
// MIRRORED ELSEWHERE — read before changing this list. The F12 info
// panel keeps a literal copy of these six names in
// internal/ui/screens/debug/phase.go, because GR#20 forbids
// internal/ui from importing internal/engine, so it cannot reference
// this slice directly. Reordering, renaming, adding or removing a
// phase here therefore requires the same edit there. A drift test in
// internal/ui/screens/debug/determinism_test.go imports MonthlyPhaseOrder()
// (the sanctioned test-file exemption) and fails if the two diverge —
// so a mistake is caught, but by CI rather than by reading. That note
// exists so you find out now instead of then; see that file's doc
// comment for the full rationale.
var monthlyPhaseOrder = [...]PhaseKind{
	PhaseProduction,
	PhaseLogisticsSettlement,
	PhaseConsumptionShortfall,
	PhasePopulation,
	PhaseLandValueDecay,
	PhaseFinance,
}

// DailyPhaseOrder returns a fresh copy of the fixed daily phase order
// (SEC-005: callers may freely mutate the returned slice — append,
// reorder, truncate — without touching the Engine's actual pipeline
// order, since it is a copy of the unexported dailyPhaseOrder array, not
// a reference to it). Matches the defensive-copy pattern
// foundation.registry.List() already uses for the same reason
// (registry.go). Allocates — call this at boot/test time, never from a
// per-tick hot path (advanceOneDailyTick ranges over the unexported
// array directly, at zero cost).
func DailyPhaseOrder() []PhaseKind {
	out := make([]PhaseKind, len(dailyPhaseOrder))
	copy(out, dailyPhaseOrder[:])
	return out
}

// MonthlyPhaseOrder returns a fresh copy of the fixed, documented order
// the monthly tick executes phases in (§3, AC-3, AC-16) — see
// DailyPhaseOrder's doc comment for the mutation-safety and allocation
// argument, which applies identically here.
func MonthlyPhaseOrder() []PhaseKind {
	out := make([]PhaseKind, len(monthlyPhaseOrder))
	copy(out, monthlyPhaseOrder[:])
	return out
}

// validPhase reports whether kind is one of the fixed phases above.
// Called only at RegisterPhaseHook time (boot-time, not the tick path),
// so the linear scan over two small fixed arrays is fine. Reads the
// unexported arrays directly (no copy needed — nothing here mutates
// them).
func validPhase(kind PhaseKind) bool {
	for _, k := range dailyPhaseOrder {
		if k == kind {
			return true
		}
	}
	for _, k := range monthlyPhaseOrder {
		if k == kind {
			return true
		}
	}
	return false
}

// Effect is one cross-shard message a PhaseHook's RunShard emits during
// a phase, applied at that phase's barrier in canonical (shard,
// sequence) order via det.ApplyBarrier (§1.2 point 2, AC-6). Sequence
// is the emitting shard's own local emission order (e.g. an
// incrementing counter the hook bumps per message it emits within one
// RunShard call) — never a wall-clock or global counter. Payload is
// opaque to engine.core: module-defined data (a delivery, a tax
// transfer, ...), interpreted only by the hook's own ApplyEffect.
type Effect struct {
	Sequence int
	Payload  any
}

// PhaseHook is implemented by a module that wants to run shard-parallel
// work during a named phase (AC-3's "modules register handlers through
// a PhaseHook interface"). A hook is registered against exactly one
// PhaseKind via Engine.RegisterPhaseHook; the same module may register
// distinct PhaseHook values against multiple phases if it has work in
// more than one.
//
// Engine boots with any mix of hooks, including zero (the
// walking-skeleton property: empty phases are legal, per M0-ENG §2's
// module-stubbing discipline and this item's "Out of scope" section —
// real simulation content is not this package's job).
type PhaseHook interface {
	// RunShard computes this hook's shard-local work for shard during
	// the phase it is registered against, returning zero or more
	// cross-shard Effects to apply at the barrier, or a non-nil error
	// if the shard's work failed. RunShard must touch only shard-local
	// scratch — no state shared with any other shard — this is the
	// whole determinism contract det.RunPhase's worker-count invariance
	// depends on (§1.2 point 2).
	//
	// Called from a POOL-SIM worker goroutine; may be called
	// concurrently for different shards. Implementations must be safe
	// for that.
	RunShard(shard int) ([]Effect, error)

	// ApplyEffect applies one Effect at the phase barrier. Called
	// single-goroutine, strictly in canonical (shard, sequence) order,
	// after every shard's RunShard has returned for this phase (AC-6).
	ApplyEffect(Effect)
}

// SingleShardHook is an OPTIONAL interface a PhaseHook may implement to
// declare that ALL of its real work happens on shard 0 — i.e. RunShard
// for shards 1..255 unconditionally returns (nil, nil), and ApplyEffect
// only ever needs to process effects RunShard(0) produced, applied in
// that call's own Sequence order.
//
// This is BUG-269's fast path (see runPhaseForHookFast below): a hook
// that opts in skips det.RunPhase's 256-shard goroutine-pool dispatch
// entirely and runs inline on the calling goroutine instead, which is
// how the report traced 65.8% of BUG-268's regression back to paying a
// full pool dispatch for hooks that only ever touch shard 0.
//
// SingleShard() must be a compile-time-constant promise (return a
// literal true) — never state- or config-dependent — because
// runPhaseForHook decides which path to take once per hook per phase
// call, with no per-shard fallback. A hook that implements this
// interface and returns true but DOES do real work on shard != 0
// silently loses that work: it is checked by WithSingleShardAssert's
// dev-mode safety net and by this package's own SingleShardHook
// conformance tests (phase_test.go), never at production runtime.
type SingleShardHook interface {
	PhaseHook
	// SingleShard reports whether this hook only ever produces
	// shard-local work and Effects for shard 0. See the interface's
	// doc comment above — this must always return the same literal
	// value.
	SingleShard() bool
}

// hookError pairs a RunShard failure with the shard that produced it,
// so runPhaseForHook can pick a deterministic (lowest-shard) error to
// surface regardless of which worker goroutine happened to finish
// first (AC-10, AC-12: no reliance on goroutine scheduling order).
type hookError struct {
	shard int
	err   error
}

// runPhaseForHook runs one PhaseHook's shard-parallel work for one
// phase via det.RunPhase (§1.2's fixed-256-shard, shard-order-merge,
// barrier-ordered-message primitive — see foundation/det/phase.go).
// hook.ApplyEffect is invoked from det.RunPhase's own single-goroutine
// barrier-application step, so ApplyEffect calls here are already
// serialized and canonically ordered without runPhaseForHook needing
// its own lock around them.
func (e *Engine) runPhaseForHook(correlationID string, hook PhaseHook) error {
	// BUG-269 fast path: a hook that has opted into SingleShardHook
	// promises shards 1..255 never do real work, so the full
	// det.RunPhase 256-shard goroutine-pool dispatch below is provably
	// unnecessary for it — see runPhaseForHookFast's doc comment for the
	// determinism argument.
	if ssh, ok := hook.(SingleShardHook); ok && ssh.SingleShard() {
		return e.runPhaseForHookFast(correlationID, hook)
	}

	var errMu sync.Mutex
	var hookErrs []hookError

	shardFn := func(shard int) (struct{}, []det.Message[Effect]) {
		effects, err := hook.RunShard(shard)
		if err != nil {
			errMu.Lock()
			hookErrs = append(hookErrs, hookError{shard: shard, err: err})
			errMu.Unlock()
		}
		if len(effects) == 0 {
			return struct{}{}, nil
		}
		msgs := make([]det.Message[Effect], len(effects))
		for i, eff := range effects {
			msgs[i] = det.Message[Effect]{Shard: shard, Sequence: eff.Sequence, Payload: eff}
		}
		return struct{}{}, msgs
	}

	combine := func(acc struct{}, _ det.ShardResult[struct{}]) struct{} { return acc }
	applyMsg := func(eff Effect) { hook.ApplyEffect(eff) }

	if _, err := det.RunPhase[struct{}, Effect](correlationID, e.poolSize, struct{}{}, shardFn, combine, applyMsg); err != nil {
		// A merge-level failure (e.g. det.ErrShardMergeIncomplete) is
		// already a registry-sourced *errs.E from the det package
		// (foundation/det/shard.go) — propagate unchanged.
		return err
	}

	if len(hookErrs) == 0 {
		return nil
	}
	sort.Slice(hookErrs, func(i, j int) bool { return hookErrs[i].shard < hookErrs[j].shard })
	first := hookErrs[0]
	return errs.Wrap(ErrPhaseHookFailed, correlationID, first.err, map[string]any{
		"shard": first.shard,
	})
}

// runPhaseForHookFast is BUG-269's fast path for a hook that has opted
// into SingleShardHook: it calls RunShard exactly once, for shard 0,
// inline on the calling goroutine — no worker pool, no channel, no
// det.RunPhase — then applies the resulting Effects directly, in the
// same order det.ApplyBarrier would have.
//
// # Why this is byte-identical to the pooled path
//
// det.RunPhase (foundation/det/phase.go) does two things with a hook's
// per-shard output that matter here:
//
//  1. Merges the 256 per-shard T values via det.MergeInOrder. For every
//     caller in this file T is struct{} (see the combine func below in
//     the pooled branch) and runPhaseForHook discards the merged value
//     outright (`if _, err := det.RunPhase[...]`) — it exists only to
//     assert exactly 256 shards ran, an invariant that is unconditionally
//     true for det.RunPhase's own fixed shard-queue loop and carries no
//     information for this hook. Skipping it changes nothing observable.
//  2. Applies every emitted det.Message[Effect] via det.ApplyBarrier, in
//     canonical (Shard, Sequence) ascending order (barrier.go). A
//     SingleShardHook's promise is that shard 0 is the ONLY shard that
//     ever emits an Effect, so every message's Shard component is the
//     same constant value — the (Shard, Sequence) sort degenerates to a
//     Sequence-only sort, which is exactly what sorting hook.RunShard(0)'s
//     own returned []Effect slice by Sequence produces. Applying that
//     sorted slice via hook.ApplyEffect, in order, is therefore the same
//     sequence of ApplyEffect calls det.RunPhase would have produced.
//
// If a hook's SingleShard() promise is false — real work happens on a
// shard other than 0 — this silently drops that work. WithSingleShardAssert
// (engine.go) is the opt-in dev-mode check that catches a lying hook by
// paying for the other 255 shards anyway and asserting they are empty;
// phase_test.go additionally proves the failure is observable at all via
// a hook that opts in and then breaks its own promise.
func (e *Engine) runPhaseForHookFast(correlationID string, hook PhaseHook) error {
	effects, err := hook.RunShard(0)
	if err != nil {
		return errs.Wrap(ErrPhaseHookFailed, correlationID, err, map[string]any{
			"shard": 0,
		})
	}

	if e.assertSingleShard {
		for shard := 1; shard < det.NumShards; shard++ {
			extra, sErr := hook.RunShard(shard)
			if sErr != nil || len(extra) != 0 {
				panic(fmt.Sprintf(
					"BUG-269 SingleShardHook promise broken: shard %d returned effects=%d err=%v (correlationID=%s)",
					shard, len(extra), sErr, correlationID,
				))
			}
		}
	}

	if len(effects) > 1 {
		sort.Slice(effects, func(i, j int) bool { return effects[i].Sequence < effects[j].Sequence })
	}
	for _, eff := range effects {
		hook.ApplyEffect(eff)
	}
	return nil
}

// runPhase runs every hook registered against kind, in registration
// order (e.hooks[kind] is a slice, never a map), invoking the optional
// PhaseObserver first. Returns the first hook error encountered,
// aborting the remaining hooks for this phase (AC-10) — the caller
// (advanceOneDailyTick) then aborts the remaining phases for this tick.
//
// This reads e.hooks[kind] WITHOUT taking e.mu (SEC-003). That is safe
// only because AdvanceTicks calls e.seal() before any phase ever runs,
// and RegisterPhaseHook refuses to mutate e.hooks once sealed — see the
// Engine.sealed field's doc comment (engine.go) for the full argument.
// Do not "fix" this by adding a lock here without first reading that
// comment: a lock taken once per phase per tick would cost contention
// this package's 0-alloc/low-overhead steady-state benchmark (AC-9) was
// written to catch regressing.
func (e *Engine) runPhase(correlationID string, kind PhaseKind) error {
	if e.observer != nil {
		e.observer(kind, e.clock.Tick(), e.clock.Month())
	}
	for _, hook := range e.hooks[kind] {
		if err := e.runPhaseForHook(correlationID, hook); err != nil {
			return err
		}
	}
	return nil
}

// PhaseObserver is called once per phase, immediately before that
// phase's hooks run, in the fixed pipeline order. It exists for
// telemetry/tests (AC-3's "observable via an instrumented/mock phase
// sequence") — the phase pipeline's own correctness never depends on
// an observer being set or on anything it does.
type PhaseObserver func(kind PhaseKind, tick int64, month int64)
