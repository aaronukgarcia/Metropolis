package replay

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// RecordKind labels the protocol message type carried by one captured
// Record — written verbatim as serialize.Record.Kind, so a reader (this
// package's own Load, or any future tool) can dispatch without decoding
// every record speculatively.
type RecordKind string

const (
	KindCommand RecordKind = "command"
	KindResult  RecordKind = "result"
	KindEvent   RecordKind = "event"
	KindDelta   RecordKind = "delta"
)

// Recorder captures a Command/CommandResult/Event/Delta stream in strict
// arrival order (AC-1). Every Observe* method appends under a single
// mutex, so concurrent callers (e.g. one goroutine forwarding a
// transport's Results() while another forwards Events()) never interleave
// a half-written record — the resulting sequence is always consistent
// with SOME valid arrival interleaving of the calls actually made,
// AC-1b's requirement.
//
// AC-1b (weakness pattern #1 — invariant enforced, not stated): the only
// way to add to the captured sequence is Observe*; there is no exported
// mutator that can insert, reorder, or splice already-captured records
// (no InsertAt, no exported slice field). Records() (below) returns a
// defensive COPY, mirroring foundation.registry.List()'s pattern, so a
// caller holding that slice cannot mutate the Recorder's own state
// through it either.
//
// Zero value is NOT ready for use — construct with NewRecorder (AC-13b:
// self-identity copy guard, see checkNotCopied).
type Recorder struct {
	mu      sync.Mutex
	records []serialize.Record

	// self holds the address NewRecorder gave this Recorder at
	// construction. Mirrors InProcTransport.self (internal/protocol/
	// transport.go) exactly: mu is a plain sync.Mutex VALUE, so a
	// struct copy ('r2 := *r') gets its OWN, independent mu, but
	// records (a slice header pointing at the SAME backing array) is
	// still ALIASED — two independently-locked appenders racing over
	// one backing array is the exact SEC-014/SEC-019 shape. atomic.Pointer,
	// not a plain field, so the identity check is race-safe and can run
	// BEFORE mu is ever touched (SEC-016: a copy taken while the
	// original's mu was held captures those mutex bytes as "locked",
	// and the copy's own next Lock() can then hang forever).
	self atomic.Pointer[Recorder]
}

// NewRecorder returns an empty Recorder ready to capture.
func NewRecorder() *Recorder {
	r := &Recorder{}
	// Stored exactly once, here, before r is returned to any caller — no
	// goroutine can have a reference to r to race this Store against
	// (mirrors NewInProcTransport/NewEngine — see self's doc comment).
	r.self.Store(r)
	return r
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Recorder value. Deliberately lock-free (a single atomic.Pointer
// Load) so it is safe to call BEFORE mu is ever touched — see self's doc
// comment for why that ordering is load-bearing, not merely tidy.
func (r *Recorder) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(codeRecorderCopied, correlationID, ctx)
	}
	return nil
}

// observe appends one record under mu, after the identity check.
func (r *Recorder) observe(kind RecordKind, correlationID string, data []byte) error {
	if err := r.checkNotCopied(correlationID, map[string]any{"kind": string(kind)}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(correlationID, map[string]any{"kind": string(kind)}); err != nil {
		return err
	}
	r.records = append(r.records, serialize.Record{Kind: string(kind), Data: data})
	return nil
}

// ObserveCommand captures cmd, encoded via protocol.EncodeCommand (never
// a bespoke encoder — the same wire form Command already has).
func (r *Recorder) ObserveCommand(cmd protocol.Command) error {
	data, err := protocol.EncodeCommand(cmd)
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, string(cmd.CorrelationID), err, map[string]any{"cause": "encoding captured Command"})
	}
	return r.observe(KindCommand, string(cmd.CorrelationID), data)
}

// ObserveResult captures res.
func (r *Recorder) ObserveResult(res protocol.CommandResult) error {
	data, err := json.Marshal(res)
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, string(res.CorrelationID), err, map[string]any{"cause": "encoding captured CommandResult"})
	}
	return r.observe(KindResult, string(res.CorrelationID), data)
}

// ObserveEvent captures ev.
func (r *Recorder) ObserveEvent(ev protocol.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, string(ev.CorrelationID), err, map[string]any{"cause": "encoding captured Event"})
	}
	return r.observe(KindEvent, string(ev.CorrelationID), data)
}

// ObserveDelta captures d.
func (r *Recorder) ObserveDelta(d protocol.Delta) error {
	data, err := json.Marshal(d)
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, string(d.CorrelationID), err, map[string]any{"cause": "encoding captured Delta"})
	}
	return r.observe(KindDelta, string(d.CorrelationID), data)
}

// Records returns a defensive COPY of every record captured so far, in
// strict arrival order (AC-1/AC-1b). Mutating the returned slice never
// affects the Recorder's own state — mirrors
// foundation.registry.Registry.List()'s copy-return pattern.
func (r *Recorder) Records() []serialize.Record {
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil
	}
	out := make([]serialize.Record, len(r.records))
	copy(out, r.records)
	return out
}

// Len returns the number of records captured so far. Equivalent to
// len(r.Records()) but without the copy.
func (r *Recorder) Len() int {
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return 0
	}
	return len(r.records)
}
