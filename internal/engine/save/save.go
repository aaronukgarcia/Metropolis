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
//
// BUG-159: name is the one manual-save input that comes straight from
// the player (or an external caller) with zero prior sanitization —
// Autosave derives its own name from a runtime-computed sequence number
// and Milestone from a fixed Tier, so neither can ever collide with
// bundle.go's internal ".replaced-stage-<random>" crash-recovery marker
// (BUG-158). SaveManual is therefore the ONLY entry point that needs
// this check, and it runs BEFORE the name is ever joined into a path
// (manualDir) or reaches any filesystem call — a colliding name is
// rejected outright rather than silently becoming permanently invisible
// to List (isReplacedSiblingName) or, worse, causing a later save to an
// unrelated prefix-matching slot to glob-match and delete it
// (reapDisplacedSiblings).
func (m *Manager) SaveManual(ctx Context, name string) error {
	// SEC-020-class: identity check before touching any field — see
	// checkNotCopied's doc comment (manager.go).
	if err := m.checkNotCopied(map[string]any{"method": "SaveManual", "name": name}); err != nil {
		return err
	}
	// BUG-160/BUG-161: reject a name that is unsafe to join, unmodified,
	// into a filesystem path (path separators, ".."/"." components, an
	// absolute or drive-letter/UNC path, a NUL byte), or is otherwise
	// degenerate input that should never reach real filesystem I/O
	// (any other C0 control character, an empty-or-whitespace-only name,
	// an overlong name) BEFORE the marker-collision check below and
	// BEFORE name is ever joined into manualDir or reaches any
	// filesystem call — closing both the arbitrary-directory-write gap
	// isReservedSaveName's substring check does nothing to stop (".."
	// contains no reserved-marker text) and BUG-161's late-failing raw
	// OS error gap.
	if isUnsafeSaveName(name) {
		return errs.New(ErrUnsafeSaveName, m.correlationID, map[string]any{"name": name})
	}
	if isReservedSaveName(name) {
		return errs.New(ErrReservedSaveName, m.correlationID, map[string]any{"name": name})
	}
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
// BUG-158: seq allocation (nextAutosaveSeq) MUST happen under the same
// m.mu that guards writeBundle's own body — not before it. Autosave used
// to compute seq outside the lock, then call the lock-taking writeBundle
// separately: a TOCTOU gap in which two overlapping Autosave calls could
// both observe the same "next" seq (neither had written anything to
// disk yet) and both proceed to writeBundle with the same finalDir — the
// second one's ordinary save-over path would then silently displace and
// delete the first's already-promoted, real autosave data, returning nil
// to the first caller. Autosave now takes m.mu itself, computes seq
// while holding it, and calls writeBundleLocked directly (bypassing
// writeBundle's own TryLock, which would otherwise deadlock against the
// lock Autosave is already holding) so seq allocation and the write it
// gates are atomic with respect to every other SaveManual/Autosave/
// Milestone call on this Manager.
func (m *Manager) Autosave(ctx Context) error {
	// SEC-020-class: identity check BEFORE m.mu is ever touched — see
	// checkNotCopied's doc comment (manager.go) for why a copy must
	// never attempt to acquire its own mu.
	if err := m.checkNotCopied(map[string]any{"method": "Autosave"}); err != nil {
		return err
	}
	if !m.mu.TryLock() {
		return errs.New(ErrSaveInProgress, m.correlationID, map[string]any{"finalDir": autosaveSubdir})
	}
	defer m.mu.Unlock()

	seq, err := nextAutosaveSeq(m.root)
	if err != nil {
		return errs.Wrap(ErrListFailed, m.correlationID, err, map[string]any{"root": m.root, "dir": m.root, "cause": "computing next autosave sequence: " + err.Error()})
	}
	meta := Meta{SaveKind: KindAutosave, DisplayName: autosaveDisplayName(seq)}
	if err := m.writeBundleLocked(ctx, autosaveDir(m.root, seq), meta); err != nil {
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
	// SEC-020-class: identity check before touching any field — see
	// checkNotCopied's doc comment (manager.go).
	if err := m.checkNotCopied(map[string]any{"method": "Milestone"}); err != nil {
		return err
	}
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
//
// BUG-157: writeBundle IS the real, production save-over path (the gap
// SEC-027 fixed one layer up in engine.core's Snapshot for a call path
// that never actually performs save-overs in production). Before
// building the new header, it checks whether finalDir already holds a
// promoted bundle (os.Stat — the same "does a save already exist at
// this discoverable name" question SaveManual/Autosave/Milestone
// implicitly answer just by calling writeBundle again with the same
// finalDir: manual/<name> and milestone/<tier> are stable names re-used
// on every re-save, and Autosave always computes a FRESH never-before-
// used seq via nextAutosaveSeq, so finalDir existing here can only ever
// mean a manual or milestone save-over, never a first-time autosave).
// When it does, it reads that prior bundle's on-disk header.json via
// serialize.ReadHeader (never the full bundle — writeBundle has no need
// for the prior shards) and merges its DebugTouched flag forward via
// serialize.Header.MergeDebugTouched (OR-merge, sticky — SEC-024)
// BEFORE the new header is written, mirroring SEC-027's fix pattern in
// engine.core's Snapshot exactly (GR#3). A finalDir that does not yet
// exist is a genuine first-time save: no prior header exists to read,
// and the freshly-built header's DebugTouched starts false exactly as
// before this fix.
func (m *Manager) writeBundle(ctx Context, finalDir string, meta Meta) error {
	// SEC-020-class: identity check BEFORE m.mu is ever touched — see
	// checkNotCopied's doc comment (manager.go) for why a copy must
	// never attempt to acquire its own mu.
	if err := m.checkNotCopied(map[string]any{"method": "writeBundle", "finalDir": finalDir}); err != nil {
		return err
	}
	if !m.mu.TryLock() {
		return errs.New(ErrSaveInProgress, m.correlationID, map[string]any{"finalDir": finalDir})
	}
	defer m.mu.Unlock()
	return m.writeBundleLocked(ctx, finalDir, meta)
}

// writeBundleLocked is writeBundle's actual body, factored out so a
// caller that must allocate a name/seq under the SAME critical section
// as the write itself (BUG-158's Autosave fix) can do so without
// deadlocking against writeBundle's own TryLock. Every caller MUST
// already hold m.mu before calling this — it does no locking of its own.
func (m *Manager) writeBundleLocked(ctx Context, finalDir string, meta Meta) error {
	// SEC-020-class: defence-in-depth identity check. Every documented
	// caller (writeBundle, Autosave) already checks checkNotCopied
	// before ever taking m.mu, so this is belt-and-braces against a
	// future caller that forgets to — see checkNotCopied's doc comment
	// (manager.go).
	if err := m.checkNotCopied(map[string]any{"method": "writeBundleLocked", "finalDir": finalDir}); err != nil {
		return err
	}
	// BUG-158: reap any stray ".replaced-stage-<random>" sibling left
	// over from a PRIOR writeBundle call to this same finalDir that
	// crashed between displacing the old bundle and promoting the new
	// one. This is the only reap point every save-to-this-slot path
	// (SaveManual/Autosave/Milestone) funnels through, so it doubles as
	// both "clean up after myself if I crash next time" prevention (the
	// stray this call itself might leave is handled by the ordinary
	// displace/promote/remove sequence below) and the sweep that clears
	// out damage from a PAST crash — best-effort, never fails the save.
	reapDisplacedSiblings(finalDir)

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

	// BUG-157/SEC-024/SEC-027: finalDir already existing means this call
	// is a save-over of a previously-promoted bundle (see the doc
	// comment above for why that's the only way this can be true) — read
	// its on-disk header and carry DebugTouched forward before this new
	// header is ever written, so a previously debug-touched save can
	// never come back clean through this, the real production save-over
	// path.
	isSaveOver := false
	if _, statErr := os.Stat(finalDir); statErr == nil {
		isSaveOver = true
		priorHeader, readErr := serialize.ReadHeader(finalDir)
		if readErr != nil {
			return errs.Wrap(ErrPriorHeaderReadFailed, m.correlationID, readErr, map[string]any{"finalDir": finalDir, "cause": readErr.Error()})
		}
		header.MergeDebugTouched(priorHeader.DebugTouched())
	} else if !os.IsNotExist(statErr) {
		return errs.Wrap(ErrPriorHeaderReadFailed, m.correlationID, statErr, map[string]any{"finalDir": finalDir, "cause": "checking for a prior save at finalDir: " + statErr.Error()})
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

	// BUG-157: a plain os.Rename(stagingDir, finalDir) fails on every OS
	// this package targets when finalDir already exists and is
	// non-empty (POSIX rename(2) requires an empty target directory;
	// Windows returns "Access is denied" for the same reason) — which is
	// exactly the save-over case isSaveOver detects above. To actually
	// deliver the save-over SaveManual/Milestone promise (not just the
	// DebugTouched merge above, which would otherwise never get
	// exercised by a real promoted overwrite), a save-over displaces the
	// prior promoted bundle to a same-parent sibling first, promotes the
	// new staged bundle into finalDir, and only then removes the
	// displaced sibling — so a crash between the two renames leaves
	// EITHER the old bundle (moved back) or the new one at finalDir,
	// never neither (mirrors AC-13's "nothing already-promoted is ever
	// affected by a failure" guarantee for the save-over case too).
	var displacedDir string
	if isSaveOver {
		displacedDir = finalDir + ".replaced-" + filepath.Base(stagingDir)
		if err := os.Rename(finalDir, displacedDir); err != nil {
			return errs.Wrap(ErrPromotionFailed, m.correlationID, err, map[string]any{"finalDir": finalDir, "displacedDir": displacedDir, "cause": "displacing prior save-over bundle: " + err.Error()})
		}
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		if displacedDir != "" {
			_ = os.Rename(displacedDir, finalDir)
		}
		return errs.Wrap(ErrPromotionFailed, m.correlationID, err, map[string]any{"stagingDir": stagingDir, "finalDir": finalDir, "cause": err.Error()})
	}
	promoted = true
	if displacedDir != "" {
		_ = os.RemoveAll(displacedDir)
	}
	return nil
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
