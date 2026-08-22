package router

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrRouterCopied: a *Router method was called on a struct copy of the
// value New returned (SEC-020 wave family — see
// docs/planning/dev-team-process.md's Weakness pattern #1/#3, and
// internal/protocol/transport.go's InProcTransport.self doc comment for
// the full identity-check rationale this mirrors). checkNotCopied (below)
// rejects every such call before it does anything else. Registered in
// this package's own V400-V499 range (data/errors.json) rather than
// reusing a sibling package's code — each guarded type in its own package
// claims its own code (GR#7). (This code was renumbered once, pre-commit
// -- see errors.go's NOTE on the V300-V399 collision with lane/ben's
// ui.screen.finance claim -- so its number does not match the sequence
// implied by ErrRouteMiss/ErrDeltaGap in errors.go; that is expected.)
const ErrRouterCopied = "MET-V402"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Router value, mirroring build.Screen.checkNotCopied,
// InProcTransport.checkNotCopied, and every other SEC-020-family guarded
// type — same mechanism, same ordering rationale, restated here because
// each guarded type owns its own check.
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not r.mu — so it is safe and correct to call BEFORE r.mu
// is ever touched.
//
// Why this matters for Router specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while pending/subs/eventRoutes are
// reference types (map, map, slice) a copy ALIASES. An unrejected copy is
// therefore a second lock domain that can read/mutate the SAME backing
// state as the original under the mistaken belief that holding its own mu
// is exclusive access — exactly SEC-020's "two locks, one referent" shape.
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held, so acquiring the copy's own mu can block
// forever. Rejecting the copy here, before Lock() is ever called, means
// that hang path is never reached.
//
// A nil r.self.Load() (a Router constructed as a bare Router{}/new(Router)
// rather than via New) is treated the same as a mismatch and rejected the
// same way.
func (r *Router) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(ErrRouterCopied, correlationID, ctx)
	}
	return nil
}
