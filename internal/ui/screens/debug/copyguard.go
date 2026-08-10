package debug

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied: a *Screen method was called on a struct copy of the
// value NewScreen returned (SEC-020 wave 3 — see docs/planning/dev-team-
// process.md's Weakness pattern #1/#3). checkNotCopied (below) rejects
// every such call before it does anything else. Screen has no codes.go
// in this package (unlike engine.core/engine.debug/protocol), so this
// constant lives here, next to the one thing that produces it.
const ErrScreenCopied = "MET-U203"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring Engine.checkNotCopied (internal/engine/
// core/engine.go), InProcTransport.checkNotCopied (internal/protocol/
// transport.go), and State.checkNotCopied (internal/engine/debug/
// copyguard.go) — same mechanism, same ordering rationale, restated here
// because each guarded type owns its own check (GR#3 does not apply
// across package boundaries with no shared import path — see phase.go's
// "literal local copy, not an import" note for the same GR#20 shape).
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not s.mu — so it is safe and correct to call BEFORE s.mu
// is ever touched.
//
// Why this matters for Screen specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while reg (*registry.Registry)
// and events ([]string) are reference types a copy ALIASES. An
// unrejected copy is therefore a second lock domain that can read/
// mutate the SAME aliased registry pointer and the SAME events backing
// array (RequestToggle's appendCapped reslices/overwrites it) as the
// original — exactly SEC-020's "two locks, one referent" shape, and
// exactly the class InProcTransport/State/Engine were already fixed for.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`s2 := *s` while another goroutine has
// s.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's
// own mu in that state can block forever, since nothing will ever
// Unlock() that specific copy's address. A guard placed AFTER the lock
// can never run for that attack, because the attack IS acquiring the
// lock; rejecting the copy here, before Lock() is ever called, means
// that hang path is never reached at all.
//
// A nil s.self.Load() (a Screen constructed as a bare `Screen{}` or
// `new(Screen)` rather than via NewScreen, so self was never stored) is
// treated the same as a mismatch and rejected the same way — every
// documented construction path is NewScreen, so an unset self is itself
// a misuse this same error correctly names.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
