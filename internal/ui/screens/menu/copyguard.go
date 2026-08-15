package menu

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied: a *Screen method was called on a struct copy of the
// value New returned (SEC-020 wave — see docs/planning/dev-team-process.md's
// Weakness pattern #1/#3). checkNotCopied (below) rejects every such call
// before it does anything else. Registered in this package's own U600-U699
// range (data/errors.json) rather than reusing ui.screen.debug's MET-U203 /
// ui.screen.map's MET-U101 / ui.screen.demo's MET-U503 — each guarded type
// in its own package claims its own code (GR#7), same as those siblings do
// for each other.
const ErrScreenCopied = "MET-U600"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring MapScreen.checkNotCopied (internal/ui/
// screens/map/copyguard.go), debug.Screen.checkNotCopied (internal/ui/
// screens/debug/copyguard.go), demo.Screen.checkNotCopied (internal/ui/
// screens/demo/copyguard.go), and Store.checkNotCopied (internal/
// foundation/data/reload.go) — same mechanism, same ordering rationale,
// restated here because each guarded type owns its own check (GR#3 does
// not apply across package boundaries with no shared import path).
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not s.mu — so it is safe and correct to call BEFORE s.mu
// is ever touched.
//
// Why this matters for Screen specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while subs/stale/saveEntries
// (maps/slices) are reference types a copy ALIASES. An unrejected copy is
// therefore a second lock domain that can read/mutate the SAME backing
// state as the original under the mistaken belief that holding its own mu
// is exclusive access — exactly SEC-020's "two locks, one referent"
// shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held, and acquiring a copy's own mu in that state can
// block forever. Rejecting the copy here, before Lock() is ever called,
// means that hang path is never reached at all.
//
// A nil s.self.Load() (a Screen constructed as a bare `Screen{}` or
// `new(Screen)` rather than via New) is treated the same as a mismatch.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
