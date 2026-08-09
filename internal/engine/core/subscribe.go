package core

import (
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is T-SUBSCR (M0-ENG §1.1): the view-subscription server
// that manages SubscriptionIDs and per-subscription Seq, and computes/
// pushes deltas off the main tick/command path (AC-7). v1 serves
// exactly one view, "engine.status" (tick, month, speed, and module
// statuses from the registry) as a whole-state JSON patch per delta —
// a trivial but valid patch strategy for a view this small; per-field
// diffing is future work once a view is large enough to matter.
//
// engineStatusView is the wire name the acceptance doc's AC-7 calls it
// by.
const engineStatusView = "engine.status"

// EngineStatusView is v1's only served view's payload shape.
type EngineStatusView struct {
	Tick    int64          `json:"tick"`
	Month   int64          `json:"month"`
	Speed   int            `json:"speed"`
	Paused  bool           `json:"paused"`
	Modules []ModuleStatus `json:"modules"`
}

// ModuleStatus is one registry.ModuleEntry's projection into the
// engine.status view.
type ModuleStatus struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Health string `json:"health"`
}

// EngineStatusView computes the current engine.status view from the
// clock and the module registry. Cheap (a handful of int/bool reads
// plus registry.List's already-sorted copy) — safe to call from the
// subscription pump goroutine on every wake.
func (e *Engine) EngineStatusView() EngineStatusView {
	c, err := e.Clock()
	if err != nil {
		// e is a struct-copied Engine (SEC-018/SEC-014) — Clock() itself
		// is guarded and returns instantly rather than hanging, but this
		// method has no correlation ID or caller to report the failure
		// to: it is called from the subscription pump goroutine
		// (commands.go's StartSubscriptionPump) on every wake, with no
		// per-call context, the same "no reporting channel" situation
		// PublishEngineStatus's own json.Marshal branch below documents.
		// A copy can never legitimately reach this point in practice —
		// nothing that constructs or drives a real subscription pump
		// does so with a copied Engine — so the zero-value Clock is used
		// and the view degrades to zeroed fields rather than hanging.
		c = Clock{}
	}
	entries := e.registry.List() // already sorted by key (registry.go), never a raw map range
	modules := make([]ModuleStatus, len(entries))
	for i, m := range entries {
		modules[i] = ModuleStatus{Key: m.Key, Status: string(m.Status), Health: string(m.Health)}
	}
	return EngineStatusView{
		Tick:    c.Tick(),
		Month:   c.Month(),
		Speed:   int(c.Speed()),
		Paused:  c.Paused(),
		Modules: modules,
	}
}

// subscription is one live view subscription's server-side state.
type subscription struct {
	id            protocol.SubscriptionID
	view          string
	seq           uint64
	pendingCorrID protocol.CorrelationID // echoed on the next delta only, then cleared
}

// SubscriptionServer is T-SUBSCR. It is safe for concurrent use: the
// command loop calls Subscribe/Unsubscribe, the subscription pump
// goroutine calls PublishEngineStatus — both under the same mutex, held
// only for the cost of a map lookup/insert, never across a push.
//
// SEC-019 (same class as SEC-014/SEC-016 on Engine): SubscriptionServer
// and NewSubscriptionServer are both exported, independent of Engine —
// any caller can dereference-and-copy a live *SubscriptionServer
// ('s2 := *s' is legal, unsafe-free, reflect-free Go). mu is a plain
// value, so the copy gets its OWN mu — but subs (a map, a reference
// type) still ALIASES the original's, exactly Engine.hooks's shape. See
// self's doc comment for the fix, which mirrors Engine's checkNotCopied
// pattern (engine.go) rather than inventing a variant.
type SubscriptionServer struct {
	mu    sync.Mutex
	alloc *protocol.SubscriptionAllocator
	subs  map[protocol.SubscriptionID]*subscription

	// self holds the address NewSubscriptionServer gave this
	// SubscriptionServer at construction (self.Store(s), set once, at
	// the end of NewSubscriptionServer, before s is returned to any
	// caller — no goroutine can have a reference to s to race that Store
	// against). atomic.Pointer, not a plain *SubscriptionServer field,
	// for the same SEC-016 reason Engine.self is atomic: a struct copy's
	// mu can be byte-for-byte "currently locked" if the copy was taken
	// while the original's mu was held, and even attempting to acquire
	// (Lock) a copy's own mu in that state can block forever, since
	// nothing will ever Unlock() that specific copy's address. The
	// identity check must therefore be race-safe and run BEFORE mu is
	// ever touched — a plain field read racing a concurrent struct copy
	// has no defined result under the Go memory model, but
	// atomic.Pointer's Load/Store do.
	self atomic.Pointer[SubscriptionServer]
}

