package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestAC7_RetentionParameterized runs the retention rule at two candidate
// values of N (2 and 5), proving the SHAPE of "N most-recent abandoned
// branches remain loadable" holds at any N, not just the checked-in
// placeholder (AC-7).
func TestAC7_RetentionParameterized(t *testing.T) {
	for _, n := range []int{2, 5} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			root := t.TempDir()
			m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac7")
			if err := m.SetMaxRetainedForks(n); err != nil {
				t.Fatalf("SetMaxRetainedForks: %v", err)
			}

			if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
				t.Fatalf("CreateCheckpoint A: %v", err)
			}
			if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
				t.Fatalf("CreateCheckpoint B: %v", err)
			}
			// Six sequential reverts to A. Each abandons the previous active
			// head, accumulating six abandoned branches: B (tick 20) and
			// fork1..fork5 (ticks 30..70); fork6 (tick 80) is the active head.
			for i := 1; i <= 6; i++ {
				if _, err := m.Revert(fixtureContext(int64(20+i*10), 1), "A"); err != nil {
					t.Fatalf("Revert %d: %v", i, err)
				}
			}

			tree, err := Lineage(root)
			if err != nil {
				t.Fatalf("Lineage: %v", err)
			}
			present := map[ID]bool{}
			for _, node := range tree.Nodes {
				present[node.ID] = true
			}

			// Exactly A + the active fork6 + N retained abandoned branches.
			if want := n + 2; len(tree.Nodes) != want {
				t.Fatalf("Lineage reports %d nodes, want %d (A + active + %d retained abandoned)", len(tree.Nodes), want, n)
			}
			if !present["A"] || !present["A.fork6"] {
				t.Fatalf("A or the active head A.fork6 missing: %v", present)
			}
			// B is the oldest abandoned branch and is always pruned once the
			// abandoned count exceeds N.
			if present["B"] {
				t.Fatalf("oldest abandoned branch B should be pruned")
			}
			// fork i (tick 20+i*10) is retained iff it is among the N most
			// recently abandoned (highest ticks): i > 5-N.
			for i := 1; i <= 5; i++ {
				id := ID(fmt.Sprintf("A.fork%d", i))
				shouldKeep := i > 5-n
				if present[id] != shouldKeep {
					t.Fatalf("fork%d present=%v, want %v (N=%d)", i, present[id], shouldKeep, n)
				}
			}
			// Every retained branch is independently loadable; every pruned
			// one no longer is.
			for id := range present {
				if _, _, err := m.Load(id); err != nil {
					t.Fatalf("retained branch %s not loadable: %v", id, err)
				}
			}
			for _, id := range []ID{"B"} {
				if _, _, err := m.Load(id); err == nil {
					t.Fatalf("pruned branch %s still loadable", id)
				}
			}
			for i := 1; i <= 5; i++ {
				if i > 5-n {
					continue // retained
				}
				id := ID(fmt.Sprintf("A.fork%d", i))
				if _, _, err := m.Load(id); err == nil {
					t.Fatalf("pruned branch %s still loadable (N=%d)", id, n)
				}
			}
		})
	}
}

