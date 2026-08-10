package synth

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestPhaseHookCountAssertionStillTrue is PhaseHookCountInHeadlessPath's
// doc-comment promise made mechanical (defence in depth, not full proof
// — see that function's doc comment for the exact limit of what this
// test can and cannot catch). It re-runs the same repo-wide
// RegisterPhaseHook call-site scan that comment describes on every `go
// test` invocation and fails loudly the moment a NEW real call site
// appears anywhere that was not already accounted for when
// PhaseHookCountInHeadlessPath was written — the specific, checkable
// half of "this constant will go stale silently" that a source scan
// CAN catch (a brand-new registration call site appearing), as opposed
// to the half it cannot (harness.headless.Run itself starting to
// register a hook using one of the ALREADY-known call-site files' own
// pattern — that requires touching internal/harness/headless or
// internal/engine/core, both out of this dispatch's file ownership, so
// a human wiring that change is squarely on the hook to update this
// list and PhaseHookCountInHeadlessPath's return value in the same
// commit).
func TestPhaseHookCountAssertionStillTrue(t *testing.T) {
	root := repoRoot(t)

	// Known-good call sites as of this test's writing (2026-08-10): the
	// interface's own declaration/internal use inside internal/engine/
	// core, and exactly one real external caller (internal/engine/
	// invariant/wire.go) which internal/harness/headless does NOT
	// import (verified separately: `grep -rn "invariant\." internal/
	// harness/headless/*.go` finds nothing). Any file NOT in this set
	// that starts calling RegisterPhaseHook fails this test.
	knownFiles := map[string]bool{
		filepath.Join("internal", "engine", "core", "commands.go"):  true,
		filepath.Join("internal", "engine", "core", "engine.go"):    true,
		filepath.Join("internal", "engine", "core", "errors.go"):    true,
		filepath.Join("internal", "engine", "core", "persist.go"):   true,
		filepath.Join("internal", "engine", "core", "phase.go"):     true,
		filepath.Join("internal", "engine", "core", "subscribe.go"): true,
		filepath.Join("internal", "engine", "invariant", "wire.go"): true,
	}

	// A real call/definition site, distinguished from a mere doc-comment
	// mention (e.g. "mirrors Engine.RegisterPhaseHook/seal's ordering")
	// by requiring an open paren directly after the identifier, exactly
	// like PhaseHookCountInHeadlessPath's own doc comment's grep does.
	callSite := regexp.MustCompile(`RegisterPhaseHook\(`)

	var found []string
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if callSite.Match(data) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = append(found, rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking repo for RegisterPhaseHook call sites: %v", walkErr)
	}

	for _, rel := range found {
		if !knownFiles[rel] {
			t.Errorf("new RegisterPhaseHook(...) call site found at %q, not in this test's known-file list — "+
				"if internal/harness/headless/run.go's engine construction now registers a real hook, "+
				"PhaseHookCountInHeadlessPath (phasehooks.go) must be updated in the SAME change, "+
				"not left asserting a stale 0", rel)
		}
	}
	if len(found) == 0 {
		t.Fatal("scan found zero RegisterPhaseHook call sites at all — the scan itself is broken " +
			"(internal/engine/core's own definition/use should always match), not a sign hooks were removed")
	}
}

// repoRoot walks up from this test file's own source location to the
// directory containing go.mod — resilient to `go test` being invoked
// from any working directory (CI, a subpackage, etc.), unlike relying
// on os.Getwd().
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked up to filesystem root without finding go.mod")
		}
		dir = parent
	}
}
