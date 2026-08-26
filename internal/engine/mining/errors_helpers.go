package mining

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// miningDepositInvalid constructs an ErrDepositDataInvalid error with full
// context for the deposit-parameters data file (data/deposits.json).
// It supplies the {cause} token the MET-E950 template requires: "deposit-parameters
// data file could not be loaded or failed schema validation: {cause}".
func miningDepositInvalid(correlationID, path, field, rule string) error {
	// The cause describes what field failed validation and why.
	cause := fmt.Sprintf("%s: %s", field, rule)
	return errs.New(ErrDepositDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}

// miningDepositQueryInvalid constructs an ErrDepositQueryOutOfBounds error
// for deposit queries (tile/cell coordinates). The MET-E952 template expects
// {cause}: "deposit query rejected: {cause}".
func miningDepositQueryInvalid(correlationID, cause string) error {
	return errs.New(ErrDepositQueryOutOfBounds, correlationID, map[string]any{
		"cause": cause,
	})
}

// miningMineTypeInvalid constructs an ErrMineTypeDataInvalid error for the
// mine-type catalogue (data/minetypes.json). The MET-E954 template expects
// {cause}: "mine-type catalogue data file could not be loaded or failed schema
// validation: {cause}".
func miningMineTypeInvalid(correlationID, field, rule string) error {
	cause := fmt.Sprintf("%s: %s", field, rule)
	return errs.New(ErrMineTypeDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}

// miningBlightDataInvalid constructs an ErrBlightDataInvalid error for the
// blight model (data/mining.json). The MET-E959 template expects {cause}:
// "blight-model data file could not be loaded or failed schema validation: {cause}".
func miningBlightDataInvalid(correlationID, field, rule string) error {
	cause := fmt.Sprintf("%s: %s", field, rule)
	return errs.New(ErrBlightDataInvalid, correlationID, map[string]any{
		"cause": cause,
	})
}

// miningBlightProfileInvalid constructs an ErrBlightProfileInvalid error for
// blighting-object registration validation. The MET-E957 template expects both
// {field} and {rule}: "blighting-object profile invalid: {field} {rule}".
func miningBlightProfileInvalid(correlationID, field, rule string) error {
	return errs.New(ErrBlightProfileInvalid, correlationID, map[string]any{
		"field": field,
		"rule":  rule,
	})
}

// miningSiteExhaustedInvalid constructs an ErrSiteExhausted error. The MET-E964
// template expects both {key} and {rule}: "site {key} exhausted or closed: {rule}".
func miningSiteExhaustedInvalid(correlationID, key, rule string) error {
	return errs.New(ErrSiteExhausted, correlationID, map[string]any{
		"key":  key,
		"rule": rule,
	})
}
