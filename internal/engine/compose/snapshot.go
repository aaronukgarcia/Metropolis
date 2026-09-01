package compose

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc3 — durable SNAPSHOT CADENCE + snapshot-aware
// restore. This file is the payoff of FEAT-1972079943 (Composition.Save/Load
// round-trip the full StateDigest byte-exact) and FEAT-1972079944
// (Composition.LoadAt seeds the clock so a snapshot restore is genuinely
// tick-continuous, not a frozen-at-tick-0 state blob): together they make it
// safe to restore from "latest snapshot + replay the journal tail" instead
// of always replaying the entire journal from genesis.
//
// # Design (Aaron's 2026-08-31 19:01 ruling on the BOW item)
//
//  1. CADENCE: every SnapshotCadenceTicks (N=360 == 1 simulated year, since
//     core.DailyTicksPerMonth==30 and a year is 12 months) a snapshot is
//     durably written via persist.Store.PutSnapshot. N is a placeholder per
//     the balance-number regime (Aaron retunes later) — it is a single
//     named constant, never a magic literal (mirrors
//     internal/engine/checkpoint's MaxRetainedForks convention).
//  2. JOURNAL RETENTION: the FULL journal is always kept. A snapshot is a
//     restore-SPEED optimization, not a replacement for the journal — the
//     genesis-replay path (RestoreCommands, already landed inc2) keeps
//     working forever, which is what GR#27's before/after comparison and
//     the Phase-3 convergence A/B parity gate both depend on.
//  3. RESTORE: GetSnapshot(latest) -> Composition.LoadAt(root, snapshotTick)
//     -> replay ONLY the journal commands recorded AFTER snapshotTick. Falls
//     back to a full genesis replay when no snapshot exists yet.
//
// # Snapshot payload shape
//
// A snapshot payload is exactly what Composition.Save already writes (a
// directory of save-bundle files), zipped into one opaque []byte so it fits
// persist.Store's []byte-shaped PutSnapshot/GetSnapshot. This deliberately
// reuses Save/Load's own on-disk bundle format as the single source of
// truth for "what a snapshot contains" (GR#3) rather than inventing a
// second serialization — the snapshot's recorded tick is read back from the
// bundle's own save.SaveSummary.CreatedAtTick (Save's save.Context already
// stamps this at save time, see save_wire.go's Save method), so no
// additional envelope/header is needed.

// SnapshotCadenceTicks is the number of engine ticks between durable
// snapshots (PLACEHOLDER, Aaron's 2026-08-31 ruling: N=360, one simulated
// year). A single named constant, never re-derived ad hoc at each call
// site, so retuning it later (balance-number regime) is a one-line change.
const SnapshotCadenceTicks int64 = 12 * core.DailyTicksPerMonth

// MaxRetainedSnapshots bounds how many durable snapshots MaybeSnapshot
// keeps for one city — mirrors internal/engine/checkpoint's
// MaxRetainedForks bounded-retention pattern (the Phase 1 inc3 design
// ruling explicitly calls for reusing it). Pruning NEVER touches the
// journal — only snapshots (a restore-speed optimization) are bounded;
// the full journal is always kept (this increment's ruling, "journal
// retention: KEEP THE FULL JOURNAL for Phase 1"). A single named
// constant, not a magic literal, so retuning it later is a one-line
// change.
const MaxRetainedSnapshots = 5

// ShouldSnapshot reports whether tick is a snapshot-cadence boundary: a
// strictly positive multiple of SnapshotCadenceTicks. Tick 0 (a freshly
// wired, never-ticked engine) is deliberately excluded — there is nothing
// meaningful to snapshot before the first tick, and a tick-0 snapshot would
// be indistinguishable from "no snapshot yet" for splitJournalAtTick's
// snapshotTick<=0 fast path below.
func ShouldSnapshot(tick int64) bool {
	return tick > 0 && tick%SnapshotCadenceTicks == 0
}

