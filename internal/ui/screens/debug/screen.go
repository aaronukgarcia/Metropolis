package debug

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// errorTailLimit is AC-6's "last 50" — Collect takes this many from the
// tail end of whatever ErrorTailFunc returns (errs.Recent() itself
// returns up to 200, oldest-first).
const errorTailLimit = 50

// eventLogLimit bounds the AC-5 world-event ticker so it cannot grow
// unbounded across a long session; only the most recent events are kept.
const eventLogLimit = 100

// Registry-sourced error codes for this package (module key ui.screen.debug).
// Each code is registered in data/errors.json's "U200-U209" reserved range.
const (
	// ErrRegistryUnavailable: the engine's registry (phase, status, metrics)
	// could not be queried or is nil.
	ErrRegistryUnavailable = "MET-U200"

	// ErrErrorTailUnavailable: the errs.Recent() tail could not be retrieved.
	ErrErrorTailUnavailable = "MET-U201"

	// ErrBoWUnavailable: the Book of Work source could not be queried.
	ErrBoWUnavailable = "MET-U202"
)

// Screen is F12: see doc.go for the full package contract. The zero
// value is not usable — construct with NewScreen.
//
// Concurrency: every exported method locks mu, so RequestToggle (called
// from an input-handling goroutine) and Collect (called from the render
// path) may safely run concurrently (AC-14's "-race clean" requirement).
type Screen struct {
	mu sync.Mutex

	// self is set once, at the end of NewScreen, to the pointer NewScreen
	// itself returns — never reassigned after that. checkNotCopied
	// (copyguard.go) compares a receiver against self to detect a struct
	// copy (SEC-020): a copy's own self field is copied unchanged, so it
	// still points at the ORIGINAL, and self.Load() != <the receiver
	// actually called through> is exactly the mismatch that identifies a
	// copy. Lock-free (atomic.Pointer), so checkNotCopied can run BEFORE
	// mu is ever touched (SEC-016's pre-lock-ordering requirement — see
	// copyguard.go for the full rationale).
	self atomic.Pointer[Screen]

	correlationID string

	reg *registry.Registry // nil: registry/phase-timing panes render "unavailable" (AC-11)

	errorTailFunc ErrorTailFunc
	runtimeFunc   RuntimeMetricsFunc
	bowSource     BoWSource // nil: BoW pane renders "unavailable"
	debugFlag     DebugFlagFunc

	events        []string
	lastToggleErr error
}

// Option customizes a Screen at construction time.
type Option func(*Screen)

// WithErrorTailSource overrides the error-tail source (defaults to
// errs.Recent). Pass nil to deliberately mark the error-tail pane
// unavailable (AC-11) — e.g. a test exercising that state.
func WithErrorTailSource(fn ErrorTailFunc) Option {
	return func(s *Screen) { s.errorTailFunc = fn }
}

// WithRuntimeSource overrides the runtime-metrics source (defaults to
// DefaultRuntimeMetricsProvider(time.Now())). Tests typically inject a
// fixed RuntimeMetrics via this to exercise AC-2 without depending on
// live process state.
func WithRuntimeSource(fn RuntimeMetricsFunc) Option {
	return func(s *Screen) { s.runtimeFunc = fn }
}

// WithBoWSource configures the AC-9 read-only BoW query source. Unset
// (the default) means the BoW pane always renders "unavailable."
func WithBoWSource(src BoWSource) Option {
	return func(s *Screen) { s.bowSource = src }
}

// WithDebugFlag configures the AC-10 debug-on/off switch source.
// Defaults to a func that always returns false — F12 is hidden until a
// caller explicitly wires the real feat.debugmode switch, matching
// M0-ENG §3's "release builds carry it, default off."
func WithDebugFlag(fn DebugFlagFunc) Option {
	return func(s *Screen) { s.debugFlag = fn }
}

