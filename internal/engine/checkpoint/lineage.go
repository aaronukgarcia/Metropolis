package checkpoint

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ID is a stable checkpoint/branch identifier. It is a single clean path
// component (validated via serialize.ValidateShardName) and is also the
// on-disk directory name of the checkpoint's bundle under the checkpoint
// root's manual/ subdirectory.
type ID string

// Checkpoint is the metadata for one checkpoint in the lineage tree,
// sourced entirely from metadata files (header.json, save-meta.json,
// checkpoint-meta.json) — never from a shard file (AC-5).
type Checkpoint struct {
	// ID is the checkpoint's identifier (also its bundle directory name).
	ID ID

	// ParentID is the immediate parent checkpoint (AC-4); empty for a root
	// checkpoint.
	ParentID ID

	// CreatedAtTick is the simulation tick the checkpoint was taken at
	// (from serialize.Header).
	CreatedAtTick int64

	// GameMonth is the in-world calendar month at checkpoint time (from
	// serialize.Header).
	GameMonth int64

	// DisplayName is the human-readable name (from feat.saveux's
	// save-meta.json).
	DisplayName string

	// Active reports whether this checkpoint is the currently-active head.
	Active bool
}

// Node is one checkpoint in the navigable lineage tree, with its children
// (AC-10: a tree structure, not a flat list a UI must re-derive).
type Node struct {
	Checkpoint Checkpoint
	Children   []*Node
}

// Tree is the lineage tree Lineage returns: a flat, deterministically
// ordered list of every checkpoint (AC-5), plus the navigable root(s)
// whose Children links form the parent→children structure a save-UI
// screen can render directly (AC-10).
type Tree struct {
	// Nodes is every checkpoint, ordered deterministically (CreatedAtTick
	// ascending, then ID ascending).
	Nodes []Checkpoint

	// Roots are the checkpoint(s) with no recorded parent, navigable via
	// Children. A well-formed tree has exactly one; a tree whose parent
	// chain was partially pruned or hand-edited may surface a checkpoint
	// as an additional root rather than hiding it.
	Roots []*Node
}

// Lineage enumerates every checkpoint under root and returns its lineage
// tree (AC-5). It reads only metadata files — header.json (via
// serialize.ReadHeader), save-meta.json (via save.ReadMeta), and this
// package's checkpoint-meta.json — never a shard file, mirroring
// feat.saveux AC-8's "never opens a shard file to answer this" discipline.
//
// A directory that is not a checkpoint (no checkpoint-meta.json) or whose
// metadata cannot be read is skipped rather than failing the whole call —
// one bad entry must not hide every other checkpoint. A filesystem-level
// failure enumerating manual/ itself is returned as ErrLineageFailed.
//
// The Active flag is sourced from the root-level active-head pointer; a
// missing or unreadable pointer degrades to "no active head" rather than
// failing the enumeration.
func Lineage(root string) (Tree, error) {
	manualDir := filepath.Join(root, manualSubdir)
	entries, err := os.ReadDir(manualDir)
	if os.IsNotExist(err) {
		return Tree{}, nil
	}
	if err != nil {
		return Tree{}, errs.Wrap(ErrLineageFailed, "", err, map[string]any{"root": root, "dir": manualDir, "cause": err.Error()})
	}

	activeID := ID("")
	if head, herr := readHead(root); herr == nil {
		activeID = head.ActiveID
	}

	nodes := make([]Checkpoint, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := ID(e.Name())
		cp, rerr := readCheckpointSummary(filepath.Join(manualDir, e.Name()), id, activeID)
		if rerr != nil {
			continue
		}
		nodes = append(nodes, cp)
	}
	sortCheckpoints(nodes)

	return buildTree(nodes), nil
}

// readCheckpointSummary reads one checkpoint's metadata-only summary,
// mirroring feat.saveux's readSummary but additionally reading this
// package's checkpoint-meta.json for parentage. A directory without a
// checkpoint-meta.json is not a checkpoint and returns an error.
func readCheckpointSummary(dir string, id ID, activeID ID) (Checkpoint, error) {
	parent, err := readCheckpointMeta(dir)
	if err != nil {
		return Checkpoint{}, err
	}
	header, err := serialize.ReadHeader(dir)
	if err != nil {
		return Checkpoint{}, err
	}
	meta, err := save.ReadMeta(dir)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		ID:            id,
		ParentID:      parent,
		CreatedAtTick: header.CreatedAtTick,
		GameMonth:     header.GameMonth,
		DisplayName:   meta.DisplayName,
		Active:        id == activeID,
	}, nil
}

// sortCheckpoints orders checkpoints deterministically: CreatedAtTick
// ascending, then ID ascending (GR#21 — the same input always yields the
// same order).
func sortCheckpoints(nodes []Checkpoint) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].CreatedAtTick != nodes[j].CreatedAtTick {
			return nodes[i].CreatedAtTick < nodes[j].CreatedAtTick
		}
		return string(nodes[i].ID) < string(nodes[j].ID)
	})
}

