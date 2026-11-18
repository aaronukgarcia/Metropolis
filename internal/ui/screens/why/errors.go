package why

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// Registry codes (data/errors.json, range V980-V989 owned by ui.screen.why).
const (
	// ErrMalformedPatch is a refused f5.why patch (bad JSON, wrong schema,
	// oversized, or a contract break); the last good frame stays.
	ErrMalformedPatch = "MET-V980"
	// ErrStaleSubscription is a delta for a subscription this screen never bound.
	ErrStaleSubscription = "MET-V981"
	// ErrScreenCopied is a method call on a struct copy of a Screen.
	ErrScreenCopied = "MET-V982"
)

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring finance.Screen.checkNotCopied exactly.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