// NewScreen constructs a Screen. reg may be nil (registry/phase-timing
// panes then always render "unavailable," AC-11) — a caller building F12
// before the module registry exists yet is expected to pass nil and
// supply a real Registry later via a fresh Screen (there is no
// re-wiring API, matching registry.Registry's own "fresh instance per
// boot/test" posture).
//
// correlationID is used for this screen's own registry-sourced log
// entries (unavailable-pane warnings — MET-U200/U201/U202); pass
// errs.NewCorrelationID() if the caller has no more specific ID to
// thread through.
func NewScreen(reg *registry.Registry, correlationID string, opts ...Option) *Screen {
	s := &Screen{
		correlationID: correlationID,
		reg:           reg,
		errorTailFunc: errs.Recent,
		runtimeFunc:   DefaultRuntimeMetricsProvider(time.Now()),
		debugFlag:     func() bool { return false },
	}
	for _, opt := range opts {
		opt(s)
	}
	// Stored once, last, after every field (including anything an Option
	// might set) is in its final construction state, and BEFORE s ever
	// escapes to a caller — see the self field's doc comment and
	// copyguard.go for why this is what makes checkNotCopied work.
	s.self.Store(s)
	return s
}

// Snapshot is the complete, immutable input Render draws from (AC-13):
// registry snapshot, error-tail snapshot, runtime-metrics snapshot, and
// phase-timing snapshot, plus the debug-visibility flag (AC-10) and the
// BoW/event-ticker panes. Collect builds one; Render never mutates it.
type Snapshot struct {
	DebugOn bool

	Build   BuildInfo
	Runtime RuntimeMetrics

	RegistryAvailable bool
	RegistryReason    string
	Registry          []registryRow

	ErrorTailAvailable bool
	ErrorTailReason    string
	ErrorTail          []errs.Entry

	PhaseSeries []PhaseSeries

	BoWAvailable bool
	BoWReason    string
	BoW          BoWSummary

	// Events is the AC-5 world-event ticker, oldest-first, capped at
	// eventLogLimit.
	Events []string
}

// Collect gathers a fresh Snapshot from every configured source. It is
// the one place in this package's request path that reads live/external
// state (registry, error tail, runtime provider, BoW source) — Render
// itself is then a pure function of the result (AC-13).
//
// SEC-020 / render-path rejection (ASM-014): Collect is the one call in
// F12's render loop that touches Screen state (Render itself, render.go,
// takes only the Snapshot Collect already produced — it never touches
// *Screen at all). On a struct-copied receiver this returns a ZERO
// Snapshot rather than an error or a panic: Render's existing AC-10
// contract already treats Snapshot.DebugOn == false as "draw nothing,"
// so a copy-hit degrades to exactly that — one blank frame — through
// logic that already existed for an unrelated reason, rather than a new
// failure path a render loop running 60 times/second would have to
// learn to handle. checkNotCopied's own errs.New call still leaves a
// registry-sourced MET-U203 trail (GR#7) even though this signature has
// no error to return it through — see State.IsOn's identical posture
// (internal/engine/debug/state.go) and its
// TestSEC020_IsOnCopyHit_IsLoggedNotSilent proof.
func (s *Screen) Collect() Snapshot {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Collect"}); err != nil {
		return Snapshot{}
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Collect"}); err != nil {
		s.mu.Unlock()
		return Snapshot{}
	}
	reg := s.reg
	errorTailFunc := s.errorTailFunc
	runtimeFunc := s.runtimeFunc
	bowSource := s.bowSource
	debugFlag := s.debugFlag
	events := append([]string(nil), s.events...)
	s.mu.Unlock()

	snap := Snapshot{
		DebugOn: debugFlag != nil && debugFlag(),
		Build:   collectBuildInfo(),
		Events:  events,
	}
	if runtimeFunc != nil {
		snap.Runtime = runtimeFunc()
	}

	s.collectRegistry(reg, &snap)
	s.collectErrorTail(errorTailFunc, &snap)
	s.collectPhaseSeries(reg, &snap)
	s.collectBoW(bowSource, &snap)

	return snap
}

func (s *Screen) collectRegistry(reg *registry.Registry, snap *Snapshot) {
	if reg == nil {
		snap.RegistryAvailable = false
		snap.RegistryReason = "module registry not booted"
		s.logUnavailable(ErrRegistryUnavailable, snap.RegistryReason)
		return
	}
	snap.RegistryAvailable = true
	snap.Registry = reg.List()
}

