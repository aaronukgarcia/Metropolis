package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// defaultPendingResultTTLTicks is how many simulation Ticks a registered
// CommandResult owner (RegisterResultHandler) is kept waiting before it is
// pruned as stale (doc.go's "Tick-based staleness" note). Chosen
// generously for skeleton-era traffic — the same reasoning
// transport.go's Default*Buffer constants use — revisit once real
// tick-to-result latency numbers exist.
const defaultPendingResultTTLTicks protocol.Tick = 64

// ResultReceiver is implemented by whatever object should receive a
// routed protocol.CommandResult — typically a screen's ApplyResult
// method, adapted, so this package never imports a concrete screen
// package (doc.go's "only finance is a real consumer today" note).
type ResultReceiver interface {
	ApplyResult(protocol.CommandResult)
}

// DeltaReceiver is implemented by whatever object should receive a routed
// protocol.Delta for one subscription — typically a screen's ApplyDelta
// method, adapted.
type DeltaReceiver interface {
	ApplyDelta(protocol.Delta)
}

// EventReceiver is implemented by whatever object should receive a routed
// protocol.Event — typically ui.screen.ticker's ingest method or
// ui.alerts' crisis-stack ingest method, adapted.
type EventReceiver interface {
	ApplyEvent(protocol.Event)
}

// pendingResult is one CorrelationID's registered result owner, with the
// Tick it was registered at (for Tick-based TTL pruning — never
// time.Now, GR#21).
type pendingResult struct {
	receiver     ResultReceiver
	registeredAt protocol.Tick
}

// eventRoute is one entry in the ordered event-routing table
// (RegisterEventRoute) — a slice, never a map, so dispatch order is
// registration order, byte-identical across runs (GR#21/§7; the ICD's
// "never a map range" discipline, matching compose.go's registrationOrder
// and ui/core's sorted-subscription iteration).
type eventRoute struct {
	prefix   string
	receiver EventReceiver
}

// Router is ui.router's Router: New(transport, opts...) constructs one;
// Run(ctx) drains transport.Results()/Deltas()/Events() on the caller's
// goroutine (intended to be the ONE dedicated T-VIEWS-absorbing goroutine
// per running process — doc.go explains why this package never composes
// with a second reader of the same transport) and dispatches each message
// per the registered routing tables. Screens register handlers via
// RegisterResultHandler/BindSubscription/RegisterEventRoute; they never
// read Transport's channels directly (code.json's registered inbound
// pattern for ui.router).
//
// The zero value is not usable — use New. Router must not be copied by
// value after construction (checkNotCopied, copyguard.go) — always use
// the *Router New returned.
type Router struct {
	transport protocol.Transport

	// resultsCh/eventsCh/deltasCh are cached from transport at
	// construction so ResultBufferOccupancy (and Run) don't need to
	// re-fetch them, and so cap()/len() (used for occupancy) are read off
	// a stable channel value.
	resultsCh <-chan protocol.CommandResult
	eventsCh  <-chan protocol.Event
	deltasCh  <-chan protocol.Delta

	seq             *protocol.SeqTracker
	correlationID   string
	pendingTTLTicks protocol.Tick

	mu          sync.Mutex
	pending     map[protocol.CorrelationID]pendingResult
	subs        map[protocol.SubscriptionID]DeltaReceiver
	eventRoutes []eventRoute // registration order — never map range
	currentTick protocol.Tick

	// stale is per-subscription staleness (BUG-359): true once a Seq gap
	// is observed on a subscription's Delta stream, cleared again by the
	// next in-order Delta — exactly ui.core ViewsLoop.apply's
	// `Stale[subID] = gap > 0` discipline (views.go), the state UI-SPEC §1's
	// staleness dot reads. ViewsLoop published this on a ViewStore snapshot;
	// this binary replaced ViewsLoop with Router (see doc.go), so Router now
	// owns it and exposes it via SubscriptionStale. Seq-derived, never
	// time.Now (GR#21). Guarded by mu.
	stale map[protocol.SubscriptionID]bool

	deltaGaps   atomic.Uint64
	routeMisses atomic.Uint64
	panics      atomic.Uint64

	// self mirrors InProcTransport.self (internal/protocol/transport.go)
	// — see copyguard.go's checkNotCopied doc comment for the full
	// rationale. Set exactly once, at the end of New, before r is
	// returned to any caller.
	self atomic.Pointer[Router]
}

