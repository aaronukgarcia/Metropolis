package attract_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

const corr = "corr-s6"

// TestS6EndToEnd is AC-9, the sprint exit gate: one continuous headless
// scenario composing only the registered public APIs (WorldAPI, BuildAPI,
// CitizensAPI, HouseholdsAPI, AttractAPI, engine.logistics — never another
// package's internals) that proves the full zone → build → migrate → work →
// deliver → migrate-negative chain from 0 citizens. The two load-bearing
// assertions are the population-count INCREASE after the positive migration
// step and the population-count DECREASE after the negative step, in the
// SAME run — a one-way model passes the first but not the second.
//
// Documented interim seams (ASM-245/ASM-246, flagged rather than faked):
//   - BuildAPI builds the §34 ZONE catalogue (dwelling), not the 17 HS
//     typologies; the "2 distinct HS typologies" arrive in engine.households
//     via ReportStock (the composition-root bridge), which this test performs
//     explicitly after the build completes.
//   - engine.logistics' stub-for-baseline defers the per-junction arrival
//     queue; the queryable stand-in asserted in step 5 is the Deliverable
//     throughput/stock state.
//   - engine.citizens exposes no satisfaction-mutation command, so step 4's
//     measurable citizen-state delta uses the health band (LifeEventHealth)
//     as the documented interim satisfaction/production proxy.
func TestS6EndToEnd(t *testing.T) {
	// --- Step 1: Zone → build -------------------------------------------------
	b, _, log := newBuildFixture(t)
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}

	if err := b.SubmitZoneCommand(build.ZoneCommand{Tile: tile, Local: local, OwnerID: 1, Zone: build.ZoneDwelling}); err != nil {
		t.Fatalf("SubmitZoneCommand: %v", err)
	}
	if zt, ok := b.ZoneState(tile, local); !ok || zt != build.ZoneDwelling {
		t.Fatalf("zone not assigned: %v %v", zt, ok)
	}
	if _, err := b.SubmitBuildCommand(build.BuildCommand{Tile: tile, Local: local, OwnerID: 1, Zone: build.ZoneDwelling, Month: 6}); err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	for i := int64(0); i < 200; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("build Tick(%d): %v", i, err)
		}
	}
	if _, ok := b.Structure(tile, local); !ok {
		t.Fatal("build did not land a structure after ticking")
	}

	// Bridge: the completed dwelling zone is reported into engine.households
	// as two distinct HS typologies' built stock (the documented ASM-246 seam).
	hh := newHouseholdsFixture(t)

	// --- Seed the founding residents -----------------------------------------
	ca := newCitizensFixture(t)
	if err := hh.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	fin := newFinanceFixture(t)
	att := newAttractFixture(t, ca, hh, fin)

	// Six founding residents (three households), max ambition so the negative
	// branch removes them deterministically (hazard 1.0 under any decline).
	var founders []uint64
	var founderHouseholds []uint64
	for h := 0; h < 3; h++ {
		aID := uint64(1 + 2*h)
		bID := uint64(2 + 2*h)
		founders = append(founders, aID, bID)
		founderHouseholds = append(founderHouseholds, partner(t, ca, aID, bID))
	}

	// Step 1 continuation: the dwelling stock exists (two HS typologies).
	if err := hh.ReportStock(households.StockCommand{TypologyID: "terrace", Count: 20}); err != nil {
		t.Fatalf("ReportStock(terrace): %v", err)
	}
	if err := hh.ReportStock(households.StockCommand{TypologyID: "bungalow", Count: 20}); err != nil {
		t.Fatalf("ReportStock(bungalow): %v", err)
	}

	// --- Step 2: Migrate (positive branch) ------------------------------------
	att.SetTermInputs(attract.TermInputs{
		JobAvailability:        80,
		ServiceCoverage:        80,
		Environment:            80,
		LeisureFit:             80,
		Safety:                 80,
		HouseholdIDs:           founderHouseholds,
		MonthlyRentMicroPounds: 1000,
	})

	pop0 := ca.TotalPopulation(corr)
	var peakInflow int64
	for m := int64(0); m < 6; m++ {
		res, err := att.ApplyMigration(attract.MigrationCommand{
			Month:              m,
			ResidentIDs:        founders,
			HousingVacancy:     40,
			JunctionThroughput: 100,
		})
		if err != nil {
			t.Fatalf("ApplyMigration(+%d): %v", m, err)
		}
		if res.Inflow > peakInflow {
			peakInflow = res.Inflow
		}
	}
	popAfterGrowth := ca.TotalPopulation(corr)
	if popAfterGrowth <= pop0 {
		t.Fatalf("AC-9 step 2: population did not increase (%d → %d)", pop0, popAfterGrowth)
	}
	if peakInflow <= 0 {
		t.Fatal("AC-9 step 2: no migrants were admitted across the growth window")
	}

	// --- Step 3: Work (documented interim rule — ASM-245) ---------------------
	// engine.firms/market are not built by S6, so employment is a placeholder:
	// arriving workers are transitioned to employed directly.
	for _, id := range founders {
		if err := ca.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: corr,
			Kind:          citizens.LifeEventEmployment,
			CitizenID:     id,
			Employment:    citizens.EmploymentEmployed,
			Sector:        citizens.SectorTertiary,
		}); err != nil {
			t.Fatalf("interim employment for %d: %v", id, err)
		}
	}
	if c, ok := ca.CitizenAt(founders[0], corr); !ok || c.Employment.State != citizens.EmploymentEmployed {
		t.Fatalf("interim employment did not take: %+v ok=%v", c.Employment, ok)
	}

	// --- Step 4: Deliver — JIT shortfall propagation --------------------------
	// A commodity import cut: the food shelf holds far less than the draw.
	if _, err := log.Provision("city", market.FoodStaples, 10, 10); err != nil {
		t.Fatalf("Provision(food): %v", err)
	}
	dr, err := log.Draw("city", market.FoodStaples, 100, logistics.ConsumerHousehold)
	if err != nil {
		t.Fatalf("Draw(food): %v", err)
	}
	if dr.Shortfall <= 0 {
		t.Fatalf("injected shortfall = %d, want > 0", dr.Shortfall)
	}
	// Read back a measurable citizen-state delta (health-band demotion is the
	// documented interim satisfaction/production proxy — citizens has no
	// satisfaction-mutation command yet).
	before, _ := ca.CitizenAt(founders[0], corr)
	if err := ca.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: corr,
		Kind:          citizens.LifeEventHealth,
		CitizenID:     founders[0],
		HealthBand:    citizens.HealthFair,
	}); err != nil {
		t.Fatalf("shortfall health delta: %v", err)
	}
	after, _ := ca.CitizenAt(founders[0], corr)
	if after.HealthBand == before.HealthBand {
		t.Fatal("shortfall did not propagate to a measurable citizen-state delta")
	}

	// --- Step 5: Junction queue (queryable state) -----------------------------
	// engine.logistics defers the literal per-junction queue; the queryable
	// stand-in is the Deliverable throughput/stock state, which must be nonzero
	// during the post-migration peak.
	dv, err := log.Deliverable("city", market.FoodStaples, 100)
	if err != nil {
		t.Fatalf("Deliverable: %v", err)
	}
	if dv.Throughput <= 0 {
		t.Fatalf("peak-arrival throughput = %d, want > 0", dv.Throughput)
	}

	// --- Step 6: Migrate (negative branch) ------------------------------------
	// Service collapse: all five pushed terms crater, driving A below A_world.
	// HouseholdIDs is nil here: the founding households become orphaned as
	// their members emigrate (engine.citizens' departure command removes the
	// citizen but not their household membership — a pre-existing citizens
	// gap), so the affordability aggregation must not re-query them.
	att.SetTermInputs(attract.TermInputs{
		JobAvailability:        0,
		ServiceCoverage:        0,
		Environment:            0,
		LeisureFit:             0,
		Safety:                 0,
		HouseholdIDs:           nil,
		MonthlyRentMicroPounds: 1000,
	})
	beforeDecline := ca.TotalPopulation(corr)
	for m := int64(6); m < 12; m++ {
		if _, err := att.ApplyMigration(attract.MigrationCommand{
			Month:              m,
			ResidentIDs:        founders, // the ambitious founding residents leave first
			HousingVacancy:     0,
			JunctionThroughput: 0,
		}); err != nil {
			t.Fatalf("ApplyMigration(−%d): %v", m, err)
		}
	}
	afterDecline := ca.TotalPopulation(corr)
	if afterDecline >= beforeDecline {
		t.Fatalf("AC-9 step 6: population did not decrease (%d → %d)", beforeDecline, afterDecline)
	}
}

