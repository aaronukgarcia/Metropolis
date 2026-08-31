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

// Note: codeFixtureLoadFailed (MET-H003) has no dedicated constructor here.
// Every real call site (fixture.go) reaches it via errs.Wrap with an actual
// underlying I/O error, not a string cause, so a fixtureLoadFailedError(id,
// path, cause string) helper never matched an actual call shape and was
// dead code (golangci `unused`, BUG-456). See fixtureCorruptError above for
// the constructor pattern that IS wired in (fixture.go/record.go call it).
