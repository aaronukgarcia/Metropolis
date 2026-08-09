package stub

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// subState tracks one live view subscription's server-side bookkeeping:
// its own monotonic Seq counter (protocol's per-subscription contract,
// subscription.go) and how far through its scripted delta stream it has
// advanced.
type subState struct {
	id       protocol.SubscriptionID
	viewName string
	seq      uint64 // last Seq handed out; next Delta uses seq+1
	scriptAt int    // index into the view's scripted delta stream
}

// nextSeq allocates the next monotonically increasing Seq for this
// subscription, starting at 1 (protocol/subscription.go's contract,
// carried forward by AC-5). Callers must hold StubEngine.mu.
func (s *subState) nextSeq() uint64 {
	s.seq++
	return s.seq
}
