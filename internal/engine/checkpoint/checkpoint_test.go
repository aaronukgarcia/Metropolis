package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestAC1_WholeStateNotDelta proves a checkpoint is a complete,
// independently-loadable bundle (AC-1): serialize.ValidateBundle passes on
// it, and after deleting its parent bundle and every other file under the
// root it still fully reconstructs state — the deletion test that rejects a
// lazy delta-against-parent implementation.
func TestAC1_WholeStateNotDelta(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "a", Score: 1.5}, entry{ID: 2, Name: "b", Score: 2.5})
	gadgets := newMemParticipant("gadget", entry{ID: 7, Name: "g", Score: 0.25})
	m := NewManager(root, []save.Participant{widgets, gadgets}, "corr-ac1")

	if _, err := m.CreateCheckpoint(fixtureContext(100, 4), "cp1", ""); err != nil {
		t.Fatalf("CreateCheckpoint cp1: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(200, 5), "cp2", "cp1"); err != nil {
		t.Fatalf("CreateCheckpoint cp2: %v", err)
	}

	bundleDir := checkpointDir(root, "cp2")
	if _, err := serialize.ValidateBundle(bundleDir); err != nil {
		t.Fatalf("checkpoint bundle failed ValidateBundle: %v", err)
	}

	// Delete the PARENT bundle and the head pointer — every other file under
	// the root — then prove cp2 alone reconstructs the full state. A
	// delta-against-parent implementation would fail exactly this deletion.
	if err := os.RemoveAll(checkpointDir(root, "cp1")); err != nil {
		t.Fatalf("RemoveAll parent: %v", err)
	}
	if err := os.RemoveAll(headPath(root)); err != nil {
		t.Fatalf("RemoveAll head pointer: %v", err)
	}

	widgets.setState(entry{ID: 99, Name: "mutated", Score: 0})
	gadgets.setState()
	if _, _, err := m.Load("cp2"); err != nil {
		t.Fatalf("Load after deleting parent and siblings: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "a", Score: 1.5}, {ID: 2, Name: "b", Score: 2.5}}; !entriesEqual(got, want) {
		t.Fatalf("widget state after load = %+v, want %+v", got, want)
	}
	if got, want := gadgets.state(), []entry{{ID: 7, Name: "g", Score: 0.25}}; !entriesEqual(got, want) {
		t.Fatalf("gadget state after load = %+v, want %+v", got, want)
	}
}

// TestAC2_ReloadReproducesExactState proves loading a checkpoint
// reconstructs participant state field-by-field identical to what was live
// at creation — never the later-mutated live state (AC-2, GR#12).
func TestAC2_ReloadReproducesExactState(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "snapshot", Score: 3.0})
	m := NewManager(root, []save.Participant{widgets}, "corr-ac2")

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "cp", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	// Mutate the live world after the checkpoint.
	widgets.setState(entry{ID: 2, Name: "later", Score: 99})

	if _, _, err := m.Load("cp"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "snapshot", Score: 3.0}}; !entriesEqual(got, want) {
		t.Fatalf("state after load = %+v, want the pre-mutation snapshot %+v", got, want)
	}
}