// Option configures a Router at construction. See WithCorrelationID and
// WithPendingResultTTL.
type Option func(*Router)

// WithCorrelationID sets the correlation ID this Router's own
// registry-sourced log entries (route misses, delta gaps, malformed
// deltas) are tagged with. Defaults to a freshly minted
// errs.NewCorrelationID() if not supplied — this ID identifies "which
// router instance logged this," not any individual routed message's own
// causality (mirrors ui.core's ViewsLoop.correlationID exactly, same
// rationale).
func WithCorrelationID(id string) Option {
	return func(r *Router) { r.correlationID = id }
}

// WithPendingResultTTL overrides defaultPendingResultTTLTicks — the
// number of simulation Ticks a RegisterResultHandler registration is kept
// before being pruned as stale. ticks <= 0 disables pruning entirely
// (registrations are kept forever, at the caller's own leak risk) —
// useful for tests that want to assert a specific pending registration is
// never pruned mid-test regardless of Tick values observed.
func WithPendingResultTTL(ticks protocol.Tick) Option {
	return func(r *Router) { r.pendingTTLTicks = ticks }
}

// New constructs a Router reading from transport. Call Run to start
// draining; call the Register*/Bind* methods (before or after Run starts
// — all are safe for concurrent use) to populate the routing tables.
func New(transport protocol.Transport, opts ...Option) *Router {
	r := &Router{
		transport:       transport,
		resultsCh:       transport.Results(),
		eventsCh:        transport.Events(),
		deltasCh:        transport.Deltas(),
		seq:             protocol.NewSeqTracker(),
		pendingTTLTicks: defaultPendingResultTTLTicks,
		pending:         make(map[protocol.CorrelationID]pendingResult),
		subs:            make(map[protocol.SubscriptionID]DeltaReceiver),
		stale:           make(map[protocol.SubscriptionID]bool),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.correlationID == "" {
		r.correlationID = errs.NewCorrelationID()
	}
	// Stored exactly once, here, before r is returned to any caller — no
	// goroutine can have a reference to r to race this Store against
	// (mirrors InProcTransport.self / SEC-020 family — see self's doc
	// comment above).
	r.self.Store(r)
	return r
}

// RegisterResultHandler registers receiver as the owner of correlationID:
// the NEXT protocol.CommandResult carrying that exact CorrelationID is
// routed to receiver.ApplyResult and the registration is then consumed
// (deleted) — one CommandResult per registered CorrelationID, matching
// the protocol's own "CommandResult is the direct acknowledgement of ONE
// Command" contract (envelope.go). Callers register BEFORE (or at) the
// moment they call transport.SendCommand with that same CorrelationID, so
// the router is guaranteed to know the owner before any result could
// possibly arrive.
func (r *Router) RegisterResultHandler(correlationID protocol.CorrelationID, receiver ResultReceiver) {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "RegisterResultHandler"}); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[correlationID] = pendingResult{receiver: receiver, registeredAt: r.currentTick}
}

// BindSubscription registers receiver as the persistent owner of every
// Delta for subscriptionID — one receiver per live subscription, matching
// ui.core's ViewsLoop's per-subscription model (each SubscriptionID
// belongs to exactly one subscribing screen). Re-binding the same
// subscriptionID replaces the previous receiver (the last bind wins), so
// a screen that re-subscribes after Unsubscribe simply binds again.
func (r *Router) BindSubscription(subscriptionID protocol.SubscriptionID, receiver DeltaReceiver) {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs[subscriptionID] = receiver
	// A fresh binding starts a fresh Seq expectation for this
	// subscription (mirrors ui.core's Unsubscribe/Reset note,
	// subscription.go) — a re-bind should not be judged against
	// whatever Seq the PREVIOUS binding last observed.
	r.seq.Reset(subscriptionID)
}

// RegisterEventRoute appends (prefix, receiver) to the ordered event
// route table (eventRoute, above). Every registered entry whose prefix is
// a prefix of an arriving Event.Kind (strings.HasPrefix) is dispatched
// to, in REGISTRATION ORDER — never map range — so multiple surfaces
// (e.g. ui.screen.ticker's kind-prefix routing and ui.alerts' severity/
// crisis routing, decided inside its own ApplyEvent) can both receive the
// same Event, deterministically. An empty prefix matches every Event.Kind
// (a catch-all route).
func (r *Router) RegisterEventRoute(kindPrefix string, receiver EventReceiver) {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "RegisterEventRoute"}); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventRoutes = append(r.eventRoutes, eventRoute{prefix: kindPrefix, receiver: receiver})
}

