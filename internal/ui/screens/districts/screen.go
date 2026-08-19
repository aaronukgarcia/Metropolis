package districts

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type SendCommandFunc func(protocol.Command) error

// opSetTaxMultiplier is AC-6's per-district tax-multiplier command. int.
// protocol's sealed v1 command vocabulary has no SetDistrictMultiplier
// Kind and this screen may not edit internal/protocol, so the action is
// carried as a protocol.DebugPayload command with a fixed Op string
// following the mandatory districts.<verb> naming (ASM-1193 precedent,
// the same seam ui.screen.services' "services.set-funding" and
// ui.screen.trade/ui.screen.menu already use).
const (
	opSetTaxMultiplier = "districts.set-tax-multiplier"
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

	correlationID     string
	subs              map[protocol.SubscriptionID]string
	stale             bool
	haveData          bool
	taxRejectedReason string
	selectedDistrict  string

	districts    []District
	haveDistrict bool

	taxSettings    []DistrictTaxSetting
	haveTaxSetting bool
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

// ApplyResult surfaces a rejected tax-multiplier change (AC-9): the
// engine's rejection reason (e.g. SEC-098's effective-rate cap) is stored
// rather than the control silently reverting with no feedback, mirroring
// services.Screen.ApplyResult's fundingRejectedReason pattern exactly.
//
// Gating note (BUG-058/ASM-1482, mirrors finance/services doc.go's
// identical FIN-8/SVC-8 note): ApplyResult itself is fully implemented and
// verified in this package's own unit tests, but the wider frame's routing
// of command results to screen sub-receivers is not wired yet pending the
// core routing-seam (ASM-1482) — this method does not invent custom
// ad-hoc wiring to work around that gap.
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
			s.taxRejectedReason = res.Error.Display
		} else {
			s.taxRejectedReason = ""
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

	if p.Districts != nil {
		s.districts = make([]District, len(*p.Districts))
		for i, d := range *p.Districts {
			s.districts[i] = District(d)
		}
		s.haveDistrict = true
	} else {
		s.haveDistrict = false
	}

	if p.TaxSettings != nil {
		s.taxSettings = make([]DistrictTaxSetting, len(*p.TaxSettings))
		for i, t := range *p.TaxSettings {
			s.taxSettings[i] = DistrictTaxSetting(t)
		}
		s.haveTaxSetting = true
	} else {
		s.haveTaxSetting = false
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

func (s *Screen) TaxRejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "TaxRejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taxRejectedReason
}

// SelectedDistrict returns the currently player-selected district (US-5's
// "same screen a district's identity lives on" scoping for AC-6's tax
// panel). Selection is local UI state, never engine-confirmed (there is no
// engine notion of a "selected" district) -- unlike TaxSettings/Districts,
// which only ever change from an applied Delta.
func (s *Screen) SelectedDistrict() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedDistrict"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedDistrict
}

func (s *Screen) SetSelectedDistrict(id string) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetSelectedDistrict"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedDistrict = id
}

// Districts (AC-2's roster) -- BLOCKED today, see doc.go: engine.policies
// is not on main, so ApplyDelta never receives a non-nil Districts field
// from any real engine; this always returns have=false in practice until
// that lands. The accessor exists so wiring AC-2 later requires no
// structural change here.
func (s *Screen) Districts() ([]District, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Districts"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveDistrict {
		return nil, false
	}
	res := make([]District, len(s.districts))
	copy(res, s.districts)
	return res, true
}

// TaxSettings (AC-6) returns every (district, instrument) tax-multiplier
// row the last delta carried -- live over engine.tax's registered edge.
func (s *Screen) TaxSettings() ([]DistrictTaxSetting, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "TaxSettings"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveTaxSetting {
		return nil, false
	}
	res := make([]DistrictTaxSetting, len(s.taxSettings))
	copy(res, s.taxSettings)
	return res, true
}

// SetDistrictMultiplier issues AC-6's per-district tax-multiplier change as
// a protocol.DebugPayload command (opSetTaxMultiplier). Mirrors
// internal/engine/tax/tax.go's SetDistrictMultiplier validation locally --
// a non-finite or negative multiplier is rejected with MET-V603 before
// ever reaching the wire, never silently clamped. An engine-side rejection
// for a value that IS finite and >=0 but still fails the SEC-098
// effective-rate cap still surfaces separately via ApplyResult/
// TaxRejectedReason (AC-9). The displayed value only updates once the
// resulting Delta arrives (AC-6's "not from a locally-mutated value"
// requirement) -- this method never mutates s.taxSettings itself.
func (s *Screen) SetDistrictMultiplier(send SendCommandFunc, districtID, instrumentID string, multiplier float64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetDistrictMultiplier"}); err != nil {
		return err
	}
	if districtID == "" || instrumentID == "" || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 {
		return errs.New(ErrInvalidDistrictMultiplier, s.correlationID, map[string]any{
			"reason":       "district/instrument must be non-empty and multiplier must be a finite value >= 0 (mirrors internal/engine/tax/tax.go SetDistrictMultiplier)",
			"district":     districtID,
			"instrumentId": instrumentID,
			"multiplier":   multiplier,
		})
	}
	args := map[string]string{
		"districtId":   districtID,
		"instrumentId": instrumentID,
		"multiplier":   strconv.FormatFloat(multiplier, 'f', -1, 64),
	}
	return send(opCommand(s.correlationID, opSetTaxMultiplier, args))
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
