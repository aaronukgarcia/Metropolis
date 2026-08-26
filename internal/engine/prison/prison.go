package prison

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Admission is one independent intake-ledger entry (AC-2): the data this
// package records the moment engine.crime sentences an offender, built from
// the sentencing event rather than trusted as a bare count. Each admission
// carries its own citizen ID, sentence length, assigned category, and a
// sentencing-event reference, so a discrepancy between this ledger's own
// count and engine.crime's figure is genuinely detectable (never a shared
// counter crime merely increments).
type Admission struct {
	CitizenID      uint64
	District       crime.DistrictID
	Month          int64
	Offence        OffenceClass
	Category       Category // "" means "assign the matched category" (AC-3's default path)
	SentenceMonths int64
	Youth          bool
	SentencingRef  uint64 // the offender-stream id crime's sentencing drew
}

// CohortRecord is the per-citizen cohort record (AC-1): independently
// queryable by citizen ID, with independent category/regime/outcome fields
// — never an aggregate headcount with no traceable owner (the exact
// anti-pattern AC-1 rejects).
type CohortRecord struct {
	CitizenID      uint64
	Offence        OffenceClass
	Category       Category
	Youth          bool
	Released       bool
	Reoffended     bool
	SentenceMonths int64
	AdmittedMonth  int64
}

// RehabSpendRequest is a rehab-spend funding-increase decision (AC-9
// interim). Until BUG-058 registers the engine.prison → engine.projections
// edge, the Slow-Fuse submission is deferred; a valid decision must carry a
// FuseYears tag in the documented [5,15] window AND a locally-computed
// projected-consequence value, and this package's own pre-submission check
// rejects a command missing either. ProjectedConsequence is a pointer so a
// missing value (nil) is distinguishable from a legitimately-zero figure —
// the pre-submission check rejects nil outright rather than silently
// treating "not supplied" as a numeric zero.
type RehabSpendRequest struct {
	Line                 RegimeLine
	Increase             int64
	FuseYears            int64
	ProjectedConsequence *float64
}

// intakeKey is the (district, month) index for the independent intake
// ledger count (AC-2). A struct key, never a map iteration — every
// IntakeCount call is a fixed-key lookup.
type intakeKey struct {
	district crime.DistrictID
	month    int64
}

// PrisonAPI is code.json's "engine.prison" inbound contract (GUID
// 66756617-ac6c-4ffd-9063-0dc0956133f5, PrisonAPI, "cohort tracking on real
// citizen records; Slow-Fuse projections"): the §43 prison estate (local
// jail → category prisons), the category-mismatch penalty, the three
// independently-funded regime programmes, the reoffending formula
// (base − regime − re-entry), the youth-offending pipeline, overcrowding
// (including §36 sold places) degrading every regime effect at once, and
// the independent intake ledger that cross-checks engine.crime's justice
// chain (AC-2).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *PrisonAPI is safe for concurrent use (AC-14): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020 family, mirroring engine.crime).
type PrisonAPI struct {
	correlationID string
	seed          uint64
	cfg           config

	mu sync.RWMutex

	// citizenExists is the injected CitizensAPI existence predicate (AC-10).
	// It is wired via SetCitizenExists by the composition root — never a
	// concrete engine.citizens import — so no unregistered cross-module edge
	// is needed (GR#20/GR#25). Nil means "not wired": Admit fails closed with
	// ErrUnknownCitizen rather than fabricating a placeholder inmate (GR#17).
	citizenExists func(uint64) bool

	// capacity is the domestic prison-place capacity (AC-7's denominator).
	// population is the current non-released inmate count (AC-8's domestic
	// term). soldPlaces is the §36 export-contract commitment (AC-8's sold
	// term) — sourced from engine.capexport's "prison-places" Committed line
	// by the composition root, but tracked here as a distinct, queryable
	// figure from domestic population.
	capacity   int64
	population int64
	soldPlaces int64

	cohorts     map[uint64]CohortRecord
	admissions  []Admission
	intakeCount map[intakeKey]int64

	regime  map[RegimeLine]int64    // funding per programme (AC-4)
	reentry map[ReentryKind]float64 // [0,1] sub-input values (AC-5)

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[PrisonAPI]
}

