package citizens

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestLoadFertilityConfigFailurePathsRenderRule (BUG-314): MET-G008's
// message template is "data/fertility.json is missing, malformed, or fails
// schema validation: {rule}", but the file-read and JSON-decode failure
// paths in LoadFertilityConfig supplied only path/cause, never rule — so
// {rule} rendered literally on the two likeliest failure paths (a missing
// file, or malformed JSON). This test triggers BOTH paths and asserts the
// rendered Display() names the actual rule with no literal "{rule}"
// surviving — rendered, not read (the "reads correct, renders broken"
// disease class).
func TestLoadFertilityConfigFailurePathsRenderRule(t *testing.T) {
	t.Run("file-read", func(t *testing.T) {
		_, err := LoadFertilityConfig(filepath.Join(t.TempDir(), "no-such-dir"), "corr")
		if err == nil {
			t.Fatal("expected an error loading fertility.json from a missing directory")
		}
		var e *errs.E
		if !errors.As(err, &e) {
			t.Fatalf("error %v is not a registry-sourced *errs.E", err)
		}
		if e.Code != ErrFertilityDataInvalid {
			t.Fatalf("code = %q, want %q", e.Code, ErrFertilityDataInvalid)
		}
		if strings.Contains(e.Display(), "{rule}") {
			t.Fatalf("Display() = %q renders {rule} literally; want the actual rule", e.Display())
		}
		if !strings.Contains(e.Display(), "must exist and be readable") {
			t.Fatalf("Display() = %q does not name the file-read rule", e.Display())
		}
	})

	t.Run("json-decode", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileFertility), []byte("{not valid json"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := LoadFertilityConfig(dir, "corr")
		if err == nil {
			t.Fatal("expected an error decoding malformed fertility.json")
		}
		var e *errs.E
		if !errors.As(err, &e) {
			t.Fatalf("error %v is not a registry-sourced *errs.E", err)
		}
		if e.Code != ErrFertilityDataInvalid {
			t.Fatalf("code = %q, want %q", e.Code, ErrFertilityDataInvalid)
		}
		if strings.Contains(e.Display(), "{rule}") {
			t.Fatalf("Display() = %q renders {rule} literally; want the actual rule", e.Display())
		}
		if !strings.Contains(e.Display(), "must decode as valid JSON") {
			t.Fatalf("Display() = %q does not name the JSON-decode rule", e.Display())
		}
	})
}

// mkFertilityCouple seeds two cold citizens (id, id+1) at the given
// birthMonth (so both partners share exactly the same age at any given sim
// month — the common case this file's tests exercise), then partners them
// via LifeEventPartner (AC-12's real household-formation path), returning
// the shared household id. childCount, if non-zero, seeds that many REAL
// child cold records into the shared household (both the cold store and the
// household's own Members list, mirroring what a genuine fertility birth
// wires through via birthChildLocked/AddMember) and mirrors the same count
// onto both partners' lineage childCount column. F2 fix (destructive-review
// REJECT on FEAT-160): the household-cap input is the household's ACTUAL
// LIVE membership (see householdChildCountLocked), never a partner's own
// childCount column, so a cap test must seed real members, not merely force
// the column.
func mkFertilityCouple(t *testing.T, api *CitizensAPI, id uint64, birthMonth int64, childCount uint8) (partnerA, partnerB, householdID uint64) {
	t.Helper()
	a := mkRecord(id, 0)
	a.BirthMonth = birthMonth
	a.Household = 0
	a.Partner = 0
	a.ChildCount = 0
	b := mkRecord(id+1, 0)
	b.BirthMonth = birthMonth
	b.Household = 0
	b.Partner = 0
	b.ChildCount = 0
	if err := api.SeedColdRecords([]ColdRecord{a, b}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: id, PartnerID: id + 1}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(id, "corr")
	if !ok {
		t.Fatal("household not formed")
	}
	if childCount > 0 {
		children := make([]ColdRecord, 0, childCount)
		for i := uint8(0); i < childCount; i++ {
			// A namespace disjoint from every id this test file's other
			// helpers use (the couple's own ids, the 100-149 extra
			// population range, and fertilityChildIDBase's organic-birth
			// space), so no seeded child can ever collide.
			childID := 900_000_000 + id*1_000 + uint64(i)
			c := mkRecord(childID, 0)
			c.Household = safeUint32(hh.ID)
			c.Partner = 0
			c.ChildCount = 0
			children = append(children, c)
		}
		if err := api.SeedColdRecords(children, "corr"); err != nil {
			t.Fatalf("SeedColdRecords children: %v", err)
		}
		api.mu.Lock()
		for _, c := range children {
			api.households[hh.ID].AddMember(c.ID)
		}
		// Mirror the same count onto both parents' lineage childCount
		// column, exactly what a real birth's incrementChildCountLocked
		// wires through to (kept internally consistent for any test that
		// also inspects coldChildCount, even though the cap itself now
		// reads live household membership, not this column).
		api.mutateColdLocked(id, func(s *ColdShard, row int) { s.childCount[row] = childCount })
		api.mutateColdLocked(id+1, func(s *ColdShard, row int) { s.childCount[row] = childCount })
		api.mu.Unlock()
	}
	return id, id + 1, hh.ID
}

