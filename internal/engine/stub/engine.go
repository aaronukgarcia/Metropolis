package stub

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// viewportViewName is the one view Folkestone-64 is really served under
// (AC-3). Any other well-formed view name still subscribes successfully
// (AC-2/AC-5 are generic over Subscribe), served the minimal
// genericViewPatch instead (viewport.go).
const viewportViewName = "f1.viewport"

// validSpeeds are the only SetSpeedPayload.Speed values StubEngine
// accepts, matching GDD §3's speed table (1/2/3, plus 8 in debug
// builds). Not exported: the engine module that eventually owns speed
// control decides whether this table itself should be protocol-visible.
var validSpeeds = map[int]bool{1: true, 2: true, 3: true, 8: true}

// Option configures a StubEngine at construction time. See WithChaos.
type Option func(*StubEngine) error

// WithChaos enables StubEngine's chaos knobs (AC-7). An invalid cfg
// (negative delay, inverted range, burst size < 2 while enabled) makes
// NewStubEngine return an error rather than silently clamping it (AC-10).
func WithChaos(cfg ChaosConfig) Option {
	return func(s *StubEngine) error {
		if err := validateChaos(cfg); err != nil {
			return err
		}
		s.chaos = cfg
		s.rng = newSplitMix64(cfg.Seed)
		return nil
	}
}

// StubEngine is H-STUB: a full internal/protocol implementation with
// canned behaviour, driven through a *protocol.InProcTransport — the
// same seam a real engine uses (AC-1). See doc.go for the package-level
// permanent-fixture statement (AC-8) and codes.go for its placeholder
// registry error codes (AC-9/AC-10).
//
// StubEngine computes nothing: AdvanceTicks does pure tick arithmetic,
// and every Delta/Event content comes from the handcrafted Folkestone-64
// fixture (fixture.go) or the scripted delta stream (viewport.go) — never
// from simulating anything.
type StubEngine struct {
	transport *protocol.InProcTransport
	world     *World
	chaos     ChaosConfig
	rng       *splitMix64 // guarded by mu; only used for chaos delay jitter

	mu      sync.Mutex
	tick    protocol.Tick
	speed   int
	paused  bool
	subs    map[protocol.SubscriptionID]*subState
	allocID *protocol.SubscriptionAllocator

	// self holds the address NewStubEngine gave this StubEngine at
	// construction (self.Store(s), set once, at the end of
	// NewStubEngine, after every Option has run and before s is
	// returned to any caller — no goroutine can have a reference to s
	// to race that Store against).
	//
	// SEC-020 wave 2: StubEngine has the exact shape
	// Engine.self/SubscriptionServer.self/InProcTransport.self were all
	// fixed for (engine/core/engine.go, engine/core/subscribe.go,
	// internal/protocol/transport.go — read first, this mirrors them
	// deliberately rather than inventing a variant). 's2 := *s' is
	// legal, unsafe-free, reflect-free Go from outside this package
	// (every field is unexported, but Go does not stop a caller from
	// dereferencing the *StubEngine NewStubEngine returned and copying
	// the struct value). mu is a plain sync.Mutex VALUE, so the copy
	// gets its OWN, independent mu — but subs (a map), transport and
	// world (pointers), and rng (a pointer to mutable PRNG state) are
	// all reference types, so a copy ALIASES every one of them. The
	// copy's mu therefore protects nothing shared: a caller driving the
	// copy's Run loop concurrently with the original mutates the SAME
	// subs map and the SAME rng state under two DIFFERENT, uncoordinated
	// locks — exactly SEC-014/SEC-019's shape, reopened here.
	//
	// atomic.Pointer, not a plain *StubEngine field, for the same
	// SEC-016 reason the three reference types above use it: the
	// identity check must be race-safe and must run BEFORE mu is ever
	// touched, because a struct copy taken while the ORIGINAL's mu
	// happens to be held captures those mutex bytes read as "locked" —
	// the copy's own next Lock() call on that captured state can then
	// park forever, since nothing will ever Unlock() that specific
	// copy's address (SEC-016's pre-lock-ordering rule: a guard placed
	// after the lock can never run for that attack, because the attack
	// IS acquiring the lock). A plain field read racing a concurrent
	// struct copy has no defined result under the Go memory model;
	// atomic.Pointer's Load/Store do.
	self atomic.Pointer[StubEngine]
}

