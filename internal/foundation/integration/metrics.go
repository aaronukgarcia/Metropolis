package integration

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is INCREMENT 5 of the Integration Engine (proposal §8 point
// 5 / §1 point 7 / §2's "Monitoring" paragraph): the observable surface
// every integration reports status/queue-depth/throughput/peak into. It
// does NOT add a new dashboard transport, a new queue tier, or a new
// resilience behaviour — it is a PULL-BASED reader sitting entirely
// outside increment 2's QueuedTransport and increment 3's Connection,
// reached only through those types' own already-public, already-reviewed
// accessors (QueuedTransport.Depth, Connection.State) via caller-supplied
// closures. Neither queue.go nor resilience.go is modified by this
// increment — see "Zero cost / zero behaviour change" below for why that
// is the whole point, not an accident.
//
// # What a Registry holds
//
// A Registry (this file) is a named collection of *IntegrationMetrics,
// one per integration the composition root wires up (a later increment,
// proposal §8 point 6 — not built here). Each *IntegrationMetrics reports:
//
//   - name                     — the integration's identity.
//   - state (up/degraded/down) — sampled on demand from an optional
//     StateFunc closure (normally a wired *Connection's own State
//     method), mapped via StatusFromConnState.
//   - queue depth per tier     — sampled on demand from an optional
//     DepthFunc closure (normally a wired *QueuedTransport's own Depth
//     method, which is already lock-guarded and O(1) — see queue.go's
//     Depth doc comment).
//   - delivered / error counts — accumulated via RecordDelivered/
//     RecordError, atomic-only, called by whatever code drives that
//     integration's Drain/Execute loop (proposal's "each integration
//     reports... into" the monitoring contract).
//   - peak depth / peak throughput — the high-water marks RecordDelivered
//     and the depth sampler observe, atomic-only (compare-and-swap loop),
//     never a lock.
//   - last error                — the most recent RecordError's message,
//     guarded by a small mutex (rare-write, monitoring-only path — never
//     touched by RunShard/Drain/Attempt/Reconnect themselves).
//
// # Zero cost / zero behaviour change on the sim path (design constraint a)
//
// Nothing in this file is called FROM det.RunPhase's shard goroutines,
// Execute, QueuedTransport.Drain, or Connection.Attempt/Reconnect — those
// functions are entirely unmodified by this increment (diff this file's
// package against increment 2/3/4's files: queue.go, resilience.go,
// executor.go, wal.go, recovery.go are untouched). A caller (normally the
// composition root, proposal §8 point 6) chooses WHEN to call
// RecordDelivered/RecordError, typically right after a Drain/Execute call
// already returned — at that point it is ordinary caller-side
// bookkeeping, not a hook injected into the hot path. The only locking
// primitive on ANY recording method is a lock-free atomic (Add /
// CompareAndSwap loop); the two places this file does take a real mutex
// (IntegrationMetrics.mu, guarding depthFn/stateFn/lastErr; Registry.mu,
// guarding the name->entry map) are both touched only by: (1) boot-time
// wiring (Register, WithDepthFunc/WithStateFunc), and (2) the monitoring
// HTTP handler's own poll (metrics_server.go), roughly once every ~2s per
// dashboard.html's poll interval — never once per tick, never from a
// shard goroutine. This is what makes metrics_test.go's determinism test
// (Execute/Drain WITH metrics attached vs WITHOUT, byte-identical) true
// by construction rather than by careful timing: metrics collection is
// never on the call graph det.MergeInOrder/det.ApplyBarrier/Drain's
// peek-send-commit critical section reaches.
//
// Counters are explicitly OUTSIDE the deterministic state hash (GR#21):
// Delivered/Errors/PeakDepth/PeakThroughput are observational telemetry
// about HOW the sim ran, never INPUTS to what the sim computes — nothing
// in engine/core, det, or protocol ever reads a *Registry or
// *IntegrationMetrics value back into simulation state.
//
// # Determinism of the JSON snapshot (design constraint b)
//
// Snapshot's per-integration ordering is by NAME, sorted ascending
// (sortedNames below) — never map iteration order, which Go deliberately
// randomizes. Within one IntegrationSnapshot, encoding/json marshals a
// Go struct's fields in declaration order, which is fixed at compile
// time — so two Snapshot() calls against identical underlying state
// produce byte-identical JSON, which is exactly what metrics_test.go's
// determinism test (two snapshots of identical state, diffed as bytes)
// checks.

// Status names an integration's coarse monitoring state (proposal §1
// point 7 / §2: "status (up/down/degraded)"). Distinct from ConnState
// (resilience.go), which has finer-grained Retrying/CatchingUp/
// Reconnecting states a monitoring dashboard doesn't need to
// distinguish — StatusFromConnState folds ConnState's four states down
// to these three.
type Status int

const (
	// StatusUp: the steady state — either no StateFunc is wired (the
	// local, always-connected degenerate case the proposal describes —
	// "Local = the degenerate always-connected case", proposal §1 point
	// 5) or a wired Connection reports StateConnected.
	StatusUp Status = iota
	// StatusDegraded: a wired Connection reports StateRetrying (a
	// transient failure still within its retry budget) or
	// StateCatchingUp (reconnected, draining backlog — not yet fully
	// caught up).
	StatusDegraded
	// StatusDown: a wired Connection reports StateReconnecting (retries
	// exhausted or the caller explicitly re-establishing the
	// connection from scratch).
	StatusDown
)

// String renders a Status for the JSON snapshot / dashboard. Never used
// on a determinism-sensitive path (no ordering or merge decision reads
// this — it is a pure display label).
func (s Status) String() string {
	switch s {
	case StatusUp:
		return "up"
	case StatusDegraded:
		return "degraded"
	case StatusDown:
		return "down"
	default:
		return "unknown"
	}
}

// StatusFromConnState maps a resilience.go Connection's ConnState onto
// this file's coarser monitoring Status (see Status's doc comment for
// the folding rule).
func StatusFromConnState(cs ConnState) Status {
	switch cs {
	case StateConnected:
		return StatusUp
	case StateRetrying, StateCatchingUp:
		return StatusDegraded
	case StateReconnecting:
		return StatusDown
	default:
		return StatusDown
	}
}

// DepthFunc is the pull-based depth seam an IntegrationMetrics samples
// on demand — normally a wired *QueuedTransport's own Depth method
// (queue.go), passed by value (a method value, not a call), so sampling
// costs exactly what QueuedTransport.Depth already costs: one lock-free
// copy-guard check plus two already-tiny tierQueue.Depth calls.
type DepthFunc func() Depth

// StateFunc is the pull-based connection-state seam an IntegrationMetrics
// samples on demand — normally a wired *Connection's own State method
// (resilience.go).
type StateFunc func() ConnState

// TierDepth is one tier's queue depth in a monitoring snapshot. OnDisk is
// only meaningful for the T1 tier (queue.go's Depth.T1OnDisk) and is
// omitted from JSON when zero.
type TierDepth struct {
	Tier   string `json:"tier"`
	Depth  int    `json:"depth"`
	OnDisk int    `json:"onDisk,omitempty"`
}

// IntegrationSnapshot is one integration's point-in-time monitoring
// snapshot (proposal §1 point 7 / §2: "status, queue depth, throughput,
// peak load"). Field order is fixed by this declaration — see this
// file's header comment on determinism.
type IntegrationSnapshot struct {
	Name           string      `json:"name"`
	State          string      `json:"state"`
	QueueDepth     []TierDepth `json:"queueDepth"`
	Delivered      int64       `json:"delivered"`
	Errors         int64       `json:"errors"`
	PeakDepth      int64       `json:"peakDepth"`
	PeakThroughput int64       `json:"peakThroughput"`
	LastError      string      `json:"lastError,omitempty"`
}

// PhaseSample is one phase-boundary observation recorded via
// Registry.ObservePhase — the optional phase-observer wire-in point
// (proposal §2's "monitoring taps... core.WithPhaseObserver(kind, tick,
// month)" hook, proposal §7). A phase runs once across the WHOLE tick,
// never per-integration, so it lives on Snapshot directly rather than
// inside any one IntegrationSnapshot.
type PhaseSample struct {
	Kind  string `json:"kind"`
	Tick  int64  `json:"tick"`
	Month int64  `json:"month"`
}

// Snapshot is the whole registry's monitoring surface — the JSON payload
// GET /metrics.json serves (metrics_server.go). Integrations is always
// sorted by Name (see this file's header comment on determinism).
// LastPhase is nil (omitted from JSON) until ObservePhase has been called
// at least once.
type Snapshot struct {
	Integrations []IntegrationSnapshot `json:"integrations"`
	LastPhase    *PhaseSample          `json:"lastPhase,omitempty"`
}

// MetricsOption customizes an IntegrationMetrics at Register time. Unset
// options leave the corresponding sampler nil — Snapshot degrades
// gracefully (empty queue depth, StatusUp per the local-degenerate-case
// default) rather than failing when a caller only wires one seam.
type MetricsOption func(*IntegrationMetrics)

// WithDepthFunc wires the queue-depth sampler (normally a *QueuedTransport
// method value).
func WithDepthFunc(fn DepthFunc) MetricsOption {
	return func(m *IntegrationMetrics) { m.setDepthFunc(fn) }
}

// WithStateFunc wires the connection-state sampler (normally a
// *Connection method value).
func WithStateFunc(fn StateFunc) MetricsOption {
	return func(m *IntegrationMetrics) { m.setStateFunc(fn) }
}

// IntegrationMetrics is one integration's monitoring entry (see this
// file's header comment for the full field list and the zero-cost
// argument). The zero value is not usable — construct via
// Registry.Register.
//
// Like tierQueue/QueuedTransport (queue.go) and Connection (resilience.go),
// IntegrationMetrics carries both mutex-guarded state (mu, guarding the
// depthFn/stateFn/lastErr fields, which a struct copy would alias while
// gaining its own independent, non-exclusive mutex) and lock-free atomic
// counters — the same SEC-020-class "two locks, one referent" hazard,
// guarded the same way: checkNotCopied, called before mu (or any atomic
// field) is ever touched, at every real entry point.
type IntegrationMetrics struct {
	name string

	mu      sync.Mutex
	depthFn DepthFunc
	stateFn StateFunc
	lastErr string

	delivered      atomic.Int64
	errorsCount    atomic.Int64
	peakDepth      atomic.Int64
	peakThroughput atomic.Int64

	// self is the SEC-020-class copy-identity guard — same pattern and
	// rationale as tierQueue.self/QueuedTransport.self (queue.go) and
	// Connection.self (resilience.go).
	self atomic.Pointer[IntegrationMetrics]
}

func newIntegrationMetrics(name string, opts ...MetricsOption) *IntegrationMetrics {
	m := &IntegrationMetrics{name: name}
	// Stored once, here, before m is returned to any caller — mirrors
	// newTierQueue/NewQueuedTransport/NewConnection's self.Store timing
	// exactly (see those constructors' doc comments for why that
	// ordering matters).
	m.self.Store(m)
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// checkNotCopied mirrors tierQueue.checkNotCopied/QueuedTransport.
// checkNotCopied/Connection.checkNotCopied exactly: a lock-free identity
// check, safe to call before mu or any atomic field is ever touched.
func (m *IntegrationMetrics) checkNotCopied(method string) error {
	if m.self.Load() != m {
		return errs.New(ErrMetricsEntryCopied, errs.NewCorrelationID(), map[string]any{
			"method": method,
			"name":   m.name,
		})
	}
	return nil
}

// Name reports this entry's integration name.
func (m *IntegrationMetrics) Name() string { return m.name }

func (m *IntegrationMetrics) setDepthFunc(fn DepthFunc) {
	if err := m.checkNotCopied("setDepthFunc"); err != nil {
		return
	}
	m.mu.Lock()
	m.depthFn = fn
	m.mu.Unlock()
}

func (m *IntegrationMetrics) setStateFunc(fn StateFunc) {
	if err := m.checkNotCopied("setStateFunc"); err != nil {
		return
	}
	m.mu.Lock()
	m.stateFn = fn
	m.mu.Unlock()
}

// RecordDelivered adds n (must be > 0, else a no-op) to the delivered
// counter and updates the peak-throughput high-water mark — the caller's
// job to call this once per batch actually delivered (e.g. with
// DrainStats.Total() right after a QueuedTransport.Drain call returns, or
// Execute's shard count on success). Atomic-only: never takes mu, never
// blocks on anything a concurrent Snapshot/RecordError call is doing.
func (m *IntegrationMetrics) RecordDelivered(n int64) {
	if err := m.checkNotCopied("RecordDelivered"); err != nil {
		return
	}
	if n <= 0 {
		return
	}
	m.delivered.Add(n)
	casMaxInt64(&m.peakThroughput, n)
}

// RecordError increments the error counter and records err's message as
// the most recent LastError (a nil err clears LastError back to empty,
// which a caller can use to signal "the transient condition cleared").
// The counter increment is atomic-only; only the LastError string write
// takes mu (rare-write, monitoring-only — never on any tick-critical
// path).
func (m *IntegrationMetrics) RecordError(err error) {
	if cerr := m.checkNotCopied("RecordError"); cerr != nil {
		return
	}
	m.errorsCount.Add(1)
	m.mu.Lock()
	if err != nil {
		m.lastErr = err.Error()
	} else {
		m.lastErr = ""
	}
	m.mu.Unlock()
}

// casMaxInt64 atomically sets *addr to n if n is currently the larger
// value — a lock-free "high-water mark" update, used by both
// RecordDelivered's peak-throughput tracking and sampleDepth's
// peak-depth tracking below.
func casMaxInt64(addr *atomic.Int64, n int64) {
	for {
		cur := addr.Load()
		if n <= cur {
			return
		}
		if addr.CompareAndSwap(cur, n) {
			return
		}
	}
}

// sampleDepth pulls the current queue depth from the wired DepthFunc (if
// any), updates the peak-depth high-water mark from the T0+T1 total, and
// renders the per-tier breakdown for the snapshot. A nil DepthFunc (never
// wired) returns an empty slice — Snapshot degrades gracefully rather
// than fabricating a zero-depth reading that would look identical to "the
// queue is genuinely empty."
func (m *IntegrationMetrics) sampleDepth() []TierDepth {
	m.mu.Lock()
	fn := m.depthFn
	m.mu.Unlock()
	if fn == nil {
		return nil
	}
	d := fn()
	casMaxInt64(&m.peakDepth, int64(d.T0)+int64(d.T1))

	t2Depth := 0
	if d.T2 {
		t2Depth = 1
	}
	return []TierDepth{
		{Tier: "T0", Depth: d.T0},
		{Tier: "T1", Depth: d.T1, OnDisk: d.T1OnDisk},
		{Tier: "T2", Depth: t2Depth},
	}
}

// sampleState pulls the current connection state from the wired
// StateFunc (if any), mapped through StatusFromConnState. A nil StateFunc
// (never wired) reports StatusUp — the local, always-connected
// degenerate case (proposal §1 point 5) is the correct default for an
// integration that has no resilience.Connection at all.
func (m *IntegrationMetrics) sampleState() Status {
	m.mu.Lock()
	fn := m.stateFn
	m.mu.Unlock()
	if fn == nil {
		return StatusUp
	}
	return StatusFromConnState(fn())
}

// Snapshot renders this entry's current monitoring snapshot. Safe to call
// concurrently with RecordDelivered/RecordError/sampleDepth/sampleState —
// every read here is either a single atomic load or a brief mu-guarded
// field read, never held across the DepthFunc/StateFunc call itself
// (mirrors Connection.nowFunc's "take the lock only to read the func
// pointer, not to hold it across the call" pattern, since a caller-
// injected DepthFunc/StateFunc could in principle be slow).
func (m *IntegrationMetrics) Snapshot() IntegrationSnapshot {
	if err := m.checkNotCopied("Snapshot"); err != nil {
		return IntegrationSnapshot{Name: m.name, State: StatusDown.String(), LastError: err.Error()}
	}

	state := m.sampleState()
	depth := m.sampleDepth()

	m.mu.Lock()
	lastErr := m.lastErr
	m.mu.Unlock()

	return IntegrationSnapshot{
		Name:           m.name,
		State:          state.String(),
		QueueDepth:     depth,
		Delivered:      m.delivered.Load(),
		Errors:         m.errorsCount.Load(),
		PeakDepth:      m.peakDepth.Load(),
		PeakThroughput: m.peakThroughput.Load(),
		LastError:      lastErr,
	}
}

// Registry is the named collection of *IntegrationMetrics the
// composition root (a later increment) wires every integration into, and
// the source ServeMetrics' /metrics.json handler polls (metrics_server.go).
// The zero value is not usable — construct via NewRegistry.
//
// Like every other guarded type in this package, Registry carries both a
// sync.Mutex VALUE (mu, guarding the name->entry map) and aliasable
// reference state (the map itself, and every *IntegrationMetrics value it
// holds) — the same SEC-020-class hazard, guarded the same way.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*IntegrationMetrics

	// lastPhase is the optional phase-observer wire-in point's most
	// recent sample (ObservePhase/PhaseObserverFunc below). Lock-free —
	// a single atomic.Pointer store/load, never guarded by mu, since it
	// is written from a potentially every-tick call site and must never
	// contend with Register/Snapshot's map access.
	lastPhase atomic.Pointer[PhaseSample]

	// self is the SEC-020-class copy-identity guard — same pattern and
	// rationale as every other guarded type in this package.
	self atomic.Pointer[Registry]
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	r := &Registry{entries: make(map[string]*IntegrationMetrics)}
	// Stored once, here, before r is returned to any caller — mirrors
	// every other constructor in this package (see IntegrationMetrics'
	// newIntegrationMetrics for the identical rationale).
	r.self.Store(r)
	return r
}

// checkNotCopied mirrors every other guarded type's checkNotCopied in
// this package exactly: a lock-free identity check, safe to call before
// r.mu is ever touched.
func (r *Registry) checkNotCopied(method string) error {
	if r.self.Load() != r {
		return errs.New(ErrMetricsRegistryCopied, errs.NewCorrelationID(), map[string]any{"method": method})
	}
	return nil
}

// Register returns the named *IntegrationMetrics entry, creating it on
// first call (idempotent — a later Register with the same name returns
// the SAME entry, applying opts on top of it, rather than replacing it
// and orphaning any counters already recorded against the original).
// This is the composition root's one wiring entry point per integration
// (proposal §8 point 6, not built in this increment, but this is the seam
// it will call).
func (r *Registry) Register(name string, opts ...MetricsOption) *IntegrationMetrics {
	if err := r.checkNotCopied("Register"); err != nil {
		// Fail conservative: return a fresh, unregistered, unshared entry
		// rather than panicking or silently returning nil — a caller that
		// only ever calls RecordDelivered/RecordError/Snapshot on the
		// returned value sees consistent behaviour either way, it simply
		// never appears in this (copied, invalid) Registry's own Snapshot.
		return newIntegrationMetrics(name, opts...)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if m, ok := r.entries[name]; ok {
		for _, opt := range opts {
			opt(m)
		}
		return m
	}

	m := newIntegrationMetrics(name, opts...)
	r.entries[name] = m
	return m
}

// Get returns the named entry without creating it, and whether it was
// found.
func (r *Registry) Get(name string) (*IntegrationMetrics, bool) {
	if err := r.checkNotCopied("Get"); err != nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.entries[name]
	return m, ok
}

// sortedNames returns every registered integration's name, sorted
// ascending — the single source of ordering Snapshot's Integrations
// slice uses (see this file's header comment on determinism: map
// iteration order is NEVER used directly for anything observable).
func (r *Registry) sortedNames() []string {
	if err := r.checkNotCopied("sortedNames"); err != nil {
		return nil
	}
	r.mu.Lock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	r.mu.Unlock()
	sort.Strings(names)
	return names
}

// Snapshot renders every registered integration's current monitoring
// snapshot, sorted by name (deterministic — see this file's header
// comment). Safe to call concurrently with Register/RecordDelivered/
// RecordError from any other goroutine.
func (r *Registry) Snapshot() Snapshot {
	if err := r.checkNotCopied("Snapshot"); err != nil {
		return Snapshot{}
	}

	names := r.sortedNames()

	r.mu.Lock()
	entries := make([]*IntegrationMetrics, 0, len(names))
	for _, name := range names {
		if m, ok := r.entries[name]; ok {
			entries = append(entries, m)
		}
	}
	r.mu.Unlock()

	out := make([]IntegrationSnapshot, 0, len(entries))
	for _, m := range entries {
		out = append(out, m.Snapshot())
	}
	return Snapshot{Integrations: out, LastPhase: r.lastPhase.Load()}
}

// ObservePhase records the most recent phase-boundary sample — the
// optional phase-observer wire-in point (proposal §2 / §7's
// core.WithPhaseObserver seam). Atomic-only (a single pointer store, no
// mutex, and no read/write of any simulation state) — safe to call once
// per phase, every tick, from whatever adapter closure the composition
// root passes to core.WithPhaseObserver, without perturbing the tick's
// own timing or determinism (GR#21): ObservePhase touches nothing this
// package doesn't already own.
func (r *Registry) ObservePhase(kind string, tick int64, month int64) {
	if err := r.checkNotCopied("ObservePhase"); err != nil {
		return
	}
	r.lastPhase.Store(&PhaseSample{Kind: kind, Tick: tick, Month: month})
}

// PhaseObserverFunc returns a closure with the same (kind, tick, month)
// shape as engine/core.PhaseObserver (func(kind PhaseKind, tick, month
// int64), where PhaseKind's underlying type is string) that a
// composition-root adapter can wrap directly, e.g.:
//
//	core.WithPhaseObserver(func(kind core.PhaseKind, tick, month int64) {
//	    reg.PhaseObserverFunc()(string(kind), tick, month)
//	})
//
// This package deliberately does not import internal/engine/core itself
// (doc.go's existing dependency boundary — foundation.integration only
// depends on det/errs/protocol) so the one-line PhaseKind->string
// conversion happens in the adapter, not here.
func (r *Registry) PhaseObserverFunc() func(kind string, tick int64, month int64) {
	if err := r.checkNotCopied("PhaseObserverFunc"); err != nil {
		// Fail conservative: a no-op closure rather than a closure bound
		// to a copy — a caller wiring this into core.WithPhaseObserver
		// gets silently-dropped samples, never a second, uncoordinated
		// Registry recording phase data nobody else can see.
		return func(string, int64, int64) {}
	}
	return r.ObservePhase
}