// Run drains transport.Results()/Deltas()/Events() until ctx is
// cancelled or all three channels have been closed (transport.Close()
// was called and every in-flight message drained). Intended to run on
// its own dedicated goroutine — the ONE T-VIEWS-absorbing reader for this
// Router's transport (doc.go).
func (r *Router) Run(ctx context.Context) error {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "Run"}); err != nil {
		return err
	}

	results := r.resultsCh
	events := r.eventsCh
	deltas := r.deltasCh

	for {
		if results == nil && events == nil && deltas == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			r.handleResult(res)
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			r.handleEvent(e)
		case d, ok := <-deltas:
			if !ok {
				deltas = nil
				continue
			}
			r.handleDelta(d)
		}
	}
}

// handleResult routes one CommandResult to its registered owner, or logs
// a routing-table miss (MET-V400) if none is registered. Also opportunistically
// prunes any pending registrations that have exceeded pendingTTLTicks
// (Tick-based staleness, GR#21).
func (r *Router) handleResult(res protocol.CommandResult) {
	r.mu.Lock()
	r.advanceTickLocked(res.Tick)
	entry, ok := r.pending[res.CorrelationID]
	if ok {
		delete(r.pending, res.CorrelationID)
	}
	r.pruneStaleLocked()
	r.mu.Unlock()

	if !ok {
		r.routeMisses.Add(1)
		_ = errs.New(ErrRouteMiss, r.correlationID, map[string]any{
			"kind": "result",
			"key":  string(res.CorrelationID),
			"tick": int64(res.Tick),
		})
		return
	}
	r.invokeResultReceiver(entry.receiver, res)
}

// handleDelta applies protocol.SeqTracker gap/duplicate detection (exactly
// ui.core's ViewsLoop.apply's own discipline, subscription.go), validates
// the patch is well-formed JSON, and routes it to the subscription's
// bound receiver — or logs a routing-table miss if none is bound.
func (r *Router) handleDelta(d protocol.Delta) {
	gap, ok := r.seq.Observe(d.SubscriptionID, d.Seq)

	r.mu.Lock()
	r.advanceTickLocked(d.Tick)
	receiver := r.subs[d.SubscriptionID]
	// BUG-359: record per-subscription staleness exactly as ui.core's
	// ViewsLoop.apply did (`Stale[subID] = gap > 0`, views.go) — but only
	// for an in-order (ok) delta. A duplicate/out-of-order arrival (!ok)
	// returns below without trusting its Seq, so it must not touch the
	// staleness flag either, matching ViewsLoop's own early-return there.
	if ok {
		r.stale[d.SubscriptionID] = gap > 0
	}
	r.pruneStaleLocked()
	r.mu.Unlock()

	if !ok {
		// Duplicate/out-of-order arrival. ui.core's ViewsLoop.apply
		// treats this "should be impossible in v1" case (InProcTransport's
		// single-writer-per-subscription design) as a dropped, logged
		// delta rather than trusting a value that may not reflect the
		// true latest state — same discipline here, reusing ui.core's
		// MET-U002 unchanged (ICD §8: "the latter is reused unchanged for
		// a malformed routed delta").
		_ = errs.New("MET-U002", r.correlationID, map[string]any{
			"subscriptionId": string(d.SubscriptionID),
			"tick":           int64(d.Tick),
			"cause":          "duplicate or out-of-order Seq",
		})
		return
	}

	if gap > 0 {
		r.deltaGaps.Add(gap)
		_ = errs.New(ErrDeltaGap, r.correlationID, map[string]any{
			"subscriptionId": string(d.SubscriptionID),
			"gap":            int64(gap),
			"seq":            int64(d.Seq),
			"tick":           int64(d.Tick),
		})
	}

	if !json.Valid(d.Patch) {
		_ = errs.New("MET-U002", r.correlationID, map[string]any{
			"subscriptionId": string(d.SubscriptionID),
			"tick":           int64(d.Tick),
			"cause":          "patch is not valid JSON",
		})
		return
	}

	if receiver == nil {
		r.routeMisses.Add(1)
		_ = errs.New(ErrRouteMiss, r.correlationID, map[string]any{
			"kind": "delta",
			"key":  string(d.SubscriptionID),
			"tick": int64(d.Tick),
		})
		return
	}
	r.invokeDeltaReceiver(receiver, d)
}

