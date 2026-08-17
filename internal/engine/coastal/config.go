package coastal

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the GR#15 data-file contract: Config is the validated,
// ordered view of data/coastal.json, and LoadConfig is this package's
// self-contained loader (os.ReadFile + encoding/json + buildConfig — the
// engine.comms/engine.firms pattern). Every tunable number the coastal
// module consumes — arrival-frequency scaling, rescue service IDs, the
// caseworker-throughput ceiling, hotel/departure cost figures, the pipeline
// duration range, and the three policy-slider coefficients — comes from
// here, never a Go literal. Loading is all-or-nothing: any failure returns
// ErrDataInvalid and no config (GR#7).

// Config is the fully-validated, ordered view of data/coastal.json. It is
// immutable after construction and passed by value, so a caller cannot
// mutate a shared instance (GR#3).
type Config struct {
	// Frequency scaling (AC-3): the per-month arrival rate is
	// BaseArrivalRate × EraMultipliers[tier] × SeasonMultipliers[season] ×
	// (1 + WorldConditionsScale × conditions).
	BaseArrivalRate      float64
	MaxBoatSize          int64
	MaxArrivalsPerMonth  int64
	EraMultipliers       [14]float64 // index = §4 milestone tier 0..13
	SeasonMultipliers    [4]float64  // index = §27 season 0..3
	WorldConditionsScale float64

	Rescue       RescueConfig
	Reception    ReceptionConfig
	Pipeline     PipelineConfig
	Policy       PolicyConfig
	WorldProfile WorldProfileConfig
}

// RescueConfig carries the §30 coastguard/lifeboat service identities the
// rescue response reads capacity from (AC-4). The services themselves are
// registered by the world's service catalogue; coastal reads their capacity
// live via engine.services.
type RescueConfig struct {
	CoastguardServiceID string
	LifeboatServiceID   string
}

// ReceptionConfig is the §30 reception-and-processing capacity: the finite
// caseworker-throughput ceiling and the hotel-requisition overflow cost and
// satisfaction friction (AC-5).
type ReceptionConfig struct {
	CaseworkerThroughputPerMonth float64
	HotelCostPerCase             int64   // micro-pounds per overflow case per month (ASM-323)
	SatisfactionFrictionPerCase  float64 // satisfaction points lost per overflow case
}

// PipelineConfig is the §30 status-pipeline shape: the months-long duration
// range, the granted/not-granted verdict rate, the managed-departure cost,
// and how many months integration speed can shave off a duration.
type PipelineConfig struct {
	MinMonths            int64
	MaxMonths            int64
	GrantRate            float64 // fraction of cases granted (in [0,1])
	DepartureCostPerCase int64   // micro-pounds per not-granted case (ASM-323)
	MaxReductionMonths   int64   // integration-speed max duration reduction
}

// PolicyConfig is the three §30 policy sliders' default values and their
// per-unit effects (AC-11). Every slider moves at least two outcome metrics
// in opposite directions — the "real trade-offs, no right answer" property
// is data, not prose.
type PolicyConfig struct {
	ProcessingFundingDefault               float64
	ProcessingFundingThroughputGainPerUnit float64
	ProcessingFundingOpexPerUnitPerMonth   int64

	HousingApproachDefault                   float64
	HousingApproachCostPerUnitPerMonth       int64   // negative: centres are cheaper
	HousingApproachFrictionIncreasePerUnit   float64 // centres concentrate friction
	HousingApproachIntegrationPenaltyPerUnit float64 // centres integrate slower

	IntegrationInvestmentDefault             float64
	IntegrationInvestmentGainPerUnit         float64
	IntegrationInvestmentOpexPerUnitPerMonth int64
}

// WorldProfileConfig is the configurable world-profile skills distribution
// the granted citizen's attainment is drawn from (AC-6): a mean ± a bounded
// deterministic spread, never a hardcoded uniform default.
type WorldProfileConfig struct {
	AttainmentMean   int32
	AttainmentSpread int32
}

// numEraTiers is the fixed size of the §4 milestone ladder (0..13) — a
// schema fact, not a balance number.
const numEraTiers = 14

// numSeasons is the fixed number of §27 seasons — a schema fact.
const numSeasons = 4

