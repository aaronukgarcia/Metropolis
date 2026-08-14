package households

import (
	"sort"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// LifeStage is a household's derived life stage, the "stage" axis of §21's
// appeal profile (stage × wealth × personality). It is derived from the
// household's member composition (see deriveLifeStage), never stored.
type LifeStage uint8

const (
	// LifeStageOther is the fallback for a household that matches none of
	// the named stages (e.g. a working-age adult household without children).
	LifeStageOther LifeStage = iota
	// LifeStageRetired: at least one member is retired.
	LifeStageRetired
	// LifeStageStudent: at least one member is a student (and none retired).
	LifeStageStudent
	// LifeStageFamily: at least one member has children (and none retired /
	// student).
	LifeStageFamily
	// LifeStageYoungSingle: a single young (under 35) member.
	LifeStageYoungSingle
)

// DwellingSizeClass is the dwelling-size class a household seeks (§5.4),
// from studio (0) to mansion (4). A bucketed enum, not a string.
type DwellingSizeClass uint8

const (
	DwellingSizeStudio DwellingSizeClass = iota
	DwellingSizeSmall
	DwellingSizeMedium
	DwellingSizeLarge
	DwellingSizeMansion
)

// MaxDwellingSizeClass is the highest dwelling-size class; used by the
// clamp that keeps a computed class in-range rather than a hardcoded "4".
const MaxDwellingSizeClass = DwellingSizeMansion

// HouseholdProfile is the stage × wealth × personality input to AppealOf
// and DwellingSizePref: a household's derived life stage, aggregated wealth
// (micro-pounds, saturated sum over members), member count, and mean
// personality vector. It is derivable from a household's members via
// [HouseholdsAPI.HouseholdProfile], and constructible directly in tests.
type HouseholdProfile struct {
	Stage       LifeStage
	Wealth      int64 // micro-pounds, saturated sum over members
	Size        int   // member count
	Personality citizens.Personality
}

// AppealScore is AppealOf's result: the typology's appeal for the given
// household profile, plus whether the score came from the documented
// neutral-appeal fallback (AC-11) rather than a genuinely computed
// tag-weighted score. Fallback is the distinguishing marker: a real zero
// (e.g. a stage tag that does not match) has Fallback=false, while an
// empty/unrecognised appealProfile has Fallback=true.
type AppealScore struct {
	Value    int64
	Fallback bool
}

// Typology is the read-only view of one loaded HS housing typology (AC-3):
// its id, name, unlock milestone, verbatim capacity/density text, the parsed
// households-per-hectare density, its appeal tags, and whether its appeal
// degraded to the neutral fallback (AC-11).
type Typology struct {
	ID           string
	Name         string
	Milestone    string
	CapacityRaw  string
	DensityPerHa int64
	AppealTags   []string
	Fallback     bool
}

// DemandEntry is one typology's household count in a demand distribution.
type DemandEntry struct {
	Typology string
	Demand   int64
}

// DemandDistribution is citywide housing demand expressed as a distribution
// over the loaded typologies (AC-5): Total is the household count, and
// Entries is in ascending typology-id order (deterministic, GR#21), so the
// per-typology figures always sum to Total.
type DemandDistribution struct {
	Total   int64
	Entries []DemandEntry
}

// Overcrowding is OvercrowdingOf's result: whether the household's
// membership exceeds its dwelling capacity (one room per member), and the
// occupancy/capacity figures the verdict was derived from (AC-7).
type Overcrowding struct {
	Overcrowded bool
	Occupants   int
	Capacity    int
}

// RentBurden is RentBurdenOf's result: whether monthly rent exceeds the
// §18 35%-of-income threshold, and the safe rent/income ratio (never NaN or
// +Inf — GR#16) it was derived from (AC-7).
type RentBurden struct {
	Burdened bool
	Ratio    float64
}

// Affordability is HousingAffordability's single combined figure (AC-9):
// an Index in [0,100] (higher = more affordable, i.e. a smaller share of
// households stressed) plus the three stress-component counts it combines,
// for drill-through. District granularity is not modelled at Baseline One,
// so this is a citywide figure (documented in AC-9's "citywide" branch).
type Affordability struct {
	Index                int64
	Overcrowded          int64
	RentBurdened         int64
	UnhousedByPreference int64
}

// StockCommand sets the built-stock count of one housing typology (the
// AC-1 "mutation path expressed only as commands"). Count is the number of
// dwelling units of that typology currently built; it must be non-negative.
type StockCommand struct {
	TypologyID string
	Count      int64
}

// rentBurdenThreshold is §18's "financial stress (rent burden > 35%
// income)" line — the only spec-stated numeric threshold near rent burden
// (§5.4/§21 give none of their own), reused here per ASM-249. A spec
// constant, not a balance placeholder.
const rentBurdenThreshold = 0.35

// deriveLifeStage derives a household's life stage from its members, in a
// fixed priority order (retired → student → family → young-single → other)
// so the result is deterministic regardless of member order (GR#21).
func deriveLifeStage(members []citizens.Citizen) LifeStage {
	var hasRetired, hasStudent, hasChild bool
	for _, m := range members {
		if m.Employment.State == citizens.EmploymentRetired {
			hasRetired = true
		}
		if m.Employment.State == citizens.EmploymentStudent {
			hasStudent = true
		}
		if len(m.Children) > 0 {
			hasChild = true
		}
	}
	if hasRetired {
		return LifeStageRetired
	}
	if hasStudent {
		return LifeStageStudent
	}
	// A household of three-or-more members is treated as a family at
	// Baseline One depth (the cold store compresses a citizen's children to
	// a count, so the reconstructed Citizen.Children is nil and the
	// per-member child signal is only visible for hot citizens; household
	// size is the deterministic cold-safe family heuristic).
	if hasChild || len(members) >= 3 {
		return LifeStageFamily
	}
	if len(members) == 1 && members[0].Age() < 35*12 {
		return LifeStageYoungSingle
	}
	return LifeStageOther
}

// meanPersonality aggregates a household's personality as the per-axis mean
// over members (integer division), so a single-member household returns its
// own vector unchanged and a two-member household returns the mid-parent
// blend. Empty input returns the zero vector.
func meanPersonality(members []citizens.Citizen) citizens.Personality {
	if len(members) == 0 {
		return citizens.Personality{}
	}
	var sum [citizens.NumPersonalityAxes]int64
	for _, m := range members {
		for a := 0; a < citizens.NumPersonalityAxes; a++ {
			sum[a] = satAdd(sum[a], int64(m.Personality[a]))
		}
	}
	n := int64(len(members))
	var p citizens.Personality
	for a := 0; a < citizens.NumPersonalityAxes; a++ {
		p[a] = int32(sum[a] / n)
	}
	return p
}

// wealthBand maps a household's aggregate wealth (micro-pounds) onto
// engine.citizens's own 0-4 income band (GR#3: the single source of truth
// for wealth banding is citizens.IncomeBandFor, not a households-local
// re-derivation). Negative or extreme wealth degrades to band 0 / 4 via
// citizens' own thresholds — never a wrap.
func wealthBand(wealthMicroPounds int64) int64 {
	return int64(citizens.IncomeBandFor(wealthMicroPounds))
}

// loadTypologies filters data/buildings.json's entries down to the HS
// housing-typology catalogue and returns it keyed by id plus an ascending-id
// order slice. The count is derived from the loaded entries — there is no
// hardcoded "17" (AC-3): a fixture with a different HS entry count yields a
// different catalogue. An empty HS catalogue is a load failure, never a
// silent empty API (GR#15).
func loadTypologies(b data.Buildings) (map[string]typologyRecord, []string, error) {
	records := make(map[string]typologyRecord)
	var order []string
	for _, e := range b.Entries {
		if e.CatalogueSection != "HS" {
			continue
		}
		rec := typologyRecord{
			id:           e.ID,
			name:         e.Name,
			milestone:    e.Unlock.Milestone,
			capacityRaw:  e.CapacityRaw,
			densityPerHa: parseDensityHHPerHa(e.CapacityRaw),
			tags:         append([]string(nil), e.AppealProfile...),
		}
		rec.fallback = !anyRecognisedTag(rec.tags)
		records[e.ID] = rec
		order = append(order, e.ID)
	}
	if len(records) == 0 {
		return nil, nil, errNoHousingTypologies
	}
	sort.Strings(order)
	return records, order, nil
}

// typologyRecord is the internal, immutable loaded form of one HS typology.
// It is populated once at Load and never mutated afterward, so the query
// methods read it without taking the API mutex (mirroring
// engine.consumption's immutable loaded maps).
type typologyRecord struct {
	id           string
	name         string
	milestone    string
	capacityRaw  string
	densityPerHa int64
	tags         []string
	fallback     bool
}

// parseDensityHHPerHa extracts the leading integer households-per-hectare
// figure from an HS entry's capacityRaw text (e.g. "45 hh/ha",
// "1,200 hh/ha", "300+premium hh/ha"). It returns 0 for entries whose
// capacity is not hh/ha-shaped (e.g. student_halls' "400 beds/block"). The
// raw string is carried verbatim on Typology (GR#15); this is a display/
// convenience parse, never a source of truth.
func parseDensityHHPerHa(capacityRaw string) int64 {
	idx := strings.Index(capacityRaw, "hh/ha")
	if idx < 0 {
		return 0
	}
	prefix := strings.ReplaceAll(capacityRaw[:idx], ",", "")
	var digits []byte
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if c < '0' || c > '9' {
			break
		}
		digits = append(digits, c)
	}
	if len(digits) == 0 {
		return 0
	}
	v, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// dwellingSizePrefThresholds are the two documented thresholds the
// dwelling-size preference refines on top of the wealth band (AC-8): a
// community-minded household or a household of three-or-more members seeks
// one size-class larger than its wealth band alone would indicate. Schema-
// shaped thresholds, not balance numbers.
const (
	communitySpaceThreshold = int64(50)
	familySizeThreshold     = int64(3)
)

// dwellingSizeClass derives the dwelling-size class a household seeks from
// its profile: the wealth band (0-4) is the dominant driver, and a
// community-minded personality or a larger household each add one bounded
// step, clamped to the [studio, mansion] range (AC-8). Pure and
// deterministic — no wall clock, no RNG.
func dwellingSizeClass(p HouseholdProfile) DwellingSizeClass {
	band := wealthBand(p.Wealth)
	var step int64
	if int64(p.Personality[citizens.AxisCommunity]) >= communitySpaceThreshold {
		step++
	}
	if int64(p.Size) >= familySizeThreshold {
		step++
	}
	v := band + step
	if v < 0 {
		v = 0
	}
	if v > int64(MaxDwellingSizeClass) {
		v = int64(MaxDwellingSizeClass)
	}
	return DwellingSizeClass(v)
}
