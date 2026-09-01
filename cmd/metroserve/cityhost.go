package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 2 inc1 — CityHost: the multi-city registry.
//
// Phase 1 proved ONE metroserve process hosting ONE durable, rehydrating
// city (main.go + persist.go). Phase 2's topology (Aaron's ruling: "each
// instance hosting M independent city sessions; elastic") needs ONE process
// to own and lifecycle N INDEPENDENT single-player cities at once. CityHost
// is that registry — the core inc2 (connection→city routing) and inc3
// (failover-by-replay) build on. It generalises exactly the single-city boot
// main.go performs (engine → compose.Wire → InProcTransport → pump +
// command-loop + tick driver) and the guarded per-city persistence
// persist.go performs (wireAndRehydrate), to a keyed map of running cities.
//
// Nothing here re-implements engine, transport, persistence, or restore: it
// CONSUMES internal/engine/core, internal/engine/compose, internal/protocol,
// and internal/persist unchanged, and reuses persist.go's wireAndRehydrate
// verbatim so the double-append guard stays single-sourced (GR#3).
//
// # Isolation (the multi-tenant guarantee, AC-4)
//
// Every city gets its OWN *core.Engine, its OWN *protocol.InProcTransport,
// its OWN child context + goroutines, and its OWN CityKey journal namespace
// under the shared Store (persist keys by SHA-256 of Tenant/City, so two
// cities never share a directory). A command routed to city A can only move
// A's engine state and A's journal; B is untouched.
//
// # Single-city-per-key under concurrency (AC-2 / AC-6)
//
// The map is guarded by a sync.Mutex, but construction (which rehydrates a
// journal and starts goroutines — not instant) does NOT run under that lock,
// or concurrent creation of DIFFERENT cities would serialise. Instead each
// key gets a cityEntry the moment it is claimed, published into the map under
// the lock, with a `ready` channel other callers block on. The FIRST caller
// to claim a key constructs; every concurrent caller for the SAME key finds
// the entry, drops the lock, waits on `ready`, and returns the identical
// *runningCity (or the identical construction error). Exactly one engine is
// ever built per key; different keys build in parallel.

// errCityHostCopied is returned by CityHost's guarded methods when the value
// has been copied after construction (SEC-020) — a copied CityHost would
// carry its sync.Mutex in a locked state and alias the cities map. Mirrors
// persist.DiskStore.checkNotCopied / ErrStoreCopied exactly.
var errCityHostCopied = errors.New("metroserve: CityHost used after being copied (SEC-020: must be used via the *CityHost from NewCityHost)")

// IdleEvictTimeout is how long a city may sit with ZERO active connections
// before the idle evictor unloads it to bound the host's memory (FEAT-1972079942
// AC-3). A rehydrating city is cheap to rebuild on the next connection from its
// journal (inc4's proven path), so holding an idle city's engine + 3 goroutines
// in memory indefinitely is pure waste.
//
// PLACEHOLDER balance number (Aaron retunes — the balance-number regime): the
// proposal is 5 minutes idle. Balance regime note (AC-6/GR#21): this is a
// WALL-CLOCK timer that gates ONLY host memory reclamation — it never reaches an
// engine, is never read on any determinism path, and changing it cannot change
// any city's simulated state, only WHEN an untouched city is unloaded.
const IdleEvictTimeout = 5 * time.Minute

// evictSweepInterval is how often the background evictor scans the city map for
// idle-past-IdleEvictTimeout cities (FEAT-1972079942 AC-3). PLACEHOLDER balance
// number (Aaron retunes): sweep every 30s. Same balance-regime caveat as
// IdleEvictTimeout — it affects only eviction latency, never sim state.
const evictSweepInterval = 30 * time.Second

// runningCity bundles one live city: its engine, its live Composition (for
// StateDigest), its transport, the CancelFunc that stops its goroutines, and
// the three done signals its pump/command-loop/tick-driver goroutines close
// on exit. stop() cancels and joins all three (no goroutine leak) then closes
// the transport — the same drain order as main.go's shutdown.
type runningCity struct {
	engine    *core.Engine
	comp      *compose.Composition
	transport *protocol.InProcTransport

	cancel   context.CancelFunc
	pumpDone <-chan struct{}
	loopDone <-chan error
	tickDone <-chan struct{}
}

