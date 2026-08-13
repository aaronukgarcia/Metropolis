package debug

import (
	"sync"
	"sync/atomic"
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
	// self holds the address NewState gave this State at construction
	// (self.Store(s), set once, at the end of NewState, before s is
	// returned to any caller — no goroutine can have a reference to s to
	// race that Store against).
	//
	// SEC-020 wave 2: State carries the exact SEC-020 shape already found
	// and fixed in Engine.self / SubscriptionServer.self / InProcTransport
	// .self (internal/engine/core/engine.go, internal/engine/core/
	// subscribe.go, internal/protocol/transport.go — read those first,
	// this mirrors them exactly rather than inventing a variant). mu is a
	// sync.Mutex VALUE: `s2 := *s` gives s2 its OWN, independent lock.
	// header (*serialize.Header) and cheatLog ([]CheatUsedEvent) are
	// reference types a copy ALIASES — same underlying header, same
	// backing array. That combination is a direct threat to this
	// package's entire reason for existing: header.DebugTouched is a
	// sticky, forever-true hygiene guarantee (doc.go; AC-3/AC-4/AC-12/
	// AC-15) that a debug-touched save can never re-enter clean balance
	// data. A copied State's independent mu mutating the SAME aliased
	// header is two uncoordinated lock domains racing to touch one
	// hygiene flag — exactly the shape SEC-016 showed can also silently
	// hang forever (a copy's mu bytes can read as "locked" if the copy
	// was taken while the original's mu was held), so the identity check
	// must be lock-free and must run before mu is ever touched. See
	// checkNotCopied (copyguard.go) for the mechanism.
	self atomic.Pointer[State]

	mu sync.Mutex

	on      bool
	header  *serialize.Header
	persist PersistFunc
	now     Clock

	entityLookup EntityLookup
	fidelityDial FidelityDial

	cheatLog []CheatUsedEvent

	// feedbackInbox is the directory SubmitFeedback (feedback.go, FEAT-065
	// AC-DM8) writes one JSON record per submission to. Empty (the
	// default) means SubmitFeedback refuses every request — see
	// WithFeedbackInbox.
	feedbackInbox string
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
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-020 wave 2; see self's doc comment above).
	s.self.Store(s)
	return s
}

// zeroClock is the default Clock: always the zero time.Time. Kept as a
// named function (rather than an inline literal at each use site) so
// the "this is deliberately not the wall clock" reasoning lives in one
// place.
func zeroClock() time.Time { return time.Time{} }

