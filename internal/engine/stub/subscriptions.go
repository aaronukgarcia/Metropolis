package stub

import (
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// subState tracks one live view subscription's server-side bookkeeping:
// its own monotonic Seq counter (protocol's per-subscription contract,
// subscription.go) and how far through its scripted delta stream it has
// advanced.
type subState struct {
	id       protocol.SubscriptionID
	viewName string
	seq      uint64 // last Seq handed out; next Delta uses seq+1
	scriptAt int    // index into the view's scripted delta stream

	// Delayed-delta delivery bookkeeping (BUG-283/BUG-284). Under the
	// DelayedDeltas chaos knob, deltas are NOT sent from an independent
	// goroutine each (which let a later Seq overtake an earlier one, and
	// delivered deltas queued before an Unsubscribe): instead they are
	// appended to pending in Seq order and drained, one at a time, by a
	// single runDelayPump goroutine. pending and pumpRunning are guarded by
	// StubEngine.mu; done is created in handleSubscribe and closed exactly
	// once in handleUnsubscribe to abort an in-flight delay and drop the
	// queue the instant the subscription ends.
	pending     []delayedDelta // FIFO queue of not-yet-sent delayed deltas, in Seq order
	pumpRunning bool           // whether a runDelayPump goroutine is currently draining pending
	done        chan struct{}  // closed on Unsubscribe: aborts the pump and drops pending
}

// delayedDelta pairs an already-built Delta with the artificial delay to
// wait before sending it. Enqueued by emitDeltaLocked, drained in order by
// runDelayPump (engine.go).
type delayedDelta struct {
	delta protocol.Delta
	delay time.Duration
}

// nextSeq allocates the next monotonically increasing Seq for this
// subscription, starting at 1 (protocol/subscription.go's contract,
// carried forward by AC-5). Callers must hold StubEngine.mu.
func (s *subState) nextSeq() uint64 {
	s.seq++
	return s.seq
}
