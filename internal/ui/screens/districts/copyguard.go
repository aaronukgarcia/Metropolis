package districts

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied (MET-V602) is declared here, next to checkNotCopied.
const ErrScreenCopied = "MET-V602"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring services.Screen.checkNotCopied /
// finance.Screen.checkNotCopied / trade.Screen.checkNotCopied exactly.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
