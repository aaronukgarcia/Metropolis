package registry

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Registry error codes for foundation.registry — reserved range
// F100-F199 in data/errors.json's "ranges.reserved" section. Every code
// below IS registered there with real severity/module/message/remedy
// fields (GR#7; closed under BUG-008). The internal/foundation/errs
// source-scan test guards against this ever drifting out of sync again.
const (
	// codeDuplicateKey: Register called twice with the same module key.
	codeDuplicateKey = "MET-F100"
	// codeNilStub: Register called with a nil stub (GR#20 — mandatory
	// Stub pairing).
	codeNilStub = "MET-F101"
	// codeUnknownKey: a mutating call (SetStatus/UpdateHealth/
	// RecordTickCost) targeted a key that was never registered.
	codeUnknownKey = "MET-F102"
	// codeCannotToggle: SetStatus called on an entry registered with
	// CanToggle: false.
	codeCannotToggle = "MET-F103"
	// codeBadConfirm: SetStatus called with a missing/mismatched confirm
	// token.
	codeBadConfirm = "MET-F104"
	// codeNoRealImpl: SetStatus asked to move an entry to status:real but
	// no real implementation was registered for that key.
	codeNoRealImpl = "MET-F105"
	// codeRegistryCopied: a *Registry method was called on a struct copy
	// of the value NewRegistry returned (SEC-020) — see checkNotCopied's
	// doc comment.
	codeRegistryCopied = "MET-F106"

	// codeInvalidStatus: WithStatus/SetStatus supplied a Status that is
	// not one of StatusReal/StatusStub/StatusOff (BUG-310) — a misspelled
	// toggle is rejected, never silently persisted as a fourth, unknown
	// status.
	codeInvalidStatus = "MET-F108"
)

// validStatus reports whether s is one of the three documented module
// statuses. A misspelled status is hostile input, not a value to silently
// persist (BUG-310).
func validStatus(s Status) bool {
	return s == StatusReal || s == StatusStub || s == StatusOff
}

// Status is a module's boot/runtime mode.
type Status string

// The three statuses a module may be in. Off means neither the real nor
// the stub implementation is active.
const (
	StatusReal Status = "real"
	StatusStub Status = "stub"
	StatusOff  Status = "off"
)

// Health is a module's self-reported operating condition, independent of
// Status (AC-6).
type Health string

const (
	HealthOK       Health = "ok"
	HealthDegraded Health = "degraded"
	HealthError    Health = "error"
)

// Module is the interface every engine module (real or stub) implements
// to register with this package. Name and Version identify the
// implementation for the F12 row and boot log; Health is the
// implementation's own self-assessment, used only to seed a registry
// entry's initial Health field at registration — after that, Health is
// whatever the most recent Registry.UpdateHealth call set it to.
type Module interface {
	Name() string
	Version() string
	Health() Health
}

// ToggleEvent describes a completed, successful status change — the
// payload handed to every registered ToggleHook. It is the wiring point
// for the world-event/ticker consumer described in M0-ENG §3 ("Crime
// module → STUB").
type ToggleEvent struct {
	Key  string
	From Status
	To   Status
}

// ToggleHook is called exactly once per successful SetStatus call.
type ToggleHook func(ToggleEvent)

// ModuleEntry is the read-only snapshot returned by Get/List/BootOrder —
// the exact six-field shape M0-ENG §3 specifies for an F12 row: name,
// semver, status, health, last-tick cost (µs), and feature-flag source,
// plus CanToggle so the UI knows whether to offer a toggle at all.
type ModuleEntry struct {
	Key                string
	Semver             string
	Status             Status
	Health             Health
	LastTickCostMicros uint64
	FlagSource         string
	CanToggle          bool

	// healthSet records whether WithHealth explicitly set Health during
	// Register — a first-class flag so Register does not have to re-run the
	// Option closures (which would fire any future side-effectful option
	// twice) to detect "was health set" (BUG-310). Unexported: it is a
	// construction bookkeeping bit, never part of the F12 snapshot.
	healthSet bool
}