// TestAC3_RevertForksNeverOverwrites builds A→{B,C}, reverts to A, and
// asserts the abandoned branch is left fully intact while a new, distinct
// branch identity is created (AC-3).
func TestAC3_RevertForksNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget")
	m := NewManager(root, []save.Participant{widgets}, "corr-ac3")

	widgets.setState(entry{ID: 1, Name: "state-A"})
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	widgets.setState(entry{ID: 2, Name: "state-B"})
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	widgets.setState(entry{ID: 3, Name: "state-C"})
	if _, err := m.CreateCheckpoint(fixtureContext(30, 1), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}

	d, err := m.Revert(fixtureContext(40, 1), "A")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if d.ID == "" || d.ID == "B" || d.ID == "C" || d.ID == "A" {
		t.Fatalf("revert produced a non-distinct branch identity %q", d.ID)
	}
	if d.ParentID != "A" {
		t.Fatalf("revert branch parent = %q, want A", d.ParentID)
	}

	// B and C are still present and independently loadable, each
	// reconstructing the exact state it held before the revert.
	if _, _, err := m.Load("B"); err != nil {
		t.Fatalf("Load B after revert: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 2, Name: "state-B"}}; !entriesEqual(got, want) {
		t.Fatalf("B reconstructed %+v, want %+v", got, want)
	}
	if _, _, err := m.Load("C"); err != nil {
		t.Fatalf("Load C after revert: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 3, Name: "state-C"}}; !entriesEqual(got, want) {
		t.Fatalf("C reconstructed %+v, want %+v", got, want)
	}

	// Reverting restored A's state into the live participants, and the new
	// active head is the fork D.
	if id, _ := m.CurrentID(); id != d.ID {
		t.Fatalf("active head after revert = %q, want the fork %q", id, d.ID)
	}
	if _, _, err := m.Load("A"); err != nil {
		t.Fatalf("Load A: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "state-A"}}; !entriesEqual(got, want) {
		t.Fatalf("A reconstructed %+v, want %+v", got, want)
	}
}

// TestAC4_ParentageExplicitNotInferred records parentage as an explicit
// typed field and proves it is not derived from creation-time ordering: two
// siblings are created with ticks out of order and still report the correct
// parent (AC-4).
func TestAC4_ParentageExplicitNotInferred(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac4")

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	// B created first (tick 20), C created second but with a LOWER tick (15)
	// — parentage must come from the explicit parent, not tick ordering.
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(15, 1), "C", "A"); err != nil {
		t.Fatalf("CreateCheckpoint C: %v", err)
	}
	if _, err := m.Revert(fixtureContext(30, 1), "A"); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	byID := map[ID]Checkpoint{}
	for _, n := range tree.Nodes {
		byID[n.ID] = n
	}
	if byID["B"].ParentID != "A" || byID["C"].ParentID != "A" {
		t.Fatalf("parentage wrong: B.parent=%q C.parent=%q, want A/A", byID["B"].ParentID, byID["C"].ParentID)
	}
	// The revert-created fork's parent is also A.
	var fork *Checkpoint
	for _, n := range tree.Nodes {
		if n.ID != "A" && n.ID != "B" && n.ID != "C" {
			cp := n
			fork = &cp
		}
	}
	if fork == nil || fork.ParentID != "A" {
		t.Fatalf("fork parentage = %+v, want a fork with parent A", fork)
	}
}

// TestAC16_CurrentIDReadOnly verifies the read-only active-head accessor:
// it reports the active checkpoint and never mutates checkpoint state
// (AC-16).
func TestAC16_CurrentIDReadOnly(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-ac16")

	id, err := m.CurrentID()
	if err != nil {
		t.Fatalf("CurrentID on fresh root: %v", err)
	}
	if id != "" {
		t.Fatalf("CurrentID on fresh root = %q, want empty", id)
	}

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if id, _ = m.CurrentID(); id != "A" {
		t.Fatalf("CurrentID = %q, want A", id)
	}

	// Reading the accessor must not change the on-disk tree.
	before, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage before: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := m.CurrentID(); err != nil {
			t.Fatalf("CurrentID: %v", err)
		}
	}
	after, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage after: %v", err)
	}
	if len(before.Nodes) != len(after.Nodes) {
		t.Fatalf("CurrentID mutated the tree: before %d nodes, after %d", len(before.Nodes), len(after.Nodes))
	}
}

// TestAC13_ByteDeterminism drives the same fixture through checkpoint
// creation twice into two roots and compares every file's bytes (AC-13).
func TestAC13_ByteDeterminism(t *testing.T) {
	build := func() (root string, m *Manager) {
		root = t.TempDir()
		widgets := newMemParticipant("widget", entry{ID: 1, Name: "w", Score: 1.25})
		gadgets := newMemParticipant("gadget", entry{ID: 9, Name: "g", Score: 0.5})
		m = NewManager(root, []save.Participant{widgets, gadgets}, "corr-ac13")
		return root, m
	}

	rootA, mA := build()
	rootB, mB := build()
	for _, step := range []struct {
		name   string
		parent ID
		tick   int64
	}{
		{"cp1", "", 10},
		{"cp2", "cp1", 20},
	} {
		if _, err := mA.CreateCheckpoint(fixtureContext(step.tick, step.tick/10), step.name, step.parent); err != nil {
			t.Fatalf("rootA CreateCheckpoint %s: %v", step.name, err)
		}
		if _, err := mB.CreateCheckpoint(fixtureContext(step.tick, step.tick/10), step.name, step.parent); err != nil {
			t.Fatalf("rootB CreateCheckpoint %s: %v", step.name, err)
		}
	}

	filesA := snapshotTree(t, rootA)
	filesB := snapshotTree(t, rootB)
	if len(filesA) != len(filesB) {
		t.Fatalf("file count mismatch: %d vs %d (%v vs %v)", len(filesA), len(filesB), keysOf(filesA), keysOf(filesB))
	}
	for path, data := range filesA {
		other, ok := filesB[path]
		if !ok {
			t.Fatalf("file %s present in rootA but absent in rootB", path)
		}
		if string(data) != string(other) {
			t.Fatalf("file %s differs between the two deterministic checkpoints", path)
		}
	}
}