// MaybeSnapshot writes a durable snapshot for city via store IF the
// composition's current engine tick is a cadence boundary (ShouldSnapshot);
// otherwise it is a fast, allocation-free no-op (ok=false, no error). This
// is the entry point a driving loop (a headless harness, or the metroserve
// tick loop once inc4 wires it) calls after every AdvanceTicks: cheap to
// call unconditionally every tick, since the common case is just one
// int64 modulo.
func (c *Composition) MaybeSnapshot(ctx context.Context, store persist.Store, city persist.CityKey) (id persist.SnapshotID, ok bool, err error) {
	clock, err := c.state.e.Clock()
	if err != nil {
		return "", false, errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "snapshot", "step": "clock"})
	}
	tick := clock.Tick()
	if !ShouldSnapshot(tick) {
		return "", false, nil
	}
	data, err := c.buildSnapshotBytes()
	if err != nil {
		return "", false, err
	}
	id, err = store.PutSnapshot(ctx, city, data)
	if err != nil {
		// store.PutSnapshot's own error is already a durable-Store-level
		// failure (mirrors persistCommandJournaler.ObserveCommand, which
		// likewise returns a raw AppendJournal error unwrapped) — surfaced
		// as-is rather than re-wrapped under a compose-specific code.
		return "", false, err
	}
	// Prune down to MaxRetainedSnapshots — best-effort bounded retention,
	// same shape as checkpoint.Manager's abandoned-branch pruning. The new
	// snapshot just written is always kept (it is the newest); pruning
	// failures are surfaced (never swallowed — an unbounded, ever-growing
	// snapshot set is exactly the storage-cost problem this bound exists
	// to prevent), but the snapshot itself was already durably written and
	// PutSnapshot's success is not undone by a later prune failure.
	if err := pruneSnapshots(ctx, store, city); err != nil {
		return id, true, err
	}
	return id, true, nil
}

// pruneSnapshots deletes the oldest snapshots for city beyond
// MaxRetainedSnapshots (ListSnapshots' documented oldest-first order makes
// "the excess prefix" exactly the snapshots to remove). A no-op when the
// count is already within bound.
func pruneSnapshots(ctx context.Context, store persist.Store, city persist.CityKey) error {
	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		return err
	}
	if len(ids) <= MaxRetainedSnapshots {
		return nil
	}
	excess := ids[:len(ids)-MaxRetainedSnapshots]
	for _, id := range excess {
		if err := store.DeleteSnapshot(ctx, city, id); err != nil {
			return err
		}
	}
	return nil
}

// buildSnapshotBytes serializes the composition's CURRENT state (via
// Composition.Save, unchanged) into a temp directory, then packs that
// directory into a single opaque []byte payload. The temp directory is
// always removed before returning, success or failure.
func (c *Composition) buildSnapshotBytes() ([]byte, error) {
	dir, err := os.MkdirTemp("", "metropolis-snapshot-*")
	if err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "mkdtemp"})
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := c.Save(dir); err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "save"})
	}
	data, err := zipDir(dir)
	if err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "zip"})
	}
	return data, nil
}

// restoreFromSnapshotBytes unpacks a snapshot payload previously produced
// by buildSnapshotBytes into a temp directory, locates the composition
// save bundle inside it, and restores c via LoadAt at the tick that bundle
// was saved at (save.SaveSummary.CreatedAtTick — the SAME field
// Composition.Save stamps via save.Context.CreatedAtTick, see
// save_wire.go). Returns the restored tick.
func (c *Composition) restoreFromSnapshotBytes(data []byte) (int64, error) {
	dir, err := os.MkdirTemp("", "metropolis-restore-*")
	if err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "mkdtemp"})
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := unzipDir(data, dir); err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "unzip"})
	}
	summaries, _, err := save.List(dir)
	if err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "list"})
	}
	tick := int64(-1)
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			tick = s.CreatedAtTick
			break
		}
	}
	if tick < 0 {
		return 0, errs.New(ErrSnapshotUnpackFailed, c.state.cid, map[string]any{
			"step": "locate",
		})
	}
	if err := c.LoadAt(dir, tick); err != nil {
		return 0, err // LoadAt already wraps with its own compose error context.
	}
	return tick, nil
}

