package core

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// MaxAdvanceTicksPerCall bounds a single AdvanceTicks command (AC-11:
// "absurdly large single-call count" is rejected, never silently
// clamped). One in-game year is 12*30 = 360 daily ticks; 10 in-game
// years per call is already generous for a headless/replay driver, so
// this leaves a wide margin before it could plausibly be a caller
// mistake (e.g. N confused with a tick-rate or a monthly count) rather
// than a deliberate large batch.
const MaxAdvanceTicksPerCall int64 = 10 * 12 * DailyTicksPerMonth

// poolFloor is the minimum POOL-SIM size on any machine (AC-4: "a floor
// preventing zero or negative pool size on low-core machines").
const poolFloor = 1

// reservedCores is M0-ENG §1.1's "leave 2 for UI+OS".
const reservedCores = 2

// poolSizeForCPUs computes POOL-SIM's worker count for a machine
// reporting numCPU logical cores, applying the reserved-cores and floor
// rules. Split out from defaultPoolSize so AC-4's floor behaviour is
// testable without mocking runtime.NumCPU.
func poolSizeForCPUs(numCPU int) int {
	n := numCPU - reservedCores
	if n < poolFloor {
		return poolFloor
	}
	return n
}

// defaultPoolSize is POOL-SIM's default sizing: runtime.NumCPU()-2,
// floored at 1 (AC-4).
func defaultPoolSize() int {
	return poolSizeForCPUs(runtime.NumCPU())
}

// NumShards returns the fixed shard count the phase pipeline partitions
// the world into (AC-5). It is always det.NumShards (256) — engine.core
// does not define its own shard count, it reuses foundation.det's.
func NumShards() int { return det.NumShards }

// Option customizes a new Engine. Unset options take the defaults
// documented on each With* function.
type Option func(*Engine)

// WithWorldSeed sets the world seed carried from construction (§1.2
// point 3) that every module's det.Stream draws would key off, once
// modules go real. Defaults to 0 (a fully valid seed — see
// det.NewStream's doc comment on zero seeds being well-defined).
func WithWorldSeed(seed uint64) Option {
	return func(e *Engine) { e.worldSeed = seed }
}

// WithPoolSize overrides POOL-SIM's worker count, bypassing
// defaultPoolSize's runtime.NumCPU()-2 sizing. Intended for tests
// (AC-15's same-seed-different-pool-size determinism smoke test) and
// for a future config override; floored at 1 the same way
// defaultPoolSize is (det.RunPhase itself also treats workers<1 as 1,
// so this is belt-and-braces, not load-bearing).
func WithPoolSize(n int) Option {
	return func(e *Engine) {
		if n < poolFloor {
			n = poolFloor
		}
		e.poolSize = n
	}
}

// WithSecondsPerMonthAt1x overrides the clock's real-time pacing
// constant (see clock.go's DefaultSecondsPerMonthAt1x doc comment for
// why this is an Option rather than a hardcoded value).
func WithSecondsPerMonthAt1x(seconds int64) Option {
	return func(e *Engine) { e.clock = NewClock(seconds) }
}

// WithPhaseObserver installs a PhaseObserver (see phase.go).
func WithPhaseObserver(obs PhaseObserver) Option {
	return func(e *Engine) { e.observer = obs }
}

// WithRegistry installs a pre-constructed module registry instead of
// the default empty one NewEngine creates. Mainly for tests that want
// to register modules before wiring an Engine, or that want a shared
// Registry visible to code outside the Engine.
func WithRegistry(r *registry.Registry) Option {
	return func(e *Engine) { e.registry = r }
}

// Engine is the tick orchestrator (T-ENGINE, M0-ENG §1.1): it owns the
// simulation clock, the module registry instance, and the fixed phase
// pipeline. See doc.go for the package-level contract this type
// implements.
//
// Engine boots with any mix of registered PhaseHooks, including zero —
// the walking-skeleton property. Real simulation content is out of
// scope for this package (see the acceptance doc's "Out of scope");
// Engine only orchestrates.
type Engine struct {
	// mu guards every field below except hooks' *contents* during a
	// phase run (hooks itself — which phases have which hook slices
	// registered — is only ever mutated at boot, before any tick runs,
	// so mu covers it too; see RegisterPhaseHook). mu is only ever held
	// for the cost of copying a handful of int64/bool fields — never
	// across a phase pipeline run or a Snapshot's marshalling, which is
	// exactly what keeps T-PERSIST (persist.go) and the tick path from
	// blocking each other (AC-8).
	mu sync.Mutex

	clock     Clock
	worldSeed uint64
	poolSize  int
	observer  PhaseObserver

	registry *registry.Registry
	hooks    map[PhaseKind][]PhaseHook

	subs *SubscriptionServer

	// deltaSignal wakes the subscription pump goroutine (commands.go's
	// StartSubscriptionPump / signalSubscriptionPump). Size 1 and
	// non-blocking-send-with-drop: multiple signals raised before the
	// pump wakes up coalesce into a single recompute, which is correct
	// for a snapshot-style view (engine.status) rather than an event
	// log — the pump always reads the LATEST state when it wakes, not
	// a queued history of every signal.
	deltaSignal chan struct{}

	// tickCounter is an atomic, lock-free observation point: it counts
	// completed daily ticks, independent of mu, so a concurrency test
	// (or future telemetry) can observe tick progress without taking
	// the same lock Snapshot briefly holds (persist_test.go's
	// concurrency assertion reads this).
	tickCounter atomic.Uint64
}

