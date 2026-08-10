package replay

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// UIPlayer is replay mode (a): a canned Results()/Events()/Deltas()
// source for a UI's Transport consumer, built once from a loaded Fixture
// (AC-3a). It carries every recorded CommandResult/Event/Delta, in
// fixture order, on buffered channels that are pre-filled and closed at
// construction — a consumer ranging over them sees exactly the recorded
// stream, then a clean channel close, with no live producer goroutine to
// manage or leak. Recorded Commands are not replayed anywhere by this
// mode (AC-3's "canned Deltas()/Events() source" — a UI has nothing to
// send commands TO in playback mode); use EnginePlayer for mode (b).
//
// UIPlayer holds no mutex (see doc.go's "Copy-safety" section) — every
// field is set once in NewUIPlayer and never mutated afterward, so a
// struct copy is exactly as safe to read as the original.
type UIPlayer struct {
	resultCh chan protocol.CommandResult
	eventCh  chan protocol.Event
	deltaCh  chan protocol.Delta
}

// NewUIPlayer builds a UIPlayer from f's recorded results/events/deltas.
func NewUIPlayer(f Fixture) (*UIPlayer, error) {
	results, err := f.Results()
	if err != nil {
		return nil, err
	}
	events, err := f.Events()
	if err != nil {
		return nil, err
	}
	deltas, err := f.Deltas()
	if err != nil {
		return nil, err
	}

	p := &UIPlayer{
		resultCh: make(chan protocol.CommandResult, len(results)),
		eventCh:  make(chan protocol.Event, len(events)),
		deltaCh:  make(chan protocol.Delta, len(deltas)),
	}
	for _, r := range results {
		p.resultCh <- r
	}
	for _, e := range events {
		p.eventCh <- e
	}
	for _, d := range deltas {
		p.deltaCh <- d
	}
	close(p.resultCh)
	close(p.eventCh)
	close(p.deltaCh)
	return p, nil
}

// Results returns the canned CommandResult stream, in fixture order.
func (p *UIPlayer) Results() <-chan protocol.CommandResult { return p.resultCh }

// Events returns the canned Event stream, in fixture order.
func (p *UIPlayer) Events() <-chan protocol.Event { return p.eventCh }

// Deltas returns the canned Delta stream, in fixture order.
func (p *UIPlayer) Deltas() <-chan protocol.Delta { return p.deltaCh }
