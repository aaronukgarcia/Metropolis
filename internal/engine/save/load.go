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
//
// opts customises this call (BUG-479): pass WithExpectedWorldSeed(seed)
// to refuse the bundle with ErrSaveSeedMismatch unless its header's
// WorldSeed equals seed, and AllowSeedMismatch() alongside it to permit
// a deliberate reseed instead of refusing. With no opts at all, Load's
// behaviour is byte-for-byte the pre-BUG-479 one: no seed check.
func (m *Manager) Load(dir string, opts ...LoadOption) (serialize.Header, Meta, error) {
	lo := resolveLoadOptions(opts)

	// SEC-020-class: identity check before touching any field — see
	// checkNotCopied's doc comment (manager.go).
	if err := m.checkNotCopied(map[string]any{"method": "Load", "dir": dir}); err != nil {
		return serialize.Header{}, Meta{}, err
	}
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

	// BUG-479: refuse a bundle whose WorldSeed does not match the
	// loading composition's own seed, BEFORE any participant Handler
	// runs (the shard-load loop below is the earliest point any
	// participant state changes, and it starts strictly after this
	// check) — so a refused load leaves every Participant, and
	// therefore the whole composition, untouched. Skipped entirely
	// unless the caller opted in via WithExpectedWorldSeed; never
	// skipped silently once opted in, unless AllowSeedMismatch was also
	// passed (the deliberate-reseed escape hatch).
	if lo.checkWorldSeed && header.WorldSeed != lo.expectedWorldSeed && !lo.allowMismatch {
		return serialize.Header{}, Meta{}, errs.New(ErrSaveSeedMismatch, m.correlationID, map[string]any{
			"dir":             dir,
			"bundleSeed":      header.WorldSeed,
			"compositionSeed": lo.expectedWorldSeed,
		})
	}

	meta, err := ReadMeta(dir)
	if err != nil {
		return serialize.Header{}, Meta{}, errs.Wrap(ErrMetaReadFailed, m.correlationID, err, map[string]any{"dir": dir, "cause": err.Error()})
	}

	// FEAT-143 AC-5: refuse a bundle whose declared game mode does not
	// match the loading session's own locked mode, BEFORE any participant
	// Handler runs (the shard-load loop below is the earliest point any
	// participant state changes) -- so a refused load leaves every
	// Participant, and therefore the whole composition, untouched. Skipped
	// entirely unless the caller opted in via WithExpectedGameMode.
	// meta.GameMode == "" (a pre-FEAT-143 bundle, or a hand-edited one
	// with no mode) is treated as a mismatch against ANY expected mode --
	// never silently accepted as a match, and never defaulted to
	// "unlimited" (the false-pass this AC explicitly rules out).
	//
	// The round's finding (P2-A): lo.expectedGameMode == "" must ALSO
	// always refuse, even against a bundle whose own meta.GameMode is
	// itself "" (a legacy save) -- otherwise WithExpectedGameMode("")
	// is an escape hatch that silently performs no real check at all.
	// This is reachable in production because GameModeWire() (this
	// package's caller-supplied expected value) returns "" whenever the
	// SEC-020 copy-guard trips on a copied *GameInit, so an empty
	// expected mode must never be treated as "matches an empty bundle
	// mode" -- it is a caller error, not a legitimate expectation, and
	// AC-5 requires fail-closed here exactly as for a real mismatch.
	if lo.checkGameMode && (lo.expectedGameMode == "" || meta.GameMode != lo.expectedGameMode) {
		return serialize.Header{}, Meta{}, errs.New(ErrGameModeMismatch, m.correlationID, map[string]any{
			"dir":             dir,
			"bundleGameMode":  meta.GameMode,
			"sessionGameMode": lo.expectedGameMode,
		})
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
//
// opts forwards unchanged to the internal per-candidate m.Load call
// (BUG-485), exactly like Load's own opts parameter: pass
// WithExpectedWorldSeed(seed) so a candidate autosave whose header
// WorldSeed does not match the caller's own composition seed is treated
// as a failed-to-load candidate (ErrSaveSeedMismatch), skipped like any
// other corrupt bundle, rather than silently becoming "the latest valid
// save" and restoring a foreign trajectory. Omitting opts entirely
// preserves LoadLatest's pre-BUG-485 behaviour byte-for-byte: no seed
// check on any candidate, matching Load's own zero-option default.
func (m *Manager) LoadLatest(opts ...LoadOption) (serialize.Header, Meta, []SkipInfo, error) {
	// SEC-020-class: identity check before touching any field — see
	// checkNotCopied's doc comment (manager.go).
	if err := m.checkNotCopied(map[string]any{"method": "LoadLatest"}); err != nil {
		return serialize.Header{}, Meta{}, nil, err
	}
	seqs, err := listAutosaveSeqs(m.root)
	if err != nil {
		return serialize.Header{}, Meta{}, nil, errs.Wrap(ErrListFailed, m.correlationID, err, map[string]any{"root": m.root, "dir": m.root, "cause": err.Error()})
	}
	sort.Sort(sort.Reverse(sort.IntSlice(seqs)))

	var skipped []SkipInfo
	for _, seq := range seqs {
		dir := autosaveDir(m.root, seq)
		header, meta, err := m.Load(dir, opts...)
		if err != nil {
			skipped = append(skipped, SkipInfo{Path: dir, Reason: err})
			continue
		}
		return header, meta, skipped, nil
	}
	return serialize.Header{}, Meta{}, skipped, errs.New(ErrNoValidSaveFound, m.correlationID, map[string]any{"root": m.root, "candidates": fmt.Sprint(len(seqs))})
}