// maxFrequencyCap is the documented sane ceiling for MaxBoatSize (people per
// boat) and MaxArrivalsPerMonth (boats per month) — a GR#15 data-placeholder
// bound, not a storage-derived one (int64 carries none). It is orders of
// magnitude above the shipped data (20 people/boat, 50 boats/month), so no
// legitimate future config is refused, while the MaxInt64 inputs that made
// Advance's totalSize accumulation wrap negative into a makeslice panic are
// rejected at the boundary (SEC-210). The per-month case allocation in Advance
// is bounded by MaxArrivalsPerMonth × MaxBoatSize ≤ maxFrequencyCap², so the
// total stays a finite, allocatable slice; satArrivalSize still saturates as
// defence in depth so the sum can never wrap even if a future config raises
// this cap.
const maxFrequencyCap int64 = 10_000

// maxCasesPerMonth is the documented ceiling on the per-month case mint —
// MaxArrivalsPerMonth × MaxBoatSize, the total number of case records a single
// Advance can allocate. maxFrequencyCap bounds each FACTOR at 10_000, but the
// product (up to maxFrequencyCap² = 10⁸) is what drives Advance's
// make([]Case, 0, totalSize) allocation: 10⁸ × 56-byte Case records is a
// ~5.6 GB single-call backing array — a validated-config OOM (SEC-220).
// Bounding the product, not just the factors, caps one Advance at ~1M cases
// (~56 MB), finite and allocatable, while remaining ~1000× the shipped data
// (50 boats/month × 20 people/boat = 1000 cases/month). GR#15 data-placeholder
// bound, not a storage-derived one (int64 carries none).
const maxCasesPerMonth int64 = 1_000_000

// maxCaseworkerThroughputPerMonth is the documented magnitude ceiling on the
// caseworker throughput (CaseworkerThroughputPerMonth, cases assignable per
// month). The field was validated only "finite > 0", so a config like 1e308
// passed Validate while throughput()'s product with the (also unbounded)
// ProcessingFundingThroughputGainPerUnit reached +Inf — and Advance's bare
// int64(T) conversion wrapped that to MinInt64 on amd64 (0 cases assigned,
// every case overflowed) but saturated to MaxInt64 on arm64 (every case
// assigned): opposite outcomes for the same config across GOOS (SEC-233).
// It is the same ceiling as maxCasesPerMonth — no config can meaningfully
// assign more caseworker-slots per month than the maximum number of cases
// that can be minted in a month — and ~1.7×10⁵ above the shipped 6, so no
// legitimate future config is refused. GR#15 data-placeholder bound.
const maxCaseworkerThroughputPerMonth = 1_000_000.0

// maxPolicyCoefficientPerUnit is the documented magnitude ceiling on the two
// §30 policy per-unit effect coefficients that reach unbounded arithmetic:
// ProcessingFundingThroughputGainPerUnit (scales the throughput ceiling) and
// HousingApproachFrictionIncreasePerUnit (scales the satisfaction friction).
// Each was validated only "finite >= 0", so a config like 1e308 passed
// Validate while (a) throughput()'s product overflowed to +Inf and Advance's
// bare int64(T) produced opposite outcomes across GOOS (SEC-233), and (b)
// effectiveFriction overflowed to +Inf, which satFriction collapsed to zero
// instead of the ceiling — a catastrophic month reported "no dissatisfaction"
// (SEC-234). The shipped magnitudes (0.8, 0.5) sit ~10⁶ below it, and the
// worst-case products it admits stay finite (throughput ≤ ~10¹², friction ≤
// ~10¹⁸). GR#15 data-placeholder bound.
const maxPolicyCoefficientPerUnit = 1_000_000.0

// maxSatisfactionFrictionPerCase is the documented magnitude ceiling on the
// per-overflow-case satisfaction friction (SatisfactionFrictionPerCase). The
// field is finite ≥ 0 like every coefficient, but a finite value large enough
// (e.g. 1e308) drives effectiveFriction — and the accumulated friction — to
// +Inf, leaking into SatisfactionFriction() and the ticker/UI (SEC-221). This
// ceiling rejects only the +Inf-driving magnitudes (~5×10⁷ × the shipped 0.02)
// while accepting every legitimate config. GR#15 data-placeholder bound.
const maxSatisfactionFrictionPerCase = 1_000_000.0

