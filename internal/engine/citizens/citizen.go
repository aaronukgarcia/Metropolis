package citizens

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Personality axis indices into the eight-axis vector (§5.1). The axes are
// fixed and ordered: sociability, ambition, conscientiousness,
// novelty-seeking, physicality, community-mindedness, patience, aesthetic
// drive. Indexing by named constant (never iterating a map) keeps every
// access deterministic.
const (
	AxisSociability   = 0
	AxisAmbition      = 1
	AxisConscientious = 2
	AxisNovelty       = 3
	AxisPhysicality   = 4
	AxisCommunity     = 5
	AxisPatience      = 6
	AxisAesthetic     = 7

	// NumPersonalityAxes is the fixed number of axes. A schema constant.
	NumPersonalityAxes = 8
)

// Personality is the eight-axis personality vector, 0-100 each (§5.1). In
// the hot AoS record the axes are full-width int32 (hot citizens are few
// and richly accessed); the cold SoA store packs the same axes to int8.
type Personality [NumPersonalityAxes]int32

// LeisureWeight indices into the eight venue-category taste weights
// (§5.1: sport, arts, nightlife, nature, community, gaming, dining, home).
const (
	LeisureSport     = 0
	LeisureArts      = 1
	LeisureNightlife = 2
	LeisureNature    = 3
	LeisureCommunity = 4
	LeisureGaming    = 5
	LeisureDining    = 6
	LeisureHome      = 7

	// NumLeisureWeights is the fixed number of venue-category weights.
	NumLeisureWeights = 8
)

// LeisureWeights is the derived distribution over venue categories. It is
// derived (deterministically) from P × education × age (§5.1) — see
// DeriveLeisureWeights.
type LeisureWeights [NumLeisureWeights]int32

// Satisfaction component indices (§5.1: housing, services, environment,
// leisure-fit, commute).
const (
	SatHousing     = 0
	SatServices    = 1
	SatEnvironment = 2
	SatLeisureFit  = 3
	SatCommute     = 4

	// NumSatisfactionComponents is the fixed number of components.
	NumSatisfactionComponents = 5
)

// Satisfaction is the five satisfaction components, each 0-100.
type Satisfaction [NumSatisfactionComponents]int32

// StageEntry is one entry in the education stage history (§5.1). EndMonth
// is -1 while the stage is ongoing.
type StageEntry struct {
	Stage      Stage
	StartMonth int32
	EndMonth   int32
}

// Education is the education record: stage history plus a quality-weighted
// attainment score and total schooling months (§5.1).
type Education struct {
	Stages          []StageEntry
	Attainment      int32 // quality-weighted attainment score
	SchoolingMonths int32 // total months of schooling received
}

// Employment is the employment state + sector pair.
type Employment struct {
	State  EmploymentState
	Sector Sector
}

// Citizen is the hot AoS record (§5.1) — the ~250B per-citizen record.
// Age is NEVER stored as a field: it is derived from BirthMonth + the
// current sim month (AC-2, so "no shared birthdays, no culls: stored to
// the month" is enforced structurally by having no Age field to desync).
// The fields are exported so `go doc Citizen` lists them (AC-1); consumers
// read them and mutate citizens ONLY through CitizensAPI's command surface
// (AC-1b), never by direct field assignment.
type Citizen struct {
	ID           uint64
	BirthMonth   int32 // absolute month index, 0 = world genesis; may be negative (born before genesis — BUG-517)
	Sex          Sex
	Household    uint64 // household id
	Partner      uint64 // partner citizen id, 0 = none
	Children     []uint64
	Home         CellRef
	Workplace    uint64 // workplace ref, 0 = none
	School       uint64 // school ref, 0 = none
	Personality  Personality
	Education    Education
	Leisure      LeisureWeights
	HealthBand   HealthBand
	Wealth       int64 // micro-pounds
	Employment   Employment
	Satisfaction Satisfaction
	Fidelity     Fidelity // HOT or WARM (COLD citizens live in the cold store)

	// Month is the absolute sim month this hot record is current at (set
	// when the record is reconstructed or elevated). It is NOT an age —
	// age is always derived as Month - BirthMonth (AC-2), never a stored
	// field to desync.
	Month int64
}

// Age returns the citizen's age in months, derived from BirthMonth + the
// record's current sim month (Month). Age is always derived, never stored
// (AC-2): there is no Age field, so "no shared birthdays, no culls: stored
// to the month" holds structurally.
func (c Citizen) Age() int64 {
	return c.Month - int64(c.BirthMonth)
}