// NewStubEngine constructs a StubEngine bound to t. t's engine-facing
// side (Commands/SendResult/SendEvent/SendDelta) is exactly what Run
// below drives; t's UI-facing Transport side is what a caller (a test, a
// harness, feat.skeleton) uses to talk to the stub, per AC-1.
func NewStubEngine(t *protocol.InProcTransport, opts ...Option) (*StubEngine, error) {
	s := &StubEngine{
		transport: t,
		world:     GenerateFolkestone64(),
		speed:     1,
		subs:      make(map[protocol.SubscriptionID]*subState),
		allocID:   protocol.NewSubscriptionAllocator(),
		rng:       newSplitMix64(0),
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	// Stored exactly once, here, after every Option has run and before
	// s is returned to any caller — no goroutine can have a reference to
	// s to race this Store against (SEC-020 wave 2; mirrors NewEngine/
	// NewSubscriptionServer/NewInProcTransport — see self's doc comment
	// above).
	s.self.Store(s)
	return s, nil
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other StubEngine value (SEC-020 wave 2, mirroring
// Engine.checkNotCopied/SubscriptionServer.checkNotCopied/
// InProcTransport.checkNotCopied — see self's doc comment on StubEngine
// for the full attack shape). Deliberately lock-free — a single
// atomic.Pointer.Load, requiring nothing else, not s.mu — so it is safe
// and correct to call BEFORE s.mu is ever touched, even for the very
// first Lock(). That ordering is the whole point (SEC-016): a struct
// copy's mu can be byte-for-byte "currently locked" if the copy was
// taken while the original's mu was held, and even attempting to
// acquire a copy's own mu in that state can block forever, since
// nothing will ever Unlock() that specific copy's address. Rejecting
// the copy via this check, before Lock() is ever called, means that
// hang path is never reached at all.
//
// A nil s.self.Load() (a StubEngine constructed as a bare
// StubEngine{}/new(StubEngine)/a hand-built literal rather than via
// NewStubEngine, so self was never stored) is treated the same as a
// mismatch and rejected the same way — every documented construction
// path is NewStubEngine, so an unset self is itself a misuse this same
// error correctly names, and rejecting it here also means such a
// value's nil subs map / nil transport / nil world are never reached
// either.
func (s *StubEngine) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(codeStubEngineCopied, correlationID, ctx)
	}
	return nil
}

// World returns the loaded Folkestone-64 fixture (AC-3).
//
// SEC-020 wave 2: deliberately left WITHOUT a checkNotCopied call,
// unlike InProcTransport's Results/Events/Deltas/Commands accessors —
// the right analogue here is engine.core's Registry()/WorldSeed()/
// PoolSize() (engine.go), not InProcTransport's channel accessors.
// world is a plain *World pointer, set exactly once in NewStubEngine
// and never reassigned afterwards; the World it points to is built by
// GenerateFolkestone64 (fixture.go) and never mutated post-construction
// (StubEngine "computes nothing" — see the package doc). A copy's
// s.world is byte-identical to the original's: reading it is safe and
// correct regardless of which StubEngine value the caller is holding,
// because there is no shared MUTABLE state behind this pointer for a
// copy's independent mu to fail to protect (contrast subs/rng, which
// this file's mu.Lock() sites guard because they DO change after
// construction). Unlike InProcTransport's channels — which are torn
// down by Close() and where returning the real, aliased channel to a
// copy would hide a genuine misuse — there is no teardown here to hide;
// failing this accessor closed would only make an already-safe read
// return an error for no safety benefit.
func (s *StubEngine) World() *World { return s.world }

// Tick returns the current fake-tick counter (AC-4).
//
// SEC-020 wave 2: identity-checked before AND after s.mu is acquired —
// see self's doc comment for why the pre-lock check must be lock-free
// and must run first; the post-lock check is defence in depth against a
// future refactor adding another s.mu-acquiring path ahead of this one
// without threading the check through it too (mirrors
// Engine.RegisterPhaseHook/seal's ordering, engine/core/engine.go).
func (s *StubEngine) Tick() protocol.Tick {
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self.Load() != s {
		return 0
	}
	return s.tick
}

// Paused reports whether the stub is currently paused (BUG-010). Mirrors
// engine.core's Clock.Paused() getter shape (internal/engine/core/clock.go)
// so a consumer written against either engine can read pause state the
// same way. Queryable only — it is NOT consulted by handleAdvanceTicks as
// an advance-gate, matching engine.core's Clock (see handleAdvanceTicks'
// BUG-010 RECONCILED comment for why). SEC-020 wave 2: identity-checked
// before AND after s.mu is acquired — see Tick's identical note above for
// why.
func (s *StubEngine) Paused() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self.Load() != s {
		return false
	}
	return s.paused
}

