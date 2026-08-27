package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is one structured log line: {ts, level, code, correlationId,
// module, msg, ctx} — the shape M0-ENG §3 specifies for logs/engine.ndjson
// and logs/ui.ndjson, one JSON object per line (NDJSON), so the F12 info
// panel's error tail can just tail-and-pretty-print.
type Entry struct {
	Ts            string         `json:"ts"`
	Level         string         `json:"level"`
	Code          string         `json:"code"`
	CorrelationID string         `json:"correlationId"`
	Module        string         `json:"module"`
	Msg           string         `json:"msg"`
	Ctx           map[string]any `json:"ctx,omitempty"`

	// Repeat is the number of ADDITIONAL times this exact Code was pushed
	// to an in-memory ringBuffer (`ring`/`copyRejectRing`) and coalesced
	// into this same slot, immediately following the occurrence this
	// Entry otherwise describes (SEC-030/SEC-031(b) — see ringBuffer.push's
	// doc comment). Zero (the default, omitted from JSON via omitempty)
	// means "seen exactly once, no coalescing" — the common case, and the
	// only case that ever reaches a FILE-backed Logger's NDJSON output
	// (Logger.Log never coalesces; only the in-memory rings do — see
	// ringBuffer.push). Ts/CorrelationID/Msg/Ctx/Level/Module on a
	// coalesced entry reflect the MOST RECENT occurrence, not the first,
	// so an operator reading "MET-U101 x4127" sees when it last fired,
	// not just when it started.
	Repeat int `json:"repeat,omitempty"`
}

// ErrLoggerCopied is returned by Log, SetClock, and Close when called on a
// Logger value that is not the one NewLogger/NewFileLogger constructed —
// i.e. a struct copy (SEC-020 wave 2). See checkNotCopied's doc comment
// for the ordering rationale shared with every other SEC-020 guard in
// this codebase.
//
// Deliberately a PLAIN sentinel (errors.New), never an errs.New(...)-
// constructed *E, unlike every other SEC-020 copy-guard error in this
// codebase (Engine/SubscriptionServer/InProcTransport/solver.Registry
// all use errs.New). This is Logger's one load-bearing exception, and
// the reasoning is worth stating fully because it is easy to "fix" back
// to the house style without noticing why it was deliberate (ASM-074):
//
// errs.New always ends by calling logEntry(e), which writes the newly
// constructed error through the package-level sink Logger — the very
// type whose method this rejection is happening inside. If Log's copy
// check called errs.New(ErrLoggerCopied, ...) here, and the package-level
// sink (set via SetSink) ever happened to be — or alias — the specific
// copied Logger value that tripped this check, that call would recurse:
// logEntry -> sink.Log -> checkNotCopied fails again -> errs.New again ->
// logEntry again, unbounded, ending in a stack overflow inside the exact
// code path responsible for keeping the audit trail alive (GR#1's "log,
// don't lose" turned into a crash). In the common case sink is the
// ORIGINAL Logger, not a copy of it, so this recursion is not expected to
// fire in practice — but "not expected to fire in the common case" is
// precisely the property a copy-guard exists to stop trusting. A plain
// sentinel error has no logEntry call anywhere in its construction, so
// this path can never recurse regardless of what sink happens to point
// to. This mirrors log.go's own pre-existing convention: every OTHER
// error Log/rotateLocked/openAppend can return (MET-F010..F013, in
// data/errors.json) is already a plain fmt.Errorf-wrapped error, raised
// by errs.New only at whatever call site further up eventually reports
// it — Logger's own methods have never constructed a registry-sourced
// error directly, for the same underlying reason.
//
// The corollary is what happens to the Entry that Log() was asked to
// write when this fires: see Log's doc comment for why it is pushed
// directly into the in-memory ring buffer rather than simply dropped —
// the "does a copy silently swallow the audit trail" question this
// sentinel's doc comment poses is answered there, not here.
var ErrLoggerCopied = errors.New("errs: logger is a struct copy of another Logger value (construct via NewLogger/NewFileLogger and use that same pointer; do not copy the struct)")

// Logger writes Entry values as NDJSON to an underlying writer. It is
// safe for concurrent use. The zero value is NOT ready for use (SEC-020
// wave 2 — see self's doc comment below); construct with NewLogger or
// NewFileLogger.
type Logger struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time

	// File-backed rotation state; zero value (path == "") means Logger
	// wraps an arbitrary io.Writer and never rotates.
	path       string
	maxBytes   int64
	maxBackups int
	size       int64
	file       *os.File

	// self holds the address NewLogger/NewFileLogger gave this Logger at
	// construction (self.Store(l), set once, at the end of each
	// constructor, before l is returned to any caller — no goroutine can
	// have a reference to l to race that Store against).
	//
	// SEC-020 wave 2: Logger is exported and so are both constructors, so
	// any caller can dereference-and-copy a live *Logger ('l2 := *l' is
	// legal, unsafe-free, reflect-free Go). mu is a plain sync.Mutex
	// VALUE, so the copy l2 gets its OWN, independent mu — but w (an
	// io.Writer interface value, typically wrapping *os.File) and file
	// (*os.File, a pointer) still point at the SAME underlying file
	// descriptor as the original. That is the highest-consequence part
	// of this type's copy hazard, worse than the usual "aliased map/
	// slice" shape seen elsewhere in SEC-020: l2.rotateLocked() (reached
	// from l2.Log() under l2's OWN, independent mu, which the original's
	// concurrent writers know nothing about) can rename/reopen path out
	// from under the ORIGINAL's in-flight Log() calls, which are still
	// writing to l.file/l.w believing they hold exclusive access via
	// l.mu. Two independent mutexes "protecting" one shared *os.File is
	// not protection at all — every other SEC-020 finding in this
	// codebase is about a copy's own operations going wrong in
	// isolation; this one is about a copy corrupting the ORIGINAL's
	// otherwise-correct, otherwise-still-locked operations from the
	// outside.
	//
	// atomic.Pointer, not a plain *Logger field, for the SEC-016 ordering
	// reason repeated at every SEC-020 site in this codebase (see
	// internal/engine/core/engine.go's self field for the full writeup):
	// a struct copy taken while the ORIGINAL's mu happened to be held
	// captures those mutex bytes read as "locked" — the copy's own next
	// Lock() call on that captured state can then park forever, since
	// nothing will ever Unlock() that specific copy's address. The
	// identity check must therefore be race-safe and run BEFORE mu is
	// ever touched — a plain field read racing a concurrent struct copy
	// has no defined result under the Go memory model, but
	// atomic.Pointer's Load/Store do.
	self atomic.Pointer[Logger]
}