// RestoreLatestSnapshotOrGenesis restores composition c — which MUST be a
// freshly Wired, never-yet-ticked Composition over e (the same precondition
// LoadAt itself carries, since a snapshot restore ends in a LoadAt call) —
// from store's durable records for city:
//
//   - If store holds at least one snapshot, restore is
//     "latest snapshot + replay the journal tail" (this increment's payoff):
//     GetSnapshot the newest one, LoadAt it (state + clock, tick-continuous),
//     then replay only the journal commands recorded strictly AFTER that
//     snapshot's tick (splitJournalAtTick below).
//   - If store holds no snapshot yet, restore falls back to the existing
//     genesis-replay path: every durably journaled command replayed from
//     tick 0 (RestoreCommands, inc2).
//
// Returns whether a snapshot was used (false == genesis fallback) and the
// final restored tick (read back from the engine's own clock after replay,
// since a tail can itself contain further AdvanceTicks commands that move
// the tick past the snapshot's own recorded value).
func RestoreLatestSnapshotOrGenesis(ctx context.Context, e *core.Engine, c *Composition, store persist.Store, city persist.CityKey) (usedSnapshot bool, tick int64, err error) {
	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		return false, 0, err
	}
	cmds, err := RestoreCommands(ctx, store, city)
	if err != nil {
		return false, 0, err
	}

	if len(ids) == 0 {
		if err := replayCommands(e, cmds); err != nil {
			return false, 0, err
		}
		finalTick, err := engineTick(e, c.state.cid)
		if err != nil {
			return false, 0, err
		}
		return false, finalTick, nil
	}

	// ListSnapshots is documented to return IDs oldest-first (deterministic
	// ascending order) — the last element is the newest snapshot.
	latest := ids[len(ids)-1]
	data, err := store.GetSnapshot(ctx, city, latest)
	if err != nil {
		return false, 0, err
	}
	snapTick, err := c.restoreFromSnapshotBytes(data)
	if err != nil {
		return false, 0, err
	}
	tail, err := splitJournalAtTick(cmds, snapTick, city, c.state.cid)
	if err != nil {
		return false, 0, err
	}
	if err := replayCommands(e, tail); err != nil {
		return false, 0, err
	}
	finalTick, err := engineTick(e, c.state.cid)
	if err != nil {
		return false, 0, err
	}
	return true, finalTick, nil
}

// engineTick is a tiny helper so RestoreLatestSnapshotOrGenesis's two
// success paths share one clock-read-and-wrap call site.
func engineTick(e *core.Engine, correlationID string) (int64, error) {
	clock, err := e.Clock()
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "snapshot", "step": "final-clock"})
	}
	return clock.Tick(), nil
}

// replayCommands applies cmds to e in order via e.HandleCommand — the SAME
// command path a live client took — surfacing the first rejection as a
// fatal restore error rather than silently skipping it (a silently-dropped
// journal command is restore-side data loss, exactly the class GR#27 and
// this whole epic exist to kill).
func replayCommands(e *core.Engine, cmds []protocol.Command) error {
	for _, cmd := range cmds {
		res := e.HandleCommand(cmd)
		if !res.Accepted {
			return errs.New(ErrSnapshotTailReplayRejected, string(cmd.CorrelationID), map[string]any{
				"kind":  string(cmd.Kind),
				"error": fmt.Sprintf("%v", res.Error),
			})
		}
	}
	return nil
}

