package data

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DebugFlag is the shared "is debug mode on" switch every Store checks
// before allowing a Reload (M0-ENG §3: debug is a runtime feature
// switch). It is a tiny atomic bool wrapper rather than a plain bool so
// the same flag instance can be shared across every config file's
// Store and toggled at any time from whatever owns the debug-mode
// decision (feat.debugmode / ui.screen.debug, once those land) without
// a data race. The zero value is disabled.
type DebugFlag struct {
	enabled atomic.Bool
}

// Enable turns debug mode on.
func (f *DebugFlag) Enable() { f.enabled.Store(true) }

// Disable turns debug mode off.
func (f *DebugFlag) Disable() { f.enabled.Store(false) }

// Enabled reports the current state.
func (f *DebugFlag) Enabled() bool { return f.enabled.Load() }

// Store holds the live, hot-reloadable value for one config file. Get
// is always safe to call concurrently with Reload: the swap on success
// is a single atomic.Pointer store, so a concurrent reader never
// observes a torn/partially-written struct (AC-4/AC-13), and a failed
// Reload never touches the stored value, so the previously-loaded
// config remains fully intact and readable (AC-11).
//
// This type does not run a file-watching goroutine — no polling, no
// fsnotify/ReadDirectoryChangesW. Reload is an explicit operation the
// caller triggers; watching the filesystem for changes and deciding
// when to call Reload is the debug console's job (a future
// feat.debugmode/ui.screen.debug concern), not this package's.
type Store[T any, PT interface {
	*T
	Validator
}] struct {
	path  string
	debug *DebugFlag

	val atomic.Pointer[T]

	reloadMu sync.Mutex // serializes concurrent Reload calls / callback dispatch order

	cbMu sync.Mutex
	cbs  []func(*T)
}

// NewStore performs the initial (non-debug-gated) load of path and
// returns a Store ready for concurrent Get and (once debug is enabled
// on the shared flag) Reload. debug may be nil, in which case Reload
// always behaves as if debug mode is disabled (CodeReloadDebugRequired).
func NewStore[T any, PT interface {
	*T
	Validator
}](path string, debug *DebugFlag, correlationID string) (*Store[T, PT], error) {
	v, err := Load[T, PT](path, correlationID)
	if err != nil {
		return nil, err
	}
	s := &Store[T, PT]{path: path, debug: debug}
	s.val.Store(&v)
	return s, nil
}

// Get returns the currently-live value. Safe for concurrent use with
// Reload.
func (s *Store[T, PT]) Get() *T {
	return s.val.Load()
}

// OnChange registers a callback invoked (synchronously, after the
// atomic swap) every time Reload succeeds. Intended for the debug
// console / any module that wants to react to a hot-reloaded config
// rather than only reading the latest value on its own schedule.
func (s *Store[T, PT]) OnChange(cb func(*T)) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	s.cbs = append(s.cbs, cb)
}

// Reload re-reads and re-validates the file at the Store's path and,
// only on success, atomically swaps it in as the new live value —
// never a partial or in-place mutation of the previous value. It is a
// no-op error (CodeReloadDebugRequired) unless the Store's DebugFlag is
// non-nil and enabled, per feat.debugmode's gating pattern (AC-5): this
// package treats "debug mode required" as the correct, documented
// behaviour for the gate, not an implementation detail callers should
// route around.
func (s *Store[T, PT]) Reload(correlationID string) error {
	if s.debug == nil || !s.debug.Enabled() {
		return errs.New(CodeReloadDebugRequired, correlationID, map[string]any{
			"path": s.path,
		})
	}

	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	v, err := Load[T, PT](s.path, correlationID)
	if err != nil {
		// AC-11: the previously-loaded config is untouched — we return
		// before ever calling s.val.Store.
		return errs.Wrap(CodeReloadFailed, correlationID, err, map[string]any{
			"path": s.path,
		})
	}

	s.val.Store(&v)

	s.cbMu.Lock()
	cbs := append([]func(*T){}, s.cbs...)
	s.cbMu.Unlock()
	for _, cb := range cbs {
		cb(&v)
	}
	return nil
}
