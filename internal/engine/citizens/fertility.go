package citizens

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Fertility / natural increase (FEAT-160), self-contained within
// engine.citizens: resident couples have children who become new
// persistent citizens, exactly mirroring mortality.go's deterministic
// per-citizen monthly-hazard idiom (Gompertz-Makeham there, a data-sourced
// triangular age curve here). Every magnitude lives in data/fertility.json
// (GR#15), never a Go literal — the same balance-placeholder discipline as
// data/census.json.

// FileFertility is the fertility config filename, relative to the resolved
// data directory. Loaded directly by this package (not registered in
// foundation/data's §24 set), the same module-owned-loader precedent
// engine.census/engine.market/engine.season use.
const FileFertility = "fertility.json"

// FertilityNumber is one schema-validated numeric parameter in
// data/fertility.json — mirrors engine.census's Number type exactly (value
// + unit + disclosure), so a downstream reader never has to guess units.
type FertilityNumber struct {
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Disclosure string  `json:"disclosure"`
}

// FertilityMeta is data/fertility.json's documentation block.
type FertilityMeta struct {
	Module        string   `json:"module"`
	FeatureKey    string   `json:"featureKey"`
	SpecRefs      []string `json:"specRefs"`
	BalanceRegime string   `json:"balanceRegime"`
}

// FertilityParams holds the childbearing-window and rate placeholders
// (FEAT-160). Every field is a balance placeholder pending Aaron's
// row-by-row pass — tests assert direction/structure only, never a pinned
// magnitude (the balance-number regime).
type FertilityParams struct {
	MinChildbearingAgeYears FertilityNumber `json:"minChildbearingAgeYears"`
	MaxChildbearingAgeYears FertilityNumber `json:"maxChildbearingAgeYears"`
	PeakFertilityAgeYears   FertilityNumber `json:"peakFertilityAgeYears"`
	BaseMonthlyBirthRate    FertilityNumber `json:"baseMonthlyBirthRate"`
	MaxChildrenPerHousehold FertilityNumber `json:"maxChildrenPerHousehold"`
}

// FertilityConfig is the loaded data/fertility.json configuration.
type FertilityConfig struct {
	Version int             `json:"version"`
	Comment string          `json:"$comment"`
	Meta    FertilityMeta   `json:"meta"`
	Params  FertilityParams `json:"params"`
}

// validate rejects a schema-invalid FertilityConfig: a missing unit or
// disclosure, a non-finite value, an inverted/out-of-order age window, a
// rate outside [0,1], or a negative household cap. No silent default
// substitution — the malformed parameter is named and the load fails
// (mirrors engine.census's Config.validate).
func (cfg *FertilityConfig) validate(correlationID string) error {
	bad := func(rule string) error {
		return errs.New(ErrFertilityDataInvalid, correlationID, map[string]any{"rule": rule})
	}

	if cfg.Version <= 0 {
		return bad("version must be positive")
	}
	if cfg.Meta.BalanceRegime == "" || cfg.Meta.FeatureKey == "" {
		return bad("meta.balanceRegime and meta.featureKey are required")
	}

	numbers := []struct {
		field string
		n     FertilityNumber
	}{
		{"params.minChildbearingAgeYears", cfg.Params.MinChildbearingAgeYears},
		{"params.maxChildbearingAgeYears", cfg.Params.MaxChildbearingAgeYears},
		{"params.peakFertilityAgeYears", cfg.Params.PeakFertilityAgeYears},
		{"params.baseMonthlyBirthRate", cfg.Params.BaseMonthlyBirthRate},
		{"params.maxChildrenPerHousehold", cfg.Params.MaxChildrenPerHousehold},
	}
	for _, e := range numbers {
		if !num.IsFinite(e.n.Value) {
			return bad(e.field + ".value must be finite")
		}
		if e.n.Unit == "" {
			return bad(e.field + ".unit is required")
		}
		if e.n.Disclosure == "" {
			return bad(e.field + ".disclosure is required")
		}
	}

	minY := cfg.Params.MinChildbearingAgeYears.Value
	maxY := cfg.Params.MaxChildbearingAgeYears.Value
	peakY := cfg.Params.PeakFertilityAgeYears.Value
	if minY < 0 {
		return bad("params.minChildbearingAgeYears must be non-negative")
	}
	if maxY <= minY {
		return bad("params.maxChildbearingAgeYears must exceed minChildbearingAgeYears")
	}
	if peakY < minY || peakY > maxY {
		return bad("params.peakFertilityAgeYears must lie within [min, max]")
	}
	rate := cfg.Params.BaseMonthlyBirthRate.Value
	if rate < 0 || rate > 1 {
		return bad("params.baseMonthlyBirthRate must be in [0,1]")
	}
	if cfg.Params.MaxChildrenPerHousehold.Value < 0 {
		return bad("params.maxChildrenPerHousehold must be non-negative")
	}
	return nil
}

