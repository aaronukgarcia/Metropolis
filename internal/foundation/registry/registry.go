package registry

import (
	"sort"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Placeholder error codes — reserved range F100-F199 (data/errors.json's
// "ranges.reserved" section) belongs to foundation.registry, but none of
// the codes below exist in data/errors.json yet. Until a maintainer adds
// them (see /new-error), errs.New degrades every one of these to the
// MET-F003 "unregistered code" fallback per GR#7 — loud (the requested
// code and a cause are rendered into the MET-F003 message) rather than
// silent or fatal. Replace these constants with nothing; once the codes
// land in the registry, errs.New starts resolving them for real with no
// call-site changes required here.
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
)

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
	return func(e *ModuleEntry) { e.Health = health }
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
}

// NewRegistry constructs an empty Registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{modules: make(map[string]*moduleRecord)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SetToggleHook installs (or replaces) the callback fired on every
// successful SetStatus call. Safe to call at any time, including after
// modules are registered.
func (r *Registry) SetToggleHook(hook ToggleHook) {
	r.mu.Lock()
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

	if entry.Semver == "" {
		entry.Semver = stub.Version()
	}
	active := stub
	if entry.Status == StatusReal && real != nil {
		active = real
	}
	if !healthWasSet(opts) {
		entry.Health = active.Health()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

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

// healthWasSet reports whether opts included WithHealth, so Register
// knows not to overwrite an explicit caller-supplied health with the
// active implementation's self-reported one. It probes by applying opts
// to a sentinel and comparing against the zero Health value — cheap and
// avoids adding a second bool to every Option closure.
func healthWasSet(opts []Option) bool {
	var probe ModuleEntry
	for _, opt := range opts {
		opt(&probe)
	}
	return probe.Health != ""
}

// Get returns a copy of the entry registered under key, and false if key
// was never registered (the standard ok-idiom — never a panic, AC-12).
func (r *Registry) Get(key string) (ModuleEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

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
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedEntriesLocked()
}

// BootOrder returns a copy of every registered entry in registration
// order — the sequence engine.core's boot loop sees, deterministic given
// the same sequence of Register calls (AC-16).
func (r *Registry) BootOrder() []ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ModuleEntry, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.modules[key].entry)
	}
	return out
}

// sortedEntriesLocked must be called with r.mu held (read or write).
func (r *Registry) sortedEntriesLocked() []ModuleEntry {
	keys := make([]string, 0, len(r.modules))
	for _, key := range r.order {
		keys = append(keys, key)
	}
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
	r.mu.Lock()
	defer r.mu.Unlock()

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
	r.mu.Lock()
	defer r.mu.Unlock()

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
	r.mu.RLock()
	defer r.mu.RUnlock()

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
	r.mu.Lock()
	defer r.mu.Unlock()

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
	if target == StatusReal && rec.real == nil {
		return ToggleEvent{}, nil, errs.New(codeNoRealImpl, errs.NewCorrelationID(), map[string]any{
			"key": key,
		})
	}

	from := rec.entry.Status
	rec.entry.Status = target
	return ToggleEvent{Key: key, From: from, To: target}, r.hook, nil
}