// Speed returns the currently configured pacing multiplier (BUG-010).
// Mirrors engine.core's Clock.Speed() getter shape (clock.go); returned
// as plain int (not core.Speed) since this package has no dependency on
// engine.core and StubEngine already stores speed as an int (see the
// StubEngine struct's speed field and handleSetSpeed's validSpeeds
// table). SEC-020 wave 2: identity-checked before AND after s.mu is
// acquired — see Tick's identical note above for why.
func (s *StubEngine) Speed() int {
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self.Load() != s {
		return 0
	}
	return s.speed
}

// Run drives the engine loop: range over t.Commands(), dispatch and
// answer each one, until ctx is cancelled or the transport closes
// (Commands() channel closes). It is the only goroutine that reads
// commands or mutates tick/speed/paused/subs directly — chaos's delayed
// sends (chaos.go) run in their own goroutines but only ever call the
// transport's non-blocking SendDelta, never touch engine state.
//
// # Exit contract (SEC-026 — read this before wiring a caller)
//
// ctx cancellation and a Commands() closure are NOT two equivalent,
// interchangeable ways to stop Run. Only ctx cancellation is the clean
// shutdown signal: Run returns ctx.Err() (nil-shaped from a caller's
// point of view — a normal, intentional stop). A Commands() closure
// that Run observes WITHOUT ctx already being cancelled is reported as
// an error (codePrematureCommandsClose, MET-P094, BUG-020) — it means
// the transport went away for some reason other than the shutdown Run
// was told about, and that must not be allowed to look like a clean
// exit.
//
// The caller-side contract this implies: cancel ctx, WAIT for Run's
// goroutine to actually return (join it — a sync.WaitGroup or an
// equivalent "done" signal), and only THEN close the transport.
// cmd/metropolis/boot.go's shutdown sequence — cancel(); wg.Wait();
// Close() — is the canonical example; follow that ordering, not the
// two-line version below.
//
// Do NOT write "cancel(); Close()" without the join in between, even
// though it looks obviously equivalent. Calling cancel() only makes
// ctx.Done() ready — it does not block until Run's select has actually
// observed and acted on that; the calling goroutine keeps running and
// can call Close() before Run's select has resolved anything. If that
// happens, Run's select sees ctx.Done() and Commands() ready at
// essentially the same moment and the Go runtime is free to pick
// EITHER one — there is no ordering guarantee that "cancel happened
// first" translates into "Run's select observes cancellation first".
// When it picks the Commands() branch, Run correctly reports
// codePrematureCommandsClose by its own logic (ctx was not yet Done at
// the instant it looked) even though the caller's intent was a clean
// stop — a false alarm that trains whoever reads the log to ignore it,
// which is the one thing BUG-020 exists to prevent. Destructive-2
// measured this: cancel();Close() with no join reported "premature"
// on the overwhelming majority of runs, and it stayed a roughly 50/50
// coin flip even once Run's goroutine was confirmed to already be
// blocked in its select before cancel() ran — proving the race is
// inherent to skipping the join, not a scheduling fluke that a "good
// enough" delay would paper over. Join before you close; there is no
// safe shortcut.
func (s *StubEngine) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cmd, ok := <-s.transport.Commands():
			if !ok {
				// BUG-020/SEC-026: a closed Commands() channel is
				// ambiguous on its own — see the "Exit contract" section
				// of Run's doc comment above for the full statement of
				// what the two triggers mean and why they are NOT
				// interchangeable. Re-checking ctx.Done() here, AFTER
				// observing ok==false, is what tells them apart: if ctx is
				// already Done, this is the clean shutdown path
				// (nil-shaped, via ctx.Err()); if it is not, something
				// closed the transport out from under a caller that hadn't
				// told Run to stop, and that is reported, never silently
				// swallowed. Today's only caller (cmd/metropolis/boot.go)
				// follows the required cancel(); wg.Wait(); Close()
				// ordering, so this branch is latent under current wiring
				// — but nothing in this package enforces that ordering on
				// Run's behalf, and a future caller, a copied transport
				// handed to NewStubEngine, or an early Close() elsewhere
				// could all close Commands() first. See
				// codePrematureCommandsClose (codes.go) —
				// cmd/metropolis/boot.go still discards Run's return value
				// (`_ = engine.Run(ctx)`), so making that caller OBSERVE
				// this distinction is a separate, out-of-scope change
				// (noted in this item's dispatch report for Bill to route).
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return errs.New(codePrematureCommandsClose, errs.NewCorrelationID(), nil)
				}
			}
			s.handle(cmd)
		}
	}
}