// Option customizes a Register call. Unset options take the defaults
// documented on each With* function.
type Option func(*ModuleEntry)

// WithVersion sets the entry's semver. Defaults to stub.Version().
func WithVersion(semver string) Option {
	return func(e *ModuleEntry) { e.Semver = semver }
}

// WithStatus sets the entry's initial status. Defaults to StatusStub —
// every module is stub-backed until explicitly told otherwise, matching
// M0-ENG §2's "modules go real one at a time."
func WithStatus(status Status) Option {
	return func(e *ModuleEntry) { e.Status = status }
}

// WithCanToggle declares, at registration time, whether the F12 UI may
// runtime-toggle this module's status. Defaults to false — a module must
// opt in to being toggled, never inferred.
func WithCanToggle(canToggle bool) Option {
	return func(e *ModuleEntry) { e.CanToggle = canToggle }
}

// WithFlagSource records the feature-flag source identifier (e.g. an env
// var or config key) that decided this module's initial status. Defaults
// to "".
func WithFlagSource(source string) Option {
	return func(e *ModuleEntry) { e.FlagSource = source }
}

// WithHealth overrides the entry's initial health. Defaults to whichever
// implementation is initially active (real if status is StatusReal and a
// real implementation was supplied, otherwise the mandatory stub)
// reporting its own Health().
func WithHealth(health Health) Option {
	return func(e *ModuleEntry) { e.Health = health; e.healthSet = true }
}

// moduleRecord is the registry's internal storage for one key: the public
// snapshot fields plus the real/stub implementations and the tick-cost
// ring buffer backing the F12 sparkline (last 60 samples).
type moduleRecord struct {
	entry   ModuleEntry
	real    Module // may be nil: not every module has a real impl yet
	stub    Module // never nil past Register (GR#20)
	history [60]uint64
	count   int // number of samples written so far, capped at 60
	pos     int // next write position in history
}

// RegistryOption customizes a Registry at construction time.
type RegistryOption func(*Registry)

// WithToggleHook installs the callback fired on every successful
// SetStatus call. Equivalent to calling SetToggleHook after construction;
// provided so a Registry can be fully wired in one NewRegistry call.
func WithToggleHook(hook ToggleHook) RegistryOption {
	return func(r *Registry) { r.hook = hook }
}

// Registry is the module registry: one row per registered module,
// safe for concurrent use by any number of readers (F12 render path) and
// writers (engine.core's tick/boot path) — see the package doc's
// "one registry, two consumers" note.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]*moduleRecord
	order   []string // registration order, for BootOrder (AC-16)
	hook    ToggleHook

	// self holds the address NewRegistry gave this Registry at
	// construction (self.Store(r), set once, at the end of NewRegistry,
	// before r is returned to any caller — no goroutine can have a
	// reference to r to race that Store against).
	//
	// SEC-020: Registry is exported with an exported constructor, and
	// every field here is unexported — but Go does not stop a caller
	// that holds the *Registry NewRegistry returned from dereferencing
	// it and copying the struct value ('r2 := *r' is legal, unsafe-free,
	// reflect-free Go from outside this package; go vet's copylocks
	// check flags only a directly-visible literal like that one, never a
	// copy built by other means — see
	// internal/engine/core/sec014_poc_test.go's e2Copy for the
	// unsafe.Pointer byte-copy precedent this package's own tests use to
	// keep exercising the regression without a vet-caught literal). mu
	// is a plain sync.RWMutex VALUE, so the copy gets its OWN,
	// independent lock — but modules (a map) and order (a slice
	// backing array) are reference types, so the copy ALIASES both. Two
	// independent locks, one shared map and one shared backing array:
	// exactly the SEC-003/014/016/018/019 shape, on the module system's
	// own bookkeeping this time.
	//
	// atomic.Pointer, not a plain *Registry field, for the SEC-016
	// reason every other guarded type in this codebase uses one (see
	// Engine.self, engine/core/engine.go; SubscriptionServer.self,
	// engine/core/subscribe.go; InProcTransport.self, protocol/
	// transport.go; State.self, engine/debug/copyguard.go): the identity
	// check must be race-safe AND must run before mu is ever touched,
	// because a struct copy taken while the ORIGINAL's mu happened to be
	// held (Lock'd or RLock'd) captures those mutex bytes read as
	// "locked" — the copy's own next Lock()/RLock() call on that
	// captured state can then park forever, since nothing will ever
	// Unlock() that specific copy's address. A plain field read racing a
	// concurrent struct copy has no defined result under the Go memory
	// model; atomic.Pointer's Load/Store do.
	self atomic.Pointer[Registry]
}

