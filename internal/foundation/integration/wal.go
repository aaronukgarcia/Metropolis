package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is INCREMENT 3's CORRECTED durability layer for the
// Integration Engine (proposal §8 point 3 / §1 point 6): a dedicated,
// append-only, durable Write-Ahead Log (WAL), entirely separate from the
// T1 tier's TRANSIENT disk overflow spill (queue.go/queue_disk.go,
// increment 2, left untouched by this file). It replaces the first
// version of recovery.go, which reused T1's overflow segments as the
// durable command log and was REJECTED on destructive review for two
// silent-data-loss defects:
//
//  1. Commit-before-apply race: tierQueue.commitLocked (queue.go)
//     deletes a T1 segment the INSTANT its command is handed to the
//     inner transport (Drain) — before the caller has applied the
//     command's effect to authoritative state, and before that effect is
//     ever checkpointed. A crash in the gap between "Drain delivered it"
//     and "the tick driver applied + would eventually checkpoint it"
//     silently loses the command: the T1 segment is already gone, and no
//     checkpoint ever captured it.
//  2. Gap-vs-boundary confusion: T1's in-memory window recycles sequence
//     numbers as commands drain and new ones arrive (queue.go's
//     peekLocked worked example), which can leave a legitimate MID-RANGE
//     hole on disk (an old spilled command still pending while a newer
//     one already occupies memory). The old recovery.go treated "no file
//     at the next sequence" as ALWAYS meaning "end of log" — for a
//     mid-range hole that is wrong: replay stops too early (misses a
//     still-pending LATER command) and double-applies an EARLIER one on
//     the next boot (it is still on disk, still gets replayed again,
//     because nothing about the hole means it was ever applied).
//
// # The fix: append-before-apply, prune-only-on-checkpoint
//
// Every command that will mutate authoritative state is APPENDED to the
// WAL (Append, below — fsync'd) BEFORE it is applied. A WAL entry is
// pruned ONLY once a checkpoint has durably captured its effect (its
// tick <= the checkpoint's tick) — NEVER merely because the command was
// delivered, drained, or applied. That is what makes recovery correct:
//
//   - Bug 1 becomes impossible: the WAL entry for a command survives a
//     crash in the accept-for-apply gap regardless of what the
//     (unrelated) T1 queue did with its own copy of the same command —
//     Recover (recovery.go) replays it straight from the WAL.
//   - Bug 2 becomes impossible: a WAL "slot" (this file's on-disk unit —
//     see "Structural contiguity" below) never has entries individually
//     deleted; the only two things that ever happen to it are Append
//     (grows the tail by exactly one) and Prune (atomically replaces the
//     WHOLE slot with a rewritten, contiguous, retained-only slot). At
//     every instant its on-disk entries form a contiguous sequence
//     range — "absent" can therefore only ever mean the torn/
//     not-yet-promoted tail, never a mid-range hole.
//
// The seam a composition root wires this through (this package
// deliberately does NOT do the wiring — proposal §8/dispatch brief):
// every point a command is accepted for application should call
// Append(tick, cmd) FIRST, check its error (a WAL write failure must
// block the apply — an applied-but-unlogged command is exactly Bug 1
// again), and only then actually apply the command's effect. After each
// successful CreateCheckpoint, the composition root should call
// Prune(checkpoint.CreatedAtTick) to reclaim space; skipping Prune is
// always SAFE (Recover simply re-filters already-checkpointed entries
// out again, see recovery.go), just wasteful of disk.
//
// # Structural contiguity (why mid-gaps are structurally impossible)
//
// Append assigns the WAL's own monotonically increasing sequence number
// to every entry in the SAME order commands are accepted for
// application, so entries are laid out in strictly increasing (seq,
// tick) order with tick non-decreasing as seq increases (a caller only
// ever appends "the next command about to be applied," which happens in
// the engine's own tick order — never reordered, never retried out of
// order). Prune removes every entry whose tick <= a given checkpoint
// tick; by the seq/tick monotonicity just established, that condition is
// ALWAYS true for a leading PREFIX of the sequence range and false for
// everything after it — a retained (kept) entry can never sit below a
// pruned one in sequence order. So a slot's surviving entries are always
// exactly the contiguous range [lowest surviving seq, highest appended
// seq], with the sole possible exception of a torn tail write for
// highest+1 (see "Atomic writes" below), which — like every atomically
// promoted file in this codebase — is simply ABSENT, never partially
// present.
//
// # Atomic writes + atomic prune ("staging then rename", twice over)
//
// Two different things are rewritten atomically here, using the SAME
// staging-then-os.Rename idiom this codebase already uses everywhere
// (queue_disk.go's writeSegment, save/bundle.go's bundle promotion,
// checkpoint/meta.go's writeHead pointer file):
//
//   - Each individual entry is written via the SAME
//     temp-file-in-staging-then-rename pattern queue_disk.go's
//     writeSegment uses (writeWALEntry, below): an entry is only ever
//     discoverable at its final path once fully written and fsync'd, so
//     a crash mid-write leaves NO file at that path — never a partial
//     one. "Torn" and "absent" are the same observable state, exactly as
//     queue_disk.go's own header comment establishes for T1 segments.
//   - Prune is a REWRITE of the retained set, and gets its OWN
//     staging-then-rename at a COARSER grain: rather than deleting
//     individual pruned entry files in place (which could leave an
//     observably PARTIAL prune after a mid-prune crash — some prefix
//     entries gone, some not, no atomic "before/after" line, and
//     "absent" would stop meaning "the tail" again), a WAL keeps TWO
//     fixed slot directories ("a" and "b") and a single small
//     WAL-CURRENT pointer file naming which one is live. Prune rebuilds
//     the FULL retained set in the currently-INACTIVE slot (each entry
//     written via the same atomic writeWALEntry), and only once every
//     retained entry is durably written does it flip WAL-CURRENT (via
//     its own temp-file-then-os.Rename, mirroring checkpoint/meta.go's
//     writeHead) to name that slot active. A crash at any point before
//     the flip leaves the OLD slot active (nothing pruned, from any
//     external reader's point of view — including a concurrent Recover);
//     a crash after leaves the NEW slot active (fully pruned). There is
//     no partially-pruned observable state, ever — which is exactly what
//     keeps "on-disk entries are a contiguous range" true even across a
//     crash mid-prune.
//
// # Determinism (GR#21)
//
// Append order is the sequence counter, assigned once per call under
// wal.mu, in caller call order — never wall-clock, never
// goroutine-scheduling order beyond the caller's own serialized calls
// (mirrors tierQueue.enqueueLocked's nextSeq assignment). Recover/replay
// order (recovery.go) is strictly ascending sequence number, read one
// entry at a time, no concurrency, no map iteration — byte-identical
// given the same on-disk slot contents, and IDEMPOTENT: Recover only
// ever READS the WAL (Prune is a separate, explicit, checkpoint-
// triggered call the recovery path itself never makes), so running
// Recover twice against the same on-disk state replays the exact same
// entries and produces the exact same rebuilt state both times — see
// recovery_test.go's TestRecover_IdempotentAcrossRepeatedRuns.

