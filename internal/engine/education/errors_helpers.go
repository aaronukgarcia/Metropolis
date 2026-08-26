package education

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// educationDataInvalid constructs an ErrEducationDataInvalid error for
// schema validation failures in the education config. The MET-G1800 template
// expects {dir} and {cause}: "data/education.json could not be loaded or
// failed schema validation (dir {dir}): {cause}".
func educationDataInvalid(correlationID, dir, field, rule string, value any) error {
	// The cause describes what field failed validation and why.
	cause := fmt.Sprintf("%s: %s (got %v)", field, rule, value)
	return errs.New(ErrEducationDataInvalid, correlationID, map[string]any{
		"dir":   dir,
		"cause": cause,
	})
}