// maxSatisfactionFriction is the finite saturation ceiling for the cumulative
// friction accumulator (defence in depth, SEC-221). The per-case value is
// bounded by maxSatisfactionFrictionPerCase, but effectiveFriction also scales
// by the (unbounded) HousingApproachFrictionIncreasePerUnit coefficient, so a
// still-valid config can produce a non-finite per-month delta; the accumulator
// saturates here so SatisfactionFriction() can never return +Inf. Far beyond
// any legitimate run (~10¹³ months at the shipped 0.02/case × 1000 cases),
// never reached in practice. GR#15 data-placeholder bound.
const maxSatisfactionFriction = 1e18

// maxFrequencyMultiplier is the documented sane ceiling on the era/season
// frequency multipliers and WorldConditionsScale (AC-3). These scale
// BaseArrivalRate (already bounded at maxFrequencyCap) to the per-month
// arrival rate. Each was previously validated only "finite and >= 0", so a
// config like {EraMultipliers[0]=1e308, SeasonMultipliers[0]=1e308,
// WorldConditionsScale=1e308} passed Validate while rateForMonth's product
// overflowed to +Inf — and arrivalCount's non-finite branch (ordered before
// the ceiling) collapsed that +Inf to ZERO arrivals instead of the max
// (SEC-228). Bounding each factor at maxFrequencyCap keeps the worst-case
// product (10⁴ × 10⁴ × 10⁴ × ~10⁴ ≈ 1e16) finite, while the shipped
// magnitudes (≤ 1.8×) sit ~10⁴ below it. GR#15 data-placeholder bound, not a
// storage-derived one.
const maxFrequencyMultiplier = 10_000.0

// maxPipelineMonths is the documented sane ceiling on the pipeline duration
// range (MinMonths/MaxMonths, AC-6/AC-7). Each was previously validated only
// "> 0"/">= minMonths", so Config{MinMonths=1, MaxMonths=MaxInt64} passed
// Validate while durationFor returned up to MaxInt64 and ResolveMonth =
// month + dm wrapped negative — a wrapped-negative resolve month reads as
// "immediately due", so the due-check granted the case on the very next
// Advance (SEC-229). Bounding the range here keeps durationFor's base modest
// (the shipped 3..9 months is ~10³ below it), and Advance saturates the
// month + dm addition as defence in depth. GR#15 data-placeholder bound, not
// a storage-derived one.
const maxPipelineMonths int64 = 10_000

// fileCoastal is data/coastal.json's filename, relative to the resolved
// data directory (see foundation/data.ResolveDataDir).
const fileCoastal = "coastal.json"

// rawCoastalData is data/coastal.json's JSON wire shape, decoded only to be
// validated and folded into the ordered Config above.
type rawCoastalData struct {
	Version      int             `json:"version"`
	Frequency    rawFrequency    `json:"frequency"`
	Rescue       rawRescue       `json:"rescue"`
	Reception    rawReception    `json:"reception"`
	Pipeline     rawPipeline     `json:"pipeline"`
	Policy       rawPolicy       `json:"policy"`
	WorldProfile rawWorldProfile `json:"worldProfile"`
}

type rawFrequency struct {
	BasePerMonth         float64   `json:"basePerMonth"`
	MaxBoatSize          int64     `json:"maxBoatSize"`
	MaxArrivalsPerMonth  int64     `json:"maxArrivalsPerMonth"`
	EraMultipliers       []float64 `json:"eraMultipliers"`
	SeasonMultipliers    []float64 `json:"seasonMultipliers"`
	WorldConditionsScale float64   `json:"worldConditionsScale"`
}

type rawRescue struct {
	CoastguardServiceID string `json:"coastguardServiceID"`
	LifeboatServiceID   string `json:"lifeboatServiceID"`
}

type rawReception struct {
	CaseworkerThroughputPerMonth float64 `json:"caseworkerThroughputPerMonth"`
	HotelCostPerCase             int64   `json:"hotelCostPerCase"`
	SatisfactionFrictionPerCase  float64 `json:"satisfactionFrictionPerCase"`
}

type rawPipeline struct {
	MinMonths            int64   `json:"minMonths"`
	MaxMonths            int64   `json:"maxMonths"`
	GrantRate            float64 `json:"grantRate"`
	DepartureCostPerCase int64   `json:"departureCostPerCase"`
	MaxReductionMonths   int64   `json:"maxReductionMonths"`
}