// splitJournalAtTick returns the suffix of cmds (in the same order) that
// were journaled AFTER the engine reached snapshotTick — i.e. exactly the
// commands a snapshot-aware restore still needs to replay on top of a
// LoadAt(snapshotTick) restore, because everything before that point is
// already captured in the snapshot's own state.
//
// snapshotTick<=0 is a degenerate case (should not occur — ShouldSnapshot
// never fires at tick<=0 — but is handled honestly rather than assumed
// away): the entire journal is returned as the tail.
//
// The tick counter only ever advances via protocol.KindAdvanceTicks
// commands (core.Engine.AdvanceTicks is the only mutator of the clock, and
// it is reached from the command path via handleAdvanceTicks — see
// core/commands.go), so this walks the journal tracking a running tick
// total and returns everything after the point that total reaches
// snapshotTick. A KindAdvanceTicks command whose N would carry the running
// total PAST snapshotTick (rather than landing exactly on it — possible
// when a caller advances many ticks in one command, e.g. driving a whole
// month at once while the cadence boundary falls mid-batch) is split: the
// portion that reaches snapshotTick is already captured by the snapshot and
// is dropped, and a synthetic AdvanceTicks command carrying only the
// REMAINDER is replayed in its place, so the tail still advances the clock
// by exactly the same total amount the original journal would have.
func splitJournalAtTick(cmds []protocol.Command, snapshotTick int64, city persist.CityKey, correlationID string) ([]protocol.Command, error) {
	if snapshotTick <= 0 {
		return cmds, nil
	}
	cityLabel := city.TenantID + "/" + city.CityID
	running := int64(0)
	for i, cmd := range cmds {
		if cmd.Kind != protocol.KindAdvanceTicks {
			continue
		}
		payload, ok := cmd.Payload.(protocol.AdvanceTicksPayload)
		if !ok {
			return nil, errs.New(ErrSnapshotTailShort, correlationID, map[string]any{
				"city": cityLabel,
				"tick": snapshotTick,
			})
		}
		switch {
		case running+payload.N < snapshotTick:
			running += payload.N
		case running+payload.N == snapshotTick:
			tail := make([]protocol.Command, len(cmds[i+1:]))
			copy(tail, cmds[i+1:])
			return tail, nil
		default: // running+payload.N > snapshotTick: this command straddles the boundary.
			remainder := running + payload.N - snapshotTick
			remCmd := cmd
			remCmd.Payload = protocol.AdvanceTicksPayload{N: remainder}
			tail := make([]protocol.Command, 0, len(cmds)-i)
			tail = append(tail, remCmd)
			tail = append(tail, cmds[i+1:]...)
			return tail, nil
		}
	}
	// Walked the whole journal without the running tick total ever reaching
	// snapshotTick: the journal and the snapshot are out of sync (a corrupt
	// Store, or a snapshot written against a different city's journal).
	return nil, errs.New(ErrSnapshotTailShort, correlationID, map[string]any{
		"city": cityLabel,
		"tick": snapshotTick,
	})
}

// zipDir packs every regular file under dir into a single in-memory zip
// archive, using forward-slash-normalized paths relative to dir as entry
// names (so the archive is portable across OSes) in filepath.Walk's
// lexical order (deterministic — GR#21 — never map/directory-listing
// order).
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(w, f)
		return err
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unzipDir extracts a zip archive built by zipDir into dir (which must
// already exist). Every entry name is defended against zip-slip (a ".."
// segment or an absolute path escaping dir) — a hostile/corrupt snapshot
// payload can never write outside the target directory.
func unzipDir(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		clean := filepath.Clean(f.Name)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return fmt.Errorf("compose: unsafe snapshot zip entry %q", f.Name)
		}
		target := filepath.Join(dir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractZipFile copies one zip.File's content to target, closing both the
// zip reader and the destination file even on a copy error (isolated into
// its own function so unzipDir's loop body needs no defer-in-loop).
func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, rc)
	return err
}
