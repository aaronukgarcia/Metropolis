package defence

import (
	"errors"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// defenceDataInvalid constructs an ErrDefenceDataInvalid error for data/defence.json
// load/validation failures with full context. It extracts field/rule from a
// *data.FieldError if provided, and supplies the {field}, {rule}, {cause} tokens
// required by the MET-G3800 template: "data/defence.json could not be loaded or
// failed schema validation (field={field}, rule={rule}, cause={cause})".
func defenceDataInvalid(correlationID string, err error) error {
	var fe *data.FieldError
	if errors.As(err, &fe) {
		return errs.Wrap(ErrDefenceDataInvalid, correlationID, err, map[string]any{
			"field": fe.Field,
			"rule":  fe.Rule,
			"cause": fe.Error(),
		})
	}
	// Fallback for non-FieldError cases: extract reasonable field/rule from
	// the error message or use generic descriptions.
	return errs.Wrap(ErrDefenceDataInvalid, correlationID, err, map[string]any{
		"field": "unknown",
		"rule":  "validation failed",
		"cause": err.Error(),
	})
}

// ineligibleSiteError constructs an ErrIneligibleSite error for a facility-siting
// command that names an ineligible cell. It supplies the {site} and {cause} tokens
// required by the MET-G3803 template: "facility-siting command named an ineligible
// cell {site} (cause={cause})".
func ineligibleSiteError(correlationID, site, cause string) error {
	return errs.New(ErrIneligibleSite, correlationID, map[string]any{
		"site":  site,
		"cause": cause,
	})
}

// missingFacilityTypeError constructs an ErrDefenceDataInvalid error for a
// facility type that is referenced by a mandate/choice but missing from the
// facilities table. It supplies the full {field}, {rule}, {cause} context.
func missingFacilityTypeError(correlationID, facilityType string) error {
	cause := fmt.Sprintf("facility type %q missing from facilities table", facilityType)
	return errs.New(ErrDefenceDataInvalid, correlationID, map[string]any{
		"field": "facilities",
		"rule":  fmt.Sprintf("must include facility type %q referenced by mandate/choice", facilityType),
		"cause": cause,
	})
}
