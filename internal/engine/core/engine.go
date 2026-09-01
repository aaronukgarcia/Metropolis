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
//
// BUG-303: NewClock now rejects a <= 0 (or, per its own comment, a
// future non-finite) seconds value with ErrInvalidPacingConstant
// (MET-E020) instead of silently constructing a garbage-pacing Clock.
// Option is `func(*Engine)` (no error return) across every other Option
// in this file, so this deliberately does NOT change that shared shape
// for one caller's sake; instead a rejected value is logged loudly
// (GR#1 — never silently swallowed) and e.clock is left as whatever
// NewEngine already set it to (its own DefaultSecondsPerMonthAt1x
// construction, which is always valid), rather than replaced with a
// zero-value Clock that would then silently report 0 pacing everywhere.
func WithSecondsPerMonthAt1x(seconds int64) Option {
	return func(e *Engine) {
		c, err := NewClock(seconds)
		if err != nil {
			_ = errs.Wrap(ErrInvalidPacingConstant, errs.NewCorrelationID(), err, map[string]any{"seconds": seconds})
			return
		}
		e.clock = c
	}
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

// WithCommandJournaler installs the seam accept() (commands.go) calls to
// record every ACCEPTED command into the replay journal — Aaron's
// engine-owns-journal DD (2026-08-31, FEAT-1972079852 inc3): "commands over
// the protocol are journaled Go-side (harness.replay estate)", not by the TS
// console. See CommandJournaler's doc comment (commands.go) for why
// engine.core defines its own minimal interface rather than importing
// internal/harness/replay's concrete Recorder type directly (mirrors
// DeltaSink's decoupling shape). Unset (nil, the default — e.g. a bare
// NewEngine() in most of this package's own tests) means no journaling:
// accept() only calls into e.journaler when one is configured, matching
// WithPhaseObserver's optional-hook shape (nil == no-op) rather than
// gameplayHandler/speed8xGate's deny-by-default shape.
func WithCommandJournaler(j CommandJournaler) Option {
	return func(e *Engine) { e.journaler = j }
}

// WithRegistry installs a pre-constructed module registry instead of
// the default empty one NewEngine creates. Mainly for tests that want
// to register modules before wiring an Engine, or that want a shared
// Registry visible to code outside the Engine.
func WithRegistry(r *registry.Registry) Option {
	return func(e *Engine) { e.registry = r }
}

// WithSingleShardAssert enables a DEV-MODE-ONLY safety net for BUG-269's
// SingleShardHook fast path (see phase.go's runPhaseForHookFast): when
// true, every fast-path hook additionally has RunShard called for every
// shard in [1, det.NumShards) — the same 255 calls the pooled path would
// have made — and panics if any of them return a non-nil error or any
// Effect, proving the hook's SingleShard() promise actually holds.
//
// This intentionally pays the full per-shard cost the fast path exists
// to avoid, so it must NEVER be enabled in production: defaults to
// false, and is meant for tests (or an explicit local debug run) that
// want the extra assurance that a hook opting into SingleShardHook is
// telling the truth, not a per-tick production safeguard.
func WithSingleShardAssert(enabled bool) Option {
	return func(e *Engine) { e.assertSingleShard = enabled }
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

	// sealedFast mirrors sealed for seal()'s atomic fast path (BUG-016).
	// sealed only ever transitions false -> true, once, under mu, and
	// never flips back — a monotonic one-way latch. That is exactly the
	// shape double-checked locking requires: once sealedFast reads true,
	// no further synchronization can ever be needed to observe it,
	// because there is no subsequent state change to miss. seal() Stores
	// true here in the SAME critical section that sets e.sealed = true
	// (see seal()), so any goroutine whose Load observes true is
	// guaranteed by the Go memory model (a sync/atomic Store
	// happens-before any Load that observes it) to also observe every
	// write ordered before that Store.
	//
	// This does NOT change RegisterPhaseHook's synchronization at all —
	// it still reads/writes e.hooks and e.sealed exclusively under mu,
	// same as before. sealedFast only lets seal()'s OWN repeat calls
	// (every AdvanceTicks call after the first) skip mu entirely, which
	// is safe precisely because seal()'s fast path does nothing but
	// report "already sealed" — it has no other state to mutate.
	sealedFast atomic.Bool

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

	// assertSingleShard is BUG-269's opt-in dev-mode safety net for the
	// SingleShardHook fast path — see WithSingleShardAssert's doc
	// comment. Defaults to false (production: fast path trusts the
	// hook's promise, pays for shard 0 only).
	assertSingleShard bool

	// gameplayHandler is the injected gameplay-command handler (see
	// GameplayCommandHandler's doc comment in commands.go). nil until
	// WithGameplayCommandHandler wires it — nil is read as "no handler
	// configured, deny KindBuy/KindZone/KindBuild/KindDemolish"
	// (ErrUnhandledCommandKind, MET-E009), never as "no handler
	// configured, accept them". The composition root injects the one
	// handler that maps those four kinds onto engine.build/engine.world's
	// command surfaces (GR#20 — engine.core neither owns nor imports the
	// modules that adjudicate gameplay intent).
	gameplayHandler GameplayCommandHandler

	// journaler is the injected replay-journaling seam accept() consults
	// (see CommandJournaler's doc comment in commands.go). nil until
	// WithCommandJournaler/SetCommandJournaler wires one — nil is read as
	// "no journaling configured, no-op" (mirrors observer's optional-hook
	// shape), NOT as gameplayHandler/speed8xGate's deny-by-default shape:
	// journaling absence is not a security gate, so there is nothing to
	// deny. Aaron DD (2026-08-31, FEAT-1972079852 inc3): the ENGINE owns
	// the journal — commands accepted over the protocol are recorded
	// Go-side via harness.replay's Recorder, never by the TS console.
	journaler CommandJournaler

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

	// pumpStarted is F1a's mechanical single-start guard (independent
	// round r1, FEAT-208 increment 1): StartSubscriptionPump
	// CompareAndSwaps this false->true before ever starting the pump
	// goroutine, so a second call on the same Engine is rejected
	// (ErrSubscriptionPumpAlreadyStarted) rather than silently starting
	// a second, concurrently-running pump — see StartSubscriptionPump's
	// doc comment (commands.go) for the ordering-corruption finding this
	// closes.
	pumpStarted atomic.Bool

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
	// DefaultSecondsPerMonthAt1x is a fixed, always-positive package
	// constant (clock.go), so NewClock cannot actually reject it here —
	// the error is checked anyway (GR#1: never assume a call that returns
	// an error cannot fail) and logged loudly rather than silently
	// discarded if that invariant is ever broken by a future edit.
	clock, err := NewClock(DefaultSecondsPerMonthAt1x)
	if err != nil {
		_ = errs.Wrap(ErrInvalidPacingConstant, errs.NewCorrelationID(), err, map[string]any{"seconds": DefaultSecondsPerMonthAt1x})
	}
	e := &Engine{
		clock:       clock,
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
	// FEAT-208: "engine.status" is v1's one always-available view,
	// registered here rather than left to a caller (compose.Wire only
	// registers the ADDITIONAL views baseline-one wires — see
	// viewRegistrationOrder) so every NewEngine, including the many
	// tests in this package that never call compose.Wire at all (e.g.
	// subscribe_test.go's TestSubscription_EngineStatusDeltas_MonotonicSeq),
	// keeps Subscribe("engine.status") working exactly as it did before
	// Subscribe became table-driven. e.engineStatusViewPatch (a bound
	// method value, not an inline closure literal — see its own doc
	// comment) always reads live state through EngineStatusView's own
	// guarded Clock()/registry.List() reads. Unreachable in practice: a
	// fresh SubscriptionServer's views table is empty and engineStatusView
	// is a fixed, well-formed constant, so RegisterView can only fail
	// here if that invariant is ever broken — logged loudly rather than
	// silently ignored (GR#1) since there is no correlationID/caller to
	// propagate a NewEngine-time failure to otherwise.
	if err := e.subs.RegisterView(engineStatusView, e.engineStatusViewPatch); err != nil {
		_ = errs.Wrap(ErrViewAlreadyRegistered, errs.NewCorrelationID(), err, map[string]any{"view": engineStatusView})
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
// BUG-456 perf: correlationID is minted LAZILY — only when a copy is
// actually detected (the never-taken path on a live engine). Per-tick hot
// callers (Clock) pass "" so the crypto/rand+Sprintf UUID cost is not paid
// on every tick; cold callers may still pass an eager ID, which is used
// as-is. This removed ~37% of all per-tick allocations (the +33% alloc-count
// regression on the 1M gate) without any behaviour change — a real copy
// still yields a valid correlation ID.
func (e *Engine) checkNotCopied(correlationID string, ctx map[string]any) error {
	if e.self.Load() != e {
		if correlationID == "" {
			correlationID = errs.NewCorrelationID()
		}
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

// HookCount returns the total number of PhaseHooks currently registered
// across all phases. It is the runtime hook-count accessor the composition
// root (internal/engine/compose) and its callers need to prove a run drove
// real hooks rather than a walking-skeleton zero (BUG-034/ASM-422). This
// is a boot-time diagnostic, NOT a tick-path call — it takes mu and ranges
// e.hooks (a map) purely to sum lengths, so its iteration order cannot
// reach simulation state (GR#21's map-range discipline applies to the tick
// path, not to this count).
func (e *Engine) HookCount() int {
	// SEC-016/SEC-018 ordering: this acquires e.mu, so the identity check
	// must run BEFORE the lock (a struct copy's mu can read as permanently
	// locked if captured mid-lock on the original). Degrade to 0 on a copy
	// rather than return an error, mirroring Tick()/Paused()'s shape — a
	// copy can never legitimately reach a boot-time diagnostic.
	if err := e.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HookCount"}); err != nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, hooks := range e.hooks {
		n += len(hooks)
	}
	return n
}

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
	// BUG-456 perf: "" — mint the correlation ID only on the (never-taken)
	// copy path; Clock() is one of the hottest per-tick read accessors.
	if err := e.checkNotCopied("", nil); err != nil {
		return Clock{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock, nil
}

// SeedClockForRestore seeds the Engine's clock to tick, WITHOUT running any
// phase or command (FEAT-1972079944, Aaron's ruling option A). This is the
// narrow, restore-only counterpart to the sealed-clock invariant documented
// on the sealed field: the clock otherwise advances ONLY via AdvanceTicks,
// and this method deliberately does not change that for a live/running
// engine -- it exists purely so a LOAD path (internal/engine/compose's
// Composition.LoadAt) can seed a freshly restored, never-yet-ticked Engine
// to the tick its saved state was captured at, closing the gap Load()
// leaves (a state-exact snapshot stuck at tick 0 -- see save_wire.go's
// Load doc comment).
//
// Restore-only, mechanically enforced, not just documented: this call is
// REJECTED with ErrEngineSealed once the Engine has run its first
// AdvanceTicks (see the sealed field's doc comment) -- the exact same
// one-way latch RegisterPhaseHook is gated on, reused here rather than a
// parallel mechanism. There is deliberately no other public API that can
// move the clock: AdvanceTicks is still the only way to advance a live
// engine, and this method cannot be called again to any effect once
// sealed, so the "clock only moves via AdvanceTicks" invariant holds for
// every engine that has ever ticked. A caller cannot use this to rewind or
// tamper with a running simulation's clock -- only to finish constructing
// one that has not started yet.
//
// tick must be >= 0 (Clock.Tick's own contract: an elapsed daily-tick
// count since genesis can never be negative) -- rejected with
// ErrInvalidClockSeed otherwise, and the clock is left unchanged.
//
// Also returns ErrEngineCopied (SEC-014/SEC-016) if called on a
// struct-copied Engine value, checked before mu is ever touched, exactly
// like every other mu-acquiring method in this file.
func (e *Engine) SeedClockForRestore(correlationID string, tick int64) error {
	if err := e.checkNotCopied(correlationID, map[string]any{"method": "SeedClockForRestore"}); err != nil {
		return err
	}
	if tick < 0 {
		return errs.New(ErrInvalidClockSeed, correlationID, map[string]any{"tick": tick})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkNotCopied(correlationID, map[string]any{"method": "SeedClockForRestore"}); err != nil {
		return err
	}
	if e.sealed {
		return errs.New(ErrEngineSealed, correlationID, map[string]any{"method": "SeedClockForRestore"})
	}
	e.clock.tick = tick
	return nil
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

// RegisterView registers fn as the patch producer for the given view
// name (FEAT-208) — the Engine-level forwarding wrapper compose.Wire
// calls, mirroring RegisterPhaseHook's own shape/discipline exactly:
// identity-checked before ever touching e.subs (SEC-016's ordering,
// applied here for the same reason), then, explicitly and directly
// (SEC-019, not merely relying on the transitive guard
// SubscriptionServer.RegisterView already applies internally — astgate's
// syntactic, no-call-graph scan cannot see across that call, the same
// documented blind spot every *Locked-helper precedent in this codebase
// already works around), a second identity check against e.subs itself
// before delegating.
func (e *Engine) RegisterView(name string, fn ViewPatchFunc) error {
	if err := e.checkNotCopied(errs.NewCorrelationID(), map[string]any{"view": name}); err != nil {
		return err
	}
	if err := e.subs.checkNotCopied(errs.NewCorrelationID(), map[string]any{"view": name}); err != nil {
		return err
	}
	return e.subs.RegisterView(name, fn)
}

// seal permanently closes hook registration. Called at the top of
// AdvanceTicks, before any phase runs. Idempotent (a second/subsequent
// AdvanceTicks call still runs the same checks but only re-confirms
// sealed is already true) — see the sealed field's doc comment for why
// removing all locking from the per-phase hot path depends on this
// running once per AdvanceTicks call, and the self field's doc comment
// for why the identity check specifically must run BEFORE mu (SEC-016).
//
// Cost (BUG-016, atomic fast path added on top of the original
// mutex-only design): the FIRST call on a given Engine pays one
// lock-free atomic.Pointer.Load (identity, pre-lock) + one mutex
// acquisition (a command-level cost, not a per-tick or per-phase one) +
// one more atomic.Pointer.Load (identity, post-lock, defence in depth) +
// one atomic.Bool.Store. EVERY SUBSEQUENT call — which is every
// AdvanceTicks call after the first for the life of the Engine —
// resolves via sealedFast.Load() alone (see its doc comment) and never
// touches mu at all: two lock-free loads total (identity + sealedFast),
// no lock, no allocation. For AC-9's steady-state benchmark, which
// calls AdvanceTicks(n=1) per iteration, that means only iteration 1
// pays a mutex acquisition; the remaining b.N-1 iterations pay none
// (confirmed by BenchmarkSeal_FastPath_NoMutex in bench_test.go and by
// re-running BenchmarkAdvanceTicks_SteadyState_ZeroModules after this
// change — still 0 allocs/op).
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
	// BUG-016 atomic fast path: sealedFast is a monotonic false->true
	// latch that mirrors e.sealed (see its doc comment). Every
	// AdvanceTicks call after the first observes true here and returns
	// having touched nothing but two lock-free atomic loads — no
	// e.mu.Lock() at all. This is safe DESPITE seal() being called
	// concurrently from multiple goroutines (SEC-003's stress test)
	// because the fast path is read-only: it makes no decision that
	// depends on anything else being simultaneously true, and the one
	// write that matters (sealedFast.Store(true) below) always happens
	// inside the mu-guarded slow path, ordered after e.sealed = true, so
	// a Load that observes true here can only do so after that write has
	// fully completed (sync/atomic Store/Load happens-before).
	if e.sealedFast.Load() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkNotCopied(correlationID, nil); err != nil {
		return err
	}
	e.sealed = true
	e.sealedFast.Store(true)
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
