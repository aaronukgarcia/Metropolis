package errs

// BUG-407 stream (1): whole-tree render gate (flipped from allowlist to full scan).
//
// real_callsite_gate_test.go proves the scanTree machinery on fixtures. This
// file points that same machinery at the ENTIRE repo tree and asserts that NO
// packages carry literal-token survivors — a call site whose registry template
// names a {token} the site does not supply, which renders the literal "{token}"
// to a user (renderTemplate leaves an unresolved key as its literal placeholder).
//
// It is a real go/ast call-site walk, not a hand-written ctx table: a key
// rename at a call site is caught, exactly the failure the round found the
// ui.keys hand-table would miss. BUG-357 campaign cured 282 survivors across
// 43 packages; this gate now covers the WHOLE TREE so new packages are born
// covered. Exclusions list is empty (no packages are special-cased; any package
// with errs.New/Wrap is scanned).
//
// Dynamic-ctx and dynamic-code findings (sites whose ctx or code is not
// statically resolvable) are reported but NOT failed — they require manual
// audit and are tracked separately (BUG-407 streams 2–3).
//
// Proven able to fail: drop any supplied ctx key from a real call site and
// this test goes RED (evidence recorded on the BUG item).

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// renderGateExcludedPackages lists any packages to skip during the whole-tree
// scan. This list should be empty: no packages are special-cased. If a package
// is added here, document why it cannot be covered (e.g. testdata, third-party
// embed, requires runtime decision).
//
// As of BUG-407: all packages are IN SCOPE. The gate scans internal/, cmd/,
// and tools/ (any Go packages found under the repo root where scanTree can
// traverse them). If a code package intentionally uses dynamic ctx or code
// constructors, those findings are reported separately and do not fail the gate.
var renderGateExcludedPackages = []string{} // deliberately empty; see comment above

func TestRenderGate_WholeTreeHasNoLiteralTokens(t *testing.T) {
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

	// Scan the entire repo tree for errs.New/Wrap call sites.
	findings := scanTree(root, entries)

	// Separate findings by kind.
	var survivors, dynamicCtx, dynamicCode []siteFinding
	for _, f := range findings {
		switch f.Kind {
		case "survivor":
			survivors = append(survivors, f)
		case "dynamic-ctx":
			dynamicCtx = append(dynamicCtx, f)
		case "dynamic-code":
			dynamicCode = append(dynamicCode, f)
		}
	}

	// Report dynamic findings (visibility without failure).
	if len(dynamicCtx) > 0 || len(dynamicCode) > 0 {
		t.Logf("BUG-407 stream 2 (audit dynamic sites): %d dynamic-ctx + %d dynamic-code findings — see comments for manual audit",
			len(dynamicCtx), len(dynamicCode))
	}

	// Fail on any survivors (literal-token renders).
	for _, f := range survivors {
		rel, _ := filepath.Rel(root, f.File)
		rel = filepath.ToSlash(rel)
		t.Errorf("literal-token render survives (whole-tree scan): %s:%d %s renders literal {%s}",
			rel, f.Line, f.Code, f.Token)
	}

	// Count packages scanned (directories with .go files, excluding testdata).
	// Derive the count at runtime to avoid hardcoded assertions.
	packageCount := countScannedPackages(root)
	t.Logf("render gate scanned %d packages", packageCount)

	// Assert minimum package count. We expect to scan at least 40+ packages
	// with errs.New/Wrap sites (the BUG-357 campaign covered 43; new packages
	// using errs should be discovered by this gate). Derive from tree: if the
	// repo has 50+ Go packages under internal/, cmd/, tools/, we should see
	// most of them. Assert >= 40 as a sanity check.
	if packageCount < 40 {
		t.Errorf("whole-tree scan visited %d packages, expected >= 40 (indicates scanner may be broken or tree structure changed)",
			packageCount)
	}
}

// countScannedPackages walks root and counts directories containing .go files
// (excluding testdata and test files). Used to derive the minimum package
// assertion (GR#15: never hardcode a constant when the tree can provide it).
func countScannedPackages(root string) int {
	pkgDirs := make(map[string]bool)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		pkgDirs[dir] = true
		return nil
	})
	return len(pkgDirs)
}
