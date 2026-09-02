package tax

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DistrictID names a city district for per-district rate multipliers (AC-6
// / ASM-285's cell-tagged district model). A plain identifier: engine.tax
// stores and resolves multipliers against it and never resolves it to
// geometry, drawing or naming (out of scope for this item).
type DistrictID string

// instrumentState is one instrument's runtime state: its immutable,
// data-loaded definition plus the mutable rate/base/EV-share the player (or
// a later policy module) sets.
type instrumentState struct {
	def     data.TaxInstrument // immutable after Load
	rate    float64            // current headline rate (percent)
	base    finance.Money      // full (pre-elasticity) base
	evShare float64            // external base-erosion fraction in [0,1]
}

// TaxAPI is code.json's "engine.tax" inbound contract (TaxAPI, "instruments
// as data-defined curves; incidence computed, not asserted"). It owns the
// six data-defined tax instruments loaded from data/tax_instruments.json,
// their settable headline rates, the per-district rate multipliers, and the
// external base-erosion (EV-share) input, and posts collected revenue
// through engine.finance's ledger.
//
// The zero value is not usable; construct via [Load] or [LoadDefault].
// A *TaxAPI is safe for concurrent use (AC-15): every mutable field is
// guarded by mu, instrument definitions are immutable after Load, and
// checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class).
type TaxAPI struct {
	mu            sync.RWMutex
	correlationID string

	instruments map[string]*instrumentState       // instrument ID -> state
	districts   map[DistrictID]map[string]float64 // district -> instrument ID -> multiplier

	finance *finance.FinanceAPI

	self atomic.Pointer[TaxAPI]
}

// Load reads and schema-validates data/tax_instruments.json from dir (via
// foundation/data.LoadTaxInstruments — the validated-load path, GR#15/GR#17)
// and builds a ready-to-query *TaxAPI whose instruments all default to their
// data-authored reference (baseline) rate. Every load failure is a
// registry-sourced *errs.E, never a silent default or panic.
func Load(dir, correlationID string) (*TaxAPI, error) {
	ti, err := data.LoadTaxInstruments(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrTaxDataInvalid, correlationID, err, map[string]any{
			"dir": dir, "cause": err.Error(),
		})
	}

	t := &TaxAPI{
		correlationID: correlationID,
		instruments:   make(map[string]*instrumentState, len(ti.Instruments)),
		districts:     make(map[DistrictID]map[string]float64),
	}

	ids := make([]string, 0, len(ti.Instruments))
	for id := range ti.Instruments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		def := ti.Instruments[id]
		t.instruments[id] = &instrumentState{def: def, rate: referenceRate(def)}
	}

	// Stored exactly once, before t is returned to any caller.
	t.self.Store(t)
	return t, nil
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it.
func LoadDefault(correlationID string) (*TaxAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *TaxAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load.
func (t *TaxAPI) checkNotCopied(method string) error {
	if t.self.Load() != t {
		return errs.New(ErrCopiedValue, t.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetFinance wires the engine.finance dependency used by Collect/CollectAll/
// CollectedRevenue (ledger posting) — the registered engine.tax → engine.finance
// edge (GR#20). A nil finance leaves those operations failing with
// ErrFinanceNotWired rather than silently no-op'ing (GR#17).
func (t *TaxAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := t.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finance = f
	return nil
}

// sortedIDsLocked returns the instrument IDs in ascending order (GR#21 —
// never map-iteration order on a path whose result matters). Caller holds
// at least a read lock.
func (t *TaxAPI) sortedIDsLocked() []string {
	if err := t.checkNotCopied("sortedIDsLocked"); err != nil {
		return nil
	}
	ids := make([]string, 0, len(t.instruments))
	for id := range t.instruments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// lookupLocked resolves an instrument ID, returning ErrUnknownInstrument for
// any key not loaded from data (AC-12) — never a zero-value instrument.
func (t *TaxAPI) lookupLocked(instrumentID string) (*instrumentState, error) {
	if err := t.checkNotCopied("lookupLocked"); err != nil {
		return nil, err
	}
	st, ok := t.instruments[instrumentID]
	if !ok {
		return nil, errs.New(ErrUnknownInstrument, t.correlationID, map[string]any{"instrument": instrumentID})
	}
	return st, nil
}

// SetRate sets an instrument's headline rate (a percentage). The rate must
// be finite and inside the instrument's data-loaded rateRange (AC-11); an
// out-of-range or non-finite rate is rejected with a registry-sourced error
// and the current rate is left unchanged — never clamped, never silently
// accepted.
func (t *TaxAPI) SetRate(instrumentID string, rate float64) error {
	if err := t.checkNotCopied("SetRate"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return err
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return errs.New(ErrNonFiniteRate, t.correlationID, map[string]any{"instrument": instrumentID, "rate": rate})
	}
	min, max := 0.0, 0.0
	if rr := st.def.RateRange; rr != nil {
		min, max = rr.MinPercent, rr.MaxPercent
	}
	if rate < min || rate > max {
		return errs.New(ErrRateOutOfRange, t.correlationID, map[string]any{
			"instrument": instrumentID, "rate": rate, "min": min, "max": max,
		})
	}
	st.rate = rate
	return nil
}

// SetBase sets an instrument's full (pre-elasticity) base in micro-pounds.
// The base is the taxed economic activity at the reference rate; the
// elasticity and EV-share responses shrink it from here. Negative bases are
// rejected (money is never negative, GR#16).
func (t *TaxAPI) SetBase(instrumentID string, base finance.Money) error {
	if err := t.checkNotCopied("SetBase"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return err
	}
	if base < 0 {
		return errs.New(ErrNegativeBase, t.correlationID, map[string]any{"instrument": instrumentID, "base": int64(base)})
	}
	st.base = base
	return nil
}

// SetEVShare sets an instrument's external base-erosion input (the
// fuel-duty EV-share shape, AC-9): the fraction of the base eroded by
// external substitution (e.g. EV adoption eroding fuel-duty revenue),
// independent of the rate. share must be a finite number in [0,1].
func (t *TaxAPI) SetEVShare(instrumentID string, share float64) error {
	if err := t.checkNotCopied("SetEVShare"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return err
	}
	if math.IsNaN(share) || math.IsInf(share, 0) || share < 0 || share > 1 {
		return errs.New(ErrInvalidEVShare, t.correlationID, map[string]any{"instrument": instrumentID, "share": share})
	}
	st.evShare = share
	return nil
}

// SetDistrictMultiplier sets an optional per-district rate multiplier for an
// instrument (AC-6). The multiplier stacks with the citywide base rate:
// effective rate = base rate × multiplier. 1.0 means no change; a multiplier
// of 0 is accepted only where it keeps the effective rate within the
// instrument's rateRange (i.e. minPercent == 0). district must be non-empty
// and multiplier a finite number >= 0; the resulting effective rate must stay
// within the instrument's data-loaded rateRange at BOTH ends (SEC-098: an
// unbounded multiplier would blow the AC-11 rate cap at district level and
// make revenue non-monotonic, and a sub-min effective rate is a balance-regime
// change — not a silent district discount).
func (t *TaxAPI) SetDistrictMultiplier(district DistrictID, instrumentID string, multiplier float64) error {
	if err := t.checkNotCopied("SetDistrictMultiplier"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if district == "" {
		return errs.New(ErrInvalidDistrictMultiplier, t.correlationID, map[string]any{
			"instrument": instrumentID, "district": string(district), "multiplier": multiplier,
		})
	}
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return err
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 {
		return errs.New(ErrInvalidDistrictMultiplier, t.correlationID, map[string]any{
			"instrument": instrumentID, "district": string(district), "multiplier": multiplier,
		})
	}
	// SEC-098: bound the effective rate within the instrument's declared
	// rateRange — BOTH ends. A multiplier driving the effective rate below
	// minPercent or above maxPercent is rejected (the doc's "must stay within
	// rateRange" is the contract; sub-min rates are a balance-regime change).
	min, max := 0.0, 0.0
	if rr := st.def.RateRange; rr != nil {
		min, max = rr.MinPercent, rr.MaxPercent
	}
	effective := st.rate * multiplier
	if effective < min || effective > max {
		return errs.New(ErrInvalidDistrictMultiplier, t.correlationID, map[string]any{
			"instrument":    instrumentID,
			"district":      string(district),
			"multiplier":    multiplier,
			"effectiveRate": effective,
			"min":           min,
			"max":           max,
		})
	}
	if t.districts[district] == nil {
		t.districts[district] = make(map[string]float64)
	}
	t.districts[district][instrumentID] = multiplier
	return nil
}

// GetDistrictMultiplier reads back the applied per-district rate multiplier
// for an instrument (AC-6's read-back): the value [SetDistrictMultiplier]
// most recently stored for that (district, instrument), or 1.0 (neutral) when
// none has been set. It reads the applied state at call time — never a
// policies-side mirror or a derived copy — so a consumer composing a further
// move (engine.policies' Enact) sees any out-of-band mutation to the real
// multiplier rather than silently clobbering it with a stale figure.
//
// district must be non-empty (ErrInvalidDistrictMultiplier) and instrumentID
// a loaded instrument (ErrUnknownInstrument) — matching SetDistrictMultiplier's
// validation, never a zero value silently treated as valid.
func (t *TaxAPI) GetDistrictMultiplier(district DistrictID, instrumentID string) (float64, error) {
	if err := t.checkNotCopied("GetDistrictMultiplier"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if district == "" {
		return 0, errs.New(ErrInvalidDistrictMultiplier, t.correlationID, map[string]any{
			"instrument": instrumentID, "district": string(district), "multiplier": "N/A (empty district)",
		})
	}
	if _, err := t.lookupLocked(instrumentID); err != nil {
		return 0, err
	}
	if d, ok := t.districts[district]; ok {
		if m, ok := d[instrumentID]; ok {
			return m, nil
		}
	}
	return 1.0, nil
}

// InstrumentInfo is the query surface for one instrument (AC-1/US-6): its
// data identity, current rate, rate bounds, elasticity, reference rate, and
// the rate/EV-responsive base, revenue and incidence computed from the
// current state at call time (never cached).
type InstrumentInfo struct {
	ID            string
	Name          string
	Category      string
	Rate          float64
	RateMin       float64
	RateMax       float64
	Elasticity    float64
	ReferenceRate float64
	Base          finance.Money
	Revenue       finance.Money
	Incidence     []BearerShare
	EVShare       float64
}

// infoLocked builds one instrument's query info from its current state.
// Caller holds at least a read lock.
func (t *TaxAPI) infoLocked(id string) InstrumentInfo {
	if err := t.checkNotCopied("infoLocked"); err != nil {
		return InstrumentInfo{}
	}
	st := t.instruments[id]
	rRef := referenceRate(st.def)
	e := elasticityCoeff(st.def)
	info := InstrumentInfo{
		ID:            id,
		Name:          st.def.Name,
		Category:      st.def.Category,
		Rate:          st.rate,
		Elasticity:    e,
		ReferenceRate: rRef,
		Base:          taxedBaseAt(st.base, st.evShare, rRef, e, st.rate),
		Revenue:       revenueAt(st.base, st.evShare, rRef, e, st.rate),
		Incidence:     incidenceSharesAt(st.def, st.rate),
		EVShare:       st.evShare,
	}
	if rr := st.def.RateRange; rr != nil {
		info.RateMin = rr.MinPercent
		info.RateMax = rr.MaxPercent
	}
	return info
}

// Instruments lists every loaded instrument with its current rate, revenue,
// elasticity-curve inputs and incidence, in sorted instrument-ID order
// (GR#21). The returned slice is owned by the caller — never an alias of the
// internal map (weakness pattern #1).
func (t *TaxAPI) Instruments() []InstrumentInfo {
	if err := t.checkNotCopied("Instruments"); err != nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	ids := t.sortedIDsLocked()
	out := make([]InstrumentInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, t.infoLocked(id))
	}
	return out
}

// Instrument returns one instrument's query info (AC-12's query half).
func (t *TaxAPI) Instrument(instrumentID string) (InstrumentInfo, error) {
	if err := t.checkNotCopied("Instrument"); err != nil {
		return InstrumentInfo{}, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if _, err := t.lookupLocked(instrumentID); err != nil {
		return InstrumentInfo{}, err
	}
	return t.infoLocked(instrumentID), nil
}

// TaxedBase returns an instrument's current rate-responsive, EV-eroded
// taxed base (AC-3): the full base shrunk by the elasticity and EV-share
// responses at the current rate.
func (t *TaxAPI) TaxedBase(instrumentID string) (finance.Money, error) {
	if err := t.checkNotCopied("TaxedBase"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return 0, err
	}
	return taxedBaseAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), st.rate), nil
}

// Revenue returns an instrument's current citywide revenue (AC-4): the
// current rate multiplied by the current, rate-responsive base — never a
// cached pre-change value.
func (t *TaxAPI) Revenue(instrumentID string) (finance.Money, error) {
	if err := t.checkNotCopied("Revenue"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return 0, err
	}
	return revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), st.rate), nil
}

// effectiveRateLocked returns an instrument's rate in a district: the
// citywide rate times the district multiplier (1.0 when none is set).
func (t *TaxAPI) effectiveRateLocked(instrumentID string, district DistrictID) float64 {
	if err := t.checkNotCopied("effectiveRateLocked"); err != nil {
		return 0
	}
	rate := t.instruments[instrumentID].rate
	if d, ok := t.districts[district]; ok {
		if m, ok := d[instrumentID]; ok {
			rate *= m
		}
	}
	return rate
}

// RevenueInDistrict returns an instrument's revenue scoped to a district
// (AC-6): revenue at the district-multiplied rate.
func (t *TaxAPI) RevenueInDistrict(instrumentID string, district DistrictID) (finance.Money, error) {
	if err := t.checkNotCopied("RevenueInDistrict"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return 0, err
	}
	rate := t.effectiveRateLocked(instrumentID, district)
	return revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), rate), nil
}

// RateInZone returns instrumentID's rate in a §34 zone class (AC-19,
// ASM-416): the citywide rate × the instrument's OWN zoneCoefficient for that
// zone — never the fixed categoryProperty lookup [zoneOverrideInstrument]
// uses for the land-value-based [BusinessRateRevenue] calculation. Any of
// the six loaded instruments may carry a populated zoneOverrides entry (the
// schema is generalised, not businessRates-only); this is the read path that
// honours it regardless of the instrument's category. A zone class outside
// §34's closed enum is rejected by the underlying zoneCoefficient call
// (ErrUnknownZoneClass) — never normalised.
func (t *TaxAPI) RateInZone(instrumentID, zoneClass string) (float64, error) {
	if err := t.checkNotCopied("RateInZone"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return 0, err
	}
	coeff, err := t.zoneCoefficient(st.def, zoneClass)
	if err != nil {
		return 0, err
	}
	return st.rate * coeff, nil
}

// RevenueInZone returns an instrument's revenue at its zone-scoped rate
// (AC-19's revenue counterpart to [RateInZone]): the instrument's own full
// base run through the elasticity/EV-share curve at rate × zoneCoefficient(
// zoneClass) — never a second, independently-derived base. A zone override
// changes which RATE the base is taxed at; it never adds or removes money
// from the base itself (conservation — an override is a rate lever, not a
// mint). Zones the instrument declares no override for resolve to the
// citywide rate (zoneCoefficient's 1.0 default), matching RateInZone.
func (t *TaxAPI) RevenueInZone(instrumentID, zoneClass string) (finance.Money, error) {
	if err := t.checkNotCopied("RevenueInZone"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return 0, err
	}
	coeff, err := t.zoneCoefficient(st.def, zoneClass)
	if err != nil {
		return 0, err
	}
	rate := st.rate * coeff
	return revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), rate), nil
}

