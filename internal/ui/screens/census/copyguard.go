package census

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ErrScreenCopied (MET-V702) is declared here, next to checkNotCopied.
const ErrScreenCopied = "MET-V702"

// checkNotCopied reports whether the receiver is a struct copy of some
// other Screen value, mirroring services.Screen.checkNotCopied /
// finance.Screen.checkNotCopied exactly (SEC-020 family, AC-13).
func (s *Screen) checkNotCopied(correlationID string, ctx map[string]any) error {
	if s.self.Load() != s {
		return errs.New(ErrScreenCopied, correlationID, ctx)
	}
	return nil
}