// Engine, Composition and Transport expose the bundled handles to callers
// (inc2's router, and the tests) without widening the struct's field access.
func (rc *runningCity) Engine() *core.Engine              { return rc.engine }
func (rc *runningCity) Composition() *compose.Composition { return rc.comp }
func (rc *runningCity) Transport() protocol.Transport     { return rc.transport }

// stop cancels the city's context, joins its pump/command-loop/tick-driver
// goroutines (so none leak), then closes its transport. Drain order mirrors
// main.go: cancel → tick → loop → pump → transport.Close. Idempotent at the
// runningCity level is NOT required (CityHost removes it from the map under
// the lock before calling stop, so stop runs exactly once per city).
func (rc *runningCity) stop() {
	rc.cancel()
	<-rc.tickDone
	<-rc.loopDone
	<-rc.pumpDone
	_ = rc.transport.Close()
}

// cityEntry is the per-key construction barrier. It is published into the map
// the instant a key is claimed (under the lock) so concurrent same-key
// callers find it and wait on `ready` rather than racing to build a second
// engine. When construction finishes, exactly one goroutine sets city XOR err
// and closes `ready`; the close is the happens-before edge that makes those
// two fields safe to read for every waiter (-race clean).
type cityEntry struct {
	ready chan struct{}
	city  *runningCity
	err   error

	// active is the number of live WS connections currently bound to this
	// city (FEAT-1972079942 AC-2). It is guarded by CityHost.mu — the SAME
	// lock guarding the city map — so the idle evictor can atomically observe
	// "active == 0" and remove the entry in one critical section, and a
	// concurrent Acquire either wins the lock first (active > 0, veto) or
	// finds the entry already gone and rebuilds cleanly (AC-4).
	active int

	// idleSince is when active last dropped to 0 (or when a READY city was last
	// handed out by GetOrCreate — the "touch"). Zero (IsZero) whenever active
	// > 0, so the evictor never even considers an in-use city. It is ALSO held
	// zero throughout construction (the GetOrCreate claim path leaves it zero,
	// and every idleSince-stamping site — the GetOrCreate touch and Release —
	// is ready-guarded so it can never stamp a still-building entry): a slow
	// build is thus never a candidate for eviction before its builder can hand
	// it out (AC-4 "idleSince stays zero throughout construction"). It gates
	// eviction and NOTHING else: a wall-clock value that never reaches an
	// engine (AC-6/GR#21). The ready-guarded GetOrCreate touch is what closes
	// the AC-4 bind-then-Acquire window: a READY city just handed to a
	// connecting client is not idle-elapsed, so it cannot be evicted in the
	// sub-second gap before that connection's onOpen→Acquire pins it (given
	// IdleEvictTimeout, minutes, far exceeds the handshake→Acquire latency,
	// seconds).
	idleSince time.Time
}