// IncidenceDisplay is AC-5's per-bearer-category incidence breakdown at the
// current rate: who bears the cost, recomputed from the rate at call time
// (never a fixed lookup table).
type IncidenceDisplay struct {
	InstrumentID string
	Rate         float64
	Shares       []BearerShare // sums to 1.0
}

// IncidenceDisplay returns an instrument's bearer split at the current rate
// (AC-5). The proportions — not just the totals — are recomputed from the
// rate, so a rate change moves the split.
func (t *TaxAPI) IncidenceDisplay(instrumentID string) (IncidenceDisplay, error) {
	if err := t.checkNotCopied("IncidenceDisplay"); err != nil {
		return IncidenceDisplay{}, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return IncidenceDisplay{}, err
	}
	return IncidenceDisplay{InstrumentID: instrumentID, Rate: st.rate, Shares: incidenceSharesAt(st.def, st.rate)}, nil
}

// IncidenceDisplayInDistrict returns the incidence at the district-multiplied
// rate (AC-6).
func (t *TaxAPI) IncidenceDisplayInDistrict(instrumentID string, district DistrictID) (IncidenceDisplay, error) {
	if err := t.checkNotCopied("IncidenceDisplayInDistrict"); err != nil {
		return IncidenceDisplay{}, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return IncidenceDisplay{}, err
	}
	rate := t.effectiveRateLocked(instrumentID, district)
	return IncidenceDisplay{InstrumentID: instrumentID, Rate: rate, Shares: incidenceSharesAt(st.def, rate)}, nil
}

