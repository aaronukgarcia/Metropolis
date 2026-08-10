package replay

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// TapTransport is the "or a tap on one" half of AC-1: given a live
// protocol.Transport, it launches one goroutine per receive channel
// (Results/Events/Deltas) forwarding every message to rec's matching
// Observe* method, until ctx is done or the channel closes. It returns
// immediately; the goroutines run in the background and never block the
// tapped transport's own senders (AC-13: recording must not require
// pausing either loop) — each forward is a plain blocking receive
// followed by a non-blocking Observe* call, so the only backpressure a
// slow Recorder could apply is on THIS goroutine's own receive, never on
// the transport's send side (which already has its own evict-oldest
// policy, transport.go).
//
// TapTransport deliberately does NOT intercept SendCommand: a Transport
// exposes no channel a passive tap could range over for outbound
// commands (SendCommand is a synchronous, single call per command, not a
// stream), so capturing "every Command sent" (AC-1) for the commands
// half is the CALLER'S responsibility — call rec.ObserveCommand(cmd)
// at the same call site that calls transport.SendCommand(cmd) (see
// gen/main.go for the canonical example generating this package's own
// sample fixture). Logged as an assumption: a fully transparent
// decorator that also wraps SendCommand was considered and rejected as
// unnecessary complexity for a single, already-synchronous call site,
// but a reasonable person could have built it differently.
func (r *Recorder) TapTransport(t protocol.Transport) {
	go r.forwardResults(t.Results())
	go r.forwardEvents(t.Events())
	go r.forwardDeltas(t.Deltas())
}

func (r *Recorder) forwardResults(ch <-chan protocol.CommandResult) {
	for v := range ch {
		_ = r.ObserveResult(v)
	}
}

func (r *Recorder) forwardEvents(ch <-chan protocol.Event) {
	for v := range ch {
		_ = r.ObserveEvent(v)
	}
}

func (r *Recorder) forwardDeltas(ch <-chan protocol.Delta) {
	for v := range ch {
		_ = r.ObserveDelta(v)
	}
}