// NewSubscriptionServer constructs an empty SubscriptionServer.
func NewSubscriptionServer() *SubscriptionServer {
	s := &SubscriptionServer{
		alloc: protocol.NewSubscriptionAllocator(),
		subs:  make(map[protocol.SubscriptionID]*subscription),
	}
	// Stored exactly once, here, before s is returned to any caller —
	// mirrors Engine's NewEngine (engine.go) for the identical reason
	// (SEC-016).
	s.self.Store(s)
	return s
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other SubscriptionServer value (SEC-019, mirroring Engine.checkNotCopied
// — engine.go). Deliberately lock-free — a single atomic.Pointer.Load,
// requiring nothing else, not s.mu — so it is safe and correct to call
// BEFORE s.mu is ever touched. A nil s.self.Load() (a SubscriptionServer
// constructed as a bare `SubscriptionServer{}`/`new(SubscriptionServer)`
// rather than via NewSubscriptionServer, so self was never stored) is
// treated the same as a mismatch and rejected the same way — every
// documented construction path is NewSubscriptionServer, so an unset
// self is itself a misuse this same error correctly names, and rejecting
// it here also means such a value's zero-value nil subs map is never
// reached either.
func (s *SubscriptionServer) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrSubscriptionServerCopied, correlationID, ctx)
	}
	return nil
}

// Subscribe validates viewName, checks it against the set of views this
// server actually serves (v1: only "engine.status"), and — if valid —
// allocates and stores a new subscription. params is accepted (per
// SubscribePayload's shape) but unused by v1's single view; kept for
// forward compatibility with parameterised views (e.g. a viewport's
// origin/extent) without a signature change.
//
// SEC-019: identity-checked BEFORE s.mu is touched (pre-lock, load-
// bearing) and again immediately after s.mu is acquired (defence in
// depth) — mirrors Engine.RegisterPhaseHook/seal's ordering exactly (see
// checkNotCopied's doc comment for why the pre-lock check must be
// lock-free and must run first).
func (s *SubscriptionServer) Subscribe(viewName string, params map[string]string, causingCorrID protocol.CorrelationID, correlationID string) (protocol.SubscriptionID, error) {
	if err := protocol.ValidateViewName(viewName); err != nil {
		return "", errs.Wrap(ErrInvalidViewName, correlationID, err, map[string]any{"view": viewName})
	}
	if viewName != engineStatusView {
		return "", errs.New(ErrUnknownView, correlationID, map[string]any{"view": viewName})
	}
	if err := s.checkNotCopied(correlationID, map[string]any{"view": viewName}); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(correlationID, map[string]any{"view": viewName}); err != nil {
		return "", err
	}
	id := s.alloc.Allocate()
	s.subs[id] = &subscription{id: id, view: viewName, pendingCorrID: causingCorrID}
	return id, nil
}