// NewLogger wraps an arbitrary io.Writer as an NDJSON sink. Rotation is
// not available for a bare writer (there is nothing to rename) — use
// NewFileLogger for a rotating file sink.
func NewLogger(w io.Writer) *Logger {
	l := &Logger{w: w, now: time.Now}
	// Stored exactly once, here, before l is returned to any caller — no
	// goroutine can have a reference to l to race this Store against
	// (SEC-020 wave 2; see self's doc comment above).
	l.self.Store(l)
	return l
}

// defaultMaxBackups is the "keep N=3" rotation depth from the task brief.
const defaultMaxBackups = 3

// NewFileLogger opens (creating/appending to) path as a rotating NDJSON
// sink: once the file reaches maxBytes, it is rotated to path+".1"
// (existing .1..maxBackups-1 shift up by one; anything beyond
// maxBackups is dropped) and a fresh file is opened at path.
//
// maxBytes <= 0 disables size-based rotation (the file grows unbounded).
// maxBackups <= 0 defaults to 3.
func NewFileLogger(path string, maxBytes int64, maxBackups int) (*Logger, error) {
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}
	f, size, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	l := &Logger{
		w:          f,
		now:        time.Now,
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		size:       size,
		file:       f,
	}
	// Stored exactly once, here, before l is returned to any caller — no
	// goroutine can have a reference to l to race this Store against
	// (SEC-020 wave 2; mirrors NewLogger — see self's doc comment).
	l.self.Store(l)
	return l, nil
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Logger value (SEC-020 wave 2, mirroring
// Engine.checkNotCopied/SubscriptionServer.checkNotCopied/
// InProcTransport.checkNotCopied/solver.Registry.checkNotCopied).
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not l.mu — so it is safe and correct to call BEFORE l.mu
// is ever touched. That ordering is not optional (SEC-016): a struct
// copy's mu can be byte-for-byte "currently locked" if the copy was
// taken while the original's mu was held, and acquiring — even just
// attempting — a copy's own mu in that state can block forever, since
// nothing will ever Unlock() that specific copy's address. Rejecting the
// copy here, before Lock() is ever called, means that hang path is never
// reached at all — on top of the file-corruption hazard self's doc
// comment on the Logger struct describes, which this same pre-lock
// rejection also prevents from ever reaching rotateLocked.
//
// A nil l.self.Load() (a Logger constructed as a bare
// `Logger{}`/`new(Logger)` rather than via NewLogger/NewFileLogger, so
// self was never stored) is treated the same as a mismatch and rejected
// the same way — every documented construction path is one of those two
// constructors, so an unset self is itself a misuse this same rejection
// correctly names, and rejecting it here also means such a value's
// zero-value nil now func is never reached either (Log would otherwise
// panic calling a nil l.now()).
func (l *Logger) checkNotCopied() bool {
	return l.self.Load() == l
}

func openAppend(path string) (*os.File, int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// SetClock overrides the logger's timestamp source. Per M0-ENG §1.1,
// engine simulation code must never call the wall clock directly on the
// tick path — inject the sim clock's Now method here for any logger the
// engine writes through. Defaults to time.Now.
//
// SEC-020 wave 2: identity-checked BEFORE l.mu is touched (pre-lock,
// load-bearing — see checkNotCopied's doc comment) and again immediately
// after l.mu is acquired (defence in depth). Called on a struct-copied
// Logger, this is a silent no-op — SetClock has no error return to carry
// a rejection through (matching InProcTransport.Close's "no return value
// to carry an error through" precedent) and, unlike Log, has no Entry to
// preserve on the reject path, so there is nothing here for the ring-
// buffer fallback (see Log's doc comment) to apply to. The observable
// consequence is on the ORIGINAL: its l.now is simply never overridden by
// a call made against a copy — proven directly in the test suite by
// asserting the original's next Log() still timestamps via whatever
// clock it already had.
func (l *Logger) SetClock(now func() time.Time) {
	if !l.checkNotCopied() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.checkNotCopied() {
		return
	}
	l.now = now
}

// Log writes one NDJSON line for e. If e.Ts is empty it is filled in
// from the logger's clock. Safe for concurrent use.
//
// SEC-020 wave 2 / ASM-074: identity-checked BEFORE l.mu is touched
// (pre-lock, load-bearing — see checkNotCopied's doc comment, and
// self's doc comment on the Logger struct for why a copy's Log() must
// never reach l.mu at all: it can corrupt the ORIGINAL's in-flight
// writes via the shared *os.File, not just hang) and again immediately
// after l.mu is acquired (defence in depth).
//
// A rejected call returns ErrLoggerCopied — a plain sentinel, not an
// errs.New(...)-constructed error; see ErrLoggerCopied's doc comment for
// why routing this specific rejection through errs.New would risk
// unbounded recursion through the package-level sink.
//
// Per ASM-074's judgement call: e is NOT simply discarded on rejection.
// It is pushed directly into the package-level in-memory ring buffer
// (the same ring Recent() reads, and the same ring logEntry already
// falls back to whenever a configured sink's Log call fails for any
// other reason) so the audit trail does not go silent just because the
// caller happened to be holding a copy — the entry is still visible via
// Recent()/the F12 debug tail, only demoted from "on disk" to
// "in-memory, best-effort" exactly as an ordinary write failure already
// is. This is deliberately NOT gated behind whether e came in via the
// errs.New/Wrap auto-logging path or a direct caller of Log(): both get
// the same fail-safe, because a directly-called Log() has no other
// reporting channel of its own either.
func (l *Logger) Log(e Entry) error {
	if !l.checkNotCopied() {
		return l.rejectCopiedLog(e)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.checkNotCopied() {
		return l.rejectCopiedLog(e)
	}

	if e.Ts == "" {
		e.Ts = l.now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal log entry: %w", err)
	}
	line = append(line, '\n')

	// BUG-307 issue 1: If a previous rotation failed, l.file may be nil.
	// In that case, l.w is also nil and we cannot write to a nil interface.
	// Return an error indicating logging is temporarily unavailable, but do not panic.
	if l.w == nil {
		return fmt.Errorf("logger write unavailable: previous rotation failed")
	}

	n, err := l.w.Write(line)
	if err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}

	// Detect short writes: if fewer bytes were written than requested,
	// that is a write failure and must be reported (BUG-307 issue 2).
	if n != len(line) {
		return fmt.Errorf("short write: wrote %d bytes of %d", n, len(line))
	}

	if l.file != nil {
		l.size += int64(n)
		if l.maxBytes > 0 && l.size >= l.maxBytes {
			if rerr := l.rotateLocked(); rerr != nil {
				return fmt.Errorf("rotate %s: %w", l.path, rerr)
			}
		}
	}

	return nil
}

// rejectCopiedLog is Log's fail-closed path for a struct-copied receiver
// (ASM-074) — split out so both the pre-lock and post-lock checkNotCopied
// failures in Log go through identical handling. It never touches l.mu
// or any other field of l: only the package-level copyRejectRing, which
// is safe to reach from a copy because it is addressed via the
// package-level `copyRejectRing` variable, never through the receiver
// itself. See Log's doc comment for the reasoning; see ErrLoggerCopied's
// doc comment for why this does not route through errs.New.
//
// SEC-031 part 2 (ASM-105): pushes into copyRejectRing, NOT the genuine
// `ring` Recent() reads. Two independent problems motivated this split,
// both confirmed: (1) when this Logger is the package-level sink,
// logEntry's own post-failure fallback ALSO pushed e into `ring` a
// second time whenever it saw the non-nil error this method returns —
// double-counting one real event against the audit trail's finite
// capacity (logEntry now special-cases ErrLoggerCopied to skip that
// second push, since this method's push already recorded it — see
// logEntry's doc comment); (2) even without that double-push, a copy
// hammering Log() in a loop shared the SAME finite, unquota'd `ring` as
// genuine entries, so it could evict real audit history at whatever rate
// it could call Log() — a guard whose own fail-safe path degrades the
// evidence it exists to preserve. Routing to a wholly separate ring
// fixes both at once: one push per event, and a hammered copy can only
// ever evict OTHER copy-rejection entries (see RecentCopyRejections),
// never a genuine one.
func (l *Logger) rejectCopiedLog(e Entry) error {
	if e.Ts == "" {
		// BUG-278: timestamp via the package-level injectable clock
		// (errs.go now()/SetClock), NOT time.Now() directly. This path is
		// reachable from every errs.New via the package sink, so a raw wall-
		// clock read here breaks sim-clock determinism (GR#21/M0-ENG §1.1)
		// and made this a fourth time.Now site beyond the three injectable-
		// clock defaults. now() reads only the package clock var — it never
		// touches l, so it stays safe to call from a struct-copied receiver.
		e.Ts = now().UTC().Format(time.RFC3339Nano)
	}
	copyRejectRing.push(e)
	return ErrLoggerCopied
}

// rotateLocked performs the rename chain (path.N-1 -> path.N, ...,
// path -> path.1) and reopens a fresh file at path. Caller must hold l.mu.
// BUG-307 issue 1 fix: Ensure that if rotation fails partway through (e.g.,
// rename fails or open fails), l.file and l.w are left in a usable state —
// either still pointing to a valid open file, or set to nil. This prevents
// subsequent writes from panicking or writing to a closed file descriptor.
func (l *Logger) rotateLocked() error {
	oldFile := l.file

	// Close the old file first (required on Windows; files cannot be renamed
	// while open). If close fails, return early without touching l.file/l.w.
	if err := oldFile.Close(); err != nil {
		return err
	}

	oldest := fmt.Sprintf("%s.%d", l.path, l.maxBackups)
	_ = os.Remove(oldest) // fine if it doesn't exist

	for i := l.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		dst := fmt.Sprintf("%s.%d", l.path, i+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				// Rotation rename failed. At this point, the old file is closed
				// but we couldn't rename it to path.1. This is an error condition.
				// We cannot proceed. l.file is still the old closed file pointer.
				// To prevent subsequent writes from panicking, we set l.file to nil.
				l.file = nil
				l.w = nil
				return err
			}
		}
	}

	if err := os.Rename(l.path, l.path+".1"); err != nil {
		// Final rename failed. Same situation: old file is closed, path still
		// exists and holds the current log data. l.file is the closed pointer.
		l.file = nil
		l.w = nil
		return err
	}

	// Rotation successful so far. Now open the new file.
	f, size, err := openAppend(l.path)
	if err != nil {
		// Open failed. At this point:
		// - Old file has been closed (cannot reopen it — the file was already
		//   rotated to path.1, so the original file descriptor is gone)
		// - The file system now has path rotated to path.1 with old data
		// - We cannot open a new path (openAppend failed)
		// Set l.file/l.w to nil so Log() can detect this and degrade gracefully.
		l.file = nil
		l.w = nil
		return err
	}

	// Success: new file opened. Update pointers.
	l.file = f
	l.w = f
	l.size = size
	return nil
}