// TestAC8_PruneNeverDeletesSharedAncestor builds A→{B,C,D} where A is the
// OLDEST checkpoint but the shared ancestor of two retained branches, and
// proves pruning B (the branch that fell outside retention) never removes A
// (AC-8 — structural sharing, not raw age).
func TestAC8_PruneNeverDeletesSharedAncestor(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac8")
	if err := m.SetMaxRetainedForks(1); err != nil {
		t.Fatalf("SetMaxRetainedForks: %v", err)
	}

	if _, err := m.CreateCheckpoint(fixtureContext(100, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(200, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(300, 1), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}
	// D abandons C, making abandoned = {B(200), C(300)}; N=1 keeps C and
	// prunes B.
	if _, err := m.CreateCheckpoint(fixtureContext(400, 1), "D", "A"); err != nil {
		t.Fatalf("CreateCheckpoint D: %v", err)
	}

	// A survives: it is the ancestor of the retained C and D, despite being
	// the OLDEST checkpoint in the tree.
	if _, _, err := m.Load("A"); err != nil {
		t.Fatalf("A (shared ancestor) was pruned: %v", err)
	}
	// B's own exclusive branch content is gone.
	if _, _, err := m.Load("B"); err == nil {
		t.Fatalf("B should have been pruned, but is still loadable")
	}
	// C (retained abandoned) and D (active) remain loadable.
	if _, _, err := m.Load("C"); err != nil {
		t.Fatalf("C (retained abandoned branch) not loadable: %v", err)
	}
	if _, _, err := m.Load("D"); err != nil {
		t.Fatalf("D (active) not loadable: %v", err)
	}

	// C's lineage is intact: its parent is still A.
	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	byID := map[ID]Checkpoint{}
	for _, node := range tree.Nodes {
		byID[node.ID] = node
	}
	if byID["C"].ParentID != "A" || byID["D"].ParentID != "A" {
		t.Fatalf("lineage broken after prune: C.parent=%q D.parent=%q", byID["C"].ParentID, byID["D"].ParentID)
	}
	if _, ok := byID["B"]; ok {
		t.Fatalf("B still present in lineage after prune")
	}
}

// TestAC9_PruneAtomicOnFailure forces a mid-prune failure and proves the
// retained set is left exactly as it was — every branch present before the
// failed prune is still present and independently loadable afterward (AC-9).
func TestAC9_PruneAtomicOnFailure(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac9")
	// Effectively disable pruning while we accumulate branches.
	if err := m.SetMaxRetainedForks(100); err != nil {
		t.Fatalf("SetMaxRetainedForks: %v", err)
	}

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	for _, b := range []struct {
		name string
		tick int64
	}{
		{"B", 20}, {"C", 30}, {"D", 40}, {"E", 50}, {"F", 60},
	} {
		if _, err := m.CreateCheckpoint(fixtureContext(b.tick, 1), b.name, "A"); err != nil {
			t.Fatalf("CreateCheckpoint %s: %v", b.name, err)
		}
	}
	if err := m.SetMaxRetainedForks(1); err != nil {
		t.Fatalf("SetMaxRetainedForks: %v", err)
	}

	// Inject a pruneRename that fails on its 2nd call, so the first rename
	// (B) succeeds and the second (C) fails partway — forcing the rollback.
	origRename := pruneRename
	calls := 0
	pruneRename = func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected mid-prune failure")
		}
		return os.Rename(oldpath, newpath)
	}
	defer func() { pruneRename = origRename }()

	// Creating G abandons F (5 abandoned branches: B,C,D,E,F); N=1 would
	// prune B,C,D,E. The injected failure aborts the rename phase. The prune
	// failure is non-fatal (SEC-190): CreateCheckpoint returns the created
	// checkpoint with a nil error, and the failure is surfaced via
	// LastPruneError rather than returned alongside the checkpoint.
	cp, err := m.CreateCheckpoint(fixtureContext(70, 1), "G", "A")
	if err != nil {
		t.Fatalf("CreateCheckpoint error = %v, want nil (prune failure is non-fatal — SEC-190)", err)
	}
	if cp.ID != "G" {
		t.Fatalf("CreateCheckpoint returned checkpoint %q, want G", cp.ID)
	}
	if !errors.Is(m.LastPruneError(), &errs.E{Code: ErrPruneFailed}) {
		t.Fatalf("LastPruneError = %v, want ErrPruneFailed", m.LastPruneError())
	}

	// Every branch present before the failed prune — A through G — is still
	// present and still independently loadable.
	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if len(tree.Nodes) != 7 {
		t.Fatalf("Lineage reports %d nodes after failed prune, want 7", len(tree.Nodes))
	}
	for _, id := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		if _, _, err := m.Load(ID(id)); err != nil {
			t.Fatalf("branch %s missing after failed prune: %v", id, err)
		}
	}
}

// TestRetentionSurvivesHeadAncestors pins the exact AC-6 invariant that the
// active head and every ancestor of a retained branch are never pruned, so
// a future edit that broke ancestor retention would fail here.
func TestRetentionSurvivesHeadAncestors(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac6")
	if err := m.SetMaxRetainedForks(0); err != nil {
		t.Fatalf("SetMaxRetainedForks: %v", err)
	}
	// N=0: only the active head and its ancestors survive.
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	// C is the active head; B becomes abandoned and is pruned (N=0). A is
	// C's ancestor and must survive.
	if _, err := m.CreateCheckpoint(fixtureContext(30, 1), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}

	if _, _, err := m.Load("A"); err != nil {
		t.Fatalf("A (ancestor of the active head) was pruned: %v", err)
	}
	if _, _, err := m.Load("C"); err != nil {
		t.Fatalf("C (active head) not loadable: %v", err)
	}
	if _, _, err := m.Load("B"); err == nil {
		t.Fatalf("B should have been pruned (N=0, abandoned), still loadable")
	}
}
