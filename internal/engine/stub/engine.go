package stub

import (
	"context"
	"sync"
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
	return s, nil
}

// World returns the loaded Folkestone-64 fixture (AC-3).
func (s *StubEngine) World() *World { return s.world }

// Tick returns the current fake-tick counter (AC-4).
func (s *StubEngine) Tick() protocol.Tick {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tick
}

// Run drives the engine loop: range over t.Commands(), dispatch and
// answer each one, until ctx is cancelled or the transport is closed
// (Commands() channel closes). It is the only goroutine that reads
// commands or mutates tick/speed/paused/subs directly — chaos's delayed
// sends (chaos.go) run in their own goroutines but only ever call the
// transport's non-blocking SendDelta, never touch engine state.
func (s *StubEngine) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cmd, ok := <-s.transport.Commands():
			if !ok {
				return nil
			}
			s.handle(cmd)
		}
	}
}

// handle dispatches one already-validated Command (protocol.Command.Validate
// ran inside InProcTransport.SendCommand before it ever reached
// s.transport.Commands()) and answers it with a CommandResult.
func (s *StubEngine) handle(cmd protocol.Command) {
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

	s.mu.Lock()
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
	s.mu.Lock()
	s.speed = p.Speed
	tick := s.tick
	s.mu.Unlock()
	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handlePause(cmd protocol.Command) protocol.CommandResult {
	s.mu.Lock()
	s.paused = true
	tick := s.tick
	s.mu.Unlock()
	return s.acceptAt(cmd, tick)
}

func (s *StubEngine) handleResume(cmd protocol.Command) protocol.CommandResult {
	s.mu.Lock()
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

	s.mu.Lock()
	id := s.allocID.Allocate()
	sub := &subState{id: id, viewName: p.ViewName}
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

	s.mu.Lock()
	_, exists := s.subs[p.SubscriptionID]
	if !exists {
		s.mu.Unlock()
		return s.reject(cmd, codeInvalidPayload, map[string]any{"subscriptionId": string(p.SubscriptionID), "cause": "unknown subscription"})
	}
	delete(s.subs, p.SubscriptionID)
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
			delay := s.rng.nextDelay(s.chaos.DelayedDeltas.MinDelay, s.chaos.DelayedDeltas.MaxDelay)
			go func(d protocol.Delta, delay time.Duration) {
				time.Sleep(delay)
				s.transport.SendDelta(d)
			}(d, delay)
			continue
		}
		s.transport.SendDelta(d)
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