// Close closes the underlying file, if this Logger owns one.
//
// SEC-020 wave 2: identity-checked BEFORE l.mu is touched (pre-lock,
// load-bearing) and again immediately after l.mu is acquired (defence in
// depth) — same ordering as Log/SetClock. This is the load-bearing guard
// for the file-corruption hazard described on the Logger struct's self
// field: a copy's Close() must never run, because it would close the
// SHARED *os.File out from under the ORIGINAL's in-flight Log() calls,
// which believe they hold exclusive access via l.mu — the copy only
// holds its own, independent, and irrelevant mu. Returns ErrLoggerCopied
// (a plain sentinel, not errs.New — see ErrLoggerCopied's doc comment)
// rather than silently no-op'ing, so a caller that mistakenly holds a
// copy finds out immediately rather than believing a Close that never
// happened.
func (l *Logger) Close() error {
	if !l.checkNotCopied() {
		return ErrLoggerCopied
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.checkNotCopied() {
		return ErrLoggerCopied
	}
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// --- package-level auto-log sink (errs.New/Wrap write through this) ---

const ringCapacity = 200

// copyRejectRingCapacity is deliberately smaller than ringCapacity and,
// more importantly, a SEPARATE buffer entirely (SEC-031 part 2 — see
// copyRejectRing's doc comment). It only needs to hold enough recent
// misuse hits to diagnose a copy-holding caller, not to compete with the
// genuine audit trail for space.
const copyRejectRingCapacity = 64

// ring is a fixed-capacity, mutex-protected circular buffer of the most
// recent log entries. It is the in-memory fallback the F12 debug tail
// reads via Recent() whenever no file sink is configured, or whenever a
// configured sink's write fails — see the "ring buffer" design note in
// docs/design/errors.md for why write failures also fall back here.
//
// Coalescing (SEC-030/SEC-031 part 2, made exact per SEC-033): push
// keeps at most one slot per distinct Code, keyed by a Code-indexed map
// (see ringBuffer.index and push's doc comment) rather than a bounded
// scan-back, so a repeating Code accumulates a Repeat count on its one
// slot instead of consuming a new one no matter how many OTHER distinct
// Codes interleave in between. This coalescing is entirely in-memory: a
// FILE-backed Logger's NDJSON output (Logger.Log) has no coalescing of
// its own and is completely unaffected — every Log() call still writes
// its own line — so the on-disk audit trail this ring is a fallback for
// never loses an occurrence either way.
//
// SEC-031 part 2: this ring holds ONLY genuine entries — a copied
// Logger's rejected Log()/SetSink misuse never lands here (see
// copyRejectRing below). Before this fix, rejectCopiedLog pushed
// directly into THIS ring, and logEntry's own post-failure fallback
// pushed the SAME entry into it a second time whenever the rejection was
// reached via the package-level sink (double-push, confirmed byte-
// identical since Ts is set once by construct() before either push) —
// and, independently, a copy hammering Log() in a tight loop could evict
// genuine already-logged entries out of this same finite, unquota'd
// buffer (confirmed: 500 rejected calls against a 50-capacity ring
// evicted a seeded genuine entry). A guard whose own fail-safe path
// degrades the evidence it exists to preserve is a real defect, not a
// cosmetic one — see copyRejectRing's doc comment for the fix (ASM-105).
type ringBuffer struct {
	mu    sync.Mutex
	buf   []Entry
	start int
	count int

	// index maps every Code currently held anywhere in buf to the exact
	// slot (an absolute index into buf, NOT an offset from start) holding
	// its one coalesced Entry (SEC-033, replacing the bounded scan-back —
	// see push's doc comment). Because push coalesces EXACTLY (any
	// currently-held entry with a matching Code, not just one within a
	// bounded window), at most one slot ever holds a given Code at a
	// time, so a single map entry per live Code is sufficient and the
	// invariant len(index) <= len(buf) <= ringCapacity always holds — the
	// index cannot itself become the unbounded resource (ASM-116),
	// because it is evicted in the same operation that evicts the ring
	// slot it points at, never on a separate schedule.
	index map[string]int
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{buf: make([]Entry, cap), index: make(map[string]int, cap)}
}

// push adds e to the ring, UNLESS an entry with the same Code is ALREADY
// held anywhere in the ring — in which case e is coalesced into that
// existing slot instead of consuming a new one (SEC-030 / SEC-031 part
// 2, ASM-106; made exact, replacing the bounded scan-back, per SEC-033
// / ASM-116).
//
// Mechanism: a Code-keyed index (map[string]int, see ringBuffer.index)
// gives push an O(1) lookup for "does this Code already have a slot,"
// rather than scanning any bounded number of recently-pushed slots. This
// closes the gap the previous design could not, by construction: the old
// `coalesceScanBack` scan only found a match within the K
// most-recently-pushed slots, so more than K distinct Codes interleaved,
// or the same Code recurring more than K entries apart, silently stopped
// coalescing and degraded toward pre-fix (no coalescing at all)
// behaviour — and K's "safe" value depended on a population (every
// registry-sourced error the ring is a fallback for) that had already
// been shown to be miscounted twice in one week (SEC-033). Keying
// coalescing by Code removes that population-vs-bound relationship from
// the design entirely: there is no K to outgrow, so no re-derivation is
// ever needed again. See TestRing_ManyDistinctCodes_ExactCoalescing for
// the direct proof this closes the scan-back gap.
//
// Eviction stays exactly as it was — oldest slot overwritten, start
// advanced by one, capacity (ringCapacity) unchanged — except that when
// an occupied slot is about to be overwritten, its Code's index entry is
// deleted FIRST, in the same critical section, so the index is never
// left pointing at a slot that no longer holds the Code it claims to
// (AC-5): a snapshot taken at any point reflects buf/start/count exactly
// as before this change, and the index is purely an accelerator over
// that same state, never a second source of truth for it.
//
// Why the prior bounded scan-back was accepted at all, and why a map
// costs more but is the correct trade here: this is a diagnostic,
// in-memory buffer, not a security boundary (Bill's framing, carried
// over unchanged from ASM-106/ASM-107) — coalescing trades exact
// per-occurrence timestamps for a bounded, always-populated ring under
// flood conditions. A map lookup/insert on every push is the standard,
// well-understood cost of exact coalescing (allocation and bookkeeping,
// no unbounded growth — see ringBuffer.index's doc comment), judged
// acceptable for this non-security-boundary path now that it closes a
// real gap rather than merely narrowing it (SEC-033's ruling). It never
// touches a FILE-backed Logger's NDJSON output (Logger.Log has no
// coalescing of its own; only ringBuffer does), so the on-disk audit
// trail this ring is a fallback for is unaffected either way.
func (r *ringBuffer) push(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if idx, ok := r.index[e.Code]; ok {
		last := &r.buf[idx]
		last.Repeat++
		last.Ts = e.Ts
		last.Level = e.Level
		last.CorrelationID = e.CorrelationID
		last.Module = e.Module
		last.Msg = e.Msg
		last.Ctx = e.Ctx
		return
	}

	idx := (r.start + r.count) % len(r.buf)
	if r.count < len(r.buf) {
		r.count++
	} else {
		// Full: idx == r.start, about to be overwritten. Drop its
		// Code's index entry FIRST, in this same critical section, so
		// the index is never left pointing at a slot no longer holding
		// that Code (AC-5) — no window exists where a lookup could
		// return a stale idx.
		delete(r.index, r.buf[idx].Code)
		r.start = (r.start + 1) % len(r.buf)
	}
	r.buf[idx] = e
	r.index[e.Code] = idx
}

func (r *ringBuffer) snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, r.count)
	for i := 0; i < r.count; i++ {
		src := r.buf[(r.start+i)%len(r.buf)]
		out[i] = src

		// BUG-307 issue 3 fix: Deep-copy the Ctx map to avoid aliasing the
		// live ring's state. Maps are reference types in Go, so a struct copy
		// (out[i] = src) only copies the interface{} wrapper, not the backing
		// map. Any mutation to the live ring's map would be visible in the
		// snapshot, and vice versa. We must make a defensive copy of the map.
		if src.Ctx != nil {
			out[i].Ctx = make(map[string]any, len(src.Ctx))
			for k, v := range src.Ctx {
				out[i].Ctx[k] = v
			}
		}
	}
	return out
}