// NewCitizen constructs a validated hot citizen record. correlationID is
// attached to every validation error (GR#1). householdExists reports
// whether a household id is known — used to reject a record referencing a
// nonexistent household (AC-13) rather than silently orphan it. The
// returned Citizen is never a silently-clamped or corrupted record: an
// invalid input returns a registry-sourced error and no record (AC-13).
func NewCitizen(c Citizen, householdExists func(uint64) bool, correlationID string) (Citizen, error) {
	if err := ValidateCitizen(c, householdExists, correlationID); err != nil {
		return Citizen{}, err
	}
	return c, nil
}

// ValidateCitizen checks every field the cold store narrows at the
// hot→cold boundary (AC-13/GR#16): a negative or over-range BirthMonth, a
// personality axis outside 0-100, a Household id referencing a nonexistent
// household, an attainment or schooling score outside the int16 range, a
// satisfaction component outside 0-100, an out-of-domain enum (sex, health
// band, stage, employment state/sector), and more than 255 children. It
// never mutates the input and never clamps — out-of-contract values are
// rejected with a registry-sourced error, never silently narrowed.
func ValidateCitizen(c Citizen, householdExists func(uint64) bool, correlationID string) error {
	if int64(c.BirthMonth) < math.MinInt16 || int64(c.BirthMonth) > math.MaxInt16 {
		return errs.New(ErrInvalidBirthMonth, correlationID, map[string]any{
			"id":         c.ID,
			"birthMonth": c.BirthMonth,
		})
	}
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		v := c.Personality[axis]
		if v < 0 || v > MaxPersonalityAxis {
			return errs.New(ErrPersonalityAxisOutOfRange, correlationID, map[string]any{
				"id":    c.ID,
				"axis":  axis,
				"value": v,
			})
		}
	}
	if c.Household != 0 && householdExists != nil && !householdExists(c.Household) {
		return errs.New(ErrUnknownHousehold, correlationID, map[string]any{
			"id":        c.ID,
			"household": c.Household,
		})
	}
	// The cold store encodes attainment as int16; a value outside that
	// range would wrap when narrowed (int16(40000) == -25536), silently
	// corrupting the record. Reject it outright (AC-13/GR#16) rather than
	// trust the type.
	if c.Education.Attainment < math.MinInt16 || c.Education.Attainment > math.MaxInt16 {
		return errs.New(ErrAttainmentOutOfRange, correlationID, map[string]any{
			"id":         c.ID,
			"attainment": c.Education.Attainment,
		})
	}
	if err := validateFieldRange(c.ID, "schoolingMonths", int64(c.Education.SchoolingMonths), math.MinInt16, math.MaxInt16, correlationID); err != nil {
		return err
	}
	for i := 0; i < NumSatisfactionComponents; i++ {
		if err := validateFieldRange(c.ID, "satisfaction", int64(c.Satisfaction[i]), 0, 100, correlationID); err != nil {
			return err
		}
	}
	if err := validateEnums(c.ID, c.Sex, c.HealthBand, currentStage(c.Education), c.Employment.State, c.Employment.Sector, correlationID); err != nil {
		return err
	}
	if len(c.Children) > 255 {
		return fieldOutOfRange(c.ID, "childCount", int64(len(c.Children)), 0, 255, correlationID)
	}
	return nil
}

// fieldOutOfRange builds the generic out-of-range registry error (GR#7),
// naming the field and its documented [min, max] contract.
func fieldOutOfRange(id uint64, field string, value, min, max int64, correlationID string) error {
	return errs.New(ErrFieldOutOfRange, correlationID, map[string]any{
		"id":    id,
		"field": field,
		"value": value,
		"min":   min,
		"max":   max,
	})
}

// validateFieldRange returns fieldOutOfRange when value is outside [min, max].
func validateFieldRange(id uint64, field string, value, min, max int64, correlationID string) error {
	if value < min || value > max {
		return fieldOutOfRange(id, field, value, min, max, correlationID)
	}
	return nil
}

// Per-field enum validators are the SINGLE source of each enum's domain:
// every write path into a citizen record (birth, bulk-seed, command,
// hot→cold, cold→hot) validates through these, so no path can drift into
// its own duplicated range check (the round-2/round-3 asymmetry exactly).
// An out-of-domain enum is a data error, not a clampable number.

func validateSex(id uint64, v Sex, correlationID string) error {
	return validateFieldRange(id, "sex", int64(v), 0, 1, correlationID)
}

