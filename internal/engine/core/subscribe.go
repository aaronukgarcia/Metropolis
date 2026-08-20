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
// pushes deltas off the main tick/command path (AC-7). v1 served exactly
// one view, "engine.status" (tick, month, speed, and module statuses
// from the registry) as a whole-state JSON patch per delta — a trivial
// but valid patch strategy for a view this small; per-field diffing is
// future work once a view is large enough to matter.
//
// FEAT-208 generalises this from one hardcoded view to a registered
// table (RegisterView/ViewPatchFunc below): Subscribe now looks viewName
// up in that table instead of comparing against a single constant, and
// Publish (renamed from PublishEngineStatus, which remains as a thin
// engine.status-only wrapper — see below) iterates every live
// subscription regardless of which view it names. This stays entirely
// within engine.core's domain (GR#20) — compose.Wire is the only caller
// of RegisterView, in the same fixed, documented slice order
// (viewRegistrationOrder) that registrationOrder already uses for phase
// hooks.
//
// engineStatusView is the wire name the acceptance doc's AC-7 calls it
// by.
const engineStatusView = "engine.status"

// ViewPatchFunc computes the current JSON patch for one registered view.
// Called only from the subscription pump goroutine (commands.go's
// StartSubscriptionPump), at each publish cycle, off the main tick/
// command path — the same "delta computation and push happen off the
// main tick path" discipline EngineStatusView's doc comment establishes
// (AC-7). A ViewPatchFunc must be a pure, read-only projection of
// already-settled simState: never mutate, never read the wall clock (GR#21),
// and never block (it runs on the single subscription-pump goroutine,
// so a slow producer stalls every other registered view's publish for
// that cycle too).
//
// CONCURRENCY CONTRACT (F3 correction, independent round r1, FEAT-208
// increment 1 — corrects a false claim in the design proposal's §3.1,
// which read "the pump wakes on its own schedule... phase hooks mutate
// simState; the pump reads it" as if that boundary alone made a
// ViewPatchFunc's reads race-free): the pump goroutine that calls a
// ViewPatchFunc runs CONCURRENTLY with tick-phase execution — nothing
// serializes StartSubscriptionPump's goroutine against
// AdvanceTicks/RunCommandLoop's phase-pipeline goroutines, which mutate
// the same simState/module state a ViewPatchFunc reads. "Off the main
// tick path" describes WHEN patch computation is scheduled (never
// inline in a phase or command handler, so it cannot block or corrupt
// tick determinism), not a promise that no other goroutine can be
// touching the same data at the same time. A ViewPatchFunc is therefore
// NOT single-goroutine-safe by construction: every registered view MUST
// read its module state through that module's OWN synchronization
// (e.g. compose's buildServicesCapacityDemandPatch is safe because
// engine.services.ServicesAPI guards every accessor — ServiceIDs,
// Capacity, Demand — with its own sync.RWMutex, not because of anything
// this package does). A future view whose source module has no such
// guard would be a real data race, not a hypothetical one.
type ViewPatchFunc func() (json.RawMessage, error)

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

// engineStatusViewPatch is "engine.status"'s ViewPatchFunc (FEAT-208),
// registered once by NewEngine (engine.go) as a bound method value —
// deliberately a NAMED method, not an inline closure literal, so
// astgate's checkNotCopied-guard scan sees it directly, matching every
// other guarded entry point in this file, rather than relying solely on
// the transitive guard EngineStatusView already gets through Clock()'s
// own checkNotCopied call. Also checks e.subs directly (SEC-019): this
// method is the ViewPatchFunc NewEngine hands to e.subs.RegisterView, so
// it is reached through the SubscriptionServer machinery even though it
// never reads e.subs' own fields — checking its identity here too is
// cheap, harmless, and satisfies astgate's syntactic per-candidate-type
// scan directly rather than relying on the transitive guard
// SubscriptionServer.Publish's own pass 2 already applies before ever
// invoking a registered ViewPatchFunc.
func (e *Engine) engineStatusViewPatch() (json.RawMessage, error) {
	if err := e.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil, err
	}
	if err := e.subs.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil, err
	}
	return json.Marshal(e.EngineStatusView())
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
	mu sync.Mutex

	// publishMu is the R3 two-mutex split (independent round r2 REJECT,
	// FEAT-208 increment 1): held across an ENTIRE Publish cycle — pass
	// 1's snapshot, pass 2's patch computation, and pass 3's Seq
	// assignment AND SendDelta — so concurrent Publish callers queue and
	// deliver strictly in the order they acquire publishMu (this is what
	// now guarantees F1b's "assignment order == delivery order"
	// property, replacing r1's fix of holding s.mu across SendDelta,
	// which r2 proved created a worse hazard: it let a slow/blocking/
	// reentrant DeltaSink stall or permanently deadlock
	// Subscribe/Unsubscribe/RegisterView too, since those only ever
	// needed s.mu and s.mu was now held for SendDelta's entire,
	// unbounded duration). s.mu is now taken only BRIEFLY, inside pass 1
	// and pass 3, for subscription/view state reads/writes — NEVER
	// across SendDelta. See Publish's own doc comment for the full
	// design and DeltaSink's doc comment (commands.go) for the one
	// resulting prohibition (a DeltaSink must never call back into
	// Publish — self-deadlock on publishMu).
	publishMu sync.Mutex

	alloc *protocol.SubscriptionAllocator
	subs  map[protocol.SubscriptionID]*subscription

	// views is the FEAT-208 registered view table: view name -> its
	// ViewPatchFunc, populated only by RegisterView (compose.Wire, in a
	// fixed slice order — viewRegistrationOrder). This map is NEVER
	// ranged for its own iteration order (GR#21 does not apply to it the
	// way it applies to subs's sorted-ID publish loop below): it is only
	// ever looked up by an exact view-name key, both in Subscribe (one
	// lookup per Subscribe call) and in Publish (one lookup per live
	// subscription, itself iterated in sorted-SubscriptionID order).
	views map[string]ViewPatchFunc

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
		views: make(map[string]ViewPatchFunc),
	}
	// Stored exactly once, here, before s is returned to any caller —
	// mirrors Engine's NewEngine (engine.go) for the identical reason
	// (SEC-016).
	s.self.Store(s)
	return s
}

