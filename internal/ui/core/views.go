package core

import (
	"encoding/json"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ViewModels is a snapshot of everything T-VIEWS has learned from the
// Delta stream: the latest patch per subscription, the tick it landed
// at, and whether that subscription is currently considered stale (a
// Seq gap was observed and no in-order Delta has arrived since).
//
// A *ViewModels value, once published via ViewStore, is treated as
// immutable by convention — T-RENDER only ever reads it (Front()); only
// ViewsLoop constructs new ones (via clone) and publishes them. This
// immutable-snapshot-plus-atomic-pointer-swap discipline is what makes
// AC-4's "no torn reads" true without a mutex on the hot render path.
type ViewModels struct {
	Patches map[protocol.SubscriptionID]json.RawMessage
	Tick    map[protocol.SubscriptionID]protocol.Tick
	Stale   map[protocol.SubscriptionID]bool
}

func newViewModels() *ViewModels {
	return &ViewModels{
		Patches: make(map[protocol.SubscriptionID]json.RawMessage),
		Tick:    make(map[protocol.SubscriptionID]protocol.Tick),
		Stale:   make(map[protocol.SubscriptionID]bool),
	}
}

// clone returns a shallow copy of v: new maps, same (immutable-by-
// convention) json.RawMessage values. Cheap enough for the "publish a
// fresh snapshot on every applied delta" policy ViewsLoop uses — view
// counts are small (a handful of live F-screen subscriptions, UI-SPEC
// §6), not per-cell.
func (v *ViewModels) clone() *ViewModels {
	out := newViewModels()
	for k, val := range v.Patches {
		out.Patches[k] = val
	}
	for k, val := range v.Tick {
		out.Tick[k] = val
	}
	for k, val := range v.Stale {
		out.Stale[k] = val
	}
	return out
}

// AnyStale reports whether any subscription in v is currently stale —
// the input to UI-SPEC §1's status-bar staleness dot.
func (v *ViewModels) AnyStale() bool {
	for _, s := range v.Stale {
		if s {
			return true
		}
	}
	return false
}

// ViewStore holds the published front ViewModels, read by T-RENDER and
// written (published) by T-VIEWS. The zero value is not usable — use
// NewViewStore.
type ViewStore struct {
	front atomic.Pointer[ViewModels]
}

// NewViewStore returns a ViewStore with an empty, non-nil initial
// snapshot published (so Front() is never nil, even before T-VIEWS has
// processed its first Delta).
func NewViewStore() *ViewStore {
	s := &ViewStore{}
	s.front.Store(newViewModels())
	return s
}

// Front returns the most recently published ViewModels snapshot. Safe
// for concurrent use; callers must not mutate the returned value (see
// ViewModels' doc comment).
func (s *ViewStore) Front() *ViewModels { return s.front.Load() }

// publish atomically swaps in vm as the new front snapshot.
func (s *ViewStore) publish(vm *ViewModels) { s.front.Store(vm) }

// ViewsLoop is T-VIEWS: it consumes protocol.Transport.Deltas(), tracks
// per-subscription Seq gaps with a protocol.SeqTracker, applies each
// Delta's Patch to its own exclusively-owned working copy, and publishes
// an immutable snapshot to a ViewStore after every applied (or dropped)
// Delta.
//
// ui.core does not interpret Patch payloads — that is each view's own
// schema, owned by the engine module that produces it (protocol's Delta
// doc comment) and consumed by the F-screen that owns that view
// (MOD-010 and later screen modules). This package's job stops at
// "is this valid JSON, and is it in sequence" — AC-9's "malformed" case
// is exactly and only that check at this layer.
type ViewsLoop struct {
	transport protocol.Transport
	seq       *protocol.SeqTracker
	store     *ViewStore
	back      *ViewModels // exclusively mutated on this loop's own goroutine
	// correlationID is used only for the MET-U002 malformed-delta log
	// entries this loop raises; it does not claim to be the correlation
	// ID of any specific command (a delta is not necessarily caused by
	// one — see protocol.Delta.CorrelationID's doc comment). A fixed
	// per-loop ID is enough to trace "which UI process-domain instance
	// logged this" without over-claiming per-delta causality.
	correlationID string
}

// NewViewsLoop constructs a ViewsLoop reading from transport and
// publishing to store. correlationID is used for this loop's own
// registry-sourced log entries (see ViewsLoop's doc comment); pass
// errs.NewCorrelationID() if the caller has no more specific ID to
// thread through.
func NewViewsLoop(transport protocol.Transport, store *ViewStore, correlationID string) *ViewsLoop {
	return &ViewsLoop{
		transport:     transport,
		seq:           protocol.NewSeqTracker(),
		store:         store,
		back:          newViewModels(),
		correlationID: correlationID,
	}
}

// Run drains transport.Deltas() until it's closed or stop is closed.
// Intended to run in its own goroutine (T-VIEWS).
func (l *ViewsLoop) Run(stop <-chan struct{}) {
	deltas := l.transport.Deltas()
	for {
		select {
		case <-stop:
			return
		case d, ok := <-deltas:
			if !ok {
				return
			}
			l.apply(d)
		}
	}
}

// apply handles one Delta: gap/staleness tracking (UI-SPEC §1), patch
// validation (AC-9), and publishing the resulting snapshot.
func (l *ViewsLoop) apply(d protocol.Delta) {
	gap, ok := l.seq.Observe(d.SubscriptionID, d.Seq)
	if !ok {
		// Duplicate/out-of-order arrival. protocol.SeqTracker's doc comment
		// notes this "should be impossible in v1" given InProcTransport's
		// single-writer-per-subscription design; treat it as a dropped,
		// logged delta rather than trusting a value that may not reflect
		// the true latest state, consistent with AC-9's "logged and
		// dropped, without crashing or corrupting other view models."
		l.logMalformed(d, "duplicate or out-of-order Seq")
		return
	}
	l.back.Stale[d.SubscriptionID] = gap > 0

	if !json.Valid(d.Patch) {
		l.logMalformed(d, "patch is not valid JSON")
		return
	}

	l.back.Patches[d.SubscriptionID] = d.Patch
	l.back.Tick[d.SubscriptionID] = d.Tick
	l.store.publish(l.back.clone())
}

func (l *ViewsLoop) logMalformed(d protocol.Delta, cause string) {
	_ = errs.New("MET-U002", l.correlationID, map[string]any{
		"subscriptionId": string(d.SubscriptionID),
		"tick":           int64(d.Tick),
		"cause":          cause,
	})
	// Still publish, so a malformed delta's staleness update (if any) is
	// visible even though its Patch was dropped.
	l.store.publish(l.back.clone())
}
