package chemicals

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// refineryDataInvalid constructs an ErrRefineryDataInvalid error for
// schema validation failures in the refinery config. The MET-G2600 template
// expects {cause}: "data/refinery.json could not be loaded or failed schema validation: {cause}".
func refineryDataInvalid(correlationID, cause string) error {
	return errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}

// refineryDataInvalidForCommodity constructs an ErrRefineryDataInvalid error
// for missing or invalid commodity data. Provides a descriptive {cause}.
func refineryDataInvalidForCommodity(correlationID, commodity string, details ...string) error {
	cause := fmt.Sprintf("invalid commodity %q", commodity)
	if len(details) > 0 && details[0] != "" {
		cause += ": " + details[0]
	}
	return errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}

// refineryNegativeCrudeError constructs an ErrRefineryNegativeCrude error for
// Operate's boundary rejection of negative crude tonnes (AC-9). It supplies
// the {tonnes} token required by the MET-G2607 template: "refinery Operate
// rejected negative crude tonnes ({tonnes})".
func refineryNegativeCrudeError(correlationID string, tonnes int64) error {
	return errs.New(ErrRefineryNegativeCrude, correlationID, map[string]any{
		"tonnes": tonnes,
	})
}
