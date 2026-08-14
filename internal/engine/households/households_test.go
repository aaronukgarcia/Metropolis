package households

import (
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- fixtures -------------------------------------------------------------

// hsEntry builds a minimal HS catalogue entry for NewFromBuildings fixtures
// (NewFromBuildings does not re-validate, so only the fields loadTypologies
// reads need be set).
func hsEntry(id string, tags ...string) data.BuildingEntry {
	return data.BuildingEntry{
		ID:               id,
		Name:             id,
		CatalogueSection: "HS",
		AppealProfile:    tags,
	}
}

// testCatalogue is the small HS fixture the AC tests run against. It carries
// the typologies the spec's own examples name (terrace, bungalow,
// penthouse_tower) so the AC-4/AC-6 scenarios map onto the real §21 shapes.
func testCatalogue() data.Buildings {
	return data.Buildings{Entries: []data.BuildingEntry{
		hsEntry("terrace", "community", "families"),
		hsEntry("bungalow", "retirees", "coastal"),
		hsEntry("penthouse_tower", "novelty", "wealth"),
		hsEntry("semi_detached", "families"),
	}}
}

// personality returns a citizens.Personality with the named axes set and the
// rest zero — the direct-profile input to AppealOf (AC-4).
func personality(novelty, community int32) citizens.Personality {
	var p citizens.Personality
	p[citizens.AxisNovelty] = novelty
	p[citizens.AxisCommunity] = community
	return p
}

// mkCitizen builds a valid cold citizen record with a specific employment
// state, wealth, and novelty/community personality (Baseline One test
// fixture — the cold store is seeded directly).
func mkCitizen(id uint64, emp citizens.EmploymentState, wealth int64, novelty, community int8) citizens.ColdRecord {
	var p [citizens.NumPersonalityAxes]int8
	p[citizens.AxisNovelty] = novelty
	p[citizens.AxisCommunity] = community
	return citizens.ColdRecord{
		ID:              id,
		BirthMonth:      0,
		Sex:             citizens.SexFemale,
		Personality:     p,
		Wealth:          wealth,
		EmploymentState: emp,
		Sector:          citizens.SectorNone,
		HealthBand:      citizens.HealthGood,
		Stage:           citizens.StageAdultEd,
	}
}

// newCitizensAPI seeds the given records into a fresh CitizensAPI.
func newCitizensAPI(t *testing.T, records ...citizens.ColdRecord) *citizens.CitizensAPI {
	t.Helper()
	api, err := citizens.NewCitizensAPI(7, "corr-households")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if len(records) > 0 {
		if err := api.SeedColdRecords(records, "corr-households"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
	}
	return api
}

// partnerCouple seeds two citizens and partners them into a household,
// returning the household id (engine.citizens' own formation — ASM-247).
func partnerCouple(t *testing.T, api *citizens.CitizensAPI, a, b citizens.ColdRecord) uint64 {
	t.Helper()
	if err := api.SeedColdRecords([]citizens.ColdRecord{a, b}, "corr-households"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: "corr-households",
		Kind:          citizens.LifeEventPartner,
		CitizenID:     a.ID,
		PartnerID:     b.ID,
	}); err != nil {
		t.Fatalf("LifeEventPartner: %v", err)
	}
	hh, ok := api.HouseholdOf(a.ID, "corr-households")
	if !ok {
		t.Fatalf("household not formed for citizen %d", a.ID)
	}
	return hh.ID
}

// newAPI builds a HouseholdsAPI over a catalogue, wired to citizens.
func newAPI(t *testing.T, b data.Buildings, ca *citizens.CitizensAPI) *HouseholdsAPI {
	t.Helper()
	api, err := NewFromBuildings(b, "corr-households")
	if err != nil {
		t.Fatalf("NewFromBuildings: %v", err)
	}
	if ca != nil {
		if err := api.SetCitizens(ca); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
	}
	return api
}

// isErr asserts err is a registry error with the given code (GR#7 — the
// code, not merely that a same-named test function exists, per BUG-100).
func isErr(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected registry error %s, got nil", code)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected error code %s, got %s", code, e.Code)
	}
}