// handle dispatches one already-validated Command (protocol.Command.Validate
// ran inside InProcTransport.SendCommand before it ever reached
// s.transport.Commands()) and answers it with a CommandResult.
func (s *StubEngine) handle(cmd protocol.Command) {
	// SEC-020 wave 2: identity-checked BEFORE dispatch — the single
	// choke point for every command-based entry into s.mu, mirroring
	// engine.core's HandleCommand entry check (commands.go). Every
	// individual handleXxx method below ALSO carries its own pre-lock
	// check (defence in depth, not the only line — same posture as
	// SEC-016's RegisterPhaseHook/seal and SEC-018's HandleCommand), but
	// this stops a copy before the switch, and before handleInspectEntity/
	// handleDebug's Tick() call, ever run.
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}); err != nil {
		s.transport.SendResult(errRefResult(cmd, err))
		return
	}
	var result protocol.CommandResult
	switch cmd.Kind {
	case protocol.KindAdvanceTicks:
		result = s.handleAdvanceTicks(cmd)
	case protocol.KindSetSpeed:
		result = s.handleSetSpeed(cmd)
	case protocol.KindPause:
		result = s.handlePause(cmd)
	case protocol.KindResume:
		result = s.handleResume(cmd)
	case protocol.KindSubscribe:
		result = s.handleSubscribe(cmd)
	case protocol.KindUnsubscribe:
		result = s.handleUnsubscribe(cmd)
	case protocol.KindInspectEntity:
		result = s.handleInspectEntity(cmd)
	case protocol.KindDebug:
		result = s.handleDebug(cmd)
	case protocol.KindBuy, protocol.KindZone, protocol.KindBuild, protocol.KindDemolish, protocol.KindSetFunding:
		result = s.handleGameplay(cmd)
	default:
		// Defensive: see codes.go's codeUnknownKind doc. Every Kind that
		// reaches here already passed commandRegistry, so this branch
		// guards against the protocol vocabulary outgrowing this switch,
		// not against malformed input.
		result = s.reject(cmd, codeUnknownKind, map[string]any{"kind": string(cmd.Kind)})
	}
	s.transport.SendResult(result)
}