// NewEngine constructs an Engine. With no options, it boots with:
// worldSeed 0, POOL-SIM sized runtime.NumCPU()-2 (floored at 1), the
// master doc §3 default pacing (480s/month at 1x), an empty module
// registry, and zero registered PhaseHooks (the walking-skeleton
// property, M0-ENG §2).
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		clock:       NewClock(DefaultSecondsPerMonthAt1x),
		poolSize:    defaultPoolSize(),
		hooks:       make(map[PhaseKind][]PhaseHook),
		deltaSignal: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.registry == nil {
		e.registry = registry.NewRegistry()
	}
	if e.subs == nil {
		e.subs = NewSubscriptionServer()
	}
	return e
}

// Registry returns the Engine's module registry instance (T-ENGINE
// "owns ... the module registry instance", per this item's deliverable
// list). Modules register themselves here (Name/Version/Health, per
// foundation.registry's Module interface) independently of
// RegisterPhaseHook, which wires their tick-path handlers — the two
// registrations are deliberately decoupled (see doc.go's "decisions"
// note): not every registered module has phase-pipeline work, and a
// PhaseHook does not have to come from a registry.Module.
func (e *Engine) Registry() *registry.Registry { return e.registry }

// WorldSeed returns the seed carried from construction (§1.2 point 3).
func (e *Engine) WorldSeed() uint64 { return e.worldSeed }

// PoolSize returns POOL-SIM's configured worker count.
func (e *Engine) PoolSize() int { return e.poolSize }

// Clock returns a snapshot (copy) of the Engine's current clock state.
// Safe for concurrent use; briefly takes mu.
func (e *Engine) Clock() Clock {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// RegisterPhaseHook wires hook into kind's phase-pipeline slot. Must be
// called before AdvanceTicks is first invoked for kind's phase to run
// it deterministically from tick 1 — there is no "hot" re-registration
// API mid-run, matching foundation.registry's "fresh Registry expected
// per boot/test" convention (see registry.go's Register doc comment).
//
// Hooks accumulate in registration order (e.hooks[kind] is a slice) —
// this order, not any later re-sort, is what a phase's hooks run in
// when there is more than one hook per phase.
func (e *Engine) RegisterPhaseHook(kind PhaseKind, hook PhaseHook) error {
	if hook == nil {
		return errs.New(ErrNilPhaseHook, errs.NewCorrelationID(), map[string]any{"phase": string(kind)})
	}
	if !validPhase(kind) {
		return errs.New(ErrUnknownPhase, errs.NewCorrelationID(), map[string]any{"phase": string(kind)})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooks[kind] = append(e.hooks[kind], hook)
	return nil
}

// AdvanceTicks drives the simulation forward by n daily logistics
// ticks, running the daily phase set for each, and the fixed monthly
// phase pipeline every time a tick completes a calendar month (every
// 30th daily tick, per DailyTicksPerMonth) — AC-1's two-layer cadence.
//
// n must be positive and at most MaxAdvanceTicksPerCall (AC-11); an
// invalid n is rejected with a registry-sourced error and does not
// advance the clock at all (not even partially).
//
// A phase hook error aborts the current tick's remaining phases (and
// therefore the whole AdvanceTicks call, since later ticks would run
// against a tick whose phases did not fully commit) and is returned
// unchanged (AC-10) — the caller (commands.go's HandleCommand) is
// responsible for turning it into a rejected CommandResult.
func (e *Engine) AdvanceTicks(correlationID string, n int64) error {
	if n <= 0 || n > MaxAdvanceTicksPerCall {
		return errs.New(ErrInvalidAdvanceTicks, correlationID, map[string]any{
			"n": n, "max": MaxAdvanceTicksPerCall,
		})
	}
	for i := int64(0); i < n; i++ {
		if err := e.advanceOneDailyTick(correlationID); err != nil {
			return err
		}
	}
	return nil
}

// advanceOneDailyTick runs DailyPhaseOrder's phases, commits the tick
// (clock advance + tickCounter bump), and — if that tick completed a
// calendar month — runs MonthlyPhaseOrder's phases in fixed order
// (AC-3).
func (e *Engine) advanceOneDailyTick(correlationID string) error {
	for _, phase := range DailyPhaseOrder {
		if err := e.runPhase(correlationID, phase); err != nil {
			return err
		}
	}

	e.mu.Lock()
	monthCompleted := e.clock.advanceOneDay()
	e.mu.Unlock()
	e.tickCounter.Add(1)

	if monthCompleted {
		for _, phase := range MonthlyPhaseOrder {
			if err := e.runPhase(correlationID, phase); err != nil {
				return err
			}
		}
	}
	return nil
}

// TicksCompleted returns the number of daily ticks committed so far, as
// an atomic counter independent of mu (see the field doc on
// Engine.tickCounter).
func (e *Engine) TicksCompleted() uint64 { return e.tickCounter.Load() }