// LoadFertilityConfig reads and validates data/fertility.json from dir,
// returning the parsed FertilityConfig. Every failure is a registry-sourced
// *errs.E (GR#7).
func LoadFertilityConfig(dir, correlationID string) (FertilityConfig, error) {
	var cfg FertilityConfig
	path := filepath.Join(dir, FileFertility)
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, errs.Wrap(ErrFertilityDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(ErrFertilityDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	if err := cfg.validate(correlationID); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadDefaultFertilityConfig resolves data/'s directory via foundation/data
// and loads data/fertility.json — the convenience entry point NewCitizensAPI
// uses.
func LoadDefaultFertilityConfig(correlationID string) (FertilityConfig, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return FertilityConfig{}, err
	}
	return LoadFertilityConfig(dir, correlationID)
}

// ageFactorYears is the per-partner triangular fertility modifier: 0 at and
// outside [minY, maxY], rising linearly to 1 at peakY, then falling linearly
// back to 0 at maxY. A placeholder curve shape (FEAT-160) — directional
// only (rises then falls, never negative, zero outside the window), never a
// pinned magnitude.
func ageFactorYears(ageYears, minY, maxY, peakY float64) float64 {
	if ageYears < minY || ageYears > maxY {
		return 0
	}
	if ageYears <= peakY {
		if peakY <= minY {
			return 1
		}
		return (ageYears - minY) / (peakY - minY)
	}
	if maxY <= peakY {
		return 1
	}
	return (maxY - ageYears) / (maxY - peakY)
}

// FertilityHazard returns the per-month probability of a birth for a couple
// whose partners are aged ageAMonths/ageBMonths, from cfg's data-sourced
// childbearing window, peak age, and base rate (§5.1/§5.4 placeholder
// curve, GR#15) — mirrors MortalityHazard's shape (a data-derived base rate
// modulated by age, clamped to [0,1]). The couple's hazard is the base rate
// scaled by BOTH partners' individual age-modifiers (ageFactorYears), so a
// couple where either partner is outside the childbearing window draws 0,
// and a couple both at the peak age draws the base rate unmodified.
// Directional guarantees: 0 whenever either partner is outside
// [min,max]; positive and rising toward the peak within the window; never
// negative; never exceeds cfg's base rate.
func FertilityHazard(ageAMonths, ageBMonths int64, cfg FertilityConfig) float64 {
	minY := cfg.Params.MinChildbearingAgeYears.Value
	maxY := cfg.Params.MaxChildbearingAgeYears.Value
	peakY := cfg.Params.PeakFertilityAgeYears.Value
	base := cfg.Params.BaseMonthlyBirthRate.Value

	fA := ageFactorYears(monthsToYears(ageAMonths), minY, maxY, peakY)
	fB := ageFactorYears(monthsToYears(ageBMonths), minY, maxY, peakY)

	h := base * fA * fB
	if h < 0 {
		return 0
	}
	if h > 1 {
		return 1
	}
	return h
}

// monthsToYears converts an age in months to fractional years.
func monthsToYears(ageMonths int64) float64 {
	return float64(ageMonths) / 12.0
}

// FertilityEligible reports whether a couple may be considered for a birth
// this month (FEAT-160): both partners genuinely partnered (partner id
// non-zero and not self-referential), both within cfg's childbearing age
// window, and the couple's household under cfg's max-children-per-household
// cap. F2 fix (destructive-review REJECT): householdChildCount MUST be the
// household's ACTUAL CURRENT child count -- the caller (applyFertilityLocked,
// via householdChildCountLocked) counts the live members of the couple's
// household that are neither partner, never either partner's own lifetime
// cold childCount column. That column is NOT a safe proxy: LifeEventPartner
// lets a citizen with children from a dissolved prior relationship join a
// FRESH household without resetting their lifetime childCount, so keying the
// cap on "whichever partner has the lower id" (the acting partner, whose
// childCount was being read) made two structurally-identical households
// diverge in eligibility purely by id ordering. Two structurally-identical
// households (same real child count) MUST always get the same eligibility
// regardless of id ordering or either partner's prior-relationship history.
func FertilityEligible(selfID, partnerID uint64, householdChildCount int, ageAMonths, ageBMonths int64, cfg FertilityConfig) bool {
	if partnerID == 0 || partnerID == selfID {
		return false
	}
	if householdChildCount >= int(cfg.Params.MaxChildrenPerHousehold.Value) {
		return false
	}
	minY := cfg.Params.MinChildbearingAgeYears.Value
	maxY := cfg.Params.MaxChildbearingAgeYears.Value
	ay := monthsToYears(ageAMonths)
	by := monthsToYears(ageBMonths)
	return ay >= minY && ay <= maxY && by >= minY && by <= maxY
}

// CoupleBirth is the per-couple monthly birth decision (mirrors
// MortalityDeath's shape exactly): a birth occurs iff a draw from the
// independent counter-based hash stream hash(worldSeed, householdID, month,
// "fertility") falls below hazard. Keyed by the couple's STABLE household id
// (not either partner's citizen id) so the draw is attributed to the couple
// exactly once — never double-counted per partner — and reproducible
// (AC-15-style determinism): the same (seed, household, month, hazard)
// always draws the same outcome. The "fertility" purpose tag keeps this
// stream independent of "mortality"/"education"/"employment" (distinct
// purpose ⇒ distinct hash, per foundation/det's counter-based stream
// construction), so a fertility draw can never perturb another citizen
// stream.
func CoupleBirth(seed uint64, householdID uint64, month int64, hazard float64) bool {
	stream := det.NewStream(seed, householdID, month, "fertility")
	return stream.Float64() < hazard
}

// fertilityChildIDBase is the disjoint high-bit namespace fertility-born
// child ids are allocated from (CitizensAPI.nextFertilityChildID starts
// here). THIS IS 2^63, NOT 2^62 (destructive-review REJECT, FEAT-169):
// engine.attract independently mints admitted-migrant citizen ids from its
// OWN high-bit prefix, migrantIDHighBit = 1<<62 (migration.go), starting a
// separate counter at 1 — the ORIGINAL 1<<62 choice here collided with
// that same range, so with both compose's citizens cold-pass tick AND
// attract's migration hook live in the same composition (FEAT-169), a
// duplicate citizen id was reachable within months of simulated play, and
// TotalPopulation's row-count-based conservation view could not see it
// (LifeEventBirth appended unconditionally — see ErrDuplicateCitizenID's
// doc comment for why that is now also independently guarded).
//
// The full disjoint id map (documented identically in compose/doc.go):
//
//	[1,        2^62)  compose-minted ids: baseline-one seed population +
//	                   any future direct compose seeding (simState.nextCitizenID)
//	[2^62,     2^63)  engine.attract-minted admitted-migrant ids
//	                   (migrantIDHighBit, migration.go)
//	[2^63,     ...)    engine.citizens fertility-born child ids (this const)
//
// This is a DOCUMENTED INTEGRATION SEAM enforced by convention across THREE
// packages (compose/attract/citizens) plus TWO independent defenses: a
// Wire-time assertion in compose (FertilityChildIDBase >=
// 2*attract.MigrantIDBase, ErrCitizenIDNamespaceSeam) and this package's
// own per-mint ErrDuplicateCitizenID rejection — not a single shared
// allocator across all three (that unification is a larger refactor,
// flagged as a follow-up, not done here). Changing this base changes the
// determinism regime (a fertility child's id, hence its InitPersonality
// draw, depends on this constant) — acceptable pre-release (no persisted
// saves to preserve compatibility with yet), but any test asserting a
// literal fertility-child id must be re-verified against the new base (the
// fertility hazard/CoupleBirth stream itself keys on householdID+month, NOT
// child id, so guaranteed-birth-month test fixtures are unaffected — only
// the resulting child's own id/personality changed).
const fertilityChildIDBase = uint64(1) << 63

// FertilityChildIDBase exports fertilityChildIDBase's value (unchanged) as
// the ID-SEAM CONTRACT boundary a consumer's own sequential id allocator
// must stay strictly below (FEAT-169's compose-side guard,
// compose.go's simState.nextCitizenID / ErrCitizenIDNamespaceSeam, and the
// Wire-time attract.MigrantIDBase cross-check). This is the ICD-preferred
// resolution of the FEAT-160/FEAT-169 open decision
// (docs/planning/icd/engine.citizens-coldpass.md §12.2): an explicit,
// checked, verified-disjoint contract between the id spaces — not a single
// shared allocator (that unification is a larger refactor, flagged as a
// follow-up, not done here).
const FertilityChildIDBase = fertilityChildIDBase

// applyFertilityLocked runs the monthly fertility pass over every citizen
// whose cold shard is scheduled on this day-tick (the same amortised
// schedule mortality/education/job-matching use, AC-6/AC-7 parity: a
// couple's fertility draw happens exactly once per calendar month). Unlike
// applyMonthly's mortality/education/job/health/satisfaction updates, this
// runs SEQUENTIALLY (never inside runShardsParallel's per-shard goroutines):
// a couple's eligibility/hazard check reads the PARTNER's age and household
// data, which — for a couple split across two shards both scheduled the
// same day-tick — would otherwise be a concurrent cross-shard read racing
// that other shard's own mortality/removeAt mutation. Called with c.mu
// already held (from AdvanceDayTick), after the parallel pass has fully
// completed, so every cross-shard read here is safe (single goroutine, no
// concurrent shard mutation in flight). Only the "acting" partner (the
// couple's lower citizen id) is processed, so a couple split across two
// scheduled shards is never double-processed. Elevated HOT/WARM citizens
// are NOT skipped here (BUG-270): every citizen — elevated or not — lives
// in the cold store, so iterating its rows covers both tiers through the
// single source of truth, and the once-per-month shard schedule guarantees
// a couple draws exactly once regardless of tier. Returns the number of
// births applied this call.
func (c *CitizensAPI) applyFertilityLocked(seed uint64, month int64, shards []int, correlationID string) int {
	births := 0
	for _, shard := range shards {
		s := c.cold[shard]
		// Snapshot the pre-pass row count: a birth appends a new row (to
		// this shard or another), and the new child must never itself be
		// considered for fertility within the SAME pass that created it.
		n := s.count()
		for i := 0; i < n; i++ {
			id := s.ids[i]
			partner := uint64(s.partners[i])
			if partner == 0 || partner == id {
				continue
			}
			if partner < id {
				continue // not the acting (lower-id) partner: skip, avoid double-processing
			}
			partnerRec, ok := c.coldRecord(partner)
			if !ok {
				continue // partner not resolvable (e.g. deceased, not yet unwired): no-op, not a crash
			}

			selfBirthMonth := s.epochMonth + int64(s.birthDelta[i])
			ageSelf := month - selfBirthMonth
			agePartner := month - partnerRec.BirthMonth
			householdID := uint64(s.households[i])
			// F2 fix: the cap input is the household's ACTUAL CURRENT child
			// count (live membership), never either partner's own lifetime
			// cold childCount column -- see FertilityEligible's doc comment.
			childCount := c.householdChildCountLocked(householdID, id, partner, month)

			if !FertilityEligible(id, partner, childCount, ageSelf, agePartner, c.fertilityCfg) {
				continue
			}
			hazard := FertilityHazard(ageSelf, agePartner, c.fertilityCfg)
			if hazard <= 0 {
				continue
			}
			if !CoupleBirth(seed, householdID, month, hazard) {
				continue
			}
			if c.birthChildLocked(id, partner, householdID, month, correlationID) {
				births++
			}
		}
	}
	return births
}

// householdChildCountLocked returns the number of a household's CURRENT
// members that are actually CHILDREN -- the household's real, live child
// count (F2 fix, FEAT-160; hardened round-3 against a secondary finding).
// This replaces reading either partner's own lifetime cold childCount
// column as the cap proxy: that column is per-CITIZEN lineage (never reset
// across re-partnering, see FertilityEligible's doc comment) while this
// walk is per-HOUSEHOLD membership, so two structurally-identical fresh
// households (0 real children) always report 0 here regardless of id
// ordering or either partner's prior-relationship history.
//
// Round-3 hardening: the original implementation counted ANY member that
// was not partnerA/partnerB, with no age/adult check -- "not one of the two
// current partners" is not the same predicate as "is a child". An adult
// non-partner member (e.g. a leaked prior-household member, or in the
// future a grown child who never left) would be miscounted as a child
// against the cap. A member is now only counted if they are genuinely BELOW
// the adult line, using cfg's own MinChildbearingAgeYears as the coherent
// adult threshold (the same window FertilityEligible already treats as
// "old enough" -- no new balance parameter invented). A member whose age
// cannot be resolved (should not happen for a live household member, but
// defensively handled per GR#1) is counted as a child rather than skipped,
// so a data gap can only ever tighten the cap, never silently loosen it.
// Must be called with c.mu already held.
func (c *CitizensAPI) householdChildCountLocked(householdID, partnerA, partnerB uint64, month int64) int {
	h, ok := c.households[householdID]
	if !ok {
		return 0
	}
	adultThresholdMonths := int64(c.fertilityCfg.Params.MinChildbearingAgeYears.Value * 12)
	n := 0
	for _, m := range h.Members {
		if m == partnerA || m == partnerB {
			continue
		}
		birthMonth, ok := c.birthMonthOfLocked(m)
		if !ok || month-birthMonth < adultThresholdMonths {
			n++
		}
	}
	return n
}

// birthMonthOfLocked returns a citizen's BirthMonth (hot or cold), or
// (0, false) if the citizen is unresolvable in either store -- mirrors
// personalityOfLocked's hot-then-cold resolution shape.
func (c *CitizensAPI) birthMonthOfLocked(id uint64) (int64, bool) {
	if cit, ok := c.hot[id]; ok {
		return int64(cit.BirthMonth), true
	}
	if r, ok := c.coldRecord(id); ok {
		return int64(r.BirthMonth), true
	}
	return 0, false
}

// birthChildLocked creates one new citizen for a couple's fertility birth
// (FEAT-160): allocates a deterministic id from the fertility-child id
// space, derives personality via InitPersonality from both parents' blended
// personality (the same parental-blend convention citizen.go documents for
// any birth), appends the cold record, and wires the child into both
// parents' childCount (cold, and Children if a parent happens to be hot)
// and the shared household's membership. Returns true iff the birth was
// applied. Must be called with c.mu already held.
func (c *CitizensAPI) birthChildLocked(parentA, parentB, householdID uint64, month int64, correlationID string) bool {
	childID := fertilityChildIDBase + c.nextFertilityChildID
	c.nextFertilityChildID++

	pA := c.personalityOfLocked(parentA)
	pB := c.personalityOfLocked(parentB)

	child := Citizen{
		ID:          childID,
		BirthMonth:  int32(month),
		Household:   householdID,
		Personality: InitPersonality(c.seed, childID, month, pA, pB),
	}
	if err := ValidateCitizen(child, c.householdExistsLocked, correlationID); err != nil {
		// Never expected for a well-formed month/config, but logged loudly
		// rather than silently dropped or allowed to corrupt the cold store
		// (GR#1) — the birth is skipped for this couple this month.
		_ = errs.Wrap(ErrFertilityBirthRejected, correlationID, err, map[string]any{
			"childID": childID,
			"parentA": parentA,
			"parentB": parentB,
			"cause":   err.Error(),
		})
		return false
	}

	r := hotToColdRecord(child, districtOf(child.Home))
	c.cold[det.ShardForEntity(childID)].append(r)

	// Write-through to both parents (cold childCount + hot Children, exactly
	// mirroring the dual-store discipline LifeEventPartner/education-drift
	// use: the cold store is the single source of truth, so the wiring
	// reaches it regardless of either parent's fidelity tier).
	c.incrementChildCountLocked(parentA, childID)
	c.incrementChildCountLocked(parentB, childID)

	if h, ok := c.households[householdID]; ok {
		h.AddMember(childID)
	}

	c.curMonthBirths++
	return true
}

// personalityOfLocked returns a citizen's current personality vector, hot
// or cold, or the zero vector if the citizen is unresolvable (defensive —
// InitPersonality's mid-parent blend degrades gracefully to a
// single-parent-equivalent blend rather than panicking).
func (c *CitizensAPI) personalityOfLocked(id uint64) Personality {
	if cit, ok := c.hot[id]; ok {
		return cit.Personality
	}
	if r, ok := c.coldRecord(id); ok {
		return widenPersonality(r.Personality)
	}
	return Personality{}
}

// incrementChildCountLocked appends childID to parentID's Children (if hot)
// and increments its cold childCount column (clamped at 255, mirroring
// safeUint8's contract — AC-13 already rejects a >255 count at
// ValidateCitizen, this is the same ceiling applied in place).
func (c *CitizensAPI) incrementChildCountLocked(parentID, childID uint64) {
	if cit, ok := c.hot[parentID]; ok {
		cit.Children = append(cit.Children, childID)
	}
	c.mutateColdLocked(parentID, func(s *ColdShard, row int) {
		if s.childCount[row] < 255 {
			s.childCount[row]++
		}
	})
}