// Unsubscribe closes a live subscription. Deltas stop for it
// immediately (the next PublishEngineStatus call simply will not find
// it in s.subs any more) — UI-SPEC §6's "deltas flow only for live
// subscriptions".
//
// SEC-019: identity-checked BEFORE s.mu is touched and again after
// acquisition — see Subscribe's doc comment.
func (s *SubscriptionServer) Unsubscribe(id protocol.SubscriptionID, correlationID string) error {
	if err := s.checkNotCopied(correlationID, map[string]any{"subscriptionId": string(id)}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(correlationID, map[string]any{"subscriptionId": string(id)}); err != nil {
		return err
	}
	if _, ok := s.subs[id]; !ok {
		return errs.New(ErrUnknownSubscription, correlationID, map[string]any{"subscriptionId": string(id)})
	}
	delete(s.subs, id)
	return nil
}

// PublishEngineStatus computes the JSON patch for view once and pushes
// a Delta (with that subscription's own monotonically increasing Seq)
// to every live "engine.status" subscription via sink. Called only from
// the subscription pump goroutine (commands.go's StartSubscriptionPump)
// — never from the phase-pipeline or command-handling call paths — so
// that delta computation and push are always off the main tick path
// (AC-7).
//
// SEC-019: identity-checked BEFORE s.mu is touched (pre-lock, load-
// bearing) and again immediately after acquisition (defence in depth) —
// same ordering as Subscribe/Unsubscribe. Unlike those two,
// PublishEngineStatus has no correlationID parameter (it runs on the
// subscription pump goroutine, off any request's call path — see
// EngineStatusView's doc comment for the identical "no reporting
// channel" situation) and no return value to carry an error through, so
// a copy is handled the same way the unreachable json.Marshal failure
// just below already is: this cycle's publish is silently dropped
// (never a partial/racy one) and the next pump wake retries against
// whichever SubscriptionServer value it is actually called against. A
// copy can never legitimately reach this point in practice — nothing
// that constructs or drives a real subscription pump does so with a
// copied SubscriptionServer (StartSubscriptionPump always closes over
// the one *SubscriptionServer NewEngine's Engine.subs holds).
func (s *SubscriptionServer) PublishEngineStatus(sink DeltaSink, view EngineStatusView) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return
	}

	patch, err := json.Marshal(view)
	if err != nil {
		// Marshalling a plain struct of ints/bools/strings cannot fail;
		// this is unreachable in practice. Per GR#1, degrade loudly
		// rather than panic: skip this publish cycle silently would be
		// a silent failure, so instead we simply drop this cycle's
		// patch (the next pump wake retries with fresh state) — there
		// is no correlation ID or caller to report this to from a
		// background goroutine, which is exactly why the encode must
		// not be able to fail in the first place.
		return
	}

	s.mu.Lock()
	if s.self.Load() != s {
		// Defence in depth (SEC-019): re-checked under the lock in case
		// a future refactor adds another path to s.mu ahead of the
		// pre-lock check above without threading this check through it
		// too — mirrors Engine.RegisterPhaseHook/seal's post-lock
		// re-check. Inlined rather than calling checkNotCopied again so
		// the already-held lock is released correctly before returning.
		s.mu.Unlock()
		return
	}
	// Collect targets deterministically (sorted by SubscriptionID)
	// rather than ranging the map directly — not required by GR#21
	// (this is not the tick/phase path and Deltas do not feed world-
	// snapshot hashing), but kept consistent with this package's
	// house style and makes test assertions/log output reproducible.
	ids := make([]protocol.SubscriptionID, 0, len(s.subs))
	for id, sub := range s.subs {
		if sub.view == engineStatusView {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type target struct {
		id     protocol.SubscriptionID
		seq    uint64
		corrID protocol.CorrelationID
	}
	targets := make([]target, 0, len(ids))
	for _, id := range ids {
		sub := s.subs[id]
		sub.seq++
		targets = append(targets, target{id: id, seq: sub.seq, corrID: sub.pendingCorrID})
		sub.pendingCorrID = "" // echoed at most once, on the first delta after Subscribe
	}
	s.mu.Unlock()

	for _, t := range targets {
		sink.SendDelta(protocol.Delta{
			SubscriptionID: t.id,
			Tick:           protocol.Tick(view.Tick),
			Seq:            t.seq,
			Patch:          patch,
			CorrelationID:  t.corrID,
		})
	}
}
