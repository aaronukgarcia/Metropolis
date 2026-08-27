package roads

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// roadsDataInvalidLoad constructs an ErrRoadsDataInvalid error for
// data/roads.json load/validation failures. The MET-G3900 template expects
// {field}, {reason}, {cause}: "data/roads.json could not be loaded or failed
// schema validation (field={field}, reason={reason}, cause={cause})".
func roadsDataInvalidLoad(correlationID, path string, err error) error {
	return errs.Wrap(ErrRoadsDataInvalid, correlationID, err, map[string]any{
		"field":  "data/roads.json",
		"reason": fmt.Sprintf("load failed at %s", path),
		"cause":  err.Error(),
	})
}

// roadsDataInvalidParse constructs an ErrRoadsDataInvalid error for
// JSON parse failures. The MET-G3900 template expects {field}, {reason}, {cause}.
func roadsDataInvalidParse(correlationID, path string, err error) error {
	return errs.Wrap(ErrRoadsDataInvalid, correlationID, err, map[string]any{
		"field":  "data/roads.json",
		"reason": fmt.Sprintf("JSON unmarshal failed at %s", path),
		"cause":  err.Error(),
	})
}

// corpusLoadFailedError constructs an ErrCorpusLoadFailed error for
// data/naming_corpus.json load/validation failures. The MET-G3913 template
// expects {cause}: "data/naming_corpus.json could not be loaded or validated
// (cause={cause})".
func corpusLoadFailedError(correlationID string, err error) error {
	return errs.Wrap(ErrCorpusLoadFailed, correlationID, err, map[string]any{
		"cause": err.Error(),
	})
}

// invalidRoadworksError constructs an ErrInvalidRoadworks error for
// malformed schedule validation failures. The MET-G3908 template expects {reason}:
// "malformed roadworks schedule ({reason})".
func invalidRoadworksError(correlationID string, reason string) error {
	return errs.New(ErrInvalidRoadworks, correlationID, map[string]any{
		"reason": reason,
	})
}

// invalidRoadworksPhaseError constructs an ErrInvalidRoadworks error for
// phase validation failures, including phase-specific context.
func invalidRoadworksPhaseError(correlationID string, phase int, reason string) error {
	return errs.New(ErrInvalidRoadworks, correlationID, map[string]any{
		"reason": fmt.Sprintf("phase %d: %s", phase, reason),
	})
}

// invalidInputError constructs an ErrInvalidInput error for invalid input values.
// The MET-G3912 template expects {reason}: "invalid input: {reason}".
func invalidInputError(correlationID string, field string, reason string) error {
	return errs.New(ErrInvalidInput, correlationID, map[string]any{
		"reason": fmt.Sprintf("field %s: %s", field, reason),
	})
}