func (s *Screen) collectErrorTail(fn ErrorTailFunc, snap *Snapshot) {
	if fn == nil {
		snap.ErrorTailAvailable = false
		snap.ErrorTailReason = "error-tail source not configured"
		s.logUnavailable(ErrErrorTailUnavailable, snap.ErrorTailReason)
		return
	}
	entries := fn()
	snap.ErrorTailAvailable = true
	snap.ErrorTail = lastN(entries, errorTailLimit)
}

// lastN returns the last n elements of entries (AC-6: Recent() may
// return up to 200, oldest-first; F12 takes the last 50 itself). Never
// mutates or aliases entries' backing array beyond what's returned.
func lastN(entries []errs.Entry, n int) []errs.Entry {
	if len(entries) <= n {
		out := make([]errs.Entry, len(entries))
		copy(out, entries)
		return out
	}
	out := make([]errs.Entry, n)
	copy(out, entries[len(entries)-n:])
	return out
}

func (s *Screen) collectPhaseSeries(reg *registry.Registry, snap *Snapshot) {
	snap.PhaseSeries = make([]PhaseSeries, len(monthlyPhaseOrder))
	for i, phase := range monthlyPhaseOrder {
		if reg == nil {
			snap.PhaseSeries[i] = PhaseSeries{Phase: phase, Available: false, Reason: "module registry not booted"}
			continue
		}
		history, ok := reg.TickCostHistory(phase)
		if !ok {
			snap.PhaseSeries[i] = PhaseSeries{Phase: phase, Available: false, Reason: "phase \"" + phase + "\" not yet registered in the module registry"}
			continue
		}
		snap.PhaseSeries[i] = PhaseSeries{Phase: phase, Micros: history, Available: true}
	}
}

func (s *Screen) collectBoW(src BoWSource, snap *Snapshot) {
	if src == nil {
		snap.BoWAvailable = false
		snap.BoWReason = "BoW source not configured"
		s.logUnavailable(ErrBoWUnavailable, snap.BoWReason)
		return
	}
	summary, err := src.Summary()
	if err != nil {
		snap.BoWAvailable = false
		snap.BoWReason = err.Error()
		s.logUnavailable(ErrBoWUnavailable, snap.BoWReason)
		return
	}
	snap.BoWAvailable = true
	snap.BoW = summary
}

func (s *Screen) logUnavailable(code, cause string) {
	_ = errs.New(code, s.correlationID, map[string]any{"cause": cause})
}

// RequestToggle performs AC-4's guarded registry toggle: key must be
// registered with CanToggle: true, and confirmInput must equal key
// exactly (the F12 UI's re-confirm step) — both checks are the
// registry's own (registry.Registry.SetStatus), reused rather than
// duplicated here (GR#3). On success, a world-event is appended to the
// ticker (AC-5, "<key> module -> <STATUS>"). On failure — CanToggle
// false, a wrong/empty confirmInput, an unknown key, or no real
// implementation for a real-status target — the registry's own
// registry-sourced error is returned unchanged, nothing is appended to
// the ticker, and the registry's state is provably unchanged (SetStatus
// never mutates on any error path) — AC-12.
func (s *Screen) RequestToggle(key string, target registry.Status, confirmInput string) error {
	ctx := map[string]any{"method": "RequestToggle", "key": key}
	if err := s.checkNotCopied(errs.NewCorrelationID(), ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	reg := s.reg
	s.mu.Unlock()

	if reg == nil {
		err := errs.New("MET-U200", s.correlationID, map[string]any{"cause": "module registry not booted"})
		s.recordToggleFailure(err)
		return err
	}

	if err := reg.SetStatus(key, target, confirmInput); err != nil {
		s.recordToggleFailure(err)
		return err
	}

	// Second, independent mu.Lock() site in this same method (the first
	// only read s.reg; this one writes lastToggleErr/events) — SEC-016's
	// pre-lock check is required at EACH acquisition, not once per
	// method: nothing prevents time passing (reg.SetStatus can block on
	// the registry's own lock) between the two, during which a concurrent
	// misuse could still only ever hand this call the same *s it started
	// with, but the check is re-run here anyway rather than trusted from
	// above, matching RegisterPhaseHook's defence-in-depth posture
	// (internal/engine/core/engine.go).
	if err := s.checkNotCopied(errs.NewCorrelationID(), ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	s.lastToggleErr = nil
	s.events = appendCapped(s.events, toggleEventText(key, target), eventLogLimit)
	s.mu.Unlock()
	return nil
}

// recordToggleFailure is RequestToggle's own failure-path lock site
// (its own mu.Lock() call, independent of RequestToggle's two above) —
// guarded the same way even though every current caller already checked
// upstream, so a future caller added ahead of a check does not silently
// inherit an unguarded path to mu (same "grep for the shape, not the
// instance" reasoning as RegisterPhaseHook's defence-in-depth re-check).
// On a copy, this silently drops the write — recordToggleFailure has no
// error return to carry a rejection through, and RequestToggle's own
// caller-facing return already carries the real error via the checks
// above.
func (s *Screen) recordToggleFailure(err error) {
	if chkErr := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "recordToggleFailure"}); chkErr != nil {
		return
	}
	s.mu.Lock()
	if chkErr := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "recordToggleFailure"}); chkErr != nil {
		s.mu.Unlock()
		return
	}
	s.lastToggleErr = err
	s.mu.Unlock()
}

