package census

import (
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// EducationBio is the census's assembled education facet of a citizen bio
// (AC-9/AC-10): the full stage trajectory from kindergarten to specialist
// university, the quality-weighted attainment, and — at the university
// tier — the specialist-university-to-industry tie, sourced from
// EducationSource and never re-authored by the census (ES-2).
type EducationBio struct {
	Attainment  int64
	Schooling   int64
	Stages      []StageView
	IndustryTie string
}

// EmploymentBio is the census's assembled job/employment facet of a
// citizen bio (sourced from CitizensSource).
type EmploymentBio struct {
	State     EmploymentState
	Sector    Sector
	Workplace uint64
}

// FamilyBio is the census's assembled family/home facet (sourced from
// CitizensSource).
type FamilyBio struct {
	Household uint64
	Partner   uint64
	Home      uint64
}

// RetirementBio is the census's assembled retirement facet: the retirement
// month derived from the citizen's birth month + the data-file retirement
// age (ASM-646), never a census-local re-derivation of mortality.
type RetirementBio struct {
	RetirementMonth int64
}

// CitizenBio is the census's cradle-to-grave life-history for one citizen
// (AC-9): an assembled view over the owning modules' live interfaces, never
// a census-local copy of a citizen record (GR#3).
type CitizenBio struct {
	GUID       GUID
	ID         uint64
	BirthMonth int64
	Sex        Sex
	Education  EducationBio
	Employment EmploymentBio
	Family     FamilyBio
	Retirement RetirementBio
	Income     int64 // micro-pounds, sourced from FinanceSource
}

// LifeEvent is one census-observed milestone in a non-citizen object's
// life-history (AC-11): e.g. a car's mileage, a house's construction, a
// chopper's air-unit events.
type LifeEvent struct {
	Tick        int64
	Description string
}

// ObjectBio is the census's generic tracked-object bio (AC-11): a citizen
// and a car both satisfy this contract. A citizen's richer cradle-to-grave
// detail lives in [CitizenBio]; the generic surface carries the object's
// GUID, kind, documented life span, and its recorded life-history.
type ObjectBio struct {
	GUID        GUID
	Kind        ObjectKind
	LifeSpan    LifeSpan
	LifeHistory []LifeEvent
}

// parseGUID splits a census GUID into its kind and owning id. It returns
// ok=false for a GUID that does not carry a recognised prefix + numeric id
// (a caller-supplied malformed GUID is ErrUnknownObject, never a panic).
func parseGUID(g GUID) (ObjectKind, uint64, bool) {
	prefixes := []struct {
		prefix string
		kind   ObjectKind
	}{
		{"citizen:", ObjectCitizen},
		{"car:", ObjectCar},
		{"house:", ObjectHouse},
		{"chopper:", ObjectChopper},
	}
	s := string(g)
	for _, p := range prefixes {
		rest, ok := strings.CutPrefix(s, p.prefix)
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		return p.kind, id, true
	}
	return 0, 0, false
}

// CitizenBio assembles a citizen's cradle-to-grave bio by reading the
// owning modules' query surfaces live: identity/employment/family/home
// (CitizensSource), education (EducationSource), income (FinanceSource),
// and the retirement month (birth month + data-file retirement age). The
// result is a deterministic function of (GUID, tick, data) — the same bio
// assembled twice at the same tick is byte-identical (AC-13).
func (c *CensusAPI) CitizenBio(guid GUID, tick int64, correlationID string) (CitizenBio, error) {
	if err := c.checkNotCopied("CitizenBio"); err != nil {
		return CitizenBio{}, err
	}
	kind, id, ok := parseGUID(guid)
	if !ok || kind != ObjectCitizen {
		return CitizenBio{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	if err := c.requireSources("CitizenBio"); err != nil {
		return CitizenBio{}, err
	}
	citizensSrc, educationSrc, _, _, _, _, financeSrc := c.snapshotSources()

	cv, ok := citizensSrc.CitizenFor(id, correlationID)
	if !ok {
		return CitizenBio{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}

	// The bio's GUID is the canonical serialisation (unpadded id), so a
	// padded spelling like "citizen:007" resolves to the same identity the
	// tracked set carries (SEC-152).
	bio := CitizenBio{
		GUID:       citizenGUID(id),
		ID:         cv.ID,
		BirthMonth: cv.BirthMonth,
		Sex:        cv.Sex,
		Employment: EmploymentBio{State: cv.Employment, Sector: cv.Sector, Workplace: cv.Workplace},
		Family:     FamilyBio{Household: cv.Household, Partner: cv.Partner, Home: cv.Home},
	}

	if ev, ok := educationSrc.EducationFor(id, correlationID); ok {
		bio.Education = EducationBio{
			Attainment:  ev.Attainment,
			Schooling:   ev.Schooling,
			Stages:      cloneStages(ev.Stages),
			IndustryTie: ev.IndustryTie,
		}
	}
	if inc, ok := financeSrc.IncomeFor(id, correlationID); ok {
		bio.Income = inc
	}
	retire, err := c.cfg.RetirementMonths(cv.BirthMonth, correlationID)
	if err != nil {
		return CitizenBio{}, err
	}
	bio.Retirement = RetirementBio{RetirementMonth: retire}

	return bio, nil
}

// ObjectBio returns the generic tracked-object bio for a non-citizen object
// (car, house, chopper) — its GUID, kind, documented life span, and its
// recorded life-history (AC-11). For a citizen, use [CitizenBio].
func (c *CensusAPI) ObjectBio(guid GUID, correlationID string) (ObjectBio, error) {
	if err := c.checkNotCopied("ObjectBio"); err != nil {
		return ObjectBio{}, err
	}
	key, ok := canonicalGUID(guid)
	if !ok {
		return ObjectBio{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.tracked[key]
	if !ok {
		return ObjectBio{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	return ObjectBio{
		GUID:        rec.guid,
		Kind:        rec.kind,
		LifeSpan:    rec.lifeSpan,
		LifeHistory: append([]LifeEvent(nil), rec.history...),
	}, nil
}

// RecordLifeEvent appends a census-observed milestone to a tracked object's
// life-history (AC-11's car/house/chopper life-history). It is a census-own
// bookkeeping call — it never touches a consumed module's state.
func (c *CensusAPI) RecordLifeEvent(guid GUID, tick int64, description string) error {
	if err := c.checkNotCopied("RecordLifeEvent"); err != nil {
		return err
	}
	key, ok := canonicalGUID(guid)
	if !ok {
		return errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.tracked[key]
	if !ok {
		return errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	rec.history = append(rec.history, LifeEvent{Tick: tick, Description: description})
	return nil
}
