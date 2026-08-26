package spiral

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// spiralConfigInvalid constructs an ErrSpiralConfigInvalid error for
// schema validation failures in the embedded spiral.json config. The MET-G1106
// template expects {detail}: "embedded spiral.json config invalid: {detail}".
func spiralConfigInvalid(correlationID, file, field string) error {
	detail := fmt.Sprintf("invalid %s: %s", file, field)
	return errs.New(ErrSpiralConfigInvalid, correlationID, map[string]any{
		"detail": detail,
	})
}

// spiralConfigInvalidWrapped wraps a cause with the {detail} context.
// Used when JSON unmarshaling fails on spiral.json.
func spiralConfigInvalidWrapped(correlationID, file string, cause error) error {
	detail := fmt.Sprintf("invalid %s: %s", file, cause.Error())
	return errs.Wrap(ErrSpiralConfigInvalid, correlationID, cause, map[string]any{
		"detail": detail,
	})
}

// invalidScenario constructs an ErrInvalidScenario error for validation failures
// in scripted-scenario definitions. The MET-G1100 template expects {field} and
// {got}: "invalid scripted-scenario definition: field {field} ({got})".
func invalidScenario(correlationID, field, got string) error {
	return errs.New(ErrInvalidScenario, correlationID, map[string]any{
		"field": field,
		"got":   got,
	})
}