// CurvePoint is one sampled point on an instrument's elasticity/revenue
// response curve.
type CurvePoint struct {
	RatePercent float64
	Base        finance.Money
	Revenue     finance.Money
}

// ElasticityCurve is an instrument's sampled base/revenue response curve
// (US-6): the data-defined shape made queryable, not rendered text.
type ElasticityCurve struct {
	InstrumentID  string
	ReferenceRate float64
	Coefficient   float64
	Points        []CurvePoint
}

// ElasticityCurve returns an instrument's response curve, sampled from its
// reference rate up to its maximum valid rate (US-6/AC-1). The curve is
// derived from the same taxedBaseAt/revenueAt functions the live Revenue
// query uses — never a separately-drawn decorative curve.
func (t *TaxAPI) ElasticityCurve(instrumentID string) (ElasticityCurve, error) {
	if err := t.checkNotCopied("ElasticityCurve"); err != nil {
		return ElasticityCurve{}, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		return ElasticityCurve{}, err
	}
	rRef := referenceRate(st.def)
	e := elasticityCoeff(st.def)
	max := rRef
	if rr := st.def.RateRange; rr != nil && rr.MaxPercent > max {
		max = rr.MaxPercent
	}

	const n = 6
	curve := ElasticityCurve{InstrumentID: instrumentID, ReferenceRate: rRef, Coefficient: e, Points: make([]CurvePoint, 0, n)}
	if max <= rRef {
		curve.Points = append(curve.Points, CurvePoint{
			RatePercent: rRef,
			Base:        taxedBaseAt(st.base, st.evShare, rRef, e, rRef),
			Revenue:     revenueAt(st.base, st.evShare, rRef, e, rRef),
		})
		return curve, nil
	}
	for i := 0; i < n; i++ {
		r := rRef + (max-rRef)*float64(i)/float64(n-1)
		curve.Points = append(curve.Points, CurvePoint{
			RatePercent: r,
			Base:        taxedBaseAt(st.base, st.evShare, rRef, e, r),
			Revenue:     revenueAt(st.base, st.evShare, rRef, e, r),
		})
	}
	return curve, nil
}

