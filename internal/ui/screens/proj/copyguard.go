package proj

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied: a *Screen method was called on a struct copy of the
// value New returned (SEC-020 wave 3 — see docs/planning/dev-team-
// process.md's Weakness pattern #1/#3). checkNotCopied (below) rejects
// every such call before it does anything else. Registered in this
// package's own V000-V099 range (data/errors.json) rather than reusing a
// sibling screen's code — each guarded type in its own package claims its
// own code (GR#7).
const ErrScreenCopied = "MET-V003"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring MapScreen.checkNotCopied (internal/ui/
// screens/map/copyguard.go), debug.Screen.checkNotCopied, and
// demo.Screen.checkNotCopied (internal/ui/screens/demo/copyguard.go) —
// same mechanism, same ordering rationale, restated here because each
// guarded type owns its own check.
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not s.mu — so it is safe and correct to call BEFORE s.mu
// is ever touched.
//
// Why this matters for Screen specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while subs is a map and curves/
// crossings are slices (reference types a copy ALIASES). An unrejected
// copy is therefore a second lock domain that can read/mutate the SAME
// backing maps/slices as the original under the mistaken belief that
// holding its own mu is exclusive access — exactly SEC-020's "two locks,
// one referent" shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held, so acquiring the copy's own mu can block
// forever. Rejecting the copy here, before Lock() is ever called, means
// that hang path is never reached.
//
// A nil s.self.Load() (a Screen constructed as a bare Screen{}/new(Screen)
// rather than via New) is treated the same as a mismatch and rejected the
// same way.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
