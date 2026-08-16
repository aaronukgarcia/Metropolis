package synth

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Scratch-repo helpers. The provenance loader shells out to real git, so
// these tests build their own throwaway repositories in a temp dir rather
// than touching the enclosing Metropolis checkout (which must stay intact
// under GR#24's no-worktree-destruction rule, and whose committed ledger is
// shared state).

func initScratchRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "synth-test@example.invalid")
	runGit(t, dir, "config", "user.name", "synth-test")
}

// runGit runs `git args...` in dir and fails the test on any error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", msg)
}

func headHash(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

func scratchWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
}

// TestLoadAcceptedRegistryFromGit_IgnoresWorkingTreeEdit is BUG-245's core
// regression test: the ledger's committed content is an empty registry, and
// a workstation then edits the working-tree file (uncommitted) to add an
// entry that self-declares the current commit's regression accepted. The
// provenance loader must read the ledger from HEAD, so that forged edit is
// never honoured.
//
// RED against the pre-fix shape: accepted.go's LoadAcceptedRegistry reads the
// working-tree file directly, so it would have indexed the forged entry and
// Reason("1M", commitHash) would report ok=true with the forged reason.
func TestLoadAcceptedRegistryFromGit_IgnoresWorkingTreeEdit(t *testing.T) {
	dir := t.TempDir()
	initScratchRepo(t, dir)

	ledgerPath := filepath.Join(dir, "perf-accepted-regressions.json")
	scratchWrite(t, ledgerPath, "[]\n")
	commitAll(t, dir, "empty accepted-regressions ledger")
	commitHash := headHash(t, dir)

	// A local, UNCOMMITTED edit — exactly the self-vouch BUG-245 closes.
	forged := `[{"preset": "1M", "commitHash": "` + commitHash + `", "reason": "self-vouched local edit, never committed"}]` + "\n"
	scratchWrite(t, ledgerPath, forged)

	registry, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json")
	if err != nil {
		t.Fatalf("LoadAcceptedRegistryFromGit: %v", err)
	}
	if reason, ok := registry.Reason("1M", commitHash); ok {
		t.Fatalf("BUG-245: the provenance loader honoured a working-tree (uncommitted) ledger edit — forged entry for commit %q resolved to reason %q; the gate must read the ledger's COMMITTED content at HEAD, never the workstation's working-tree file", commitHash, reason)
	}
}

// TestLoadAcceptedRegistryFromGit_UncommittedLedgerIsEmpty covers the
// untracked-ledger half of the same forge: the ledger exists only as an
// untracked file on disk (never committed at HEAD at all). It must read as
// empty, never honoured — this is the exact state the repo was in while the
// ledger was an uncommitted new file.
func TestLoadAcceptedRegistryFromGit_UncommittedLedgerIsEmpty(t *testing.T) {
	dir := t.TempDir()
	initScratchRepo(t, dir)

	// Give HEAD a commit that is NOT the ledger.
	scratchWrite(t, filepath.Join(dir, "keep.txt"), "x\n")
	commitAll(t, dir, "throwaway commit")

	// The ledger exists on disk but is untracked.
	scratchWrite(t, filepath.Join(dir, "perf-accepted-regressions.json"),
		`[{"preset": "1M", "commitHash": "abcdef", "reason": "uncommitted self-vouch"}]`+"\n")

	registry, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json")
	if err != nil {
		t.Fatalf("LoadAcceptedRegistryFromGit on an untracked ledger should be empty-not-error, got %v", err)
	}
	if reason, ok := registry.Reason("1M", "abcdef"); ok {
		t.Fatalf("BUG-245: an untracked (never-committed) ledger entry was honoured — reason=%q; a ledger with no committed provenance must read as empty", reason)
	}
}

