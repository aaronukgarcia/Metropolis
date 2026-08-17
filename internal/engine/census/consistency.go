package census

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Facet is one tracked aspect of a modelled object (US-4). For a citizen
// the facets are job, family, home, workplace, retirement and income (AC-5);
// non-citizen objects carry a single presence facet, since their owning
// modules are not among the seven consumers (ES-3).
type Facet string

const (
	FacetJob        Facet = "job"
	FacetFamily     Facet = "family"
	FacetHome       Facet = "home"
	FacetWorkplace  Facet = "workplace"
	FacetRetirement Facet = "retirement"
	FacetIncome     Facet = "income"
	FacetPresence   Facet = "presence"
)

// CheckInRecord is the consistency checker's per-object liveness record
// (AC-5): the object's GUID, its per-facet last-seen timestamps, and the
// derived least-check-in — the oldest (least recent) of those timestamps,
// the "least" reading (ASM-645). A facet absent from the map has never
// been observed and does not contribute to the least-check-in.
type CheckInRecord struct {
	GUID         GUID
	Kind         ObjectKind
	LifeSpan     LifeSpan
	Facets       map[Facet]int64
	LeastCheckIn int64
}

// LostObject is the CC's lost-object finding (AC-6): an object whose least
// check-in lags the current tick beyond the data-defined threshold. The
// object is never silently dropped from the tracked set — it is flagged.
type LostObject struct {
	GUID         GUID
	Kind         ObjectKind
	LeastCheckIn int64
	Lag          int64
	Threshold    int64
}

// checkInRecord is the CC's internal tracked-object record.
type checkInRecord struct {
	guid     GUID
	kind     ObjectKind
	lifeSpan LifeSpan
	facets   map[Facet]int64
	history  []LifeEvent
}

// leastCheckIn returns the oldest per-facet last-seen timestamp, or -1 when
// no facet has ever been observed. The minimum across facets is the entire
// point of the "least" reading (AC-5): a single stale facet must surface,
// even while every other facet still refreshes.
func (r *checkInRecord) leastCheckIn() int64 {
	if len(r.facets) == 0 {
		return -1
	}
	min := int64(0)
	first := true
	for _, t := range r.facets {
		if first || t < min {
			min = t
			first = false
		}
	}
	return min
}

// checkLocked runs the consistency checker thread over the snapshot: it
// refreshes the facets it observed this tick, then flags any tracked object
// whose least check-in lags the current tick beyond the configured
// threshold (AC-5/AC-6). Caller holds c.mu.
func (c *CensusAPI) checkLocked(snap *Snapshot) {
	lagTicks, err := c.cfg.CheckInLagTicks(c.correlationID)
	if err != nil {
		lagTicks = 1 // unreachable past Load validation; fail closed to 1 tick
	}

	seen := make(map[GUID]bool, len(snap.Citizens))
	for _, cv := range snap.Citizens {
		guid := citizenGUID(cv.ID)
		seen[guid] = true
		rec := c.tracked[guid]
		if rec == nil {
			rec = &checkInRecord{
				guid:     guid,
				kind:     ObjectCitizen,
				lifeSpan: LifeSpanWholeGame,
				facets:   make(map[Facet]int64),
			}
			c.tracked[guid] = rec
		}
		// The always-observed citizen facets refresh every tick the citizen
		// is present; the income facet refreshes only when the finance source
		// tracks that citizen, so a citizen whose income tracking stops is
		// caught by the minimum-derived least-check-in.
		rec.facets[FacetJob] = snap.Tick
		rec.facets[FacetFamily] = snap.Tick
		rec.facets[FacetHome] = snap.Tick
		rec.facets[FacetWorkplace] = snap.Tick
		rec.facets[FacetRetirement] = snap.Tick
		if _, ok := snap.Income[cv.ID]; ok {
			rec.facets[FacetIncome] = snap.Tick
		}
	}

	// An object absent from the snapshot (removed from its owning module)
	// refreshes nothing this tick, so its least check-in falls behind — and
	// once it lags past the threshold it is flagged, never silently dropped
	// from the tracked set (AC-6). Iteration is over sorted GUIDs (GR#21).
	c.lost = c.lost[:0]
	for _, guid := range sortedGUIDs(c.tracked) {
		rec := c.tracked[guid]
		least := rec.leastCheckIn()
		if least < 0 {
			continue // never observed (e.g. a registered non-citizen with no wired source)
		}
		lag := num.SatSub(snap.Tick, least)
		if lag > lagTicks {
			c.lost = append(c.lost, LostObject{
				GUID:         guid,
				Kind:         rec.kind,
				LeastCheckIn: least,
				Lag:          lag,
				Threshold:    lagTicks,
			})
		}
	}
}