// NewRegistry constructs an empty Registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{modules: make(map[string]*moduleRecord)}
	for _, opt := range opts {
		opt(r)
	}
	// Stored exactly once, here, at the end of construction, before r is
	// returned to any caller — no goroutine can have a reference to r to
	// race this Store against (SEC-020; mirrors NewEngine/
	// NewSubscriptionServer/NewInProcTransport/NewState — see self's doc
	// comment above).
	r.self.Store(r)
	return r
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Registry value (SEC-020, mirroring Engine.checkNotCopied/
// SubscriptionServer.checkNotCopied/InProcTransport.checkNotCopied/
// State.checkNotCopied). Deliberately lock-free — a single
// atomic.Pointer.Load, requiring nothing else, not r.mu — so it is safe
// and correct to call BEFORE r.mu is EVER touched, including for
// RLock().
//
// That ordering is not optional (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (r.mu.RLock()'d by a concurrent reader, or
// Lock()'d by a concurrent writer — RWMutex's internal state encodes
// both), and acquiring — even just attempting — a copy's own mu in that
// state can block forever, since nothing will ever Unlock()/RUnlock()
// that specific copy's address. A guard placed AFTER the lock can never
// run for that attack, because the attack IS acquiring the lock;
// rejecting the copy here, before Lock()/RLock() is ever called, means
// that hang path is never reached at all. Every one of this package's
// nine r.mu.Lock()/r.mu.RLock() call sites (see the enumeration in this
// file's package-level SEC-020 note below the imports, and
// registry_test.go) is guarded this way, RLock sites included — a
// reader on a copy is exactly as broken as a writer on one.
//
// A nil r.self.Load() (a Registry constructed as a bare `Registry{}`,
// `new(Registry)`, or a hand-built literal rather than via NewRegistry,
// so self was never stored) is treated the same as a mismatch and
// rejected the same way — every documented construction path is
// NewRegistry, so an unset self is itself a misuse this same error
// correctly names, and rejecting it here also means such a value's
// nil modules map is never reached either.
//
// This hand-rolled guard is the pre-existing implementation that the
// reusable CopyGuard[T] wrapper generalises for NEW modules — see
// copyguard.go, which also carries the defensive-copy helpers
// (CloneMap/CloneSlice) closing SEC-066, alongside SEC-080/SEC-093's
// safe-coercion siblings in foundation.num (feat.securehelpers, FEAT-135).
func (r *Registry) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(codeRegistryCopied, correlationID, ctx)
	}
	return nil
}

// SetToggleHook installs (or replaces) the callback fired on every
// successful SetStatus call. Safe to call at any time, including after
// modules are registered.
func (r *Registry) SetToggleHook(hook ToggleHook) {
	// SEC-020: identity check BEFORE r.mu is touched at all — see
	// checkNotCopied's doc comment for why a copy must never acquire its
	// own mu.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetToggleHook"}); err != nil {
		return
	}
	r.mu.Lock()
	// Defence-in-depth re-check under the lock (cheap: one more atomic
	// load) — mirrors Engine.RegisterPhaseHook/seal's post-lock re-check
	// (engine/core/engine.go).
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetToggleHook"}); err != nil {
		r.mu.Unlock()
		return
	}
	r.hook = hook
	r.mu.Unlock()
}

