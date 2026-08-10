package uitest

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// fixturePlayback adapts a *replay.UIPlayer (which only exposes
// Results()/Events()/Deltas() — mode (a) in harness.replay's doc.go) into
// the full protocol.Transport shape core.NewViewsLoop needs (AC-3), and
// counts every Delta actually forwarded to the consuming ViewsLoop
// (deltasSeen) so AwaitDeltas can detect AC-3b's "fixture exhausted
// before the script's expected effects were all observed" condition
// deterministically — by a real completion signal (the pump goroutine's
// exhausted channel closing once the fixture's own Deltas() channel
// closes), never a timing guess.
//
// SendCommand is deliberately a hard rejection (MET-H105), not a silent
// no-op: fixture playback is read-only canned data (harness.replay's
// UIPlayer doc comment — "a UI has nothing to send commands TO in
// playback mode"), so an accidental SendCommand against it is a caller
// bug worth surfacing immediately rather than swallowing.
type fixturePlayback struct {
	player *replay.UIPlayer

	deltaOut   chan protocol.Delta
	exhausted  chan struct{}
	deltasSeen atomic.Int64
}

// barrierSubID is a reserved SubscriptionID pump uses for a single
// synthetic "barrier" Delta appended strictly after every real delta the
// fixture carries (see pump's doc comment) — never a subscription any
// real fixture or caller-supplied DrawFunc is expected to use. Harness's
// AwaitDeltas polls for its arrival in the ViewStore as a hard,
// non-timing proof that every preceding real delta has already been
// applied.
const barrierSubID protocol.SubscriptionID = "uitest.internal.barrier"

// newFixturePlayback starts the forwarding pump goroutine and returns
// the ready-to-use adapter. stop, when closed, unblocks a pump send that
// is waiting for a consumer that will never arrive again (e.g. Harness
// is shutting down mid-replay) — without it, a Harness.Stop() call that
// stops the consuming ViewsLoop while the pump is still mid-send would
// leak the pump goroutine forever.
func newFixturePlayback(player *replay.UIPlayer, stop <-chan struct{}) *fixturePlayback {
	p := &fixturePlayback{
		player:    player,
		deltaOut:  make(chan protocol.Delta),
		exhausted: make(chan struct{}),
	}
	go p.pump(stop)
	return p
}

// pump forwards every Delta from the fixture's own (pre-filled, already
// closed once exhausted) channel to deltaOut, counting each one AFTER
// the forwarding send completes (not before) — so DeltasSeen() reaching
// N is a hard guarantee that ViewsLoop (T-VIEWS, uicore.NewViewsLoop's
// single consuming goroutine) has already RECEIVED the Nth delta, never
// an overcount racing the rendezvous.
//
// Once the fixture's own Deltas() channel closes (exhausted), pump sends
// exactly one synthetic barrier Delta (barrierSubID) before it stops.
// Because ViewsLoop consumes deltaOut strictly in FIFO order on ONE
// goroutine (uicore.ViewsLoop.Run's for/select loop — see that type's
// doc comment), ViewsLoop cannot begin processing the barrier until it
// has FINISHED applying (and publishing) every real delta sent before
// it. So Harness.AwaitDeltas polling the ViewStore for the barrier's
// arrival is a genuine, non-timing-based proof that every real delta's
// effects have already landed — the piece a raw "N deltas forwarded"
// count alone cannot prove, since forwarding a delta and that delta's
// apply() completing are two different events on two different
// goroutines with no direct happens-before edge between them without
// this barrier (GR#21: determinism via a real completion signal, never
// a sleep).
//
// exhausted closes once pump has finished (after the barrier attempt) —
// the real, non-timing-based signal AwaitDeltas also uses for AC-3b's
// "ran dry before the expected count" case.
func (p *fixturePlayback) pump(stop <-chan struct{}) {
	defer close(p.deltaOut)
	defer close(p.exhausted)
	for d := range p.player.Deltas() {
		select {
		case p.deltaOut <- d:
			p.deltasSeen.Add(1)
		case <-stop:
			return
		}
	}
	barrier := protocol.Delta{SubscriptionID: barrierSubID, Tick: protocol.Tick(p.deltasSeen.Load() + 1), Seq: 1, Patch: []byte("{}")}
	select {
	case p.deltaOut <- barrier:
	case <-stop:
	}
}

// SendCommand implements protocol.Transport. Always rejects (MET-H105) —
// see fixturePlayback's doc comment.
func (p *fixturePlayback) SendCommand(protocol.Command) error {
	return errs.New(codeFixturePlaybackReadOnly, errs.NewCorrelationID(), nil)
}

// Results implements protocol.Transport, passed through unmodified from
// the underlying UIPlayer (only Deltas needs the counting wrapper —
// AwaitDeltas only ever needs to reason about delta exhaustion, per
// AC-3b's own wording).
func (p *fixturePlayback) Results() <-chan protocol.CommandResult { return p.player.Results() }

// Events implements protocol.Transport, passed through unmodified.
func (p *fixturePlayback) Events() <-chan protocol.Event { return p.player.Events() }

// Deltas implements protocol.Transport: the counting-forwarded channel,
// not the underlying UIPlayer's own — see pump's doc comment.
func (p *fixturePlayback) Deltas() <-chan protocol.Delta { return p.deltaOut }

// Close implements protocol.Transport. A no-op: fixturePlayback owns no
// resource that needs releasing beyond the pump goroutine, which exits
// on its own once the fixture drains (or stop closes, passed at
// construction).
func (p *fixturePlayback) Close() error { return nil }

// DeltasSeen returns the number of deltas forwarded (received off the
// fixture) so far. Safe for concurrent use (atomic load).
func (p *fixturePlayback) DeltasSeen() int64 { return p.deltasSeen.Load() }

// Exhausted returns a channel that closes once the fixture's Delta
// stream has fully drained (every already-buffered delta forwarded).
func (p *fixturePlayback) Exhausted() <-chan struct{} { return p.exhausted }
