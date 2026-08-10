package mapscreen

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrMapScreenCopied: a *MapScreen method was called on a struct copy of
// the value NewMapScreen returned (SEC-020 wave 3 — see docs/planning/
// dev-team-process.md's Weakness pattern #1/#3). checkNotCopied (below)
// rejects every such call before it does anything else. Like ui.screen
// .debug, this package has no codes.go of its own (errors.go holds only
// local decode errors, not registry codes), so this constant lives here,
// next to the one thing that produces it.
const ErrMapScreenCopied = "MET-U101"

// checkNotCopied reports whether the receiver is a struct copy of some
// other MapScreen value, mirroring Engine.checkNotCopied (internal/
// engine/core/engine.go), InProcTransport.checkNotCopied (internal/
// protocol/transport.go), State.checkNotCopied (internal/engine/debug/
// copyguard.go), and debug.Screen.checkNotCopied (internal/ui/screens/
// debug/copyguard.go) — same mechanism, same ordering rationale.
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not m.mu — so it is safe and correct to call BEFORE m.mu
// is ever touched.
//
// Why this matters for MapScreen specifically: mu is a sync.Mutex VALUE
// (a copy gets its own, independent lock) while grid ([]cellData) is a
// reference type a copy ALIASES. An unrejected copy is therefore a
// second lock domain that can read/mutate the SAME backing array as the
// original — applyFullLocked/applySparseLocked/snapshotLocked all touch
// grid under the assumption that holding mu is exclusive access to it,
// an assumption a copy's independent mu silently breaks. Exactly SEC-
// 020's "two locks, one referent" shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`m2 := *m` while another goroutine has
// m.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's
// own mu in that state can block forever, since nothing will ever
// Unlock() that specific copy's address. A guard placed AFTER the lock
// can never run for that attack, because the attack IS acquiring the
// lock; rejecting the copy here, before Lock() is ever called, means
// that hang path is never reached at all.
//
// A nil m.self.Load() (a MapScreen constructed as a bare `MapScreen{}`
// or `new(MapScreen)` rather than via NewMapScreen, so self was never
// stored) is treated the same as a mismatch and rejected the same way —
// every documented construction path is NewMapScreen, so an unset self
// is itself a misuse this same error correctly names.
func (m *MapScreen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if m.self.Load() != m {
		return errs.New(ErrMapScreenCopied, correlationID, ctx)
	}
	return nil
}