func validateHealthBand(id uint64, v HealthBand, correlationID string) error {
	return validateFieldRange(id, "healthBand", int64(v), 0, int64(MaxHealthBand), correlationID)
}

func validateStage(id uint64, v Stage, correlationID string) error {
	return validateFieldRange(id, "stage", int64(v), 0, int64(StageAdultEd), correlationID)
}

func validateEmploymentState(id uint64, v EmploymentState, correlationID string) error {
	// Domain widened to include EmploymentOffMap (5) — FEAT-198, ICD
	// docs/planning/icd/engine.citizens-offmap.md §8: still MET-G007 via
	// the same generic fieldOutOfRange path, only the upper bound moved.
	return validateFieldRange(id, "employmentState", int64(v), 0, int64(EmploymentOffMap), correlationID)
}

func validateSector(id uint64, v Sector, correlationID string) error {
	return validateFieldRange(id, "sector", int64(v), 0, int64(SectorPublic), correlationID)
}

// validateEnums checks all five closed enums (the birth/bulk-seed entry
// points, which hold a full record, call this once; the command entry
// point calls the individual validators above for just the fields it
// mutates). Both routes go through the same per-field validators.
func validateEnums(id uint64, sex Sex, health HealthBand, stage Stage, state EmploymentState, sector Sector, correlationID string) error {
	if err := validateSex(id, sex, correlationID); err != nil {
		return err
	}
	if err := validateHealthBand(id, health, correlationID); err != nil {
		return err
	}
	if err := validateStage(id, stage, correlationID); err != nil {
		return err
	}
	if err := validateEmploymentState(id, state, correlationID); err != nil {
		return err
	}
	return validateSector(id, sector, correlationID)
}

// ValidateColdRecord checks a bulk-seed ColdRecord against the same
// hot→cold boundary contract as ValidateCitizen (AC-13/GR#16): the cold
// store is the single source of truth, so a seed record that would narrow
// to a wrapped or out-of-domain value is rejected rather than appended.
func ValidateColdRecord(r ColdRecord, correlationID string) error {
	if r.BirthMonth < math.MinInt16 || r.BirthMonth > math.MaxInt16 {
		return errs.New(ErrInvalidBirthMonth, correlationID, map[string]any{
			"id":         r.ID,
			"birthMonth": r.BirthMonth,
		})
	}
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		v := r.Personality[axis]
		if v < 0 || v > MaxPersonalityAxis {
			return errs.New(ErrPersonalityAxisOutOfRange, correlationID, map[string]any{
				"id":    r.ID,
				"axis":  axis,
				"value": v,
			})
		}
	}
	for _, v := range []int32{r.SatHousing, r.SatServices, r.SatEnvironment, r.SatLeisureFit, r.SatCommute} {
		if err := validateFieldRange(r.ID, "satisfaction", int64(v), 0, 100, correlationID); err != nil {
			return err
		}
	}
	return validateEnums(r.ID, r.Sex, r.HealthBand, r.Stage, r.EmploymentState, r.Sector, correlationID)
}

// safeInt16 narrows an int32 into the int16 range with clamping (GR#16:
// never trust a stored field's declared type — coerce via a safe helper,
// never a bare type conversion that wraps). Used by hotToColdRecord for
// the fields the cold store encodes as int16.
func safeInt16(v int32) int16 {
	if v < math.MinInt16 {
		return math.MinInt16
	}
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(v)
}

// safeUint8 narrows an int into the uint8 range with clamping (GR#16).
func safeUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// safeUint32 narrows a uint64 reference id (home cell, workplace, school)
// into the uint32 cold column with clamping (GR#16: never a bare
// uint32(...) that wraps). 2^32 ids is far beyond this project's 100M-citizen
// ceiling, so the clamp is defense-in-depth only — the documented cold-store
// representation is uint32. Household/partner ids are NO LONGER narrowed
// through this helper (births-unblock lane, 2026-09-02): engine.attract's
// migrant ids (1<<62) and this package's own fertility-child ids (1<<63)
// both live far outside uint32's range, and safeUint32 was silently
// saturating every cross-cohort partner/household reference to
// math.MaxUint32 — permanently zeroing Citizen.Partner for those couples and
// making births structurally impossible outside the closed seed cohort. See
// ColdShard's doc comment (coldshard.go) for the full finding.
func safeUint32(v uint64) uint32 {
	const max = uint64(^uint32(0)) // math.MaxUint32
	if v > max {
		return uint32(max)
	}
	return uint32(v)
}

