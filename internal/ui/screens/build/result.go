package build

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ApplyResult is this screen's router.ResultReceiver (AC-B4): the engine's
// verdict on a BuyLand/BuildOn/Demolish command. A rejection records the
// engine's own Display text so the screen can show WHY, never silence; an
// accept clears any earlier rejection.
func (s *Screen) ApplyResult(res protocol.CommandResult) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	if res.Accepted {
		s.rejectedReason = ""
		return
	}
	switch {
	case res.Error != nil && res.Error.Display != "":
		s.rejectedReason = res.Error.Display
	case res.Error != nil:
		s.rejectedReason = res.Error.Code
	default:
		s.rejectedReason = "command rejected"
	}
}

// RejectedReason returns the engine's text for the most recent rejected
// command, or "" when there is none to show.
func (s *Screen) RejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RejectedReason"}); err != nil {
		return ""
	}
	return s.rejectedReason
}