// coldChildCount is a white-box test accessor into a citizen's cold
// childCount column (the persisted, single-source-of-truth lineage signal a
// birth writes through to — see fertility.go's incrementChildCountLocked).
func coldChildCount(t *testing.T, api *CitizensAPI, id uint64) uint8 {
	t.Helper()
	api.mu.RLock()
	defer api.mu.RUnlock()
	r, ok := api.coldRecord(id)
	if !ok {
		t.Fatalf("citizen %d not found in cold store", id)
	}
	return r.ChildCount
}

// TestFertilityBirthOccursForEligibleCouple (FEAT-160): a partnered couple
// both at peak childbearing age produces a real new citizen within a
// bounded number of months, wired into both parents' lineage (cold
// childCount) and the shared household's membership. seed=2/householdID=1
// (the first household formed in a fresh API)/peak age 28y at month 400
// is a fixed, deterministic (seed, household, month) triple verified (via
// fertility.go's own CoupleBirth/FertilityHazard) to draw a birth at sim
// month 405 — so this test is deterministic, not flaky: it either always
// passes or always fails for this data file's placeholder rates.
func TestFertilityBirthOccursForEligibleCouple(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths // birthMonth so age == 28y at month 400
	parentA, parentB, householdID := mkFertilityCouple(t, api, 10, startMonth, 0)

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	popBefore := api.TotalPopulation("corr")
	const monthsToTry = 6 // verified: the birth draw succeeds at month 405 (offset 5)
	for i := 0; i < monthsToTry; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}

	popAfter := api.TotalPopulation("corr")
	if popAfter != popBefore+1 {
		t.Fatalf("population = %d, want %d (exactly one birth expected within %d months)", popAfter, popBefore+1, monthsToTry)
	}

	childID := fertilityChildIDBase // nextFertilityChildID started at 0
	child, ok := api.CitizenAt(childID, "corr")
	if !ok {
		t.Fatalf("expected child %d to exist", childID)
	}
	if child.BirthMonth != 405 {
		t.Fatalf("child.BirthMonth = %d, want 405", child.BirthMonth)
	}
	if child.Household != householdID {
		t.Fatalf("child.Household = %d, want %d", child.Household, householdID)
	}

	hh, ok := api.Household(householdID, "corr")
	if !ok {
		t.Fatal("household vanished")
	}
	foundChild := false
	for _, m := range hh.Members {
		if m == childID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("household members %v do not include the new child %d", hh.Members, childID)
	}
	if len(hh.Members) != 3 {
		t.Fatalf("household membership = %d, want 3 (2 parents + 1 child)", len(hh.Members))
	}

	// Both parents' lineage signal (cold childCount, the persisted source of
	// truth) reflects the birth — never double-counted, never attributed to
	// only one partner.
	if got := coldChildCount(t, api, parentA); got != 1 {
		t.Fatalf("parentA childCount = %d, want 1", got)
	}
	if got := coldChildCount(t, api, parentB); got != 1 {
		t.Fatalf("parentB childCount = %d, want 1", got)
	}
}