// CityHost owns and lifecycles N independent running cities in one process.
//
// SEC-020: CityHost carries a sync.Mutex BY VALUE plus an aliasable map, so a
// value copy would duplicate a possibly-locked mutex and alias the map. The
// `self` atomic.Pointer + checkNotCopied (called before the lock in every
// mutating method) is the astgate-required copy guard, mirroring
// persist.DiskStore.
type CityHost struct {
	self atomic.Pointer[CityHost] // SEC-020 copy guard

	// store is the shared durable backend for every city, or nil in
	// no-persist mode (persistDir ""). persist keys each city by CityKey, so
	// one Store safely namespaces all N journals.
	store persist.Store

	tickInterval time.Duration

	// snapshotEvery is the durable snapshot cadence (ticks) every city built
	// by this host drives its command loop with (FEAT-1972079936 Phase 1
	// inc3b, snapshotdriver.go's startCommandLoop) — 0 disables snapshotting.
	// Fixed at construction (WithSnapshotEvery / defaults to
	// compose.SnapshotCadenceTicks), read only by buildCity, itself only
	// ever called from GetOrCreate's single first-claimant path per key, so
	// no lock is needed to read it (same "read-only after construction"
	// shape as engineOpts/tickInterval above).
	snapshotEvery int64

	// engineOpts are EXTRA core.Options appended after the per-city seed when
	// each engine is built. Empty for production (default pool sizing); tests
	// set core.WithPoolSize(1) here to match inc4's deterministic/fast engine.
	engineOpts []core.Option

	// logw receives the informational rehydrate lines wireAndRehydrate emits.
	// Defaults to os.Stdout (matching inc4); tests redirect to io.Discard.
	logw io.Writer

	// onBuildStart is a TEST-ONLY construction seam (nil on every production
	// path). When set, GetOrCreate's FIRST claimant for a key calls it AFTER the
	// not-yet-ready cityEntry has been published into the map (so a concurrent
	// same-key caller already finds it) and BEFORE construction runs — letting a
	// test hold a build open mid-flight to exercise the touch/Release-during-build
	// eviction race. It is read only here, on the claimant path, established by
	// the test's own goroutine-launch happens-before, so it never races the
	// evictor or the guarded methods.
	onBuildStart func()

	// rootCtx/rootCancel bound EVERY city's lifetime to the host, independent
	// of any caller's request context: a city keeps running after the
	// GetOrCreate call that built it returns, and stops only on Shutdown(key)
	// or Close(). Close cancels rootCtx as a belt-and-braces fan-out.
	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu     sync.Mutex // guards cities (incl. each entry's active/idleSince)
	cities map[persist.CityKey]*cityEntry

	// idleTimeout / sweepInterval are the eviction tunables (FEAT-1972079942
	// AC-3), seeded from IdleEvictTimeout / evictSweepInterval by NewCityHost.
	// They are unexported fields rather than consts precisely so a test can set
	// them tiny (a few ms) and exercise real eviction without sleeping minutes
	// — production never touches them.
	idleTimeout   time.Duration
	sweepInterval time.Duration

	// evictorDone is closed when the single background evictor goroutine exits
	// (on rootCtx cancellation). Close joins it BEFORE tearing down cities, so
	// no in-flight eviction races Close's own teardown (AC-5, no double stop).
	evictorDone chan struct{}
}

// CityHostOption configures optional CityHost construction knobs
// (FEAT-1972079936 Phase 1 inc3b). Variadic and appended at the END of
// NewCityHost/newCityHost's parameter lists deliberately — every pre-inc3b
// call site (attack_cityhost_test.go, cityhost_test.go, eviction_test.go,
// the old 2-arg NewCityHost) keeps compiling and behaving identically with
// zero options passed.
type CityHostOption func(*CityHost)

// WithSnapshotEvery sets the durable snapshot cadence (in ticks) every city
// this host builds drives its command loop with (snapshotdriver.go). 0
// disables snapshotting. Meaningless (silently ignored by
// startCommandLoop's own nil-store check) when persistDir is "" — a
// no-persist host has no Store to snapshot into.
func WithSnapshotEvery(ticks int64) CityHostOption {
	return func(h *CityHost) {
		// SEC-020: every function taking a *CityHost parameter guards against
		// a copied value before touching its fields (astgate-enforced) — see
		// newCityHost's doc comment on why self is stamped before options run,
		// which is what makes this check real rather than vacuous.
		if err := h.checkNotCopied(); err != nil {
			return
		}
		h.snapshotEvery = ticks
	}
}

// NewCityHost constructs a CityHost. persistDir "" runs every city in
// no-persist mode (in-memory journaler only, allowed for tests, matching
// inc4's default-off); a non-empty persistDir opens ONE shared DiskStore
// under which each city rehydrates from its own CityKey journal on
// (re)creation. tickInterval is the per-city wall-clock tick cadence (reused
// verbatim from main.go's tickLoop).
func NewCityHost(persistDir string, tickInterval time.Duration, opts ...CityHostOption) (*CityHost, error) {
	return newCityHost(persistDir, tickInterval, IdleEvictTimeout, evictSweepInterval, opts...)
}

