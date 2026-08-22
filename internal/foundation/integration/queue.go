package integration

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is INCREMENT 2 of the Integration Engine (proposal §8): a
// FIFO, priority-tiered overflow queue with disk spill and backpressure,
// inserted at the protocol.Transport seam (proposal §7's "queue/overflow
// layer wraps protocol.Transport" seam). It does NOT implement the
// resilience/retry/reconnect state machine or crash-recovery replay
// (proposal §8 point 3) — those are later increments; this package only
// guarantees that a command is never silently lost between "the UI called
// SendCommand" and "the inner transport actually accepted it," and that
// the order it eventually reaches the inner transport in is a
// deterministic function of arrival order + tier, never of wall-clock
// time or goroutine scheduling.
//
// # Tiers (proposal §3)
//
// Every Command is classified into a Class (integration.go, already
// defined by increment 1) via ClassOf(cmd.Kind):
//
//   - ClassT0Critical:    FIFO, memory-only, no disk fallback. T0's
//     contract is "never queued past the current tick" (proposal §3) —
//     spilling a T0 command to disk would let it survive INTO a later
//     tick, which violates that contract worse than rejecting it
//     outright. A T0 command that cannot be enqueued is
//     ErrT0QueueExhausted (queue_errors.go), a registry error, never a
//     silent drop.
//   - ClassT1Batchable:   FIFO, memory window backed by a disk-spilled
//     overflow tail (see "The hybrid memory+disk FIFO" below). Heavy,
//     cadence-batchable work; absorbs bursts without ever dropping a
//     command. A spill failure (e.g. disk full) is ErrSpillWriteFailed,
//     again never a silent drop.
//   - ClassT2Coalescible: latest-wins. Deliberately NOT a FIFO queue at
//     all — proposal §3/§1.3 explicitly calls out T2 (telemetry/display
//     state) as "safe to drop intermediate frames," so this tier holds
//     at most one pending command, and a new SendCommand simply
//     overwrites whatever was still pending. This can never fail with a
//     queue-exhaustion error, by design.
//
// # Priority + FIFO (proposal §1 point 3)
//
// Across tiers, priority is strict: Drain always offers T0's next
// command to the inner transport before ever looking at T1, and T1
// before T2. WITHIN a tier, order is strict FIFO — arrival order, never
// FILO — which is what makes drain order a pure function of (tier,
// arrival sequence) and therefore replay-reproducible (proposal §2's
// "the overflow queue doubles as the durable command log").
//
// # The hybrid memory+disk FIFO (T0 and T1)
//
// Both T0 and T1 use the same tierQueue type (T0 simply has disk
// spilling disabled). Every enqueued command is assigned a monotonically
// increasing per-tier sequence number AT ENQUEUE TIME, regardless of
// whether it lands in the in-memory window or (T1 only) spills straight
// to disk once that window is full:
//
//	enqueue: if len(memory) < memCap -> append to memory (tagged seq)
//	         else                    -> write disk segment (tagged seq)
//
// Draining always asks for the LOWEST outstanding sequence number
// (nextDrainSeq), regardless of whether it currently lives in memory or
// on disk: if memory's front element's seq matches, take it from memory;
// otherwise it must be the disk segment for that exact seq, so read it
// from there. This is what keeps drain order correct even though the
// in-memory window can end up holding LATER-arriving commands while an
// EARLIER one is still stranded on disk (a new command arrives with room
// in memory while an old spilled one hasn't drained yet) — the seq
// number, not physical location, is the single source of truth for
// order. See tierQueue.peekLocked's comment for a worked example.
//
// # Determinism (GR#21)
//
// Every ordering decision in this file is driven by: (a) tier priority,
// a fixed constant order (T0, T1, T2); (b) a per-tier monotonically
// increasing sequence counter, assigned once per enqueue under that
// tier's own mutex; and (c) plain slice/field reads under that mutex —
// no map iteration, no wall-clock read, no goroutine-scheduling-order
// dependency anywhere in Drain, peekLocked, or commitLocked. Two runs
// that call SendCommand with the same commands in the same order (from
// however many concurrent goroutines, so long as each command reaches
// SendCommand's Validate/lock acquisition — real concurrent callers race
// on ARRIVAL order same as any queue, which is a caller-level fact, not
// something this package can or should paper over) and then Drain
// produce byte-identical sequences of commands handed to the inner
// transport. queue_test.go's determinism test proves this by re-running
// the exact same enqueue sequence twice and diffing the drained order.