// ZoneCell pairs a §34 zone class with finance's LandCell input for one
// cell (AC-7). BusinessRateRevenue multiplies the zone-class coefficient by
// the cell's land value, sourced from finance.LandPrice — never a second,
// independently-maintained land-value figure inside engine.tax.
type ZoneCell struct {
	ZoneClass string
	Cell      finance.LandCell
}

// zoneOverrideInstrument returns the instrument whose zoneOverrides drive
// [BusinessRateRevenue]'s AC-7 land-value×zone calculation: the
// property-category instrument carrying at least one zone override (the
// data's zone-scoped business-rates entry). Sorted scan so the result is
// deterministic if the set ever grows.
//
// This is BusinessRateRevenue's own lookup, scoped to categoryProperty
// because AC-7 is specifically the land-value-based revenue calculation —
// it is NOT the general zoneOverrides dispatch for the other five
// instruments (ASM-416/AC-19, BUG-588). A non-property instrument's own
// zoneOverrides is honoured via [RateInZone]/[RevenueInZone], which resolve
// zoneCoefficient against that instrument's own def — never this
// category-filtered helper.
func (t *TaxAPI) zoneOverrideInstrument() (*instrumentState, bool) {
	if err := t.checkNotCopied("zoneOverrideInstrument"); err != nil {
		return nil, false
	}
	for _, id := range t.sortedIDsLocked() {
		st := t.instruments[id]
		if st.def.Category == categoryProperty && len(st.def.ZoneOverrides) > 0 {
			return st, true
		}
	}
	return nil, false
}