// Register adds a module under key, pairing it with its mandatory stub
// (GR#20: a nil stub is rejected with a registry-sourced error, never
// accepted). real may be nil if no real implementation exists yet — such
// a module can only ever be StatusStub or StatusOff until a real
// implementation is registered (there is no re-registration API; a fresh
// Registry is expected per boot/test).
//
// Duplicate keys are rejected with a registry-sourced error rather than
// silently overwriting the earlier registration (AC-11).
func (r *Registry) Register(key string, real, stub Module, opts ...Option) error {
	// SEC-020: identity check BEFORE r.mu is touched at all (well ahead
	// of the r.mu.Lock() below) — see checkNotCopied's doc comment.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "Register"}); err != nil {
		return err
	}
	if stub == nil {
		return errs.New(codeNilStub, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}

	entry := ModuleEntry{
		Key:    key,
		Status: StatusStub,
		Health: HealthOK,
	}
	for _, opt := range opts {
		opt(&entry)
	}
	entry.Key = key // opts must never override the registration key

	if !validStatus(entry.Status) {
		return errs.New(codeInvalidStatus, errs.NewCorrelationID(), map[string]any{
			"key": key, "status": string(entry.Status),
		})
	}

	if entry.Semver == "" {
		entry.Semver = stub.Version()
	}
	active := stub
	if entry.Status == StatusReal && real != nil {
		active = real
	}
	if !entry.healthSet {
		entry.Health = active.Health()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Defence-in-depth re-check under the lock (cheap: one more atomic
	// load) — mirrors Engine.RegisterPhaseHook/seal's post-lock re-check.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "Register"}); err != nil {
		return err
	}

	if _, exists := r.modules[key]; exists {
		return errs.New(codeDuplicateKey, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	if entry.Status == StatusReal && real == nil {
		return errs.New(codeNoRealImpl, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}

	r.modules[key] = &moduleRecord{entry: entry, real: real, stub: stub}
	r.order = append(r.order, key)
	return nil
}

// Get returns a copy of the entry registered under key, and false if key
// was never registered (the standard ok-idiom — never a panic, AC-12).
func (r *Registry) Get(key string) (ModuleEntry, bool) {
	// SEC-020: identity check BEFORE r.mu.RLock() — a reader on a copy
	// is exactly as broken as a writer (see checkNotCopied's doc
	// comment).
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "Get"}); err != nil {
		return ModuleEntry{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "Get"}); err != nil {
		return ModuleEntry{}, false
	}

	rec, ok := r.modules[key]
	if !ok {
		return ModuleEntry{}, false
	}
	return rec.entry, true
}

// List returns a copy of every registered entry, sorted by key — stable
// and deterministic across repeated calls (AC-10), never Go map iteration
// order (GR#21).
func (r *Registry) List() []ModuleEntry {
	// SEC-020 / ASM-REG-001 (see this file's accessor-guarding note near
	// TickCostHistory): guarded even though List already returns a
	// defensive copy — the danger is r.mu.RLock() itself hanging on a
	// copy's captured-locked mutex bytes, not merely the data List would
	// hand back. See checkNotCopied's doc comment.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "List"}); err != nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "List"}); err != nil {
		return nil
	}
	return r.sortedEntriesLocked()
}

// BootOrder returns a copy of every registered entry in registration
// order — the sequence engine.core's boot loop sees, deterministic given
// the same sequence of Register calls (AC-16).
func (r *Registry) BootOrder() []ModuleEntry {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BootOrder"}); err != nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BootOrder"}); err != nil {
		return nil
	}

	out := make([]ModuleEntry, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.modules[key].entry)
	}
	return out
}

// sortedEntriesLocked must be called with r.mu held (read or write).
func (r *Registry) sortedEntriesLocked() []ModuleEntry {
	keys := make([]string, 0, len(r.modules))
	keys = append(keys, r.order...)
	sort.Strings(keys)

	out := make([]ModuleEntry, 0, len(keys))
	for _, key := range keys {
		out = append(out, r.modules[key].entry)
	}
	return out
}

