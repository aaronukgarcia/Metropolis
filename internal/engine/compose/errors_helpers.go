package compose

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// moduleFailed constructs an ErrModuleFailed error for module construction
// or wiring failures. The MET-G801 template expects {module} and {cause}:
// "composition root: required module {module} failed: {cause}".
func moduleFailed(correlationID, module, cause string) error {
	return errs.New(ErrModuleFailed, correlationID, map[string]any{
		"module": module,
		"cause":  cause,
	})
}

// moduleFailedWrapped wraps a cause error with the {module} and {cause} context.
func moduleFailedWrapped(correlationID, module string, cause error) error {
	return errs.Wrap(ErrModuleFailed, correlationID, cause, map[string]any{
		"module": module,
		"cause":  cause.Error(),
	})
}

// moduleDataInvalid constructs a moduleFailed error for schema validation issues.
func moduleDataInvalid(correlationID, module, field string, value any) error {
	cause := fmt.Sprintf("invalid %s: %v", field, value)
	return moduleFailed(correlationID, module, cause)
}