// newCityHost is NewCityHost with the two eviction tunables made explicit
// (FEAT-1972079942 AC-3). Production goes through NewCityHost with the const
// defaults; tests call this directly with tiny idleTimeout/sweepInterval so they
// exercise real eviction in milliseconds without sleeping minutes. Both tunables
// are fixed at construction and read only by the evictor goroutine started
// below — the `go` establishes happens-before, so they are read race-free
// without a lock (they never change after construction).
func newCityHost(persistDir string, tickInterval, idleTimeout, sweepInterval time.Duration, opts ...CityHostOption) (*CityHost, error) {
	var store persist.Store
	if persistDir != "" {
		disk, err := persist.NewDiskStore(persistDir)
		if err != nil {
			return nil, fmt.Errorf("metroserve: CityHost open persist store %q: %w", persistDir, err)
		}
		store = disk
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &CityHost{
		store:         store,
		tickInterval:  tickInterval,
		snapshotEvery: compose.SnapshotCadenceTicks,
		logw:          os.Stdout,
		rootCtx:       ctx,
		rootCancel:    cancel,
		cities:        make(map[persist.CityKey]*cityEntry),
		idleTimeout:   idleTimeout,
		sweepInterval: sweepInterval,
		evictorDone:   make(chan struct{}),
	}
	// self is stamped BEFORE options run (not after, as a pre-inc3b draft of
	// this function had it) so every CityHostOption closure can call the
	// SAME checkNotCopied guard every other function taking a *CityHost
	// parameter must (SEC-020, astgate-enforced) — h is freshly constructed
	// and not yet reachable from anywhere else, so self.Load()==h holds
	// trivially true here; this is what makes the check in WithSnapshotEvery
	// below a real guard rather than a check that could never fail.
	h.self.Store(h)
	for _, opt := range opts {
		opt(h)
	}
	// Start the single idle evictor (FEAT-1972079942 AC-3). It runs under the
	// host root context and is joined by Close via evictorDone (AC-5).
	go h.runEvictor()
	return h, nil
}

// checkNotCopied guards against a CityHost value being copied after
// construction. SEC-020: every mutating method calls this before taking the
// lock. A CityHost must always be used via the *CityHost from NewCityHost.
func (h *CityHost) checkNotCopied() error {
	if h.self.Load() != h {
		return errCityHostCopied
	}
	return nil
}

// GetOrCreate returns the running city for cityKey, building it on first use.
//
//   - Already running → returns the SAME *runningCity (idempotent; a second
//     caller never gets a second engine).
//   - Not running → builds a fresh city: a new engine seeded deterministically
//     from cityKey (seedForCity), wired + rehydrated via persist.go's shared
//     wireAndRehydrate (persist on when the host has a Store), then a pump +
//     command loop + per-city tick driver on a per-city child of the host's
//     root context. Registered, then returned.
//   - Concurrent same-key callers yield EXACTLY ONE city (the cityEntry
//     barrier): the first claimant builds, the rest wait and share the result.
//   - A construction/rehydrate error (incl. a corrupt journal — FATAL per
//     inc4) returns the error and registers NOTHING (the claimed entry is
//     removed before its `ready` is closed, so no half-built city lingers).
//
// The passed ctx cancels the rehydrate/build only; the city itself runs under
// the host's root context and outlives this call.
func (h *CityHost) GetOrCreate(ctx context.Context, cityKey persist.CityKey) (*runningCity, error) {
	if err := h.checkNotCopied(); err != nil {
		return nil, err
	}

	h.mu.Lock()
	if e, ok := h.cities[cityKey]; ok {
		// Touch the idle clock (FEAT-1972079942 AC-4): handing this city to a
		// (re)connecting client resets its idle-since to now, so the evictor
		// cannot unload it in the gap before the connection's onOpen→Acquire
		// pins active > 0.
		//
		// BUT the touch must fire ONLY on a READY entry. active == 0 is true for
		// a pinned-free ready city AND for a city still being CONSTRUCTED (the
		// claim path below deliberately keeps idleSince ZERO throughout the build
		// so a slow build cannot be evicted before it is ready). An unguarded
		// `if e.active == 0` here would therefore also fire while a concurrent
		// first claimant is still building this key, stamping a NON-ZERO idleSince
		// on a still-building entry; if that build outlives idleTimeout the evictor
		// deletes and stop()s it mid-build, handing every caller a torn city whose
		// transport is already closed (AC-4 invariant "idleSince stays zero
		// throughout construction"). Guard with a NON-BLOCKING read of the ready
		// channel, under the SAME lock the touch runs under: ready-closed => the
		// city is truly built and idle, safe to touch; ready-open => still under
		// construction, leave idleSince zero (the claim-path invariant).
		select {
		case <-e.ready:
			if e.active == 0 {
				e.idleSince = time.Now()
			}
		default:
			// still constructing — leave idleSince zero (claim-path invariant)
		}
		h.mu.Unlock()
		<-e.ready // may already be closed
		if e.err != nil {
			return nil, e.err
		}
		return e.city, nil
	}
	// Claim the key: publish a not-yet-ready entry so concurrent same-key
	// callers wait on it instead of building a second engine. idleSince stays
	// ZERO (not idle-eligible) throughout construction — the evictor skips any
	// entry with a zero idleSince — so a slow build can never be evicted out from
	// under the caller before it is even ready (AC-4). The idle clock starts
	// only once the city is READY, below.
	entry := &cityEntry{ready: make(chan struct{})}
	h.cities[cityKey] = entry
	h.mu.Unlock()

	// Test-only construction seam (nil in production): hold this first claimant
	// open mid-build so a test can drive a concurrent same-key touch (and the
	// evictor) against a still-building entry.
	if h.onBuildStart != nil {
		h.onBuildStart()
	}

	city, err := buildCity(h.rootCtx, ctx, h.store, cityKey, h.tickInterval, h.snapshotEvery, h.engineOpts, h.logw)
	if err != nil {
		// Register NOTHING on failure: remove the claim before signalling, so
		// no half-built city is ever observable in the map, and a later
		// GetOrCreate for the same key retries from scratch.
		h.mu.Lock()
		delete(h.cities, cityKey)
		h.mu.Unlock()
		entry.err = err
		close(entry.ready)
		return nil, err
	}
	entry.city = city
	// Start the idle clock now the city is READY (FEAT-1972079942 AC-3/AC-4),
	// under the lock so it is ordered against Acquire/Release/evictIdle. If a
	// concurrent Acquire has already pinned this city (active > 0) leave
	// idleSince zero — it is in use, not idle. Timing the window from readiness
	// (not from the claim above) means an arbitrarily slow build is never a
	// candidate for eviction before the caller can Acquire it.
	h.mu.Lock()
	if entry.active == 0 {
		entry.idleSince = time.Now()
	}
	h.mu.Unlock()
	close(entry.ready)
	return city, nil
}

// Shutdown stops and removes a single city: cancel its context, join its
// goroutines, close its transport, drop it from the map. Idempotent —
// shutting an unknown or already-shut city is a no-op, never a panic. If the
// city is still being constructed by a concurrent GetOrCreate, Shutdown waits
// for construction to finish (or fail) before tearing it down, so it can
// never leak the goroutines that construction is about to start.
func (h *CityHost) Shutdown(cityKey persist.CityKey) error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	h.mu.Lock()
	entry, ok := h.cities[cityKey]
	if ok {
		delete(h.cities, cityKey)
	}
	h.mu.Unlock()
	if !ok {
		return nil // idempotent no-op
	}
	<-entry.ready // let an in-flight construction settle first
	if entry.city != nil {
		entry.city.stop()
	}
	return nil
}

