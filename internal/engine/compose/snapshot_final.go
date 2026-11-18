package compose

import (
	"context"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1197 — the FINAL snapshot: a durable snapshot written when a hosted
// city stops (SIGTERM, idle eviction), at whatever tick the city reached
// rather than only at a cadence boundary, so the next wake replays a
// zero-length tail instead of up to a cadence's worth of ticks.
//
// # Skip reasons (never a worse restore than not snapshotting)
//
//   - tick-zero: nothing to capture beyond genesis.
//   - journal-dirty: the BUG-480 latch (MET-G813): a snapshot after a lost
//     append can never be proven tail-consistent.
//   - persist-halted: the engine halted on a journal failure (BUG-472).
//   - trailing-command: the journal's LAST record is not an AdvanceTicks.
//     Restore splits the journal at the snapshot's tick and replays what
//     follows the AdvanceTicks that reaches it; a command journaled after
//     that point is already inside a final snapshot, so it would be applied
//     twice. A cadence snapshot cannot hit this (it is taken inside the
//     tick's own result), a stop-time one can (a player command between two
//     ticks). Skipping keeps the previous snapshot + a longer tail, which
//     restores exactly.
//   - partial-tick: the engine's tick differs from the total the durable
//     journal's AdvanceTicks records cover. An AdvanceTicks(N>1) that fails
//     mid-loop leaves the engine at running+k ticks while the command, which
//     is journaled only on acceptance, is NOT in the journal; a snapshot at
//     that tick could never be reconciled (splitJournalAtTick: ErrSnapshotTailShort).
//     Skipping keeps the previous snapshot + the journal, no worse than before.
//   - deadline: the context expired before the snapshot was durable.
//
// # Deadline semantics
//
// The build and the write run on one goroutine; FinalSnapshot returns as
// soon as ctx is done. That goroutine re-checks ctx before PutSnapshot, so
// a snapshot that finishes building after the deadline is never written.
// PutSnapshot is temp-file-then-rename, so a snapshot cut off mid-write (or
// a process killed mid-write) leaves only an unreferenced temp file; the
// previous snapshot stays the latest. FinalSnapshotResult.Done closes when
// that goroutine has fully finished: a caller that keeps using the process
// (idle eviction) must wait on it before Close()-ing the composition; a
// caller about to exit need not.

// FinalSnapshotResult reports what FinalSnapshot did.
type FinalSnapshotResult struct {
	// ID is the written snapshot's ID when Wrote is true.
	ID persist.SnapshotID
	// Wrote is true when a snapshot became durable.
	Wrote bool
	// Tick is the engine tick the attempt was made at.
	Tick int64
	// SkipReason is non-empty when no snapshot was attempted or it was
	// abandoned; one of the reasons in the package comment above.
	SkipReason string
	// Done is closed when any background work FinalSnapshot started has
	// finished (already closed when none was started).
	Done <-chan struct{}
}

// Skip reasons reported in FinalSnapshotResult.SkipReason.
const (
	FinalSkipTickZero        = "tick-zero"
	FinalSkipJournalDirty    = "journal-dirty"
	FinalSkipPersistHalted   = "persist-halted"
	FinalSkipTrailingCommand = "trailing-command"
	FinalSkipPartialTick     = "partial-tick"
	FinalSkipDeadline        = "deadline"
)

// FinalSnapshot writes a durable snapshot of the composition's current state
// bounded by ctx's deadline. The caller must have quiesced the engine (no
// command or tick in flight) first. It never writes a partial snapshot as
// the latest and never errors on a skip; err is only a genuine build/write
// failure (the previous snapshot is untouched in that case too).
func (c *Composition) FinalSnapshot(ctx context.Context, store persist.Store, city persist.CityKey) (FinalSnapshotResult, error) {
	closed := make(chan struct{})
	close(closed)
	res := FinalSnapshotResult{Done: closed}

	clock, err := c.state.e.Clock()
	if err != nil {
		return res, errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "snapshot", "step": "clock"})
	}
	res.Tick = clock.Tick()
	if res.Tick <= 0 {
		res.SkipReason = FinalSkipTickZero
		return res, nil
	}
	if dj, isDirty := c.Journaler().(dirtyJournaler); isDirty && dj.Dirty() {
		if dj.MarkDirtyLoggedOnce() {
			_ = errs.New(ErrSnapshotRefusedDirty, c.state.cid, map[string]any{
				"city": city.TenantID + "/" + city.CityID,
				"tick": res.Tick,
			})
		}
		res.SkipReason = FinalSkipJournalDirty
		return res, nil
	}
	if _, _, halted := c.state.e.PersistHalted(); halted {
		res.SkipReason = FinalSkipPersistHalted
		return res, nil
	}
	if err := ctx.Err(); err != nil {
		res.SkipReason = FinalSkipDeadline
		return res, nil
	}
	trailing, journalTicks, err := journalTailState(ctx, store, city)
	if err != nil {
		if ctx.Err() != nil {
			res.SkipReason = FinalSkipDeadline
			return res, nil
		}
		return res, err
	}
	if trailing {
		res.SkipReason = FinalSkipTrailingCommand
		return res, nil
	}
	if journalTicks != res.Tick {
		res.SkipReason = FinalSkipPartialTick
		return res, nil
	}

	type outcome struct {
		id  persist.SnapshotID
		err error
	}
	done := make(chan struct{})
	out := make(chan outcome, 1)
	go func() {
		defer close(done)
		data, berr := c.buildSnapshotBytes()
		if berr != nil {
			out <- outcome{err: berr}
			return
		}
		if c.finalBuildHook != nil {
			c.finalBuildHook()
		}
		// Deadline gate: a snapshot that finished building late is dropped,
		// never written.
		if ctx.Err() != nil {
			out <- outcome{err: ctx.Err()}
			return
		}
		id, perr := store.PutSnapshot(ctx, city, data)
		if perr == nil {
			if pe := pruneSnapshots(context.Background(), store, city); pe != nil {
				// The snapshot is durable; a prune failure only leaves an
				// extra old snapshot. Not an abandon.
				_ = pe
			}
		}
		out <- outcome{id: id, err: perr}
	}()
	res.Done = done

	select {
	case o := <-out:
		if o.err != nil {
			if ctx.Err() != nil {
				res.SkipReason = FinalSkipDeadline
				return res, nil
			}
			return res, o.err
		}
		res.ID, res.Wrote = o.id, true
		return res, nil
	case <-ctx.Done():
		// The goroutine may still finish its write (rename is atomic); if
		// it already won the race, report the durable result truthfully.
		select {
		case o := <-out:
			if o.err == nil {
				res.ID, res.Wrote = o.id, true
				return res, nil
			}
		default:
		}
		res.SkipReason = FinalSkipDeadline
		return res, nil
	}
}