func (s *StubEngine) handleAdvanceTicks(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.AdvanceTicksPayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}
	if p.N <= 0 {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"n": p.N, "cause": "AdvanceTicksPayload.N must be positive"})
	}
	// SEC-006: bound N the same way engine.core.AdvanceTicks bounds it
	// (maxAdvanceTicksPerCall, codes.go) — reject, never silently clamp.
	if p.N > maxAdvanceTicksPerCall {
		return s.reject(cmd, codeAdvanceTicksOutOfBounds, map[string]any{"n": p.N, "max": maxAdvanceTicksPerCall})
	}

	// SEC-020 wave 2: identity-checked BEFORE s.mu — one of this
	// package's s.mu.Lock() sites (see enumeration in the dispatch
	// report). Also caught by handle()'s entry-point check (defence in
	// depth, not the only line), but guarded here directly too so this
	// method is safe even if ever called by a future non-handle path.
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}))
	}
	// BUG-010 RECONCILED (Bill's ruling, 2026-08-13): AdvanceTicks does
	// NOT gate on s.paused here, deliberately matching engine.core's real
	// Clock/Engine contract instead of diverging from it. engine.core's
	// clock.go documents Paused as feeding only SecondsPerMonth/
	// TicksPerRealSecond for a future real-time driver — advanceOneDay's
	// own doc comment there is explicit that "the explicit AdvanceTicks
	// command is the deliberate, explicit driver of ticks" and
	// intentionally bypasses Paused. A stub that gated here instead would
	// give stub-based tests false confidence about pause semantics that
	// the real engine does not honour. Paused()/Speed() remain exported
	// getters (still correct/useful to expose) — they are just not used
	// as an advance-gate. Do NOT reintroduce a `if s.paused { ... }`
	// no-op here; that was tried and reverted for exactly this reason.
	// SEC-006 (Weakness pattern #1 — guard the arithmetic, not just the
	// input): bounding N per call does not by itself prove s.tick, which
	// accumulates across every call for the life of the engine, can
	// never overflow int64. With N capped at maxAdvanceTicksPerCall
	// (~3600), reaching math.MaxInt64 would take on the order of 2.5e15
	// calls — not reachable by any real client in this engine's
	// lifetime — but "practically unreachable" is not the same claim as
	// "impossible", so the running total is checked explicitly rather
	// than trusted. A call that would overflow is rejected outright
	// (the tick counter is left unchanged), exactly like an
	// out-of-bounds N.
	if s.tick > protocol.Tick(math.MaxInt64)-protocol.Tick(p.N) {
		s.mu.Unlock()
		return s.reject(cmd, codeAdvanceTicksOutOfBounds, map[string]any{
			"n": p.N, "currentTick": int64(s.tick), "cause": "would overflow the tick counter",
		})
	}
	s.tick += protocol.Tick(p.N)
	tick := s.tick
	subsSnapshot := make([]*subState, 0, len(s.subs))
	for _, sub := range s.subs {
		subsSnapshot = append(subsSnapshot, sub)
	}
	for _, sub := range subsSnapshot {
		s.advanceSubscriptionScriptLocked(sub, tick)
	}
	s.mu.Unlock()

	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleSetSpeed(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.SetSpeedPayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}
	if !validSpeeds[p.Speed] {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"speed": p.Speed, "cause": "unsupported speed multiplier"})
	}
	// SEC-020 wave 2: identity-checked BEFORE s.mu, and again after
	// acquisition — see handleAdvanceTicks' identical note above.
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}))
	}
	s.speed = p.Speed
	tick := s.tick
	s.mu.Unlock()
	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handlePause(cmd protocol.Command) protocol.CommandResult {
	// SEC-020 wave 2: identity-checked BEFORE s.mu, and again after
	// acquisition — see handleAdvanceTicks' identical note above.
	if err := s.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), nil))
	}
	s.paused = true
	tick := s.tick
	s.mu.Unlock()
	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleResume(cmd protocol.Command) protocol.CommandResult {
	// SEC-020 wave 2: identity-checked BEFORE s.mu, and again after
	// acquisition — see handleAdvanceTicks' identical note above.
	if err := s.checkNotCopied(string(cmd.CorrelationID), nil); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), nil))
	}
	s.paused = false
	tick := s.tick
	s.mu.Unlock()
	return s.acceptAt(cmd, tick)
}