// Close shuts down ALL cities cleanly and prevents further use. It cancels
// the host root context (a belt-and-braces fan-out so no city can outlive the
// host even if a stop() were missed), snapshots the current cities under the
// lock, empties the map, then joins every city. The snapshot makes the
// teardown order-independent (GR#21: the map range affects no behaviour — it
// only collects the set to stop). Idempotent.
func (h *CityHost) Close() error {
	if err := h.checkNotCopied(); err != nil {
		return err
	}
	h.rootCancel()

	// Join the evictor BEFORE tearing cities down (FEAT-1972079942 AC-5): once
	// evictorDone is observed closed, no eviction is in flight, so Close and the
	// evictor can never both call stop() on the same city (stop is not
	// idempotent). Idempotent across repeat Close calls — a closed channel
	// receives immediately.
	<-h.evictorDone

	h.mu.Lock()
	entries := make([]*cityEntry, 0, len(h.cities))
	for _, e := range h.cities {
		entries = append(entries, e)
	}
	h.cities = make(map[persist.CityKey]*cityEntry)
	h.mu.Unlock()

	for _, e := range entries {
		<-e.ready // let any in-flight construction settle
		if e.city != nil {
			e.city.stop()
		}
	}
	return nil
}

// Acquire records that one more live connection is now bound to cityKey
// (FEAT-1972079942 AC-2): it increments the city's active-connection count under
// the host lock and clears its idle clock, so the evictor will never unload a
// city with a live connection. metroserve wires wsserver's onOpen hook to this.
//
// In the metroserve wiring GetOrCreate (the handshake's transport resolve)
// always runs before this connection's onOpen, so the entry exists here. An
// Acquire on a not-yet-built city is therefore not expected — but, per AC-2,
// it is a defensive log-and-ignore (symmetric with a stray Release), never a
// panic and never a build: Acquire is a refcount, not a constructor.
func (h *CityHost) Acquire(tenantID, cityID string) {
	if err := h.checkNotCopied(); err != nil {
		return // a copied host cannot safely mutate; drop, like every guarded method
	}
	key := persist.CityKey{TenantID: tenantID, CityID: cityID}
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cities[key]
	if !ok {
		_, _ = fmt.Fprintf(h.logw, "metroserve: CityHost.Acquire for unknown city %s/%s ignored (no running city to pin)\n", tenantID, cityID)
		return
	}
	e.active++
	e.idleSince = time.Time{} // in use: never a candidate for eviction (AC-2)
}

