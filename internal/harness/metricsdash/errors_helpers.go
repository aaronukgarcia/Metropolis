package metricsdash

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// feedbackWriteFailed constructs an ErrFeedbackWriteFailed error for feedback
// inbox write failures. The MET-H404 template expects {path}:
// "failed to write the logged note to the feedback inbox: {path}".
func feedbackWriteFailed(correlationID, path string, cause error) error {
	return errs.Wrap(codeFeedbackWriteFailed, correlationID, cause, map[string]any{
		"path": path,
	})
}

// gateStatusFailed constructs a codeGateStatusSourceUnavailable error for gate
// status query failures. The MET-H402 template expects {sprint} and {reason}:
// "could not obtain sprint {sprint}'s gate status: {reason}".
func gateStatusFailed(correlationID, sprint, reason string) error {
	return errs.New(codeGateStatusSourceUnavailable, correlationID, map[string]any{
		"sprint": sprint,
		"reason": reason,
	})
}

// lintReportFailed constructs a codeLintSourceUnavailable error for BOW lint
// report query failures. The MET-H401 template expects {reason}:
// "could not obtain the BOW lint drift report: {reason}".
func lintReportFailed(correlationID, reason string) error {
	return errs.New(codeLintSourceUnavailable, correlationID, map[string]any{
		"reason": reason,
	})
}

// weaknessDataFailed constructs a codeWeaknessSourceUnavailable error for
// weakness histogram query failures. The MET-H400 template expects {reason}:
// "could not obtain the weakness-histogram source data: {reason}".
func weaknessDataFailed(correlationID, reason string) error {
	return errs.New(codeWeaknessSourceUnavailable, correlationID, map[string]any{
		"reason": reason,
	})
}
