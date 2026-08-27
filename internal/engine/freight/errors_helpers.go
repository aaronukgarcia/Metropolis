package freight

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file's helpers are deliberately FREE FUNCTIONS, not FreightAPI methods:
// astgate treats every method on a copy-guarded candidate type as a potential
// unguarded entry point, and pure error constructors need no receiver state
// beyond what their callers (already inside guarded FreightAPI paths) pass in.
// Mirrors negativeTonnageError/freightDataInvalidError below and the
// education/replay errors_helpers.go precedent.

// modalCapError constructs an ErrModalCapExceeded error for a movement whose
// tonnage exceeds the mode's documented cap or falls below the minimum. It
// supplies the {tonnes}, {mode}, {max} tokens required by the MET-G1006
// template: "movement tonnage {tonnes} exceeds the {mode} modal cap (max {max})".
func modalCapError(correlationID string, mode Mode, tonnes int64, maxTonnes int64) error {
	return errs.New(ErrModalCapExceeded, correlationID, map[string]any{
		"tonnes": tonnes,
		"mode":   string(mode),
		"max":    maxTonnes,
	})
}

// maxModalCapTonnes returns the largest MaxTonnesPerMovement across all
// configured modes. Taking the maximum is order-independent, so ranging over
// the map here is deterministic in effect (GR#21): the same config always
// yields the same value regardless of iteration order.
func maxModalCapTonnes(caps map[Mode]modalCap) int64 {
	maxTonnes := int64(0)
	for _, cap := range caps {
		if cap.MaxTonnesPerMovement > maxTonnes {
			maxTonnes = cap.MaxTonnesPerMovement
		}
	}
	return maxTonnes
}

// modalCapUnknownError constructs an ErrModalCapExceeded error for an unknown
// transport mode (one not present in the loaded config). The {max} token is
// filled with the largest configured per-movement cap so the message stays
// truthful ("no mode allows more than this") and deterministic.
func modalCapUnknownError(correlationID string, caps map[Mode]modalCap, mode Mode, tonnes int64) error {
	return modalCapError(correlationID, mode, tonnes, maxModalCapTonnes(caps))
}

// insufficientStockError constructs an ErrModalCapExceeded error (the code the
// existing call site already uses; a dedicated stock code is a registry
// decision, flagged on BUG-357) for a Ship whose source lacks sufficient
// stock. The template's {mode} token is filled with "unknown" because the
// mode was already validated before this branch and is not what failed.
func insufficientStockError(correlationID string, commodity Commodity, tonnes int64, maxTonnes int64) error {
	return errs.New(ErrModalCapExceeded, correlationID, map[string]any{
		"tonnes":    tonnes,
		"mode":      "unknown",
		"max":       maxTonnes,
		"commodity": string(commodity),
	})
}

// negativeTonnageError constructs an ErrNegativeTonnage error for a movement
// with negative or zero tonnage. It supplies the {tonnes}, {commodity} tokens
// required by the MET-G1007 template: "negative tonnage {tonnes} for commodity {commodity}".
func negativeTonnageError(correlationID string, commodity Commodity, tonnes int64) error {
	return errs.New(ErrNegativeTonnage, correlationID, map[string]any{
		"tonnes":    tonnes,
		"commodity": string(commodity),
	})
}

// freightDataInvalidError constructs an ErrFreightDataInvalid error for
// data/freight.json load/validation failures. It supplies the {cause} token
// required by the MET-G1000 template: "freight data invalid: {cause}".
func freightDataInvalidError(correlationID, field, rule string) error {
	cause := fmt.Sprintf("field %s: %s", field, rule)
	return errs.New(ErrFreightDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}