// --- AC-2: membership is read from CitizensAPI, never re-implemented ------

func TestMembershipConsistentWithCitizensAPI(t *testing.T) {
	ca := newCitizensAPI(t)
	h := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentEmployed, 0, 0, 0))
	api := newAPI(t, testCatalogue(), ca)

	got, err := api.MembersOf(h)
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	theirs, ok := ca.Household(h, "corr-households")
	if !ok {
		t.Fatalf("CitizensAPI lost household %d", h)
	}
	if len(got) != len(theirs.Members) {
		t.Fatalf("member list length = %d, CitizensAPI reports %d", len(got), len(theirs.Members))
	}
	gotSet := map[uint64]bool{}
	for _, m := range got {
		gotSet[m] = true
	}
	for _, m := range theirs.Members {
		if !gotSet[m] {
			t.Fatalf("HouseholdsAPI member list %v missing citizen %d reported by CitizensAPI", got, m)
		}
	}
}

// --- AC-3: typology count is derived from data, not a hardcoded 17 -------

func TestTypologyCountDerivedFromData(t *testing.T) {
	eighteen := make([]data.BuildingEntry, 0, 18)
	for i := 0; i < 18; i++ {
		eighteen = append(eighteen, hsEntry("hs_"+string(rune('a'+i)), "families"))
	}
	api18, err := NewFromBuildings(data.Buildings{Entries: eighteen}, "corr-count")
	if err != nil {
		t.Fatalf("NewFromBuildings(18): %v", err)
	}
	if got := api18.TypologyCount(); got != 18 {
		t.Fatalf("TypologyCount() = %d, want 18 (data-derived, not a hardcoded 17)", got)
	}

	five := []data.BuildingEntry{
		hsEntry("a", "families"), hsEntry("b", "retirees"), hsEntry("c", "students"),
		hsEntry("d", "novelty"), hsEntry("e", "wealth"),
	}
	api5, err := NewFromBuildings(data.Buildings{Entries: five}, "corr-count")
	if err != nil {
		t.Fatalf("NewFromBuildings(5): %v", err)
	}
	if got := api5.TypologyCount(); got != 5 {
		t.Fatalf("TypologyCount() = %d, want 5", got)
	}
}

// --- AC-4: appeal differs across segments for the SAME typology ----------

func TestAppealDifferentiatedBySegment(t *testing.T) {
	api := newAPI(t, testCatalogue(), nil)

	noveltySeeking := HouseholdProfile{Stage: LifeStageOther, Wealth: 0, Personality: personality(100, 0)}
	communityMinded := HouseholdProfile{Stage: LifeStageOther, Wealth: 0, Personality: personality(0, 100)}

	n, err := api.AppealOf("penthouse_tower", noveltySeeking)
	if err != nil {
		t.Fatalf("AppealOf penthouse/novelty: %v", err)
	}
	c, err := api.AppealOf("penthouse_tower", communityMinded)
	if err != nil {
		t.Fatalf("AppealOf penthouse/community: %v", err)
	}
	if n.Value <= c.Value {
		t.Fatalf("penthouse_tower should appeal more to novelty-seekers than community-minded: %d <= %d", n.Value, c.Value)
	}

	retiree := HouseholdProfile{Stage: LifeStageRetired, Wealth: 0}
	young := HouseholdProfile{Stage: LifeStageYoungSingle, Wealth: 0}
	r, err := api.AppealOf("bungalow", retiree)
	if err != nil {
		t.Fatalf("AppealOf bungalow/retiree: %v", err)
	}
	y, err := api.AppealOf("bungalow", young)
	if err != nil {
		t.Fatalf("AppealOf bungalow/young: %v", err)
	}
	if r.Value <= y.Value {
		t.Fatalf("bungalow should appeal more to a retiree than a young single: %d <= %d", r.Value, y.Value)
	}
}

// --- AC-5: demand is independent of the built-stock mix ------------------