// walSlotA/walSlotB are the two fixed, alternating on-disk slot names a
// WAL's entries live under (root/wal-a, root/wal-b). Exactly one is
// "current" (named by WAL-CURRENT) at any moment; Prune always rebuilds
// into whichever one is NOT current, then flips the pointer — see this
// file's header comment.
const (
	walSlotA = "wal-a"
	walSlotB = "wal-b"

	// walCurrentFileName is the root-level pointer file naming the
	// active slot, written via the same atomic temp-then-rename pattern
	// as checkpoint/meta.go's writeHead.
	walCurrentFileName = "WAL-CURRENT"

	// walEntryFileExt/walEntrySeqWidth mirror queue_disk.go's
	// segmentFileExt/segmentSeqWidth exactly (see that file's doc
	// comment) — a fixed-width, zero-padded, monotonic-sequence file
	// name per entry, kept lexically sorted for forensic inspection but
	// never relied on for correctness (this file always lists a slot
	// directory in full via os.ReadDir — see "why a directory scan" in
	// recovery.go's header comment, which this file's List function
	// shares the rationale of).
	walEntryFileExt  = ".wal"
	walEntrySeqWidth = 20

	// walStagingSubdir is the per-slot staging directory writeWALEntry
	// stages an in-progress entry write in before promoting it via
	// os.Rename — mirrors queue_disk.go's spillStagingSubdir.
	walStagingSubdir = ".wal-staging"
)

