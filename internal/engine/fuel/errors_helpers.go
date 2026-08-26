package fuel

import (
	"errors"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fuelDataInvalid constructs an ErrFuelDataInvalid error with proper context
// extraction from a data loading error. The MET-G4200 template expects
// {field}, {reason}, {cause}: "data/fuel.json could not be loaded or failed
// engine.fuel's schema validation (field={field}, reason={reason}, cause={cause})".
func fuelDataInvalid(correlationID string, loadErr error) error {
	field := "unknown"
	reason := "unknown"

	// Extract field and reason from FieldError if present
	var fieldErr *data.FieldError
	if errors.As(loadErr, &fieldErr) {
		field = fieldErr.Field
		reason = fieldErr.Rule
	} else if loadErr != nil {
		reason = loadErr.Error()
	}

	return errs.Wrap(ErrFuelDataInvalid, correlationID, loadErr, map[string]any{
		"field":  field,
		"reason": reason,
		"cause":  loadErr.Error(),
	})
}

// fuelDomainInvalid constructs an ErrInvalidInput error for a query/setter
// input outside its documented domain. The MET-G4208 template expects {field}
// and {value}: "A setter/query input was outside its documented domain ({field}={value})".
func fuelDomainInvalid(correlationID, field string, value any) error {
	return errs.New(ErrInvalidInput, correlationID, map[string]any{
		"field": field,
		"value": fmt.Sprintf("%v", value),
	})
}
