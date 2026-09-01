package integration

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/checkpoint"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is INCREMENT 3, part 2 of the Integration Engine (proposal
// §8): CRASH RECOVERY (proposal §1 point 6, §2's "Crash recovery"),
// closing the gap confirmed 2026-08-18: checkpoint.Manager can rewind to
// the last COMPLETED checkpoint, but nothing replays forward from it —
// the harness.replay Recorder that would is in-memory only (ASM-442/470,
// checkpoint/doc.go's "Reuse of feat.saveux and harness.replay" section).
//
// CORRECTED VERSION: the first version of this file reused the T1 tier's
// transient disk overflow spill (queue.go/queue_disk.go) as the durable
// command log and was REJECTED on destructive review for two silent-
// data-loss defects (commit-before-apply race; gap-vs-boundary mid-log
// hole). It is replaced here by wal.go's dedicated, durability-scoped,
// prune-on-checkpoint-only Write-Ahead Log — see wal.go's header comment
// for the full design and exactly how each of those two defects becomes
// structurally impossible. This file's job stays the same: rebuild full
// state from the last checkpoint, then replay whatever the durable log
// still holds beyond it.
//
// # Determinism (GR#21)
//
// Replay order is the WAL's own sequence number, strictly ascending,
// applied one entry at a time with no concurrency and no map iteration —
// byte-identical given the same on-disk WAL contents. recovery_test.go's
// crash-recovery test proves the REBUILT state (checkpoint + replay) is
// byte-identical to a reference state built by applying the exact same
// commands WITHOUT ever crashing.
//
// # Idempotence
//
// Recover only ever READS the checkpoint and the WAL — it never prunes,
// never deletes, never mutates anything on disk (Prune is wal.go's own
// separate, explicit, checkpoint-triggered call that the recovery path
// itself never makes). Running Recover twice in a row against the exact
// same on-disk state therefore replays the exact same set of entries
// (every WAL entry whose tick is strictly greater than the loaded
// checkpoint's tick) and produces the exact same rebuilt state both
// times — it can never double-apply within, or across, repeated Recover
// calls. See recovery_test.go's TestRecover_IdempotentAcrossRepeatedRuns.

// RecoverPlan groups the two on-disk roots and the participant registry
// crash recovery needs: CheckpointRoot is the root a checkpoint.Manager
// was/would be constructed against (internal/engine/checkpoint); WALRoot
// is the durable command log's root — the same string a live *WAL
// (wal.go) was/would be constructed with (NewWAL's root argument) before
// the crash. Participants is the exact same []save.Participant list the
// live engine registered its checkpointable state through — Recover
// reuses checkpoint.Manager.Load verbatim (GR#3/GR#20), so this package
// never reimplements state reconstruction.
type RecoverPlan struct {
	CheckpointRoot string
	WALRoot        string
	Participants   []save.Participant
	CorrelationID  string

	// ExpectedWorldSeed is the composition's own world seed (BUG-485) —
	// the same value the live engine passes as save.Context.WorldSeed to
	// every checkpoint.Manager.CreateCheckpoint call before a crash.
	// Recover threads it into the checkpoint.Manager it constructs so the
	// checkpoint load this function performs refuses a differently-seeded
	// checkpoint bundle with save.ErrSaveSeedMismatch instead of silently
	// rebuilding live state from a foreign trajectory (mirroring
	// compose.Composition.Load's BUG-479 check one layer up). 0 is a
	// legitimate seed value, not an "unchecked" sentinel — there is no
	// way to construct a RecoverPlan that skips the check.
	ExpectedWorldSeed int64
}

// RecoverResult reports what Recover actually did: whether a checkpoint
// existed to load at all (a fresh root has none — HadCheckpoint false,
// CheckpointID/CheckpointTick zero-valued, which is not an error), the
// checkpoint's own identity and the simulation tick it was taken at
// (serialize.Header.CreatedAtTick, via checkpoint.Manager.Load), and how
// much of the WAL was replayed on top of it.
type RecoverResult struct {
	HadCheckpoint  bool
	CheckpointID   checkpoint.ID
	CheckpointTick int64

	// ReplayStartSeq/LastReplayedSeq/ReplayedCount describe the replay
	// window actually applied: [ReplayStartSeq, LastReplayedSeq], count
	// entries — counting ONLY entries actually applied (tick strictly
	// greater than the loaded checkpoint's tick; see this file's header
	// comment on idempotence). ReplayedCount == 0 (LastReplayedSeq left
	// at its zero value) means nothing beyond the checkpoint needed
	// replaying.
	ReplayStartSeq  int64
	LastReplayedSeq int64
	ReplayedCount   int
}

