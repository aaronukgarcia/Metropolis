package core

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrRenderLoopCopied: a *RenderLoop method was called on a struct copy
// of the value NewRenderLoop returned (BUG-018). checkNotCopied (below)
// rejects every such call before it does anything else. Registered in
// this package's own U000-U099 range (data/errors.json) as MET-U003.
//
// Found by the SEC-019 junior during the SEC-020 class enumeration and
// correctly kept as its own fix rather than folded into that class: this
// is a different mechanism from SEC-020's lock/aliased-referent shape
// (RenderLoop has no mutex at all). owned is an atomic.Bool VALUE — a
// struct copy gets its own, entirely independent atomic word, so a
// copy's CompareAndSwap(false, true) in renderOnce can succeed even
// while the original already owns the screen: two goroutines each
// correctly believing they hold T-RENDER's exclusive ownership, and each
// free to call methods on RenderScreen concurrently (two writers to one
// terminal, not unsafe memory access).
const ErrRenderLoopCopied = "MET-U003"

// checkNotCopied reports whether the receiver is a struct copy of some
// other RenderLoop value, mirroring Engine.checkNotCopied
// (internal/engine/core/engine.go, SEC-014/SEC-016/SEC-018),
// SubscriptionServer.checkNotCopied (internal/protocol/subscription.go,
// SEC-019), and Screen.checkNotCopied (internal/ui/screens/demo/
// copyguard.go) — same mechanism, same ordering rationale, restated here
// because each guarded type owns its own check (GR#3 does not apply
// across package boundaries with no shared import path).
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not r.owned — so it is safe and correct to call BEFORE
// r.owned.CompareAndSwap is ever attempted.
//
// Why this matters for RenderLoop specifically: owned is an atomic.Bool
// VALUE (a copy gets its own, independent atomic word, unlike a mutex
// which at least fails closed by hanging) — an unrejected copy is a
// second, entirely independent ownership domain that can CAS to "owned"
// at the exact same time the original holds it, with neither side ever
// seeing a conflict. That is exactly the two-writers-to-one-terminal
// failure this fix closes.
//
// A nil r.self.Load() (a RenderLoop constructed as a bare
// `RenderLoop{}` or `new(RenderLoop)` rather than via NewRenderLoop, so
// self was never stored) is treated the same as a mismatch and rejected
// the same way — every documented construction path is NewRenderLoop, so
// an unset self is itself a misuse this same error correctly names.
func (r *RenderLoop) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(ErrRenderLoopCopied, correlationID, ctx)
	}
	return nil
}