// zoneCoefficient returns the per-zone rate coefficient for zoneClass: the
// instrument's zoneOverride rateMultiplier (or relief-derived coefficient)
// when one is declared, else 1.0 (the full citywide rate). A zone class
// outside §34's closed 8-way enum is rejected (weakness pattern #4: a
// zone-class key is a lookup key, so an unknown spelling is hostile input,
// never normalised).
func (t *TaxAPI) zoneCoefficient(def data.TaxInstrument, zoneClass string) (float64, error) {
	if err := t.checkNotCopied("zoneCoefficient"); err != nil {
		return 0, err
	}
	if !zoneClassEnum[zoneClass] {
		return 0, errs.New(ErrUnknownZoneClass, t.correlationID, map[string]any{"zone": zoneClass})
	}
	ov, ok := def.ZoneOverrides[zoneClass]
	if !ok {
		return 1.0, nil
	}
	if ov.RateMultiplier != nil {
		return *ov.RateMultiplier, nil
	}
	if ov.ReliefPercent != nil {
		return (100 - *ov.ReliefPercent) / 100, nil
	}
	return 1.0, nil
}

// BusinessRateRevenue computes revenue for the zone-scoped property
// instrument across the given cells (AC-7): each cell contributes
// zoneCoefficient(zone) × finance.LandPrice(cell), the contributions are
// summed into the instrument's full base, and the result is the current
// rate applied to that rate-responsive base. The land value comes from
// finance.LandPrice (the registered engine.tax → engine.finance edge) —
// never a second land-value figure.
func (t *TaxAPI) BusinessRateRevenue(cells []ZoneCell) (finance.Money, error) {
	if err := t.checkNotCopied("BusinessRateRevenue"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	st, ok := t.zoneOverrideInstrument()
	if !ok {
		t.mu.RUnlock()
		return 0, errs.New(ErrUnknownInstrument, t.correlationID, map[string]any{"instrument": "zone-override property instrument"})
	}
	def := st.def
	rate := st.rate
	evShare := st.evShare
	t.mu.RUnlock()

	var fullBase finance.Money
	for _, c := range cells {
		coeff, err := t.zoneCoefficient(def, c.ZoneClass)
		if err != nil {
			return 0, err
		}
		fullBase = satAddMoney(fullBase, moneyFromFloat(coeff*float64(finance.LandPrice(c.Cell))))
	}
	return revenueAt(fullBase, evShare, referenceRate(def), elasticityCoeff(def), rate), nil
}

// Collect computes instrumentID's current revenue and posts it through the
// wired engine.finance ledger as a tax transfer (payer → treasury), returning
// the posted amount (AC-8). Requires SetFinance. Zero or negative revenue
// posts nothing and returns 0.
func (t *TaxAPI) Collect(instrumentID string) (finance.Money, error) {
	if err := t.checkNotCopied("Collect"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	st, err := t.lookupLocked(instrumentID)
	if err != nil {
		t.mu.RUnlock()
		return 0, err
	}
	def := st.def
	rev := revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), st.rate)
	spec := postingFor(def)
	fin := t.finance
	t.mu.RUnlock()

	if fin == nil {
		return 0, errs.New(ErrFinanceNotWired, t.correlationID, map[string]any{"operation": "Collect"})
	}
	if rev <= 0 {
		return 0, nil
	}
	if _, err := fin.Post(finance.Transaction{
		Description: "tax collection (" + instrumentID + ")",
		Entries: []finance.Entry{
			{Account: spec.payer, Side: finance.SideDebit, Amount: rev, Category: spec.cat},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: rev, Category: spec.cat},
		},
	}); err != nil {
		return 0, err
	}
	return rev, nil
}