type rawPolicy struct {
	ProcessingFundingDefault               float64 `json:"processingFundingDefault"`
	ProcessingFundingThroughputGainPerUnit float64 `json:"processingFundingThroughputGainPerUnit"`
	ProcessingFundingOpexPerUnitPerMonth   int64   `json:"processingFundingOpexPerUnitPerMonth"`

	HousingApproachDefault                   float64 `json:"housingApproachDefault"`
	HousingApproachCostPerUnitPerMonth       int64   `json:"housingApproachCostPerUnitPerMonth"`
	HousingApproachFrictionIncreasePerUnit   float64 `json:"housingApproachFrictionIncreasePerUnit"`
	HousingApproachIntegrationPenaltyPerUnit float64 `json:"housingApproachIntegrationPenaltyPerUnit"`

	IntegrationInvestmentDefault             float64 `json:"integrationInvestmentDefault"`
	IntegrationInvestmentGainPerUnit         float64 `json:"integrationInvestmentGainPerUnit"`
	IntegrationInvestmentOpexPerUnitPerMonth int64   `json:"integrationInvestmentOpexPerUnitPerMonth"`
}

type rawWorldProfile struct {
	Skills rawSkills `json:"skills"`
}

type rawSkills struct {
	AttainmentMean   int32 `json:"attainmentMean"`
	AttainmentSpread int32 `json:"attainmentSpread"`
}

// LoadConfig reads, decodes and validates data/coastal.json from path,
// returning the ordered config or ErrDataInvalid. Every failure is a
// registry-sourced *errs.E — never a panic, never a silent default.
func LoadConfig(path, correlationID string) (Config, error) {
	var zero Config
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawCoastalData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	return buildConfig(raw, path, correlationID)
}

