package save

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestList_NeverOpensShardFiles is AC-8: delete every shard file from a
// bundle after writing it (leaving header.json and save-meta.json
// intact), and confirm List still succeeds and reports the same
// summary.
func TestList_NeverOpensShardFiles(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "x", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")
	if err := mgr.SaveManual(fixtureContext(5, 1), "s1"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	before, readErrs, err := List(root)
	if err != nil || len(readErrs) != 0 {
		t.Fatalf("List before: err=%v readErrs=%v", err, readErrs)
	}
	if len(before) != 1 {
		t.Fatalf("List before returned %d summaries, want 1", len(before))
	}

	dir := manualDir(root, "s1")
	shardsDir := serialize.ShardsDir(dir)
	entries, err := os.ReadDir(shardsDir)
	if err != nil {
		t.Fatalf("reading shards dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("test setup: expected at least one shard file to delete")
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(shardsDir, e.Name())); err != nil {
			t.Fatalf("removing shard file %s: %v", e.Name(), err)
		}
	}

	after, readErrs, err := List(root)
	if err != nil {
		t.Fatalf("List after shard deletion: %v", err)
	}
	if len(readErrs) != 0 {
		t.Fatalf("List after shard deletion reported readErrs=%v, want none (header/meta are untouched)", readErrs)
	}
	if len(after) != 1 {
		t.Fatalf("List after shard deletion returned %d summaries, want 1", len(after))
	}
	if after[0] != before[0] {
		t.Fatalf("List summary changed after shard deletion: before=%+v after=%+v", before[0], after[0])
	}
}

// TestList_SkipsStagingDirectory confirms an in-flight (never-promoted)
// staging bundle is never visible to List, independent of AC-9's own
// forced-failure test.
func TestList_SkipsStagingDirectory(t *testing.T) {
	root := t.TempDir()
	// Manufacture a staging dir directly, bypassing writeBundle, to
	// prove List's own enumeration logic — not merely "writeBundle
	// always cleans up" — is what keeps staging invisible.
	stagingDir, err := newStagingDir(root, "test-corr")
	if err != nil {
		t.Fatalf("newStagingDir: %v", err)
	}
	if err := os.MkdirAll(serialize.ShardsDir(stagingDir), 0o755); err != nil {
		t.Fatalf("mkdir shards: %v", err)
	}
	h := serialize.NewHeader(1, 2, 3, "v")
	if err := serialize.WriteHeader(stagingDir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := WriteMeta(stagingDir, Meta{SaveKind: KindManual, DisplayName: "ghost"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	summaries, _, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("List returned %d summaries, want 0 — a staged-but-never-promoted bundle must never be discoverable", len(summaries))
	}
}
