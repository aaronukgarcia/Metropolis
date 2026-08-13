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

	// self holds the address NewStore gave this Store at construction
	// (self.Store(s), set once, at the end of NewStore, never stored to
	// again). BUG-125's fix, mirroring engine.core's
	// Engine.self/checkNotCopied (SEC-014/SEC-016) EXACTLY (GR#3 — don't
	// invent a new pattern; see World.self/World.checkNotCopied for
	// another verbatim application of the same idiom): Store is
	// exported, so any holder of a *Store[T, PT] can legally write
	// `s2 := *s1` — no unsafe, no reflect, plain Go. reloadMu and cbMu
	// are plain sync.Mutex values, so the copy s2 gets its OWN,
	// independently-zeroed locks — but s2.cbs (a slice, a reference
	// type once it has backing-array capacity) can still ALIAS s1.cbs,
	// and s2.self still points at the ORIGINAL s1 (copied by value,
	// unchanged). Kestrel's Destructive review (BUG-125) reproduced
	// this live: concurrent OnChange calls through the original and a
	// copy, each serialized by its own independent cbMu but appending
	// to the same shared cbs backing array, produced an actual -race
	// data race (1 of 5 runs) — the literal SEC-020 shape, not a
	// hypothetical. checkNotCopied compares the receiver's own address
	// against self, and a copy's address can never equal the
	// original's.
	//
	// atomic.Pointer[Store[T, PT]], not a plain *Store[T, PT], for the
	// same reason SEC-016 forced Engine.self's type: a plain,
	// unsynchronized field read done lock-free, concurrently with a
	// struct copy that touches the whole struct's memory as one
	// operation, has no defined result in the Go memory model.
	// atomic.Pointer makes self's own load/store well-defined
	// regardless of what else is concurrently happening to neighbouring
	// fields: Store (the verb) happens exactly once, in NewStore,
	// before any goroutine can have a reference to s to race against;
	// every subsequent Load is a single lock-free atomic read.
	self atomic.Pointer[Store[T, PT]]
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
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-016; see self's doc comment).
	s.self.Store(s)
	return s, nil
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Store value (BUG-125, mirroring engine.core's
// Engine.checkNotCopied exactly — SEC-014/SEC-016 family). Deliberately
// lock-free — a single atomic.Pointer.Load, requiring nothing else, not
// s.reloadMu, not s.cbMu — so it is safe and correct to call BEFORE
// either lock is ever touched. A nil s.self.Load() (a Store constructed
// as a bare `Store[T, PT]{}`/`new(Store[T, PT])` rather than via
// NewStore, so self was never stored) is treated the same as a mismatch
// and rejected the same way — every documented construction path is
// NewStore, so an unset self is itself a misuse this same error
// correctly names.
func (s *Store[T, PT]) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(CodeStoreCopied, correlationID, ctx)
	}
	return nil
}

// Get returns the currently-live value. Safe for concurrent use with
// Reload. Returns CodeStoreCopied if called on a struct-copied Store
// value (BUG-125/SEC-020) rather than the *Store NewStore returned.
func (s *Store[T, PT]) Get() (*T, error) {
	correlationID := errs.NewCorrelationID()
	if err := s.checkNotCopied(correlationID, map[string]any{"path": s.path}); err != nil {
		return nil, err
	}
	return s.val.Load(), nil
}

// OnChange registers a callback invoked (synchronously, after the
// atomic swap) every time Reload succeeds. Intended for the debug
// console / any module that wants to react to a hot-reloaded config
// rather than only reading the latest value on its own schedule.
// Returns CodeStoreCopied if called on a struct-copied Store value
// (BUG-125/SEC-020) rather than the *Store NewStore returned — checked
// BEFORE cbMu is ever touched, so a copy never gets as far as appending
// into (and thereby racing) the shared cbs backing array.
func (s *Store[T, PT]) OnChange(cb func(*T)) error {
	correlationID := errs.NewCorrelationID()
	if err := s.checkNotCopied(correlationID, map[string]any{"path": s.path}); err != nil {
		return err
	}
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	s.cbs = append(s.cbs, cb)
	return nil
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
	// Checked BEFORE reloadMu/cbMu are ever touched (SEC-016 ordering —
	// see self's doc comment): a struct copy's reloadMu/cbMu can be
	// byte-for-byte "currently locked" if the copy was taken while the
	// original's lock was held, and acquiring a copy's own lock in that
	// state can block forever, since nothing will ever Unlock() that
	// specific copy's address. Rejecting the copy here means that hang
	// path — and the cbs-aliasing race Kestrel reproduced (BUG-125) —
	// is never reached at all.
	if err := s.checkNotCopied(correlationID, map[string]any{"path": s.path}); err != nil {
		return err
	}

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