func TestDemandIndependentOfStock(t *testing.T) {
	ca := newCitizensAPI(t)
	h1 := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentRetired, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentRetired, 0, 0, 0))
	h2 := partnerCouple(t, ca,
		mkCitizen(3, citizens.EmploymentEmployed, 0, 100, 0),
		mkCitizen(4, citizens.EmploymentEmployed, 0, 100, 0))
	api := newAPI(t, testCatalogue(), ca)
	ids := []uint64{h1, h2}

	// Stock mix A: all terraces.
	if err := api.ReportStock(StockCommand{TypologyID: "terrace", Count: 100}); err != nil {
		t.Fatalf("ReportStock A: %v", err)
	}
	dA, err := api.DemandByType(ids)
	if err != nil {
		t.Fatalf("DemandByType A: %v", err)
	}

	// Stock mix B: all bungalows — population composition unchanged.
	if err := api.ReportStock(StockCommand{TypologyID: "terrace", Count: 0}); err != nil {
		t.Fatalf("ReportStock clear: %v", err)
	}
	if err := api.ReportStock(StockCommand{TypologyID: "bungalow", Count: 100}); err != nil {
		t.Fatalf("ReportStock B: %v", err)
	}
	dB, err := api.DemandByType(ids)
	if err != nil {
		t.Fatalf("DemandByType B: %v", err)
	}

	if !distributionsEqual(dA, dB) {
		t.Fatalf("demand must be independent of stock mix: A=%+v B=%+v", dA, dB)
	}
	if dA.Total != int64(len(ids)) {
		t.Fatalf("demand total = %d, want %d (sums to household count)", dA.Total, len(ids))
	}
}

func distributionsEqual(a, b DemandDistribution) bool {
	if a.Total != b.Total || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		if a.Entries[i] != b.Entries[i] {
			return false
		}
	}
	return true
}

// --- AC-6: unhoused-by-preference is distinct from raw vacancy -----------

func TestUnhousedByPreference(t *testing.T) {
	ca := newCitizensAPI(t)
	h1 := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentRetired, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentRetired, 0, 0, 0)) // prefers bungalow
	h2 := partnerCouple(t, ca,
		mkCitizen(3, citizens.EmploymentEmployed, 0, 100, 0),
		mkCitizen(4, citizens.EmploymentEmployed, 0, 100, 0)) // prefers penthouse_tower
	api := newAPI(t, testCatalogue(), ca)

	// 100% single-typology stock (terraces) at positive citywide vacancy:
	// 100 terraces for 2 households. The retiree and novelty segments'
	// top-preference typologies (bungalow, tower) are entirely absent.
	if err := api.ReportStock(StockCommand{TypologyID: "terrace", Count: 100}); err != nil {
		t.Fatalf("ReportStock: %v", err)
	}

	unhoused, err := api.UnhousedByPreference([]uint64{h1, h2})
	if err != nil {
		t.Fatalf("UnhousedByPreference: %v", err)
	}
	if unhoused <= 0 {
		t.Fatalf("UnhousedByPreference() = %d, want > 0 in an all-terrace city with retiree/novelty segments", unhoused)
	}
}

// --- AC-7: overcrowding + rent burden ------------------------------------

func TestOvercrowding(t *testing.T) {
	// The derivation boundary (what OvercrowdingOf applies to a household):
	// occupants > capacity ⇒ overcrowded; under capacity ⇒ not.
	over := overcrowdingFrom(citizens.Household{ID: 1, Members: []uint64{1, 2, 3}, DwellingRooms: 2})
	if !over.Overcrowded {
		t.Fatal("3 members in 2 rooms must be overcrowded")
	}
	if over.Occupants != 3 || over.Capacity != 2 {
		t.Fatalf("occupancy/capacity = %d/%d, want 3/2", over.Occupants, over.Capacity)
	}
	under := overcrowdingFrom(citizens.Household{ID: 2, Members: []uint64{1, 2}, DwellingRooms: 4})
	if under.Overcrowded {
		t.Fatal("2 members in 4 rooms must not be overcrowded")
	}

	// The method path: a formed household delegates to CitizensAPI and an
	// unknown id returns a registry error (AC-10), never a silent zero.
	ca := newCitizensAPI(t)
	h := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentEmployed, 0, 0, 0))
	api := newAPI(t, testCatalogue(), ca)
	got, err := api.OvercrowdingOf(h)
	if err != nil {
		t.Fatalf("OvercrowdingOf: %v", err)
	}
	if got.Overcrowded || got.Occupants != 2 || got.Capacity != 2 {
		t.Fatalf("couple household = %+v, want {false 2 2}", got)
	}
	_, err = api.OvercrowdingOf(999)
	isErr(t, err, ErrUnknownHousehold)
}