// Class already carries T0/T1/T2 (integration.go, increment 1). This
// file adds the KIND -> Class mapping the queue layer needs: increment
// 1's Integration[T,M] contract already required a class from every real
// integration, but the protocol.Command vocabulary (commands.go) predates
// this queue and carries no class field of its own. Rather than modify
// protocol.Command (protocol is neutral ground, per its own doc.go, and
// changing its wire shape is a breaking-change decision this increment
// has no mandate to make), classification lives entirely here as an
// additive, closed lookup table — "a minimal tier-tagging seam without
// breaking existing callers."

// kindClass maps every protocol.Kind (commands.go) to its queue Class.
// A Kind with no entry here defaults to ClassT1Batchable (classOfDefault)
// — the safe middle tier: never assumed so urgent it can bypass FIFO
// ordering to jump the T0 queue, and never assumed safe to coalesce away
// (T2 is opt-in only, for kinds this table has affirmatively decided are
// latest-wins-safe).
var kindClass = map[protocol.Kind]Class{
	// T0 critical: clock/simulation control. Must run every tick and
	// must never be silently starved by a T1/T2 backlog.
	protocol.KindAdvanceTicks: ClassT0Critical,
	protocol.KindPause:        ClassT0Critical,
	protocol.KindResume:       ClassT0Critical,

	// T2 coalescible: SetSpeed is the one command in the v1 vocabulary
	// that is genuinely latest-wins safe — if three SetSpeed commands
	// arrive before the engine catches up, only the LAST one has any
	// observable effect on final state (the intermediate speeds were
	// never actually run at), so dropping the intermediates changes
	// nothing (proposal §3's coalescible-telemetry rationale, extended
	// to this one idempotent-latest-wins control command).
	protocol.KindSetSpeed: ClassT2Coalescible,

	// T1 batchable: everything else — view-subscription control and the
	// gameplay build-queue commands. These are authoritative (a dropped
	// Buy is a dropped land purchase) but tolerate FIFO batching/delay,
	// unlike T0's every-tick requirement.
	protocol.KindSubscribe:     ClassT1Batchable,
	protocol.KindUnsubscribe:   ClassT1Batchable,
	protocol.KindInspectEntity: ClassT1Batchable,
	protocol.KindDebug:         ClassT1Batchable,
	protocol.KindBuy:           ClassT1Batchable,
	protocol.KindZone:          ClassT1Batchable,
	protocol.KindBuild:         ClassT1Batchable,
	protocol.KindDemolish:      ClassT1Batchable,
	protocol.KindSetFunding:    ClassT1Batchable,
}

// classOfDefault is the Class an unrecognised Kind falls back to — see
// kindClass's doc comment.
const classOfDefault = ClassT1Batchable

// ClassOf reports the queue tier a Command's Kind classifies into.
// Exported so a tick driver (or a test) can reason about tiering without
// reaching into this package's unexported lookup table directly.
func ClassOf(kind protocol.Kind) Class {
	if c, ok := kindClass[kind]; ok {
		return c
	}
	return classOfDefault
}

// tierOrder is the fixed, deterministic priority order Drain walks:
// T0 before T1 before T2, exactly proposal §1 point 3's "FILO is banned
// for anything authoritative; strict priority across tiers."
var tierOrder = [3]Class{ClassT0Critical, ClassT1Batchable, ClassT2Coalescible}

// queuedCmd pairs a Command with the monotonic per-tier sequence number
// it was assigned at enqueue time — see this file's header comment on
// why physical location (memory vs disk) is NOT the source of order.
type queuedCmd struct {
	seq int64
	cmd protocol.Command
}

// tierQueue is the hybrid memory+disk FIFO backing the T0 and T1 tiers
// (this file's header comment has the full design). diskRoot == ""
// means "T0 mode": disk spilling is disabled outright, and enqueueLocked
// rejects (rather than spills) once memory is full.
//
// tierQueue is only ever reached through QueuedTransport's own t0/t1
// pointer fields (never copied or exposed outside this package), but it
// carries the same shape every other SEC-020-guarded type in this
// codebase does — a mutex plus aliasable reference state (memory, a
// disk-path string used for real filesystem I/O) — so it gets the same
// self-identity copy guard (checkNotCopied) InProcTransport/Engine/
// save.Manager/errs.Logger all use, applied at every real entry point
// (Depth, sendOrEnqueue, peek, commit) rather than the unexported
// *Locked helpers those call while already holding t.mu (the same
// "*Locked-suffixed helper" blind spot this project already treats as
// safe — see internal/foundation/errs/copyguard_test.go's rotateLocked
// precedent).
type tierQueue struct {
	mu sync.Mutex

	memory []queuedCmd
	memCap int

	diskRoot string // "" disables disk spill (T0)

	nextSeq      int64 // next sequence number to assign on enqueue
	nextDrainSeq int64 // next sequence number Drain expects to commit

	exhaustedCode string // registry code when enqueue cannot happen at all

	// self is the SEC-020-class copy-identity guard — same pattern as
	// protocol.InProcTransport.self; see that field's doc comment for
	// the full rationale.
	self atomic.Pointer[tierQueue]
}

