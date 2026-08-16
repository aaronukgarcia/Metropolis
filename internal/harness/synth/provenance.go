package synth

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// This file is BUG-245: the accepted-regressions ledger's provenance is the
// git COMMIT, not the workstation. The pure parser (accepted.go's
// LoadAcceptedRegistry) reads whatever is in a file on disk and trusts it —
// which is fine for a read-only consumer like metricsdash, but is exactly the
// forgeable shape the audit flagged for the GATE: a developer can edit
// perf-accepted-regressions.json locally, run cmd/perfci, and self-vouch a
// regression with no commit, no PR, and no record in git history.
//
// The loaders here close that path mechanically, not by convention:
//
//   - LoadAcceptedRegistryFromGit reads the ledger's COMMITTED content at
//     HEAD via `git show HEAD:<relPath>` — a working-tree edit (tracked or
//     untracked) is invisible to it, because it never reads the working-tree
//     file at all. The provenance is "this content is in the commit under
//     test", which on a branch-protected repo means "a reviewed PR added it".
//   - It also refuses any entry whose commitHash does not resolve to a real
//     commit object in the repository (`git rev-parse --verify <hash>^{commit}`),
//     so a committed entry cannot name a fabricated or uncommitted hash.
//
// A ledger path that is NOT present at HEAD (never committed, or present only
// as an untracked file on disk) is treated as an EMPTY registry — "nothing
// has ever been accepted" — mirroring accepted.go's "absence is not failure"
// posture for a missing file. A git failure that is NOT "path absent from
// HEAD" (git unavailable, not a repository) is a hard error: provenance
// cannot be established, so the gate fails closed rather than guessing.

// LoadAcceptedRegistryFromGit reads the accepted-regressions ledger's
// COMMITTED content at HEAD of the repository rooted at repoRoot, where
// relPath is the ledger's path relative to repoRoot (forward slashes). See
// the package-level comment for the full BUG-245 rationale. It returns an
// empty registry when the ledger has never been committed at HEAD, and a
// hard error when git is unavailable, the path is unreadable from HEAD for
// any other reason, the committed content is malformed, or an entry names a
// commit hash that is not a real commit object in the repository.
func LoadAcceptedRegistryFromGit(repoRoot, relPath string) (AcceptedRegistry, error) {
	data, exists, err := gitShowHead(repoRoot, relPath)
	if err != nil {
		return nil, fmt.Errorf("synth: reading committed accepted-regressions ledger HEAD:%s from %q: %w", relPath, repoRoot, err)
	}
	if !exists {
		// Never committed at HEAD (or present only as an untracked working
		// tree file) — "nothing has ever been accepted". This is the half of
		// BUG-245 that makes a local edit inert: the untracked/dirty file is
		// not honoured, it is not even read.
		return AcceptedRegistry{}, nil
	}

	registry, err := parseAcceptedRegistry(data, "HEAD:"+relPath)
	if err != nil {
		return nil, err
	}

	// Verify each entry's commitHash resolves to a real commit object, in a
	// deterministic order so the first offending entry reported is stable.
	keys := make([]acceptedKey, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Preset != keys[j].Preset {
			return keys[i].Preset < keys[j].Preset
		}
		return keys[i].CommitHash < keys[j].CommitHash
	})
	for _, key := range keys {
		if err := gitCommitExists(repoRoot, key.CommitHash); err != nil {
			return nil, fmt.Errorf(
				"synth: accepted-regressions ledger HEAD:%s entry for preset %q references commit %q, which is not a real commit in this repository -- an acceptance must name a committed, reviewed commit, not a bare local or fabricated hash (BUG-245): %w",
				relPath, key.Preset, key.CommitHash, err,
			)
		}
	}
	return registry, nil
}

// LoadAcceptedRegistryFromWorkingDir loads the accepted-regressions ledger
// with committed-at-HEAD provenance, resolving the repository root from the
// current working directory and the ledger's location relative to it. This
// is cmd/perfci's production loader (BUG-245): a local, uncommitted edit to
// the ledger cannot self-vouch a regression, and a ledger that resolves
// outside the repository (which therefore cannot carry committed provenance)
// is refused rather than trusted.
func LoadAcceptedRegistryFromWorkingDir(path string) (AcceptedRegistry, error) {
	rootOut, err := gitExec(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("synth: cannot establish git provenance for the accepted-regressions ledger (is %q inside a git checkout?): %w", path, err)
	}
	repoRoot := strings.TrimSpace(string(rootOut))

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("synth: resolving accepted-regressions ledger path %q: %w", path, err)
	}
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return nil, fmt.Errorf("synth: relating accepted-regressions ledger %q to repository root %q: %w", path, repoRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("synth: accepted-regressions ledger %q resolves outside the repository root %q -- a ledger outside the repository cannot carry committed provenance, refusing to trust it (BUG-245)", path, repoRoot)
	}
	return LoadAcceptedRegistryFromGit(repoRoot, filepath.ToSlash(rel))
}

// gitShowHead returns the committed content of HEAD:relPath in repoRoot.
// The second return value is false when the path does not exist in HEAD
// (either never committed, or present only as an untracked file on disk) —
// the caller treats that as an empty registry. Any other git failure is
// returned as a hard error.
func gitShowHead(repoRoot, relPath string) ([]byte, bool, error) {
	out, err := gitExec(repoRoot, "show", "HEAD:"+relPath)
	if err == nil {
		return out, true, nil
	}
	msg := err.Error()
	if strings.Contains(msg, "does not exist in 'HEAD'") || strings.Contains(msg, "exists on disk, but not in 'HEAD'") {
		return nil, false, nil
	}
	return nil, false, err
}

// gitCommitExists returns nil when hash resolves to a real commit object in
// repoRoot's repository, else an error. `git rev-parse --verify <hash>^{commit}`
// prints the resolved hash and exits 0 only when <hash> peels to a commit.
func gitCommitExists(repoRoot, hash string) error {
	_, err := gitExec(repoRoot, "rev-parse", "--verify", "--quiet", hash+"^{commit}")
	return err
}

// gitExec runs `git args...` with working directory dir and returns its
// stdout. GIT_OPTIONAL_LOCKS=0 keeps these read-only invocations from ever
// contending for (or creating) a repository index.lock — these commands are
// all reads (show, rev-parse), and the lock-avoidance keeps concurrent test
// runs and read-only checkouts safe.
func gitExec(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