var (
	sinkMu sync.Mutex
	sink   *Logger
	ring   = newRingBuffer(ringCapacity)

	// copyRejectRing (SEC-031 part 2) is a SEPARATE, smaller ring holding
	// ONLY entries that were logged solely because Log() or SetSink was
	// called against a struct-copied Logger (SEC-020/ASM-074). Kept apart
	// from `ring` specifically so a copy-holding caller — whether hammering
	// Log() directly or having been installed via a not-yet-guarded SetSink
	// — can never evict genuine audit entries out of the buffer Recent()
	// reads: the two rings have independent, unrelated capacities, so
	// filling one has zero effect on the other. Read via
	// RecentCopyRejections(), never Recent().
	copyRejectRing = newRingBuffer(copyRejectRingCapacity)
)

// SetSink configures the package-level ErrorSink that every error
// constructed by New/Wrap is automatically logged through. Pass nil to
// go back to the in-memory-only ring buffer (e.g. in tests).
//
// SEC-031 part 1: identity-checked before installing a non-nil l. This
// is the one SEC-020 guard on *Logger that could not be found by this
// initiative's standard enumeration technique (grep every `func (x *T)`
// method and cross-check against lock sites) — SetSink takes *Logger as
// an ARGUMENT, not as the receiver, so it fell outside every method-
// shaped sweep, across all nine SEC-020 types, by four different agents,
// nine separate times (Bill's finding, logged as BUG-024 input: this is
// the strongest argument yet that this class of check belongs in a
// mechanical gate rather than a procedure). Confirmed exploitable: handing
// SetSink a byte-copied *Logger and then making 20 errs.New calls put
// zero bytes on the real logger's writer, with no error, no panic, and no
// signal anywhere — persistent logging silently and permanently defeated
// for the rest of the process.
//
// Fail-closed shape (ASM-104): SetSink now RETURNS an error, mirroring
// solver.Registry.Register/SetFailoverHook's SEC-020 wave 2 fix rather
// than Logger's own receiver methods (which mostly fail closed silently,
// because SEC-020 wave 2's brief judged a receiver method's caller to
// often have no better recourse than the type's own existing error
// contract). SetSink is different: it is a one-shot, deliberate,
// almost-always-boot-time configuration call with exactly one caller per
// process in practice, so there is no good reason to hide a copy-attack
// (or a plain wiring bug) from that caller behind a silent no-op the way
// a hot-path accessor might reasonably need to. A rejected l installs
// NOTHING — the previously configured sink (nil or a real Logger) is left
// exactly as it was, never cleared and never replaced by the copy, so a
// rejected SetSink call can never make logging silently WORSE than it
// already was.
func SetSink(l *Logger) error {
	if l != nil && !l.checkNotCopied() {
		return ErrLoggerCopied
	}
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sink = l
	return nil
}

