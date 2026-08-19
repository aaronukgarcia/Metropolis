package services

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type SendCommandFunc func(protocol.Command) error

const (
	opSetFunding = "services.set-funding"
)

func opCommand(correlationID string, op string, args map[string]string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: op, Args: args},
	}
}

type Screen struct {
	mu sync.Mutex

	self atomic.Pointer[Screen]

	correlationID         string
	subs                  map[protocol.SubscriptionID]string
	stale                 bool
	haveData              bool
	fundingRejectedReason string

	sliders        []ServiceSlider
	haveSliders    bool
	capacityDemand []CapacityDemand
	haveCapacity   bool
	responseTimes  []ResponseTimeStat
	haveResponse   bool
	waitingLists   []WaitingList
	haveWaiting    bool
	pie            *PublicServicePieView
	havePie        bool
}

func New(correlationID string) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
	}
	s.self.Store(s)
	return s
}

func (s *Screen) Subscribe(send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewSubscriptionName},
	}
	return send(cmd)
}

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

// ApplyResult surfaces a rejected funding-slider change (SVC-8): the
// engine's rejection reason (e.g. below a hard floor) is stored rather
// than the slider silently reverting with no feedback, mirroring
// finance.Screen.ApplyResult's loanRejectedReason pattern exactly.
func (s *Screen) ApplyResult(res protocol.CommandResult) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyResult"}); err != nil {
		return
	}

	if string(res.CorrelationID) == s.correlationID {
		if !res.Accepted && res.Error != nil {
			s.fundingRejectedReason = res.Error.Display
		} else {
			s.fundingRejectedReason = ""
		}
	}
}

func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}
	if view != ViewSubscriptionName {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}

	p, err := decodeWirePatch(delta.Patch)
	if err != nil {
		s.logMalformed(err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.haveData = true

	if p.Sliders != nil {
		s.sliders = make([]ServiceSlider, len(*p.Sliders))
		for i, sl := range *p.Sliders {
			s.sliders[i] = ServiceSlider(sl)
		}
		s.haveSliders = true
	} else {
		s.haveSliders = false
	}

	if p.CapacityDemand != nil {
		s.capacityDemand = make([]CapacityDemand, len(*p.CapacityDemand))
		for i, c := range *p.CapacityDemand {
			s.capacityDemand[i] = CapacityDemand(c)
		}
		s.haveCapacity = true
	} else {
		s.haveCapacity = false
	}

	if p.ResponseTimes != nil {
		s.responseTimes = make([]ResponseTimeStat, len(*p.ResponseTimes))
		for i, r := range *p.ResponseTimes {
			s.responseTimes[i] = ResponseTimeStat(r)
		}
		s.haveResponse = true
	} else {
		s.haveResponse = false
	}

	if p.WaitingLists != nil {
		s.waitingLists = make([]WaitingList, len(*p.WaitingLists))
		for i, w := range *p.WaitingLists {
			s.waitingLists[i] = WaitingList{
				ID:           w.ID,
				Label:        w.Label,
				CurrentCount: w.CurrentCount,
				TrendHistory: append([]float64(nil), w.TrendHistory...),
			}
		}
		s.haveWaiting = true
	} else {
		s.haveWaiting = false
	}

	if p.PublicServicePie != nil {
		s.pie = &PublicServicePieView{}
		s.pie.Slices = make([]PieSlice, len(p.PublicServicePie.Slices))
		for i, sl := range p.PublicServicePie.Slices {
			s.pie.Slices[i] = PieSlice(sl)
		}
		s.havePie = true
	} else {
		s.havePie = false
	}
}

func (s *Screen) HaveData() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "HaveData"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haveData
}

func (s *Screen) Stale() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stale
}

func (s *Screen) SetStale(v bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale = v
}

func (s *Screen) FundingRejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "FundingRejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fundingRejectedReason
}

func (s *Screen) Sliders() ([]ServiceSlider, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Sliders"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveSliders {
		return nil, false
	}
	res := make([]ServiceSlider, len(s.sliders))
	copy(res, s.sliders)
	return res, true
}

func (s *Screen) CapacityDemand() ([]CapacityDemand, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CapacityDemand"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveCapacity {
		return nil, false
	}
	res := make([]CapacityDemand, len(s.capacityDemand))
	copy(res, s.capacityDemand)
	return res, true
}

