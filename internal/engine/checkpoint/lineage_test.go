package checkpoint

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestAC5_LineageMetadataOnly proves Lineage answers purely from metadata
// files — after deleting every shard file from every bundle it still
// succeeds and reports correct identifiers, parentage, and ticks (AC-5).
func TestAC5_LineageMetadataOnly(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac5")

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(20, 2), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(30, 3), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}

	// Delete every shard file, leaving only metadata.
	for _, id := range []string{"A", "B", "C"} {
		if err := os.RemoveAll(serialize.ShardsDir(checkpointDir(root, ID(id)))); err != nil {
			t.Fatalf("RemoveAll shards for %s: %v", id, err)
		}
	}

	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage after shard deletion: %v", err)
	}
	byID := map[ID]Checkpoint{}
	for _, n := range tree.Nodes {
		byID[n.ID] = n
	}
	if len(byID) != 3 {
		t.Fatalf("Lineage reported %d checkpoints, want 3", len(byID))
	}
	if byID["A"].ParentID != "" || byID["B"].ParentID != "A" || byID["C"].ParentID != "A" {
		t.Fatalf("parentage wrong: %+v %+v %+v", byID["A"], byID["B"], byID["C"])
	}
	if byID["A"].CreatedAtTick != 10 || byID["B"].CreatedAtTick != 20 || byID["C"].CreatedAtTick != 30 {
		t.Fatalf("ticks wrong: %+v", byID)
	}
	if byID["A"].GameMonth != 1 || byID["B"].GameMonth != 2 || byID["C"].GameMonth != 3 {
		t.Fatalf("months wrong: %+v", byID)
	}
	if !byID["C"].Active || byID["A"].Active || byID["B"].Active {
		t.Fatalf("active flags wrong: A=%v B=%v C=%v", byID["A"].Active, byID["B"].Active, byID["C"].Active)
	}
}

// TestAC10_TreeStructureNavigable proves Lineage returns a navigable tree
// (parent→children) whose root-to-leaf paths reflect the lineage — walked
// via the Children structure itself, not re-derived from ParentID (AC-10).
func TestAC10_TreeStructureNavigable(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac10")

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(30, 1), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}
	fork, err := m.Revert(fixtureContext(40, 1), "A")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}

	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if len(tree.Roots) != 1 {
		t.Fatalf("expected exactly 1 root, got %d", len(tree.Roots))
	}
	rootNode := tree.Roots[0]
	if rootNode.Checkpoint.ID != "A" {
		t.Fatalf("root = %q, want A", rootNode.Checkpoint.ID)
	}

	childIDs := make([]string, 0, len(rootNode.Children))
	for _, c := range rootNode.Children {
		childIDs = append(childIDs, string(c.Checkpoint.ID))
		if len(c.Children) != 0 {
			t.Fatalf("leaf %q unexpectedly has children", c.Checkpoint.ID)
		}
	}
	sort.Strings(childIDs)
	want := []string{"B", "C", string(fork.ID)}
	sort.Strings(want)
	if len(childIDs) != len(want) {
		t.Fatalf("children = %v, want %v", childIDs, want)
	}
	for i := range childIDs {
		if childIDs[i] != want[i] {
			t.Fatalf("children = %v, want %v", childIDs, want)
		}
	}
}

// TestManualSubdirMirrorsSaveLayout is the weakness-pattern-#2 drift test
// for the duplicated "manual" subdirectory literal: it asserts this
// package's checkpoint layout is exactly where feat.saveux actually writes
// manual saves, so the two can never silently diverge.
func TestManualSubdirMirrorsSaveLayout(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-drift")
	if _, err := m.CreateCheckpoint(fixtureContext(1, 1), "cp", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	summaries, _, err := save.List(root)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	wantPath := filepath.Join(root, manualSubdir, "cp")
	found := false
	for _, s := range summaries {
		if s.Path == wantPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("checkpoint not discoverable by feat.saveux at %q — feat.checkpoint's manualSubdir constant has drifted from feat.saveux's on-disk manual-save layout; update manualSubdir in meta.go (changing one requires changing the other)", wantPath)
	}
}
