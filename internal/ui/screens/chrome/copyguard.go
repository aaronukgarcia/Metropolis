package chrome

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// checkNotCopied reports whether the receiver is a struct copy of some
// other Chrome value, mirroring Engine.checkNotCopied (internal/engine/
// core/engine.go), mapscreen.MapScreen.checkNotCopied, and the other SEC-
// 020 copy guards — same mechanism, same ordering rationale.
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring nothing
// else, not c.mu — so it is safe and correct to call BEFORE c.mu is ever
// touched. Why it matters here specifically: mu is a sync.Mutex VALUE (a
// copy gets its own, independent lock) while alerts is a slice (reference
// type) a copy ALIASES. An unrejected copy is a second lock domain that
// could append to the SAME backing array as the original — exactly SEC-020's
// "two locks, one referent" shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held, and acquiring a copy's own mu in that state can
// block forever. Rejecting the copy here, before Lock() is ever called,
// means that hang path is never reached.
//
// A nil c.self.Load() (a Chrome constructed as a bare `Chrome{}` rather
// than via NewChrome, so self was never stored) is treated the same as a
// mismatch and rejected the same way — every documented construction path
// is NewChrome.
//
// The correlation ID is minted only on the failure path (unlike the older
// siblings, which mint one per call as an argument) — the check itself is a
// hot-path atomic load that must stay allocation-free, and the ID is only
// needed when the error is actually constructed.
func (c *Chrome) checkNotCopied(ctx map[string]any) error {
	if c.self.Load() != c {
		return errs.New(ErrChromeCopied, errs.NewCorrelationID(), ctx)
	}
	return nil
}