func newTierQueue(memCap int, diskRoot string, exhaustedCode string) *tierQueue {
	if memCap < 1 {
		memCap = 1
	}
	t := &tierQueue{memCap: memCap, diskRoot: diskRoot, exhaustedCode: exhaustedCode}
	// Stored once, here, before t is returned to any caller — mirrors
	// protocol.NewInProcTransport's self.Store timing (see
	// InProcTransport.self's doc comment).
	t.self.Store(t)
	return t
}

// checkNotCopied mirrors protocol.InProcTransport.checkNotCopied and
// QueuedTransport.checkNotCopied exactly: a lock-free identity check,
// safe to call before t.mu is ever touched.
func (t *tierQueue) checkNotCopied(correlationID string, method string) error {
	if t.self.Load() != t {
		return errs.New(ErrQueueTransportCopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// depthLocked reports this tier's total pending count (memory + disk)
// and how many of those are currently on disk. Caller must hold t.mu.
func (t *tierQueue) depthLocked() (total, onDisk int) {
	total = int(t.nextSeq - t.nextDrainSeq)
	onDisk = total - len(t.memory)
	if onDisk < 0 {
		onDisk = 0
	}
	return total, onDisk
}

// Depth reports this tier's total pending count and how many of those
// are on disk (0 for a T0 tier, which never spills).
func (t *tierQueue) Depth() (total, onDisk int) {
	if err := t.checkNotCopied(errs.NewCorrelationID(), "Depth"); err != nil {
		return 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.depthLocked()
}

// compactMemoryLocked releases memory's backing array once its unused
// prefix slack grows large — repeated `t.memory = t.memory[1:]` pops
// (commitLocked) never shrink the underlying array on their own, so a
// long-running queue that stays mostly-drained would otherwise retain an
// ever-growing backing array purely as slack. Cheap (a single copy) and
// only triggered once slack clearly dominates live entries, so it never
// runs on every commit.
func (t *tierQueue) compactMemoryLocked() {
	if cap(t.memory)-len(t.memory) <= t.memCap*2 {
		return
	}
	fresh := make([]queuedCmd, len(t.memory), t.memCap)
	copy(fresh, t.memory)
	t.memory = fresh
}

// enqueueLocked assigns cmd the next sequence number and either appends
// it to the in-memory window (room available) or spills it to disk
// (window full, disk spill enabled). Caller must hold t.mu.
func (t *tierQueue) enqueueLocked(correlationID string, cmd protocol.Command) error {
	if len(t.memory) < t.memCap {
		seq := t.nextSeq
		t.memory = append(t.memory, queuedCmd{seq: seq, cmd: cmd})
		t.nextSeq++
		return nil
	}
	if t.diskRoot == "" {
		return errs.New(t.exhaustedCode, correlationID, map[string]any{"kind": string(cmd.Kind)})
	}
	seq := t.nextSeq
	if err := writeSegment(t.diskRoot, seq, cmd, correlationID); err != nil {
		return err
	}
	t.nextSeq++
	return nil
}

// peekLocked returns the command at nextDrainSeq WITHOUT removing it —
// Drain (queue.go) must confirm the inner transport actually accepted
// the command before this tier commits to having sent it (commitLocked),
// so a command is never lost to a subsequent inner-transport failure
// between peek and commit. Caller must hold t.mu.
//
// Worked example of why physical location can diverge from arrival
// order (referenced by this file's header comment): memCap=2, tier
// starts empty. Enqueue three commands (seq 0,1,2): 0 and 1 land in
// memory (window full), 2 spills to disk. Drain commits seq 0 then seq 1
// (both from memory) — memory is now empty, nextDrainSeq=2. A FOURTH
// command (seq 3) then enqueues: memory has room again, so seq 3 lands
// in MEMORY even though seq 2 is still sitting on disk, uncommitted.
// peekLocked at this point must still return seq 2 (from disk), not seq
// 3 (memory's only entry) — which is exactly what comparing
// memory[0].seq against nextDrainSeq (rather than just returning
// whatever is in memory) guarantees.
func (t *tierQueue) peekLocked(correlationID string) (protocol.Command, bool, error) {
	if t.nextDrainSeq >= t.nextSeq {
		return protocol.Command{}, false, nil
	}
	if len(t.memory) > 0 && t.memory[0].seq == t.nextDrainSeq {
		return t.memory[0].cmd, true, nil
	}
	cmd, err := readSegment(t.diskRoot, t.nextDrainSeq, correlationID)
	if err != nil {
		return protocol.Command{}, false, err
	}
	return cmd, true, nil
}

// commitLocked removes the command peekLocked most recently returned
// (nextDrainSeq) — from memory's front if that's where it lives,
// otherwise deleting its disk segment — and advances nextDrainSeq.
// Caller must hold t.mu, and must only call this after successfully
// handing the peeked command to the inner transport.
func (t *tierQueue) commitLocked() {
	seq := t.nextDrainSeq
	if len(t.memory) > 0 && t.memory[0].seq == seq {
		t.memory = t.memory[1:]
		t.compactMemoryLocked()
	} else {
		removeSegment(t.diskRoot, seq)
	}
	t.nextDrainSeq++
}

// commit is commitLocked's guarded, self-locking entry point — Drain
// (via QueuedTransport.commitTier) calls it standalone, exactly as peek
// wraps peekLocked below.
func (t *tierQueue) commit(correlationID string) {
	if err := t.checkNotCopied(correlationID, "commit"); err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commitLocked()
}

// sendOrEnqueue is SendCommand's per-tier entry point: if this tier
// currently has no backlog at all, it tries the inner transport directly
// (no added latency in the common, unloaded case); if that fails with
// ErrCommandQueueFull, OR the tier already had a backlog (a direct send
// would jump ahead of already-queued commands, breaking FIFO order), it
// enqueues instead. Any OTHER inner-transport error (e.g.
// ErrTransportClosed) is a genuine rejection and propagates immediately
// — it is never queued, since queuing it could never succeed either.
//
// Holding t.mu across the inner.SendCommand call is safe: every
// Transport.SendCommand implementation in this codebase (InProcTransport,
// and per its own doc comment, a future gRPC client stub) is
// non-blocking by contract, so this can never stall behind a slow
// reader.
func (t *tierQueue) sendOrEnqueue(inner protocol.Transport, correlationID string, cmd protocol.Command) error {
	if err := t.checkNotCopied(correlationID, "sendOrEnqueue"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.nextDrainSeq == t.nextSeq {
		err := inner.SendCommand(cmd)
		if err == nil {
			return nil
		}
		if !errors.Is(err, protocol.ErrCommandQueueFull) {
			return err
		}
	}
	return t.enqueueLocked(correlationID, cmd)
}

// coalesceSlot backs the T2 tier: at most one pending command, latest
// overwrites earlier (proposal §3's "safe to drop intermediate frames").
// generation is bumped on every Set so Commit can detect and refuse to
// clear a slot that was overwritten by a NEWER command between a Drain
// call's Peek and Commit — otherwise a fast-arriving replacement command
// could be silently discarded by a commit meant for the one it replaced.
type coalesceSlot struct {
	mu         sync.Mutex
	has        bool
	generation int64
	cmd        protocol.Command
}

// Set overwrites (or sets) the pending T2 command. Never fails — that is
// T2's entire contract (proposal §3).
func (s *coalesceSlot) Set(cmd protocol.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.has = true
	s.generation++
	s.cmd = cmd
}

// Peek returns the pending command (if any) and the generation it was
// set at, without clearing it.
func (s *coalesceSlot) Peek() (protocol.Command, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd, s.generation, s.has
}

// Commit clears the slot IFF it is still holding the same generation
// Peek returned — see the type's doc comment for why.
func (s *coalesceSlot) Commit(generation int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.has && s.generation == generation {
		s.has = false
	}
}

// Depth reports whether a T2 command is currently pending.
func (s *coalesceSlot) Depth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.has
}

// Config configures a QueuedTransport. T0MemCap/T1MemCap default (via
// NewQueuedTransport) to DefaultT0MemCap/DefaultT1MemCap when <= 0.
// DiskRoot is required — the T1 tier's overflow segments (queue_disk.go)
// live under DiskRoot/t1.
type Config struct {
	DiskRoot string
	T0MemCap int
	T1MemCap int
}

// Default in-memory high-water marks — chosen to be generously above
// realistic single-tick UI/harness command bursts (skeleton-era traffic,
// per protocol.DefaultCommandBuffer's own doc comment) while still small
// enough that the disk-spill path (T1) and the reject-outright path (T0)
// are both easy to exercise in tests. Revisit once perf runs put real
// numbers on burst sizes, exactly as protocol.DefaultCommandBuffer's own
// comment already flags for the buffers this queue sits behind.
const (
	DefaultT0MemCap = 64
	DefaultT1MemCap = 256
)

// QueuedTransport wraps an inner protocol.Transport, implementing
// Transport itself, and never fails SendCommand with
// protocol.ErrCommandQueueFull: when the inner transport can't accept a
// command right now, QueuedTransport queues it (proposal §2's "queue/
// overflow layer wraps protocol.Transport" seam) instead of rejecting it,
// per tier (this file's header comment). Results/Events/Deltas/Close
// pass straight through to inner — this increment's queue is inbound
// (SendCommand) only; InProcTransport already has its own outbound
// evict-oldest policy for Results/Events/Deltas (protocol/transport.go).
type QueuedTransport struct {
	inner protocol.Transport

	t0 *tierQueue
	t1 *tierQueue
	t2 *coalesceSlot

	// self is the SEC-020-class copy guard — identical pattern and
	// identical rationale to protocol.InProcTransport.self (see that
	// type's doc comment for the full argument): QueuedTransport has
	// both mutex-guarded state (t0/t1's own internal mu, t2's mu) and
	// aliasable reference fields (inner, t0, t1, t2 are all
	// pointers/interfaces), so a struct copy of *QueuedTransport shares
	// all of that aliased state while gaining nothing from it — every
	// exported method is guarded to fail closed on a copy via
	// checkNotCopied, called before any lock is ever touched.
	self atomic.Pointer[QueuedTransport]

	// drainMu serialises Drain calls against EACH OTHER across the WHOLE
	// peek -> inner.SendCommand -> commit critical section (BUG-class
	// P0, destructive REJECT on increment 2's queue review, 2026-08-18):
	// peekHighestPriority/q.inner.SendCommand/commitTier used to run as
	// three SEPARATE lock acquisitions per tier (peek takes+releases
	// tierQueue.mu, the inner send happens with NO lock held at all,
	// commit takes+releases tierQueue.mu again), so two goroutines both
	// calling Drain could interleave: A peeks seq N, releases the tier
	// lock; B peeks the SAME seq N (A hasn't committed yet) and sends it
	// to inner; A then also sends its (already-peeked) copy of N to
	// inner; both A and B commit, advancing nextDrainSeq by TWO for a
	// single logical command — the result is simultaneously a double
	// delivery of seq N (sent to inner twice) AND a silent loss of
	// whatever seq N+1 lands on after that double advance (its
	// commit never happens, so it is skipped over and orphaned in the
	// tier's memory/disk backing store forever, with Depth eventually
	// going negative once nextDrainSeq outruns nextSeq). Repro: 500 T1
	// Buy commands enqueued, 4 goroutines each looping Drain(1) — 501
	// delivered (one command double-sent), 212 vanished, Depth().T1 ==
	// -1.
	//
	// The fix holds drainMu for a Drain call's ENTIRE body (every
	// iteration's peek+send+commit, not just one command), which is
	// simpler than a per-command lock/unlock and gives the same
	// guarantee: whichever goroutine is inside Drain owns the ENTIRE
	// peek-send-commit sequence for every command it processes,
	// uninterrupted by any other goroutine's Drain call, so
	// nextDrainSeq only ever advances once per actually-delivered
	// command and a peeked-but-not-yet-committed command can never be
	// independently re-peeked and re-sent by a second concurrent
	// drainer.
	//
	// drainMu is DELIBERATELY separate from every tierQueue's own mu and
	// from t2's mu: those per-tier mutexes still exist to guard
	// SendCommand's enqueue path (sendOrEnqueue), which must stay fully
	// concurrent with an in-flight Drain — an enqueuing caller only ever
	// needs a brief tier-local critical section (append to memory / spill
	// to disk), never drainMu, so producers are never blocked behind a
	// (potentially slow, disk-touching) drain. Only Drain-vs-Drain
	// contends on drainMu; Drain-vs-SendCommand still only contends on
	// the relevant tier's own mu, exactly as before this fix.
	//
	// Backpressure interaction: when q.inner.SendCommand returns
	// ErrCommandQueueFull mid-drain, Drain returns immediately WITHOUT
	// calling commitTier — the peeked command was never removed from its
	// tier (peek never mutates state, only commit does), so it is still
	// sitting at nextDrainSeq for the very next Drain call (by this or
	// any other goroutine, once drainMu is released) to peek and retry.
	// Nothing about drainMu changes that contract; it just guarantees no
	// OTHER goroutine can interleave a peek of that same not-yet-committed
	// command while this Drain call still holds it.
	drainMu sync.Mutex
}

// NewQueuedTransport constructs a QueuedTransport wrapping inner, with
// the T1 tier's overflow segments rooted at cfg.DiskRoot. cfg.T0MemCap/
// cfg.T1MemCap <= 0 fall back to DefaultT0MemCap/DefaultT1MemCap.
func NewQueuedTransport(inner protocol.Transport, cfg Config) *QueuedTransport {
	t0Cap := cfg.T0MemCap
	if t0Cap <= 0 {
		t0Cap = DefaultT0MemCap
	}
	t1Cap := cfg.T1MemCap
	if t1Cap <= 0 {
		t1Cap = DefaultT1MemCap
	}

	q := &QueuedTransport{
		inner: inner,
		t0:    newTierQueue(t0Cap, "", ErrT0QueueExhausted),
		t1:    newTierQueue(t1Cap, cfg.DiskRoot, ""),
		t2:    &coalesceSlot{},
	}
	// Stored exactly once, here, before q is returned to any caller —
	// mirrors protocol.NewInProcTransport's self.Store timing exactly
	// (see InProcTransport.self's doc comment for why that ordering
	// matters).
	q.self.Store(q)
	return q
}

// checkNotCopied mirrors protocol.InProcTransport.checkNotCopied
// exactly: a lock-free identity check, safe to call before ANY of
// QueuedTransport's mutexes are ever touched.
func (q *QueuedTransport) checkNotCopied(correlationID string, method string) error {
	if q.self.Load() != q {
		return errs.New(ErrQueueTransportCopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// SendCommand implements protocol.Transport. It validates cmd exactly as
// protocol.InProcTransport.SendCommand does, classifies it (ClassOf),
// and routes it through that tier's sendOrEnqueue (T0/T1) or Set (T2).
// It returns an error only when cmd is invalid, the inner transport
// rejects it for a reason OTHER than being full (e.g. closed), or the
// command genuinely could not be enqueued (T0 exhausted, T1 disk-spill
// failure) — never for ordinary backpressure, which this method absorbs
// into the queue instead of surfacing to the caller (proposal §4:
// "backpressure, never silent drop" — from the CALLER's perspective, a
// queued command is accepted, not dropped nor rejected).
func (q *QueuedTransport) SendCommand(cmd protocol.Command) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	correlationID := string(cmd.CorrelationID)
	if err := q.checkNotCopied(correlationID, "SendCommand"); err != nil {
		return err
	}

	switch ClassOf(cmd.Kind) {
	case ClassT0Critical:
		return q.t0.sendOrEnqueue(q.inner, correlationID, cmd)
	case ClassT2Coalescible:
		q.t2.Set(cmd)
		return nil
	default: // ClassT1Batchable, and any future class defaulting per ClassOf
		return q.t1.sendOrEnqueue(q.inner, correlationID, cmd)
	}
}

// DrainStats reports what one Drain call did, per tier — Sent[i]
// corresponds to tierOrder[i] (T0, T1, T2).
type DrainStats struct {
	Sent [3]int
}

// Total reports the total number of commands this DrainStats represents
// across every tier.
func (d DrainStats) Total() int {
	return d.Sent[0] + d.Sent[1] + d.Sent[2]
}

// Drain is the backpressure signal's OTHER half (Depth/Backpressure
// below report state; Drain is what a tick driver calls to make
// progress catching the queue up): it repeatedly takes the
// highest-priority pending command (T0 first, then T1, then T2) and
// offers it to the inner transport, committing that tier's dequeue only
// after the inner transport accepts it. It stops when: budget commands
// have been sent (budget <= 0 means unlimited — drain everything
// currently pending, but never blocks waiting for MORE to arrive); every
// tier is empty; the inner transport reports
// protocol.ErrCommandQueueFull (ordinary backpressure — Drain returns
// normally, with whatever remains still queued, so the tick driver can
// slow down and call Drain again later, per proposal §4's "the sim
// applies backpressure... to CATCH UP"); or a tier reports a genuine
// error (e.g. ErrSpillReadFailed on a corrupt disk segment, or the inner
// transport being closed) — in which case Drain returns that error
// immediately, leaving every not-yet-drained command still safely
// queued (a failed drain attempt never loses anything already
// committed-pending).
func (q *QueuedTransport) Drain(budget int) (DrainStats, error) {
	var stats DrainStats
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Drain"); err != nil {
		return stats, err
	}

	// Serialise this ENTIRE Drain call's peek->send->commit sequence
	// against any other concurrent Drain call — see drainMu's doc
	// comment (above, on the QueuedTransport struct) for the full
	// rationale and the exact double-delivery/silent-loss failure mode
	// this closes. Deliberately does NOT touch any tier's own mu here —
	// SendCommand's enqueue path stays fully concurrent with a
	// in-flight Drain.
	q.drainMu.Lock()
	defer q.drainMu.Unlock()

	unlimited := budget <= 0
	for {
		if !unlimited && stats.Total() >= budget {
			return stats, nil
		}

		tierIdx, cmd, gen, ok, err := q.peekHighestPriority()
		if err != nil {
			return stats, err
		}
		if !ok {
			return stats, nil
		}

		if err := q.inner.SendCommand(cmd); err != nil {
			if errors.Is(err, protocol.ErrCommandQueueFull) {
				return stats, nil
			}
			return stats, err
		}

		q.commitTier(tierIdx, gen)
		stats.Sent[tierIdx]++
	}
}

// peekHighestPriority returns the next command to offer the inner
// transport, in strict tier priority (tierOrder), without committing any
// tier's dequeue. tierIdx indexes into tierOrder/DrainStats.Sent.
//
// gen carries the T2 coalesceSlot's PEEK-TIME generation (0 and unused for
// T0/T1, whose tierQueue tracks commit position itself via nextDrainSeq) --
// BUG-302 (Bro audit, 2026-08-20): this used to be discarded here and
// commitTier would re-peek T2 to recover a generation, which is always the
// CURRENT generation, not the one actually offered to (and accepted by) the
// inner transport a moment before. A SendCommand(B) landing in the
// coalesceSlot between this peek and Drain's eventual commit would then have
// its generation committed instead of A's -- silently discarding B, which
// was never sent to inner, in violation of T2's latest-wins contract (a
// stale command may be delivered at most once; a newer one may NEVER be
// lost). Carrying the peek-time generation through to Commit (see
// commitTier, coalesceSlot.Commit) closes that window: Commit refuses to
// clear a slot whose generation has since moved on, leaving the newer
// command intact for the NEXT Drain to peek and deliver.
func (q *QueuedTransport) peekHighestPriority() (tierIdx int, cmd protocol.Command, gen int64, ok bool, err error) {
	for i, tier := range tierOrder {
		switch tier {
		case ClassT0Critical:
			c, has, e := q.t0.peek(errs.NewCorrelationID())
			if e != nil {
				return i, protocol.Command{}, 0, false, e
			}
			if has {
				return i, c, 0, true, nil
			}
		case ClassT1Batchable:
			c, has, e := q.t1.peek(errs.NewCorrelationID())
			if e != nil {
				return i, protocol.Command{}, 0, false, e
			}
			if has {
				return i, c, 0, true, nil
			}
		case ClassT2Coalescible:
			c, g, has := q.t2.Peek()
			if has {
				return i, c, g, true, nil
			}
		}
	}
	return 0, protocol.Command{}, 0, false, nil
}

// peek is peekLocked with its own lock acquisition — Drain calls it
// standalone (not already holding t.mu), unlike sendOrEnqueue which
// holds the lock across both the empty-check and the direct-send
// attempt.
func (t *tierQueue) peek(correlationID string) (protocol.Command, bool, error) {
	if err := t.checkNotCopied(correlationID, "peek"); err != nil {
		return protocol.Command{}, false, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peekLocked(correlationID)
}

// commitTier commits the dequeue peekHighestPriority most recently
// returned for tierOrder[tierIdx]. gen is the T2 coalesceSlot generation
// peekHighestPriority captured at PEEK time (ignored for T0/T1, which track
// commit position internally via nextDrainSeq) -- see peekHighestPriority's
// doc comment (BUG-302) for why this must be the peek-time generation and
// never a re-peeked, possibly-newer one.
func (q *QueuedTransport) commitTier(tierIdx int, gen int64) {
	switch tierOrder[tierIdx] {
	case ClassT0Critical:
		q.t0.commit(errs.NewCorrelationID())
	case ClassT1Batchable:
		q.t1.commit(errs.NewCorrelationID())
	case ClassT2Coalescible:
		// Commit(gen) is a no-op (leaves the slot intact) if the slot's
		// CURRENT generation has moved past gen -- i.e. a newer
		// SendCommand landed after this peek but before this commit. That
		// newer command survives untouched for the next Drain to peek and
		// deliver, exactly coalesceSlot.Commit's doc comment's contract.
		q.t2.Commit(gen)
	}
}

// Depth reports current per-tier pending counts: T0/T1's total (memory +
// disk) and disk-only portion, and whether T2 currently holds a pending
// (not-yet-drained) command.
type Depth struct {
	T0       int
	T1       int
	T1OnDisk int
	T2       bool
}

// Depth returns the queue's current backlog, for a tick driver or
// monitoring surface to inspect.
func (q *QueuedTransport) Depth() Depth {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Depth"); err != nil {
		return Depth{}
	}
	t0, _ := q.t0.Depth()
	t1, t1Disk := q.t1.Depth()
	return Depth{T0: t0, T1: t1, T1OnDisk: t1Disk, T2: q.t2.Depth()}
}

// Backpressure reports whether the queue has reached a state where a
// tick driver should slow down rather than keep advancing at full speed
// (proposal §4's "the tick driver can slow rather than overrun" signal):
// true whenever ANY command has actually spilled to disk (T1OnDisk > 0
// — the unambiguous "we are behind" signal, since spilling only happens
// once the in-memory high-water mark is exceeded) or the T0 tier has any
// backlog at all (T0's contract is "never queued past the current
// tick," so ANY T0 backlog, however small, means the driver is already
// behind on its strictest tier).
func (q *QueuedTransport) Backpressure() bool {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Backpressure"); err != nil {
		// Fail conservative: a copied handle is an invalid state, so tell
		// the caller to slow down rather than report the all-clear a
		// zero-value Depth would otherwise imply.
		return true
	}
	d := q.Depth()
	return d.T1OnDisk > 0 || d.T0 > 0
}

// closedResultCh, closedEventCh, and closedDeltaCh are the "fail closed"
// values Results/Events/Deltas return instead of inner's real, aliased
// channel when called on a struct-copied QueuedTransport — mirrors
// protocol.InProcTransport's identical closedResultCh/closedEventCh/
// closedDeltaCh (see that type's doc comment for the full rationale). A
// receive on a closed channel always returns the zero value with
// ok=false, so a caller ranging over one of these exits immediately
// rather than blocking forever (a nil channel) or silently reading the
// original's live traffic (the real, aliased channel).
var (
	closedResultCh = closedChanOf[protocol.CommandResult]()
	closedEventCh  = closedChanOf[protocol.Event]()
	closedDeltaCh  = closedChanOf[protocol.Delta]()
)

// closedChanOf constructs and immediately closes a fresh, unbuffered
// channel of T. Used only to build the package-level closedXCh fallbacks
// above.
func closedChanOf[T any]() chan T {
	ch := make(chan T)
	close(ch)
	return ch
}

// Results implements protocol.Transport by passing through to inner —
// see this type's doc comment on why the outbound side is untouched by
// this increment.
func (q *QueuedTransport) Results() <-chan protocol.CommandResult {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Results"); err != nil {
		return closedResultCh
	}
	return q.inner.Results()
}

// Events implements protocol.Transport by passing through to inner.
func (q *QueuedTransport) Events() <-chan protocol.Event {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Events"); err != nil {
		return closedEventCh
	}
	return q.inner.Events()
}

// Deltas implements protocol.Transport by passing through to inner.
func (q *QueuedTransport) Deltas() <-chan protocol.Delta {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Deltas"); err != nil {
		return closedDeltaCh
	}
	return q.inner.Deltas()
}

// Close implements protocol.Transport by closing inner. Queued-but-not-
// yet-drained T1 disk segments are deliberately left in place (not
// cleaned up) — proposal §2/§7 designs the disk overflow log to double
// as the durable command log a later increment's crash recovery replays
// forward from, so Close must not destroy that history. In-memory-only
// backlog (T0, T1's memory window, T2) is unavoidably lost on Close,
// same as it would be on process exit — this increment does not persist
// the memory window, only the disk-spilled tail.
func (q *QueuedTransport) Close() error {
	if err := q.checkNotCopied(errs.NewCorrelationID(), "Close"); err != nil {
		return err
	}
	return q.inner.Close()
}

var _ protocol.Transport = (*QueuedTransport)(nil)