func TestRentBurden(t *testing.T) {
	ca := newCitizensAPI(t)
	h := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentEmployed, 0, 0, 0))
	api := newAPI(t, testCatalogue(), ca)

	// rent/income = 0.40 > 0.35 ⇒ burdened.
	burdened, err := api.RentBurdenOf(h, 40, 100)
	if err != nil {
		t.Fatalf("RentBurdenOf burdened: %v", err)
	}
	if !burdened.Burdened {
		t.Fatal("rent/income 0.40 must be rent-burdened (§18 > 35%)")
	}
	// rent/income = 0.30 < 0.35 ⇒ not burdened.
	clear, err := api.RentBurdenOf(h, 30, 100)
	if err != nil {
		t.Fatalf("RentBurdenOf clear: %v", err)
	}
	if clear.Burdened {
		t.Fatal("rent/income 0.30 must not be rent-burdened")
	}
	if clear.Ratio <= 0 || math.IsNaN(clear.Ratio) || math.IsInf(clear.Ratio, 0) {
		t.Fatalf("ratio must be a finite positive figure, got %v", clear.Ratio)
	}
	// zero income ⇒ the citizens sentinel above the threshold.
	noIncome, err := api.RentBurdenOf(h, 1, 0)
	if err != nil {
		t.Fatalf("RentBurdenOf no income: %v", err)
	}
	if !noIncome.Burdened {
		t.Fatal("zero income must read as rent-burdened (citizens sentinel), not NaN")
	}
}

// --- AC-8: dwelling-size preference varies with wealth -------------------

func TestDwellingSizePrefVariesByWealth(t *testing.T) {
	ca := newCitizensAPI(t)
	// Two households differing ONLY in wealth band (same size/personality).
	poor := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentEmployed, 0, 0, 0))
	rich := partnerCouple(t, ca,
		mkCitizen(3, citizens.EmploymentEmployed, 120_000_000_000, 0, 0),
		mkCitizen(4, citizens.EmploymentEmployed, 120_000_000_000, 0, 0))
	api := newAPI(t, testCatalogue(), ca)

	poorClass, err := api.DwellingSizePref(poor)
	if err != nil {
		t.Fatalf("DwellingSizePref poor: %v", err)
	}
	richClass, err := api.DwellingSizePref(rich)
	if err != nil {
		t.Fatalf("DwellingSizePref rich: %v", err)
	}
	if poorClass == richClass {
		t.Fatalf("dwelling-size preference must differ by wealth band, both = %d", poorClass)
	}
	if richClass < poorClass {
		t.Fatalf("richer household should seek a larger dwelling: rich=%d poor=%d", richClass, poorClass)
	}
}

// --- AC-10: registry-sourced errors, no silent zero ----------------------

func TestInvalidHouseholdReturnsRegistryError(t *testing.T) {
	ca := newCitizensAPI(t)
	api := newAPI(t, testCatalogue(), ca)

	members, err := api.MembersOf(999)
	if err == nil {
		t.Fatal("MembersOf(unknown) must return an error")
	}
	isErr(t, err, ErrUnknownHousehold)
	if members != nil {
		t.Fatalf("MembersOf(unknown) must not return a silently-zeroed member list, got %v", members)
	}
	// No record was created: querying the same id again yields the same error.
	if _, err2 := api.MembersOf(999); !errors.Is(err2, &errs.E{Code: ErrUnknownHousehold}) {
		t.Fatalf("second MembersOf(unknown) error = %v, want ErrUnknownHousehold", err2)
	}
}

