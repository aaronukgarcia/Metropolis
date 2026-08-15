package diagrams

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrEngineCopied: a *Engine method was called on a struct copy of the
// value NewEngine returned (SEC-020 — see docs/planning/dev-team-process.md's
// Weakness pattern #1/#3). checkNotCopied (below) rejects every such call
// before it does anything else. Like ui.screens.map's ErrMapScreenCopied
// and ui.screens.debug's ErrScreenCopied, this package has no codes.go of
// its own (errors.go holds only the local MET-U900 decode error), so this
// constant lives here, next to the one thing that produces it.
const ErrEngineCopied = "MET-U905"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Engine value, mirroring World.checkNotCopied (internal/engine/
// world/grid.go), MapScreen.checkNotCopied (internal/ui/screens/map/
// copyguard.go), and debug.Screen.checkNotCopied (internal/ui/screens/
// debug/copyguard.go) — same mechanism, same ordering rationale.
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring nothing
// else, not e.mu — so it is safe and correct to call BEFORE e.mu is ever
// touched.
//
// Why this matters for Engine specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while cache (map[uint64]cacheEntry)
// is a reference type a copy ALIASES. An unrejected copy is therefore a
// second lock domain that can read/mutate the SAME aliased map as the
// original — exactly SEC-020's "two locks, one referent" shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`e2 := *e` while another goroutine has
// e.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's
// own mu in that state can block forever, since nothing will ever
// Unlock() that specific copy's address. A guard placed AFTER the lock
// can never run for that attack, because the attack IS acquiring the
// lock; rejecting the copy here, before Lock() is ever called, means
// that hang path is never reached at all.
//
// A nil e.self.Load() (an Engine constructed as a bare `Engine{}` or
// `new(Engine)` rather than via NewEngine, so self was never stored) is
// treated the same as a mismatch and rejected the same way — every
// documented construction path is NewEngine, so an unset self is itself a
// misuse this same error correctly names.
func (e *Engine) checkNotCopied() error {
	if e.self.Load() != e {
		return errs.New(ErrEngineCopied, errs.NewCorrelationID(), nil)
	}
	return nil
}
