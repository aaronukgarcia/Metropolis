package replay

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fixtureCorruptError constructs a codeFixtureCorrupt error for fixture
// corruption cases. The MET-H002 template expects {path} and {cause}:
// "fixture at {path} is corrupt: {cause}".
func fixtureCorruptError(correlationID, path, cause string) error {
	return errs.New(codeFixtureCorrupt, correlationID, map[string]any{
		"path":  path,
		"cause": cause,
	})
}

// fixtureLoadFailedError constructs a codeFixtureLoadFailed error. The
// MET-H003 template expects {path} and {cause}: "fixture at {path} could
// not be read: {cause}".
func fixtureLoadFailedError(correlationID, path, cause string) error {
	return errs.New(codeFixtureLoadFailed, correlationID, map[string]any{
		"path":  path,
		"cause": cause,
	})
}