// Recover rebuilds full state deterministically after a crash/reboot
// (proposal §1 point 6 / §2 "Crash recovery"):
//
//  1. Load the last checkpoint (checkpoint.Manager.CurrentID + Load) to
//     rebuild every registered Participant's state — the base every
//     subsequently replayed command is applied on top of. A fresh root
//     with no checkpoint yet is NOT an error (RecoverResult.HadCheckpoint
//     is simply false); every WAL entry then replays on top of each
//     Participant's own zero-value state (cutoffTick used for the
//     tick-filter below is math.MinInt64 in that case — "no checkpoint"
//     means "nothing is already captured", not "tick 0 is already
//     captured", since tick 0 is itself a valid, real simulation tick).
//  2. List the WAL's CURRENT slot (wal.go's listWALSeqs — the same
//     directory-scan-at-recovery-time rationale wal.go's own header
//     comment documents: a cold Recover has no live in-memory sequence
//     counter to start from) and, for every entry found IN ASCENDING
//     SEQUENCE ORDER, decode it (readWALEntry) and apply it via apply
//     (the caller-supplied function that turns a decoded protocol.Command
//     into whatever mutation the live engine would have performed had it
//     not crashed) — but ONLY if the entry's own recorded tick is
//     STRICTLY GREATER than the checkpoint's tick (or, with no
//     checkpoint, always). An entry at or below the checkpoint's tick is
//     skipped, never re-applied — its effect is already durably captured
//     in the checkpoint bundle Load just restored, and re-applying it
//     would double-apply state the checkpoint already reflects (this is
//     also exactly what makes Recover idempotent across repeated calls
//     against unpruned WAL contents — see this file's header comment).
//
// A present-but-undecodable WAL entry (genuine corruption, distinct from
// a torn/never-promoted tail entry, which simply never appears in
// listWALSeqs' directory listing at all — see wal.go's "Atomic writes"
// section) is a real error (ErrWALReadFailed, wrapped from readWALEntry)
// and halts replay immediately, exactly like a failing apply
// (ErrRecoveryApplyFailed) — neither is ever silently skipped.
//
// apply is called synchronously, once per replayed entry, in ascending
// sequence order, on the calling goroutine — Recover adds no concurrency
// of its own (replay order is the whole determinism guarantee).
func Recover(plan RecoverPlan, apply func(protocol.Command) error) (RecoverResult, error) {
	var result RecoverResult

	mgr := checkpoint.NewManager(plan.CheckpointRoot, plan.Participants, plan.CorrelationID, plan.ExpectedWorldSeed)
	id, err := mgr.CurrentID()
	if err != nil {
		return result, errs.Wrap(ErrRecoveryCheckpointLoadFailed, plan.CorrelationID, err, map[string]any{"phase": "current-id"})
	}

	// cutoffTick is the tick BELOW OR AT which a WAL entry's effect is
	// already durably captured by the loaded checkpoint, and must
	// therefore be skipped rather than re-applied (see this function's
	// doc comment, step 2). math.MinInt64 with no checkpoint at all means
	// every WAL entry is strictly greater — nothing is skipped.
	cutoffTick := int64(math.MinInt64)
	if id != "" {
		header, _, loadErr := mgr.Load(id)
		if loadErr != nil {
			return result, errs.Wrap(ErrRecoveryCheckpointLoadFailed, plan.CorrelationID, loadErr, map[string]any{
				"phase":        "load",
				"checkpointId": string(id),
			})
		}
		result.HadCheckpoint = true
		result.CheckpointID = id
		result.CheckpointTick = header.CreatedAtTick
		cutoffTick = header.CreatedAtTick
	}

	slot, err := readCurrentSlot(plan.WALRoot, plan.CorrelationID)
	if err != nil {
		return result, err
	}
	slotDir := walSlotDir(plan.WALRoot, slot)

	seqs, err := listWALSeqs(slotDir, plan.CorrelationID)
	if err != nil {
		return result, err
	}

	for _, seq := range seqs {
		tick, cmd, err := readWALEntry(slotDir, seq, plan.CorrelationID)
		if err != nil {
			// Present at its final, atomically-renamed path but failed
			// to read/decode — genuine corruption (wal.go's header
			// comment on why a torn write is instead simply ABSENT from
			// listWALSeqs' directory listing, never present-but-broken).
			return result, err
		}
		if tick <= cutoffTick {
			// Already durably captured by the loaded checkpoint — never
			// re-applied (idempotence; this function's doc comment).
			continue
		}

		if applyErr := apply(cmd); applyErr != nil {
			return result, errs.Wrap(ErrRecoveryApplyFailed, plan.CorrelationID, applyErr, map[string]any{"seq": seq})
		}

		if result.ReplayedCount == 0 {
			result.ReplayStartSeq = seq
		}
		result.LastReplayedSeq = seq
		result.ReplayedCount++
	}

	return result, nil
}