// TestFertilityNoBirthForSingleCitizen (FEAT-160): an unpartnered citizen
// (Partner == 0) is never fertility-eligible, so no birth ever occurs for
// them regardless of age or how many months advance.
func TestFertilityNoBirthForSingleCitizen(t *testing.T) {
	api, err := NewCitizensAPI(5, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	r := mkRecord(20, 0)
	r.BirthMonth = 0 // age == whatever month advances to; irrelevant, no partner
	r.Household = 0
	r.Partner = 0
	r.ChildCount = 0
	if err := api.SeedColdRecords([]ColdRecord{r}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	popBefore := api.TotalPopulation("corr")
	for i := 0; i < 12; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	if got := api.TotalPopulation("corr"); got != popBefore {
		t.Fatalf("population changed for an unpartnered citizen: before=%d after=%d", popBefore, got)
	}
}

// TestFertilityNoBirthOutsideAgeWindow (FEAT-160): a couple both well
// outside the data-sourced childbearing age window (10 years old — below
// data/fertility.json's minChildbearingAgeYears) draws a hazard of exactly
// 0 (FertilityHazard's own directional contract), so CoupleBirth can never
// return true for them — checked directly against the same
// (seed, household, month) triple TestFertilityBirthOccursForEligibleCouple
// proves DOES produce a birth for age-eligible partners, isolating age as
// the deciding factor rather than a coincidental non-draw.
func TestFertilityNoBirthOutsideAgeWindow(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const tooYoungAgeMonths = 10 * 12
	const startMonth = 400 - tooYoungAgeMonths
	_, _, _ = mkFertilityCouple(t, api, 10, startMonth, 0)

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	popBefore := api.TotalPopulation("corr")
	for i := 0; i < 6; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	if got := api.TotalPopulation("corr"); got != popBefore {
		t.Fatalf("population changed for an under-age couple: before=%d after=%d", popBefore, got)
	}
}

// TestFertilityNoBirthAtHouseholdCap (FEAT-160): a couple identical in
// every respect to TestFertilityBirthOccursForEligibleCouple's
// guaranteed-birth setup (same seed/household id/months/peak age), except
// already AT data/fertility.json's maxChildrenPerHousehold cap, produces NO
// birth — isolating the cap as the deciding factor (the same hazard draw
// that DOES succeed for an under-cap couple is proven suppressed here, not
// merely "didn't happen to roll a birth").
func TestFertilityNoBirthAtHouseholdCap(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths
	const capChildren = 4 // data/fertility.json's maxChildrenPerHousehold placeholder
	mkFertilityCouple(t, api, 10, startMonth, capChildren)

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	popBefore := api.TotalPopulation("corr")
	for i := 0; i < 6; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	if got := api.TotalPopulation("corr"); got != popBefore {
		t.Fatalf("population changed for a couple already at the household cap: before=%d after=%d", popBefore, got)
	}
}

// TestFertilityEligibleDirectional (FEAT-160, pure-function unit coverage):
// FertilityEligible's three gates (partnered, age window, household cap)
// each independently flip eligibility, checked directly rather than through
// a probabilistic integration run.
func TestFertilityEligibleDirectional(t *testing.T) {
	cfg, err := LoadDefaultFertilityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultFertilityConfig: %v", err)
	}
	const peakMonths = 28 * 12

	if FertilityEligible(1, 0, 0, peakMonths, peakMonths, cfg) {
		t.Fatal("partner==0 must never be eligible")
	}
	if FertilityEligible(1, 1, 0, peakMonths, peakMonths, cfg) {
		t.Fatal("self-partnered must never be eligible")
	}
	if !FertilityEligible(1, 2, 0, peakMonths, peakMonths, cfg) {
		t.Fatal("a partnered, in-window, under-cap couple must be eligible")
	}
	if FertilityEligible(1, 2, 0, 10*12, peakMonths, cfg) {
		t.Fatal("a partner below the childbearing age window must not be eligible")
	}
	if FertilityEligible(1, 2, 0, peakMonths, 90*12, cfg) {
		t.Fatal("a partner above the childbearing age window must not be eligible")
	}
	cap := int(cfg.Params.MaxChildrenPerHousehold.Value)
	if FertilityEligible(1, 2, cap, peakMonths, peakMonths, cfg) {
		t.Fatal("a couple at the household cap must not be eligible")
	}
}

// TestFertilityHazardDirectional (FEAT-160): mirrors mortality_test.go's
// TestMortalityHazardClamped/TestMortalityHazardIncreasesWithAge style —
// directional-only checks against FertilityHazard, never a pinned
// magnitude (the balance-number regime).
func TestFertilityHazardDirectional(t *testing.T) {
	cfg, err := LoadDefaultFertilityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultFertilityConfig: %v", err)
	}
	const peakMonths = 28 * 12
	const tooYoungMonths = 10 * 12
	const tooOldMonths = 90 * 12

	peak := FertilityHazard(peakMonths, peakMonths, cfg)
	if peak <= 0 {
		t.Fatalf("hazard at peak age for both partners must be positive, got %g", peak)
	}
	if h := FertilityHazard(tooYoungMonths, peakMonths, cfg); h != 0 {
		t.Fatalf("hazard with one partner below the window must be exactly 0, got %g", h)
	}
	if h := FertilityHazard(peakMonths, tooOldMonths, cfg); h != 0 {
		t.Fatalf("hazard with one partner above the window must be exactly 0, got %g", h)
	}
	// Never exceeds [0,1] even at extreme ages (GR#16 clamping discipline).
	for _, age := range []int64{0, 12 * 500, -12 * 500} {
		h := FertilityHazard(age, peakMonths, cfg)
		if h < 0 || h > 1 {
			t.Fatalf("hazard %g out of [0,1] at age %d", h, age)
		}
	}
}

