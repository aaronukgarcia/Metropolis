package protocol

import (
	"errors"
	"sync"
)

// Default buffer sizes for NewInProcTransport. Chosen generously for
// skeleton-era traffic (a handful of subscriptions, human-paced command
// rate); revisit once H-SYNTH perf runs (GDD §15, M0-ENG §1.3) put real
// numbers on subscription counts and delta rates at scale.
const (
	DefaultCommandBuffer = 64
	DefaultResultBuffer  = 64
	DefaultEventBuffer   = 256
	DefaultDeltaBuffer   = 256
)

// Transport is the UI-facing seam to the engine: send commands, receive
// results/events/deltas. It is the ONE interface that both the v1
// in-process implementation (InProcTransport, below) and a future gRPC
// implementation satisfy identically — "flip a flag for out-of-process,
// remote, or cloud, TUI unchanged" (GDD §15). No gRPC code exists in this
// package; see the "gRPC mapping" comment block below for how it would
// plug in without changing this interface.
//
// T-VIEWS (M0-ENG §1.1) is the intended sole reader of Results/Events/
// Deltas; T-INPUT/T-RENDER send commands via SendCommand. Nothing in
// this package enforces single-reader/single-writer — that discipline
// lives in the process topology (M0-ENG §1.1), not the type system.
type Transport interface {
	// SendCommand validates cmd (Command.Validate) and enqueues it for
	// the engine. It returns an error immediately, without blocking, if
	// cmd is invalid or the transport cannot accept it right now (e.g.
	// InProcTransport's command queue is full, or the transport is
	// closed) — callers must treat a returned error as "not sent," never
	// assume a queued retry happens for them.
	//
	// Deliberately synchronous-return rather than channel-based on this
	// side: SendCommand is called from T-INPUT/T-RENDER, which "never
	// blocks, ever" (M0-ENG §1.1) — an error return that the caller can
	// turn into an immediate "busy" indicator is the only shape that
	// keeps that promise. Compare to Results/Events/Deltas below, which
	// ARE channel-based because their consumer (T-VIEWS) is a dedicated
	// loop built to range over them.
	SendCommand(cmd Command) error

	// Results, Events, and Deltas are receive-only channels of outbound
	// engine messages. They are closed when Close is called and never
	// otherwise closed out from under a reader (no send-on-closed-channel
	// races are possible from the reader's side).
	Results() <-chan CommandResult
	Events() <-chan Event
	Deltas() <-chan Delta

	// Close shuts the transport down: stops accepting commands (further
	// SendCommand calls return ErrTransportClosed) and closes the
	// Results/Events/Deltas channels after any in-flight sends complete.
	// Idempotent.
	Close() error
}

// ErrTransportClosed is returned by SendCommand (and by the engine-side
// SendResult/SendEvent/SendDelta methods on InProcTransport) once Close
// has been called.
var ErrTransportClosed = errors.New("protocol: transport is closed")

// ErrCommandQueueFull is returned by InProcTransport.SendCommand when the
// bounded command channel has no room and no reader is currently
// draining it. Unlike the outbound drop policy below, commands are never
// silently dropped — a full queue is reported to the caller so it can
// decide whether to retry, surface "engine busy," or (per UI-SPEC's
// input loop never blocking) simply skip this keystroke's command.
var ErrCommandQueueFull = errors.New("protocol: command queue is full")