// Recent returns a snapshot of the most recent (up to 200) GENUINE log
// entries held in the in-memory ring buffer — populated whenever no file
// sink is configured, and as a fail-safe whenever a configured sink's
// write fails. This is the primitive the F12 debug info panel's error
// tail (M0-ENG §3) reads. Since SEC-031, this deliberately excludes
// copy-misuse entries — see RecentCopyRejections for those.
func Recent() []Entry {
	return ring.snapshot()
}

// RecentCopyRejections returns a snapshot of the most recent (up to
// copyRejectRingCapacity) log entries that were dropped solely because
// Log() was called against a struct-copied Logger (SEC-020/ASM-074) —
// kept in copyRejectRing, entirely separate from the genuine audit trail
// Recent() serves (SEC-031 part 2). This is a diagnostic surface for
// finding a copy-holding caller, not part of the F12 audit tail; a
// caller hammering Log() against a copy can fill and churn THIS buffer
// as fast as it likes without touching Recent()'s genuine entries at
// all — that isolation is the entire point of the fix.
func RecentCopyRejections() []Entry {
	return copyRejectRing.snapshot()
}

// logEntry is the single choke point every error constructed by New/Wrap
// passes through: write to the configured sink if there is one, and fall
// back to the ring buffer if there isn't one, or if the sink write fails.
// Errors are stored, never printed-and-lost (GR#1).
//
// SEC-031 part 2: when s.Log(e) fails specifically with ErrLoggerCopied,
// this does NOT also push e into `ring` — Log's own rejectCopiedLog path
// already recorded e in copyRejectRing (see that method's doc comment).
// Before this fix, this fallback pushed the SAME entry into `ring` a
// SECOND time whenever the configured sink happened to be a
// struct-copied Logger, double-counting one real event against the
// audit trail's finite capacity (confirmed byte-identical, since Ts is
// set once by construct() before either push could run). Every OTHER
// failure mode (marshal error, write error, rotate error — all plain
// fmt.Errorf, never ErrLoggerCopied) still falls back to `ring` exactly
// as before.
func logEntry(e Entry) {
	sinkMu.Lock()
	s := sink
	sinkMu.Unlock()

	if s == nil {
		ring.push(e)
		return
	}
	err := s.Log(e)
	if err == nil {
		return
	}
	if errors.Is(err, ErrLoggerCopied) {
		return
	}
	ring.push(e)
}

// resetSinkForTest restores the package-level sink state to its zero
// value (no configured sink, both `ring` and `copyRejectRing` empty).
// Test-only.
func resetSinkForTest() {
	sinkMu.Lock()
	sink = nil
	sinkMu.Unlock()
	ring = newRingBuffer(ringCapacity)
	copyRejectRing = newRingBuffer(copyRejectRingCapacity)
}
