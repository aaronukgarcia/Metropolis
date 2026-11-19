package compose

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1243 shared test helpers.

// bug1243SetBuilt sets both migration-modifier flags (safety, environment) of
// an already wired city, overriding whatever data/attract_terms.json said.
// The shipped file is today's behaviour (both built), so this is the quick way
// to force "built" as the reference; the data-variant helpers below select an
// option the way a data edit would.
func bug1243SetBuilt(c *Composition, built bool) {
	c.state.attractTerms.Built.Modifiers.Safety = &built
	c.state.attractTerms.Built.Modifiers.Environment = &built
}

// bug1243Neutral is the data edit for option A: both modifiers not built.
func bug1243Neutral(doc map[string]any) {
	m := bug1243Sub(bug1243Sub(doc, "built"), "modifiers")
	m["safety"], m["environment"] = false, false
}

// bug1243Floor is the data edit for option C: option A's flags plus
// floorAtBaseline for both modifiers.
func bug1243Floor(doc map[string]any) {
	bug1243Neutral(doc)
	w := bug1243Sub(bug1243Sub(doc, "built"), "whenUnbuilt")
	w["safety"], w["environment"] = "floorAtBaseline", "floorAtBaseline"
}

// bug1243Baseline returns the data edit for option B: option A plus a lower
// outside-world baseline.
func bug1243Baseline(v float64) func(doc map[string]any) {
	return func(doc map[string]any) {
		bug1243Neutral(doc)
		bug1243Sub(doc, "world")["aWorld"] = v
	}
}

// bug1243ParksFloor is the data edit for the parks option (shipped off).
func bug1243ParksFloor(doc map[string]any) { bug1243Sub(doc, "leisure")["floorAtBaseline"] = true }

// bug1243Sub returns doc[key] as an object, creating it when absent.
func bug1243Sub(doc map[string]any, key string) map[string]any {
	m, ok := doc[key].(map[string]any)
	if !ok {
		m = map[string]any{}
		doc[key] = m
	}
	return m
}

// bug1243DataVariant points METROPOLIS_DATA_DIR at a copy of data/ whose
// attract_terms.json has been edited by edit, for the life of the test.
func bug1243DataVariant(t *testing.T, edit func(doc map[string]any)) {
	t.Helper()
	bug1243RawVariant(t, func(b []byte) []byte {
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("shipped attract_terms.json: %v", err)
		}
		edit(doc)
		nb, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return nb
	})
}

// bug1243RawVariant is bug1243DataVariant for edits that need raw bytes (an
// empty file, a value that is not valid in a Go map).
func bug1243RawVariant(t *testing.T, edit func(b []byte) []byte) {
	t.Helper()
	src, err := data.ResolveDataDir(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	dst := t.TempDir()
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if rel == attractTermsFile {
			b = edit(b)
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy data dir: %v", err)
	}
	t.Setenv("METROPOLIS_DATA_DIR", dst)
}