// New constructs a ready PrisonAPI from a validated config. correlationID is
// attached to every error the returned API constructs (GR#1); an empty one
// mints a fresh ID. The citizen-existence predicate is wired later via
// SetCitizenExists.
func New(seed uint64, cfg config, correlationID string) (*PrisonAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrPrisonDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	a := &PrisonAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		cohorts:       make(map[uint64]CohortRecord),
		intakeCount:   make(map[intakeKey]int64),
		regime:        make(map[RegimeLine]int64),
		reentry:       make(map[ReentryKind]float64),
	}
	// Armed exactly once, before a is returned to any caller (SEC-020).
	a.self.Store(a)
	return a, nil
}

// Load reads and validates data/prison.json from dir and returns a ready
// *PrisonAPI (GR#15). Every failure is a registry-sourced *errs.E — never a
// panic or a silent default.
func Load(dir, correlationID string) (*PrisonAPI, error) {
	cfg, err := loadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(0, cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*PrisonAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(0, cfg, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *PrisonAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (p *PrisonAPI) checkNotCopied(method string) error {
	if p.self.Load() != p {
		return errs.New(ErrCopiedValue, p.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizenExists wires the CitizensAPI existence predicate (AC-10). The
// predicate reports whether a citizen ID is a real, present citizen; Admit
// rejects an ID the predicate reports absent with ErrUnknownCitizen, never
// silently fabricating a placeholder inmate record (GR#17).
func (p *PrisonAPI) SetCitizenExists(fn func(uint64) bool) error {
	if err := p.checkNotCopied("SetCitizenExists"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.citizenExists = fn
	return nil
}

// SetCapacity sets the domestic prison-place capacity (AC-7's denominator).
// A negative capacity is rejected (GR#16).
func (p *PrisonAPI) SetCapacity(n int64) error {
	if err := p.checkNotCopied("SetCapacity"); err != nil {
		return err
	}
	if n < 0 {
		return errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "capacity", "value": n})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.capacity = n
	return nil
}

// Capacity returns the domestic prison-place capacity.
func (p *PrisonAPI) Capacity() int64 {
	if err := p.checkNotCopied("Capacity"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capacity
}

// SetSoldPlaces sets the §36 sold-places commitment (AC-8): a distinct,
// queryable export-contract figure from domestic population, that still
// counts identically toward the overcrowding denominator.
func (p *PrisonAPI) SetSoldPlaces(n int64) error {
	if err := p.checkNotCopied("SetSoldPlaces"); err != nil {
		return err
	}
	if n < 0 {
		return errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "soldPlaces", "value": n})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.soldPlaces = n
	return nil
}

// SoldPlaces returns the §36 sold-places commitment (AC-8), distinct from
// [PrisonAPI.DomesticPopulation].
func (p *PrisonAPI) SoldPlaces() int64 {
	if err := p.checkNotCopied("SoldPlaces"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.soldPlaces
}

// DomesticPopulation returns the current non-released inmate count (AC-8's
// domestic term), distinct from [PrisonAPI.SoldPlaces].
func (p *PrisonAPI) DomesticPopulation() int64 {
	if err := p.checkNotCopied("DomesticPopulation"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.population
}

// matchedCategory returns the category matching an offence's risk profile
// (AC-3): minor → open, serious → standard, violent → high-security. This is
// the spec's structure, not a balance figure, so it lives in code.
func matchedCategory(o OffenceClass) Category {
	switch o {
	case OffenceMinor:
		return CategoryOpen
	case OffenceSerious:
		return CategoryStandard
	case OffenceViolent:
		return CategoryHighSecurity
	}
	return CategoryStandard
}

func validOffence(o OffenceClass) bool {
	switch o {
	case OffenceMinor, OffenceSerious, OffenceViolent:
		return true
	}
	return false
}

func validCategory(c Category) bool {
	switch c {
	case CategoryOpen, CategoryStandard, CategoryHighSecurity:
		return true
	}
	return false
}

// Admit independently records one admission as its own ledger entry (AC-1,
// AC-2): it validates the citizen exists (via the wired predicate), the
// category is registered (or assigns the matched one when empty), and the
// sentence is positive, then creates a queryable cohort record and increments
// the (district, month) intake count. On any failure nothing is mutated —
// never a placeholder inmate, never a silently-clamped record.
func (p *PrisonAPI) Admit(a Admission) error {
	if err := p.checkNotCopied("Admit"); err != nil {
		return err
	}
	if !validOffence(a.Offence) {
		return errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "offence", "value": string(a.Offence)})
	}
	if a.SentenceMonths <= 0 {
		return errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "sentenceMonths", "value": a.SentenceMonths})
	}

	category := a.Category
	if category == "" {
		category = matchedCategory(a.Offence)
	} else if !validCategory(category) {
		return errs.New(ErrUnregisteredCategory, p.correlationID, map[string]any{"category": string(category)})
	}

	// Resolve the existence predicate BEFORE taking the write lock (it may be
	// a composition-root call with its own locking).
	p.mu.RLock()
	exists := p.citizenExists
	p.mu.RUnlock()
	if exists == nil {
		return errs.New(ErrUnknownCitizen, p.correlationID, map[string]any{"citizen": a.CitizenID, "reason": "citizen existence predicate not wired"})
	}
	if !exists(a.CitizenID) {
		return errs.New(ErrUnknownCitizen, p.correlationID, map[string]any{"citizen": a.CitizenID})
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Reject a re-admit of a citizen whose cohort already exists and is not
	// yet released: a second admission of the same live cohort would
	// double-increment population, silently overwrite the first admission's
	// record, and double-count intakeCount — and Release could only decrement
	// once, leaving a permanent conservation leak that corrupts the AC-2
	// intake-vs-crime cross-check. A citizen whose prior cohort is already
	// released may be re-admitted normally.
	if existing, ok := p.cohorts[a.CitizenID]; ok && !existing.Released {
		return invalidAdmission(p.correlationID, "citizenID")
	}

	rec := CohortRecord{
		CitizenID:      a.CitizenID,
		Offence:        a.Offence,
		Category:       category,
		Youth:          a.Youth,
		SentenceMonths: a.SentenceMonths,
		AdmittedMonth:  a.Month,
	}
	p.cohorts[a.CitizenID] = rec
	p.admissions = append(p.admissions, a)
	p.intakeCount[intakeKey{district: a.District, month: a.Month}]++
	p.population++
	return nil
}

// requireCohort returns the cohort record for citizenID or a registry-sourced
// ErrUnknownCitizen error (never a silently-created placeholder). Callers
// hold at least a.mu.RLock.
func (p *PrisonAPI) requireCohort(citizenID uint64) (CohortRecord, error) {
	rec, ok := p.cohorts[citizenID]
	if !ok {
		return CohortRecord{}, errs.New(ErrUnknownCitizen, p.correlationID, map[string]any{"citizen": citizenID})
	}
	return rec, nil
}

// Cohort returns the per-citizen cohort record (AC-1): independently
// queryable by citizen ID with independent category/regime/outcome fields.
func (p *PrisonAPI) Cohort(citizenID uint64) (CohortRecord, bool, error) {
	if err := p.checkNotCopied("Cohort"); err != nil {
		return CohortRecord{}, false, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec, ok := p.cohorts[citizenID]
	if !ok {
		return CohortRecord{}, false, nil
	}
	return rec, true, nil
}

// IntakeCount reports the independently-tracked number of offenders the
// prison actually received from the given district for the given month
// (AC-2). It implements engine.crime's PrisonIntake seam, so
// CrimeAPI.VerifyPrisonIntake cross-checks its own sentenced-to-prison figure
// against this independent ledger.
func (p *PrisonAPI) IntakeCount(district crime.DistrictID, month int64) int64 {
	if err := p.checkNotCopied("IntakeCount"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.intakeCount[intakeKey{district: district, month: month}]
}

// Release releases an inmate (AC-11). Releasing an inmate already released,
// or never admitted, is rejected with ErrAlreadyReleased — never a silent
// no-op.
func (p *PrisonAPI) Release(citizenID uint64) error {
	if err := p.checkNotCopied("Release"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, err := p.requireCohort(citizenID)
	if err != nil {
		return err
	}
	if rec.Released {
		return errs.New(ErrAlreadyReleased, p.correlationID, map[string]any{"citizen": citizenID})
	}
	rec.Released = true
	p.cohorts[citizenID] = rec
	p.population--
	return nil
}

// SetRegimeFunding sets the funding for one regime programme (AC-4). The
// line must be one of the three programmes and the amount non-negative —
// never silently clamped, never a silently-dropped unknown line (AC-11).
func (p *PrisonAPI) SetRegimeFunding(line RegimeLine, amount int64) error {
	if err := p.checkNotCopied("SetRegimeFunding"); err != nil {
		return err
	}
	switch line {
	case RegimeEducation, RegimeWork, RegimeAddictionTreatment:
	default:
		return invalidRegimeFunding(p.correlationID, line, 0)
	}
	if amount < 0 {
		return invalidRegimeFunding(p.correlationID, line, amount)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.regime[line] = amount
	return nil
}

// RegimeFunding returns the funding for one regime programme.
func (p *PrisonAPI) RegimeFunding(line RegimeLine) (int64, error) {
	if err := p.checkNotCopied("RegimeFunding"); err != nil {
		return 0, err
	}
	switch line {
	case RegimeEducation, RegimeWork, RegimeAddictionTreatment:
	default:
		return 0, invalidRegimeFunding(p.correlationID, line, 0)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.regime[line], nil
}

// SetReentrySupport sets one re-entry support sub-input to a [0,1] value
// (AC-5). A value outside [0,1] is rejected (AC-5's independently-sourced
// term — never silently clamped).
func (p *PrisonAPI) SetReentrySupport(kind ReentryKind, value float64) error {
	if err := p.checkNotCopied("SetReentrySupport"); err != nil {
		return err
	}
	switch kind {
	case ReentryProbation, ReentryEmployment, ReentryHousing:
	default:
		return invalidReentrySupport(p.correlationID, kind, 0)
	}
	if !num.IsFinite(value) || value < 0 || value > 1 {
		return invalidReentrySupport(p.correlationID, kind, value)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reentry[kind] = value
	return nil
}

// overcrowdingPressureLocked returns the overcrowding severity in [0,1]
// (AC-7): the excess of (domestic population + sold places) over capacity,
// as a fraction of capacity, clamped to [0,1]. Callers hold at least
// a.mu.RLock. Sold places count identically to domestic population (AC-8).
func (p *PrisonAPI) overcrowdingPressureLocked() float64 {
	if p.capacity <= 0 {
		return 0
	}
	pop := p.population + p.soldPlaces
	if pop <= p.capacity {
		return 0
	}
	pressure := float64(pop-p.capacity) / float64(p.capacity)
	return clamp01(pressure)
}

// rawRegimeEffectLocked returns a programme's un-degraded effect (AC-4): the
// data-loaded maxEffect scaled by the funding's fraction toward its
// cost-for-max, clamped to [0,1]. Each programme's effect is isolated — one
// line's funding never moves another line's contribution.
func (p *PrisonAPI) rawRegimeEffectLocked(line RegimeLine) float64 {
	rl := p.cfg.Regime[string(line)]
	funding := p.regime[line]
	if funding <= 0 {
		return 0
	}
	frac := float64(funding) / float64(rl.CostForMax)
	return rl.MaxEffect * clamp01(frac)
}

// regimeEffectLocked returns a programme's overcrowding-degraded effect
// (AC-7): the raw effect scaled by (1 − degradeMax·pressure), so every one of
// the three programmes degrades in the same tick under overcrowding.
func (p *PrisonAPI) regimeEffectLocked(line RegimeLine) float64 {
	return p.rawRegimeEffectLocked(line) * (1 - p.cfg.Overcrowding.DegradeMax*p.overcrowdingPressureLocked())
}

// reentrySupportLocked returns the summed re-entry support contribution
// (AC-5): the three independently-sourced sub-inputs (probation capacity,
// employment uptake, housing-on-release), each maxEffect × value.
func (p *PrisonAPI) reentrySupportLocked() float64 {
	total := 0.0
	for _, kind := range []ReentryKind{ReentryProbation, ReentryEmployment, ReentryHousing} {
		total += p.cfg.Reentry[string(kind)].MaxEffect * p.reentry[kind]
	}
	return total
}

// EducationEffect returns the education programme's overcrowding-degraded
// effect (AC-4/AC-7). It is one of the three independently-isolable regime
// accessors.
func (p *PrisonAPI) EducationEffect() float64 {
	if err := p.checkNotCopied("EducationEffect"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.regimeEffectLocked(RegimeEducation)
}

// WorkEffect returns the work-programme effect (AC-4/AC-7).
func (p *PrisonAPI) WorkEffect() float64 {
	if err := p.checkNotCopied("WorkEffect"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.regimeEffectLocked(RegimeWork)
}

// AddictionEffect returns the addiction-treatment effect (AC-4/AC-7).
func (p *PrisonAPI) AddictionEffect() float64 {
	if err := p.checkNotCopied("AddictionEffect"); err != nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.regimeEffectLocked(RegimeAddictionTreatment)
}

// baseRate returns the data-loaded base reoffending rate for an offence and
// age band (AC-5/GR#15). Youth carries its own (lower) base — the prevention
// synergy §43/§28 names, held as data rather than a code literal.
func (p *PrisonAPI) baseRate(o OffenceClass, youth bool) float64 {
	age := string(AgeAdult)
	if youth {
		age = string(AgeYouth)
	}
	return p.cfg.BaseRates[age][string(o)]
}

// ReoffendingRate computes the §43 reoffending formula for a citizen
// (AC-3/AC-5):
//
//	ReoffendingRate = Base(offence, age)
//	                + CategoryMismatchPenalty (when placed off-profile)
//	                − RegimeEffect(education + work + addiction)
//	                − ReentrySupport(probation + employment + housing)
//
// The three terms are independently sourced: Base from the data table,
// RegimeEffect from the three AC-4 accessors (overcrowding-degraded), and
// ReentrySupport from its three named sub-inputs. A category mismatch raises
// the rate (AC-3's counterintuitive direction) — never "stricter is safer".
func (p *PrisonAPI) ReoffendingRate(citizenID uint64) (float64, error) {
	if err := p.checkNotCopied("ReoffendingRate"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec, err := p.requireCohort(citizenID)
	if err != nil {
		return 0, err
	}
	base := p.baseRate(rec.Offence, rec.Youth)
	regime := p.regimeEffectLocked(RegimeEducation) +
		p.regimeEffectLocked(RegimeWork) +
		p.regimeEffectLocked(RegimeAddictionTreatment)
	reentry := p.reentrySupportLocked()

	mismatch := 0.0
	if rec.Category != matchedCategory(rec.Offence) {
		mismatch = p.cfg.CategoryMismatchPenalty
	}
	return clamp01(base + mismatch - regime - reentry), nil
}

// Reoffended draws the reoffend-or-not outcome for a citizen (AC-12): a
// deterministic counter-based draw hash(worldSeed, citizenID, month,
// "reoffend") compared against the citizen's reoffending rate — never a
// shared/global RNG source, never a wall-clock read (GR#21). The drawn
// outcome is persisted back onto the citizen's cohort record (US-2's
// "eventual reoffend-or-not" field), so [PrisonAPI.Cohort] reflects the same
// outcome a subsequent caller reads — the field is a real, queryable result,
// not a dead slot that is never written.
func (p *PrisonAPI) Reoffended(citizenID uint64) (bool, error) {
	if err := p.checkNotCopied("Reoffended"); err != nil {
		return false, err
	}
	rate, err := p.ReoffendingRate(citizenID)
	if err != nil {
		return false, err
	}
	p.mu.RLock()
	rec, ok := p.cohorts[citizenID]
	p.mu.RUnlock()
	if !ok {
		return false, errs.New(ErrUnknownCitizen, p.correlationID, map[string]any{"citizen": citizenID})
	}
	s := det.NewStream(p.seed, citizenID, rec.AdmittedMonth, "reoffend")
	outcome := s.Float64() < rate

	// Persist the drawn outcome onto the cohort record (US-2). The draw is a
	// pure function of immutable fields (seed, citizenID, AdmittedMonth) plus
	// the rate already read above, so a concurrent second draw persists the
	// same value — idempotent, never a torn or re-drawn result.
	p.mu.Lock()
	if cur, ok := p.cohorts[citizenID]; ok {
		cur.Reoffended = outcome
		p.cohorts[citizenID] = cur
	}
	p.mu.Unlock()

	return outcome, nil
}

// AdultPipelineCost returns the adult-pipeline cost per offender for an
// offence (AC-6) — the data-loaded adult cost. Youth is a distinct, cheaper
// pipeline (see [PrisonAPI.YouthPipelineCost]).
func (p *PrisonAPI) AdultPipelineCost(o OffenceClass) (int64, error) {
	if err := p.checkNotCopied("AdultPipelineCost"); err != nil {
		return 0, err
	}
	if !validOffence(o) {
		return 0, errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "offence", "value": string(o)})
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg.AdultCostPerOffender[string(o)], nil
}

// YouthPipelineCost returns the youth-pipeline cost per offender for an
// offence (AC-6): the adult cost scaled by the data-loaded youth cost
// multiplier — a distinct, cheaper pipeline with its own prevention synergy,
// not merely a cheaper cell.
func (p *PrisonAPI) YouthPipelineCost(o OffenceClass) (int64, error) {
	if err := p.checkNotCopied("YouthPipelineCost"); err != nil {
		return 0, err
	}
	if !validOffence(o) {
		return 0, errs.New(ErrInvalidAdmission, p.correlationID, map[string]any{"field": "offence", "value": string(o)})
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	adult := p.cfg.AdultCostPerOffender[string(o)]
	return int64(float64(adult) * p.cfg.Youth.CostMultiplier), nil
}

// RehabSpend applies a rehab-spend funding increase subject to this
// package's own Slow-Fuse pre-submission check (AC-9 interim, BUG-058
// block): the command must carry a FuseYears tag in the documented [5,15]
// window AND a non-nil, finite locally-computed projected-consequence value,
// otherwise it is rejected with ErrSlowFuseRejected — never silently applied.
// A supplied zero is a legitimate locally-computed consequence (distinguishable
// from the nil "not supplied" case) and is accepted. The real
// engine.projections submission is deferred until the edge is registered.
func (p *PrisonAPI) RehabSpend(req RehabSpendRequest) error {
	if err := p.checkNotCopied("RehabSpend"); err != nil {
		return err
	}
	switch req.Line {
	case RegimeEducation, RegimeWork, RegimeAddictionTreatment:
	default:
		return invalidRegimeFunding(p.correlationID, req.Line, 0)
	}
	if req.Increase <= 0 {
		return invalidRegimeFunding(p.correlationID, req.Line, req.Increase)
	}
	if req.FuseYears < p.cfg.FuseYears.Min || req.FuseYears > p.cfg.FuseYears.Max {
		return slowFuseRejected(p.correlationID, fmt.Sprintf("fuseYears %d outside [%d,%d]", req.FuseYears, p.cfg.FuseYears.Min, p.cfg.FuseYears.Max))
	}
	if req.ProjectedConsequence == nil || !num.IsFinite(*req.ProjectedConsequence) {
		return slowFuseRejected(p.correlationID, "missing local projected-consequence value")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.regime[req.Line] += req.Increase
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
