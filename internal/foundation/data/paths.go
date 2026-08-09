package data

import (
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// dataDirEnv is the override environment variable, mirroring
// foundation.errors' METROPOLIS_ERRORS_PATH pattern (see
// errs/registry.go) but for the whole data/ directory rather than a
// single file.
const dataDirEnv = "METROPOLIS_DATA_DIR"

// relDataDir is the data directory location relative to the repo root.
const relDataDir = "data"

// dataDirMarker is a file that must exist directly inside a candidate
// data/ directory for findDirUpward to accept it. A bare directory
// named "data" is not a strong enough signal on its own — this
// package's own directory (internal/foundation/data) is itself named
// "data" and would otherwise be found as a false positive one level up
// from a `go test` run inside this package. Requiring one of the
// known §24 files to actually be present (mirroring how
// errs/registry.go searches for the file data/errors.json, not a bare
// directory) rules that out.
const dataDirMarker = FileConsumption

// ResolveDataDir finds the data/ directory using the documented
// resolution order:
//
//  1. $METROPOLIS_DATA_DIR, if set — used verbatim, no further search.
//  2. Walking upward from the running executable's directory, looking
//     for a data/ directory at each level.
//  3. Walking upward from the current working directory, looking for a
//     data/ directory at each level (this is what makes `go test` work
//     regardless of the per-package working directory Go gives it).
//
// correlationID is used only to construct the error on failure.
func ResolveDataDir(correlationID string) (string, error) {
	if p := os.Getenv(dataDirEnv); p != "" {
		return p, nil
	}

	if exe, err := os.Executable(); err == nil {
		if p, ok := findDirUpward(filepath.Dir(exe), relDataDir); ok {
			return p, nil
		}
	}

	if wd, err := os.Getwd(); err == nil {
		if p, ok := findDirUpward(wd, relDataDir); ok {
			return p, nil
		}
	}

	return "", errs.New(CodeDataDirNotFound, correlationID, map[string]any{
		"env": dataDirEnv,
	})
}

// findDirUpward looks for a directory named rel joined onto start,
// containing dataDirMarker, then each successive parent directory,
// until found or the filesystem root is reached.
func findDirUpward(start, rel string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(filepath.Join(candidate, dataDirMarker)); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