// InProcTransport is the v1 Transport: everything moves over Go
// channels within one process (M0-ENG §1.1's "in-process v1; identical
// over gRPC later"). A single InProcTransport instance is shared by both
// domains — the UI side calls SendCommand/Results/Events/Deltas/Close
// (satisfying Transport); the engine side calls the Commands/SendResult/
// SendEvent/SendDelta methods below. There is exactly one instance per
// running game process, constructed once at startup and handed to both
// T-VIEWS and T-SUBSCR/T-ENGINE.
//
// # Outbound drop/stale policy (UI-SPEC §1: "if a delta is late, the
// last frame stands")
//
// SendResult, SendEvent, and SendDelta are non-blocking: the engine's
// T-SUBSCR/T-ENGINE goroutines must never stall on a slow or absent UI
// reader (that would make the simulation's correctness depend on the
// render loop, which M0-ENG §1.1 forbids — "no shared memory between
// domains" extends in spirit to "no shared pacing" either). When a
// channel is full, the OLDEST queued message of that kind is evicted to
// make room for the newest, rather than dropping the newest and keeping
// stale data queued. This is a deliberate reading of "the last frame
// stands": it should be the actual last frame (freshest engine state),
// not whatever happened to be queued first. The trade-off, and why it's
// an open question for the freeze review rather than a settled design,
// is in docs/design/protocol.md.
//
// This policy applies uniformly to Results, Events, and Deltas in v1 for
// implementation simplicity. It is least appropriate for CommandResult
// (a dropped acceptance/rejection for a specific command is a worse UX
// gap than a dropped delta, which the next delta supersedes anyway) —
// flagged explicitly as an open question in docs/design/protocol.md
// rather than special-cased silently here.
//
// # gRPC mapping (comment only — no gRPC code or dependency in this file)
//
// A future grpc.Transport would implement the same Transport interface:
//   - SendCommand              -> a unary RPC (Engine.Send) whose request
//     is the JSON- or protobuf-encoded Command and whose response is
//     either "queued" or a gRPC status error (mapping ErrCommandQueueFull
//     etc. to codes.ResourceExhausted and friends).
//   - Results/Events/Deltas    -> one server-streaming RPC per channel
//     (or a single multiplexed stream carrying a oneof, if per-message
//     head-of-line blocking across kinds turns out to matter), with the
//     client-side stub filling local Go channels exactly like
//     InProcTransport's outbound buffers — so T-VIEWS is unchanged.
//   - The drop/stale policy moves to the SERVER side of the streaming
//     RPC (a slow client's flow-control window fills, and the server
//     must make the same evict-oldest choice InProcTransport makes
//     locally) — same policy, different enforcement point.
//   - Close                    -> closing the client connection /
//     cancelling the streaming RPCs' contexts.
//
// codec.go's JSON encode/decode is reused as-is for the gRPC transport
// too if the wire format stays JSON-over-gRPC (google.protobuf.Any-style
// payload) rather than native protobuf messages — that choice is
// deferred to when gRPC is actually switched on, per GDD §15.
type InProcTransport struct {
	cmdCh    chan Command
	resultCh chan CommandResult
	eventCh  chan Event
	deltaCh  chan Delta

	closed    chan struct{}
	closeOnce func()

	// closeMu serialises every send (SendCommand and the engine-side
	// SendResult/SendEvent/SendDelta) against Close. BUG-007: Close used
	// to close the channels with no synchronisation at all, so a sender
	// that passed the `<-closed` peek in trySendEvictOldest could still
	// land its `ch <- v` after Close closed that same channel underneath
	// it — a send-on-closed-channel panic, not a benign data race.
	//
	// Senders take closeMu.RLock() for the duration of their send
	// attempt; Close takes closeMu.Lock() before closing anything, so it
	// cannot run concurrently with any send that is already past the
	// closed-check-and-send window, and any sender that arrives after
	// Close has the write lock blocks until Close finishes and then
	// observes t.closed as already closed. This cannot deadlock Close
	// against a stuck sender: every send under RLock
	// (trySendEvictOldest's bounded evict-retry loop, and SendCommand's
	// single non-blocking `select`) is non-blocking by construction, so
	// the longest Close can ever wait for an RLock holder is bounded by
	// that holder's own non-blocking work, never by a slow or absent
	// reader on the other end.
	closeMu sync.RWMutex
}