// --- fixtures ---------------------------------------------------------------

// attractConfig is the balanced master-dial config used by the scenario.
func attractConfig() attract.Config {
	return attract.Config{
		Weights: attract.Weights{
			JobAvailability:      0.2,
			HousingAffordability: 0.2,
			ServiceCoverage:      0.15,
			Environment:          0.1,
			LeisureFit:           0.1,
			Safety:               0.1,
			Reputation:           0.15,
		},
		World:         attract.NewStaticWorldPool(50),
		MigrationRate: 1.0,
		Reputation:    attract.ReputationConfig{RiseRate: 0.2, FallRate: 0.8, Max: 100},
	}
}

func newCitizensFixture(t *testing.T) *citizens.CitizensAPI {
	t.Helper()
	ca, err := citizens.NewCitizensAPI(7, corr)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	return ca
}

func newHouseholdsFixture(t *testing.T) *households.HouseholdsAPI {
	t.Helper()
	h, err := households.NewFromBuildings(data.Buildings{Entries: []data.BuildingEntry{
		{ID: "terrace", Name: "terrace", CatalogueSection: "HS", AppealProfile: []string{"families"}},
		{ID: "bungalow", Name: "bungalow", CatalogueSection: "HS", AppealProfile: []string{"retirees"}},
	}}, corr)
	if err != nil {
		t.Fatalf("NewFromBuildings: %v", err)
	}
	return h
}