func buildConfig(raw rawCoastalData, path, correlationID string) (Config, error) {
	fail := func(field, rule string) (Config, error) {
		return Config{}, errs.New(ErrDataInvalid, correlationID, map[string]any{
			"path":   path,
			"field":  field,
			"reason": rule,
		})
	}
	var c Config

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Frequency (AC-3).
	f := raw.Frequency
	if !num.IsFinite(f.BasePerMonth) || f.BasePerMonth < 0 || f.BasePerMonth > float64(maxFrequencyCap) {
		return fail("frequency.basePerMonth", "must be finite and in [0, maxFrequencyCap] — the documented frequency ceiling (SEC-220)")
	}
	if f.MaxBoatSize <= 0 || f.MaxBoatSize > maxFrequencyCap {
		return fail("frequency.maxBoatSize", "must be in (0, maxFrequencyCap] — the documented sane ceiling (SEC-210)")
	}
	if f.MaxArrivalsPerMonth <= 0 || f.MaxArrivalsPerMonth > maxFrequencyCap {
		return fail("frequency.maxArrivalsPerMonth", "must be in (0, maxFrequencyCap] — the documented sane ceiling (SEC-210)")
	}
	if f.MaxBoatSize*f.MaxArrivalsPerMonth > maxCasesPerMonth {
		return fail("frequency", "maxBoatSize × maxArrivalsPerMonth must be <= maxCasesPerMonth — the documented per-month case-mint ceiling (SEC-220)")
	}
	if len(f.EraMultipliers) != numEraTiers {
		return fail("frequency.eraMultipliers", "must declare exactly 14 multipliers (tier 0..13)")
	}
	for i, m := range f.EraMultipliers {
		if !num.IsFinite(m) || m < 0 || m > maxFrequencyMultiplier {
			return fail("frequency.eraMultipliers", "each multiplier must be finite and in [0, maxFrequencyMultiplier] — the documented frequency-multiplier ceiling (SEC-228)")
		}
		c.EraMultipliers[i] = m
	}
	if len(f.SeasonMultipliers) != numSeasons {
		return fail("frequency.seasonMultipliers", "must declare exactly 4 multipliers (season 0..3)")
	}
	for i, m := range f.SeasonMultipliers {
		if !num.IsFinite(m) || m < 0 || m > maxFrequencyMultiplier {
			return fail("frequency.seasonMultipliers", "each multiplier must be finite and in [0, maxFrequencyMultiplier] — the documented frequency-multiplier ceiling (SEC-228)")
		}
		c.SeasonMultipliers[i] = m
	}
	if !num.IsFinite(f.WorldConditionsScale) || f.WorldConditionsScale < 0 || f.WorldConditionsScale > maxFrequencyMultiplier {
		return fail("frequency.worldConditionsScale", "must be finite and in [0, maxFrequencyMultiplier] — the documented frequency-multiplier ceiling (SEC-228)")
	}
	c.BaseArrivalRate = f.BasePerMonth
	c.MaxBoatSize = f.MaxBoatSize
	c.MaxArrivalsPerMonth = f.MaxArrivalsPerMonth
	c.WorldConditionsScale = f.WorldConditionsScale

	// Rescue (AC-4): service IDs are identity — non-empty.
	if raw.Rescue.CoastguardServiceID == "" {
		return fail("rescue.coastguardServiceID", "required, must be a non-empty service ID")
	}
	if raw.Rescue.LifeboatServiceID == "" {
		return fail("rescue.lifeboatServiceID", "required, must be a non-empty service ID")
	}
	c.Rescue = RescueConfig(raw.Rescue)

	// Reception (AC-5).
	r := raw.Reception
	if !num.IsFinite(r.CaseworkerThroughputPerMonth) || r.CaseworkerThroughputPerMonth <= 0 || r.CaseworkerThroughputPerMonth > maxCaseworkerThroughputPerMonth {
		return fail("reception.caseworkerThroughputPerMonth", "must be finite and in (0, maxCaseworkerThroughputPerMonth] — the documented throughput ceiling (SEC-233)")
	}
	if r.HotelCostPerCase < 0 {
		return fail("reception.hotelCostPerCase", "must be >= 0")
	}
	if !num.IsFinite(r.SatisfactionFrictionPerCase) || r.SatisfactionFrictionPerCase < 0 || r.SatisfactionFrictionPerCase > maxSatisfactionFrictionPerCase {
		return fail("reception.satisfactionFrictionPerCase", "must be finite and in [0, maxSatisfactionFrictionPerCase] — the documented friction magnitude ceiling (SEC-221)")
	}
	c.Reception = ReceptionConfig(r)

	// Pipeline (AC-6/AC-7).
	p := raw.Pipeline
	if p.MinMonths <= 0 || p.MinMonths > maxPipelineMonths {
		return fail("pipeline.minMonths", "must be in (0, maxPipelineMonths] — the documented pipeline-duration ceiling (SEC-229)")
	}
	if p.MaxMonths < p.MinMonths || p.MaxMonths > maxPipelineMonths {
		return fail("pipeline.maxMonths", "must be in [minMonths, maxPipelineMonths] — the documented pipeline-duration ceiling (SEC-229)")
	}
	if !num.IsFinite(p.GrantRate) || p.GrantRate < 0 || p.GrantRate > 1 {
		return fail("pipeline.grantRate", "must be in [0,1]")
	}
	if p.DepartureCostPerCase < 0 {
		return fail("pipeline.departureCostPerCase", "must be >= 0")
	}
	if p.MaxReductionMonths < 0 {
		return fail("pipeline.maxReductionMonths", "must be >= 0")
	}
	c.Pipeline = PipelineConfig(p)

	// Policy (AC-11): every slider's default must be in [0,1]; gain/cost
	// figures must be finite and non-negative (except the housing-approach
	// cost adjustment, which is negative — centres are cheaper).
	pol := raw.Policy
	if !inUnit(pol.ProcessingFundingDefault) {
		return fail("policy.processingFundingDefault", "must be in [0,1]")
	}
	if !num.IsFinite(pol.ProcessingFundingThroughputGainPerUnit) || pol.ProcessingFundingThroughputGainPerUnit < 0 || pol.ProcessingFundingThroughputGainPerUnit > maxPolicyCoefficientPerUnit {
		return fail("policy.processingFundingThroughputGainPerUnit", "must be finite and in [0, maxPolicyCoefficientPerUnit] — the documented policy-coefficient ceiling (SEC-233)")
	}
	if pol.ProcessingFundingOpexPerUnitPerMonth < 0 {
		return fail("policy.processingFundingOpexPerUnitPerMonth", "must be >= 0")
	}
	if !inUnit(pol.HousingApproachDefault) {
		return fail("policy.housingApproachDefault", "must be in [0,1]")
	}
	if pol.HousingApproachCostPerUnitPerMonth > 0 {
		return fail("policy.housingApproachCostPerUnitPerMonth", "must be <= 0 (centres are cheaper — the cost coefficient is non-positive)")
	}
	if !num.IsFinite(pol.HousingApproachFrictionIncreasePerUnit) || pol.HousingApproachFrictionIncreasePerUnit < 0 || pol.HousingApproachFrictionIncreasePerUnit > maxPolicyCoefficientPerUnit {
		return fail("policy.housingApproachFrictionIncreasePerUnit", "must be finite and in [0, maxPolicyCoefficientPerUnit] — the documented policy-coefficient ceiling (SEC-234)")
	}
	if !num.IsFinite(pol.HousingApproachIntegrationPenaltyPerUnit) || pol.HousingApproachIntegrationPenaltyPerUnit < 0 {
		return fail("policy.housingApproachIntegrationPenaltyPerUnit", "must be finite and >= 0")
	}
	if !inUnit(pol.IntegrationInvestmentDefault) {
		return fail("policy.integrationInvestmentDefault", "must be in [0,1]")
	}
	if !num.IsFinite(pol.IntegrationInvestmentGainPerUnit) || pol.IntegrationInvestmentGainPerUnit < 0 {
		return fail("policy.integrationInvestmentGainPerUnit", "must be finite and >= 0")
	}
	if pol.IntegrationInvestmentOpexPerUnitPerMonth < 0 {
		return fail("policy.integrationInvestmentOpexPerUnitPerMonth", "must be >= 0")
	}
	c.Policy = PolicyConfig(pol)

	// World profile (AC-6): attainment mean/spread bounded to int16 (the
	// citizen cold store encodes attainment as int16 — GR#16).
	wp := raw.WorldProfile.Skills
	if wp.AttainmentMean < 0 || wp.AttainmentMean > 32767 {
		return fail("worldProfile.skills.attainmentMean", "must be in 0..32767 (int16 cold-store bound)")
	}
	if wp.AttainmentSpread < 0 || wp.AttainmentSpread > 32767 {
		return fail("worldProfile.skills.attainmentSpread", "must be in 0..32767 (int16 cold-store bound)")
	}
	c.WorldProfile = WorldProfileConfig(wp)

	return c, nil
}