// UpdateHealth sets a module's health independently of its status (AC-6).
// Returns a registry-sourced error if key was never registered.
func (r *Registry) UpdateHealth(key string, health Health) error {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "UpdateHealth"}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "UpdateHealth"}); err != nil {
		return err
	}

	rec, ok := r.modules[key]
	if !ok {
		return errs.New(codeUnknownKey, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	rec.entry.Health = health
	return nil
}

// RecordTickCost records a module's most recent per-tick cost in
// microseconds. The registry stores what it is told; it never measures
// anything itself — engine.core's phase pipeline is the sole intended
// caller, once per module per tick. Entry.LastTickCostMicros always
// reflects the latest call (not an accumulating sum); the last 60 samples
// are additionally retained for TickCostHistory, the F12 sparkline
// source.
func (r *Registry) RecordTickCost(key string, micros uint64) error {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "RecordTickCost"}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "RecordTickCost"}); err != nil {
		return err
	}

	rec, ok := r.modules[key]
	if !ok {
		return errs.New(codeUnknownKey, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	rec.entry.LastTickCostMicros = micros
	rec.history[rec.pos] = micros
	rec.pos = (rec.pos + 1) % len(rec.history)
	if rec.count < len(rec.history) {
		rec.count++
	}
	return nil
}

// TickCostHistory returns up to the last 60 RecordTickCost values for
// key, oldest first — the F12 per-phase sparkline source. Returns false
// if key was never registered.
func (r *Registry) TickCostHistory(key string) ([]uint64, bool) {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "TickCostHistory"}); err != nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "TickCostHistory"}); err != nil {
		return nil, false
	}

	rec, ok := r.modules[key]
	if !ok {
		return nil, false
	}

	out := make([]uint64, rec.count)
	start := (rec.pos - rec.count + len(rec.history)) % len(rec.history)
	for i := 0; i < rec.count; i++ {
		out[i] = rec.history[(start+i)%len(rec.history)]
	}
	return out, true
}

// SetStatus is the guarded runtime toggle (M0-ENG §3): it changes key's
// status only when the entry was registered with CanToggle: true and the
// caller supplies confirmToken equal to key — the lightweight
// confirm-token pattern the F12 UI is expected to satisfy by having the
// user re-confirm the exact module key before the toggle is issued,
// rather than a single stray click flipping a live module mid-tick.
//
// On success, the registered ToggleHook (if any) fires exactly once with
// the before/after status — the wiring point for the "Crime module →
// STUB" world-event ticker (M0-ENG §3). The hook is invoked outside the
// registry's lock so a hook that itself calls back into the registry
// cannot deadlock.
func (r *Registry) SetStatus(key string, target Status, confirmToken string) error {
	event, hook, err := r.setStatusLocked(key, target, confirmToken)
	if err != nil {
		return err
	}
	if hook != nil {
		hook(event)
	}
	return nil
}

// setStatusLocked performs the guarded mutation under the registry's
// write lock and returns the hook to invoke (captured under the same
// lock, so it can never race with a concurrent SetToggleHook) alongside
// the event. The caller invokes the hook after the lock is released.
func (r *Registry) setStatusLocked(key string, target Status, confirmToken string) (ToggleEvent, ToggleHook, error) {
	// SEC-020: identity check BEFORE r.mu is touched — SetStatus itself
	// never touches r.mu directly, this method (its sole caller) is the
	// actual lock site, so the guard lives here.
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "SetStatus"}); err != nil {
		return ToggleEvent{}, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"key": key, "method": "SetStatus"}); err != nil {
		return ToggleEvent{}, nil, err
	}

	rec, ok := r.modules[key]
	if !ok {
		return ToggleEvent{}, nil, errs.New(codeUnknownKey, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	if !rec.entry.CanToggle {
		return ToggleEvent{}, nil, errs.New(codeCannotToggle, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	if confirmToken == "" || confirmToken != key {
		return ToggleEvent{}, nil, errs.New(codeBadConfirm, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}
	if !validStatus(target) {
		return ToggleEvent{}, nil, errs.New(codeInvalidStatus, errs.NewCorrelationID(), map[string]any{
			"key": key, "status": string(target),
		})
	}
	if target == StatusReal && rec.real == nil {
		return ToggleEvent{}, nil, errs.New(codeNoRealImpl, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}

	from := rec.entry.Status
	rec.entry.Status = target
	return ToggleEvent{Key: key, From: from, To: target}, r.hook, nil
}