func TestUnknownTypologyReturnsRegistryError(t *testing.T) {
	api := newAPI(t, testCatalogue(), nil)

	score, err := api.AppealOf("nonexistent", HouseholdProfile{Stage: LifeStageOther})
	isErr(t, err, ErrUnknownTypology)
	if score.Fallback {
		t.Fatal("an unknown typology must error, not degrade to the neutral-appeal fallback")
	}
	if score.Value != 0 {
		t.Fatalf("unknown typology must not return a silently-zeroed-but-nonzero appeal, got %d", score.Value)
	}

	if err := api.ReportStock(StockCommand{TypologyID: "nonexistent", Count: 5}); !errors.Is(err, &errs.E{Code: ErrUnknownTypology}) {
		t.Fatalf("ReportStock(unknown) error = %v, want ErrUnknownTypology", err)
	}
}

// --- AC-11: empty/unrecognised appealProfile → neutral fallback ----------

func TestEmptyAppealProfileFallback(t *testing.T) {
	b := data.Buildings{Entries: []data.BuildingEntry{
		hsEntry("retirees_only", "retirees"),
		{ID: "empty", Name: "empty", CatalogueSection: "HS", AppealProfile: nil},
		{ID: "unknown", Name: "unknown", CatalogueSection: "HS", AppealProfile: []string{"mystery-tag"}},
	}}
	api := newAPI(t, b, nil)
	young := HouseholdProfile{Stage: LifeStageYoungSingle, Wealth: 0}

	// A real zero (recognised tag, non-matching stage) is NOT a fallback.
	realZero, err := api.AppealOf("retirees_only", young)
	if err != nil {
		t.Fatalf("AppealOf retirees_only: %v", err)
	}
	if realZero.Fallback {
		t.Fatal("a recognised tag with a non-matching stage must not be a fallback")
	}
	if realZero.Value != 0 {
		t.Fatalf("non-matching retirees tag should score 0, got %d", realZero.Value)
	}

	// Empty appealProfile → fallback, distinguishable from the real zero.
	empty, err := api.AppealOf("empty", young)
	if err != nil {
		t.Fatalf("AppealOf empty: %v", err)
	}
	if !empty.Fallback {
		t.Fatal("empty appealProfile must degrade to the neutral-appeal fallback")
	}
	if empty.Value != 0 {
		t.Fatalf("fallback appeal must be neutral zero, got %d", empty.Value)
	}

	// Unrecognised tag array → fallback too.
	unknown, err := api.AppealOf("unknown", young)
	if err != nil {
		t.Fatalf("AppealOf unknown: %v", err)
	}
	if !unknown.Fallback {
		t.Fatal("an unrecognised tag array must degrade to the neutral-appeal fallback")
	}
}

// --- conservation invariant: no orphan, no double-housing ----------------

