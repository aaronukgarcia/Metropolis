package uitest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSnapshotRoundTrip is AC-4/AC-5: driving a Harness against a
// fixture, rendering, and capturing produces a plain-text cell-buffer
// snapshot that AssertSnapshot compares against a committed golden file
// under testdata/snapshots/ — this test's own golden
// (testdata/snapshots/TestSnapshotRoundTrip.golden) was generated with
// `go test ./internal/harness/uitest/... -run TestSnapshotRoundTrip -update`
// and is committed alongside this file (AC-5's "human-diffable in a
// PR" — it is plain text, reviewed like any other diff).
func TestSnapshotRoundTrip(t *testing.T) {
	fx := buildFixture(t, 2)
	h := NewHarness(errs.NewCorrelationID(), nil, countDraw)
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}
	if err := h.RunScript("b", 2, 2*time.Second); err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if _, err := h.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	AssertSnapshot(t, got)
}

// TestSnapshotMismatchDetected is AC-4's other half: a deliberately
// mutated buffer (compared against a hand-seeded golden) produces a
// non-empty, reported diff. This exercises loadOrUpdateSnapshot directly
// (AssertSnapshot's pure comparison step) rather than going through
// AssertSnapshot/t.Run — a failing subtest would mark THIS test failed
// too (Go's testing package propagates subtest failure to the parent),
// which would make a passing suite report a spurious failure for a test
// whose whole point is to prove a mismatch is detected, not to itself
// fail the build.
func TestSnapshotMismatchDetected(t *testing.T) {
	path, err := snapshotPath(t.Name())
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("AAAA\n"), 0o644); err != nil {
		t.Fatalf("seeding golden: %v", err)
	}
	defer func() { _ = os.Remove(path) }()

	matched, want, err := loadOrUpdateSnapshot(path, "BBBB\n", false) // deliberately different from the seeded golden
	if err != nil {
		t.Fatalf("loadOrUpdateSnapshot: unexpected error: %v", err)
	}
	if matched {
		t.Fatal("loadOrUpdateSnapshot: got matched=true for deliberately different content, want false (non-empty diff)")
	}
	if want != "AAAA\n" {
		t.Errorf("loadOrUpdateSnapshot returned want=%q, want %q (the seeded golden)", want, "AAAA\n")
	}
}

// TestMissingGoldenReportsDistinctError is AC-8: a missing/unreadable
// golden produces a distinct, registry-sourced MET-H102 error,
// distinguishable from a content-mismatch failure.
func TestMissingGoldenReportsDistinctError(t *testing.T) {
	path, err := snapshotPath(t.Name())
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	_ = os.Remove(path) // ensure absent regardless of prior runs

	_, _, err = loadOrUpdateSnapshot(path, "anything\n", false)
	if err == nil {
		t.Fatal("loadOrUpdateSnapshot against a missing golden: got nil error, want MET-H102")
	}
	if !errors.Is(err, &errs.E{Code: codeMissingGolden}) {
		t.Errorf("error %v is not %s (missing golden)", err, codeMissingGolden)
	}
}

// TestSnapshotNameRejectsTraversal is AC-5b: a hostile snapshot-name
// segment (here, "..", the classic traversal payload) is rejected
// outright rather than resolving outside testdata/snapshots/.
func TestSnapshotNameRejectsTraversal(t *testing.T) {
	cases := []string{
		"../../../etc/passwd",
		"Test/../../../etc/passwd",
		"..",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := snapshotPath(name)
			if err == nil {
				t.Fatalf("snapshotPath(%q): got nil error, want rejection", name)
			}
			if !errors.Is(err, &errs.E{Code: codeHostileSnapshotName}) {
				t.Errorf("error %v is not %s (hostile snapshot name)", err, codeHostileSnapshotName)
			}
		})
	}
}

// TestSnapshotNameAcceptsRealSubtestHierarchy confirms the traversal
// guard does not also reject ordinary, well-formed subtest names (no
// false positives — a guard that rejects everything is not a guard).
func TestSnapshotNameAcceptsRealSubtestHierarchy(t *testing.T) {
	path, err := snapshotPath("TestFoo/case_one")
	if err != nil {
		t.Fatalf("snapshotPath(TestFoo/case_one): unexpected error: %v", err)
	}
	if want := filepath.Join("TestFoo", "case_one.golden"); !strings.HasSuffix(path, want) {
		t.Errorf("path = %q, want it to end in %q", path, want)
	}
}
