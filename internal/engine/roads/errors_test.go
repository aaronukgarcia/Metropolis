package roads

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestAllRegistryCodesResolve (GR#7/AC-12/AC-13) asserts every error code
// this package declares is actually registered in data/errors.json — a code
// that is not registered would be silently replaced by errs.New with
// MET-F003, which this test catches.
func TestAllRegistryCodesResolve(t *testing.T) {
	codes := []string{
		ErrRoadsDataInvalid,
		ErrCopiedValue,
		ErrRoadNotFound,
		ErrNodeNotFound,
		ErrInvalidClass,
		ErrIncompatibleUpgrade,
		ErrFootprintObstructed,
		ErrWorldNotWired,
		ErrInvalidRoadworks,
		ErrUnknownObjectKind,
		ErrSpeedLimitOutOfBounds,
		ErrMonthRegression,
		ErrInvalidInput,
		ErrCorpusLoadFailed,
	}
	for _, code := range codes {
		e := errs.New(code, "test-correlation", nil)
		if e.Code != code {
			t.Errorf("code %s not registered in data/errors.json: errs.New returned %s", code, e.Code)
		}
	}
}

// TestCorpusLoadFailureIsLoud (AC-13) asserts a malformed
// data/naming_corpus.json fails Load loudly through foundation.data's
// validated-load path (wrapped as ErrCorpusLoadFailed), never a silent empty
// name that could ship visible to the player.
func TestCorpusLoadFailureIsLoud(t *testing.T) {
	dir, err := data.ResolveDataDir("test")
	if err != nil {
		t.Fatal(err)
	}
	roadsJSON, err := os.ReadFile(filepath.Join(dir, "roads.json"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "roads.json"), roadsJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty categories object fails NamingCorpus.Validate (no place names).
	if err := os.WriteFile(filepath.Join(tmp, "naming_corpus.json"), []byte(`{"version":1,"categories":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tmp, 42, "test"); !errors.Is(err, &errs.E{Code: ErrCorpusLoadFailed}) {
		t.Fatalf("Load with malformed corpus = %v, want ErrCorpusLoadFailed", err)
	}
}
