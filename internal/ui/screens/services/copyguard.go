package services

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied (MET-V502) is declared here, next to checkNotCopied.
const ErrScreenCopied = "MET-V502"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring finance.Screen.checkNotCopied /
// trade.Screen.checkNotCopied exactly.
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