// handleEvent dispatches one Event to every registered route whose prefix
// matches, in registration order (never map range), or logs a
// routing-table miss if none matched at all.
func (r *Router) handleEvent(e protocol.Event) {
	r.mu.Lock()
	r.advanceTickLocked(e.Tick)
	routes := make([]eventRoute, len(r.eventRoutes))
	copy(routes, r.eventRoutes)
	r.pruneStaleLocked()
	r.mu.Unlock()

	matched := false
	for _, rt := range routes {
		if strings.HasPrefix(e.Kind, rt.prefix) {
			matched = true
			r.invokeEventReceiver(rt.receiver, e)
		}
	}
	if !matched {
		r.routeMisses.Add(1)
		_ = errs.New(ErrRouteMiss, r.correlationID, map[string]any{
			"kind": "event",
			"key":  e.Kind,
			"tick": int64(e.Tick),
		})
	}
}

// invokeResultReceiver calls receiver.ApplyResult(res), recovering from
// any panic the receiver raises (recoverReceiverPanic, below) so a bug in
// one screen's ApplyResult can never crash the whole UI process or stop
// this Router from routing subsequent messages (GR#1; doc.go's
// "recover-log-continue" policy).
func (r *Router) invokeResultReceiver(receiver ResultReceiver, res protocol.CommandResult) {
	defer r.recoverReceiverPanic("result", string(res.CorrelationID), res.Tick)
	receiver.ApplyResult(res)
}

// invokeDeltaReceiver is invokeResultReceiver's sibling for ApplyDelta.
func (r *Router) invokeDeltaReceiver(receiver DeltaReceiver, d protocol.Delta) {
	defer r.recoverReceiverPanic("delta", string(d.SubscriptionID), d.Tick)
	receiver.ApplyDelta(d)
}

// invokeEventReceiver is invokeResultReceiver's sibling for ApplyEvent.
func (r *Router) invokeEventReceiver(receiver EventReceiver, e protocol.Event) {
	defer r.recoverReceiverPanic("event", e.Kind, e.Tick)
	receiver.ApplyEvent(e)
}

// recoverReceiverPanic is the recover() point every invoke*Receiver
// method defers. If the just-returned/panicking receiver call panicked,
// it recovers, increments PanicCount, and raises MET-V403 (ErrReceiverPanic)
// naming the receiver kind, routing key, and tick -- never silently
// (GR#1/GR#17). If the receiver call did NOT panic, recover() returns nil
// and this is a no-op, so the common (no-panic) path pays only the cost
// of one deferred function call, no allocation.
//
// This is the ONLY recover() in this package, deliberately placed at the
// single choke point every receiver invocation passes through, rather
// than duplicated per handle* method -- doc.go's "one poisoned message
// never stops routing" policy applies uniformly to all three message
// kinds because all three route through here.
func (r *Router) recoverReceiverPanic(kind, key string, tick protocol.Tick) {
	if rec := recover(); rec != nil {
		r.panics.Add(1)
		_ = errs.New(ErrReceiverPanic, r.correlationID, map[string]any{
			"kind":  kind,
			"key":   key,
			"tick":  int64(tick),
			"cause": fmt.Sprint(rec),
		})
	}
}

// advanceTickLocked updates r.currentTick if tick is newer. Called with
// r.mu held. Ticks are sim-clock-derived (protocol.Tick's own doc
// comment); this is the ONLY place Router tracks "how far has the
// simulation gotten," and it is driven entirely by values carried on
// routed messages — never time.Now (GR#21).
func (r *Router) advanceTickLocked(tick protocol.Tick) {
	if tick > r.currentTick {
		r.currentTick = tick
	}
}

// pruneStaleLocked evicts any pending result registration whose
// registeredAt is more than pendingTTLTicks behind currentTick. Called
// with r.mu held, from every handle* method's own tick-update, so pruning
// piggybacks on ordinary message traffic rather than needing a separate
// ticker goroutine (this package owns exactly one goroutine — doc.go).
func (r *Router) pruneStaleLocked() {
	if r.pendingTTLTicks <= 0 {
		return
	}
	cutoff := r.currentTick - r.pendingTTLTicks
	for corrID, p := range r.pending {
		if p.registeredAt < cutoff {
			delete(r.pending, corrID)
			_ = errs.New(ErrRouteMiss, r.correlationID, map[string]any{
				"kind": "result-stale-pruned",
				"key":  string(corrID),
				"tick": int64(r.currentTick),
			})
		}
	}
}