// NewInProcTransport constructs an InProcTransport with the given buffer
// sizes. Use the Default* constants for typical v1 usage.
func NewInProcTransport(cmdBuf, resultBuf, eventBuf, deltaBuf int) *InProcTransport {
	t := &InProcTransport{
		cmdCh:    make(chan Command, cmdBuf),
		resultCh: make(chan CommandResult, resultBuf),
		eventCh:  make(chan Event, eventBuf),
		deltaCh:  make(chan Delta, deltaBuf),
		closed:   make(chan struct{}),
	}
	var once bool
	t.closeOnce = func() {
		if !once {
			once = true
			close(t.closed)
			close(t.cmdCh)
			close(t.resultCh)
			close(t.eventCh)
			close(t.deltaCh)
		}
	}
	return t
}

// --- UI-facing side (Transport) ---

// SendCommand implements Transport.
func (t *InProcTransport) SendCommand(cmd Command) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	// Hold closeMu for the duration of the closed-check-and-send window;
	// see the closeMu doc comment on InProcTransport (BUG-007) for why
	// this is required to avoid a send-on-closed-channel panic against a
	// concurrent Close.
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	select {
	case <-t.closed:
		return ErrTransportClosed
	default:
	}
	select {
	case t.cmdCh <- cmd:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

// Results implements Transport.
func (t *InProcTransport) Results() <-chan CommandResult { return t.resultCh }

// Events implements Transport.
func (t *InProcTransport) Events() <-chan Event { return t.eventCh }

// Deltas implements Transport.
func (t *InProcTransport) Deltas() <-chan Delta { return t.deltaCh }

// Close implements Transport. Idempotent.
//
// Close takes closeMu's write lock before closing anything, which blocks
// until every sender currently mid-send (holding closeMu.RLock — see the
// closeMu doc comment above, BUG-007) has finished. That is safe rather
// than deadlock-prone specifically because sends are non-blocking by
// construction: nothing holding the RLock can be stuck waiting on a
// reader, so the wait here is always bounded.
func (t *InProcTransport) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	t.closeOnce()
	return nil
}

// --- Engine-facing side ---

// Commands returns the receive-only channel the engine's T-ENGINE/
// T-SUBSCR (M0-ENG §1.1) ranges over to consume incoming commands.
func (t *InProcTransport) Commands() <-chan Command { return t.cmdCh }

// SendResult pushes a CommandResult toward the UI, applying the
// evict-oldest drop policy documented on InProcTransport if the result
// buffer is full. Returns false if the transport is closed (nothing was
// sent) or true otherwise (sent, though an older queued result may have
// been evicted to make room — see the policy doc above).
func (t *InProcTransport) SendResult(r CommandResult) bool {
	// See the closeMu doc comment on InProcTransport (BUG-007): holding
	// RLock for the whole trySendEvictOldest call is what closes the
	// send-vs-Close TOCTOU window.
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	return trySendEvictOldest(t.closed, t.resultCh, r)
}

// SendEvent pushes an Event toward the UI under the same policy as
// SendResult.
func (t *InProcTransport) SendEvent(e Event) bool {
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	return trySendEvictOldest(t.closed, t.eventCh, e)
}

// SendDelta pushes a Delta toward the UI under the same policy as
// SendResult. Callers should route Delta.Seq through a SeqTracker
// (subscription.go) on the receiving side to detect evictions.
func (t *InProcTransport) SendDelta(d Delta) bool {
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()
	return trySendEvictOldest(t.closed, t.deltaCh, d)
}

// trySendEvictOldest sends v on ch without blocking. If ch is full, it
// evicts the oldest queued value (a non-blocking receive) and retries,
// bounded by cap(ch) attempts so a concurrent reader draining the
// channel at the same time can never cause an infinite loop — if by the
// last attempt ch is still full (pathological: cap==0, or another
// writer racing us), the send is skipped and false is returned rather
// than spinning or blocking.
func trySendEvictOldest[T any](closed <-chan struct{}, ch chan T, v T) bool {
	select {
	case <-closed:
		return false
	default:
	}
	attempts := cap(ch) + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		select {
		case ch <- v:
			return true
		default:
		}
		select {
		case <-ch:
			// evicted the oldest queued value; loop around to retry the send
		default:
			// channel was drained concurrently between the two selects;
			// loop around to retry the send directly
		}
	}
	return false
}