// CollectAll posts every instrument's current revenue in sorted instrument-ID
// order (GR#21) and returns the total posted. The total is the per-call sum
// of what this call posted — engine.tax keeps no running "total collected"
// accumulator; the ledger-derived figure is [CollectedRevenue].
func (t *TaxAPI) CollectAll() (finance.Money, error) {
	if err := t.checkNotCopied("CollectAll"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	type posting struct {
		id   string
		rev  finance.Money
		spec postingSpec
	}
	posts := make([]posting, 0, len(t.instruments))
	for _, id := range t.sortedIDsLocked() {
		st := t.instruments[id]
		posts = append(posts, posting{
			id:   id,
			rev:  revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), st.rate),
			spec: postingFor(st.def),
		})
	}
	fin := t.finance
	t.mu.RUnlock()

	if fin == nil {
		return 0, errs.New(ErrFinanceNotWired, t.correlationID, map[string]any{"operation": "CollectAll"})
	}
	var total finance.Money
	for _, p := range posts {
		if p.rev <= 0 {
			continue
		}
		if _, err := fin.Post(finance.Transaction{
			Description: "tax collection (" + p.id + ")",
			Entries: []finance.Entry{
				{Account: p.spec.payer, Side: finance.SideDebit, Amount: p.rev, Category: p.spec.cat},
				{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: p.rev, Category: p.spec.cat},
			},
		}); err != nil {
			return 0, err
		}
		total = satAddMoney(total, p.rev)
	}
	return total, nil
}

// CollectedRevenue returns the total tax revenue recorded in the wired
// engine.finance ledger (AC-8): it derives from a FinanceAPI query at call
// time, never from an engine.tax accumulator.
func (t *TaxAPI) CollectedRevenue() (finance.Money, error) {
	if err := t.checkNotCopied("CollectedRevenue"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	fin := t.finance
	t.mu.RUnlock()
	if fin == nil {
		return 0, errs.New(ErrFinanceNotWired, t.correlationID, map[string]any{"operation": "CollectedRevenue"})
	}
	return fin.TaxRevenue(), nil
}

// RevenueTotal returns the projected citywide revenue: the sum of every
// instrument's current Revenue, in sorted instrument-ID order (GR#21). It is
// a pure per-call computation (no posting, no accumulator) and is the
// pre-collection counterpart to [CollectedRevenue].
func (t *TaxAPI) RevenueTotal() finance.Money {
	if err := t.checkNotCopied("RevenueTotal"); err != nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total finance.Money
	for _, id := range t.sortedIDsLocked() {
		st := t.instruments[id]
		total = satAddMoney(total, revenueAt(st.base, st.evShare, referenceRate(st.def), elasticityCoeff(st.def), st.rate))
	}
	return total
}