// Validate checks an already-constructed Config (the New entry point, so a
// caller can pass a hand-built Config without touching the data file). It
// reuses the same rules as buildConfig by round-tripping through the raw
// shape's invariant checks; it is deliberately narrow — it only rejects a
// config whose fields are out-of-domain (GR#15/GR#16).
func (c Config) Validate() error {
	if !num.IsFinite(c.BaseArrivalRate) || c.BaseArrivalRate < 0 || c.BaseArrivalRate > float64(maxFrequencyCap) {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "baseArrivalRate", "reason": "must be finite and in [0, maxFrequencyCap] (SEC-220)"})
	}
	if c.MaxBoatSize <= 0 || c.MaxBoatSize > maxFrequencyCap || c.MaxArrivalsPerMonth <= 0 || c.MaxArrivalsPerMonth > maxFrequencyCap {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "frequency", "reason": "maxBoatSize and maxArrivalsPerMonth must be in (0, maxFrequencyCap]"})
	}
	if c.MaxBoatSize*c.MaxArrivalsPerMonth > maxCasesPerMonth {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "frequency", "reason": "maxBoatSize × maxArrivalsPerMonth must be <= maxCasesPerMonth (SEC-220)"})
	}
	for i, m := range c.EraMultipliers {
		if !num.IsFinite(m) || m < 0 || m > maxFrequencyMultiplier {
			return errs.New(ErrDataInvalid, "", map[string]any{"field": "eraMultipliers", "reason": "each multiplier must be finite and in [0, maxFrequencyMultiplier] (SEC-228)", "index": i})
		}
	}
	for i, m := range c.SeasonMultipliers {
		if !num.IsFinite(m) || m < 0 || m > maxFrequencyMultiplier {
			return errs.New(ErrDataInvalid, "", map[string]any{"field": "seasonMultipliers", "reason": "each multiplier must be finite and in [0, maxFrequencyMultiplier] (SEC-228)", "index": i})
		}
	}
	if !num.IsFinite(c.WorldConditionsScale) || c.WorldConditionsScale < 0 || c.WorldConditionsScale > maxFrequencyMultiplier {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "worldConditionsScale", "reason": "must be finite and in [0, maxFrequencyMultiplier] (SEC-228)"})
	}
	if c.Rescue.CoastguardServiceID == "" || c.Rescue.LifeboatServiceID == "" {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "rescue", "reason": "service IDs must be non-empty"})
	}
	if !num.IsFinite(c.Reception.CaseworkerThroughputPerMonth) || c.Reception.CaseworkerThroughputPerMonth <= 0 || c.Reception.CaseworkerThroughputPerMonth > maxCaseworkerThroughputPerMonth {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "reception.caseworkerThroughputPerMonth", "reason": "must be finite and in (0, maxCaseworkerThroughputPerMonth] (SEC-233)"})
	}
	if c.Reception.HotelCostPerCase < 0 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "reception.hotelCostPerCase", "reason": "must be >= 0"})
	}
	if !num.IsFinite(c.Reception.SatisfactionFrictionPerCase) || c.Reception.SatisfactionFrictionPerCase < 0 || c.Reception.SatisfactionFrictionPerCase > maxSatisfactionFrictionPerCase {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "reception.satisfactionFrictionPerCase", "reason": "must be finite and in [0, maxSatisfactionFrictionPerCase] (SEC-221)"})
	}
	if c.Pipeline.MinMonths <= 0 || c.Pipeline.MinMonths > maxPipelineMonths || c.Pipeline.MaxMonths < c.Pipeline.MinMonths || c.Pipeline.MaxMonths > maxPipelineMonths {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "pipeline", "reason": "minMonths must be in (0, maxPipelineMonths] and maxMonths in [minMonths, maxPipelineMonths] (SEC-229)"})
	}
	if !num.IsFinite(c.Pipeline.GrantRate) || c.Pipeline.GrantRate < 0 || c.Pipeline.GrantRate > 1 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "pipeline.grantRate", "reason": "must be in [0,1]"})
	}
	if c.Pipeline.DepartureCostPerCase < 0 || c.Pipeline.MaxReductionMonths < 0 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "pipeline", "reason": "departureCostPerCase and maxReductionMonths must be >= 0"})
	}
	for name, v := range map[string]float64{
		"processingFundingDefault":     c.Policy.ProcessingFundingDefault,
		"housingApproachDefault":       c.Policy.HousingApproachDefault,
		"integrationInvestmentDefault": c.Policy.IntegrationInvestmentDefault,
	} {
		if !inUnit(v) {
			return errs.New(ErrDataInvalid, "", map[string]any{"field": name, "reason": "must be in [0,1]"})
		}
	}
	if c.Policy.HousingApproachCostPerUnitPerMonth > 0 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "policy.housingApproachCostPerUnitPerMonth", "reason": "must be <= 0 (centres are cheaper)"})
	}
	if !num.IsFinite(c.Policy.ProcessingFundingThroughputGainPerUnit) || c.Policy.ProcessingFundingThroughputGainPerUnit < 0 ||
		c.Policy.ProcessingFundingThroughputGainPerUnit > maxPolicyCoefficientPerUnit {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "policy.processingFundingThroughputGainPerUnit", "reason": "must be finite and in [0, maxPolicyCoefficientPerUnit] (SEC-233)"})
	}
	if !num.IsFinite(c.Policy.HousingApproachFrictionIncreasePerUnit) || c.Policy.HousingApproachFrictionIncreasePerUnit < 0 ||
		c.Policy.HousingApproachFrictionIncreasePerUnit > maxPolicyCoefficientPerUnit {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "policy.housingApproachFrictionIncreasePerUnit", "reason": "must be finite and in [0, maxPolicyCoefficientPerUnit] (SEC-234)"})
	}
	if c.Policy.ProcessingFundingOpexPerUnitPerMonth < 0 ||
		!num.IsFinite(c.Policy.HousingApproachIntegrationPenaltyPerUnit) || c.Policy.HousingApproachIntegrationPenaltyPerUnit < 0 ||
		!num.IsFinite(c.Policy.IntegrationInvestmentGainPerUnit) || c.Policy.IntegrationInvestmentGainPerUnit < 0 ||
		c.Policy.IntegrationInvestmentOpexPerUnitPerMonth < 0 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "policy", "reason": "gain/cost figures must be finite and non-negative"})
	}
	if c.WorldProfile.AttainmentMean < 0 || c.WorldProfile.AttainmentMean > 32767 ||
		c.WorldProfile.AttainmentSpread < 0 || c.WorldProfile.AttainmentSpread > 32767 {
		return errs.New(ErrDataInvalid, "", map[string]any{"field": "worldProfile.skills", "reason": "attainment mean/spread must be in 0..32767"})
	}
	return nil
}

// inUnit reports whether v is a finite value in [0,1].
func inUnit(v float64) bool {
	return num.IsFinite(v) && v >= 0 && v <= 1
}