// otherWALSlot returns the slot NOT named by slot — the target Prune
// always rebuilds into. A slot name this package did not itself produce
// (e.g. a corrupted/foreign WAL-CURRENT) falls back to walSlotA, the same
// "fail toward the well-known default" posture readCurrentSlot's own doc
// comment documents.
func otherWALSlot(slot string) string {
	if slot == walSlotA {
		return walSlotB
	}
	return walSlotA
}

// walSlotDir returns the directory one of a WAL root's two fixed slots
// lives under.
func walSlotDir(root, slot string) string {
	return filepath.Join(root, slot)
}

// walStagingDir returns the staging directory writeWALEntry stages an
// in-progress write in before promoting it into slotDir, mirroring
// queue_disk.go's spillStagingDir.
func walStagingDir(slotDir string) string {
	return filepath.Join(slotDir, walStagingSubdir)
}

// walEntryPath returns the final, discoverable path of the WAL entry for
// sequence seq under slotDir.
func walEntryPath(slotDir string, seq int64) string {
	return filepath.Join(slotDir, fmt.Sprintf("%0*d%s", walEntrySeqWidth, seq, walEntryFileExt))
}

// walCurrentPath returns the WAL-CURRENT pointer file's path under root.
func walCurrentPath(root string) string {
	return filepath.Join(root, walCurrentFileName)
}

// walEntryWire is the on-disk JSON shape of one WAL entry: the command's
// own tick (the sim tick it applies at — the field Prune filters on) and
// its protocol-encoded bytes. Kept as a thin envelope around
// protocol.EncodeCommand's own output (rather than re-marshalling
// protocol.Command's fields directly) so this file never has to track
// protocol's own wire-shape decisions (GR#3 — never reimplement).
type walEntryWire struct {
	Seq  int64           `json:"seq"`
	Tick int64           `json:"tick"`
	Cmd  json.RawMessage `json:"cmd"`
}

// readCurrentSlot reads root's WAL-CURRENT pointer. A missing file (a
// WAL that has never appended anything yet) is NOT an error: it yields
// walSlotA, the well-known default a fresh WAL's first Append implicitly
// writes into. A present-but-malformed pointer file — corrupted contents
// outside this package's own atomic writer — is a real error
// (ErrWALWriteFailed's read-side counterpart would be over-engineering
// for a single-line pointer file; folded into ErrWALDirCreateFailed's
// sibling ErrWALWriteFailed code is inaccurate too, so this uses the
// same read-failure code recovery.go's WAL-entry reads use,
// ErrWALReadFailed, since a malformed pointer is exactly the same class
// of "present but undecodable" hazard).
func readCurrentSlot(root, correlationID string) (string, error) {
	raw, err := os.ReadFile(walCurrentPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return walSlotA, nil
		}
		return "", errs.Wrap(ErrWALReadFailed, correlationID, err, map[string]any{"path": walCurrentPath(root), "cause": err.Error()})
	}
	slot := strings.TrimSpace(string(raw))
	if slot != walSlotA && slot != walSlotB {
		return "", errs.New(ErrWALReadFailed, correlationID, map[string]any{"path": walCurrentPath(root), "cause": "WAL-CURRENT names neither known slot"})
	}
	return slot, nil
}

// writeCurrentSlot atomically points root's WAL-CURRENT pointer at slot —
// a temp file in root, fsync'd, then os.Rename'd into place, exactly
// checkpoint/meta.go's writeHead pattern (see that function's doc
// comment for why this ordering is what makes the pointer flip atomic).
func writeCurrentSlot(root, slot, correlationID string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return errs.Wrap(ErrWALDirCreateFailed, correlationID, err, map[string]any{"root": root, "cause": err.Error()})
	}
	tmp, err := os.CreateTemp(root, "wal-current-*.tmp")
	if err != nil {
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": slot, "cause": err.Error()})
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.WriteString(slot); err != nil {
		_ = tmp.Close()
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": slot, "cause": err.Error()})
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": slot, "cause": err.Error()})
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": slot, "cause": err.Error()})
	}
	if err := os.Rename(tmpPath, walCurrentPath(root)); err != nil {
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": slot, "cause": err.Error()})
	}
	return nil
}

