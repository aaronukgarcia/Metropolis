package save

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// SaveManual writes a manual save (US-1, AC-3) named name. Manual saves
// are never pruned. Returns ErrSaveInProgress (AC-11) if another save is
// already in flight on this Manager.
func (m *Manager) SaveManual(ctx Context, name string) error {
	meta := Meta{SaveKind: KindManual, DisplayName: name}
	return m.writeBundle(ctx, manualDir(m.root, name), meta)
}

// Autosave writes one rolling yearly autosave (US-2, AC-4) and then
// prunes the oldest autosave(s) beyond the 10-slot retention window —
// ONLY after the new autosave has been written and promoted
// successfully (AC-4/AC-13's ordering guarantee; see writeBundle and
// doc.go's "Atomic promotion" section). Callers MUST invoke this off
// the simulation's own year-boundary tick/event, never a wall-clock
// timer (AC-15) — this package has no way to enforce that from inside
// itself, since it takes no clock/scheduler dependency at all.
func (m *Manager) Autosave(ctx Context) error {
	seq, err := nextAutosaveSeq(m.root)
	if err != nil {
		return errs.Wrap(ErrListFailed, m.correlationID, err, map[string]any{"root": m.root, "dir": m.root, "cause": "computing next autosave sequence: " + err.Error()})
	}
	meta := Meta{SaveKind: KindAutosave, DisplayName: autosaveDisplayName(seq)}
	if err := m.writeBundle(ctx, autosaveDir(m.root, seq), meta); err != nil {
		return err
	}
	return m.pruneAutosaves()
}

func autosaveDisplayName(seq int) string {
	return "Autosave #" + strconv.Itoa(seq)
}

// Milestone writes a milestone save (US-3, AC-5) tagged with tier, the
// §4 population-tier this crossing belongs to. Milestone saves are
// never pruned by Autosave's retention rotation. This package does not
// itself detect the crossing — see doc.go's "Milestone-trigger linkage"
// section; the caller (the future engine.unlocks/engine.core wiring)
// supplies tier once it has determined a crossing occurred.
func (m *Manager) Milestone(ctx Context, tier Tier) error {
	meta := Meta{
		SaveKind:            KindMilestone,
		DisplayName:         tier.Name,
		MilestoneTierNumber: tier.Number,
		MilestoneTierName:   tier.Name,
	}
	return m.writeBundle(ctx, milestoneDir(m.root, tier), meta)
}

// writeBundle is the shared save path for SaveManual/Autosave/Milestone
// (AC-3/AC-4/AC-5 all funnel through here): it stages a full bundle
// under root/.staging, streams every registered Participant's records
// through serialize.WriteShard (AC-7 — no []serialize.Record
// accumulation), writes the header and this package's own Meta sidecar,
// runs serialize.ValidateBundle on the STAGED bundle, and only then
// promotes it (os.Rename) to finalDir (AC-9). Any failure before
// promotion leaves finalDir completely untouched and removes the
// staging directory — nothing already-promoted is ever affected
// (AC-13's failure-path guarantee falls out of this ordering).
//
// At most one writeBundle call runs at a time per *Manager (AC-11): a
// concurrent call finding mu already held returns ErrSaveInProgress
// immediately rather than blocking, queuing, or interleaving shard
// writes with the in-flight save — the observable, typed outcome AC-11
// requires (this package's chosen answer to that AC's deliberately-left-
// open queue-vs-reject question, ASM #5: reject, because it makes the
// concurrency test's assertions unambiguous and never silently delays a
// player-triggered manual save behind a scheduled autosave).
func (m *Manager) writeBundle(ctx Context, finalDir string, meta Meta) error {
	if !m.mu.TryLock() {
		return errs.New(ErrSaveInProgress, m.correlationID, map[string]any{"finalDir": finalDir})
	}
	defer m.mu.Unlock()

	stagingDir, err := newStagingDir(m.root, m.correlationID)
	if err != nil {
		return err
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	// newStagingDir (via os.MkdirTemp) already created stagingDir itself
	// uniquely, so serialize.CreateBundleDir's own "must not already
	// exist" guard (appropriate for a caller-NAMED final directory)
	// would reject it here; only the shards/ subdirectory still needs
	// creating.
	if err := os.MkdirAll(serialize.ShardsDir(stagingDir), 0o755); err != nil {
		return errs.Wrap(ErrStagingCreateFailed, m.correlationID, err, map[string]any{"root": m.root, "stagingDir": stagingDir, "cause": err.Error()})
	}

	header := serialize.NewHeader(ctx.WorldSeed, ctx.CreatedAtTick, ctx.GameMonth, ctx.AppVersion)
	if ctx.DebugTouched {
		header.TouchDebug()
	}

	ser := serialize.NDJSONSerializer{}
	for _, p := range m.participants {
		shardMeta := serialize.ShardMeta{Name: p.Kind(), Kind: p.Kind(), Encoding: "ndjson+gzip"}
		f, err := serialize.CreateShardWriter(stagingDir, shardMeta)
		if err != nil {
			return errs.Wrap(ErrParticipantWriteFailed, m.correlationID, err, map[string]any{"kind": p.Kind(), "cause": "creating shard writer"})
		}
		writtenMeta, writeErr := ser.WriteShard(f, shardMeta, p.Source())
		closeErr := f.Close()
		if writeErr != nil {
			return errs.Wrap(ErrParticipantWriteFailed, m.correlationID, writeErr, map[string]any{"kind": p.Kind(), "cause": "writing shard"})
		}
		if closeErr != nil {
			return errs.Wrap(ErrParticipantWriteFailed, m.correlationID, closeErr, map[string]any{"kind": p.Kind(), "cause": "closing shard file"})
		}
		header.ShardIndex = append(header.ShardIndex, writtenMeta)
	}

	if err := serialize.WriteHeader(stagingDir, header); err != nil {
		return errs.Wrap(ErrHeaderWriteFailed, m.correlationID, err, map[string]any{"stagingDir": stagingDir, "cause": err.Error()})
	}
	if err := WriteMeta(stagingDir, meta); err != nil {
		return errs.Wrap(ErrMetaWriteFailed, m.correlationID, err, map[string]any{"stagingDir": stagingDir, "cause": err.Error()})
	}

	if _, err := serialize.ValidateBundle(stagingDir); err != nil {
		return errs.Wrap(ErrStagedValidationFailed, m.correlationID, err, map[string]any{"stagingDir": stagingDir, "cause": err.Error()})
	}

	if err := ensureParentDir(finalDir); err != nil {
		return errs.Wrap(ErrPromotionFailed, m.correlationID, err, map[string]any{"finalDir": finalDir, "cause": "creating parent directory: " + err.Error()})
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return errs.Wrap(ErrPromotionFailed, m.correlationID, err, map[string]any{"stagingDir": stagingDir, "finalDir": finalDir, "cause": err.Error()})
	}
	promoted = true
	return nil
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