// buildTree assembles the navigable tree from a sorted flat node list.
// Parentage comes from each checkpoint's explicit ParentID (AC-4), never
// from the sort order. A checkpoint whose parent is not present (pruned or
// hand-edited away) is surfaced as an additional root rather than dropped.
func buildTree(nodes []Checkpoint) Tree {
	byID := make(map[ID]*Node, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &Node{Checkpoint: nodes[i]}
	}

	roots := make([]*Node, 0, 1)
	for i := range nodes {
		node := byID[nodes[i].ID]
		if nodes[i].ParentID == "" {
			roots = append(roots, node)
			continue
		}
		if parent, ok := byID[nodes[i].ParentID]; ok {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	// nodes is sorted, so roots and each node's Children are already in
	// the same deterministic order; the sort here is belt-and-braces.
	sort.Slice(roots, func(i, j int) bool {
		a, b := roots[i].Checkpoint, roots[j].Checkpoint
		if a.CreatedAtTick != b.CreatedAtTick {
			return a.CreatedAtTick < b.CreatedAtTick
		}
		return string(a.ID) < string(b.ID)
	})

	return Tree{Nodes: nodes, Roots: roots}
}

// pruneRename and pruneRemove are os.Rename/os.RemoveAll indirections,
// swapped by tests to force a mid-prune failure (AC-9's "no branch left
// half-deleted" check). They are never swapped in production code.
var (
	pruneRename = os.Rename
	pruneRemove = os.RemoveAll
)

// prune removes abandoned branches beyond MaxRetainedForks (AC-6/AC-7):
// it computes the retained set — the active head, the N most-recently-
// abandoned branch heads, and every ancestor of a retained node — and
// stages every other checkpoint out of the discoverable tree. It is two-
// phase (AC-9): first every prunable directory is renamed into the
// .pruning area (the atomic visibility commit), and only after all renames
// succeed are they deleted. A rename failure rolls every already-staged
// directory back, leaving the retained set exactly as it was. The returned
// error is non-fatal to the caller: CreateCheckpoint/Revert surface it via
// LastPruneError rather than failing the already-promoted create/revert
// (SEC-190).
func (m *Manager) prune() error {
	tree, err := Lineage(m.root)
	if err != nil {
		return err
	}
	prunable := computePrunable(tree.Nodes, m.maxRetainedForks)
	if len(prunable) == 0 {
		return nil
	}

	pruningRoot := filepath.Join(m.root, pruningSubdir)
	// Reap debris from a prior prune whose delete phase failed, so the
	// staging names below are fresh (best-effort — never fails the prune).
	_ = os.RemoveAll(pruningRoot)
	if err := os.MkdirAll(pruningRoot, 0o755); err != nil {
		return errs.Wrap(ErrPruneFailed, m.correlationID, err, map[string]any{"pruningRoot": pruningRoot, "cause": err.Error()})
	}

	type staged struct{ from, to string }
	stagedDirs := make([]staged, 0, len(prunable))
	for _, id := range prunable {
		from := checkpointDir(m.root, id)
		to := filepath.Join(pruningRoot, string(id))
		if err := pruneRename(from, to); err != nil {
			// Roll back every already-staged rename, newest first, so the
			// discoverable tree is exactly what it was before the attempt.
			for i := len(stagedDirs) - 1; i >= 0; i-- {
				_ = pruneRename(stagedDirs[i].to, stagedDirs[i].from)
			}
			return errs.Wrap(ErrPruneFailed, m.correlationID, err, map[string]any{"id": id, "cause": err.Error()})
		}
		stagedDirs = append(stagedDirs, staged{from: from, to: to})
	}

	// Delete phase: best-effort. The directories are already out of the
	// discoverable tree; a failure here leaves debris under .pruning that
	// the next prune reaps.
	for _, s := range stagedDirs {
		_ = pruneRemove(s.to)
	}
	return nil
}

// computePrunable returns the checkpoint IDs to prune, in deterministic
// (CreatedAtTick ascending, then ID) order. It keeps the active head, the
// maxRetained most-recently-abandoned branch heads, and every ancestor of
// a retained node (AC-8: an old checkpoint that is still an ancestor of a
// retained branch is never pruned).
func computePrunable(nodes []Checkpoint, maxRetained int) []ID {
	if len(nodes) == 0 || maxRetained < 0 {
		return nil
	}

	parentOf := make(map[ID]ID, len(nodes))
	childCount := make(map[ID]int, len(nodes))
	active := ID("")
	for _, cp := range nodes {
		parentOf[cp.ID] = cp.ParentID
		if cp.Active {
			active = cp.ID
		}
	}
	for _, cp := range nodes {
		if cp.ParentID != "" {
			childCount[cp.ParentID]++
		}
	}

	// Abandoned branches are the non-active leaves (checkpoints with no
	// children). The active head is always the most recently created
	// checkpoint and therefore always a leaf.
	abandoned := make([]Checkpoint, 0)
	for _, cp := range nodes {
		if childCount[cp.ID] == 0 && cp.ID != active {
			abandoned = append(abandoned, cp)
		}
	}
	// Most-recent first: highest CreatedAtTick, then highest ID as a
	// deterministic tiebreak.
	sort.Slice(abandoned, func(i, j int) bool {
		if abandoned[i].CreatedAtTick != abandoned[j].CreatedAtTick {
			return abandoned[i].CreatedAtTick > abandoned[j].CreatedAtTick
		}
		return string(abandoned[i].ID) > string(abandoned[j].ID)
	})

	// Retained = active head + the maxRetained most-recent abandoned heads
	// + every ancestor of a retained node.
	retained := make(map[ID]bool, len(nodes))
	retained[active] = true
	keep := abandoned
	if len(abandoned) > maxRetained {
		keep = abandoned[:maxRetained]
	}
	for _, cp := range keep {
		retained[cp.ID] = true
	}
	for id := range retained {
		for {
			p, ok := parentOf[id]
			if !ok || p == "" {
				break
			}
			if retained[p] {
				break
			}
			retained[p] = true
			id = p
		}
	}

	prunable := make([]ID, 0)
	for _, cp := range nodes { // nodes is sorted ascending, so prunable is too
		if !retained[cp.ID] {
			prunable = append(prunable, cp.ID)
		}
	}
	return prunable
}