// DeltaGapCount returns the cumulative number of skipped Delta.Seq values
// this Router has observed across every subscription (ICD §10's "drop/gap
// rate" monitoring signal). Safe for concurrent use.
//
// checkNotCopied guarded (SEC-020 family): unlike the unexported handle*/
// invoke*/*Locked helpers (astgate-accepted, accepted-findings.json —
// reached only through Run's own guarded entry), this is a directly
// callable EXPORTED method any caller (monitoring code, tests) can invoke
// on whatever *Router value it holds — including a struct copy — so it
// gets its own guard, mirroring build.Screen.Stale()'s identical pattern
// for a simple no-lock accessor.
func (r *Router) DeltaGapCount() uint64 {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "DeltaGapCount"}); err != nil {
		return 0
	}
	return r.deltaGaps.Load()
}

// SubscriptionStale reports whether subscriptionID's Delta stream is
// currently considered stale: a Seq gap was observed and no in-order Delta
// has arrived since (BUG-359). This is the per-subscription input to
// UI-SPEC §1's staleness dot — the state ui.core's ViewsLoop used to
// publish on a ViewStore snapshot before Router replaced it as this
// binary's transport consumer (doc.go). A subscription never seen (or one
// whose only Deltas were in-order) reports false. Safe for concurrent use;
// checkNotCopied guarded and lock-taking — mirrors PendingResultCount's
// identical double-checked accessor pattern.
func (r *Router) SubscriptionStale(subscriptionID protocol.SubscriptionID) bool {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "SubscriptionStale"}); err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "SubscriptionStale"}); err != nil {
		return false
	}
	return r.stale[subscriptionID]
}

// RouteMissCount returns the cumulative number of routing-table misses
// (unregistered CorrelationID/SubscriptionID/Event.Kind, plus stale-pruned
// result registrations) this Router has raised MET-V400 for. Safe for
// concurrent use. checkNotCopied guarded — see DeltaGapCount's doc comment.
func (r *Router) RouteMissCount() uint64 {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "RouteMissCount"}); err != nil {
		return 0
	}
	return r.routeMisses.Load()
}

// PanicCount returns the cumulative number of registered-receiver panics
// this Router has recovered from (recoverReceiverPanic, above; MET-V403).
// A non-zero value means some screen's ApplyResult/ApplyDelta/ApplyEvent
// panicked -- the router survived and kept routing, but the panicking
// receiver's own bug still needs fixing (doc.go's policy). Safe for
// concurrent use. checkNotCopied guarded — see DeltaGapCount's doc comment.
func (r *Router) PanicCount() uint64 {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "PanicCount"}); err != nil {
		return 0
	}
	return r.panics.Load()
}

// PendingResultCount returns the number of CorrelationIDs currently
// registered via RegisterResultHandler awaiting their CommandResult. Safe
// for concurrent use — used by tests and monitoring to observe pruning
// behaviour. checkNotCopied guarded, double-checked around the lock
// (pre-lock per SEC-016, and again after acquiring r.mu, defence in
// depth) — mirrors build.Screen.Stale()'s identical lock-taking-accessor
// pattern exactly.
func (r *Router) PendingResultCount() int {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "PendingResultCount"}); err != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "PendingResultCount"}); err != nil {
		return 0
	}
	return len(r.pending)
}

// ResultBufferOccupancy reports the transport's Results() channel
// occupancy as a fraction in [0, 1] — cap()==0 reports 0 rather than
// dividing by zero. This is the "surface result-buffer pressure" signal
// ICD §9 requires (CommandResult drop is the worst UX gap of the three
// outbound kinds; v1 has no per-kind drop policy, but pressure must at
// least be observable). Safe for concurrent use — cap/len on a channel
// are always safe to read concurrently with sends/receives on it.
// checkNotCopied guarded — see DeltaGapCount's doc comment.
func (r *Router) ResultBufferOccupancy() float64 {
	if err := r.checkNotCopied(r.correlationID, map[string]any{"method": "ResultBufferOccupancy"}); err != nil {
		return 0
	}
	c := cap(r.resultsCh)
	if c <= 0 {
		return 0
	}
	return float64(len(r.resultsCh)) / float64(c)
}