// Release records that one connection bound to cityKey has ended
// (FEAT-1972079942 AC-2): it decrements the active-connection count under the
// host lock and, when the count reaches 0, starts the idle clock so the evictor
// can eventually unload the city. metroserve wires wsserver's onClose hook here.
//
// Never drives the count negative and never panics: a Release with no matching
// Acquire (unknown key, or a count already at 0) is logged and ignored (AC-2).
// That keeps the exactly-once onOpen/onClose contract's failure mode benign — a
// spurious onClose can only ever be a no-op, never an underflow.
func (h *CityHost) Release(tenantID, cityID string) {
	if err := h.checkNotCopied(); err != nil {
		return
	}
	key := persist.CityKey{TenantID: tenantID, CityID: cityID}
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cities[key]
	if !ok || e.active == 0 {
		_, _ = fmt.Fprintf(h.logw, "metroserve: CityHost.Release for %s/%s with no active connection ignored (stray onClose; no underflow)\n", tenantID, cityID)
		return
	}
	e.active--
	if e.active == 0 {
		// Start the idle clock only on a READY city — symmetric with the
		// GetOrCreate touch. In the production wiring Acquire/Release straddle a
		// READY city (onOpen/onClose follow a settled GetOrCreate), so this guard
		// is a no-op there. But defensively, were an Acquire+Release pair to
		// straddle CONSTRUCTION (active 0→1→0 on a still-building entry), an
		// unguarded stamp here would re-introduce the exact torn-city defect the
		// touch guard closes: a non-zero idleSince on a still-building entry the
		// evictor could then tear out mid-build. The non-blocking ready read keeps
		// idleSince zero until the city is actually built (the claim-path invariant).
		select {
		case <-e.ready:
			e.idleSince = time.Now() // idle clock starts now (AC-3)
		default:
			// still constructing — leave idleSince zero (claim-path invariant)
		}
	}
}

// runEvictor is the single background idle-eviction goroutine (FEAT-1972079942
// AC-3/AC-5), started by NewCityHost and joined by Close via evictorDone. It
// sweeps every sweepInterval and exits promptly when the host root context is
// cancelled, finishing any teardown already begun in the current sweep first.
func (h *CityHost) runEvictor() {
	defer close(h.evictorDone)
	if err := h.checkNotCopied(); err != nil {
		return // never happens (started on the real host), but keeps the guard live for astgate
	}
	ticker := time.NewTicker(h.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.rootCtx.Done():
			return
		case <-ticker.C:
			h.evictIdle()
		}
	}
}

// evictIdle runs one eviction sweep (FEAT-1972079942 AC-3/AC-4). Under the host
// lock it COLLECTS every city that has been idle (active == 0) for longer than
// idleTimeout AND removes each from the map in the SAME critical section — the
// atomic count-recheck-and-remove that closes the evict-vs-reconnect race (AC-4):
// a concurrent Acquire/GetOrCreate either takes the lock first (active > 0 or a
// fresh idleSince → not collected) or finds the entry already gone and rebuilds
// cleanly, so no connection is ever bound to a tearing-down city. The actual
// stop() (which joins the city's 3 goroutines) runs AFTER the lock is released,
// so a slow teardown never blocks Acquire/Release/GetOrCreate.
//
// GR#21: the map range only COLLECTS a set; each key's evict decision is
// independent of iteration order, and teardown order is irrelevant.
func (h *CityHost) evictIdle() {
	if err := h.checkNotCopied(); err != nil {
		return
	}
	now := time.Now()
	h.mu.Lock()
	var doomed []*cityEntry
	for key, e := range h.cities {
		if e.active == 0 && !e.idleSince.IsZero() && now.Sub(e.idleSince) > h.idleTimeout {
			doomed = append(doomed, e)
			delete(h.cities, key)
		}
	}
	h.mu.Unlock()

	// Teardown outside the lock. Each doomed entry is already removed from the
	// map, so no other path (GetOrCreate/Shutdown/Close) can observe or stop it
	// — this goroutine owns it exclusively now (AC-4/AC-5, no double stop).
	for _, e := range doomed {
		<-e.ready // let any in-flight construction settle before stopping
		if e.city != nil {
			e.city.stop()
		}
	}
}

