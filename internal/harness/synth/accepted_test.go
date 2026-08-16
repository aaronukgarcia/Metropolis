package synth

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadAcceptedRegistry_MissingFileIsEmptyNotError mirrors AC-8's
// "absence is not failure" posture, applied to the registry file: most
// repository states have never had a regression accepted, so a missing
// perf-accepted-regressions.json must not fail an ordinary CI run.
func TestLoadAcceptedRegistry_MissingFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	registry, err := LoadAcceptedRegistry(path)
	if err != nil {
		t.Fatalf("LoadAcceptedRegistry on a missing file should not error, got %v", err)
	}
	if reason, ok := registry.Reason("1M", "anything"); ok {
		t.Fatalf("empty registry unexpectedly reported an acceptance: reason=%q ok=%v", reason, ok)
	}
}

// TestLoadAcceptedRegistry_ValidEntriesAreIndexed proves a well-formed
// registry round-trips: an entry for (preset, commitHash) is reachable via
// Reason, and an entry for a DIFFERENT preset or commit is not — the
// registry must not accidentally accept a commit under the wrong preset,
// which would silently widen an acceptance beyond what a human actually
// reviewed.
func TestLoadAcceptedRegistry_ValidEntriesAreIndexed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	content := `[
		{"preset": "1M", "commitHash": "abc123", "reason": "engine.core gained a real phase hook; reviewed by aaron"},
		{"preset": "10M", "commitHash": "def456", "reason": "10M-scale allocator change; expected"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}

	registry, err := LoadAcceptedRegistry(path)
	if err != nil {
		t.Fatalf("LoadAcceptedRegistry: %v", err)
	}

	if reason, ok := registry.Reason("1M", "abc123"); !ok || reason == "" {
		t.Fatalf("Reason(1M, abc123) = (%q, %v), want a non-empty reason and ok=true", reason, ok)
	}
	if _, ok := registry.Reason("10M", "abc123"); ok {
		t.Fatal("Reason(10M, abc123) = ok=true, want false — an entry must not corroborate the wrong PRESET for a matching commit hash")
	}
	if _, ok := registry.Reason("1M", "def456"); ok {
		t.Fatal("Reason(1M, def456) = ok=true, want false — an entry must not corroborate the wrong COMMIT for a matching preset")
	}
	if _, ok := registry.Reason("1M", "never-committed"); ok {
		t.Fatal("Reason(1M, never-committed) = ok=true, want false")
	}
}

// TestLoadAcceptedRegistry_EmptyFileIsEmptyNotError: a human may create
// this file ahead of ever needing an entry (e.g. `git add` an empty `[]`)
// — that must behave identically to a missing file, not as a parse error.
func TestLoadAcceptedRegistry_EmptyFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatalf("writing empty registry fixture: %v", err)
	}

	registry, err := LoadAcceptedRegistry(path)
	if err != nil {
		t.Fatalf("LoadAcceptedRegistry: %v", err)
	}
	if reason, ok := registry.Reason("1M", "anything"); ok {
		t.Fatalf("empty-array registry unexpectedly reported an acceptance: reason=%q ok=%v", reason, ok)
	}
}

// TestLoadAcceptedRegistry_MalformedJSONIsAHardError proves the registry
// fails CLOSED, not open: this file is BUG-095's sole evidence source, so
// a corrupted copy of it must be a loud, hard error (cmd/perfci exits 2),
// never silently treated as "nothing accepted" — which would relocate
// BUG-097's missing-vs-lost ambiguity into a security-relevant file.
func TestLoadAcceptedRegistry_MalformedJSONIsAHardError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("writing malformed registry fixture: %v", err)
	}

	if _, err := LoadAcceptedRegistry(path); err == nil {
		t.Fatal("LoadAcceptedRegistry on malformed JSON returned nil error, want a hard failure")
	}
}

// TestLoadAcceptedRegistry_IncompleteEntryIsAHardError proves an entry
// missing preset, commitHash, or reason is rejected outright rather than
// silently indexed as a wildcard or an unreachable no-op — an incomplete
// entry passing review would be a reviewer error this loader should
// surface immediately, not swallow.
func TestLoadAcceptedRegistry_IncompleteEntryIsAHardError(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"missing preset", `[{"commitHash": "abc123", "reason": "why"}]`},
		{"missing commitHash", `[{"preset": "1M", "reason": "why"}]`},
		{"missing reason", `[{"preset": "1M", "commitHash": "abc123"}]`},
		{"blank reason", `[{"preset": "1M", "commitHash": "abc123", "reason": "   "}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("writing registry fixture: %v", err)
			}
			if _, err := LoadAcceptedRegistry(path); err == nil {
				t.Fatalf("LoadAcceptedRegistry(%s) returned nil error, want a hard failure", tc.name)
			}
		})
	}
}

// TestLoadAcceptedRegistry_DuplicateEntryIsAHardError: two entries for the
// same (preset, commitHash) are ambiguous — refusing to guess which
// reason is authoritative is the same fail-closed posture as everything
// else in this file.
func TestLoadAcceptedRegistry_DuplicateEntryIsAHardError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-accepted-regressions.json")
	content := `[
		{"preset": "1M", "commitHash": "abc123", "reason": "first"},
		{"preset": "1M", "commitHash": "abc123", "reason": "second, contradicting"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}

	if _, err := LoadAcceptedRegistry(path); err == nil {
		t.Fatal("LoadAcceptedRegistry with a duplicate (preset, commitHash) entry returned nil error, want a hard failure")
	}
}

// TestAcceptedLedger_IsCheckedIn is BUG-245's guard on the accepted-
// regressions ledger itself: the ledger must be a git-tracked, checked-in
// file whose changes go through PR review like any code — not an untracked
// file a workstation can create and a local go test/perfci run then
// silently trusts. A local edit to an UNTRACKED ledger produces no diff,
// no review, and no record, so the "a human reviewed this acceptance"
// claim rests on nothing; committing the ledger as a real source file is
// what makes adding an entry a reviewed PR change rather than a local
// forgery. This test pins the ledger's presence in the source tree (the
// checked-in default is an empty registry — nothing accepted) so removing
// it, or failing to commit it, fails the build exactly like deleting any
// other fixture.
//
// go test runs with this package's directory as its working directory
// (internal/harness/synth), so ../../.. is the repository root — where
// perf-accepted-regressions.json is checked in and where cmd/perfci's
// -accepted-regressions default and .github/workflows/ci.yml both resolve.
func TestAcceptedLedger_IsCheckedIn(t *testing.T) {
	path := filepath.Join("..", "..", "..", "perf-accepted-regressions.json")

	// os.ReadFile (not LoadAcceptedRegistry) is the existence check:
	// LoadAcceptedRegistry deliberately treats a MISSING file as an empty
	// registry (the "absence is not failure" posture this package's
	// TestLoadAcceptedRegistry_MissingFileIsEmptyNotError pins), so it
	// cannot distinguish "checked in and empty" from "never committed" —
	// which is exactly the distinction BUG-245 exists to enforce.
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("BUG-245: accepted-regressions ledger %q is not checked in — the ledger must be a git-tracked file reviewed in PR, never an untracked file a local perfci/go test run silently trusts: %v", path, err)
	}

	registry, err := LoadAcceptedRegistry(path)
	if err != nil {
		t.Fatalf("BUG-245: checked-in ledger %q does not parse as an AcceptedRegistry: %v", path, err)
	}
	if _, ok := registry.Reason("1M", "anything"); ok {
		t.Fatalf("BUG-245: the checked-in ledger must default to an empty registry (nothing accepted); it unexpectedly accepts a commit")
	}
}
