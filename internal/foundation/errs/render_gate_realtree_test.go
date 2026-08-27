package errs

// BUG-357 render gate (real-tree ratchet).
//
// real_callsite_gate_test.go proves the scanTree machinery on fixtures. This
// file points that same machinery at the REAL repo tree and asserts that the
// packages already reconciled for BUG-357 carry ZERO literal-token survivors —
// a call site whose registry template names a {token} the site does not supply,
// which renders the literal "{token}" to a user (renderTemplate leaves an
// unresolved key as its literal placeholder).
//
// It is a real go/ast call-site walk, not a hand-written ctx table: a key
// rename at a call site is caught, exactly the failure the round found the
// ui.keys hand-table would miss. As more packages are swept clean under
// BUG-357, add them to renderGateFixedPackages and this ratchet locks them.
//
// Proven able to fail: drop any supplied ctx key from a fixed package's real
// call site and this test goes RED (evidence recorded on the BUG item).

import (
	"path/filepath"
	"strings"
	"testing"
)

// renderGateFixedPackages are repo-relative package directories that have been
// reconciled to their registry templates and must stay free of literal-token
// survivors. Grow this list as BUG-357 module sweeps land.
var renderGateFixedPackages = []string{
	filepath.Join("internal", "engine", "coastal"),
	filepath.Join("internal", "engine", "accelerator"),
	filepath.Join("internal", "engine", "social"),
	filepath.Join("internal", "engine", "news"),
	filepath.Join("internal", "engine", "mining"),
	filepath.Join("internal", "engine", "education"),
	filepath.Join("internal", "harness", "replay"),
	filepath.Join("internal", "engine", "spiral"),
	filepath.Join("internal", "engine", "chemicals"),
	filepath.Join("internal", "engine", "fuel"),
	filepath.Join("internal", "engine", "prison"),
	filepath.Join("internal", "harness", "metricsdash"),
	filepath.Join("internal", "engine", "compose"),
	filepath.Join("internal", "engine", "traffic"),
	filepath.Join("internal", "engine", "defence"),
	filepath.Join("internal", "engine", "freight"),
	filepath.Join("internal", "engine", "refuse"),
	filepath.Join("internal", "engine", "roads"),
}

func TestRenderGate_FixedPackagesHaveNoLiteralTokens(t *testing.T) {
	regPath, err := resolveRegistryPath()
	if err != nil {
		t.Fatalf("resolve registry path: %v", err)
	}
	entries, err := loadRegistry()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	// repo root = the directory that contains data/errors.json.
	root := filepath.Dir(filepath.Dir(regPath))

	for _, pkg := range renderGateFixedPackages {
		pkgDir := filepath.Join(root, pkg)
		findings := scanTree(pkgDir, entries)
		for _, f := range findings {
			if f.Kind != "survivor" {
				continue // dynamic-ctx / dynamic-code are a separate class, not a literal render
			}
			rel, _ := filepath.Rel(root, f.File)
			rel = filepath.ToSlash(rel)
			t.Errorf("literal-token render survives in a BUG-357 fixed package: %s:%d %s renders literal {%s}",
				rel, f.Line, f.Code, f.Token)
		}
		if !strings.HasPrefix(pkg, "internal") {
			t.Fatalf("fixed package %q must be a repo-relative internal path", pkg)
		}
	}
}
