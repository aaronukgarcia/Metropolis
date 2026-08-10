package uitest

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// updateGolden is AC-6's "-update" mode: pass -update to a `go test`
// invocation against this package (or a package importing it) to
// (re)write golden snapshots from the current captured output instead of
// comparing against them. Deliberately a separate, explicit flag rather
// than any environment-driven or implicit toggle, so goldens can never
// drift silently (AC-6).
var updateGolden = flag.Bool("update", false, "uitest: regenerate golden snapshots from current output instead of comparing against them")

// snapshotDir is where every golden lives, relative to the importing
// test's working directory (Go runs package tests with that package's
// directory as cwd) — testdata/ is the Go-idiomatic, go-tool-ignored
// location (AC-5).
const snapshotDir = "testdata/snapshots"

// snapshotPath resolves name (AC-5b: MUST be t.Name(), see doc.go's
// "Snapshot names are path segments, not labels" section) into a file
// path under snapshotDir. t.Name() uses "/" as its own subtest-hierarchy
// separator, which this function preserves as nested directories — but
// each resulting segment is validated via [serialize.ValidateShardName]
// (the same function harness.replay's fixture names and
// serialize.ShardMeta.Name are checked with) before being joined, and
// the final resolved path is confirmed to still fall under snapshotDir
// as defence in depth. A hostile segment (e.g. "..") is rejected outright
// (MET-H103) — never filepath.Clean'd or substituted with a fallback.
func snapshotPath(name string) (string, error) {
	segments := strings.Split(name, "/")
	for _, seg := range segments {
		if err := serialize.ValidateShardName(seg); err != nil {
			return "", errs.Wrap(codeHostileSnapshotName, errs.NewCorrelationID(), err, map[string]any{
				"name": name, "segment": seg,
			})
		}
	}
	relParts := append(append([]string{}, segments[:len(segments)-1]...), segments[len(segments)-1]+".golden")
	full := filepath.Join(append([]string{snapshotDir}, relParts...)...)

	absDir, err := filepath.Abs(snapshotDir)
	if err != nil {
		return "", errs.Wrap(codeHostileSnapshotName, errs.NewCorrelationID(), err, map[string]any{"name": name})
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", errs.Wrap(codeHostileSnapshotName, errs.NewCorrelationID(), err, map[string]any{"name": name})
	}
	if absFull != absDir && !strings.HasPrefix(absFull, absDir+string(filepath.Separator)) {
		return "", errs.New(codeHostileSnapshotName, errs.NewCorrelationID(), map[string]any{
			"name": name, "resolved": absFull, "cause": "resolved outside testdata/snapshots",
		})
	}
	return full, nil
}

// loadOrUpdateSnapshot is AssertSnapshot's pure comparison step, split
// out so tests can exercise AC-8's distinct missing-golden condition
// directly against a returned error, rather than scraping a *testing.T's
// failure log. With update true, it (re)writes path from got and always
// reports matched=true (AC-6). Otherwise it loads path and reports
// whether got equals the stored golden; a missing file is reported as a
// registry-sourced MET-H102 error (AC-8), distinct from matched=false (a
// content mismatch is the ordinary, expected outcome of a comparison
// that found a difference — not itself an "error").
func loadOrUpdateSnapshot(path, got string, update bool) (matched bool, want string, err error) {
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, "", err
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			return false, "", err
		}
		return true, got, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", errs.Wrap(codeMissingGolden, errs.NewCorrelationID(), err, map[string]any{"path": path})
		}
		return false, "", err
	}
	want = string(raw)
	return got == want, want, nil
}

// AssertSnapshot compares got against the golden file named by
// t.Name() (AC-5b). With -update (AC-6), the golden is (re)written from
// got instead of compared — reviewable as an ordinary file diff before
// commit, never a silent drift. A missing golden without -update fails
// with a distinct, registry-sourced "no golden — run with -update" error
// (MET-H102, AC-8), never confused with a content-mismatch failure.
func AssertSnapshot(t *testing.T, got string) {
	t.Helper()

	path, err := snapshotPath(t.Name())
	if err != nil {
		t.Fatalf("uitest: %v", err)
	}

	matched, want, err := loadOrUpdateSnapshot(path, got, *updateGolden)
	if err != nil {
		t.Fatalf("uitest: %v", err)
	}
	if !matched {
		t.Fatalf(
			"uitest: snapshot mismatch for %s\n(golden: %s — re-run with -update if this change is intentional)\n--- want ---\n%s--- got ---\n%s",
			t.Name(), path, want, got,
		)
	}
}