// TrackObject registers a non-citizen object (car, house, chopper) in the
// census's tracked set under its GUID (AC-11). It is idempotent per GUID.
// This is a census-own bookkeeping call — it never touches a consumed
// module's state.
func (c *CensusAPI) TrackObject(guid GUID, kind ObjectKind, lifeSpan LifeSpan) error {
	if err := c.checkNotCopied("TrackObject"); err != nil {
		return err
	}
	if err := validateObjectIdentity(guid, kind, lifeSpan, c.correlationID); err != nil {
		return err
	}
	// Store under the canonical serialisation (kind name + unpadded id), not
	// the caller's raw spelling, so "car:007" and "car:7" are one record and
	// one identity (SEC-152, GR#3).
	key, ok := canonicalGUID(guid)
	if !ok {
		// Unreachable past validateObjectIdentity; fail closed anyway (GR#1).
		return errs.New(ErrInvalidGUID, c.correlationID, map[string]any{"guid": string(guid)})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.tracked[key]; exists {
		return nil
	}
	c.tracked[key] = &checkInRecord{
		guid:     key,
		kind:     kind,
		lifeSpan: lifeSpan,
		facets:   make(map[Facet]int64),
	}
	return nil
}

// validateObjectIdentity enforces the GUID trust boundary for TrackObject
// (SEC-127): the GUID must round-trip through parseGUID, its prefix-derived
// kind must match the declared kind, the life span must be a valid domain
// value, and a citizen (always whole-game per checkLocked) must not be
// registered short-lived. A contradictory or unparseable identity is rejected
// with a registry-sourced error, never stored where it could never resolve
// through CitizenBio/parseGUID or where it would corrupt a citizen's kind.
func validateObjectIdentity(guid GUID, kind ObjectKind, lifeSpan LifeSpan, correlationID string) error {
	parsedKind, _, ok := parseGUID(guid)
	if !ok {
		return errs.New(ErrInvalidGUID, correlationID, map[string]any{"guid": string(guid)})
	}
	if parsedKind != kind {
		return errs.New(ErrInvalidGUID, correlationID, map[string]any{
			"guid":       string(guid),
			"kind":       kind,
			"parsedKind": parsedKind,
		})
	}
	if lifeSpan != LifeSpanShortLived && lifeSpan != LifeSpanWholeGame {
		return errs.New(ErrInvalidGUID, correlationID, map[string]any{
			"guid":     string(guid),
			"lifeSpan": lifeSpan,
		})
	}
	if kind == ObjectCitizen && lifeSpan == LifeSpanShortLived {
		return errs.New(ErrInvalidGUID, correlationID, map[string]any{
			"guid":     string(guid),
			"lifeSpan": lifeSpan,
		})
	}
	return nil
}

// CheckIn returns the consistency checker's per-object record, or
// ErrUnknownObject if no object carries that GUID (AC-21). The returned
// record is a defensive copy.
func (c *CensusAPI) CheckIn(guid GUID) (CheckInRecord, error) {
	if err := c.checkNotCopied("CheckIn"); err != nil {
		return CheckInRecord{}, err
	}
	key, ok := canonicalGUID(guid)
	if !ok {
		return CheckInRecord{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.tracked[key]
	if !ok {
		return CheckInRecord{}, errs.New(ErrUnknownObject, c.correlationID, map[string]any{"guid": string(guid)})
	}
	out := CheckInRecord{
		GUID:         rec.guid,
		Kind:         rec.kind,
		LifeSpan:     rec.lifeSpan,
		Facets:       make(map[Facet]int64, len(rec.facets)),
		LeastCheckIn: rec.leastCheckIn(),
	}
	for f, t := range rec.facets {
		out.Facets[f] = t
	}
	return out, nil
}

// TrackedObjects returns every tracked object's GUID, sorted (the CC's
// query surface, AC-1c). The set never silently forgets an object (AC-6).
func (c *CensusAPI) TrackedObjects() []GUID {
	if err := c.checkNotCopied("TrackedObjects"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return sortedGUIDs(c.tracked)
}

// LostObjects returns the CC's current lost-object findings, oldest GUID
// first (the CC's lost-object surface, AC-1c/AC-6).
func (c *CensusAPI) LostObjects() []LostObject {
	if err := c.checkNotCopied("LostObjects"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]LostObject(nil), c.lost...)
}