// snapshotTree walks root and returns every file's bytes keyed by its
// root-relative path, excluding this package's transient staging/pruning
// directories (which are never part of the final discoverable tree).
func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func keysOf(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- boundary / error-path tests (GR#1, GR#7) ---

func TestCreateCheckpointRejectsInvalidID(t *testing.T) {
	m := NewManager(t.TempDir(), nil, "corr")
	for _, name := range []string{"", ".", "..", "../evil", "a/b", `a\b`, "a:b", "trailing."} {
		if _, err := m.CreateCheckpoint(fixtureContext(1, 1), name, ""); !errors.Is(err, &errs.E{Code: ErrInvalidCheckpointID}) {
			t.Fatalf("CreateCheckpoint(%q) error = %v, want ErrInvalidCheckpointID", name, err)
		}
	}
}

func TestCreateCheckpointRejectsDanglingParent(t *testing.T) {
	m := NewManager(t.TempDir(), []save.Participant{newMemParticipant("w")}, "corr")
	if _, err := m.CreateCheckpoint(fixtureContext(1, 1), "cp", "does-not-exist"); !errors.Is(err, &errs.E{Code: ErrParentNotFound}) {
		t.Fatalf("CreateCheckpoint with dangling parent error = %v, want ErrParentNotFound", err)
	}
}

func TestRevertRejectsNonCheckpoint(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("w")}, "corr")

	// A non-existent ID is not a checkpoint.
	if _, err := m.Revert(fixtureContext(1, 1), "ghost"); !errors.Is(err, &errs.E{Code: ErrNotACheckpoint}) {
		t.Fatalf("Revert(nonexistent) error = %v, want ErrNotACheckpoint", err)
	}

	// A real manual save (no checkpoint-meta.json) is also not a checkpoint.
	sm := save.NewManager(root, []save.Participant{newMemParticipant("w")}, "corr")
	if err := sm.SaveManual(fixtureContext(1, 1), "plain-save"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	if _, err := m.Revert(fixtureContext(2, 2), "plain-save"); !errors.Is(err, &errs.E{Code: ErrNotACheckpoint}) {
		t.Fatalf("Revert(manual-save) error = %v, want ErrNotACheckpoint", err)
	}
}

func TestSetMaxRetainedForksRejectsNegative(t *testing.T) {
	m := NewManager(t.TempDir(), nil, "corr")
	if err := m.SetMaxRetainedForks(-1); !errors.Is(err, &errs.E{Code: ErrInvalidForkConfig}) {
		t.Fatalf("SetMaxRetainedForks(-1) error = %v, want ErrInvalidForkConfig", err)
	}
}

func TestManagerCopied(t *testing.T) {
	m := NewManager(t.TempDir(), nil, "corr")
	copied := managerByteCopy(m)
	if _, err := copied.CreateCheckpoint(fixtureContext(1, 1), "cp", ""); !errors.Is(err, &errs.E{Code: ErrCheckpointCopied}) {
		t.Fatalf("copied Manager CreateCheckpoint error = %v, want ErrCheckpointCopied", err)
	}
	if _, err := copied.CurrentID(); !errors.Is(err, &errs.E{Code: ErrCheckpointCopied}) {
		t.Fatalf("copied Manager CurrentID error = %v, want ErrCheckpointCopied", err)
	}
}

// managerByteCopy performs the SEC-020-class struct-copy attack via a raw
// byte-for-byte memcpy through unsafe.Pointer, mirroring feat.saveux's
// managerByteCopy / registryByteCopy: a literal `m2 := *m` is legal Go but
// go vet's copylocks check statically flags it, which the baseline requires
// to pass. The byte copy produces identical runtime semantics (mu bytes
// copied as-is, saveMgr pointer copied, self pointer bytes copied
// unchanged) without a statically-flaggable copy expression.
func managerByteCopy(m *Manager) *Manager {
	c := new(Manager)
	*(*[unsafe.Sizeof(Manager{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Manager{})]byte)(unsafe.Pointer(m))
	return c
}

// blockingParticipant holds a SaveManual's Source open until released, so a
// test can hold one CreateCheckpoint in flight deterministically and assert
// the second is rejected (single-checkpoint-in-flight guard).
type blockingParticipant struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingParticipant) Kind() string { return "block" }

func (b *blockingParticipant) Source() serialize.RecordSource {
	close(b.started)
	<-b.release
	return func() (serialize.Record, bool, error) { return serialize.Record{}, false, nil }
}

func (b *blockingParticipant) Handler() serialize.RecordHandler {
	return func(serialize.Record) error { return nil }
}

func TestCreateCheckpointRejectsConcurrent(t *testing.T) {
	bp := &blockingParticipant{started: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(t.TempDir(), []save.Participant{bp}, "corr")

	done := make(chan error, 1)
	go func() {
		_, err := m.CreateCheckpoint(fixtureContext(1, 1), "cp1", "")
		done <- err
	}()
	<-bp.started

	if _, err := m.CreateCheckpoint(fixtureContext(2, 2), "cp2", ""); !errors.Is(err, &errs.E{Code: ErrCheckpointInProgress}) {
		t.Fatalf("concurrent CreateCheckpoint error = %v, want ErrCheckpointInProgress", err)
	}
	close(bp.release)
	if err := <-done; err != nil {
		t.Fatalf("first CreateCheckpoint errored after release: %v", err)
	}
}