func TestHouseholdConservationInvariant(t *testing.T) {
	ca := newCitizensAPI(t)
	h1 := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, 0, 0, 0),
		mkCitizen(2, citizens.EmploymentEmployed, 0, 0, 0))
	h2 := partnerCouple(t, ca,
		mkCitizen(3, citizens.EmploymentRetired, 0, 0, 0),
		mkCitizen(4, citizens.EmploymentRetired, 0, 0, 0))
	h3 := partnerCouple(t, ca,
		mkCitizen(5, citizens.EmploymentStudent, 0, 100, 0),
		mkCitizen(6, citizens.EmploymentStudent, 0, 100, 0))
	api := newAPI(t, testCatalogue(), ca)

	households := []uint64{h1, h2, h3}
	seen := map[uint64]uint64{} // citizen → household
	for _, h := range households {
		theirs, ok := ca.Household(h, "corr-households")
		if !ok {
			t.Fatalf("CitizensAPI lost household %d", h)
		}
		// Cross-API membership agreement (HouseholdsAPI mirrors CitizensAPI).
		mine, err := api.MembersOf(h)
		if err != nil {
			t.Fatalf("MembersOf(%d): %v", h, err)
		}
		if len(mine) != len(theirs.Members) {
			t.Fatalf("household %d: HouseholdsAPI members %v != CitizensAPI %v", h, mine, theirs.Members)
		}
		for _, m := range theirs.Members {
			// No double-housing: each citizen belongs to exactly one household.
			if prev, dup := seen[m]; dup {
				t.Fatalf("citizen %d double-housed in households %d and %d", m, prev, h)
			}
			seen[m] = h
			// No orphan: the citizen's own Household field points back.
			cit, ok := ca.CitizenAt(m, "corr-households")
			if !ok {
				t.Fatalf("citizen %d in household %d is missing (orphaned)", m, h)
			}
			if cit.Household != h {
				t.Fatalf("citizen %d points to household %d, want %d (orphaned)", m, cit.Household, h)
			}
		}
	}
	// Every citizen is housed exactly once: |seen| == total citizens.
	if len(seen) != 6 {
		t.Fatalf("expected 6 distinct housed citizens, got %d", len(seen))
	}
}

// --- numeric safety (FEAT-086): ±MaxInt64 and mixed signs never wrap -----

func TestNumericSafetyExtremes(t *testing.T) {
	ca := newCitizensAPI(t)
	h := partnerCouple(t, ca,
		mkCitizen(1, citizens.EmploymentEmployed, math.MaxInt64, 100, 0),
		mkCitizen(2, citizens.EmploymentEmployed, math.MaxInt64, 100, 0))
	api := newAPI(t, testCatalogue(), ca)

	// MaxInt64 stock is accepted and never wraps the unhoused computation.
	if err := api.ReportStock(StockCommand{TypologyID: "terrace", Count: math.MaxInt64}); err != nil {
		t.Fatalf("ReportStock(MaxInt64): %v", err)
	}
	unhoused, err := api.UnhousedByPreference([]uint64{h})
	if err != nil {
		t.Fatalf("UnhousedByPreference(MaxInt64 stock): %v", err)
	}
	if unhoused < 0 {
		t.Fatalf("unhoused wrapped negative: %d", unhoused)
	}

	// MaxInt64 rent vs income 1: finite ratio, burdened, never +Inf/NaN.
	rb, err := api.RentBurdenOf(h, math.MaxInt64, 1)
	if err != nil {
		t.Fatalf("RentBurdenOf(MaxInt64 rent): %v", err)
	}
	if !rb.Burdened {
		t.Fatal("MaxInt64 rent over 1 income must be burdened")
	}
	if math.IsNaN(rb.Ratio) || math.IsInf(rb.Ratio, 0) {
		t.Fatalf("ratio must be finite, got %v", rb.Ratio)
	}

	// Negative rent/income are rejected, never wrapped.
	if _, err := api.RentBurdenOf(h, -1, 100); !errors.Is(err, &errs.E{Code: ErrInvalidAmount}) {
		t.Fatalf("negative rent error = %v, want ErrInvalidAmount", err)
	}
	if _, err := api.RentBurdenOf(h, 100, -1); !errors.Is(err, &errs.E{Code: ErrInvalidAmount}) {
		t.Fatalf("negative income error = %v, want ErrInvalidAmount", err)
	}
	if err := api.ReportStock(StockCommand{TypologyID: "terrace", Count: -1}); !errors.Is(err, &errs.E{Code: ErrInvalidStock}) {
		t.Fatalf("negative stock error = %v, want ErrInvalidStock", err)
	}

	// MaxInt64 wealth: wealth band clamps to the top band, appeal stays finite.
	s, err := api.AppealOf("penthouse_tower", HouseholdProfile{
		Stage:       LifeStageOther,
		Wealth:      math.MaxInt64,
		Personality: personality(100, 0),
	})
	if err != nil {
		t.Fatalf("AppealOf(MaxInt64 wealth): %v", err)
	}
	if s.Value < 0 {
		t.Fatalf("appeal wrapped negative: %d", s.Value)
	}
}