func (s *Screen) ResponseTimes() ([]ResponseTimeStat, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ResponseTimes"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveResponse {
		return nil, false
	}
	res := make([]ResponseTimeStat, len(s.responseTimes))
	copy(res, s.responseTimes)
	return res, true
}

func (s *Screen) WaitingLists() ([]WaitingList, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "WaitingLists"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveWaiting {
		return nil, false
	}
	res := make([]WaitingList, len(s.waitingLists))
	for i, w := range s.waitingLists {
		res[i] = WaitingList{
			ID:           w.ID,
			Label:        w.Label,
			CurrentCount: w.CurrentCount,
			TrendHistory: append([]float64(nil), w.TrendHistory...),
		}
	}
	return res, true
}

// PublicServicePie (SVC-6) — BLOCKED: see doc.go's SVC-6 note. Always
// returns have=false today because nothing sends the wire field yet
// (no engine.fiscal outbound edge is registered for ui.screen.services,
// BUG-058 candidate); the accessor and its render path exist so wiring
// SVC-6 later requires no structural change here.
func (s *Screen) PublicServicePie() (PublicServicePieView, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "PublicServicePie"}); err != nil {
		return PublicServicePieView{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.havePie {
		return PublicServicePieView{}, false
	}
	res := PublicServicePieView{Slices: make([]PieSlice, len(s.pie.Slices))}
	copy(res.Slices, s.pie.Slices)
	return res, true
}

// normalizeFundingLevel rescales rawValue (a slider position in the
// slider's own UI display domain, e.g. min..max) into the engine's [0,1]
// funding-level fraction — internal/engine/services/api.go:266-292's
// ServicesAPI.SetFunding hard-rejects any level outside [0,1] (the
// codebase-wide funding-level convention; never a UI-scaled absolute like
// 0-1000). A degenerate min>=max domain has no meaningful fraction and
// returns NaN so the caller rejects rather than silently misreporting 0.
func normalizeFundingLevel(min, max, rawValue float64) float64 {
	if max <= min {
		return math.NaN()
	}
	return (rawValue - min) / (max - min)
}

// SetFunding issues SVC-1's per-service funding-slider change as a
// protocol.DebugPayload command with the fixed Op string
// "services.set-funding" (ASM-1193 precedent — int.protocol's sealed v1
// command vocabulary has no SetFunding Kind and this screen may not edit
// internal/protocol). sl is the slider being changed (its Min/Max define
// the UI display domain the player sees — the slider MAY still display
// 0-1000 or a percentage) and rawValue is the new position in that same
// display domain. The wire value this method actually sends is rawValue
// rescaled into the engine's [0,1] funding-level fraction via
// normalizeFundingLevel, mirroring internal/engine/services/api.go's
// SetFunding contract exactly — a non-finite, negative, or above-domain
// rawValue (one that rescales to a level outside [0,1]) is rejected
// locally with MET-V503 before ever reaching the engine, never silently
// clamped. An engine-side rejection for a value inside [0,1] (e.g. below a
// hard floor, or ErrNotUnlocked) still surfaces separately via
// ApplyResult/FundingRejectedReason (SVC-8).
func (s *Screen) SetFunding(send SendCommandFunc, sl ServiceSlider, rawValue float64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetFunding"}); err != nil {
		return err
	}
	level := normalizeFundingLevel(sl.Min, sl.Max, rawValue)
	if math.IsNaN(level) || math.IsInf(level, 0) || level < 0 || level > 1 {
		return errs.New(ErrInvalidFundingRequest, s.correlationID, map[string]any{
			"reason":   "funding level outside the engine's [0,1] domain (internal/engine/services/api.go SetFunding)",
			"id":       sl.ID,
			"rawValue": rawValue,
			"min":      sl.Min,
			"max":      sl.Max,
			"level":    level,
		})
	}
	args := map[string]string{
		"id":    sl.ID,
		"value": strconv.FormatFloat(level, 'f', -1, 64),
	}
	return send(opCommand(s.correlationID, opSetFunding, args))
}

func (s *Screen) logMalformed(cause error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logMalformed"}); err != nil {
		return
	}
	_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
		"view":  ViewSubscriptionName,
		"cause": cause.Error(),
	})
}

func (s *Screen) logUnknownSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logUnknownSubscription"}); err != nil {
		return
	}
	_ = errs.New(ErrStaleSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