// IsOn reports whether debug mode is currently active — the single
// read every other module should use (AC-1).
//
// SEC-020 wave 2 / ASM-002: identity-checked BEFORE mu is touched (pre-
// lock, load-bearing per SEC-016) and again immediately after mu is
// acquired (defence in depth) — same ordering as every other guarded
// method in this file, and BEFORE the copy-attack question below.
//
// On a struct-copied State, IsOn FAILS CLOSED — returns false — rather
// than growing an error return. This is a deliberate, logged design
// decision (see ASM-002 in the BOW), not an oversight: IsOn is the
// single most-consulted read in this package (every requireOn-gated
// capability funnels through it, and it is the concrete method value
// wired as engine.core's injected Speed8xGate — Speed8xGate's own
// signature is `func(correlationID string) error` with NO room for a
// "the gate itself is broken" third outcome, only allow/deny). Changing
// IsOn's signature to return an error would ripple into that contract
// and every other call site that treats IsOn as a plain bool today.
// "false" is also the SAFE reading here: a copy denies every debug
// capability exactly as if debug had never been enabled on it, which is
// the correct behaviour given a copy must never be able to grant debug
// powers or touch the header (this method's whole caller-visible
// contract). It is not SILENT to the system, only to this one caller: a
// copy hit is still recorded through the standard registry-sourced
// logging path (errs.New's side effect, return value deliberately
// discarded — the same documented pattern cheats.go's codeCheatUsed
// uses for a non-error audit line), so a copy-attack in production still
// leaves a trail even though this call site cannot surface it as an
// error without breaking the gate contract above.
func (s *State) IsOn() bool {
	if s.self.Load() != s {
		_ = errs.New(ErrStateCopied, errs.NewCorrelationID(), map[string]any{"method": "IsOn"})
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self.Load() != s {
		_ = errs.New(ErrStateCopied, errs.NewCorrelationID(), map[string]any{"method": "IsOn"})
		return false
	}
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

	// SEC-020 wave 2: identity check BEFORE mu is touched at all — a
	// struct copy's mu may already read as "locked" (copied mid-Lock from
	// the original), and calling s.mu.Lock() on such a copy before
	// rejecting it can block forever (SEC-016). This is also the method
	// with the highest stakes in the whole package: Enable is the ONLY
	// place that sticky-flags header.DebugTouched, and header is aliased
	// (not copied) across a struct copy — an unrejected copy here would
	// let a second, uncoordinated lock domain race the original to touch
	// the SAME save header.
	if err := s.checkNotCopied(correlationID, map[string]any{"source": string(source)}); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Defence-in-depth re-check under the lock (cheap: one more atomic
	// load) — mirrors Engine.RegisterPhaseHook/seal's post-lock re-check.
	if err := s.checkNotCopied(correlationID, map[string]any{"source": string(source)}); err != nil {
		return err
	}

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
//
// SEC-020 wave 2: identity-checked BEFORE mu is touched and again after
// acquisition — same ordering as Enable/IsOn. Disable has no
// correlationID parameter (matching its existing signature, which this
// fix does not change) and no error return to carry a rejection through,
// so — mirroring SubscriptionServer.PublishEngineStatus's identical
// no-correlationID/no-return situation — a copy simply has its Disable()
// call silently dropped rather than mutating its own independent `on`
// flag: dropping is safe because the real State's on-state (the only one
// that matters) is left exactly as it was, and a copy's own `on` field
// was never a channel any other code reads through anyway.
func (s *State) Disable() {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Disable"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Disable"}); err != nil {
		return
	}
	s.on = false
}

// requireOn is the single gate check every capability below (speed-8x,
// cheats, entity inspector, fidelity dial, console, fixture
// record/replay controls) routes through — one registry code
// (ErrDebugRequired), the capability name carried in ctx, never a
// silent no-op and never a panic (AC-9, AC-11).
func (s *State) requireOn(correlationID, capability string) error {
	// SEC-020 wave 2: checked here too, ahead of (and in addition to)
	// IsOn's own internal guard below — every gated capability (cheats,
	// entity inspector, fidelity dial, speed-8x, console, fixture
	// controls) funnels through requireOn, so rejecting the copy here
	// with the precise ErrStateCopied is strictly more diagnostic than
	// falling through to IsOn's fail-closed "false" and reporting the
	// generic ErrDebugRequired instead — a caller debugging "why is my
	// gate denied" should not have to rule out a copy-identity bug by
	// hand. checkNotCopied is lock-free, so this costs nothing before
	// IsOn's own pre-lock check runs.
	if err := s.checkNotCopied(correlationID, map[string]any{"capability": capability}); err != nil {
		return err
	}
	if s.IsOn() {
		return nil
	}
	return errs.New(ErrDebugRequired, correlationID, map[string]any{"capability": capability})
}

// nowFunc returns the currently configured Clock's reading, taking the
// lock only to read the func pointer itself (not to hold it across the
// call, since a caller-injected Clock could in principle be slow or
// re-enter this package).
//
// SEC-020 wave 2: identity-checked BEFORE mu is touched and again after
// acquisition — same ordering as every other guarded method here. On a
// copy, fails closed to zeroClock() (never reads the aliased original's
// `now` func pointer through the copy's own independent lock) — the same
// "never fabricate a real timestamp for an event that didn't happen on
// the real State" reasoning zeroClock's own doc comment already states
// for the no-WithClock case.
func (s *State) nowFunc() time.Time {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "nowFunc"}); err != nil {
		return zeroClock()
	}
	s.mu.Lock()
	now := s.now
	s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "nowFunc"}); err != nil {
		return zeroClock()
	}
	if now == nil {
		return zeroClock()
	}
	return now()
}