// handleSubscribe allocates a SubscriptionID and immediately pushes the
// first Delta for it. The protocol envelope (envelope.go) gives
// CommandResult no room for auxiliary return data by design, so — per
// deltas.go's own doc comment ("CorrelationID echoes the Command that
// caused this delta... e.g. a Subscribe's very first delta") — the
// SubscriptionID a caller learns is the one carried on that first Delta,
// which is correlated back to this Subscribe via CorrelationID. See this
// item's dispatch report for the AC-1/AC-5 phrasing this resolves.
func (s *StubEngine) handleSubscribe(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.SubscribePayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}
	if err := protocol.ValidateViewName(p.ViewName); err != nil {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"viewName": p.ViewName, "cause": err.Error()})
	}

	// SEC-020 wave 2: identity-checked BEFORE s.mu, and again after
	// acquisition — see handleAdvanceTicks' identical note above.
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"viewName": p.ViewName}); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), map[string]any{"viewName": p.ViewName}))
	}
	id := s.allocID.Allocate()
	sub := &subState{id: id, viewName: p.ViewName, done: make(chan struct{})}
	s.subs[id] = sub
	tick := s.tick

	var initial any
	if p.ViewName == viewportViewName {
		initial = fullViewportSnapshot(s.world)
	} else {
		initial = genericViewPatch{ViewName: p.ViewName, Note: "stub: subscription opened"}
	}
	s.emitDeltaLocked(sub, tick, initial, cmd.CorrelationID)
	s.mu.Unlock()

	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleUnsubscribe(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.UnsubscribePayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}

	// SEC-020 wave 2: identity-checked BEFORE s.mu, and again after
	// acquisition — see handleAdvanceTicks' identical note above.
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"subscriptionId": string(p.SubscriptionID)}); err != nil {
		return errRefResult(cmd, err)
	}
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return errRefResult(cmd, errs.New(codeStubEngineCopied, string(cmd.CorrelationID), map[string]any{"subscriptionId": string(p.SubscriptionID)}))
	}
	sub, exists := s.subs[p.SubscriptionID]
	if !exists {
		s.mu.Unlock()
		return s.reject(cmd, codeInvalidPayload, map[string]any{"subscriptionId": string(p.SubscriptionID), "cause": "unknown subscription"})
	}
	delete(s.subs, p.SubscriptionID)
	// BUG-283: closing done aborts any in-flight delayed-delta wait and
	// tells the delivery pump to drop everything still queued for this
	// subscription — the engine must stop pushing the instant Unsubscribe is
	// processed (deltas.go/AC-5). done is closed exactly once: this branch
	// only runs when the subscription existed and was just deleted, so a
	// second Unsubscribe for the same ID takes the !exists path above. Both
	// the close here and the pump's own liveness re-check run under s.mu, so
	// no delta can slip out for an already-processed Unsubscribe.
	close(sub.done)
	tick := s.tick
	s.mu.Unlock()

	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleInspectEntity(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.InspectEntityPayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}
	if p.EntityRef == "" {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"cause": "InspectEntityPayload.EntityRef must not be empty"})
	}
	tick := s.Tick()
	s.transport.SendEvent(protocol.Event{
		Kind:          "entity.inspected",
		Tick:          tick,
		Severity:      protocol.SeverityInfo,
		EntityRefs:    []string{p.EntityRef},
		CorrelationID: cmd.CorrelationID,
	})
	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleDebug(cmd protocol.Command) protocol.CommandResult {
	p, ok := cmd.Payload.(protocol.DebugPayload)
	if !ok {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"kind": string(cmd.Kind)})
	}
	if p.Op == "" {
		return s.reject(cmd, codeInvalidPayload, map[string]any{"cause": "DebugPayload.Op must not be empty"})
	}
	tick := s.Tick()
	s.transport.SendEvent(protocol.Event{
		Kind:          "debug.op.executed",
		Tick:          tick,
		Severity:      protocol.SeverityInfo,
		Fields:        map[string]string{"op": p.Op},
		CorrelationID: cmd.CorrelationID,
	})
	return s.acceptAt(cmd, tick)
}

// handleGameplay accepts the gameplay-intent commands (Buy, Zone, Build,
// Demolish — added to the protocol vocabulary by the ASM-485 extension;
// SetFunding — added FEAT-208 increment 3, internal/protocol/commands.go)
// as no-ops. The stub computes
// nothing and owns no build/finance/ownership state to accept or reject
// against, so it cannot adjudicate BLD-7's "engine rejects and the reason
// surfaces" path — that is engine.build/engine.finance's job when those
// modules land. Accepting rather than falling through to codeUnknownKind
// is required by AC-2 (TestStubEngine_AllKnownKindsHandled asserts every
// protocol.KnownKinds() kind is handled) and is the only response that
// keeps BUG-039's invariant intact: a no-op accept mutates no engine
// state and never touches the shared World fixture.
func (s *StubEngine) handleGameplay(cmd protocol.Command) protocol.CommandResult {
	// SEC-020 wave 2: identity-checked before accepting — a command-based
	// entry into s (via handle()'s switch), so it carries the same copy
	// guard as every other handleXxx method (defence in depth on top of
	// handle()'s entry-point check and Tick()'s own guard).
	if err := s.checkNotCopied(string(cmd.CorrelationID), map[string]any{"kind": string(cmd.Kind)}); err != nil {
		return errRefResult(cmd, err)
	}
	return s.acceptAt(cmd, s.Tick())
}