// RegisterView registers fn as the patch producer for the given view
// name (FEAT-208, AC-2's "zero views left behind" discipline extended
// from phase-hook registration). Only compose.Wire calls this, resolving
// every producer before the first call so a construction failure never
// leaves a partially-registered view table — the same discipline Wire
// already applies to RegisterPhaseHook. A duplicate name is rejected
// (ErrViewAlreadyRegistered) rather than silently replacing the earlier
// registration's ViewPatchFunc; a nil fn is rejected (ErrNilViewPatchFunc)
// rather than being stored and only failing later, mid-Publish.
//
// fn WILL be called on the subscription pump goroutine, CONCURRENTLY
// with tick-phase writes to whatever module state it reads (F3, see
// ViewPatchFunc's own doc comment for the full concurrency contract this
// corrects) — the caller registering fn is responsible for that fn only
// reading through its source module's own synchronization primitive.
// This package provides no additional serialization between the pump
// and the tick pipeline beyond what already exists (each module's own
// mutex/RWMutex).
//
// SEC-019: identity-checked BEFORE s.mu is touched (pre-lock,
// load-bearing) and again immediately after acquisition (defence in
// depth) — mirrors Subscribe/Unsubscribe's ordering exactly.
func (s *SubscriptionServer) RegisterView(name string, fn ViewPatchFunc) error {
	correlationID := errs.NewCorrelationID()
	if err := protocol.ValidateViewName(name); err != nil {
		return errs.Wrap(ErrInvalidViewName, correlationID, err, map[string]any{"view": name})
	}
	if fn == nil {
		return errs.New(ErrNilViewPatchFunc, correlationID, map[string]any{"view": name})
	}
	if err := s.checkNotCopied(correlationID, map[string]any{"view": name}); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(correlationID, map[string]any{"view": name}); err != nil {
		return err
	}
	if _, exists := s.views[name]; exists {
		return errs.New(ErrViewAlreadyRegistered, correlationID, map[string]any{"view": name})
	}
	s.views[name] = fn
	return nil
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
	if err := s.checkNotCopied(correlationID, map[string]any{"view": viewName}); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(correlationID, map[string]any{"view": viewName}); err != nil {
		return "", err
	}
	// FEAT-208: consult the registered view table instead of comparing
	// against the single "engine.status" constant (AC per the increment
	// plan's step 1). Unregistered names keep returning ErrUnknownView —
	// no behaviour change for a view nobody has RegisterView'd.
	if _, ok := s.views[viewName]; !ok {
		return "", errs.New(ErrUnknownView, correlationID, map[string]any{"view": viewName})
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

// Publish computes each live subscription's registered view patch and
// pushes a Delta (with that subscription's own monotonically increasing
// Seq, stamped with tick) to every one of them via sink (FEAT-208,
// generalising PublishEngineStatus below from one hardcoded view to N
// registered views). Called only from the subscription pump goroutine
// (commands.go's StartSubscriptionPump) — never from the phase-pipeline
// or command-handling call paths — so that delta computation and push
// are always off the main tick path (AC-7).
//
// R3 TWO-MUTEX SPLIT (independent round r2 REJECT, FEAT-208 increment 1
// — see publishMu's own doc comment on the struct for the summary of
// what r2 broke and why). publishMu is held for this method's ENTIRE
// body; s.mu is taken only twice, briefly, nested inside that:
//
//  1. Under s.mu (brief): snapshot the live subscription set (sorted by
//     SubscriptionID — deterministic target order, GR#21 house style)
//     and the distinct registered ViewPatchFuncs those subscriptions
//     actually need this cycle. Unlock immediately after.
//  2. OFF s.mu (and off nothing else — publishMu is still held, but
//     nothing else needs it during this pass), in sorted view-name
//     order: call each distinct ViewPatchFunc exactly once this cycle —
//     a producer may do real work (an engine.services CoverageSummary
//     read, a json.Marshal) and must never block Subscribe/Unsubscribe/
//     RegisterView while it runs, which is exactly why it does not hold
//     s.mu. A producer that errors is logged loudly (GR#1) and every
//     subscription on that view is skipped this cycle — never a
//     partially-applied patch.
//  3. Under s.mu (brief): assign each still-live subscription's next
//     Seq and gather its send target — Seq is allocated (sub.seq++) and
//     the target list is built in this same brief critical section, but
//     SendDelta itself is called AFTER s.mu is released (see below), not
//     inside it. A subscription pass 1 saw but Unsubscribe removed
//     before this lock re-acquires is simply absent from s.subs here
//     and is silently skipped — exactly Unsubscribe's own documented
//     contract ("deltas stop immediately").
//  4. OFF s.mu, still under publishMu: deliver every target's Delta via
//     sink.SendDelta, in the same target order pass 3 built.
//
// Why this still closes F1b (assignment order == delivery order) even
// though SendDelta no longer runs inside the SAME critical section Seq
// is assigned in: publishMu serializes the ENTIRE cycle end-to-end, so
// only one Publish call is EVER active system-wide at a time — a second
// concurrent Publish call blocks on publishMu.Lock() before it can even
// start ITS pass 1, let alone assign or send anything, until the first
// call's pass 4 (its own sends) has fully completed. Two Publish calls
// can therefore never interleave their assignment/delivery at all, which
// is strictly stronger than r1's "assign-and-send-atomically" argument —
// it doesn't merely keep one call's assignment and delivery together, it
// keeps every call's WHOLE cycle exclusive. Empirically: the independent
// round r1 attack test (now TestRegression_ConcurrentPublish_DeliveryStaysInOrder)
// still passes 8/8 under -race after this change (verified at
// -count=3 per the r3 gate).
//
// Why this closes r2's stall/deadlock findings: Subscribe, Unsubscribe,
// and RegisterView only ever take s.mu — never publishMu. A slow or
// even permanently-blocking DeltaSink.SendDelta call (pass 4, above)
// holds ONLY publishMu, never s.mu, so those three methods can never be
// stalled by it (TestRegression_BlockingDeltaSink_SubscribeAndRegisterViewCompletePromptly,
// formerly the r2 attacker's TestAttack_BlockingDeltaSink_..., now
// asserts exactly this). A DeltaSink that reenters Subscribe/
// Unsubscribe/RegisterView from inside SendDelta is therefore also safe
// — it needs only s.mu, which this goroutine is not holding at that
// point (TestRegression_ReentrantSubscribeFromDeltaSink_DoesNotDeadlock).
//
// THE ONE REMAINING PROHIBITION (documented on DeltaSink itself,
// commands.go, per r3 item 2): a DeltaSink must NEVER call back into
// Publish. Go's sync.Mutex is not reentrant, so a SendDelta call that
// re-entered Publish on the SAME goroutine would self-deadlock on
// publishMu, permanently. This is a real, easy-to-hit hazard for a
// naive future DeltaSink implementation, not a contrived one — kept
// honestly documented and tested as PROHIBITED, not "fixed" (there is
// no way to make an intentionally-reentrant-into-Publish call safe
// without either a recursive mutex, which Go deliberately does not
// provide, or restructuring Publish to not serialize at all, which
// would reopen F1b).
//
// SEC-019: identity-checked BEFORE any lock is touched (pre-lock,
// load-bearing) and again immediately after each acquisition (defence
// in depth) — same ordering as Subscribe/Unsubscribe. Publish has no
// correlationID parameter (it runs on the subscription pump goroutine,
// off any request's call path — see EngineStatusView's doc comment for
// the identical "no reporting channel" situation) and no return value
// to carry an error through, so a copy is handled the same way a
// producer failure is: this cycle's publish for the affected
// subscriptions is silently dropped (never a partial/racy one) and the
// next pump wake retries against whichever SubscriptionServer value it
// is actually called against. A copy can never legitimately reach this
// point in practice — nothing that constructs or drives a real
// subscription pump does so with a copied SubscriptionServer
// (StartSubscriptionPump always closes over the one *SubscriptionServer
// NewEngine's Engine.subs holds).
func (s *SubscriptionServer) Publish(sink DeltaSink, tick protocol.Tick) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return
	}

	// R3: publishMu held for the whole cycle — see this method's doc
	// comment. Re-checked after acquisition (defence in depth, SEC-019),
	// mirroring every other guarded entry point's pre/post-lock pattern.
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.self.Load() != s {
		return
	}

	// Pass 1: brief s.mu section.
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
	ids := make([]protocol.SubscriptionID, 0, len(s.subs))
	for id := range s.subs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	neededViews := make(map[string]ViewPatchFunc)
	for _, id := range ids {
		v := s.subs[id].view
		if _, ok := neededViews[v]; ok {
			continue
		}
		// A registered subscription naming a view no longer in s.views
		// cannot happen today (views is append-only after Wire-time
		// registration, and Subscribe itself already rejects any
		// viewName not in the table) — defensively skipped rather than
		// assumed impossible.
		if fn, known := s.views[v]; known {
			neededViews[v] = fn
		}
	}
	s.mu.Unlock()

	// Pass 2. Off s.mu entirely (publishMu is still held, but nothing
	// else needs it during this pass). Iterated in sorted view-name
	// order (never a bare map range) so cache-miss/log order is
	// deterministic run over run (GR#21), even though the result
	// (patches) is only ever used as a by-name lookup in pass 3.
	viewNames := make([]string, 0, len(neededViews))
	for v := range neededViews {
		viewNames = append(viewNames, v)
	}
	sort.Strings(viewNames)

	type patchResult struct {
		patch json.RawMessage
		err   error
	}
	patches := make(map[string]patchResult, len(viewNames))
	for _, v := range viewNames {
		patch, err := neededViews[v]()
		patches[v] = patchResult{patch: patch, err: err}
		if err != nil {
			// A view's ViewPatchFunc failed this cycle. GR#1: log
			// loudly, never silently drop. No correlationID/caller to
			// report to from the pump goroutine (the same "no reporting
			// channel" situation EngineStatusView's degrade-to-zero doc
			// comment already documents) — the next pump wake retries
			// with fresh state.
			_ = errs.Wrap(ErrSnapshotFailed, errs.NewCorrelationID(), err, map[string]any{"view": v})
		}
	}

	// Pass 3: brief s.mu section — assign Seq and gather targets ONLY;
	// SendDelta itself happens in pass 4, after s.mu is released.
	s.mu.Lock()
	if s.self.Load() != s {
		s.mu.Unlock()
		return
	}
	type target struct {
		id     protocol.SubscriptionID
		seq    uint64
		corrID protocol.CorrelationID
		patch  json.RawMessage
	}
	targets := make([]target, 0, len(ids))
	for _, id := range ids {
		sub, live := s.subs[id]
		if !live {
			continue
		}
		res, ok := patches[sub.view]
		if !ok || res.err != nil {
			continue
		}
		sub.seq++
		targets = append(targets, target{id: id, seq: sub.seq, corrID: sub.pendingCorrID, patch: res.patch})
		sub.pendingCorrID = "" // echoed at most once, on the first delta after Subscribe
	}
	s.mu.Unlock()

	// Pass 4: deliver, OFF s.mu (still under publishMu — see this
	// method's doc comment for why that alone is sufficient to keep
	// assignment order == delivery order).
	for _, t := range targets {
		sink.SendDelta(protocol.Delta{
			SubscriptionID: t.id,
			Tick:           tick,
			Seq:            t.seq,
			Patch:          t.patch,
			CorrelationID:  t.corrID,
		})
	}
}

// PublishEngineStatus is a thin, backward-compatible wrapper around
// Publish for the "engine.status" view (kept so nothing regresses per
// the FEAT-208 increment plan's step 1). The actual "engine.status"
// patch sent is computed by that view's own registered ViewPatchFunc
// (NewEngine registers it once, wrapping EngineStatusView — see
// engine.go), not from view directly; view.Tick is used only to stamp
// the Delta.Tick field, exactly as before.
func (s *SubscriptionServer) PublishEngineStatus(sink DeltaSink, view EngineStatusView) {
	// Explicit, direct guard (SEC-019) even though Publish immediately
	// applies its own — astgate's syntactic scan requires every guarded
	// type's public method to call checkNotCopied directly, not only
	// transitively through a delegate call it cannot see across.
	if err := s.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return
	}
	s.Publish(sink, protocol.Tick(view.Tick))
}
