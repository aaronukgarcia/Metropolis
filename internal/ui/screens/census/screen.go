package census

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type SendCommandFunc func(protocol.Command) error

// Fixed Op strings for this screen's outbound commands (census.<verb>,
// the ui.screen.trade/ui.screen.services ASM-1193 precedent: int.protocol's
// sealed v1 command vocabulary has no Select-KPI/Select-Citizen Kind and
// this screen may not edit internal/protocol, so the action rides a
// protocol.DebugPayload command with a fixed Op string instead).
const (
	opSelectKPI     = "census.select-kpi"
	opSelectCitizen = "census.select-citizen"
)

func opCommand(correlationID string, op string, args map[string]string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: op, Args: args},
	}
}

// Screen is F6's protocol-only view-model (GR#20/AC-1): every figure it
// renders arrives through ApplyDelta's "f6.census" subscription patches,
// never a direct internal/engine/census read.
type Screen struct {
	mu sync.Mutex

	self atomic.Pointer[Screen]

	correlationID           string
	subs                    map[protocol.SubscriptionID]string
	stale                   bool
	haveData                bool
	selectionRejectedReason string

	ageBands     [NumAgeBands]int64
	haveAgeBands bool

	sexSeries     [NumSexBuckets]int64
	haveSexSeries bool

	educationTiers     [NumEducationTiers]int64
	haveEducationTiers bool

	blueWhiteCollar     BlueWhiteCollar
	haveBlueWhiteCollar bool

	kpis     []KPITile
	haveKPIs bool

	kpiSources     map[string]KPISource
	haveKPISources bool

	selectedBio *CitizenBio
	haveBio     bool

	linkage     EducationCrimeLinkage
	haveLinkage bool
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

// ApplyResult surfaces a rejected census.select-kpi/census.select-citizen
// command (mirrors services.Screen.ApplyResult/finance.Screen.ApplyResult
// exactly). Gated on the ui.router binding per the ASM-1482 note (doc.go).
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
			s.selectionRejectedReason = res.Error.Display
		} else {
			s.selectionRejectedReason = ""
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

	if p.AgeBands != nil {
		s.ageBands = *p.AgeBands
		s.haveAgeBands = true
	} else {
		s.haveAgeBands = false
	}

	if p.SexSeries != nil {
		s.sexSeries = *p.SexSeries
		s.haveSexSeries = true
	} else {
		s.haveSexSeries = false
	}

	if p.EducationTiers != nil {
		s.educationTiers = *p.EducationTiers
		s.haveEducationTiers = true
	} else {
		s.haveEducationTiers = false
	}

	if p.BlueWhiteCollar != nil {
		s.blueWhiteCollar = BlueWhiteCollar{Blue: p.BlueWhiteCollar.Blue, White: p.BlueWhiteCollar.White}
		s.haveBlueWhiteCollar = true
	} else {
		s.haveBlueWhiteCollar = false
	}

	if p.KPIs != nil {
		s.kpis = make([]KPITile, len(*p.KPIs))
		for i, k := range *p.KPIs {
			s.kpis[i] = KPITile(k)
		}
		s.haveKPIs = true
	} else {
		s.haveKPIs = false
	}

	if p.KPISources != nil {
		s.kpiSources = make(map[string]KPISource, len(*p.KPISources))
		for _, ks := range *p.KPISources {
			if ks.Unavailable {
				s.logKPIUnavailable(ks.Key, ks.Reason)
			}
			s.kpiSources[ks.Key] = KPISource{
				Key:         ks.Key,
				EntityIDs:   append([]uint64(nil), ks.EntityIDs...),
				LineValue:   ks.LineValue,
				Unavailable: ks.Unavailable,
				Reason:      ks.Reason,
			}
		}
		s.haveKPISources = true
	} else {
		s.haveKPISources = false
	}

	if p.SelectedBio != nil {
		if p.SelectedBio.Unavailable {
			s.logBioUnavailable(p.SelectedBio.GUID, p.SelectedBio.Reason)
		}
		bio := citizenBioFromWire(*p.SelectedBio)
		s.selectedBio = &bio
		s.haveBio = true
	} else {
		s.selectedBio = nil
		s.haveBio = false
	}

	if p.EducationCrimeLinkage != nil {
		s.linkage = EducationCrimeLinkage{
			Population:         p.EducationCrimeLinkage.Population,
			MeanAttainment:     p.EducationCrimeLinkage.MeanAttainment,
			CrimeRate:          p.EducationCrimeLinkage.CrimeRate,
			UneducatedFraction: p.EducationCrimeLinkage.UneducatedFraction,
			PolicyCoefficient:  p.EducationCrimeLinkage.PolicyCoefficient,
		}
		s.haveLinkage = true
	} else {
		s.haveLinkage = false
	}
}

