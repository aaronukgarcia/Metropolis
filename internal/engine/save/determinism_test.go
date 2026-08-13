package save

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSaveManual_ByteDeterminism is AC-14: two saves taken from the same
// deterministic state (same worldSeed, same CreatedAtTick, same
// "command log" driving the fixture to that state) must produce
// byte-identical bundles across EVERY file in the bundle directory —
// header, every shard, and this package's own save-meta.json — not just
// one shard or the header alone.
func TestSaveManual_ByteDeterminism(t *testing.T) {
	buildFixture := func() []Participant {
		return []Participant{
			newWidgetParticipant(
				widget{ID: 1, Name: "alpha", Score: 3.5},
				widget{ID: 2, Name: "beta", Score: -1.25},
			),
			newGadgetParticipant(
				gadget{SerialNo: "SN-001", Weight: 10},
			),
		}
	}
	ctx := fixtureContext(500, 42)

	root1 := t.TempDir()
	if err := NewManager(root1, buildFixture(), "corr-1").SaveManual(ctx, "det"); err != nil {
		t.Fatalf("SaveManual run 1: %v", err)
	}
	root2 := t.TempDir()
	if err := NewManager(root2, buildFixture(), "corr-2").SaveManual(ctx, "det"); err != nil {
		t.Fatalf("SaveManual run 2: %v", err)
	}

	dir1 := manualDir(root1, "det")
	dir2 := manualDir(root2, "det")

	files1 := allFilesRelative(t, dir1)
	files2 := allFilesRelative(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle directory %q has no files", dir1)
	}
	sort.Strings(files1)
	sort.Strings(files2)
	if len(files1) != len(files2) {
		t.Fatalf("bundle file sets differ in count: run1=%v run2=%v", files1, files2)
	}
	for i := range files1 {
		if files1[i] != files2[i] {
			t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
		}
	}

	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		if err != nil {
			t.Fatalf("reading %s from run 1: %v", rel, err)
		}
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		if err != nil {
			t.Fatalf("reading %s from run 2: %v", rel, err)
		}
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic state (correlation ID differs by design and is NOT persisted into bundle bytes, so this must not be that)", rel)
		}
	}
}

func allFilesRelative(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %q: %v", dir, err)
	}
	return out
}