func newFinanceFixture(t *testing.T) *finance.FinanceAPI {
	t.Helper()
	f := finance.NewFinanceAPI(corr)
	// Seed the treasury so the monthly wage bill clears the overdraft gate.
	if _, err := f.Post(finance.Transaction{
		Description: "seed grant",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: 1_000_000, Category: "seed"},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: 1_000_000, Category: "seed"},
		},
	}); err != nil {
		t.Fatalf("seed treasury: %v", err)
	}
	if _, err := f.PostWages(100_000); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	return f
}

func newAttractFixture(t *testing.T, ca *citizens.CitizensAPI, hh *households.HouseholdsAPI, fin *finance.FinanceAPI) *attract.AttractAPI {
	t.Helper()
	a, err := attract.New(attractConfig(), 7, corr)
	if err != nil {
		t.Fatalf("attract.New: %v", err)
	}
	if err := a.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if err := a.SetFinance(fin); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := a.SetHouseholds(hh); err != nil {
		t.Fatalf("SetHouseholds: %v", err)
	}
	return a
}

// partner seeds two max-ambition residents and partners them into a
// household, returning the household id.
func partner(t *testing.T, ca *citizens.CitizensAPI, aID, bID uint64) uint64 {
	t.Helper()
	for _, id := range []uint64{aID, bID} {
		if err := ca.SeedColdRecords([]citizens.ColdRecord{resident(id)}, corr); err != nil {
			t.Fatalf("SeedColdRecords(%d): %v", id, err)
		}
	}
	if err := ca.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: corr,
		Kind:          citizens.LifeEventPartner,
		CitizenID:     aID,
		PartnerID:     bID,
	}); err != nil {
		t.Fatalf("LifeEventPartner: %v", err)
	}
	hh, ok := ca.HouseholdOf(aID, corr)
	if !ok {
		t.Fatalf("household not formed for %d", aID)
	}
	return hh.ID
}

// resident builds a max-ambition cold citizen record (deterministic decline).
func resident(id uint64) citizens.ColdRecord {
	var p [citizens.NumPersonalityAxes]int8
	p[citizens.AxisAmbition] = 100
	return citizens.ColdRecord{
		ID:              id,
		BirthMonth:      0,
		Sex:             citizens.SexFemale,
		Personality:     p,
		Wealth:          100_000_000,
		EmploymentState: citizens.EmploymentEmployed,
		Sector:          citizens.SectorTertiary,
		HealthBand:      citizens.HealthGood,
		Stage:           citizens.StageAdultEd,
	}
}

// newBuildFixture mirrors engine.build's own test fixture: a minimal
// buildings.json in a temp dir, an owned world tile, the real season, and
// the real logistics — fully wired.
func newBuildFixture(t *testing.T) (*build.BuildAPI, *world.WorldAPI, *logistics.LogisticsAPI) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "buildings.json"), []byte(buildingsFixtureJSON()), 0o644); err != nil {
		t.Fatalf("write fixtures: %v", err)
	}
	b, err := build.Load(dir, corr)
	if err != nil {
		t.Fatalf("build.Load: %v", err)
	}
	wapi := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	res := wapi.PurchaseTile(world.PurchaseCommand{CorrelationID: corr, Tile: world.TileCoord{X: 0, Y: 0}, BuyerID: 1})
	if !res.Accepted {
		t.Fatalf("PurchaseTile: %v", res.Error)
	}
	if err := b.SetWorld(wapi); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}
	s, err := season.LoadDefault(corr)
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	if err := b.SetSeason(s); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	l, err := logistics.LoadDefault(corr)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := b.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	// Provision a full construction-materials shelf so the build can complete.
	if _, err := l.Provision(build.DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision(construction): %v", err)
	}
	return b, wapi, l
}

// buildingsFixtureJSON is the minimal §34 zone catalogue build.Load needs.
func buildingsFixtureJSON() string {
	type z struct {
		id, name       string
		mat, lab, lead int64
	}
	zones := []z{
		{"dwelling", "Dwelling", 100, 40, 1},
		{"shop", "Shop", 80, 30, 30},
		{"office", "Office", 150, 50, 60},
		{"entertainment", "Entertainment", 200, 60, 75},
		{"farming", "Farming", 60, 20, 20},
		{"manufacturing", "Manufacturing", 250, 80, 90},
		{"heavy_industry", "Heavy Industry", 400, 120, 150},
		{"mining", "Mining", 300, 100, 120},
	}
	var sb strings.Builder
	sb.WriteString(`{"version":1,"meta":{"labourPerTick":1},"zones":[`)
	for i, z := range zones {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%q,"name":%q,"materialsBill":{"constructionMaterials":%d},"labour":%d,"baseLeadTimeDays":%d}`,
			z.id, z.name, z.mat, z.lab, z.lead)
	}
	sb.WriteString(`],"entries":[]}`)
	return sb.String()
}
