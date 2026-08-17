package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Directory layout under a checkpoint root (which is also a feat.saveux
// save root — see doc.go). Checkpoints are stored as manual saves, so
// each checkpoint bundle lives at <root>/manual/<id>/ (header.json +
// shards/ + save-meta.json, written by feat.saveux's SaveManual), plus
// this package's own checkpoint-meta.json lineage sidecar in that same
// directory. The active-head pointer is a single root-level file, so a
// revert updates exactly one file and never mutates any checkpoint
// bundle (AC-3: the abandoned branch is left fully intact).
const (
	// manualSubdir mirrors feat.saveux's own manual-save subdirectory
	// ("manual"), where SaveManual promotes every bundle. Duplicated here
	// as a literal because feat.saveux does not export it; a drift test
	// (TestManualSubdirMirrorsSaveLayout) asserts this stays in step with
	// feat.saveux's real on-disk behaviour.
	manualSubdir = "manual"

	// checkpointMetaFileName is this package's own lineage sidecar file,
	// distinct from int.serializer's header.json and feat.saveux's
	// save-meta.json.
	checkpointMetaFileName = "checkpoint-meta.json"

	// headFileName is the root-level active-head pointer file.
	headFileName = "checkpoint-head.json"

	// pruningSubdir is where pruned branch directories are staged during
	// the rename phase of pruning, outside the manual/ tree Lineage walks.
	pruningSubdir = ".pruning"
)

// forkNamePrefix is the literal separator-and-marker prepended to a
// target's name when deriving a fork identifier: forkName(target, seq)
// renders "<target>.fork<seq>". A single named constant (GR#3) so the
// fork-name length budget below and the renderer can never disagree.
const forkNamePrefix = ".fork"

// maxForkSeqDigits is the number of decimal digits in the largest int64
// (19). forkName renders the persisted fork counter (an int64 advanced
// with saturating arithmetic, GR#16), so reserving this many digits for
// the sequence is what guarantees a fork name never outgrows its target's
// domain no matter how large the counter gets.
const maxForkSeqDigits = len("9223372036854775807") // 19

// maxSaveNameLen is the longest manual-save name feat.saveux's SaveManual
// accepts (255 bytes — save.maxSaveNameLen, duplicated here because it is
// unexported; the drift test TestMaxCheckpointNameLenTracksSaveManualLimit
// asserts this stays in step with save's real limit). It is the single
// named source of the length domain every derived identifier in this
// package is bounded against (GR#3): maxCheckpointNameLen (the create-at
// bound below) and nextFreeForkName's fork-name bound (SEC-196) both
// reference it, never a re-typed 255.
const maxSaveNameLen = 255

// maxCheckpointNameLen is the longest checkpoint name CreateCheckpoint
// accepts. It is maxSaveNameLen minus the fork-name suffix budget
// forkNamePrefix + maxForkSeqDigits. The bound makes the create-at domain
// (what CreateCheckpoint accepts) and the revert-at domain (what Revert's
// SaveManual accepts for the derived fork name) agree (SEC-189): every
// creatable checkpoint is revertible at ANY fork sequence. It bounds only
// the single-revert case; SEC-196 closed the fork-of-fork chain in
// nextFreeForkName by bounding the derived fork name against maxSaveNameLen
// directly.
const maxCheckpointNameLen = maxSaveNameLen - len(forkNamePrefix) - maxForkSeqDigits

// MaxRetainedForks is the number of abandoned (non-active-head) branches
// retained as independently loadable; older abandoned branches auto-prune
// (AC-6). It is a single named constant — every enforcement site refers to
// this name (or a Manager's runtime override via SetMaxRetainedForks),
// never a re-typed literal.
//
// BALANCE PLACEHOLDER: this value is pending Aaron's balance-regime
// approval (placeholder + directional tests + delegated proposal + Aaron's
// row-by-row approval + balance pass). It is asserted nowhere as final;
// tests parameterise the retention rule across several candidate values so
// the shape holds at any N.
const MaxRetainedForks = 5

// checkpointDir returns the bundle directory for checkpoint id under root.
func checkpointDir(root string, id ID) string {
	return filepath.Join(root, manualSubdir, string(id))
}

// checkpointMetaPath returns the lineage sidecar path within a bundle dir.
func checkpointMetaPath(dir string) string {
	return filepath.Join(dir, checkpointMetaFileName)
}

// headPath returns the active-head pointer path for a root.
func headPath(root string) string {
	return filepath.Join(root, headFileName)
}

// checkpointMeta is the wire shape of checkpoint-meta.json. It carries
// only parentage — never a timestamp, random value, or wall-clock field —
// so checkpoint bundles stay byte-deterministic (AC-13).
type checkpointMeta struct {
	// ParentID is the immediate parent checkpoint (AC-4); empty for a root.
	ParentID ID `json:"parentId"`
}

// writeCheckpointMeta writes m as indented JSON to dir/checkpoint-meta.json,
// matching feat.saveux's WriteMeta style (MarshalIndent + trailing newline).
func writeCheckpointMeta(dir string, parent ID) error {
	encoded, err := json.MarshalIndent(checkpointMeta{ParentID: parent}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(checkpointMetaPath(dir), encoded, 0o644)
}

// readCheckpointMeta reads dir/checkpoint-meta.json and returns the
// recorded parent ID. A missing or malformed file is an error — a
// directory without this sidecar is not a checkpoint.
func readCheckpointMeta(dir string) (ID, error) {
	raw, err := os.ReadFile(checkpointMetaPath(dir))
	if err != nil {
		return "", err
	}
	var m checkpointMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	return m.ParentID, nil
}

// headState is the wire shape of checkpoint-head.json: the currently
// active checkpoint and the monotonic fork-name counter.
type headState struct {
	// ActiveID is the identifier of the currently-active checkpoint/branch
	// head. Empty when no checkpoint exists yet.
	ActiveID ID `json:"activeId"`

	// ForkSeq is a monotonic counter used to derive unique, deterministic
	// fork checkpoint names ("<target>.fork<Seq>") on revert.
	ForkSeq int64 `json:"forkSeq"`
}

// readHead reads root's active-head pointer. A missing file yields a zero
// headState (no active head, fork sequence 0) with no error — a fresh root
// is not a failure. A present-but-malformed file is an error.
func readHead(root string) (headState, error) {
	raw, err := os.ReadFile(headPath(root))
	if os.IsNotExist(err) {
		return headState{}, nil
	}
	if err != nil {
		return headState{}, err
	}
	var h headState
	if err := json.Unmarshal(raw, &h); err != nil {
		return headState{}, err
	}
	return h, nil
}

// writeHead atomically writes h to root's active-head pointer: it writes a
// temp file in the same directory and renames it into place, so a crash or
// I/O failure leaves EITHER the old pointer or the new one — never a
// half-written file. The head pointer is the one root-level mutable state a
// revert updates, so its atomicity is what keeps the fork tree and the
// recorded active head from disagreeing after a failed write.
func writeHead(root string, h headState) error {
	encoded, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	tmp, err := os.CreateTemp(root, "checkpoint-head-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup of the temp file on any failure short of the
	// final rename. After a successful rename the temp name no longer
	// exists, so the Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, headPath(root))
}

// validateCheckpointID rejects an identifier that is not a single clean
// path component, reusing serialize.ValidateShardName (the same function
// ShardMeta.Name and harness.replay's fixture names are validated through —
// GR#3, never a reimplementation). An ID becomes a directory name under
// manual/, so it is exactly the hostile-path-component position
// ValidateShardName exists for.
func validateCheckpointID(id ID) error {
	if err := serialize.ValidateShardName(string(id)); err != nil {
		return err
	}
	return nil
}

// isCheckpoint reports whether id names an existing checkpoint under root:
// its bundle directory carries a checkpoint-meta.json sidecar. This is the
// SIDE-CAR-based predicate and stays the gate for parent/revert-target
// validity — a plain manual save must never qualify as a parent or a
// revert target. It is deliberately NOT the namespace-occupancy check for
// name collisions, because the manual/ namespace is shared with
// feat.saveux's SaveManual (see bundleExists).
func isCheckpoint(root string, id ID) bool {
	if id == "" {
		return false
	}
	_, err := os.Stat(checkpointMetaPath(checkpointDir(root, id)))
	return err == nil
}

// bundleExists reports whether id names any existing bundle directory
// under root's manual/ namespace — a checkpoint (with a
// checkpoint-meta.json sidecar) OR a plain manual save (without one). The
// manual/ namespace is shared with feat.saveux's SaveManual (proved by
// TestManualSubdirMirrorsSaveLayout), so a collision check keyed only on
// isCheckpoint's sidecar would miss a same-named manual save and silently
// save-over it (SEC-188). This is the namespace-occupancy check for
// CreateCheckpoint's name-collision and nextFreeForkName's fork-name
// collision; isCheckpoint (sidecar-based) remains the gate for
// parent/revert-target validity.
func bundleExists(root string, id ID) bool {
	if id == "" {
		return false
	}
	_, err := os.Stat(checkpointDir(root, id))
	return err == nil
}

// forkName derives the deterministic, unique checkpoint identifier for a
// fork off target using the monotonic fork sequence (GR#15: derived from
// the persisted head state, never a hardcoded suffix).
func forkName(target ID, seq int64) string {
	return fmt.Sprintf("%s%s%d", target, forkNamePrefix, seq)
}
