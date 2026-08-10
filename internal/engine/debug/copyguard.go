package debug

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// checkNotCopied reports whether the receiver is a struct copy of some
// other State value (SEC-020 wave 2, mirroring Engine.checkNotCopied /
// SubscriptionServer.checkNotCopied / InProcTransport.checkNotCopied —
// internal/engine/core/engine.go, internal/engine/core/subscribe.go,
// internal/protocol/transport.go). Deliberately lock-free — a single
// atomic.Pointer.Load, requiring nothing else, not s.mu — so it is safe
// and correct to call BEFORE s.mu is ever touched.
//
// That ordering is not optional (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`s2 := *s` while some other goroutine has
// s.mu.Lock()'d), and acquiring — even just attempting — a copy's own mu
// in that state can block forever, since nothing will ever Unlock() that
// specific copy's address. A guard placed AFTER the lock can never run
// for that attack, because the attack IS acquiring the lock; rejecting
// the copy here, before Lock()/RLock() is ever called, means that hang
// path is never reached at all.
//
// A nil s.self.Load() (a State constructed as a bare `State{}` or
// `new(State)` rather than via NewState, so self was never stored) is
// treated the same as a mismatch and rejected the same way — every
// documented construction path is NewState, so an unset self is itself a
// misuse this same error correctly names, and rejecting it here also
// means such a value's zero-value fields (nil header, nil now, etc.) are
// never reached either.
func (s *State) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrStateCopied, correlationID, ctx)
	}
	return nil
}