// buildCity constructs one fully-live city: engine (deterministically seeded
// from key) → compose.Wire + guarded rehydrate (persist on when store != nil)
// → InProcTransport → subscription pump + command loop + per-city tick driver
// on a child of rootCtx. On ANY error it tears down whatever it started and
// returns the error with nothing left running, so GetOrCreate can register
// nothing. It is a free function (not a *CityHost method) deliberately: it
// takes no candidate-typed value, so it carries no SEC-020 copy-guard
// obligation.
func buildCity(rootCtx, buildCtx context.Context, store persist.Store, key persist.CityKey, tickInterval time.Duration, snapshotEvery int64, engineOpts []core.Option, logw io.Writer) (*runningCity, error) {
	opts := make([]core.Option, 0, 1+len(engineOpts))
	opts = append(opts, core.WithWorldSeed(seedForCity(key)))
	opts = append(opts, engineOpts...)
	e := core.NewEngine(opts...)

	var (
		comp *compose.Composition
		err  error
	)
	if store == nil {
		// No-persist mode: exactly compose.Wire(e, nil), same default-off path
		// as inc4's persistDir "".
		comp, err = compose.Wire(e, nil)
		if err != nil {
			return nil, fmt.Errorf("compose.Wire failed: %w", err)
		}
	} else {
		// Persist on: reuse persist.go's single-sourced guarded rehydrate so
		// the double-append guard is never re-implemented (GR#3).
		comp, err = wireAndRehydrate(buildCtx, e, store, key, logw)
		if err != nil {
			return nil, err
		}
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	ctx, cancel := context.WithCancel(rootCtx)

	pumpDone, err := e.StartSubscriptionPump(ctx, transport)
	if err != nil {
		cancel()
		_ = transport.Close()
		return nil, fmt.Errorf("city %s: StartSubscriptionPump failed: %w", key.CityID, err)
	}

	// FEAT-1972079936 Phase 1 inc3b: correlationID must be minted BEFORE
	// startCommandLoop (it needs the tick driver's own correlation ID to
	// recognise tick-cadence CommandResults — see snapshotdriver.go), then
	// reused unchanged by tickLoop below exactly as pre-inc3b.
	correlationID := string(protocol.NewCorrelationID())
	loopDone := startCommandLoop(ctx, e, transport, comp, store, key, snapshotEvery, correlationID, logw)
	tickDone := tickLoop(ctx, transport, tickInterval, correlationID)

	return &runningCity{
		engine:    e,
		comp:      comp,
		transport: transport,
		cancel:    cancel,
		pumpDone:  pumpDone,
		loopDone:  loopDone,
		tickDone:  tickDone,
	}, nil
}

// seedForCity derives a city's world seed DETERMINISTICALLY from its CityKey:
// the first 8 bytes (big-endian) of SHA-256(SHA-256(TenantID) || SHA-256(CityID)).
// Hashing each field to its own FIXED-LENGTH (32-byte) sub-digest before
// concatenating makes the derivation injective across the Tenant/City split for
// ALL inputs — including fields that themselves contain NUL bytes, which a plain
// "TenantID || 0x00 || CityID" separator does NOT (e.g. {"a","\x00b"} and
// {"a\x00","b"} both flatten to "a\x00\x00b"; the per-field hashing keeps them
// distinct). This satisfies AC-6's "no global mutable seed, no time.Now": the
// same key always yields the same seed, in any process, so a city rehydrated on
// a fresh host reconstructs the identical engine — the property AC-5's restart
// durability relies on — while two different cities get independent seeds and
// therefore independent worlds.
func seedForCity(key persist.CityKey) uint64 {
	tenant := sha256.Sum256([]byte(key.TenantID))
	city := sha256.Sum256([]byte(key.CityID))
	sum := sha256.Sum256(append(tenant[:], city[:]...))
	return binary.BigEndian.Uint64(sum[:8])
}