// writeWALEntry atomically writes one WAL entry (seq, tick, cmd) under
// slotDir: encode -> write to a fresh temp file under
// slotDir/.wal-staging -> fsync -> close -> os.Rename into
// walEntryPath(slotDir, seq). Mirrors queue_disk.go's writeSegment
// exactly (see that function's doc comment for the full rationale); any
// failure along the way leaves NO file at the final path.
func writeWALEntry(slotDir string, seq, tick int64, cmd protocol.Command, correlationID string) error {
	staging := walStagingDir(slotDir)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return errs.Wrap(ErrWALDirCreateFailed, correlationID, err, map[string]any{"root": slotDir, "cause": err.Error()})
	}

	cmdData, err := protocol.EncodeCommand(cmd)
	if err != nil {
		// cmd has already passed Validate by the time Append/Prune call
		// this (Append validates below; Prune only ever re-writes an
		// entry this same function already encoded once successfully),
		// so this is effectively unreachable — handled explicitly rather
		// than ignored regardless (GR#1).
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	encoded, err := json.Marshal(walEntryWire{Seq: seq, Tick: tick, Cmd: cmdData})
	if err != nil {
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}

	tmp, err := os.CreateTemp(staging, "wal-*")
	if err != nil {
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}

	finalPath := walEntryPath(slotDir, seq)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return errs.Wrap(ErrWALWriteFailed, correlationID, err, map[string]any{"seq": seq, "cause": err.Error()})
	}
	return nil
}

// readWALEntry reads and decodes the WAL entry for sequence seq under
// slotDir. Any failure — missing, unreadable, or failing to decode — is
// ErrWALReadFailed; an entry is NEVER treated as validly decoded unless
// every step succeeds in full (mirrors queue_disk.go's readSegment).
func readWALEntry(slotDir string, seq int64, correlationID string) (tick int64, cmd protocol.Command, err error) {
	path := walEntryPath(slotDir, seq)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return 0, protocol.Command{}, errs.Wrap(ErrWALReadFailed, correlationID, readErr, map[string]any{"seq": seq, "cause": readErr.Error()})
	}
	var wire walEntryWire
	if jsonErr := json.Unmarshal(data, &wire); jsonErr != nil {
		return 0, protocol.Command{}, errs.Wrap(ErrWALReadFailed, correlationID, jsonErr, map[string]any{"seq": seq, "cause": jsonErr.Error()})
	}
	decoded, decErr := protocol.DecodeCommand(wire.Cmd)
	if decErr != nil {
		return 0, protocol.Command{}, errs.Wrap(ErrWALReadFailed, correlationID, decErr, map[string]any{"seq": seq, "cause": decErr.Error()})
	}
	return wire.Tick, decoded, nil
}