// TestLoadAcceptedRegistryFromGit_RejectsEntryNamingUncommittedHash proves a
// COMMITTED ledger entry that names a commit hash which does not exist in the
// repository is a hard error — a self-declared hash has no committed
// provenance, so it must be refused rather than trusted.
//
// RED against the pre-fix shape: the pure parser indexes the entry verbatim
// with no existence check and would have returned it.
func TestLoadAcceptedRegistryFromGit_RejectsEntryNamingUncommittedHash(t *testing.T) {
	dir := t.TempDir()
	initScratchRepo(t, dir)

	scratchWrite(t, filepath.Join(dir, "perf-accepted-regressions.json"),
		`[{"preset": "1M", "commitHash": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "reason": "names a hash that was never committed"}]`+"\n")
	commitAll(t, dir, "ledger naming a non-existent commit")

	if _, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json"); err == nil {
		t.Fatal("BUG-245: LoadAcceptedRegistryFromGit accepted a ledger entry naming a commit hash that does not exist in the repository — an acceptance must name a committed, reviewed commit, not a bare local or fabricated hash")
	}
}

// TestLoadAcceptedRegistryFromGit_HonorsCommittedEntry is the positive
// control: a committed ledger entry naming a real commit must still be
// honoured, proving the provenance hardening did not remove the accept path
// (BUG-095) — it only moved the evidence from "whatever is on disk" to "what
// is committed at HEAD".
func TestLoadAcceptedRegistryFromGit_HonorsCommittedEntry(t *testing.T) {
	dir := t.TempDir()
	initScratchRepo(t, dir)

	// Two commits: the first establishes a real HEAD hash to accept, the
	// second commits the ledger entry naming that hash.
	scratchWrite(t, filepath.Join(dir, "keep.txt"), "x\n")
	commitAll(t, dir, "throwaway commit")
	first := headHash(t, dir)

	scratchWrite(t, filepath.Join(dir, "perf-accepted-regressions.json"),
		`[{"preset": "1M", "commitHash": "`+first+`", "reason": "reviewed slowdown acceptance"}]`+"\n")
	commitAll(t, dir, "accept first commit's regression")

	registry, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json")
	if err != nil {
		t.Fatalf("LoadAcceptedRegistryFromGit: %v", err)
	}
	if reason, ok := registry.Reason("1M", first); !ok || reason == "" {
		t.Fatalf("Reason(1M, %q) = (%q, %v), want a non-empty reason and ok=true — a committed entry naming a real commit must be honoured", first, reason, ok)
	}
}

// TestLoadAcceptedRegistryFromGit_NotARepositoryFailsClosed proves that when
// git provenance cannot be established (the directory is not a repository),
// the loader fails closed rather than silently treating the ledger as empty.
func TestLoadAcceptedRegistryFromGit_NotARepositoryFailsClosed(t *testing.T) {
	dir := t.TempDir() // deliberately not `git init`'d
	if _, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json"); err == nil {
		t.Fatal("BUG-245: LoadAcceptedRegistryFromGit on a non-repository returned nil error — provenance cannot be established without git, so it must fail closed")
	}
}

// TestLoadAcceptedRegistryFromGit_MalformedCommittedLedgerFailsClosed proves
// the committed-content path keeps accepted.go's fail-closed-on-malformed
// posture: a ledger that IS committed but unparseable is a hard error, never
// silently "nothing accepted".
func TestLoadAcceptedRegistryFromGit_MalformedCommittedLedgerFailsClosed(t *testing.T) {
	dir := t.TempDir()
	initScratchRepo(t, dir)

	scratchWrite(t, filepath.Join(dir, "perf-accepted-regressions.json"), "{not valid json\n")
	commitAll(t, dir, "malformed ledger")

	if _, err := LoadAcceptedRegistryFromGit(dir, "perf-accepted-regressions.json"); err == nil {
		t.Fatal("LoadAcceptedRegistryFromGit on a committed-but-malformed ledger returned nil error, want a hard failure (fail closed)")
	}
}
