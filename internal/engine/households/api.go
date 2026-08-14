package households

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// HouseholdsAPI is code.json's "engine.households" inbound contract
// (GUID 5591af37-0157-4e07-a8cf-b4ab87db7466): the §21 seventeen-typology
// housing catalogue, per-typology appeal profiles over household stage ×
// wealth × personality, citywide demand as a distribution over typologies,
// the unhoused-by-preference signal, and overcrowding / rent-burden
// derivation — with query methods and a command-only mutation path
// ([ReportStock]).
//
// The zero value is not usable; construct via [Load], [LoadDefault], or
// [NewFromBuildings]. A *HouseholdsAPI is safe for concurrent use (AC-14):
// the mutable stock map and the wired citizens dependency are guarded by mu,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class, mirroring engine.build's BuildAPI). The loaded typology
// catalogue is immutable after construction and is read without mu.
type HouseholdsAPI struct {
	correlationID string

	// typologies / typologyOrder are populated once at Load and never
	// mutated afterward (immutable): typologies maps typology id → record,
	// and typologyOrder is the ascending-id iteration order (GR#21).
	typologies    map[string]typologyRecord
	typologyOrder []string

	// stock is the per-typology built-stock count (dwelling units),
	// mutated only via ReportStock under mu. Unreported typologies read 0.
	stock map[string]int64

	// citizens is the engine.citizens dependency, wired via SetCitizens and
	// read under mu. Household membership/composition resolves through it
	// (ASM-247) — never a households-local membership store.
	citizens *citizens.CitizensAPI

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer, mirroring
	// engine.build's BuildAPI.self / engine.world's World.self). It is
	// stored exactly once, at the end of construction, before the value is
	// returned to any caller.
	self atomic.Pointer[HouseholdsAPI]
}