// advanceSubscriptionScriptLocked pushes sub's next scripted delta, if
// any remain (AC-6). Callers must hold s.mu.
func (s *StubEngine) advanceSubscriptionScriptLocked(sub *subState, tick protocol.Tick) {
	script := scriptFor(sub.viewName)
	if sub.scriptAt >= len(script) {
		return
	}
	patch := script[sub.scriptAt]
	sub.scriptAt++
	s.emitDeltaLocked(sub, tick, patch, "")
}

// emitDeltaLocked pushes one scripted patch as one or more Deltas
// (BurstConfig.Size copies under burst chaos, AC-7b) with monotonically
// increasing per-subscription Seq (AC-5), optionally delayed under
// DelayConfig (AC-7a). Only the first copy carries correlationID
// (non-empty only for a Subscribe's initial delta — see handleSubscribe).
// Callers must hold s.mu; the chaos-delay goroutine spawned below does
// NOT touch engine state, only the already-built Delta value and the
// transport, so it is safe to run after mu is released by the caller.
func (s *StubEngine) emitDeltaLocked(sub *subState, tick protocol.Tick, patch any, correlationID protocol.CorrelationID) {
	count := 1
	if s.chaos.BurstDeltas.Enabled {
		count = s.chaos.BurstDeltas.Size
	}
	raw := encodePatch(patch)
	for i := 0; i < count; i++ {
		corr := protocol.CorrelationID("")
		if i == 0 {
			corr = correlationID
		}
		d := protocol.Delta{
			SubscriptionID: sub.id,
			Tick:           tick,
			Seq:            sub.nextSeq(),
			Patch:          raw,
			CorrelationID:  corr,
		}
		if s.chaos.DelayedDeltas.Enabled {
			// s.rng is guarded by s.mu (which every caller of this method
			// holds), so the delay draw stays deterministic in Seq order.
			delay := s.rng.nextDelay(s.chaos.DelayedDeltas.MinDelay, s.chaos.DelayedDeltas.MaxDelay)
			// BUG-284: enqueue for in-order, per-subscription delivery
			// instead of spawning an independent goroutine per delta.
			// Independent per-delta sleeps let a later-Seq delta with a
			// shorter delay overtake an earlier one; a single pump draining
			// this FIFO queue sequentially guarantees delivery order ==
			// enqueue order == Seq/Tick order (GR#21). No map-range or other
			// nondeterministic ordering sits in the delivery path.
			// BUG-283: the pump re-checks subscription liveness before every
			// send, so a delta queued before an Unsubscribe is dropped.
			sub.pending = append(sub.pending, delayedDelta{delta: d, delay: delay})
			if !sub.pumpRunning {
				sub.pumpRunning = true
				go s.runDelayPump(sub)
			}
			continue
		}
		s.transport.SendDelta(d)
	}
}