// journalTailState reads the durable journal once and reports whether its
// last record is a command other than AdvanceTicks (the trailing-command skip
// reason; an empty journal at tick > 0 counts as off-tick: there is no tick
// record the snapshot could be reconciled against) and the total ticks its
// AdvanceTicks records cover (the partial-tick check).
func journalTailState(ctx context.Context, store persist.Store, city persist.CityKey) (trailing bool, ticks int64, err error) {
	recs, err := store.ReadJournal(ctx, city)
	if err != nil {
		return false, 0, err
	}
	if len(recs) == 0 {
		return true, 0, nil
	}
	for i, rec := range recs {
		// BUG-1224 r2: a live frame is a rule-version envelope since rule 1;
		// only the envelope-aware decoder reads both shapes.
		frame, derr := decodeJournalFrame(rec, i, len(recs))
		if derr != nil {
			return false, 0, derr
		}
		cmd := frame.Command
		if cmd.Kind != protocol.KindAdvanceTicks {
			if i == len(recs)-1 {
				trailing = true
			}
			continue
		}
		p, ok := cmd.Payload.(protocol.AdvanceTicksPayload)
		if !ok {
			return false, 0, errs.New(ErrSnapshotTailShort, "", map[string]any{
				"city": city.TenantID + "/" + city.CityID,
				"tick": ticks,
			})
		}
		ticks += p.N
	}
	return trailing, ticks, nil
}
