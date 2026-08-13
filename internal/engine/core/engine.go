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

// Speed8xGate is the injected seam handleSetSpeed (commands.go)
// consults before accepting Speed8xDebug (BUG-009). engine.core does
// not know or care who backs this — feat.debugmode's
// *debug.State.AllowSpeed8x method value satisfies this exact
// signature today, but engine.core imports nothing from
// internal/engine/debug to make that true (GR#20: modules consume each
// other only via registered interfaces, never a direct import across
// the engine.core <-> feat.debugmode seam). The caller that owns both
// modules (cmd/metropolis's bootstrap) is where the two sides actually
// meet: it constructs a *debug.State and passes
// WithSpeed8xGate(state.AllowSpeed8x) when building the Engine.
//
// A nil correlationID string is never passed; gate implementations are
// expected to thread it straight into any registry-sourced error they
// construct, the same way every other rejection in this package does.
type Speed8xGate func(correlationID string) error

// WithSpeed8xGate installs the gate handleSetSpeed consults before
// accepting Speed8xDebug. Unset (the default an Engine boots with when
// no caller supplies this option — e.g. a bare NewEngine() in a test,
// or a release build that forgot to wire feat.debugmode) means
// deny-by-default: Speed8xDebug is refused, never silently permitted,
// until a caller explicitly wires a gate. See checkSpeed8xAllowed in
// commands.go for where that default is enforced.
func WithSpeed8xGate(gate Speed8xGate) Option {
	return func(e *Engine) { e.speed8xGate = gate }
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
	// mu guards every field below, hooks and sealed included. mu is only
	// ever held for the cost of copying a handful of int64/bool fields,
	// appending to a hook slice, or flipping sealed — never across a
	// phase pipeline run or a Snapshot's marshalling, which is exactly
	// what keeps T-PERSIST (persist.go) and the tick path from blocking
	// each other (AC-8).
	mu sync.Mutex

	// sealed is flipped true under mu the first time AdvanceTicks runs
	// (see seal()) and never flipped back. It is what makes runPhase's
	// per-phase, per-tick read of e.hooks[kind] (phase.go) safe WITHOUT
	// taking mu on the hot path (SEC-003 fix):
	//
	//   - Every RegisterPhaseHook call takes mu, checks sealed, and only
	//     mutates e.hooks if it is still false — so once sealed is true,
	//     e.hooks can never be mutated again by any goroutine, by
	//     construction, not by convention.
	//   - AdvanceTicks calls seal() (which takes mu) before its tick loop
	//     runs a single phase. Because seal() and RegisterPhaseHook both
	//     serialize through the same mu, any RegisterPhaseHook call is
	//     either fully complete-and-visible before seal() acquires the
	//     lock, or observes sealed==true and is rejected — there is no
	//     interleaving where a write is still in flight once a read
	//     starts. That is what lets runPhase skip locking entirely: by
	//     the time it ever reads e.hooks, the map is provably immutable
	//     for the remaining lifetime of the Engine.
	//
	// This trades a single mu.Lock/Unlock per AdvanceTicks CALL (not per
	// tick, not per phase) for removing all locking from the per-phase
	// hot path — see seal()'s doc comment for the cost accounting against
	// AC-9's 0-alloc steady-state benchmark.
	//
	// API-contract note (ASM-guarded — see the dispatch report): before
	// this fix, "RegisterPhaseHook after AdvanceTicks has run" was an
	// undefined, racy, sometimes-fatal call sequence. After this fix it
	// is a defined, rejected one (ErrEngineSealed) — RegisterPhaseHook's
	// doc comment already said hooks must be registered before the first
	// AdvanceTicks call; sealing is that same contract enforced instead
	// of merely documented.
	sealed bool

	// self holds the address NewEngine gave this Engine at construction
	// (self.Store(e), set once, at the end of NewEngine, never stored to
	// again). It is Metropolis's answer to SEC-014/SEC-016: `e2 := *e`
	// is legal, unsafe-free, reflect-free Go — every field of Engine is
	// unexported, but that does not stop a caller from dereferencing the
	// *Engine NewEngine returned and copying the struct value. mu and
	// sealed are plain values, so the copy e2 gets its OWN mu and its
	// OWN sealed flag, independent of e's — but e2.hooks (a map, a
	// reference type) still ALIASES e.hooks, and e2.self still points at
	// the ORIGINAL e (copied by value, unchanged). That is exactly the
	// signal a copy cannot erase: checkNotCopied compares the receiver's
	// own address against self, and a copy's address can never equal
	// the original's.
	//
	// TYPE, NOT JUST *Engine (SEC-016 — Destructive-2, round 2). SEC-014
	// shipped this as a plain `self *Engine` field, checked only AFTER
	// e.mu.Lock() in RegisterPhaseHook/seal(). That closed SEC-003's
	// crash but opened something worse: a struct copy taken at a moment
	// when the ORIGINAL's mu happened to be locked inherits mu's bytes
	// byte-for-byte, including the "currently locked" bit — but nobody
	// will ever call Unlock() against the COPY's address, only the
	// original's. The copy's own next Lock() call then parks in
	// runtime_SemacquireMutex FOREVER: no crash, no error, no
	// correlation ID, just a leaked goroutine — arguably worse than the
	// crash it replaced, because a crash is loud and this is silent.
	// Reproduced live (TestSEC016_PoC_CopyDuringLockCanHangForever, run
	// against the code exactly as it stood before this field's type
	// changed) with a goroutine dump showing multiple goroutines parked
	// at identical RegisterPhaseHook -> Mutex.Lock -> lockSlow ->
	// runtime_SemacquireMutex stacks.
	//
	// The fix is ordering, not mechanism: the identity check must run
	// BEFORE mu is touched at all, so a copy is rejected without ever
	// acquiring its own (possibly pre-locked) mutex. But a plain,
	// unsynchronized `e.self` field read done lock-free, concurrently
	// with a struct copy that is itself concurrently reading e's memory
	// while another goroutine mutates OTHER fields of e (mu, sealed)
	// under lock, has no defined synchronization in the Go memory model
	// — even though self's own bytes are never rewritten after
	// construction, the copy operation touches the whole struct's memory
	// as one operation, and only a properly synchronized read gives that
	// operation a defined result rather than an implementation-observed
	// one. atomic.Pointer[Engine] makes self's own value load/store
	// well-defined regardless of what else is concurrently happening to
	// neighbouring fields: Store happens exactly once, in NewEngine,
	// before any goroutine can have a reference to e to race against;
	// every subsequent Load is a single lock-free atomic read requiring
	// nothing else — not mu, not sealed, nothing that a copy could have
	// captured in a bad state.
	//
	// checkNotCopied is called from both RegisterPhaseHook and seal()
	// TWICE: once before e.mu.Lock() (the load-bearing check — this is
	// what stops a copy from ever touching its own mu, closing SEC-016),
	// and once again after e.mu.Lock() as defence in depth (cheap — one
	// more atomic load — and guards against a future code path that
	// reaches mu without going through the pre-lock check first). Either
	// check alone is sufficient once identity itself is race-safe; both
	// together cost one extra Load, not one extra lock.
	//
	// Cost: two atomic.Pointer.Load calls per RegisterPhaseHook call
	// (already boot-time-only) and two per AdvanceTicks CALL (inside
	// seal(), which already takes mu once for the sealed flag) — no
	// allocation, and not on the per-tick/per-phase hot path (confirmed
	// by re-running BenchmarkAdvanceTicks_SteadyState_ZeroModules after
	// this change — see the dispatch report; still 0 allocs/op).
	self atomic.Pointer[Engine]

	clock     Clock
	worldSeed uint64
	poolSize  int
	observer  PhaseObserver

	// speed8xGate is BUG-009's injected debug-speed gate (see
	// Speed8xGate's doc comment above). nil until WithSpeed8xGate wires
	// it — nil is read as "no gate configured, deny Speed8xDebug"
	// (ErrSpeed8xGateNotConfigured, MET-E015) by checkSpeed8xAllowed
	// (commands.go), never as "no gate configured, allow it".
	speed8xGate Speed8xGate

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
	// Stored exactly once, here, before e is returned to any caller —
	// no goroutine can have a reference to e to race this Store against
	// (SEC-016; see self's doc comment).
	e.self.Store(e)
	return e
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Engine value (SEC-014/SEC-016). Deliberately lock-free — a
// single atomic.Pointer.Load, requiring nothing else, not e.mu, not any
// other field — so it is safe and correct to call BEFORE e.mu is ever
// touched. That ordering is the whole point (SEC-016): a struct copy's
// mu can be byte-for-byte "currently locked" if the copy was taken
// while the original's mu was held, and acquiring (even just
// attempting) a copy's own mu in that state can block forever, since
// nothing will ever Unlock() that specific copy's address. Rejecting
// the copy via this check, before Lock() is ever called, means that
// hang path is never reached at all.
//
// A nil e.self.Load() (an Engine constructed as a bare
// `Engine{}`/`new(Engine)` rather than via NewEngine, so self was never
// stored) is treated the same as a mismatch and rejected the same way —
// every documented construction path is NewEngine, so an unset self is
// itself a misuse this same error correctly names, and rejecting it
// here also means such an Engine's zero-value nil hooks map is never
// reached either.
func (e *Engine) checkNotCopied(correlationID string, ctx map[string]any) error {
	if e.self.Load() != e {
		return errs.New(ErrEngineCopied, correlationID, ctx)
	}
	return nil
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
//
// SEC-018: identity-checked BEFORE mu is touched, same as
// RegisterPhaseHook/seal (SEC-016) — one of eight e.mu.Lock() sites in
// this package's non-test files, all of which now share this ordering.
// A copy's mu can read as permanently "locked" if it was captured while
// the original's mu was held; without this check, Clock() on such a
// copy would hang forever exactly like RegisterPhaseHook did pre-SEC-016
// (Tester-1 reproduced this live: 3,000 copies racing a lock hammer,
// then Clock() on each — only 1,786 returned, the rest wedged
// permanently on this exact call).
func (e *Engine) Clock() (Clock, error) {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return Clock{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock, nil
}

// RegisterPhaseHook wires hook into kind's phase-pipeline slot. Must be
// called before AdvanceTicks is first invoked for kind's phase to run
// it deterministically from tick 1 — there is no "hot" re-registration
// API mid-run, matching foundation.registry's "fresh Registry expected
// per boot/test" convention (see registry.go's Register doc comment).
// This is now enforced, not just documented (SEC-003): once the Engine
// has sealed (see the sealed field's doc comment), a late
// RegisterPhaseHook call is rejected with ErrEngineSealed rather than
// racing runPhase's unsynchronized read of e.hooks. It also rejects a
// call on a struct-copied Engine value with ErrEngineCopied (SEC-014/
// SEC-016 — see the self field's doc comment) BEFORE e.mu is ever
// touched, let alone before e.hooks is — see the identity-check-ordering
// note below.
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
	// SEC-016: identity check BEFORE mu is touched at all. A struct
	// copy's mu may already read as "locked" (copied mid-Lock from the
	// original) — calling e.mu.Lock() on such a copy before rejecting it
	// can block forever, since nothing will ever Unlock() that specific
	// copy's address. checkNotCopied needs only a lock-free atomic load,
	// so it can run, and reject, before mu is acquired.
	if err := e.checkNotCopied(errs.NewCorrelationID(), map[string]any{"phase": string(kind)}); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Defence-in-depth re-check under the lock: cheap (one more atomic
	// load, no allocation) and guards against a future refactor adding
	// another mu-acquiring path ahead of the pre-lock check without
	// threading this check through it too. self is never reassigned
	// after NewEngine, so this is not expected to ever disagree with the
	// pre-lock result on today's code paths — but it is not the ONLY
	// line of defence, which is the property that actually matters
	// (SEC-016's finding was specifically about relying on a single,
	// too-late check).
	if err := e.checkNotCopied(errs.NewCorrelationID(), map[string]any{"phase": string(kind)}); err != nil {
		return err
	}
	if e.sealed {
		return errs.New(ErrEngineSealed, errs.NewCorrelationID(), map[string]any{"phase": string(kind)})
	}
	e.hooks[kind] = append(e.hooks[kind], hook)
	return nil
}

// seal permanently closes hook registration. Called at the top of
// AdvanceTicks, before any phase runs. Idempotent (a second/subsequent
// AdvanceTicks call still runs the same two cheap checks but only
// re-confirms sealed is already true) — see the sealed field's doc
// comment for why removing all locking from the per-phase hot path
// depends on this running once per AdvanceTicks call, and the self
// field's doc comment for why the identity check specifically must run
// BEFORE mu (SEC-016).
//
// Cost: one lock-free atomic.Pointer.Load (identity, pre-lock) + one
// mutex acquisition (a command-level cost, not a per-tick or per-phase
// one) + one more atomic.Pointer.Load (identity, post-lock, defence in
// depth) per AdvanceTicks CALL — for AC-9's steady-state benchmark,
// which calls AdvanceTicks(n=1) per iteration, that is two lock-free
// loads and one uncontended Lock/Unlock pair per iteration, none of
// which allocate, so this does not regress the 0-alloc property
// (confirmed by re-running BenchmarkAdvanceTicks_SteadyState_
// ZeroModules after this change — see the dispatch report; still 0
// allocs/op).
//
// Returns ErrEngineCopied (SEC-014/SEC-016) if called on a struct-copied
// Engine value — rejected here BEFORE mu is touched at all (the
// load-bearing fix for SEC-016: reaching e.mu.Lock() on a copy whose mu
// bytes were captured mid-lock on the original can hang forever, so the
// check that would reject the copy must never require acquiring that
// same mu to run) and, redundantly, again immediately after mu is
// acquired (defence in depth). Either way, this always resolves before
// sealed is set and before the caller's AdvanceTicks loop can ever reach
// runPhase's unlocked read of e.hooks.
func (e *Engine) seal(correlationID string) error {
	if err := e.checkNotCopied(correlationID, nil); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkNotCopied(correlationID, nil); err != nil {
		return err
	}
	e.sealed = true
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
//
// Also returns ErrEngineCopied (SEC-014) and runs zero phases if called
// on a struct-copied Engine value — see seal()'s doc comment for why
// this check happens before any phase ever runs.
func (e *Engine) AdvanceTicks(correlationID string, n int64) error {
	if n <= 0 || n > MaxAdvanceTicksPerCall {
		return errs.New(ErrInvalidAdvanceTicks, correlationID, map[string]any{
			"n": n, "max": MaxAdvanceTicksPerCall,
		})
	}
	if err := e.seal(correlationID); err != nil {
		return err
	}
	for i := int64(0); i < n; i++ {
		if err := e.advanceOneDailyTick(correlationID); err != nil {
			return err
		}
	}
	return nil
}

// advanceOneDailyTick runs dailyPhaseOrder's phases, commits the tick
// (clock advance + tickCounter bump), and — if that tick completed a
// calendar month — runs monthlyPhaseOrder's phases in fixed order
// (AC-3). Ranges over the unexported arrays directly (zero allocation,
// SEC-005) rather than calling DailyPhaseOrder()/MonthlyPhaseOrder(),
// which allocate a fresh copy on every call and exist for external
// callers only.
//
// SEC-018 enumeration note: this is the 8th of this package's eight
// e.mu.Lock() sites (see engine.go/commands.go/persist.go), and it is
// the one deliberately left WITHOUT its own checkNotCopied call.
// advanceOneDailyTick is unexported with exactly one call site
// (AdvanceTicks' loop), which only ever reaches it after seal() has
// already rejected a copy — and self never changes for an Engine's
// lifetime, so that result cannot go stale mid-loop. Adding a redundant
// check here would cost one more atomic load PER TICK (not per
// AdvanceTicks call) for zero additional safety, unlike the
// RegisterPhaseHook/seal double-check, which guards a call path that
// genuinely has more than one way in. See ASM-* in the dispatch report
// for this judgement call, logged as I made it (v1.7.2).
func (e *Engine) advanceOneDailyTick(correlationID string) error {
	for _, phase := range dailyPhaseOrder {
		if err := e.runPhase(correlationID, phase); err != nil {
			return err
		}
	}

	e.mu.Lock()
	monthCompleted := e.clock.advanceOneDay()
	e.mu.Unlock()
	e.tickCounter.Add(1)

	if monthCompleted {
		for _, phase := range monthlyPhaseOrder {
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
