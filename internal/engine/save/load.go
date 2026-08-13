package save

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Load reconstructs every registered Participant's state from the
// bundle at dir (US-4, GR#12): it runs serialize.ValidateBundle first
// (catching corruption/truncation before any participant sees a single
// record — AC-10's precondition), then for each shard in the header's
// ShardIndex, resolves the matching Participant by Kind and streams the
// shard's records to that Participant's Handler via
// serialize.NDJSONSerializer.ReadShard (never loading a whole shard
// into memory first — the same streaming contract AC-7 requires on the
// write side).
//
// Returns the bundle's Header and this package's own Meta on success.
// A FormatVersion major mismatch (AC-12) surfaces as
// ErrFormatVersionMismatch, wrapping serialize.CheckFormatVersion's own
// error (raised via serialize.ValidateBundle's internal ReadHeader
// call). Every other ValidateBundle failure (checksum/size mismatch,
// missing header, a shard path that is a directory instead of a file,
// a semantically-bogus header field, ...) surfaces as
// ErrBundleValidationFailed, wrapping ValidateBundle's own error —
// always a registry-sourced *errs.E (GR#7), never the bare underlying
// error.
func (m *Manager) Load(dir string) (serialize.Header, Meta, error) {
	if _, err := os.Stat(dir); err != nil {
		return serialize.Header{}, Meta{}, errs.Wrap(ErrBundleNotFound, m.correlationID, err, map[string]any{"dir": dir, "cause": err.Error()})
	}

	header, err := serialize.ValidateBundle(dir)
	if err != nil {
		if fvErr := checkFormatVersionCause(err); fvErr != nil {
			return serialize.Header{}, Meta{}, errs.Wrap(ErrFormatVersionMismatch, m.correlationID, fvErr, map[string]any{"dir": dir, "cause": fvErr.Error()})
		}
		return serialize.Header{}, Meta{}, errs.Wrap(ErrBundleValidationFailed, m.correlationID, err, map[string]any{"dir": dir, "cause": err.Error()})
	}

	meta, err := ReadMeta(dir)
	if err != nil {
		return serialize.Header{}, Meta{}, errs.Wrap(ErrMetaReadFailed, m.correlationID, err, map[string]any{"dir": dir, "cause": err.Error()})
	}

	byKind := make(map[string]Participant, len(m.participants))
	for _, p := range m.participants {
		byKind[p.Kind()] = p
	}

	ser := serialize.NDJSONSerializer{}
	for _, shardMeta := range header.ShardIndex {
		p, ok := byKind[shardMeta.Kind]
		if !ok {
			return serialize.Header{}, Meta{}, errs.New(ErrUnknownParticipantKind, m.correlationID, map[string]any{"kind": shardMeta.Kind, "dir": dir})
		}
		f, err := serialize.OpenShardReader(dir, shardMeta)
		if err != nil {
			return serialize.Header{}, Meta{}, errs.Wrap(ErrShardReadFailed, m.correlationID, err, map[string]any{"kind": shardMeta.Kind, "cause": "opening shard reader"})
		}
		readErr := ser.ReadShard(f, m.maxDecodedBytes, p.Handler())
		closeErr := f.Close()
		if readErr != nil {
			return serialize.Header{}, Meta{}, errs.Wrap(ErrShardReadFailed, m.correlationID, readErr, map[string]any{"kind": shardMeta.Kind, "cause": "reading shard"})
		}
		if closeErr != nil {
			return serialize.Header{}, Meta{}, errs.Wrap(ErrShardReadFailed, m.correlationID, closeErr, map[string]any{"kind": shardMeta.Kind, "cause": "closing shard reader"})
		}
	}

	return header, meta, nil
}

// checkFormatVersionCause returns err itself when err (or something it
// wraps) originated from serialize.CheckFormatVersion's format-mismatch
// path, or nil otherwise. serialize doesn't export a sentinel for this
// specifically, so this checks the same string CheckFormatVersion's
// error always contains ("is not compatible with this build's format
// major") — brittle only to that package's own wording changing, which
// would be a deliberate, reviewed edit to a well-documented function.
func checkFormatVersionCause(err error) error {
	if err == nil {
		return nil
	}
	const marker = "is not compatible with this build's format major"
	if strings.Contains(err.Error(), marker) {
		return err
	}
	return nil
}

// SkipInfo records one bundle LoadLatest skipped because it failed to
// load, alongside the reason (AC-10 — the skipped entry must be
// reported, not silently absorbed).
type SkipInfo struct {
	Path   string
	Reason error
}

// LoadLatest loads the newest-still-valid autosave bundle under root
// (AC-10, GR#17, mirroring BUG-054's fix one layer up): it walks
// autosave sequence numbers from newest to oldest, skipping any bundle
// that fails to Load (corrupted/truncated) rather than aborting on the
// first bad entry, and returns the first one that DOES load
// successfully together with a record of everything skipped along the
// way. Returns ErrNoValidSaveFound only if every autosave in the
// history failed to load.
func (m *Manager) LoadLatest() (serialize.Header, Meta, []SkipInfo, error) {
	seqs, err := listAutosaveSeqs(m.root)
	if err != nil {
		return serialize.Header{}, Meta{}, nil, errs.Wrap(ErrListFailed, m.correlationID, err, map[string]any{"root": m.root, "dir": m.root, "cause": err.Error()})
	}
	sort.Sort(sort.Reverse(sort.IntSlice(seqs)))

	var skipped []SkipInfo
	for _, seq := range seqs {
		dir := autosaveDir(m.root, seq)
		header, meta, err := m.Load(dir)
		if err != nil {
			skipped = append(skipped, SkipInfo{Path: dir, Reason: err})
			continue
		}
		return header, meta, skipped, nil
	}
	return serialize.Header{}, Meta{}, skipped, errs.New(ErrNoValidSaveFound, m.correlationID, map[string]any{"root": m.root, "candidates": fmt.Sprint(len(seqs))})
}
