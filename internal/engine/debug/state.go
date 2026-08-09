package debug

import (
	"sync"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Clock is the injectable time source this package uses for the one
// legitimate timestamp it produces (a cheat-used audit entry, AC-14).
// This package never reads the wall clock directly on any path — the
// caller (engine bootstrap) must inject the sim clock's Now method via
// WithClock, exactly as foundation/errs.SetClock works. A State
// constructed without WithClock stamps every event with the zero
// time.Time rather than falling back to the stdlib wall clock — see
// NewState's doc comment for why the fallback is deliberately not the
// wall clock.
type Clock func() time.Time

// PersistFunc is the caller-injected write-through for the sticky
// DebugTouched flag Enable sets on the active save header (AC-3, AC-12).
// This package does not own save/header persistence (int.serializer's
// job, already frozen) — PersistFunc is the seam a caller with a real
// write path (e.g. engine.core's persist.go) wires in via WithPersist.
// A nil PersistFunc (the default) is treated as "persistence is a
// no-op success" — appropriate for callers that only need the in-memory
// Header.DebugTouched bit (e.g. a header not yet backed by any file).
type PersistFunc func() error

// EnableSource identifies which of the three converging enable paths
// (§14 / M0-ENG §3) requested debug ON. All three route through Enable,
// which is the only place IsOn's value is ever set to true — this is
// what AC-1 means by "single source of truth": whichever source a
// caller used, the same State.IsOn() read reflects it afterward.
type EnableSource string

const (
	// SourceFlag is the --debug CLI flag path.
	SourceFlag EnableSource = "flag"
	// SourceConfig is the config-file path.
	SourceConfig EnableSource = "config"
	// SourcePalette is the ":debug on" in-game palette command path.
	SourcePalette EnableSource = "palette"
)

// validEnableSource reports whether s is one of the three documented
// sources. Not a map so a future new source is a one-line switch-case
// addition, not a map-iteration determinism concern (GR#21) — not that
// this particular check is ever on the tick path, but the package-wide
// discipline is to avoid maps for closed-set membership checks anyway.
func validEnableSource(s EnableSource) bool {
	switch s {
	case SourceFlag, SourceConfig, SourcePalette:
		return true
	default:
		return false
	}
}

// State is feat.debugmode's single source of truth (AC-1): every other
// module (ui.screen.debug, engine.core's speed control, cheat call
// sites) is expected to read whether debug is on through one shared
// *State's IsOn(), never a locally-cached copy. Safe for concurrent use.
//
// The zero State is not ready to use — construct with NewState, which
// applies sane zero-value defaults (off, no header, no persist hook, no
// injected seams) that every gated method below handles explicitly
// rather than panicking on.
type State struct {
	mu sync.Mutex

	on      bool
	header  *serialize.Header
	persist PersistFunc
	now     Clock

	entityLookup EntityLookup
	fidelityDial FidelityDial

	cheatLog []CheatUsedEvent
}

// Option customizes a new State. Unset options take the defaults
// documented on each With* function.
type Option func(*State)

// WithHeader wires the active save header Enable sticky-flags (AC-3).
// Required before Enable will succeed — see ErrNoHeaderConfigured.
func WithHeader(h *serialize.Header) Option {
	return func(s *State) { s.header = h }
}

// WithPersist wires the write-through PersistFunc Enable calls after
// sticky-flagging the header in memory (AC-12). Defaults to nil (a
// no-op success) if unset — see PersistFunc's doc comment.
func WithPersist(fn PersistFunc) Option {
	return func(s *State) { s.persist = fn }
}

// WithClock overrides the injectable time source used to stamp
// cheat-used audit entries (AC-14). See Clock's doc comment.
func WithClock(now Clock) Option {
	return func(s *State) { s.now = now }
}

// WithEntityLookup wires the resolver InspectEntity uses (AC-7).
func WithEntityLookup(fn EntityLookup) Option {
	return func(s *State) { s.entityLookup = fn }
}

// WithFidelityDial wires the implementation the gated FidelityDial
// accessor exposes once debug is on (AC-8).
func WithFidelityDial(d FidelityDial) Option {
	return func(s *State) { s.fidelityDial = d }
}

// NewState constructs a State. With no options it is off (AC-2), has no
// header/persist/entity-lookup/fidelity-dial seam wired, and its Clock
// stamps every event with the zero time.Time — deliberately NOT the
// stdlib wall-clock function (which may not appear anywhere in this
// package's non-test source, AC-14/GR#21); a caller that cares about
// real cheat-used timestamps must call WithClock with the sim clock's
// Now method, exactly as foundation/errs.SetClock works.
func NewState(opts ...Option) *State {
	s := &State{
		now: zeroClock,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// zeroClock is the default Clock: always the zero time.Time. Kept as a
// named function (rather than an inline literal at each use site) so
// the "this is deliberately not the wall clock" reasoning lives in one
// place.
func zeroClock() time.Time { return time.Time{} }

// IsOn reports whether debug mode is currently active — the single
// read every other module should use (AC-1).
func (s *State) IsOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on
}

// Enable turns debug mode on via source, sticky-flagging the wired save
// header first (AC-3) and only then flipping IsOn() to true. Order
// matters: if there is no header wired (ErrNoHeaderConfigured) or the
// injected PersistFunc reports a failure (ErrEnablePersistFailed),
// Enable returns a registry-sourced error and IsOn() remains false —
// reporting success while the sticky flag silently failed to persist
// would itself violate the hygiene contract this package exists to
// protect (AC-12).
//
// Calling Enable again while already on (any source, including a
// different one than originally used) is a harmless re-touch: the
// header write is idempotent (TouchDebug/MergeDebugTouched only ever OR
// true in) and IsOn() simply stays true.
func (s *State) Enable(source EnableSource, correlationID string) error {
	if !validEnableSource(source) {
		return errs.New(ErrUnknownEnableSource, correlationID, map[string]any{"source": string(source)})
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.header == nil {
		return errs.New(ErrNoHeaderConfigured, correlationID, map[string]any{"source": string(source)})
	}

	s.header.TouchDebug()

	if s.persist != nil {
		if err := s.persist(); err != nil {
			return errs.Wrap(ErrEnablePersistFailed, correlationID, err, map[string]any{"source": string(source)})
		}
	}

	s.on = true
	return nil
}

// Disable turns debug mode off for the rest of the process, WITHOUT
// touching the header's DebugTouched flag — that flag is sticky
// forever, by design (AC-4, AC-15, and doc.go's package-level warning).
// Do not add a path here (or anywhere) that clears DebugTouched.
func (s *State) Disable() {
	s.mu.Lock()
	s.on = false
	s.mu.Unlock()
}

// requireOn is the single gate check every capability below (speed-8x,
// cheats, entity inspector, fidelity dial, console, fixture
// record/replay controls) routes through — one registry code
// (ErrDebugRequired), the capability name carried in ctx, never a
// silent no-op and never a panic (AC-9, AC-11).
func (s *State) requireOn(correlationID, capability string) error {
	if s.IsOn() {
		return nil
	}
	return errs.New(ErrDebugRequired, correlationID, map[string]any{"capability": capability})
}

// nowFunc returns the currently configured Clock's reading, taking the
// lock only to read the func pointer itself (not to hold it across the
// call, since a caller-injected Clock could in principle be slow or
// re-enter this package).
func (s *State) nowFunc() time.Time {
	s.mu.Lock()
	now := s.now
	s.mu.Unlock()
	if now == nil {
		return zeroClock()
	}
	return now()
}
