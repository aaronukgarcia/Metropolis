package demo

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied: a *Screen method was called on a struct copy of the
// value New returned (SEC-020 wave 3 — see docs/planning/dev-team-
// process.md's Weakness pattern #1/#3). checkNotCopied (below) rejects
// every such call before it does anything else. Registered in this
// package's own U500-U599 range (data/errors.json) rather than reusing
// ui.screen.debug's MET-U203/ui.screen.map's MET-U101 — each guarded
// type in its own package claims its own code, same as those two
// siblings do for each other (GR#7).
//
// Widowmaker's Destructive round (BOW FEAT-018, REJECT) reproduced this
// live: `s2 := *s` then two goroutines each correctly locking their OWN
// copy's mu while mutating applyHousing's typologies map crashed the
// process outright under go test -race with "fatal error: concurrent
// map read and map write" — not merely a flagged race, a hard crash.
const ErrScreenCopied = "MET-U503"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring MapScreen.checkNotCopied (internal/ui/
// screens/map/copyguard.go), debug.Screen.checkNotCopied (internal/ui/
// screens/debug/copyguard.go), and Store.checkNotCopied (internal/
// foundation/data/reload.go, BUG-125) — same mechanism, same ordering
// rationale, restated here because each guarded type owns its own check
// (GR#3 does not apply across package boundaries with no shared import
// path).
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not s.mu — so it is safe and correct to call BEFORE s.mu
// is ever touched.
//
// Why this matters for Screen specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while subs/stale/typologies (maps)
// and ageMonths/personality/hoursByActivity/leisureTaste/typologyOrder
// (slices) are reference types a copy ALIASES. An unrejected copy is
// therefore a second lock domain that can read/mutate the SAME backing
// maps/arrays as the original under the mistaken belief that holding its
// own mu is exclusive access — exactly SEC-020's "two locks, one
// referent" shape, and exactly what Widowmaker's applyHousing
// proof-of-concept exploited (concurrent map read/write, no race
// detector needed to see it — a hard crash).
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
// `new(Screen)` rather than via New, so self was never stored) is
// treated the same as a mismatch and rejected the same way — every
// documented construction path is New, so an unset self is itself a
// misuse this same error correctly names.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
