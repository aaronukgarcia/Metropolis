package errs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-920: shared skip rule for the repo-tree scanner tests in this
// package (real_callsite_gate_test.go's scanTree, render_gate_realtree_test.go's
// countScannedPackages). Test-only concern, so it lives in a _test.go file
// here rather than in a new production package (which would need GR#20
// registry/edge registration for a test-only helper).
//
// IDENTICAL COPY: internal/harness/synth/skipscandir_test.go carries the
// same helper and doc comment for that package's walkers (phasehooks_test.go,
// reachability_test.go). A future change to the skip rule must update BOTH
// copies — there is no shared production package to change once.
//
// skipScanDir reports whether the directory named name at path should be
// excluded from a repo-tree source scan (a filepath.Walk/WalkDir callback
// should return filepath.SkipDir when this is true; isDir must be the
// entry's own IsDir(), and this always returns false for non-directories).
//
// On the main checkout, walking from the repository root while skipping
// only "testdata" also means descending into every parallel-lane git
// worktree checked out under .claude/worktrees/* (each one a full source
// copy — roughly 250 of them at the time BUG-920 was found), a stray
// worktree checked out elsewhere in the tree, and webconsole/node_modules
// — turning a scan that takes seconds in a clean worktree or on CI into
// one that times out (20 minutes measured on this package).
//
// A directory is skipped when:
//   - its name starts with "." — covers .git, .claude (and therefore every
//     .claude/worktrees/* lane checkout), and any other dot-directory
//     convention. No scanner using this helper intentionally reads .github,
//     so folding it into the same dot-prefix rule is safe;
//   - its name is "node_modules" — third-party JS dependency trees are
//     never Go source and are large enough to make a walk slow even when
//     they're harmless;
//   - its name is "testdata" — the long-standing exclusion every scanner
//     already had, folded in here so callers apply exactly one rule; or
//   - it is itself a git worktree root: `git worktree add` marks a linked
//     checkout by writing a top-level ".git" ENTRY that is a plain file
//     containing a "gitdir: ..." pointer, never a directory (a normal
//     clone's ".git" is a directory). This catches a worktree checked out
//     under a name that does not start with "." — exactly the shape of
//     the stray "gitMetropolis.claudeworktreeslane-bug690" directory found
//     alongside the main checkout, which the dot-prefix rule alone would
//     miss.
func skipScanDir(path, name string, isDir bool) bool {
	if !isDir {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	if name == "node_modules" || name == "testdata" {
		return true
	}
	if fi, err := os.Lstat(filepath.Join(path, ".git")); err == nil && fi.Mode().IsRegular() {
		return true
	}
	return false
}

func TestSkipScanDir_DotDirectorySkipped(t *testing.T) {
	root := t.TempDir()
	if !skipScanDir(root, ".git", true) {
		t.Fatalf("skipScanDir(%q) = false, want true (dot-directory)", ".git")
	}
}

func TestSkipScanDir_NodeModulesSkipped(t *testing.T) {
	root := t.TempDir()
	if !skipScanDir(root, "node_modules", true) {
		t.Fatalf("skipScanDir(node_modules) = false, want true")
	}
}

func TestSkipScanDir_TestdataSkipped(t *testing.T) {
	root := t.TempDir()
	if !skipScanDir(root, "testdata", true) {
		t.Fatalf("skipScanDir(testdata) = false, want true")
	}
}

func TestSkipScanDir_WorktreeRootSkipped(t *testing.T) {
	// A linked git worktree's top-level ".git" entry is a FILE (a gitdir
	// pointer), never a directory — reproduce that shape exactly, under
	// an ordinary-looking directory name that does NOT start with "."
	// (mirroring the stray "gitMetropolis.claudeworktreeslane-bug690"
	// directory found in production).
	root := t.TempDir()
	worktree := filepath.Join(root, "some-worktree-checkout")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", worktree, err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /some/path\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.git): %v", err)
	}
	if !skipScanDir(worktree, "some-worktree-checkout", true) {
		t.Fatalf("skipScanDir(worktree root) = false, want true")
	}
}

func TestSkipScanDir_OrdinaryPackageDirectoryNotSkipped(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "somepkg")
	if err := os.Mkdir(pkgDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", pkgDir, err)
	}
	if skipScanDir(pkgDir, "somepkg", true) {
		t.Fatalf("skipScanDir(ordinary package dir) = true, want false")
	}
}

func TestSkipScanDir_WalkRootNeverSkipped(t *testing.T) {
	// The walk's own root must never be excluded by this helper alone —
	// every call site special-cases path == root before calling this, but
	// pin that a root shaped like a worktree (dot-prefixed, or carrying a
	// .git FILE, as every .claude/worktrees/lane-* checkout does) would
	// otherwise report skip=true, which is exactly why callers must guard
	// path != root themselves.
	root := t.TempDir()
	worktreeRoot := filepath.Join(root, ".claude-style-root")
	if err := os.Mkdir(worktreeRoot, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", worktreeRoot, err)
	}
	if !skipScanDir(worktreeRoot, filepath.Base(worktreeRoot), true) {
		t.Fatalf("skipScanDir(dot-prefixed root) = false, want true — callers must special-case path == root, not rely on this helper to exempt it")
	}
}
