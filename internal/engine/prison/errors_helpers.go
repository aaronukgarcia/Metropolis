package prison

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// invalidAdmission constructs an ErrInvalidAdmission error for admission
// validation failures. The MET-G4306 template expects {field}:
// "invalid admission: field {field}".
func invalidAdmission(correlationID, field string) error {
	return errs.New(ErrInvalidAdmission, correlationID, map[string]any{
		"field": field,
	})
}

// invalidRegimeFunding constructs an ErrInvalidRegimeFunding error.
// The MET-G4304 template expects {line} and {amount}:
// "invalid regime funding: line {line}, amount {amount}".
func invalidRegimeFunding(correlationID string, line RegimeLine, amount int64) error {
	return errs.New(ErrInvalidRegimeFunding, correlationID, map[string]any{
		"line":   string(line),
		"amount": amount,
	})
}

// invalidReentrySupport constructs an ErrInvalidReentrySupport error.
// The MET-G4308 template expects {kind} and {value}:
// "invalid re-entry support value for {kind}: {value}".
func invalidReentrySupport(correlationID string, kind ReentryKind, value float64) error {
	return errs.New(ErrInvalidReentrySupport, correlationID, map[string]any{
		"kind":  string(kind),
		"value": value,
	})
}

// slowFuseRejected constructs an ErrSlowFuseRejected error for rehab-spend
// pre-submission check failures. The MET-G4307 template expects {reason}:
// "rehab-spend rejected by the Slow-Fuse pre-submission check: {reason}".
func slowFuseRejected(correlationID, reason string) error {
	return errs.New(ErrSlowFuseRejected, correlationID, map[string]any{
		"reason": reason,
	})
}