// citizenBioFromWire converts a wireCitizenBio into the exported CitizenBio
// view type, deep-copying the Stages slice (SEC-063 — no aliasing of the
// decoded wire struct's backing array).
func citizenBioFromWire(w wireCitizenBio) CitizenBio {
	stages := make([]EducationStage, len(w.Stages))
	for i, st := range w.Stages {
		stages[i] = EducationStage(st)
	}
	return CitizenBio{
		GUID:       w.GUID,
		ID:         w.ID,
		BirthMonth: w.BirthMonth,
		Sex:        w.Sex,
		Education: CitizenEducationBio{
			Attainment:  w.Attainment,
			Schooling:   w.Schooling,
			Stages:      stages,
			IndustryTie: w.IndustryTie,
		},
		Employment:  CitizenEmploymentBio{State: w.State, Sector: w.Sector, Workplace: w.Workplace},
		Family:      CitizenFamilyBio{Household: w.Household, Partner: w.Partner, Home: w.Home},
		Retirement:  w.Retirement,
		Income:      w.Income,
		Unavailable: w.Unavailable,
		Reason:      w.Reason,
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

func (s *Screen) SelectionRejectedReason() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectionRejectedReason"}); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectionRejectedReason
}

// AgeBandSeries returns AC-3's age-band spline: source
// census.demographics.ageBands (AC-2's field-traceability table, doc.go).
func (s *Screen) AgeBandSeries() ([NumAgeBands]int64, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "AgeBandSeries"}); err != nil {
		return [NumAgeBands]int64{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveAgeBands {
		return [NumAgeBands]int64{}, false
	}
	return s.ageBands, true
}

// SexSeries returns AC-3's sex spline: source census.demographics.sexSeries.
func (s *Screen) SexSeries() ([NumSexBuckets]int64, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SexSeries"}); err != nil {
		return [NumSexBuckets]int64{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveSexSeries {
		return [NumSexBuckets]int64{}, false
	}
	return s.sexSeries, true
}

// EducationTierSeries returns AC-3's education-tier spline: source
// census.demographics.educationTiers.
func (s *Screen) EducationTierSeries() ([NumEducationTiers]int64, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "EducationTierSeries"}); err != nil {
		return [NumEducationTiers]int64{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveEducationTiers {
		return [NumEducationTiers]int64{}, false
	}
	return s.educationTiers, true
}

// BlueWhiteCollarSplit returns AC-4's workforce split: source
// census.demographics.blueWhiteCollar.
func (s *Screen) BlueWhiteCollarSplit() (BlueWhiteCollar, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BlueWhiteCollarSplit"}); err != nil {
		return BlueWhiteCollar{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveBlueWhiteCollar {
		return BlueWhiteCollar{}, false
	}
	return s.blueWhiteCollar, true
}

// KPITiles returns AC-5's eight city-KPI tiles: source census.demographics.kpis.
func (s *Screen) KPITiles() ([]KPITile, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "KPITiles"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveKPIs {
		return nil, false
	}
	res := make([]KPITile, len(s.kpis))
	copy(res, s.kpis)
	return res, true
}

// KPISource returns AC-6's drill-in resolution for one KPI key. ok=false
// when this KPI's source has not been sent (distinct from Unavailable=true,
// which means the engine actively rejected the query, AC-12).
func (s *Screen) KPISource(key string) (KPISource, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "KPISource"}); err != nil {
		return KPISource{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveKPISources {
		return KPISource{}, false
	}
	src, ok := s.kpiSources[key]
	if !ok {
		return KPISource{}, false
	}
	return KPISource{
		Key:         src.Key,
		EntityIDs:   append([]uint64(nil), src.EntityIDs...),
		LineValue:   src.LineValue,
		Unavailable: src.Unavailable,
		Reason:      src.Reason,
	}, true
}

// SelectedBio returns AC-7's currently-drilled citizen bio.
func (s *Screen) SelectedBio() (CitizenBio, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedBio"}); err != nil {
		return CitizenBio{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveBio || s.selectedBio == nil {
		return CitizenBio{}, false
	}
	bio := *s.selectedBio
	bio.Education.Stages = append([]EducationStage(nil), s.selectedBio.Education.Stages...)
	return bio, true
}

// EducationCrimeLinkageView returns AC-8's education→crime linkage report.
func (s *Screen) EducationCrimeLinkageView() (EducationCrimeLinkage, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "EducationCrimeLinkageView"}); err != nil {
		return EducationCrimeLinkage{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveLinkage {
		return EducationCrimeLinkage{}, false
	}
	return s.linkage, true
}

// SelectKPI issues AC-6's KPI drill-in selection as a protocol.DebugPayload
// command with the fixed Op string "census.select-kpi" (ASM-1193
// precedent). The engine's resolved source arrives back through the next
// "f6.census" patch's kpiSources field (KPISource), not this call's return
// value — mirroring services.Screen.SetFunding's fire-and-await-patch
// shape. A rejection surfaces via ApplyResult/SelectionRejectedReason,
// gated on the ui.router binding (ASM-1482, doc.go).
func (s *Screen) SelectKPI(send SendCommandFunc, key string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectKPI"}); err != nil {
		return err
	}
	return send(opCommand(s.correlationID, opSelectKPI, map[string]string{"key": key}))
}

// SelectCitizen issues AC-7's citizen-bio drill-in selection as a
// protocol.DebugPayload command with the fixed Op string
// "census.select-citizen" (ASM-1193 precedent). guid is the census GUID
// (e.g. "citizen:42") named by a KPISource's EntityIDs (AC-6->AC-7 chain).
func (s *Screen) SelectCitizen(send SendCommandFunc, guid string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectCitizen"}); err != nil {
		return err
	}
	return send(opCommand(s.correlationID, opSelectCitizen, map[string]string{"guid": guid}))
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

func (s *Screen) logKPIUnavailable(key, reason string) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logKPIUnavailable"}); err != nil {
		return
	}
	_ = errs.New(ErrKPIUnavailable, s.correlationID, map[string]any{
		"key":    key,
		"reason": reason,
	})
}

func (s *Screen) logBioUnavailable(guid, reason string) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "logBioUnavailable"}); err != nil {
		return
	}
	_ = errs.New(ErrBioUnavailable, s.correlationID, map[string]any{
		"guid":   guid,
		"reason": reason,
	})
}
