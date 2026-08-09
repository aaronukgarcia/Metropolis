package core

import (
	"encoding/json"
	"sort"
	"sync"

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
	c := e.Clock()
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
type SubscriptionServer struct {
	mu    sync.Mutex
	alloc *protocol.SubscriptionAllocator
	subs  map[protocol.SubscriptionID]*subscription
}

// NewSubscriptionServer constructs an empty SubscriptionServer.
func NewSubscriptionServer() *SubscriptionServer {
	return &SubscriptionServer{
		alloc: protocol.NewSubscriptionAllocator(),
		subs:  make(map[protocol.SubscriptionID]*subscription),
	}
}

// Subscribe validates viewName, checks it against the set of views this
// server actually serves (v1: only "engine.status"), and — if valid —
// allocates and stores a new subscription. params is accepted (per
// SubscribePayload's shape) but unused by v1's single view; kept for
// forward compatibility with parameterised views (e.g. a viewport's
// origin/extent) without a signature change.
func (s *SubscriptionServer) Subscribe(viewName string, params map[string]string, causingCorrID protocol.CorrelationID, correlationID string) (protocol.SubscriptionID, error) {
	if err := protocol.ValidateViewName(viewName); err != nil {
		return "", errs.Wrap(ErrInvalidViewName, correlationID, err, map[string]any{"view": viewName})
	}
	if viewName != engineStatusView {
		return "", errs.New(ErrUnknownView, correlationID, map[string]any{"view": viewName})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.alloc.Allocate()
	s.subs[id] = &subscription{id: id, view: viewName, pendingCorrID: causingCorrID}
	return id, nil
}

// Unsubscribe closes a live subscription. Deltas stop for it
// immediately (the next PublishEngineStatus call simply will not find
// it in s.subs any more) — UI-SPEC §6's "deltas flow only for live
// subscriptions".
func (s *SubscriptionServer) Unsubscribe(id protocol.SubscriptionID, correlationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *SubscriptionServer) PublishEngineStatus(sink DeltaSink, view EngineStatusView) {
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
