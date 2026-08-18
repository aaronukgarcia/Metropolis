package integration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is the disk half of the T1 tier's hybrid memory+disk FIFO
// (queue.go's tierQueue): one segment file per spilled command, named by
// its monotonic per-tier sequence number, written via the SAME
// staging-directory-then-os.Rename pattern internal/engine/save uses
// (save/bundle.go's newStagingDir + the "atomic promotion" doc in
// save/doc.go) so a segment is only ever discoverable at its final path
// once it is completely and correctly written. That is what makes
// queue_test.go's torn-write test meaningful: a segment file can only
// exist at segmentPath(root, seq) as the product of a single atomic
// rename of fully-written bytes, so a reader (readSegment) that finds a
// file there and still fails to decode it has found genuine corruption —
// something outside this package's own writer touched it — never a
// window where a partially-flushed write is legitimately "still in
// progress."
const (
	spillStagingSubdir = ".spill-staging"
	segmentFileExt     = ".cmd"
	// segmentSeqWidth is generous for an int64 sequence — 20 digits
	// covers the full int64 range with room to spare, and a fixed width
	// keeps directory listings lexically sorted in sequence order for
	// any forensic/manual inspection (not relied on for correctness —
	// this package always addresses segments by their exact numeric
	// seq, never by listing the directory).
	segmentSeqWidth = 20
)

// segmentDir returns the directory a tier's promoted (fully-written)
// overflow segment files live in, directly under root.
func segmentDir(root string) string {
	return root
}

// spillStagingDir returns the directory writeSegment stages an
// in-progress segment write in before promoting it via os.Rename — never
// scanned by readSegment/removeSegment, mirroring save/bundle.go's
// stagingRoot.
func spillStagingDir(root string) string {
	return filepath.Join(root, spillStagingSubdir)
}

// segmentPath returns the final, discoverable path of the overflow
// segment for sequence seq under root.
func segmentPath(root string, seq int64) string {
	return filepath.Join(segmentDir(root), fmt.Sprintf("%0*d%s", segmentSeqWidth, seq, segmentFileExt))
}

// writeSegment atomically writes cmd's encoded form to its seq's segment
// file under root: encode -> write to a fresh temp file under
// root/.spill-staging -> fsync -> close -> os.Rename into
// segmentPath(root, seq). Any failure along the way removes the
// half-written temp file (best-effort) and returns a registry error
// (ErrSpillDirCreateFailed or ErrSpillWriteFailed) — the caller (tierQueue.
// enqueueLocked) treats any non-nil return as "not queued at all," never
// as a partially-queued command, which is what keeps the per-tier
// sequence counter consistent with what is actually durable.
func writeSegment(root string, seq int64, cmd protocol.Command, correlationID string) error {
	staging := spillStagingDir(root)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return errs.Wrap(ErrSpillDirCreateFailed, correlationID, err, map[string]any{"root": root, "cause": err.Error()})
	}

	data, err := protocol.EncodeCommand(cmd)
	if err != nil {
		// EncodeCommand only fails on a cmd whose Payload cannot be
		// JSON-marshalled — cmd has already passed Command.Validate by
		// the time enqueueLocked calls us (queue.go), so this is
		// effectively unreachable for any payload registered in
		// commands.go, but is still handled explicitly rather than
		// ignored (GR#1).
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}

	tmp, err := os.CreateTemp(staging, "spill-*")
	if err != nil {
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}

	finalPath := segmentPath(root, seq)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrSpillWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	return nil
}

// readSegment reads and decodes the overflow segment for sequence seq
// under root. Any failure — the file missing, unreadable, or failing
// protocol.DecodeCommand — is reported as ErrSpillReadFailed; a segment
// is NEVER treated as validly decoded unless DecodeCommand succeeds in
// full (see this file's header comment on why a corrupt/torn segment can
// only mean external interference, never a legitimate in-progress
// write).
func readSegment(root string, seq int64, correlationID string) (protocol.Command, error) {
	path := segmentPath(root, seq)
	data, err := os.ReadFile(path)
	if err != nil {
		return protocol.Command{}, errs.Wrap(ErrSpillReadFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	cmd, err := protocol.DecodeCommand(data)
	if err != nil {
		return protocol.Command{}, errs.Wrap(ErrSpillReadFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	return cmd, nil
}

// removeSegment deletes the overflow segment for sequence seq under
// root, once its command has been successfully handed to the inner
// transport (tierQueue.commitLocked). Best-effort, like save/bundle.go's
// reapDisplacedSiblings: a leftover segment file after a successful send
// is disk-usage clutter, not a correctness hazard — this package always
// addresses segments by nextDrainSeq's exact numeric sequence, never by
// listing the directory, so a stray unremoved file can never be
// re-delivered or mistaken for a pending one.
func removeSegment(root string, seq int64) {
	_ = os.Remove(segmentPath(root, seq))
}