// LastToggleError returns the most recent RequestToggle failure, or nil
// if the last attempt (if any) succeeded. AC-12's "surfaces why." On a
// struct-copied receiver, returns the checkNotCopied error itself rather
// than nil — nil here would read as "last toggle succeeded," which is
// not a safe default to fabricate for a caller holding a copy it should
// never have.
func (s *Screen) LastToggleError() error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LastToggleError"}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LastToggleError"}); err != nil {
		return err
	}
	return s.lastToggleErr
}

// toggleEventText formats the AC-5 world-event ticker line, e.g.
// "crime module -> STUB" (M0-ENG §3's own example uses an arrow glyph;
// this package spells it "->" so it round-trips through plain-ASCII
// terminal rendering without relying on a specific glyph's availability
// — render.go is free to substitute "→" purely as a display choice,
// this is the underlying event text).
func toggleEventText(key string, target registry.Status) string {
	return key + " module -> " + strings.ToUpper(string(target))
}

func appendCapped(events []string, next string, limit int) []string {
	events = append(events, next)
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events
}

// TailEntry returns the full errs.Entry (ts, level, code, correlationId,
// module, msg, ctx) at index within snap.ErrorTail — AC-7's "Enter on a
// tail entry opens the full log": the compact tail row already only
// shows a truncated summary (render.go), but every field was present in
// the Entry all along, so "opening" it is purely returning the same
// struct in full rather than re-fetching anything.
//
// SEC-020 enumeration note: deliberately NOT checkNotCopied-guarded.
// Every other exported method on Screen is (see copyguard.go /
// screen_sec020_test.go's enumeration), but TailEntry reads zero fields
// of its *Screen receiver — snap and index are both caller-supplied
// values, s is never dereferenced. There is no aliased state here for a
// struct copy to corrupt or leak, so a guard would reject a call that is
// already 100% safe on a copy, purely for uniformity. Left unguarded on
// purpose rather than silently — if this method ever starts reading an s
// field, it must gain the guard at that point.
func (s *Screen) TailEntry(snap Snapshot, index int) (errs.Entry, bool) {
	if index < 0 || index >= len(snap.ErrorTail) {
		return errs.Entry{}, false
	}
	return snap.ErrorTail[index], true
}

// sortedPriorities is BoWSummary's fixed P0->P3 (then anything else,
// alphabetically) rendering order — never Go map iteration order.
func sortedPriorities(byPriority map[string]int) []string {
	keys := make([]string, 0, len(byPriority))
	for k := range byPriority {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, pok := priorityRank(keys[i])
		pj, pjok := priorityRank(keys[j])
		if pok && pjok {
			return pi < pj
		}
		if pok != pjok {
			return pok
		}
		return keys[i] < keys[j]
	})
	return keys
}

func priorityRank(p string) (int, bool) {
	switch p {
	case "P0":
		return 0, true
	case "P1":
		return 1, true
	case "P2":
		return 2, true
	case "P3":
		return 3, true
	default:
		return 0, false
	}
}
