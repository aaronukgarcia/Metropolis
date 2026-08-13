package metricsdash

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoTickOrEngineCoreImports is AC-13: this package reads BOW/
// perf-CI data and logs a defect note — none of that may touch tick
// state, world seed, or any deterministic-replay-relevant path. The
// only internal/engine import this package has is internal/engine/debug
// (feedback.go, reused for its FeedbackRecord SCHEMA TYPE only — no
// State, no tick-affecting method is ever called), which this test
// explicitly allows; internal/engine/core (the tick engine itself) and
// internal/protocol (the command/tick transport) must never appear in
// any non-test .go file in this package.
func TestNoTickOrEngineCoreImports(t *testing.T) {
	bannedPrefixes := []string{
		"github.com/aaronukgarcia/Metropolis/internal/engine/core",
		"github.com/aaronukgarcia/Metropolis/internal/protocol",
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("glob found zero .go files -- the test itself is broken, not that the package is empty")
	}

	var allImports []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("reading %s: %v", f, rerr)
		}
		file, perr := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parsing %s: %v", f, perr)
		}
		for _, imp := range file.Imports {
			path, uqerr := strconv.Unquote(imp.Path.Value)
			if uqerr != nil {
				continue
			}
			allImports = append(allImports, path)
			for _, banned := range bannedPrefixes {
				if path == banned {
					t.Errorf("%s imports %q, which is banned for feat.metricsdash by AC-13 (no tick-path/engine.core coupling)", f, path)
				}
			}
		}
	}

	// Negative control: prove the import scan actually found real
	// imports (a scanner that silently finds nothing would make the
	// assertions above decoration, not a check).
	foundSynth := false
	for _, path := range allImports {
		if path == "github.com/aaronukgarcia/Metropolis/internal/harness/synth" {
			foundSynth = true
		}
	}
	if !foundSynth {
		t.Fatal("expected to find the known internal/harness/synth import (perf.go) -- the import scanner is broken, not that this package stopped importing synth")
	}
}

// TestLogNote_NeverReadsWallClockDirectlyWithoutInjectedNow documents
// (AC-13's spirit applied to feedback.go specifically) that LogNote's
// timestamp comes from its injected `now` parameter when one is
// supplied -- proven by TestLogNote_WritesRealFeedbackRecord
// (feedback_test.go) asserting an exact injected timestamp round-trips
// unchanged. This test only proves LogNote is NOT on any tick-driving
// call path: it can be invoked with no engine/State/session object at
// all.
func TestLogNote_NeverRequiresAnEngineSession(t *testing.T) {
	dir := t.TempDir()
	// No debug.State, no engine.core.Engine, no protocol transport --
	// just a directory and a string. If this compiles and runs, the
	// logging affordance has no structural dependency on a live tick
	// loop.
	if err := LogNote(dir, NoteBug, "no engine session needed", "test", nil); err != nil {
		t.Fatalf("LogNote should not require any engine/session wiring: %v", err)
	}
}
