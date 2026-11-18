package why

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SendCommandFunc sends one protocol command (the boot transport's SendCommand).
type SendCommandFunc func(protocol.Command) error

// Screen is the F5 WHY screen's model: the last good f5.why patch. All
// methods are safe for concurrent use (input, router and draw run on
// different goroutines) and refuse to run on a struct copy (checkNotCopied,
// as every copy-guarded screen).
type Screen struct {
	mu sync.Mutex

	self atomic.Pointer[Screen]

	correlationID string
	subs          map[protocol.SubscriptionID]string

	// patch is the last good patch (nil until one is accepted). A patch with
	// Known=false is a good patch: it means "no month closed yet".
	patch *Patch
}

// New builds a Screen. correlationID labels the screen's own commands and
// log entries.
func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
	}
	s.self.Store(s)
	return s
}

// Subscribe sends the subscribe command for the f5.why view.
func (s *Screen) Subscribe(send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	return send(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewSubscriptionName},
	})
}

// BindSubscription records the subscription id the engine assigned.
func (s *Screen) BindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.subs[id] = ViewSubscriptionName
}

// UnbindSubscription forgets a subscription id.
func (s *Screen) UnbindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	delete(s.subs, id)
}

// ApplyDelta decodes one delta. A delta for an unbound subscription is
// logged (MET-V981) and dropped. A refused patch is logged (MET-V980) and
// dropped: the last good patch stays, never partially applied.
func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		s.mu.Unlock()
		return
	}
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok || view != ViewSubscriptionName {
		_ = errs.New(ErrStaleSubscription, s.correlationID, map[string]any{
			"subscriptionId": string(delta.SubscriptionID),
		})
		return
	}
	p, err := decodePatch(delta.Patch)
	if err != nil {
		_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
			"cause": err.Error(),
		})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.patch = &p
}

// HaveData reports whether any good patch has arrived (even a known=false one).
func (s *Screen) HaveData() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	return s.patch != nil
}

// Snapshot returns a deep copy of the last good patch and whether a month
// has closed (patch.Known). ok is false when nothing usable is held.
func (s *Screen) Snapshot() (Patch, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Snapshot"}); err != nil {
		return Patch{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Snapshot"}); err != nil {
		return Patch{}, false
	}
	if s.patch == nil || !s.patch.Known {
		return Patch{}, false
	}
	return clonePatch(*s.patch), true
}

// clonePatch deep-copies the slices and pointers of p.
func clonePatch(p Patch) Patch {
	out := p
	out.Levers = cloneLevers(p.Levers)
	out.Modifiers = cloneLevers(p.Modifiers)
	out.AWorld = cloneF(p.AWorld)
	out.Neutral = cloneF(p.Neutral)
	out.MovedIn = cloneI(p.MovedIn)
	out.Left = cloneI(p.Left)
	out.Inflow = cloneI(p.Inflow)
	out.Outflow = cloneI(p.Outflow)
	if p.Homes != nil {
		h := *p.Homes
		out.Homes = &h
	}
	if p.Homeless != nil {
		h := *p.Homeless
		out.Homeless = &h
	}
	return out
}

func cloneLevers(in []Lever) []Lever {
	if in == nil {
		return nil
	}
	out := make([]Lever, len(in))
	for i, l := range in {
		out[i] = l
		out[i].Parts = append([]Part(nil), l.Parts...)
	}
	return out
}

func cloneF(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneI(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
