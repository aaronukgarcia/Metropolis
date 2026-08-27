package refuse

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// unknownStreamError constructs an ErrUnknownStream error for a stream
// validation failure. The MET-G1909 template expects {stream}:
// "refuse stream {stream} is not a registered waste stream".
func unknownStreamError(correlationID string, stream Stream) error {
	return errs.New(ErrUnknownStream, correlationID, map[string]any{
		"stream": string(stream),
	})
}

// invalidContaminationError constructs an ErrInvalidContamination error for
// a contamination level outside [0,1]. The MET-G1904 template expects {level}:
// "contamination level {level} is outside [0,1]".
func invalidContaminationError(correlationID string, level float64) error {
	return errs.New(ErrInvalidContamination, correlationID, map[string]any{
		"level": level,
	})
}

// invalidFundingError constructs an ErrInvalidFunding error for a funding
// level outside [0,1]. The MET-G1905 template expects {level}:
// "funding level {level} is outside [0,1]".
func invalidFundingError(correlationID string, level float64) error {
	return errs.New(ErrInvalidFunding, correlationID, map[string]any{
		"level": level,
	})
}

// copiedValueError constructs an ErrCopiedValue error for a method called on
// a struct-copied RefuseAPI. The MET-G1907 template expects {method}:
// "RefuseAPI method {method} called on a struct-copied value".
func copiedValueError(correlationID string, method string) error {
	return errs.New(ErrCopiedValue, correlationID, map[string]any{
		"method": method,
	})
}

// dependencyNotWiredError constructs an ErrDependencyNotWired error for a
// method call before dependencies are wired. The MET-G1908 template expects
// {method}: "dependency not wired for method {method}".
func dependencyNotWiredError(correlationID string, method string) error {
	return errs.New(ErrDependencyNotWired, correlationID, map[string]any{
		"method": method,
	})
}
