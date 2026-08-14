package consumption

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// realAPI loads a *UtilityAPI against the repository's own data/
// directory (via ResolveDataDir), for tests that check the actual
// spec-transcribed figures (AC-2/AC-3/AC-4/AC-20) rather than a synthetic
// fixture.
func realAPI(t *testing.T) *UtilityAPI {
	t.Helper()
	api, err := LoadDefault(testCorrelationID())
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return api
}

func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}