// TestFertilityDeterministic (GR#21): two independently constructed
// CitizensAPI instances, given the identical seed and command sequence,
// produce byte-identical population state (PopulationHash) and the same
// total population after the same number of months — including the same
// fertility-driven births at the same months with the same ids.
func TestFertilityDeterministic(t *testing.T) {
	run := func() (*CitizensAPI, [32]byte) {
		api, err := NewCitizensAPI(2, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		const peakAgeMonths = 28 * 12
		const startMonth = 400 - peakAgeMonths
		mkFertilityCouple(t, api, 10, startMonth, 0)
		api.mu.Lock()
		api.month = 400
		api.mu.Unlock()
		for i := 0; i < 8; i++ {
			if err := api.AdvanceMonth("corr"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api, api.PopulationHash("corr")
	}

	apiA, hashA := run()
	apiB, hashB := run()

	if hashA != hashB {
		t.Fatalf("PopulationHash differs across identical runs: %x vs %x", hashA, hashB)
	}
	if popA, popB := apiA.TotalPopulation("corr"), apiB.TotalPopulation("corr"); popA != popB {
		t.Fatalf("TotalPopulation differs across identical runs: %d vs %d", popA, popB)
	}
	childID := fertilityChildIDBase
	childA, okA := apiA.CitizenAt(childID, "corr")
	childB, okB := apiB.CitizenAt(childID, "corr")
	if okA != okB {
		t.Fatalf("child presence differs across identical runs: %v vs %v", okA, okB)
	}
	if okA && childA.BirthMonth != childB.BirthMonth {
		t.Fatalf("child birth month differs across identical runs: %d vs %d", childA.BirthMonth, childB.BirthMonth)
	}
}

// TestFertilityConservation (§14/US-1 people-conservation identity,
// invariant/people.go's Closing - Opening == TrackedDelta): every advanced
// month, TotalPopulation's actual change equals VitalEvents' own
// births-minus-deaths for that month exactly — the citizens-package-level
// proof that a fertility birth flows through the SAME tracked-delta
// accounting mortality's deaths already report, so nothing appears or
// vanishes uncounted.
func TestFertilityConservation(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths
	mkFertilityCouple(t, api, 10, startMonth, 0)
	// A second, larger population sampled across a wider age spread so
	// mortality has real (if small) probability too — the conservation
	// identity must hold whether or not any deaths actually occur.
	var extra []ColdRecord
	for id := uint64(100); id < 150; id++ {
		r := mkRecord(id, 0)
		r.BirthMonth = 400 - int64(id%900) // spread of ages, some old
		r.Household = 0
		r.Partner = 0
		extra = append(extra, r)
	}
	if err := api.SeedColdRecords(extra, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	for i := 0; i < 10; i++ {
		before := api.TotalPopulation("corr")
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
		after := api.TotalPopulation("corr")
		births, deaths := api.VitalEvents("corr")
		want := before + births - deaths
		if after != want {
			t.Fatalf("month %d: population conservation violated: before=%d births=%d deaths=%d after=%d want=%d",
				i, before, births, deaths, after, want)
		}
	}
}

// TestFertilityEligibleIgnoresStaleChildCountAcrossRepartnering (F2
// regression, destructive-review REJECT on FEAT-160): pre-fix,
// applyFertilityLocked's cap check read the ACTING (lower-id) partner's own
// lifetime cold childCount column as a proxy for the household's real child
// count. That proxy is false after re-partnering: LifeEventPartner lets a
// citizen with children from a dissolved prior relationship join a fresh
// household without resetting their lifetime childCount, so a couple's
// eligibility ended up depending on which partner happened to have the
// lower id -- purely incidental, never a real household property. This test
// builds two structurally-identical fresh households (0 REAL children in
// either), differing only in which partner (lower- or higher-id) carries a
// stale AT-CAP lifetime childCount from a prior relationship, and asserts:
// (a) the household's actual live child count is 0 for both (the F2 fix's
// householdChildCountLocked, never either partner's own childCount column),
// and (b) both households are equally fertility-eligible.
func TestFertilityEligibleIgnoresStaleChildCountAcrossRepartnering(t *testing.T) {
	cfg, err := LoadDefaultFertilityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultFertilityConfig: %v", err)
	}
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths
	cap := int(cfg.Params.MaxChildrenPerHousehold.Value)
	if cap <= 0 {
		t.Fatalf("data/fertility.json maxChildrenPerHousehold must be positive for this test to be meaningful, got %d", cap)
	}

	// Household A: the LOWER-id partner (10) carries a stale AT-CAP lifetime
	// childCount from a dissolved prior relationship; the household itself
	// is a fresh pairing with 0 real children.
	partnerA1, partnerA2, hhA := mkFertilityCouple(t, api, 10, startMonth, 0)
	api.mu.Lock()
	api.mutateColdLocked(partnerA1, func(s *ColdShard, row int) { s.childCount[row] = uint8(cap) })
	api.mu.Unlock()

	// Household B: structurally identical, except the SAME stale AT-CAP
	// count sits on the HIGHER-id partner instead -- the only difference is
	// which partner (by id ordering) carries the history.
	partnerB1, partnerB2, hhB := mkFertilityCouple(t, api, 20, startMonth, 0)
	api.mu.Lock()
	api.mutateColdLocked(partnerB2, func(s *ColdShard, row int) { s.childCount[row] = uint8(cap) })
	api.mu.Unlock()

	api.mu.RLock()
	childCountA := api.householdChildCountLocked(hhA, partnerA1, partnerA2, 400)
	childCountB := api.householdChildCountLocked(hhB, partnerB1, partnerB2, 400)
	api.mu.RUnlock()

	if childCountA != 0 || childCountB != 0 {
		t.Fatalf("household child counts must be 0 for freshly-paired childless households regardless of stale lifetime childCount: A=%d B=%d", childCountA, childCountB)
	}

	elA := FertilityEligible(partnerA1, partnerA2, childCountA, peakAgeMonths, peakAgeMonths, cfg)
	elB := FertilityEligible(partnerB1, partnerB2, childCountB, peakAgeMonths, peakAgeMonths, cfg)
	if !elA || !elB {
		t.Fatalf("structurally identical fresh households must be equally fertility-eligible regardless of id ordering or which partner carries a stale lifetime childCount: eligibleA=%v eligibleB=%v", elA, elB)
	}
}

// TestHouseholdChildCountIgnoresAdultNonPartnerMember (round-3 hardening,
// secondary finding on householdChildCountLocked): the original
// implementation counted ANY household member that was not one of the two
// current partners as a child, with no age/adult check -- "not a partner"
// is not the same predicate as "is a child". This builds a household with
// an ADULT non-partner member present (e.g. the shape the round-3 P1
// household-leak bug could produce, or a future grown child who never
// left) alongside one genuine young child, and asserts the adult is NOT
// counted toward the fertility cap while the genuine child IS.
func TestHouseholdChildCountIgnoresAdultNonPartnerMember(t *testing.T) {
	cfg, err := LoadDefaultFertilityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultFertilityConfig: %v", err)
	}
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const month = 400
	const startMonth = month - peakAgeMonths
	partnerA, partnerB, hh := mkFertilityCouple(t, api, 30, startMonth, 0)

	// A genuine young child (age 0 at `month`): must count.
	child := mkRecord(9001, 0)
	child.Household = safeUint32(hh)
	child.Partner = 0
	child.BirthMonth = month

	// An ADULT non-partner member (e.g. a leaked prior-household member, or
	// a grown child): born well before the adult threshold, so at `month`
	// they are firmly an adult -- must NOT count.
	minAdultAgeMonths := int64(cfg.Params.MinChildbearingAgeYears.Value * 12)
	adultMember := mkRecord(9002, 0)
	adultMember.Household = safeUint32(hh)
	adultMember.Partner = 0
	adultMember.BirthMonth = month - minAdultAgeMonths - 12*12 // well past the adult line

	if err := api.SeedColdRecords([]ColdRecord{child, adultMember}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.households[hh].AddMember(child.ID)
	api.households[hh].AddMember(adultMember.ID)
	api.month = month
	count := api.householdChildCountLocked(hh, partnerA, partnerB, month)
	api.mu.Unlock()

	if count != 1 {
		t.Fatalf("householdChildCountLocked = %d, want 1 (only the genuine young child, never the adult non-partner member)", count)
	}
}