// listWALSeqs lists every promoted (fully-written, atomically-renamed)
// WAL entry's sequence number under slotDir, sorted ascending. A missing
// slotDir (never appended into) yields an empty, non-error result. This
// is a full directory scan, deliberately — exactly the same "recovery-
// only bootstrap, not a steady-state access pattern" exception
// recovery.go's old lowestPendingSeq documented, now used by BOTH
// Recover (recovery.go) and Prune (below), since neither has a live
// in-memory sequence counter to start from (Recover runs cold; Prune
// must see the FULL retained set to rebuild the other slot, not just its
// lowest member).
func listWALSeqs(slotDir, correlationID string) ([]int64, error) {
	entries, err := os.ReadDir(slotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(ErrWALReadFailed, correlationID, err, map[string]any{"root": slotDir, "cause": err.Error()})
	}

	var seqs []int64
	for _, e := range entries {
		if e.IsDir() {
			// Skips walStagingSubdir (".wal-staging") — never-promoted,
			// in-progress writes live there and must never be mistaken
			// for a durable entry.
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, walEntryFileExt) {
			continue
		}
		numStr := strings.TrimSuffix(name, walEntryFileExt)
		n, convErr := strconv.ParseInt(numStr, 10, 64)
		if convErr != nil {
			// Not one of this package's own zero-padded sequence names —
			// ignore rather than fail the whole scan over an unrelated
			// file someone else left in the directory.
			continue
		}
		seqs = append(seqs, n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

// WAL is a per-integration-substrate, append-only, durable command log
// (this file's header comment has the full design). The zero value is
// not usable — construct via NewWAL.
//
// A *WAL is safe for concurrent use: mu guards nextSeq and the active
// slot, both of which Append and Prune mutate. Like every other
// SEC-020-guarded type in this package (tierQueue, QueuedTransport,
// Connection), WAL carries both a sync.Mutex VALUE and an aliasable
// string/int field set a struct copy would alias while gaining an
// independent, non-exclusive mutex — the same "two locks, one referent"
// hazard, guarded the same way: checkNotCopied, called before mu is ever
// touched, at every real entry point.
type WAL struct {
	mu sync.Mutex

	root          string
	correlationID string

	slot    string // current active slot (walSlotA/walSlotB)
	nextSeq int64  // next global sequence number Append will assign

	// self is the SEC-020-class copy-identity guard — same pattern and
	// rationale as tierQueue.self/QueuedTransport.self/Connection.self.
	self atomic.Pointer[WAL]
}

// NewWAL constructs (or re-opens) a *WAL rooted at root. It reads
// WAL-CURRENT (defaulting to walSlotA for a fresh root — readCurrentSlot's
// doc comment) and scans that slot's directory (listWALSeqs) to recover
// nextSeq as one-past the highest sequence already durably appended —
// this is what lets Append keep assigning STRICTLY increasing sequence
// numbers across a process restart, gap-free, exactly as this file's
// header comment's "Structural contiguity" section requires. A fresh
// root (nothing ever appended) yields nextSeq 0.
func NewWAL(root, correlationID string) (*WAL, error) {
	slot, err := readCurrentSlot(root, correlationID)
	if err != nil {
		return nil, err
	}
	seqs, err := listWALSeqs(walSlotDir(root, slot), correlationID)
	if err != nil {
		return nil, err
	}
	next := int64(0)
	if len(seqs) > 0 {
		next = seqs[len(seqs)-1] + 1
	}

	w := &WAL{root: root, correlationID: correlationID, slot: slot, nextSeq: next}
	// Stored once, here, before w is returned to any caller — mirrors
	// NewQueuedTransport/NewConnection's self.Store timing exactly.
	w.self.Store(w)
	return w, nil
}

// checkNotCopied mirrors tierQueue.checkNotCopied/QueuedTransport.
// checkNotCopied/Connection.checkNotCopied exactly: a lock-free identity
// check, safe to call before w.mu is ever touched.
func (w *WAL) checkNotCopied(method string) error {
	if w.self.Load() != w {
		return errs.New(ErrWALCopied, w.correlationID, map[string]any{"method": method})
	}
	return nil
}

// Append durably logs cmd — the command about to be applied at tick — to
// the WAL: assigns it the next global sequence number and writes it,
// fsync'd, into the CURRENT slot (writeWALEntry) BEFORE returning. A
// non-nil return means cmd was NOT durably logged; the caller MUST NOT
// apply the command's effect in that case (this file's header comment's
// "seam" note — an applied-but-unlogged command reintroduces exactly Bug
// 1, the commit-before-apply race, on the WAL's own persistence path
// this time). On success, seq is the assigned sequence number (useful
// for logging/monitoring — this package's Recover never needs it back).
func (w *WAL) Append(tick int64, cmd protocol.Command) (seq int64, err error) {
	if err := w.checkNotCopied("Append"); err != nil {
		return 0, err
	}
	if err := cmd.Validate(); err != nil {
		return 0, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	seq = w.nextSeq
	if err := writeWALEntry(walSlotDir(w.root, w.slot), seq, tick, cmd, w.correlationID); err != nil {
		return 0, err
	}
	w.nextSeq++
	return seq, nil
}

// PruneResult reports what one Prune call did.
type PruneResult struct {
	// PrunedCount is how many entries were dropped (tick <= the supplied
	// checkpoint tick).
	PrunedCount int
	// RetainedCount is how many entries survived (tick > the supplied
	// checkpoint tick) and were carried forward into the (possibly new)
	// active slot.
	RetainedCount int
}

// Prune reclaims WAL space for every entry whose tick is <= checkpointTick
// — proposal's "prune only on checkpoint": the caller (a composition root,
// per this file's header "seam" note) is expected to call this AFTER a
// checkpoint at checkpointTick has been successfully created, never
// before, and never merely because commands were delivered/applied
// (that would reintroduce Bug 1).
//
// Implementation: reads every entry in the CURRENT slot (listWALSeqs +
// readWALEntry), and if nothing would actually be pruned, returns
// immediately without touching disk (PrunedCount 0 is a valid, cheap,
// common no-op). Otherwise it re-writes every RETAINED entry (tick >
// checkpointTick), unchanged (same seq, same tick, same cmd), into the
// OTHER (currently inactive) slot — first clearing that slot's directory
// so it starts from a clean state (safe: an inactive slot's prior
// contents, if any, are always a STALE prune from before the previous
// flip, since the pointer only ever names one slot "live" and Append
// only ever writes into the live one) — then atomically flips
// WAL-CURRENT to the newly-rebuilt slot (writeCurrentSlot). Only after
// the flip is durable does it best-effort clean up the now-abandoned old
// slot's directory (a leftover there after a crash between the flip and
// this cleanup is disk-usage clutter, not a correctness hazard — mirrors
// queue_disk.go's removeSegment/save/bundle.go's reapDisplacedSiblings
// precedent — the OLD slot is never consulted again once WAL-CURRENT
// names the new one).
//
// See this file's header comment's "Atomic writes + atomic prune"
// section for why this whole-slot rebuild-then-pointer-flip is used
// instead of deleting pruned entries in place.
func (w *WAL) Prune(checkpointTick int64) (PruneResult, error) {
	if err := w.checkNotCopied("Prune"); err != nil {
		return PruneResult{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	activeDir := walSlotDir(w.root, w.slot)
	seqs, err := listWALSeqs(activeDir, w.correlationID)
	if err != nil {
		return PruneResult{}, err
	}

	type retained struct {
		seq  int64
		tick int64
		cmd  protocol.Command
	}
	var keep []retained
	pruned := 0
	for _, seq := range seqs {
		tick, cmd, err := readWALEntry(activeDir, seq, w.correlationID)
		if err != nil {
			return PruneResult{}, err
		}
		if tick <= checkpointTick {
			pruned++
			continue
		}
		keep = append(keep, retained{seq: seq, tick: tick, cmd: cmd})
	}

	if pruned == 0 {
		// Nothing to reclaim — deliberately a no-op (this function's doc
		// comment): no rebuild, no pointer flip, no disk churn.
		return PruneResult{PrunedCount: 0, RetainedCount: len(keep)}, nil
	}

	target := otherWALSlot(w.slot)
	targetDir := walSlotDir(w.root, target)
	// The inactive slot's contents (if any) are always a stale prior
	// prune — safe to discard before rebuilding (see this method's doc
	// comment). Best-effort: a failure here is surfaced as a real error
	// below via the MkdirAll inside writeWALEntry if it actually matters;
	// leftover stale files that RemoveAll couldn't clear would only ever
	// be entries this prune is about to overwrite by identical seq
	// anyway (writeWALEntry's os.Rename replaces same-named files).
	_ = os.RemoveAll(targetDir)

	for _, r := range keep {
		if err := writeWALEntry(targetDir, r.seq, r.tick, r.cmd, w.correlationID); err != nil {
			return PruneResult{}, errs.Wrap(ErrWALPruneFailed, w.correlationID, err, map[string]any{"seq": r.seq, "cause": err.Error()})
		}
	}

	if err := writeCurrentSlot(w.root, target, w.correlationID); err != nil {
		return PruneResult{}, errs.Wrap(ErrWALPruneFailed, w.correlationID, err, map[string]any{"cause": err.Error()})
	}

	oldDir := activeDir
	w.slot = target
	// Best-effort, SYNCHRONOUS cleanup of the now-abandoned old slot —
	// never consulted again once WAL-CURRENT names the new one (see this
	// method's doc comment). Deliberately not backgrounded: Prune already
	// holds w.mu for its own rebuild work, and a caller that just pruned
	// reasonably expects the old slot's disk usage reclaimed before Prune
	// returns, not racing an unrelated goroutine against its own next
	// Append/Prune call.
	_ = os.RemoveAll(oldDir)

	return PruneResult{PrunedCount: pruned, RetainedCount: len(keep)}, nil
}