// safeSat narrows a satisfaction component (int32) into the int8 cold
// column, clamping to the documented [0, 100] contract (GR#16: never a
// bare int8(...) conversion that wraps — int8(200) == -56).
func safeSat(v int32) int8 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int8(v)
}

// safeInt16FromInt64 narrows an int64 month delta into the int16 cold
// column with clamping (GR#16: never a bare int16(...) that wraps —
// int16(40000) == -25536).
func safeInt16FromInt64(v int64) int16 {
	if v < math.MinInt16 {
		return math.MinInt16
	}
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(v)
}

// InitPersonality initialises a personality vector at birth from a
// parental blend plus deterministic noise, via the counter-based hash
// stream (AC-3, AC-15). parentA/parentB are the two parents' vectors (a
// citizen with only one recorded parent passes the same vector twice);
// each axis is the mid-parent blend clamped to [0, MaxPersonalityAxis],
// then perturbed by a deterministic draw from hash(worldSeed, id, month,
// "personality"). The draw never leaves the 0-100 range because it is
// applied as a bounded delta around the blended value.
func InitPersonality(seed uint64, id uint64, month int64, parentA, parentB Personality) Personality {
	var p Personality
	stream := det.NewStream(seed, id, month, "personality")
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		blend := (parentA[axis] + parentB[axis]) / 2
		// Bounded deterministic noise in [-6, +6] around the blend, drawn
		// from the same stream at the axis's own position (position-
		// independent and order-free).
		delta := int32(stream.IntN(13)) - 6
		v := blend + delta
		if v < 0 {
			v = 0
		} else if v > MaxPersonalityAxis {
			v = MaxPersonalityAxis
		}
		p[axis] = v
	}
	return p
}

// ApplyEducationEffect drifts a personality vector per schooling quality
// (§5.1: good schooling widens ambition/novelty-seeking/taste range; poor
// schooling narrows them) — the mechanism by which education modifies P
// over time rather than P being fixed at birth (AC-3). quality is the
// quality-weighted attainment score (positive = good schooling); the drift
// magnitude is bounded so axes stay in [0, MaxPersonalityAxis].
func ApplyEducationEffect(p Personality, quality int32) Personality {
	out := p
	// Drift sign/scale from quality: positive quality raises ambition and
	// novelty-seeking, negative lowers them; the magnitude is a
	// deterministic, bounded function of the score.
	shift := quality / 20 // deterministic, integer-only
	for _, axis := range []int{AxisAmbition, AxisNovelty} {
		v := out[axis] + shift
		if v < 0 {
			v = 0
		} else if v > MaxPersonalityAxis {
			v = MaxPersonalityAxis
		}
		out[axis] = v
	}
	return out
}

// DeriveLeisureWeights derives the leisure taste weights from P ×
// education × age (§5.1: "derived (deterministically)"). It is a pure
// function — the same inputs always produce the same weights — so the cold
// store never needs to persist it (a compression win: it is reconstructed
// on life-write/inspection rather than stored).
func DeriveLeisureWeights(p Personality, attainment int32, ageMonths int64) LeisureWeights {
	var w LeisureWeights
	// Map each axis to a venue category by a fixed, documented weighting:
	// sociability → dining/nightlife, physicality → sport, community →
	// community, aesthetic → arts, novelty → gaming/nightlife, and age +
	// attainment broaden the range.
	w[LeisureSport] = p[AxisPhysicality]
	w[LeisureArts] = p[AxisAesthetic]
	w[LeisureNightlife] = (p[AxisSociability] + p[AxisNovelty]) / 2
	w[LeisureNature] = (MaxPersonalityAxis - p[AxisNovelty] + p[AxisCommunity]) / 2
	w[LeisureCommunity] = p[AxisCommunity]
	w[LeisureGaming] = p[AxisNovelty]
	w[LeisureDining] = p[AxisSociability]
	w[LeisureHome] = (MaxPersonalityAxis - p[AxisSociability] + p[AxisPatience]) / 2

	// Age widens/narrows the spread deterministically; attainment raises
	// the arts/nature weights (quality schooling widens taste range).
	spread := ageMonths / 12 / 10 // older ⇒ broader spread
	if spread < 0 {
		spread = 0
	}
	for i := 0; i < NumLeisureWeights; i++ {
		v := w[i] + int32(spread) + attainment/50
		if v < 0 {
			v = 0
		} else if v > MaxPersonalityAxis {
			v = MaxPersonalityAxis
		}
		w[i] = v
	}
	return w
}
