package traffic

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// invalidInput constructs an ErrInvalidInput error with full context describing
// what input validation failed. It supplies the {context} token required by the
// MET-G4502 template: "invalid traffic input ({context})".
func invalidInput(correlationID, context string) error {
	return errs.New(ErrInvalidInput, correlationID, map[string]any{
		"context": context,
	})
}

// invalidInputf is like invalidInput but formats the context string.
func invalidInputf(correlationID, format string, args ...any) error {
	return invalidInput(correlationID, fmt.Sprintf(format, args...))
}