// Load reads and validates data/buildings.json (via foundation/data.
// LoadBuildings), derives the HS housing-typology catalogue from its
// catalogueSection "HS" entries, and returns a ready *HouseholdsAPI.
// correlationID is attached to every error this call (and the returned
// API's methods) construct (GR#1). Every failure is a registry-sourced
// *errs.E — never a silent default substitution, never a panic (AC-3).
// The engine.citizens dependency is wired later via SetCitizens.
func Load(dir, correlationID string) (*HouseholdsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	buildings, err := data.LoadBuildings(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrTypologyDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return NewFromBuildings(buildings, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot
// wiring, tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*HouseholdsAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// NewFromBuildings derives the HS housing-typology catalogue directly from an
// already-loaded data.Buildings value (rather than re-reading the file), so
// tests can construct an API over a fixture catalogue with a different HS
// entry count (AC-3's fixture-swap check). correlationID is attached to
// every error this call (and the returned API's methods) construct (GR#1).
func NewFromBuildings(b data.Buildings, correlationID string) (*HouseholdsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	typologies, order, err := loadTypologies(b)
	if err != nil {
		return nil, errs.Wrap(ErrTypologyDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	api := &HouseholdsAPI{
		correlationID: correlationID,
		typologies:    typologies,
		typologyOrder: order,
		stock:         make(map[string]int64),
	}
	// Armed exactly once, before api is returned to any caller (SEC-020).
	api.self.Store(api)
	return api, nil
}

// checkNotCopied rejects a method call on a struct-copied *HouseholdsAPI
// (SEC-020 family, mirroring engine.build's BuildAPI.checkNotCopied).
// Lock-free — a single atomic.Pointer.Load — and therefore safe to run
// before mu is ever touched.
func (h *HouseholdsAPI) checkNotCopied(method string) error {
	if h.self.Load() != h {
		return errs.New(ErrCopiedValue, h.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency used for household
// membership/composition and per-citizen record queries (ASM-247).
func (h *HouseholdsAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := h.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.citizens = c
	return nil
}

// citizens returns the wired CitizensAPI (nil until SetCitizens), read under
// mu so it is safe against a concurrent SetCitizens.
func (h *HouseholdsAPI) citizensAPI() *citizens.CitizensAPI {
	if err := h.checkNotCopied("citizensAPI"); err != nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.citizens
}

// ReportStock sets one housing typology's built-stock count (dwelling
// units) — the command-based mutation path (AC-1). A negative count or an
// unrecognised typology id is rejected with a registry-sourced error and no
// state change (AC-10, GR#16). The stock only feeds UnhousedByPreference /
// HousingAffordability; DemandByType deliberately ignores it (AC-5).
func (h *HouseholdsAPI) ReportStock(cmd StockCommand) error {
	if err := h.checkNotCopied("ReportStock"); err != nil {
		return err
	}
	if cmd.Count < 0 {
		return errs.New(ErrInvalidStock, h.correlationID, map[string]any{
			"typology": cmd.TypologyID,
			"count":    cmd.Count,
		})
	}
	if _, ok := h.typologies[cmd.TypologyID]; !ok {
		return errs.New(ErrUnknownTypology, h.correlationID, map[string]any{
			"typology": cmd.TypologyID,
		})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stock[cmd.TypologyID] = cmd.Count
	return nil
}

// stockOf returns a typology's current built-stock count (0 when never
// reported), read under mu.
func (h *HouseholdsAPI) stockOf(typologyID string) int64 {
	if err := h.checkNotCopied("stockOf"); err != nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stock[typologyID]
}

// Typologies returns the loaded HS housing typologies in ascending-id order
// (GR#21) — a read-only snapshot; the count is data-derived (AC-3).
func (h *HouseholdsAPI) Typologies() []Typology {
	if err := h.checkNotCopied("Typologies"); err != nil {
		return nil
	}
	out := make([]Typology, 0, len(h.typologyOrder))
	for _, id := range h.typologyOrder {
		out = append(out, h.typologySnapshot(id))
	}
	return out
}

// Typology returns one loaded typology by id, and whether it exists.
func (h *HouseholdsAPI) Typology(id string) (Typology, bool) {
	if err := h.checkNotCopied("Typology"); err != nil {
		return Typology{}, false
	}
	if _, ok := h.typologies[id]; !ok {
		return Typology{}, false
	}
	return h.typologySnapshot(id), true
}

// TypologyCount returns the number of loaded housing typologies — derived
// from the data at load time, never a hardcoded literal (AC-3).
func (h *HouseholdsAPI) TypologyCount() int {
	if err := h.checkNotCopied("TypologyCount"); err != nil {
		return 0
	}
	return len(h.typologyOrder)
}

// typologySnapshot builds the read-only Typology view of one loaded record.
func (h *HouseholdsAPI) typologySnapshot(id string) Typology {
	if err := h.checkNotCopied("typologySnapshot"); err != nil {
		return Typology{}
	}
	rec := h.typologies[id]
	return Typology{
		ID:           rec.id,
		Name:         rec.name,
		Milestone:    rec.milestone,
		CapacityRaw:  rec.capacityRaw,
		DensityPerHa: rec.densityPerHa,
		AppealTags:   append([]string(nil), rec.tags...),
		Fallback:     rec.fallback,
	}
}

// AppealOf returns a typology's appeal for a household profile — a function
// over the profile's stage × wealth × personality (AC-4), not a single
// scalar per typology. An unrecognised typology id returns ErrUnknownTypology
// (AC-10); a typology whose tag array is empty/unrecognised returns a
// neutral-appeal fallback with AppealScore.Fallback == true (AC-11).
func (h *HouseholdsAPI) AppealOf(typologyID string, profile HouseholdProfile) (AppealScore, error) {
	if err := h.checkNotCopied("AppealOf"); err != nil {
		return AppealScore{}, err
	}
	rec, ok := h.typologies[typologyID]
	if !ok {
		return AppealScore{}, errs.New(ErrUnknownTypology, h.correlationID, map[string]any{
			"typology": typologyID,
		})
	}
	if rec.fallback {
		return AppealScore{Value: 0, Fallback: true}, nil
	}
	var score int64
	for _, tag := range rec.tags {
		score = num.SatAdd(score, appealContribution(tag, profile))
	}
	return AppealScore{Value: score}, nil
}

// MembersOf returns a household's member citizen ids, read from CitizensAPI
// (ASM-247/AC-2) — the single source of truth for membership. An unknown
// householdId returns ErrUnknownHousehold (AC-10).
func (h *HouseholdsAPI) MembersOf(householdID uint64) ([]uint64, error) {
	if err := h.checkNotCopied("MembersOf"); err != nil {
		return nil, err
	}
	c := h.citizensAPI()
	if c == nil {
		return nil, errs.New(ErrDependencyMissing, h.correlationID, map[string]any{"dependency": "citizens", "operation": "MembersOf"})
	}
	hh, ok := c.Household(householdID, h.correlationID)
	if !ok {
		return nil, errs.New(ErrUnknownHousehold, h.correlationID, map[string]any{"household": householdID})
	}
	return append([]uint64(nil), hh.Members...), nil
}

// HouseholdProfile derives a household's stage × wealth × personality
// profile from its members via CitizensAPI. An unknown householdId returns
// ErrUnknownHousehold; a member id that does not resolve returns
// ErrOrphanedMember (the conservation invariant's orphan half, loud rather
// than silently skipped).
func (h *HouseholdsAPI) HouseholdProfile(householdID uint64) (HouseholdProfile, error) {
	if err := h.checkNotCopied("HouseholdProfile"); err != nil {
		return HouseholdProfile{}, err
	}
	c := h.citizensAPI()
	if c == nil {
		return HouseholdProfile{}, errs.New(ErrDependencyMissing, h.correlationID, map[string]any{"dependency": "citizens", "operation": "HouseholdProfile"})
	}
	hh, ok := c.Household(householdID, h.correlationID)
	if !ok {
		return HouseholdProfile{}, errs.New(ErrUnknownHousehold, h.correlationID, map[string]any{"household": householdID})
	}
	members := make([]citizens.Citizen, 0, len(hh.Members))
	var wealth int64
	for _, mid := range hh.Members {
		cit, ok := c.CitizenAt(mid, h.correlationID)
		if !ok {
			return HouseholdProfile{}, errs.New(ErrOrphanedMember, h.correlationID, map[string]any{
				"member":    mid,
				"household": householdID,
			})
		}
		members = append(members, cit)
		wealth = num.SatAdd(wealth, cit.Wealth)
	}
	return HouseholdProfile{
		Stage:       deriveLifeStage(members),
		Wealth:      wealth,
		Size:        len(members),
		Personality: meanPersonality(members),
	}, nil
}

// OvercrowdingOf reports whether a household's membership exceeds its
// dwelling capacity (one room per member), using CitizensAPI's own
// Household.Overcrowded (GR#3 — the citizens entity is the single source of
// the overcrowding definition, AC-7). An unknown householdId returns
// ErrUnknownHousehold (AC-10).
func (h *HouseholdsAPI) OvercrowdingOf(householdID uint64) (Overcrowding, error) {
	if err := h.checkNotCopied("OvercrowdingOf"); err != nil {
		return Overcrowding{}, err
	}
	c := h.citizensAPI()
	if c == nil {
		return Overcrowding{}, errs.New(ErrDependencyMissing, h.correlationID, map[string]any{"dependency": "citizens", "operation": "OvercrowdingOf"})
	}
	hh, ok := c.Household(householdID, h.correlationID)
	if !ok {
		return Overcrowding{}, errs.New(ErrUnknownHousehold, h.correlationID, map[string]any{"household": householdID})
	}
	return overcrowdingFrom(hh), nil
}

// overcrowdingFrom derives the Overcrowding verdict from a household's
// membership and dwelling capacity, applying the citizens.Household
// entity's own Overcrowded definition (one room per member — GR#3). It is
// the single place this package maps a citizens.Household onto an
// Overcrowding, so the occupants > capacity and under-threshold shapes are
// both testable at this package's own unit boundary (AC-7).
func overcrowdingFrom(hh citizens.Household) Overcrowding {
	return Overcrowding{
		Overcrowded: hh.Overcrowded(),
		Occupants:   len(hh.Members),
		Capacity:    int(hh.DwellingRooms),
	}
}

// RentBurdenOf reports whether monthly rent exceeds the §18 35%-of-income
// threshold for a household, reusing CitizensAPI's Household.RentBurdenRatio
// (GR#3 — the citizens entity owns the safe ratio; §18's threshold is the
// only spec-stated numeric line, ASM-249). Negative rent or income returns
// ErrInvalidAmount (FEAT-086); an unknown householdId returns
// ErrUnknownHousehold (AC-10). The returned Ratio is never NaN or +Inf
// (income ≤ 0 yields the citizens sentinel, not a division-by-zero).
func (h *HouseholdsAPI) RentBurdenOf(householdID uint64, monthlyRentMicroPounds, monthlyIncomeMicroPounds int64) (RentBurden, error) {
	if err := h.checkNotCopied("RentBurdenOf"); err != nil {
		return RentBurden{}, err
	}
	if monthlyRentMicroPounds < 0 || monthlyIncomeMicroPounds < 0 {
		return RentBurden{}, errs.New(ErrInvalidAmount, h.correlationID, map[string]any{
			"rent":   monthlyRentMicroPounds,
			"income": monthlyIncomeMicroPounds,
		})
	}
	c := h.citizensAPI()
	if c == nil {
		return RentBurden{}, errs.New(ErrDependencyMissing, h.correlationID, map[string]any{"dependency": "citizens", "operation": "RentBurdenOf"})
	}
	hh, ok := c.Household(householdID, h.correlationID)
	if !ok {
		return RentBurden{}, errs.New(ErrUnknownHousehold, h.correlationID, map[string]any{"household": householdID})
	}
	ratio := hh.RentBurdenRatio(monthlyRentMicroPounds, monthlyIncomeMicroPounds)
	return RentBurden{
		Burdened: ratio > rentBurdenThreshold,
		Ratio:    ratio,
	}, nil
}

// DemandByType returns citywide housing demand as a distribution over the
// loaded typologies (AC-5): for each household, the typology with the
// highest appeal (ties broken by ascending typology id, deterministic per
// GR#21), tallied into a per-typology figure whose total is the household
// count. Demand is computed from the population's stage/wealth/personality
// composition ALONE — it is independent of the current built-stock mix
// (demand is what households would prefer, not what happens to be built).
// householdIDs is the household-id set the composition root supplies to
// aggregate over (CitizensAPI exposes per-id queries, not an enumeration).
func (h *HouseholdsAPI) DemandByType(householdIDs []uint64) (DemandDistribution, error) {
	if err := h.checkNotCopied("DemandByType"); err != nil {
		return DemandDistribution{}, err
	}
	counts := make(map[string]int64, len(h.typologyOrder))
	for _, hid := range householdIDs {
		profile, err := h.HouseholdProfile(hid)
		if err != nil {
			return DemandDistribution{}, err
		}
		top := h.topTypology(profile)
		counts[top] = num.SatAdd(counts[top], 1)
	}

	entries := make([]DemandEntry, 0, len(h.typologyOrder))
	var total int64
	for _, id := range h.typologyOrder {
		d := counts[id] // 0 for an unreported/preferred-never typology
		total = num.SatAdd(total, d)
		entries = append(entries, DemandEntry{Typology: id, Demand: d})
	}
	return DemandDistribution{Total: total, Entries: entries}, nil
}

// topTypology returns the typology id with the highest appeal for a profile,
// breaking ties by the ascending-id iteration order (deterministic, GR#21 —
// no map iteration, no RNG).
func (h *HouseholdsAPI) topTypology(profile HouseholdProfile) string {
	if err := h.checkNotCopied("topTypology"); err != nil {
		return ""
	}
	best := ""
	var bestScore int64
	for _, id := range h.typologyOrder {
		rec := h.typologies[id]
		var score int64
		if !rec.fallback {
			for _, tag := range rec.tags {
				score = num.SatAdd(score, appealContribution(tag, profile))
			}
		}
		if best == "" || score > bestScore {
			best, bestScore = id, score
		}
	}
	return best
}

// UnhousedByPreference returns the number of households whose preferred
// typology is under-supplied — the demand shortfall summed over every
// typology whose preferred demand exceeds its built stock (AC-6). It is a
// distinct signal from raw vacancy: citywide vacancy (summed stock across
// all typologies) can be positive while a whole personality segment is left
// unhoused-by-preference because its typologies are absent from stock.
func (h *HouseholdsAPI) UnhousedByPreference(householdIDs []uint64) (int64, error) {
	if err := h.checkNotCopied("UnhousedByPreference"); err != nil {
		return 0, err
	}
	d, err := h.DemandByType(householdIDs)
	if err != nil {
		return 0, err
	}
	var unhoused int64
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, e := range d.Entries {
		stock := h.stock[e.Typology] // 0 when never reported
		if e.Demand > stock {
			unhoused = num.SatAdd(unhoused, num.SatSub(e.Demand, stock))
		}
	}
	return unhoused, nil
}

// DwellingSizePref returns the dwelling-size class a household seeks —
// a function of household size, wealth, and personality (AC-8), never a
// fixed constant. An unknown householdId returns ErrUnknownHousehold (AC-10).
func (h *HouseholdsAPI) DwellingSizePref(householdID uint64) (DwellingSizeClass, error) {
	if err := h.checkNotCopied("DwellingSizePref"); err != nil {
		return 0, err
	}
	profile, err := h.HouseholdProfile(householdID)
	if err != nil {
		return 0, err
	}
	return dwellingSizeClass(profile), nil
}

// HousingAffordability returns a single citywide affordability figure
// combining overcrowding + rent burden + unhoused-by-preference (AC-9) —
// the one queried value engine.attract's housingAffordability term needs,
// so the master dial does not reimplement this package's math. District
// granularity is not modelled at Baseline One, so this is a citywide figure
// (the documented branch of AC-9). monthlyRent/monthlyIncome are the
// Baseline One economic inputs (per-household income differentiation is a
// later sprint); a negative figure returns ErrInvalidAmount (FEAT-086).
func (h *HouseholdsAPI) HousingAffordability(householdIDs []uint64, monthlyRentMicroPounds, monthlyIncomeMicroPounds int64) (Affordability, error) {
	if err := h.checkNotCopied("HousingAffordability"); err != nil {
		return Affordability{}, err
	}
	if monthlyRentMicroPounds < 0 || monthlyIncomeMicroPounds < 0 {
		return Affordability{}, errs.New(ErrInvalidAmount, h.correlationID, map[string]any{
			"rent":   monthlyRentMicroPounds,
			"income": monthlyIncomeMicroPounds,
		})
	}
	total := int64(len(householdIDs))
	var overcrowded, rentBurdened int64
	for _, hid := range householdIDs {
		oc, err := h.OvercrowdingOf(hid)
		if err != nil {
			return Affordability{}, err
		}
		if oc.Overcrowded {
			overcrowded = num.SatAdd(overcrowded, 1)
		}
		rb, err := h.RentBurdenOf(hid, monthlyRentMicroPounds, monthlyIncomeMicroPounds)
		if err != nil {
			return Affordability{}, err
		}
		if rb.Burdened {
			rentBurdened = num.SatAdd(rentBurdened, 1)
		}
	}
	unhoused, err := h.UnhousedByPreference(householdIDs)
	if err != nil {
		return Affordability{}, err
	}
	stressed := num.SatAdd(num.SatAdd(overcrowded, rentBurdened), unhoused)
	return Affordability{
		Index:                affordabilityIndex(stressed, total),
		Overcrowded:          overcrowded,
		RentBurdened:         rentBurdened,
		UnhousedByPreference: unhoused,
	}, nil
}

// affordabilityIndex maps a stressed-household count (overcrowded +
// rent-burdened + unhoused-by-preference, a household may be stressed in
// more than one way) onto a [0,100] index, higher = more affordable. A
// vacant city (total == 0) reads fully affordable (100); a fully-stressed
// city reads 0. Integer-only with saturating multiply (FEAT-086).
func affordabilityIndex(stressed, total int64) int64 {
	if total <= 0 {
		return 100
	}
	if stressed >= total {
		return 0
	}
	num, _ := num.SafeMul(100, num.SatSub(total, stressed))
	return num / total
}
