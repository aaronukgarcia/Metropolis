package attract

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// validWeights returns the shipped ten-lever shape: ten lever weights of
// 0.1 (sum 1), the two modifiers at 0.1 each, reputation 0.15, and the
// seven momentum reinforcers at 0.02.
func validWeights() Weights {
	var w Weights
	for i := range w.Levers {
		w.Levers[i] = 0.1
	}
	for i := range w.Modifiers {
		w.Modifiers[i] = 0.1
	}
	w.Reputation = 0.15
	for i := range w.Reinforcers {
		w.Reinforcers[i] = 0.02
	}
	return w
}

// validConfig returns a balanced, valid Config: the ten-lever weights, a
// static A_world of 50, unit migration rate, and asymmetric reputation
// rates (fallRate > riseRate - the Detroit-trap asymmetry).
func validConfig() Config {
	return Config{
		Weights:       validWeights(),
		World:         NewStaticWorldPool(50),
		MigrationRate: 1.0,
		Reputation:    ReputationConfig{RiseRate: 0.2, FallRate: 0.8, Max: 100},
	}
}

// uniformTI returns TermInputs with the housing gate built (attract
// computes its value), the nine pushed levers and both modifiers built at
// value v, and the given household context. It is the ten-lever
// replacement for the old "five terms all at v" fixtures.
func uniformTI(v float64, ids []uint64, rent int64) TermInputs {
	in := TermInputs{HouseholdIDs: ids, MonthlyRentMicroPounds: rent}
	for i := 0; i < LeverCount; i++ {
		in.Levers[i] = LeverTerm{Value: v, Built: true}
	}
	for i := 0; i < ModifierCount; i++ {
		in.Modifiers[i] = LeverTerm{Value: v, Built: true}
	}
	return in
}

// newAPI builds a wired AttractAPI over a config, with optional residents
// seeded into a CitizensAPI. Residents are also partnered in pairs into
// households (returned as the household-id set) when pairUp is true.
func newAPI(t *testing.T, cfg Config) (*AttractAPI, *citizens.CitizensAPI, *households.HouseholdsAPI, *finance.FinanceAPI) {
	t.Helper()
	a, err := New(cfg, 7, "corr-attract")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ca, err := citizens.NewCitizensAPI(7, "corr-attract")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	h, err := households.NewFromBuildings(testCatalogue(), "corr-attract")
	if err != nil {
		t.Fatalf("NewFromBuildings: %v", err)
	}
	if err := h.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens(households): %v", err)
	}
	f := finance.NewFinanceAPI("corr-attract")
	if err := a.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens(attract): %v", err)
	}
	if err := a.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := a.SetHouseholds(h); err != nil {
		t.Fatalf("SetHouseholds: %v", err)
	}
	return a, ca, h, f
}

// testCatalogue is a small HS fixture (the same shapes engine.households'
// own tests use) so the HousingAffordability term has real typologies to
// aggregate over.
func testCatalogue() data.Buildings {
	return data.Buildings{Entries: []data.BuildingEntry{
		{ID: "terrace", Name: "terrace", CatalogueSection: "HS", AppealProfile: []string{"families"}},
		{ID: "bungalow", Name: "bungalow", CatalogueSection: "HS", AppealProfile: []string{"retirees"}},
	}}
}

// mkResident builds a valid cold citizen record with a controllable ambition
// personality axis (AC-6's per-resident emigration input).
func mkResident(id uint64, ambition int8) citizens.ColdRecord {
	var p [citizens.NumPersonalityAxes]int8
	p[citizens.AxisAmbition] = ambition
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

// partnerCouple seeds two residents and partners them into a household,
// returning the household id (engine.citizens' own formation).
func partnerCouple(t *testing.T, ca *citizens.CitizensAPI, a, b citizens.ColdRecord) uint64 {
	t.Helper()
	if err := ca.SeedColdRecords([]citizens.ColdRecord{a, b}, "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := ca.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: "corr-attract",
		Kind:          citizens.LifeEventPartner,
		CitizenID:     a.ID,
		PartnerID:     b.ID,
	}); err != nil {
		t.Fatalf("LifeEventPartner: %v", err)
	}
	hh, ok := ca.HouseholdOf(a.ID, "corr-attract")
	if !ok {
		t.Fatalf("household not formed for citizen %d", a.ID)
	}
	return hh.ID
}

// seedTreasury credits the city treasury from the external world so a
// subsequent PostWages debit clears the overdraft gate (mirrors finance's
// own seedTreasury test helper).
func seedTreasury(t *testing.T, f *finance.FinanceAPI, amount int64) {
	t.Helper()
	if _, err := f.Post(finance.Transaction{
		Description: "test seed grant",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(amount), Category: "seed"},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(amount), Category: "seed"},
		},
	}); err != nil {
		t.Fatalf("seedTreasury: %v", err)
	}
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

// weightsBlockJSON is the shipped "weights" block shape (ten levers at 0.1,
// two modifiers at 0.1, reputation 0.15, seven reinforcers at 0.02), as a
// JSON value for embedding in config documents.
const weightsBlockJSON = `{
	"levers": [
		{"id":"housing","weight":0.1},{"id":"jobs","weight":0.1},{"id":"services","weight":0.1},
		{"id":"healthcare","weight":0.1},{"id":"policy","weight":0.1},{"id":"shops","weight":0.1},
		{"id":"parks","weight":0.1},{"id":"transport","weight":0.1},{"id":"congestion","weight":0.1},
		{"id":"tourism","weight":0.1}],
	"modifiers": [{"id":"safety","weight":0.1},{"id":"environment","weight":0.1}],
	"reputation": 0.15,
	"reinforcers": [
		{"id":"sports","weight":0.02},{"id":"welfare","weight":0.02},{"id":"education","weight":0.02},
		{"id":"prospects","weight":0.02},{"id":"wellpaidjobs","weight":0.02},{"id":"hospitals","weight":0.02},
		{"id":"parks","weight":0.02}]
}`