// runDelayPump drains sub.pending one delta at a time, in strict FIFO
// (== Seq) order, waiting each delta's artificial delay before sending it.
// Exactly one pump runs per subscription while its queue is non-empty;
// emitDeltaLocked (re)starts it under s.mu when it enqueues into an idle
// queue, and it exits when the queue drains or the subscription ends. It
// is the joint fix for BUG-283 and BUG-284:
//
//   - BUG-284 (out-of-order delivery): serialising every delayed send for a
//     subscription through this single goroutine makes delivery order equal
//     enqueue order, which emitDeltaLocked produces in ascending Seq. The
//     old one-goroutine-per-delta design let independent sleeps reorder
//     deltas, tripping SeqTracker.Observe's ok=false "treat as a bug" path.
//
//   - BUG-283 (use-after-unsubscribe): sub.done (closed by handleUnsubscribe)
//     aborts an in-flight delay immediately, and before every send the pump
//     re-checks — under the SAME s.mu handleUnsubscribe uses to delete the
//     subscription — that the subscription is still live. A delta queued
//     before an Unsubscribe is therefore dropped, never delivered.
//
// transport.SendDelta is non-blocking (it evicts the oldest queued delta
// rather than blocking — transport.go), so holding s.mu across the send is
// safe and is what makes the liveness check and the send atomic against
// Unsubscribe.
func (s *StubEngine) runDelayPump(sub *subState) {
	// SEC-020 copyguard: this goroutine takes s.mu below, so — like every
	// other s.mu-taking path in this file — it refuses to run on a struct
	// copy BEFORE ever touching the lock (checkNotCopied is a lock-free
	// atomic load; the pre-lock ordering is load-bearing, see its doc
	// comment). It is only ever spawned by emitDeltaLocked, itself reached
	// only through a command handler that already passed checkNotCopied on
	// the real engine, so this never fires in practice — it is defence in
	// depth. A rejected copy simply abandons the pump (it holds no aliased
	// resource to release: pending/done belong to the copy's own subState).
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return
	}
	for {
		// Pop the next queued delta in FIFO order (BUG-284).
		s.mu.Lock()
		if len(sub.pending) == 0 {
			sub.pumpRunning = false
			s.mu.Unlock()
			return
		}
		dd := sub.pending[0]
		sub.pending = sub.pending[1:]
		s.mu.Unlock()

		// Wait this delta's artificial delay, aborting the instant the
		// subscription is unsubscribed so nothing queued behind it lingers
		// (BUG-283). A zero delay needs no wait; the liveness check below
		// still guards it.
		if dd.delay > 0 {
			timer := time.NewTimer(dd.delay)
			select {
			case <-sub.done:
				timer.Stop()
				s.mu.Lock()
				sub.pending = nil
				sub.pumpRunning = false
				s.mu.Unlock()
				return
			case <-timer.C:
			}
		}

		// Liveness-checked send under s.mu (BUG-283): if the subscription was
		// unsubscribed while this delta waited, drop it and everything behind
		// it. handleUnsubscribe deletes from s.subs under this same lock, so
		// a delta can never be delivered for an already-processed Unsubscribe.
		s.mu.Lock()
		if _, alive := s.subs[sub.id]; !alive {
			sub.pending = nil
			sub.pumpRunning = false
			s.mu.Unlock()
			return
		}
		s.transport.SendDelta(dd.delta)
		s.mu.Unlock()
	}
}

// scriptFor returns the scripted delta stream for viewName (AC-6). It is
// a pure function of viewName — no engine state — kept as a package-level
// function rather than a method for that reason.
func scriptFor(viewName string) []any {
	if viewName == viewportViewName {
		vs := scriptedViewportDeltas()
		out := make([]any, len(vs))
		for i, v := range vs {
			out[i] = v
		}
		return out
	}
	return []any{genericViewPatch{ViewName: viewName, Note: "stub: no scripted content for this view in v1"}}
}

// acceptAt builds an Accepted CommandResult at the given tick.
func (s *StubEngine) acceptAt(cmd protocol.Command, tick protocol.Tick) protocol.CommandResult {
	return protocol.CommandResult{CorrelationID: cmd.CorrelationID, Tick: tick, Accepted: true}
}

// reject builds a rejected CommandResult carrying a registry-sourced
// ErrorRef (AC-9), never a bare error or a panic. See codes.go for what
// "registry-sourced" means today given code isn't registered yet.
func (s *StubEngine) reject(cmd protocol.Command, code string, ctx map[string]any) protocol.CommandResult {
	tick := s.Tick()
	e := errs.New(code, string(cmd.CorrelationID), ctx)
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          tick,
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: e.Code, Display: e.Display()},
	}
}

// errRefResult builds a rejected CommandResult directly from an
// already-constructed registry-sourced error (SEC-020 wave 2's
// checkNotCopied result), rather than through reject/codes.go's code
// constants. Used ONLY on the copy-rejection path: Tick(), which
// reject() would otherwise call to fill in the result's Tick field, is
// itself guarded and returns 0 on a copy, so passing 0 directly here
// skips a second, redundant checkNotCopied call for the same rejection.
// err is expected to be a *errs.E (everything this package's own
// checkNotCopied constructs is); the fallback keeps GR#1's "never a bare
// error" promise even if that ever stops being true.
func errRefResult(cmd protocol.Command, err error) protocol.CommandResult {
	var ref *protocol.ErrorRef
	if e, ok := err.(*errs.E); ok {
		ref = &protocol.ErrorRef{Code: e.Code, Display: e.Display()}
	} else {
		wrapped := errs.Wrap(codeStubEngineCopied, string(cmd.CorrelationID), err, nil)
		ref = &protocol.ErrorRef{Code: wrapped.Code, Display: wrapped.Display()}
	}
	return protocol.CommandResult{
		CorrelationID: cmd.CorrelationID,
		Tick:          0,
		Accepted:      false,
		Error:         ref,
	}
}
