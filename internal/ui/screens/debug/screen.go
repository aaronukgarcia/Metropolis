package debug

import (
	"sort"
	"strings"
	"sync"
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

// Screen is F12: see doc.go for the full package contract. The zero
// value is not usable — construct with NewScreen.
//
// Concurrency: every exported method locks mu, so RequestToggle (called
// from an input-handling goroutine) and Collect (called from the render
// path) may safely run concurrently (AC-14's "-race clean" requirement).
type Screen struct {
	mu sync.Mutex

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
func (s *Screen) Collect() Snapshot {
	s.mu.Lock()
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
		s.logUnavailable("MET-U200", snap.RegistryReason)
		return
	}
	snap.RegistryAvailable = true
	snap.Registry = reg.List()
}

func (s *Screen) collectErrorTail(fn ErrorTailFunc, snap *Snapshot) {
	if fn == nil {
		snap.ErrorTailAvailable = false
		snap.ErrorTailReason = "error-tail source not configured"
		s.logUnavailable("MET-U201", snap.ErrorTailReason)
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
		s.logUnavailable("MET-U202", snap.BoWReason)
		return
	}
	summary, err := src.Summary()
	if err != nil {
		snap.BoWAvailable = false
		snap.BoWReason = err.Error()
		s.logUnavailable("MET-U202", snap.BoWReason)
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
	s.mu.Lock()
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

	s.mu.Lock()
	s.lastToggleErr = nil
	s.events = appendCapped(s.events, toggleEventText(key, target), eventLogLimit)
	s.mu.Unlock()
	return nil
}

func (s *Screen) recordToggleFailure(err error) {
	s.mu.Lock()
	s.lastToggleErr = err
	s.mu.Unlock()
}

// LastToggleError returns the most recent RequestToggle failure, or nil
// if the last attempt (if any) succeeded. AC-12's "surfaces why."
func (s *Screen) LastToggleError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
